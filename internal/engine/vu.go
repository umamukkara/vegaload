package engine

import (
	"context"
	"time"
)

// runVU runs iterations back-to-back on a single virtual user until ctx
// is cancelled. Each iteration's result is sent to rec. vuID identifies
// this VU in recorded results; it has no meaning beyond that.
//
// runVU does not itself enforce a timeout on iter — that is the
// caller's job, typically by deriving ctx from context.WithTimeout at
// the executor level. A VU that never returns from iter will never
// stop; protocol implementations are expected to respect ctx.
func runVU(ctx context.Context, vuID int, iter IterationFunc, rec Recorder) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		start := time.Now()
		err := iter(ctx)
		rec.Record(IterationResult{
			VUID:     vuID,
			Start:    start,
			Duration: time.Since(start),
			Err:      err,
		})
	}
}
