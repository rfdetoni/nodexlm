package safedownload

import (
	"context"
	"os"
	"strconv"
	"sync"
	"time"
)

type bandwidthKey struct{}

type bandwidthBudget struct {
	mu      sync.Mutex
	policy  func() int64
	maximum int64
	rate    int64
	sampled time.Time
	next    time.Time
}

// WithBandwidthPolicy sets an optional dynamic cap in bytes per second.
// Zero means unlimited. The fixed environment cap, if set, is still honored.
func WithBandwidthPolicy(ctx context.Context, policy func() int64) context.Context {
	return context.WithValue(ctx, bandwidthKey{}, &bandwidthBudget{policy: policy, maximum: bandwidthLimit()})
}

func bandwidthLimit() int64 {
	n, _ := strconv.ParseInt(os.Getenv("NODEXLM_DOWNLOAD_LIMIT_BPS"), 10, 64)
	return max(0, n)
}

func ensureBandwidth(ctx context.Context) context.Context {
	if _, ok := ctx.Value(bandwidthKey{}).(*bandwidthBudget); ok {
		return ctx
	}
	return WithBandwidthPolicy(ctx, nil)
}

func paceDownload(ctx context.Context, bytes int) error {
	budget, _ := ctx.Value(bandwidthKey{}).(*bandwidthBudget)
	if budget == nil || bytes <= 0 {
		return ctx.Err()
	}
	budget.mu.Lock()
	now := time.Now()
	if now.Sub(budget.sampled) >= time.Second {
		budget.rate = budget.maximum
		if budget.policy != nil {
			if dynamic := budget.policy(); dynamic > 0 && (budget.rate == 0 || dynamic < budget.rate) {
				budget.rate = dynamic
			}
		}
		budget.sampled = now
	}
	if budget.rate <= 0 {
		budget.next = now
		budget.mu.Unlock()
		return ctx.Err()
	}
	start := maxTime(now, budget.next)
	budget.next = start.Add(time.Duration(bytes) * time.Second / time.Duration(budget.rate))
	budget.mu.Unlock()
	delay := time.Until(start)
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
