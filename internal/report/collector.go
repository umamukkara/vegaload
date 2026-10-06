// Package report implements FR-RPT-01 through 04 and FR-CLI-05: turning
// a run's recorded iterations into the structured data a self-contained
// HTML report, a JSON summary, and (eventually) the MCP get_results
// tool all read from. It implements engine.Recorder, exactly the seam
// internal/engine's own doc comment on Recorder sets aside for it, and
// knows nothing about protocols, scripting, or the CLI.
package report

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vegaload/vegaload/internal/engine"
)

// sample is the minimal per-iteration record Collector keeps. Keeping
// only what percentile and time-bucket computation need (not the full
// engine.IterationResult) keeps memory bounded even at NFR-03's
// 1,000,000-request scale: roughly 32 bytes/sample, ~32MB at that size.
type sample struct {
	offset time.Duration // time since the run started
	dur    time.Duration
	failed bool
}

// Collector implements engine.Recorder, accumulating every iteration of
// a run so Finish can compute the percentiles and time-bucketed series a
// Result needs. It is safe for concurrent use — Run calls Record from
// every VU goroutine at once.
type Collector struct {
	mu      sync.Mutex
	start   time.Time
	samples []sample

	// liveTotal/liveFailed mirror the counts in samples but are safe to
	// read cheaply (no lock, no copy) while a run is still in progress —
	// cmd/vegaload's -output jsonl mode polls these for periodic
	// "progress" events (see run.go) without contending with Record on
	// every one of potentially a million iterations.
	liveTotal  atomic.Int64
	liveFailed atomic.Int64

	// checks holds the per-name totals of check() calls (FR-CLI-12),
	// guarded by checkMu rather than mu so a busy scenario that checks
	// every response does not contend with Record.
	checkMu    sync.Mutex
	checkOrder []string
	checks     map[string]*CheckResult
}

// NewCollector returns a Collector ready to record a run that is
// starting now.
func NewCollector() *Collector {
	return &Collector{start: time.Now()}
}

// Record implements engine.Recorder.
func (c *Collector) Record(r engine.IterationResult) {
	s := sample{
		offset: r.Start.Sub(c.start),
		dur:    r.Duration,
		failed: r.Err != nil,
	}
	c.mu.Lock()
	c.samples = append(c.samples, s)
	c.mu.Unlock()

	c.liveTotal.Add(1)
	if s.failed {
		c.liveFailed.Add(1)
	}
}

// MaxCheckNames bounds how many distinct check names a run keeps. Further
// names are counted together under OtherChecksName.
const MaxCheckNames = 100

// OtherChecksName is the bucket for check names past MaxCheckNames.
const OtherChecksName = "(other checks)"

// RecordCheck counts one evaluation of the named check (FR-CLI-12). It
// implements netapi.CheckRecorder and is safe for concurrent use.
func (c *Collector) RecordCheck(name string, passed bool) {
	c.checkMu.Lock()
	defer c.checkMu.Unlock()
	if c.checks == nil {
		c.checks = map[string]*CheckResult{}
	}
	cr := c.checks[name]
	if cr == nil && len(c.checks) >= MaxCheckNames {
		// A name built from an id or URL would grow this map with every
		// iteration. Past the cap, new names share one bucket, so the
		// counts (and check_rate) stay right and memory stays fixed.
		name = OtherChecksName
		cr = c.checks[name]
	}
	if cr == nil {
		cr = &CheckResult{Name: name}
		c.checks[name] = cr
		c.checkOrder = append(c.checkOrder, name)
	}
	if passed {
		cr.Passes++
	} else {
		cr.Fails++
	}
}

// snapshotChecks returns a copy of the check totals, in first-seen order.
func (c *Collector) snapshotChecks() []CheckResult {
	c.checkMu.Lock()
	defer c.checkMu.Unlock()
	if len(c.checkOrder) == 0 {
		return nil
	}
	out := make([]CheckResult, 0, len(c.checkOrder))
	for _, n := range c.checkOrder {
		out = append(out, *c.checks[n])
	}
	return out
}

// Counts returns the total and failed iteration counts recorded so
// far. Safe to call concurrently with Record, including while the run
// it belongs to is still in progress.
func (c *Collector) Counts() (total, failed int64) {
	return c.liveTotal.Load(), c.liveFailed.Load()
}

// bucketWidth is how wide each point in a Result's TimeSeries is. One
// second gives a readable RPS/error-rate line for a run of any length
// this phase's hard caps allow (see internal/safety), without the
// series itself becoming the thing that makes the report large.
const bucketWidth = time.Second

// Finish computes a Result from every sample recorded so far. Call it
// once, after the executor has returned; executor identifies the load
// shape (ex.Name()) and elapsed is the run's measured wall-clock time,
// both of which the caller already has from runScenario.
func (c *Collector) Finish(executor string, elapsed time.Duration) *Result {
	c.mu.Lock()
	samples := make([]sample, len(c.samples))
	copy(samples, c.samples)
	c.mu.Unlock()

	res := &Result{
		Executor:  executor,
		StartedAt: c.start,
		Elapsed:   elapsed,
		Total:     int64(len(samples)),
		Checks:    c.snapshotChecks(),
	}
	if len(samples) == 0 {
		return res
	}

	durations := make([]time.Duration, len(samples))
	var sum time.Duration
	var failed int64
	buckets := map[int64]*bucketAccum{}

	for i, s := range samples {
		durations[i] = s.dur
		sum += s.dur
		if s.failed {
			failed++
		}
		idx := int64(s.offset / bucketWidth)
		b := buckets[idx]
		if b == nil {
			b = &bucketAccum{}
			buckets[idx] = b
		}
		b.count++
		if s.failed {
			b.failed++
		}
	}

	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })

	res.Failed = failed
	res.Latency = Latency{
		Min:  durations[0],
		Max:  durations[len(durations)-1],
		Mean: sum / time.Duration(len(durations)),
		P50:  percentile(durations, 50),
		P90:  percentile(durations, 90),
		P95:  percentile(durations, 95),
		P99:  percentile(durations, 99),
	}
	if res.Total > 0 {
		res.ErrorRate = float64(failed) / float64(res.Total)
	}
	res.TimeSeries = buildSeries(buckets)
	return res
}

type bucketAccum struct {
	count  int64
	failed int64
}

// percentile returns the p-th percentile (0-100) of sorted, a slice
// already sorted ascending. It uses the nearest-rank method — simple,
// and exact enough for a load-testing report; no interpolation.
func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := (p * len(sorted)) / 100
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

// buildSeries turns the bucket map into a dense, time-ordered slice —
// dense so a chart renderer never has to guess whether a missing index
// means "no bucket" or "zero requests that second".
func buildSeries(buckets map[int64]*bucketAccum) []Point {
	if len(buckets) == 0 {
		return nil
	}
	var maxIdx int64
	for idx := range buckets {
		if idx > maxIdx {
			maxIdx = idx
		}
	}
	series := make([]Point, maxIdx+1)
	for idx := int64(0); idx <= maxIdx; idx++ {
		p := Point{Offset: time.Duration(idx) * bucketWidth}
		if b, ok := buckets[idx]; ok {
			p.Requests = b.count
			p.Failed = b.failed
			p.RPS = float64(b.count) / bucketWidth.Seconds()
			if b.count > 0 {
				p.ErrorRate = float64(b.failed) / float64(b.count)
			}
		}
		series[idx] = p
	}
	return series
}
