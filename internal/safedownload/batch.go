package safedownload

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

type workerBudgetKey struct{}

func workersFor(ctx context.Context) int {
	if n, ok := ctx.Value(workerBudgetKey{}).(int); ok {
		return n
	}
	return downloadWorkers()
}

// BatchFile sizes must be established against an immutable remote revision.
type BatchFile struct {
	Source string
	Target string
	Size   int64
}

// FetchBatch transfers shards concurrently under one global connection
// budget. All outputs remain private staging files; publication belongs to
// the catalog. A failed sibling cancels outstanding work and retains stages.
func FetchBatch(ctx context.Context, client *http.Client, files []BatchFile, limit int64, progress func(int64, int64)) error {
	if len(files) == 0 || len(files) > 128 || limit <= 0 {
		return errors.New("invalid download batch")
	}
	var total int64
	seen := make(map[string]bool, len(files))
	dir := filepath.Dir(files[0].Target)
	for _, f := range files {
		if f.Size < 4 || f.Size > limit-total || filepath.Dir(f.Target) != dir || seen[f.Target] {
			return errors.New("invalid batch size or staging path")
		}
		seen[f.Target] = true
		total += f.Size
	}
	select {
	case transfers <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-transfers }()
	free, err := FreeBytes(dir)
	if err != nil {
		return err
	}
	// Conservative admission: do not credit sparse logical lengths as actual
	// allocated disk bytes. Each fetch also checks the available space.
	if uint64(total) > free {
		return errors.New("insufficient free disk space for model set")
	}
	ctx, cancel := context.WithCancel(ensureBandwidth(ctx))
	defer cancel()
	if client == nil {
		client = Client()
	}
	workers := downloadWorkers()
	parallel := min(len(files), max(1, min(4, workers/2)))
	ctx = context.WithValue(ctx, workerBudgetKey{}, max(1, workers/parallel))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failure error
	received := make([]int64, len(files))
	next := 0
	for range parallel {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				mu.Lock()
				i := next
				next++
				mu.Unlock()
				if i >= len(files) {
					return
				}
				f := files[i]
				n, err := fetch(ctx, client, f.Source, f.Target, f.Size, func(n, _ int64) {
					mu.Lock()
					defer mu.Unlock()
					received[i] = min(n, f.Size)
					if progress != nil {
						var sum int64
						for _, n := range received {
							sum += n
						}
						progress(sum, total)
					}
				}, true)
				if err == nil && n != f.Size {
					err = errors.New("shard size changed")
					_ = os.Remove(f.Target)
					_ = os.Remove(f.Target + ".json")
				}
				if err != nil {
					mu.Lock()
					if failure == nil {
						failure = fmt.Errorf("shard %d: %w", i+1, err)
						cancel()
					}
					mu.Unlock()
					return
				}
			}
		}()
	}
	wg.Wait()
	if failure != nil {
		return failure
	}
	return ctx.Err()
}
