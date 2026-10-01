package engine

import (
	"context"
	"time"
)

// Step runs a variable number of virtual users, holding each Stage's
// target VU count constant for that stage's duration, then jumping
// straight to the next stage's target — no interpolation in between,
// unlike Ramp.
type Step struct {
	Stages []Stage
}

// Name implements Executor.
func (s Step) Name() string { return "step" }

// Duration implements Executor.
func (s Step) Duration() time.Duration { return totalStageDuration(s.Stages) }

// targetAt returns the held target VU count at elapsed time t.
func (s Step) targetAt(elapsed time.Duration) int {
	return stageTarget(s.Stages, elapsed, false)
}

// Run implements Executor.
func (s Step) Run(ctx context.Context, iter IterationFunc, rec Recorder) error {
	return runScaled(ctx, s.Duration(), s.targetAt, iter, rec)
}
