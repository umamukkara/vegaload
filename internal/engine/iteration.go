// Package engine implements VegaLoad's core execution engine: the virtual
// user (VU) scheduler and the load-shape executors that decide how many
// VUs run, and when, over the life of a scenario.
//
// This package knows nothing about HTTP, gRPC, WebSocket, JavaScript, or
// Harness RT. It schedules calls to an IterationFunc supplied by the
// caller — the protocol and scripting layers (built later in Phase 0)
// are what give that function its real behaviour. See AGENTS.md at the
// repository root for the module boundary this package is expected to
// hold.
package engine

import (
	"context"
	"sync/atomic"
	"time"
)

// IterationFunc runs one iteration of a scenario. It returns a non-nil
// error if the iteration failed — a connection error, a timeout, a
// threshold the scenario itself chose to enforce, and so on. A VU calls
// this repeatedly for as long as its executor keeps it alive.
//
// Implementations should respect ctx cancellation promptly: an executor
// cancels ctx when the load shape's duration elapses or the run is
// stopped, and a slow-to-return IterationFunc delays shutdown.
type IterationFunc func(ctx context.Context) error

// IterationResult is what the engine records after every iteration.
// VUID identifies which VU ran the iteration; it has no meaning beyond
// that, except for ConstantArrivalRate, where -1 marks an arrival that
// was dropped rather than run (see that executor's doc comment).
type IterationResult struct {
	VUID     int
	Start    time.Time
	Duration time.Duration
	Err      error
}

// Recorder receives one IterationResult per completed iteration, from
// any number of VUs concurrently. Implementations must be safe for
// concurrent use.
//
// This package ships only Summary, a minimal Recorder for tests and
// simple use. The real HTML/JSON report (Phase 1, internal/report) will
// implement this same interface with full percentile tracking — engine
// does not need to know that package exists.
type Recorder interface {
	Record(IterationResult)
}

// Summary is a minimal Recorder that keeps running counts: total
// iterations, failed iterations, and mean duration. It exists so the
// engine and its tests have something to record into in Phase 0,
// before internal/report exists. It is not a substitute for that
// package's real percentile tracking.
type Summary struct {
	total  atomic.Int64
	failed atomic.Int64
	nsSum  atomic.Int64
}

// NewSummary returns an empty Summary, ready to use.
func NewSummary() *Summary {
	return &Summary{}
}

// Record implements Recorder.
func (s *Summary) Record(r IterationResult) {
	s.total.Add(1)
	s.nsSum.Add(r.Duration.Nanoseconds())
	if r.Err != nil {
		s.failed.Add(1)
	}
}

// Total returns the number of iterations recorded so far.
func (s *Summary) Total() int64 { return s.total.Load() }

// Failed returns the number of recorded iterations that had a non-nil
// error, including arrivals ConstantArrivalRate dropped.
func (s *Summary) Failed() int64 { return s.failed.Load() }

// MeanDuration returns the mean iteration duration across every
// recorded iteration, including failed ones. It returns 0 if nothing
// has been recorded yet.
func (s *Summary) MeanDuration() time.Duration {
	total := s.total.Load()
	if total == 0 {
		return 0
	}
	return time.Duration(s.nsSum.Load() / total)
}
