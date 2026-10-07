package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/report"
)

func writeBaseline(t *testing.T, r *report.Result) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "baseline.json")
	if err := report.WriteJSON(p, r); err != nil {
		t.Fatal(err)
	}
	return p
}

// baselineRun runs a short protocol-direct run against a fast local
// server, with extra flags, and returns the exit code and written summary.
func baselineRun(t *testing.T, extra ...string) (int, report.Result, string) {
	t.Helper()
	return baselineRunAgainst(t, func(w http.ResponseWriter, r *http.Request) {}, extra...)
}

// baselineRunAgainst is baselineRun with a chosen server. A server that
// answers 500 gives a run whose error rate is 100%, which is worse than a
// clean baseline on every machine. A latency check cannot do that: on
// Windows the clock is coarse, and a fast local call can measure 0s.
func baselineRunAgainst(t *testing.T, h http.HandlerFunc, extra ...string) (int, report.Result, string) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	out := filepath.Join(dir, "summary.json")
	audit := filepath.Join(dir, "audit.log")
	args := []string{"-target", srv.URL, "-protocol", "http1", "-vus", "2", "-duration", "300ms",
		"-no-report", "-audit-log", audit, "-out", out}
	args = append(args, extra...)
	code := cmdRun(args)
	var res report.Result
	if data, err := os.ReadFile(out); err == nil {
		if err := json.Unmarshal(data, &res); err != nil {
			t.Fatal(err)
		}
	}
	return code, res, audit
}

func goodBaseline() *report.Result {
	return &report.Result{Total: 1000, Elapsed: time.Second, Latency: report.Latency{P95: 10 * time.Second}}
}

func TestCmdRun_Baseline_WithinTheLimitExitsZero(t *testing.T) {
	code, res, _ := baselineRun(t, "-baseline", writeBaseline(t, goodBaseline()), "-max-regression", "10")
	if code != 0 || res.Baseline == nil || !res.Baseline.Passed || res.Baseline.MaxRegressionPercent != 10 {
		t.Fatalf("code=%d baseline=%+v", code, res.Baseline)
	}
}

func TestCmdRun_Baseline_WorseExitsThree(t *testing.T) {
	// The baseline had no errors. This run fails every request.
	code, res, auditPath := baselineRunAgainst(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }, "-baseline", writeBaseline(t, goodBaseline()), "-max-regression", "10")
	if code != 3 || res.Baseline == nil || res.Baseline.Passed {
		t.Fatalf("code=%d baseline=%+v", code, res.Baseline)
	}
	if len(res.Baseline.Notes) == 0 || !strings.Contains(res.Baseline.Notes[0], "error rate") {
		t.Errorf("notes = %v", res.Baseline.Notes)
	}
	entries := readAudit(t, auditPath)
	if len(entries) != 1 || entries[0].Baseline == "" || entries[0].BaselinePassed == nil || *entries[0].BaselinePassed {
		t.Errorf("audit = %+v", entries)
	}
}

func TestCmdRun_Baseline_ComposesWithThresholds(t *testing.T) {
	b := goodBaseline()
	code, res, _ := baselineRun(t, "-baseline", writeBaseline(t, b), "-threshold", "p50 < 0s")
	if code != 3 || res.ThresholdsPassed == nil || *res.ThresholdsPassed || res.Baseline == nil || !res.Baseline.Passed {
		t.Fatalf("code=%d thresholds=%v baseline=%+v", code, res.ThresholdsPassed, res.Baseline)
	}
}

func TestCmdRun_Baseline_UsageErrorsExitTwo(t *testing.T) {
	empty := writeBaseline(t, &report.Result{})
	notJSON := filepath.Join(t.TempDir(), "x.json")
	os.WriteFile(notJSON, []byte("nope"), 0o644)
	cases := map[string][]string{
		"max-regression alone": {"-max-regression", "10"},
		"negative":             {"-baseline", writeBaseline(t, goodBaseline()), "-max-regression", "-5"},
		"missing file":         {"-baseline", filepath.Join(t.TempDir(), "nope.json")},
		"not a report":         {"-baseline", notJSON},
		"empty baseline":       {"-baseline", empty},
	}
	for name, extra := range cases {
		if code, _, _ := baselineRun(t, extra...); code != 2 {
			t.Errorf("%s: exit %d, want 2", name, code)
		}
	}
}

func TestCmdRun_NoBaselineLeavesTheResultAlone(t *testing.T) {
	code, res, _ := baselineRun(t)
	if code != 0 || res.Baseline != nil {
		t.Fatalf("code=%d baseline=%+v", code, res.Baseline)
	}
}

func TestPrintBaseline_ShowsTheVerdict(t *testing.T) {
	var sb strings.Builder
	printBaseline(&sb, &report.Result{Baseline: &report.BaselineResult{
		Path: "b.json", MaxRegressionPercent: 10, Passed: false,
		Metrics: []report.BaselineMetric{{Name: "latency.p95", Baseline: 1e8, Candidate: 2e8, Unit: "ns", Regressed: true}},
		Notes:   []string{"p95 latency rose"},
	}})
	out := sb.String()
	for _, want := range []string{"baseline b.json", "max regression 10%", "FAIL", "latency.p95", "p95 latency rose"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	sb.Reset()
	printBaseline(&sb, &report.Result{})
	if sb.Len() != 0 {
		t.Errorf("no baseline should print nothing, got %q", sb.String())
	}
}

func TestGateFailures_ListsBothWhenBothFail(t *testing.T) {
	no, yes := false, true
	both := &report.Result{
		Thresholds: []report.ThresholdResult{{Name: "fast", Passed: false, Observed: "2s"}}, ThresholdsPassed: &no,
		Baseline: &report.BaselineResult{Path: "b.json", Passed: false, Notes: []string{"p95 rose"}},
	}
	got := gateFailures(both)
	if len(got) != 2 || !strings.HasPrefix(got[0], "thresholds breached: fast") || !strings.Contains(got[1], "worse than the baseline b.json: p95 rose") {
		t.Fatalf("got %q", got)
	}
	onlyBaseline := &report.Result{ThresholdsPassed: &yes, Baseline: both.Baseline}
	if got := gateFailures(onlyBaseline); len(got) != 1 || !strings.Contains(got[0], "baseline") {
		t.Fatalf("got %q", got)
	}
	if got := gateFailures(&report.Result{}); len(got) != 0 {
		t.Fatalf("a clean run should list nothing, got %q", got)
	}
}

func TestCmdRun_Baseline_BothGatesFailExitsThreeWithBothVerdicts(t *testing.T) {
	code, res, _ := baselineRunAgainst(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }, "-baseline", writeBaseline(t, goodBaseline()), "-threshold", "p50 < 0s")
	if code != 3 || res.ThresholdsPassed == nil || *res.ThresholdsPassed || res.Baseline == nil || res.Baseline.Passed {
		t.Fatalf("code=%d thresholds=%v baseline=%+v", code, res.ThresholdsPassed, res.Baseline)
	}
	if len(gateFailures(&res)) != 2 {
		t.Fatalf("want both failures listed, got %q", gateFailures(&res))
	}
}
