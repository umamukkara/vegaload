package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteHTML_SelfContained(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.html")

	res := &Result{
		Executor:  "ramp",
		StartedAt: time.Now(),
		Elapsed:   2 * time.Second,
		Total:     100,
		Failed:    5,
		ErrorRate: 0.05,
		Latency: Latency{
			Min: time.Millisecond, Mean: 10 * time.Millisecond, Max: 50 * time.Millisecond,
			P50: 8 * time.Millisecond, P90: 20 * time.Millisecond, P95: 30 * time.Millisecond, P99: 45 * time.Millisecond,
		},
		TimeSeries: []Point{
			{Offset: 0, Requests: 50, Failed: 2, RPS: 50, ErrorRate: 0.04},
			{Offset: time.Second, Requests: 50, Failed: 3, RPS: 50, ErrorRate: 0.06},
		},
	}

	if err := WriteHTML(path, res); err != nil {
		t.Fatalf("WriteHTML: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	out := string(data)

	// Self-contained: no external resource references at all (FR-RPT-01/03).
	for _, bad := range []string{"http://", "https://", "<script src", "<link rel=\"stylesheet\""} {
		if strings.Contains(out, bad) {
			t.Errorf("report references an external resource (%q); it must render fully offline", bad)
		}
	}
	if !strings.HasPrefix(out, "<!doctype html>") {
		t.Error("report should be a standalone HTML document")
	}
	if !strings.Contains(out, "<svg") {
		t.Error("report should embed inline SVG charts, not reference external images")
	}
	if !strings.Contains(out, "100") { // total requests
		t.Error("report should show the total request count")
	}
}

func TestWriteHTML_EmptyResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.html")
	res := &Result{Executor: "fixed-vus"}

	if err := WriteHTML(path, res); err != nil {
		t.Fatalf("WriteHTML on an empty result should not error: %v", err)
	}
}

func TestWriteHTML_Thresholds(t *testing.T) {
	failed := false
	res := &Result{
		Executor: "fixed-vus",
		Total:    10,
		Thresholds: []ThresholdResult{
			{Name: "fast <api>", Metric: "p95", Operator: "<", Value: "1ms", Observed: "3ms", Passed: false},
			{Name: "low errors", Metric: "error_rate", Operator: "<", Value: "0.01", Observed: "0", Passed: true},
		},
		ThresholdsPassed: &failed,
	}
	out := renderHTML(res)
	for _, want := range []string{"Thresholds breached", ">FAIL<", ">PASS<", "fast &lt;api&gt;", "3ms"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q", want)
		}
	}
	for _, bad := range []string{"http://", "https://"} {
		if strings.Contains(out, bad) {
			t.Errorf("report references an external resource (%q)", bad)
		}
	}

	passed := true
	res.Thresholds = res.Thresholds[1:]
	res.ThresholdsPassed = &passed
	if out := renderHTML(res); !strings.Contains(out, "All thresholds passed") {
		t.Error("a passing run should say so")
	}

	res.Thresholds, res.ThresholdsPassed = nil, nil
	if out := renderHTML(res); strings.Contains(out, "Thresholds") {
		t.Error("a run without thresholds must not render a thresholds section")
	}
}
