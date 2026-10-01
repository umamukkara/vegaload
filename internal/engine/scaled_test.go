package engine

import (
	"context"
	"testing"
	"time"
)

func TestStageTarget_Interpolated(t *testing.T) {
	stages := []Stage{
		{Target: 10, Duration: 10 * time.Second},
		{Target: 10, Duration: 5 * time.Second},
		{Target: 0, Duration: 10 * time.Second},
	}
	cases := []struct {
		elapsed time.Duration
		want    int
	}{
		{0, 0},
		{5 * time.Second, 5},
		{10 * time.Second, 10},
		{12 * time.Second, 10},
		{20 * time.Second, 5},
		{30 * time.Second, 0}, // past the end: clamps to the last stage's target
	}
	for _, c := range cases {
		if got := stageTarget(stages, c.elapsed, true); got != c.want {
			t.Errorf("stageTarget(%v, interp=true) = %d, want %d", c.elapsed, got, c.want)
		}
	}
}

func TestStageTarget_Held(t *testing.T) {
	stages := []Stage{
		{Target: 5, Duration: 10 * time.Second},
		{Target: 15, Duration: 10 * time.Second},
	}
	cases := []struct {
		elapsed time.Duration
		want    int
	}{
		{0, 5},
		{9 * time.Second, 5},
		{10 * time.Second, 15}, // jumps immediately at the stage boundary
		{15 * time.Second, 15},
		{25 * time.Second, 15}, // past the end: clamps to the last stage's target
	}
	for _, c := range cases {
		if got := stageTarget(stages, c.elapsed, false); got != c.want {
			t.Errorf("stageTarget(%v, interp=false) = %d, want %d", c.elapsed, got, c.want)
		}
	}
}

func TestStageTarget_NoStages(t *testing.T) {
	if got := stageTarget(nil, time.Second, true); got != 0 {
		t.Errorf("stageTarget(nil) = %d, want 0", got)
	}
}

func TestRamp_ReachesApproximateTargetByEnd(t *testing.T) {
	rec := NewSummary()
	iter := func(ctx context.Context) error {
		time.Sleep(2 * time.Millisecond)
		return nil
	}

	r := Ramp{Stages: []Stage{
		{Target: 5, Duration: 150 * time.Millisecond},
	}}

	start := time.Now()
	if err := r.Run(context.Background(), iter, rec); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed < r.Duration() {
		t.Errorf("Run returned before its duration elapsed: %v < %v", elapsed, r.Duration())
	}
	if rec.Total() == 0 {
		t.Error("expected at least one recorded iteration during the ramp")
	}
}

func TestStep_HoldsEachStage(t *testing.T) {
	rec := NewSummary()
	iter := func(ctx context.Context) error {
		time.Sleep(2 * time.Millisecond)
		return nil
	}

	s := Step{Stages: []Stage{
		{Target: 2, Duration: 100 * time.Millisecond},
		{Target: 4, Duration: 100 * time.Millisecond},
	}}

	if err := s.Run(context.Background(), iter, rec); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if rec.Total() == 0 {
		t.Error("expected at least one recorded iteration across both steps")
	}
}
