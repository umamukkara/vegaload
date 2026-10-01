package diagnose

import (
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/report"
)

func TestAnalyze_ZeroIterations(t *testing.T) {
	f := Analyze(&report.Result{})
	if !f.ZeroIterations {
		t.Fatal("expected ZeroIterations to be true for an empty result")
	}
	if len(f.Notes) != 1 {
		t.Fatalf("expected exactly one note, got %d: %v", len(f.Notes), f.Notes)
	}
}

func TestAnalyze_HighFailureRate(t *testing.T) {
	res := &report.Result{Total: 100, Failed: 60, ErrorRate: 0.6}
	f := Analyze(res)
	if !f.HighFailureRate {
		t.Error("expected HighFailureRate for a 60% error rate")
	}
	if f.FailurePercent != 60 {
		t.Errorf("FailurePercent = %v, want 60", f.FailurePercent)
	}
}

func TestAnalyze_NoFailures(t *testing.T) {
	res := &report.Result{Total: 100, Failed: 0, ErrorRate: 0}
	f := Analyze(res)
	if f.HighFailureRate {
		t.Error("did not expect HighFailureRate with zero failures")
	}
	if f.LatencyLongTail {
		t.Error("did not expect LatencyLongTail with no latency data")
	}
}

func TestAnalyze_LatencyLongTail(t *testing.T) {
	res := &report.Result{
		Total: 100,
		Latency: report.Latency{
			P50: 10 * time.Millisecond,
			P99: 100 * time.Millisecond, // 10x p50
		},
	}
	f := Analyze(res)
	if !f.LatencyLongTail {
		t.Error("expected LatencyLongTail when p99 is 10x p50")
	}
}

func TestAnalyze_LatencyNotLongTail(t *testing.T) {
	res := &report.Result{
		Total: 100,
		Latency: report.Latency{
			P50: 10 * time.Millisecond,
			P99: 30 * time.Millisecond, // 3x p50
		},
	}
	f := Analyze(res)
	if f.LatencyLongTail {
		t.Error("did not expect LatencyLongTail when p99 is only 3x p50")
	}
}

func TestAnalyze_TransientFailure(t *testing.T) {
	res := &report.Result{
		Total:     100,
		Failed:    10,
		ErrorRate: 0.1,
		TimeSeries: []report.Point{
			{Offset: 0, Requests: 50, Failed: 0, ErrorRate: 0},
			{Offset: time.Second, Requests: 50, Failed: 10, ErrorRate: 0.2},
		},
	}
	f := Analyze(res)
	if f.WorstBucket == nil {
		t.Fatal("expected a WorstBucket")
	}
	if f.WorstBucket.OffsetSeconds != 1 {
		t.Errorf("WorstBucket.OffsetSeconds = %v, want 1", f.WorstBucket.OffsetSeconds)
	}
	if !f.Transient {
		t.Error("expected Transient when the worst bucket's rate is well above the overall rate")
	}
}

func TestAnalyze_SpreadFailure(t *testing.T) {
	res := &report.Result{
		Total:     100,
		Failed:    20,
		ErrorRate: 0.2,
		TimeSeries: []report.Point{
			{Offset: 0, Requests: 50, Failed: 10, ErrorRate: 0.2},
			{Offset: time.Second, Requests: 50, Failed: 10, ErrorRate: 0.2},
		},
	}
	f := Analyze(res)
	if f.Transient {
		t.Error("did not expect Transient when every bucket has the same error rate")
	}
}

func TestSuggestThresholds_RoundsUpAndFloors(t *testing.T) {
	res := &report.Result{
		Latency:   report.Latency{P95: 83 * time.Millisecond},
		ErrorRate: 0, // below the 1% floor
	}
	th := SuggestThresholds(res)
	// 83ms * 1.2 = 99.6, rounds up to the nearest 10ms -> 100ms
	if th.LatencyP95 != "100ms" {
		t.Errorf("LatencyP95 = %q, want %q", th.LatencyP95, "100ms")
	}
	if th.ErrorRate != 0.01 {
		t.Errorf("ErrorRate = %v, want 0.01 (floor)", th.ErrorRate)
	}
}

func TestSuggestThresholds_ScalesObservedErrorRate(t *testing.T) {
	res := &report.Result{
		Latency:   report.Latency{P95: 50 * time.Millisecond},
		ErrorRate: 0.1, // 1.5x = 0.15, above the floor
	}
	th := SuggestThresholds(res)
	if diff := th.ErrorRate - 0.15; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("ErrorRate = %v, want ~0.15", th.ErrorRate)
	}
}

func TestRoundUpToNearest10_ExactMultiple(t *testing.T) {
	if got := roundUpToNearest10(100); got != 100 {
		t.Errorf("roundUpToNearest10(100) = %d, want 100", got)
	}
}

func TestRoundUpToNearest10_ZeroOrNegative(t *testing.T) {
	if got := roundUpToNearest10(0); got != 10 {
		t.Errorf("roundUpToNearest10(0) = %d, want 10", got)
	}
}
