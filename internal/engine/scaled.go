package engine

import (
	"context"
	"sync"
	"time"
)

// tickInterval is how often Ramp and Step re-evaluate the target VU
// count and scale the running pool up or down to match.
const tickInterval = 50 * time.Millisecond

// stageTarget walks stages to find the one covering elapsed, and
// returns either the interpolated (interp=true) or held (interp=false)
// VU count within that stage. The VU count at the start of the first
// stage is assumed to be 0; elapsed at or past the end of the last
// stage clamps to that stage's target.
func stageTarget(stages []Stage, elapsed time.Duration, interp bool) int {
	var t time.Duration
	prev := 0
	for _, s := range stages {
		end := t + s.Duration
		if elapsed < end || s.Duration == 0 {
			if !interp || s.Duration == 0 {
				return s.Target
			}
			frac := float64(elapsed-t) / float64(s.Duration)
			return prev + int(frac*float64(s.Target-prev))
		}
		t = end
		prev = s.Target
	}
	if len(stages) == 0 {
		return 0
	}
	return stages[len(stages)-1].Target
}

// runScaled runs a pool of VUs whose size is re-evaluated every
// tickInterval by calling targetAt(elapsed), scaling the live pool up
// or down to match. It returns once dur has elapsed or ctx is
// cancelled, after every live VU has stopped.
//
// Ramp and Step share this scheduler; they differ only in how
// targetAt computes the number at a given elapsed time (interpolated
// vs held).
func runScaled(ctx context.Context, dur time.Duration, targetAt func(time.Duration) int, iter IterationFunc, rec Recorder) error {
	ctx, cancel := context.WithTimeout(ctx, dur)
	defer cancel()

	start := time.Now()
	var mu sync.Mutex
	var wg sync.WaitGroup
	cancels := make([]context.CancelFunc, 0)

	scaleTo := func(target int) {
		mu.Lock()
		defer mu.Unlock()
		current := len(cancels)
		switch {
		case target > current:
			for i := current; i < target; i++ {
				vuCtx, vuCancel := context.WithCancel(ctx)
				cancels = append(cancels, vuCancel)
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					runVU(vuCtx, id, iter, rec)
				}(i)
			}
		case target < current:
			for i := current - 1; i >= target; i-- {
				cancels[i]()
				cancels = cancels[:i]
			}
		}
	}

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	scaleTo(targetAt(0))
	for {
		select {
		case <-ctx.Done():
			mu.Lock()
			for _, c := range cancels {
				c()
			}
			mu.Unlock()
			wg.Wait()
			return nil
		case <-ticker.C:
			scaleTo(targetAt(time.Since(start)))
		}
	}
}
