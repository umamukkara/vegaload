package engine

import (
	"context"
	"errors"
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
	defer cancel()
	calls := 0
	iter := func(ctx context.Context) error {
		calls++
		if calls == 1 {
			return errors.New("boom")
		}
		cancel() // stop after the successful second iteration
		return nil
	}

	runVU(ctx, 0, iter, rec)

	if rec.Total() != 2 {
		t.Fatalf("Total() = %d, want 2", rec.Total())
	}
	if rec.Failed() != 1 {
		t.Fatalf("Failed() = %d, want 1 (only the boom iteration)", rec.Failed())
	}
}

func TestRunVU_DoesNotRecordRunEndCancellation(t *testing.T) {
	rec := NewSummary()
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	iter := func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}

	done := make(chan struct{})
	go func() {
		runVU(ctx, 0, iter, rec)
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("iteration did not start")
	}
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runVU did not stop after context cancellation")
	}

	if rec.Total() != 0 {
		t.Fatalf("Total() = %d, want 0 (run-end cancellation must not be recorded)", rec.Total())
	}
	if rec.Failed() != 0 {
		t.Fatalf("Failed() = %d, want 0", rec.Failed())
	}
}

func TestFixedVUs_DoesNotCountRunEndCancellationsAsFailures(t *testing.T) {
	rec := NewSummary()
	// Each VU blocks until the run deadline cancels ctx, then returns
	// that cancellation — the failure mode this bug used to inflate.
	iter := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}

	f := FixedVUs{VUs: 5, Dur: 50 * time.Millisecond}
	if err := f.Run(context.Background(), iter, rec); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if rec.Failed() != 0 {
		t.Fatalf("Failed() = %d, want 0 (in-flight VUs cut off at run end)", rec.Failed())
	}
	if rec.Total() != 0 {
		t.Fatalf("Total() = %d, want 0", rec.Total())
	}
}

// laggingCtx models a context whose deadline has passed but whose timer
// has not fired yet: Err is nil and Done never closes.
type laggingCtx struct {
	context.Context
	dl time.Time
}

func (c laggingCtx) Deadline() (time.Time, bool) { return c.dl, true }
func (c laggingCtx) Done() <-chan struct{}       { return nil }
func (c laggingCtx) Err() error                  { return nil }

func TestRecordIteration_DropsErrorsAfterDeadlineEvenIfCtxNotCancelledYet(t *testing.T) {
	rec := NewSummary()
	past := laggingCtx{Context: context.Background(), dl: time.Now().Add(-time.Millisecond)}
	recordIteration(past, rec, IterationResult{Err: errors.New("dial: i/o timeout")})
	if rec.Total() != 0 {
		t.Fatalf("an error after the deadline was recorded: total = %d", rec.Total())
	}

	// A success is still recorded, and an error before the deadline is a
	// real failure.
	recordIteration(past, rec, IterationResult{})
	future := laggingCtx{Context: context.Background(), dl: time.Now().Add(time.Hour)}
	recordIteration(future, rec, IterationResult{Err: errors.New("real failure")})
	if rec.Total() != 2 || rec.Failed() != 1 {
		t.Fatalf("total = %d, failed = %d, want 2 and 1", rec.Total(), rec.Failed())
	}
}

// A fast-failing driver must not pile up failures while the run's timer
// is late: runVU stops as soon as the deadline passes.
func TestRunVU_StopsAtDeadlineWithoutWaitingForCancel(t *testing.T) {
	rec := NewSummary()
	dl := time.Now().Add(30 * time.Millisecond)
	ctx := laggingCtx{Context: context.Background(), dl: dl}
	iter := func(context.Context) error {
		if !time.Now().Before(dl) {
			return errors.New("dial: i/o timeout") // what a driver reports after the deadline
		}
		return nil
	}

	done := make(chan struct{})
	go func() { runVU(ctx, 0, iter, rec); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runVU kept running after the deadline")
	}
	if rec.Failed() != 0 {
		t.Fatalf("failed = %d, want 0: calls cut off by the deadline are not failures", rec.Failed())
	}
}
