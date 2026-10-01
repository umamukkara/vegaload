package main

import (
	"context"
	"sync"
)

// iterCloser is what both js.VU and python.VU satisfy: one iteration,
// plus a way to release whatever resource (a goja runtime, a subprocess)
// backs it.
type iterCloser interface {
	Iteration(ctx context.Context) error
	Close() error
}

// vuPool exists because of a mismatch between how the engine calls
// iterations and what the scripting runtimes need. engine.Executor.Run
// takes a single IterationFunc and calls it concurrently from every VU
// goroutine it spawns (see internal/engine/fixed.go, scaled.go, and
// arrival.go) — fine for the protocol drivers, whose Do methods are
// built to tolerate concurrent calls, but not for a js.VU (one Goja
// runtime, not safe for concurrent use) or a python.VU (one subprocess
// talking a single-request-at-a-time protocol over one pipe).
//
// vuPool bridges the two: iteration borrows an idle VU (or creates one,
// up to its capacity), runs it, and returns it. Close reclaims every VU
// it ever created, which matters most for python.VU — an unreclaimed
// one is a leaked subprocess.
//
// This also means a script's top-level state (a counter declared with
// let/const, module-level Python globals) is per *borrowed instance*
// for the life of the pool, not guaranteed to stick to the same virtual
// user iteration after iteration — iteration doesn't know which VU ID
// is calling it, only engine.IterationFunc's signature, and that
// signature carries no VU ID today. A future revision of
// engine.IterationFunc to pass one would let this affinity become
// exact; until then this is Phase 0's documented limitation, not a
// silent bug.
type vuPool struct {
	ch    chan iterCloser
	newFn func() (iterCloser, error)

	mu  sync.Mutex
	all []iterCloser
}

// newVUPool returns a pool that creates VUs on demand via newFn, up to
// capacity concurrently-borrowed instances (a VU created beyond that,
// to serve a momentary burst, is closed on return rather than kept).
// capacity is clamped to at least 1.
func newVUPool(capacity int, newFn func() (iterCloser, error)) *vuPool {
	if capacity <= 0 {
		capacity = 1
	}
	return &vuPool{
		ch:    make(chan iterCloser, capacity),
		newFn: newFn,
	}
}

// iteration implements engine.IterationFunc.
func (p *vuPool) iteration(ctx context.Context) error {
	vu, isNew, err := p.borrow()
	if err != nil {
		return err
	}
	if isNew {
		p.mu.Lock()
		p.all = append(p.all, vu)
		p.mu.Unlock()
	}
	defer p.release(vu)

	return vu.Iteration(ctx)
}

// borrowAndRelease creates (or reuses) one VU and immediately returns it
// to the pool. Callers use it right after constructing a pool to
// validate that newFn actually works — a broken scenario file fails
// here, before the executor starts — without consuming the capacity
// that validation borrow would otherwise hold onto.
func (p *vuPool) borrowAndRelease() (iterCloser, error) {
	vu, isNew, err := p.borrow()
	if err != nil {
		return nil, err
	}
	if isNew {
		p.mu.Lock()
		p.all = append(p.all, vu)
		p.mu.Unlock()
	}
	p.release(vu)
	return vu, nil
}

func (p *vuPool) borrow() (vu iterCloser, isNew bool, err error) {
	select {
	case vu := <-p.ch:
		return vu, false, nil
	default:
	}
	vu, err = p.newFn()
	if err != nil {
		return nil, false, err
	}
	return vu, true, nil
}

func (p *vuPool) release(vu iterCloser) {
	select {
	case p.ch <- vu:
	default:
		// Pool is at capacity: this VU was created to serve a momentary
		// burst beyond it, so it is closed rather than tracked forever.
		_ = vu.Close()
		p.mu.Lock()
		for i, v := range p.all {
			if v == vu {
				p.all = append(p.all[:i], p.all[i+1:]...)
				break
			}
		}
		p.mu.Unlock()
	}
}

// Close releases every VU this pool ever created, whether it is
// currently idle in the pool or not.
func (p *vuPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	var firstErr error
	for _, vu := range p.all {
		if err := vu.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	p.all = nil
	return firstErr
}
