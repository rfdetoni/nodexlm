package local

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBenchmarkStartupHasNoInternalDeadline(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &Managed{UnboundedStartup: true}
	startup, stop := m.startupContext(parent, time.Nanosecond)
	defer stop()
	if _, exists := startup.Deadline(); exists {
		t.Fatal("benchmark introduced a startup deadline")
	}
	cancel()
	<-startup.Done()
	if !errors.Is(startup.Err(), context.Canceled) {
		t.Fatalf("expected parent cancellation, got %v", startup.Err())
	}
}

func TestNormalStartupRetainsDeadline(t *testing.T) {
	m := &Managed{}
	startup, stop := m.startupContext(context.Background(), time.Second)
	defer stop()
	if _, exists := startup.Deadline(); !exists {
		t.Fatal("normal startup lost its deadline")
	}
}

func TestBenchmarkStartupRespectsCallerDeadline(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	m := &Managed{UnboundedStartup: true}
	startup, stop := m.startupContext(parent, time.Second)
	defer stop()
	want, _ := parent.Deadline()
	got, ok := startup.Deadline()
	if !ok || !got.Equal(want) {
		t.Fatalf("caller deadline was not preserved: %v %v", got, want)
	}
}
