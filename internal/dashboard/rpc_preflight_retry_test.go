package dashboard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"
)

func TestRPCPreflightRetriesFreshConnectionAfterEOF(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	calls, retries := 0, 0
	count, err := retryRPCPreflight(ctx, func() (uint32, error) {
		calls++
		if calls == 1 {
			return 0, fmt.Errorf("ALLOC_BUFFER: %w", io.EOF)
		}
		return 2, nil
	}, func(int, error) { retries++ })
	if err != nil || count != 2 || calls != 2 || retries != 1 {
		t.Fatalf("unexpected retry: count=%d calls=%d retries=%d err=%v", count, calls, retries, err)
	}
}

func TestRPCPreflightDoesNotRetryCorruption(t *testing.T) {
	calls := 0
	expected := errors.New("GET_TENSOR readback mismatch")
	_, err := retryRPCPreflight(context.Background(), func() (uint32, error) {
		calls++
		return 0, expected
	}, func(int, error) { t.Fatal("corruption must not be retried") })
	if !errors.Is(err, expected) || calls != 1 {
		t.Fatalf("unexpected retry: %d %v", calls, err)
	}
}

func TestRPCPreflightCancelsRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := retryRPCPreflight(ctx, func() (uint32, error) {
		return 0, io.EOF
	}, func(int, error) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
}
