package engine

import (
	"context"
	"time"
)

// Ramp runs a variable number of virtual users, linearly interpolating
// the target VU count between each Stage's start and end over that
// stage's duration — the "ramping-vus" shape k6 and Gatling both call a
// ramp. The VU count starts at 0 before the first stage.
type Ramp struct {
	Stages []Stage
}

// Name implements Executor.
func (r Ramp) Name() string { return "ramp" }

// Duration implements Executor.
func (r Ramp) Duration() time.Duration { return totalStageDuration(r.Stages) }

// targetAt returns the interpolated target VU count at elapsed time t.
func (r Ramp) targetAt(elapsed time.Duration) int {
	return stageTarget(r.Stages, elapsed, true)
}

// Run implements Executor.
func (r Ramp) Run(ctx context.Context, iter IterationFunc, rec Recorder) error {
	return runScaled(ctx, r.Duration(), r.targetAt, iter, rec)
}
