package safedownload

import (
	"context"
	"errors"
	"testing"
)

func TestSharedBandwidthCancellation(t *testing.T) {
	t.Setenv("NODEXLM_DOWNLOAD_LIMIT_BPS", "1024")
	ctx, cancel := context.WithCancel(ensureBandwidth(context.Background()))
	defer cancel()
	if err := paceDownload(ctx, 1024); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := paceDownload(ctx, 1024); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled transfer must not wait for budget", err)
	}
}

func TestDynamicBandwidthUsesLowerCap(t *testing.T) {
	t.Setenv("NODEXLM_DOWNLOAD_LIMIT_BPS", "1048576")
	ctx := WithBandwidthPolicy(context.Background(), func() int64 { return 512 << 10 })
	if err := paceDownload(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if got := ctx.Value(bandwidthKey{}).(*bandwidthBudget).rate; got != 512<<10 {
		t.Fatal("wrong shared rate", got)
	}
	if err := paceDownload(ctx, 1); err != nil {
		t.Fatal(err)
	}
}
