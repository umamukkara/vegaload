package report

import (
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/engine"
)

func TestCollector_FinishEmpty(t *testing.T) {
	c := NewCollector()
	res := c.Finish("fixed-vus", 0)
	if res.Total != 0 {
		t.Errorf("Total = %d, want 0", res.Total)
	}
	if len(res.TimeSeries) != 0 {
		t.Errorf("TimeSeries should be empty for a run with no samples")
	}
}

func TestCollector_FinishComputesLatencyAndErrorRate(t *testing.T) {
	c := NewCollector()
	start := c.start

	// Five iterations, all in bucket 0 (within the first second), one
	// of which fails. Durations: 10ms,20ms,30ms,40ms,50ms (sorted).
	durs := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond, 40 * time.Millisecond, 50 * time.Millisecond}
	for i, d := range durs {
		var err error
		if i == len(durs)-1 {
			err = errBoom
		}
		c.Record(engine.IterationResult{VUID: i, Start: start, Duration: d, Err: err})
	}

	res := c.Finish("fixed-vus", 100*time.Millisecond)
	if res.Total != 5 {
		t.Fatalf("Total = %d, want 5", res.Total)
	}
	if res.Failed != 1 {
		t.Errorf("Failed = %d, want 1", res.Failed)
	}
	if res.ErrorRate != 0.2 {
		t.Errorf("ErrorRate = %v, want 0.2", res.ErrorRate)
	}
	if res.Latency.Min != 10*time.Millisecond {
		t.Errorf("Latency.Min = %v, want 10ms", res.Latency.Min)
	}
	if res.Latency.Max != 50*time.Millisecond {
		t.Errorf("Latency.Max = %v, want 50ms", res.Latency.Max)
	}
	if len(res.TimeSeries) != 1 {
		t.Fatalf("len(TimeSeries) = %d, want 1 (all samples in bucket 0)", len(res.TimeSeries))
	}
	if res.TimeSeries[0].Requests != 5 || res.TimeSeries[0].Failed != 1 {
		t.Errorf("TimeSeries[0] = %+v, want {Requests:5 Failed:1 ...}", res.TimeSeries[0])
	}
}

func TestCollector_FinishBucketsByOffset(t *testing.T) {
	c := NewCollector()
	start := c.start

	c.Record(engine.IterationResult{Start: start, Duration: time.Millisecond})
	c.Record(engine.IterationResult{Start: start.Add(1500 * time.Millisecond), Duration: time.Millisecond})
	c.Record(engine.IterationResult{Start: start.Add(3200 * time.Millisecond), Duration: time.Millisecond})

	res := c.Finish("fixed-vus", 4*time.Second)
	// Samples land in buckets 0, 1, and 3 — bucket 2 has zero requests
	// but must still appear (dense series), per buildSeries's doc comment.
	if len(res.TimeSeries) != 4 {
		t.Fatalf("len(TimeSeries) = %d, want 4 (dense through the last non-empty bucket)", len(res.TimeSeries))
	}
	if res.TimeSeries[2].Requests != 0 {
		t.Errorf("TimeSeries[2].Requests = %d, want 0 (empty bucket)", res.TimeSeries[2].Requests)
	}
	if res.TimeSeries[3].Requests != 1 {
		t.Errorf("TimeSeries[3].Requests = %d, want 1", res.TimeSeries[3].Requests)
	}
}

type testErr string

func (e testErr) Error() string { return string(e) }

const errBoom = testErr("boom")
