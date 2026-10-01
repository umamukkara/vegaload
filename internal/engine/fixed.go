package engine

import (
	"context"
	"sync"
	"time"
)

// FixedVUs runs a constant number of virtual users for a fixed
// duration. Each VU loops iterations back-to-back for the whole
// duration. This is the simplest load shape: no ramping, no arrival
// rate, just "N users, hammering, for T time."
type FixedVUs struct {
	VUs int
	Dur time.Duration
}

// Name implements Executor.
func (f FixedVUs) Name() string { return "fixed-vus" }

// Duration implements Executor.
func (f FixedVUs) Duration() time.Duration { return f.Dur }

// Run implements Executor.
func (f FixedVUs) Run(ctx context.Context, iter IterationFunc, rec Recorder) error {
	if f.VUs <= 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, f.Dur)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(f.VUs)
	for i := 0; i < f.VUs; i++ {
		go func(id int) {
			defer wg.Done()
			runVU(ctx, id, iter, rec)
		}(i)
	}
	wg.Wait()
	return nil
}
