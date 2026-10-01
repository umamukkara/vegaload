package engine

import (
	"context"
	"testing"
	"time"
)

func TestFixedVUs_RunsForItsFullDuration(t *testing.T) {
	rec := NewSummary()
	iter := func(ctx context.Context) error {
		time.Sleep(5 * time.Millisecond)
		return nil
	}

	f := FixedVUs{VUs: 4, Dur: 100 * time.Millisecond}
	start := time.Now()
	if err := f.Run(context.Background(), iter, rec); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed < f.Dur {
		t.Errorf("Run returned before its duration elapsed: %v < %v", elapsed, f.Dur)
	}
	if rec.Total() == 0 {
		t.Error("expected at least one recorded iteration")
	}
}

func TestFixedVUs_ZeroVUsIsANoop(t *testing.T) {
	rec := NewSummary()
	iter := func(ctx context.Context) error {
		t.Error("iter should never be called with 0 VUs")
		return nil
	}

	f := FixedVUs{VUs: 0, Dur: 50 * time.Millisecond}
	if err := f.Run(context.Background(), iter, rec); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if rec.Total() != 0 {
		t.Errorf("Total() = %d, want 0", rec.Total())
	}
}

func TestFixedVUs_NameAndDuration(t *testing.T) {
	f := FixedVUs{VUs: 10, Dur: 2 * time.Minute}
	if f.Name() != "fixed-vus" {
		t.Errorf("Name() = %q, want %q", f.Name(), "fixed-vus")
	}
	if f.Duration() != 2*time.Minute {
		t.Errorf("Duration() = %v, want %v", f.Duration(), 2*time.Minute)
	}
}
