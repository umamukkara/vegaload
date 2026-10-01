package engine

import (
	"context"
	"time"
)

// Executor runs a scenario's iterations according to one load shape
// (fixed-VU, ramp, step, or constant-arrival-rate) and reports every
// iteration to rec. Run blocks until the shape's duration elapses or ctx
// is cancelled, whichever comes first.
//
// The four executors in this package are the only four load shapes
// FR-CLI-04 requires for Phase 0. Adding a fifth later means adding a
// type that implements this interface — it should never require
// changing the ones that already exist.
type Executor interface {
	// Name identifies the load shape, e.g. "fixed-vus", "ramp".
	Name() string
	// Duration is the total wall-clock time this executor will run for.
	Duration() time.Duration
	// Run executes the shape, calling iter for every iteration across
	// however many VUs the shape decides to run, and sending every
	// result to rec.
	Run(ctx context.Context, iter IterationFunc, rec Recorder) error
}

// Stage is one segment of a Ramp or Step load shape: hold, or move
// toward, Target VUs over Duration.
type Stage struct {
	Target   int
	Duration time.Duration
}

// totalStageDuration sums the Duration of every stage.
func totalStageDuration(stages []Stage) time.Duration {
	var total time.Duration
	for _, s := range stages {
		total += s.Duration
	}
	return total
}
