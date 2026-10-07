package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/vegaload/vegaload/internal/engine"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/report"
	"github.com/vegaload/vegaload/internal/threshold"
)

func TestAutoGrace(t *testing.T) {
	if g := autoGrace(time.Minute); g != 5*time.Second {
		t.Errorf("1m run: grace = %s, want 5s", g)
	}
	if g := autoGrace(8 * time.Second); g != 2*time.Second {
		t.Errorf("8s run: grace = %s, want 2s", g)
	}
}

func TestParseRunArgs_AbortFlags(t *testing.T) {
	base := []string{"-target", "http://127.0.0.1:1", "-protocol", "http1"}
	if _, err := parseRunArgs(append(base, "-abort-on-breach")); err == nil || !strings.Contains(err.Error(), "-abort-on-breach needs") {
		t.Errorf("without thresholds: err = %v", err)
	}
	if _, err := parseRunArgs(append(base, "-abort-grace", "2s")); err == nil || !strings.Contains(err.Error(), "-abort-grace needs -abort-on-breach") {
		t.Errorf("grace alone: err = %v", err)
	}
	if _, err := parseRunArgs(append(base, "-abort-on-breach", "-abort-grace", "-1s", "-threshold", "failed < 1")); err == nil {
		t.Error("negative grace should fail")
	}
	cfg, err := parseRunArgs(append(base, "-abort-on-breach", "-abort-grace", "2s", "-threshold", "failed < 1"))
	if err != nil || !cfg.AbortOnBreach || cfg.AbortGrace != 2*time.Second {
		t.Errorf("cfg = %+v, err = %v", cfg, err)
	}
}

func TestBreachWatcher_StickyStopsAtOnce_SustainedNeedsAStreak(t *testing.T) {
	c := report.NewCollector()
	start := time.Now()
	for i := 0; i < 30; i++ {
		c.Record(failedIteration(time.Second))
	}
	mk := func(expr string, grace time.Duration) *breachWatcher {
		th, err := threshold.Parse(expr)
		if err != nil {
			t.Fatal(err)
		}
		w := newBreachWatcher([]threshold.Threshold{th}, c, "fixed-vus", start, grace)
		w.interval = 5 * time.Millisecond
		w.sustain = 3
		return w
	}
	cancelled := 0
	cancel := func() { cancelled++ }

	// Sticky: stops at the first look, even inside the grace period.
	stop := make(chan struct{})
	info := mk("failed < 5", time.Hour).watch(stop, cancel)
	if info == nil || info.Threshold != "failed < 5" || cancelled != 1 {
		t.Fatalf("sticky: info = %+v, cancelled = %d", info, cancelled)
	}

	// Sustained, inside the grace period: never stops.
	done := make(chan *report.AbortInfo, 1)
	stop = make(chan struct{})
	go func() { done <- mk("p95 < 1ms", time.Hour).watch(stop, cancel) }()
	select {
	case got := <-done:
		t.Fatalf("stopped inside the grace period: %+v", got)
	case <-time.After(100 * time.Millisecond):
	}
	close(stop)
	if got := <-done; got != nil {
		t.Errorf("stop returned %+v, want nil", got)
	}

	// Sustained, past the grace period: stops after the streak.
	stop = make(chan struct{})
	info = mk("p95 < 1ms", 0).watch(stop, cancel)
	if info == nil || info.Threshold != "p95 < 1ms" {
		t.Fatalf("sustained: info = %+v", info)
	}
}

func TestApplyAbort_AlwaysFailsTheThreshold(t *testing.T) {
	yes := true
	res := &report.Result{
		Thresholds:       []report.ThresholdResult{{Name: "fast", Passed: true, Observed: "100ms"}, {Name: "other", Passed: true}},
		ThresholdsPassed: &yes,
	}
	applyAbort(res, &report.AbortInfo{Threshold: "fast", Observed: "400ms", At: 3 * time.Second})
	if res.Thresholds[0].Passed || res.Thresholds[0].Observed != "400ms" || !res.Thresholds[1].Passed {
		t.Errorf("thresholds = %+v", res.Thresholds)
	}
	if res.ThresholdsPassed == nil || *res.ThresholdsPassed || res.Aborted == nil {
		t.Errorf("result = %+v", res)
	}
	applyAbort(res, nil) // no abort: no change
}

func TestCmdRun_AbortOnBreach_StopsTheRunEarly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	out := filepath.Join(dir, "summary.json")
	began := time.Now()
	code := cmdRun([]string{
		"-target", srv.URL, "-protocol", "http1",
		"-vus", "2", "-duration", "60s", "-no-report",
		"-audit-log", filepath.Join(dir, "audit.log"), "-out", out,
		"-abort-on-breach", "-threshold", "failed < 3", "-threshold", "rps >= 1",
	})
	if code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
	if took := time.Since(began); took > 30*time.Second {
		t.Fatalf("the run took %s; it should have stopped early", took)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var res report.Result
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatal(err)
	}
	if res.Aborted == nil || res.Aborted.Threshold != "failed < 3" {
		t.Fatalf("aborted = %+v", res.Aborted)
	}
	if res.ThresholdsPassed == nil || *res.ThresholdsPassed {
		t.Error("thresholds_passed should be false")
	}

	var buf bytes.Buffer
	printResult(&buf, &res)
	if !strings.Contains(buf.String(), "ABORTED") {
		t.Errorf("text summary lacks the abort line:\n%s", buf.String())
	}
	if !strings.Contains(report.MarkdownSummary(&res), "Aborted") {
		t.Error("markdown summary lacks the abort line")
	}
}

func TestCmdRun_AbortOnBreach_HealthyRunIsNotStopped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	out := filepath.Join(dir, "summary.json")
	code := cmdRun([]string{
		"-target", srv.URL, "-protocol", "http1",
		"-vus", "2", "-duration", "400ms", "-no-report",
		"-audit-log", filepath.Join(dir, "audit.log"), "-out", out,
		"-abort-on-breach", "-threshold", "failed < 3", "-threshold", "p95 < 10s",
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	data, _ := os.ReadFile(out)
	var res report.Result
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatal(err)
	}
	if res.Aborted != nil {
		t.Errorf("aborted = %+v, want nil", res.Aborted)
	}
}

// failedIteration is one failed iteration that took d, for filling a
// Collector in a test.
func failedIteration(d time.Duration) engine.IterationResult {
	return engine.IterationResult{Start: time.Now(), Duration: d, Err: errors.New("boom")}
}

func TestCmdRun_AbortOnBreach_WorksForEveryShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	shapes := map[string][]string{
		"ramp":                  {"-executor", "ramp", "-stages", "4:2s,4:30s"},
		"step":                  {"-executor", "step", "-stages", "2:30s,4:30s"},
		"constant-arrival-rate": {"-executor", "constant-arrival-rate", "-rate", "50", "-max-vus", "5", "-duration", "60s"},
	}
	for name, extra := range shapes {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			args := append([]string{
				"-target", srv.URL, "-protocol", "http1", "-vus", "2", "-no-report",
				"-audit-log", filepath.Join(dir, "audit.log"),
				"-abort-on-breach", "-threshold", "failed < 3",
			}, extra...)
			began := time.Now()
			if code := cmdRun(args); code != 3 {
				t.Fatalf("exit code = %d, want 3", code)
			}
			if took := time.Since(began); took > 20*time.Second {
				t.Fatalf("took %s, should have stopped early", took)
			}
		})
	}
}
