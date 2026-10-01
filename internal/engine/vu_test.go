package engine

import (
	"context"
	"testing"
	"time"
)

func TestRunVU_StopsOnContextCancel(t *testing.T) {
	rec := NewSummary()
	ctx, cancel := context.WithCancel(context.Background())
	iter := func(ctx context.Context) error { return nil }

	done := make(chan struct{})
	go func() {
		runVU(ctx, 0, iter, rec)
		close(done)
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runVU did not stop after context cancellation")
	}

	if rec.Total() == 0 {
		t.Error("expected at least one iteration to have been recorded before cancellation")
	}
}

func TestRunVU_RecordsIterationErrors(t *testing.T) {
	rec := NewSummary()
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	iter := func(ctx context.Context) error {
		calls++
		if calls == 1 {
			cancel() // stop after exactly one iteration
			return context.Canceled
		}
		return nil
	}

	runVU(ctx, 0, iter, rec)

	if rec.Total() != 1 {
		t.Fatalf("Total() = %d, want 1", rec.Total())
	}
	if rec.Failed() != 1 {
		t.Fatalf("Failed() = %d, want 1 (the recorded iteration returned an error)", rec.Failed())
	}
}
