package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vegaload/vegaload/internal/report"
)

// thresholdRun runs `vegaload run` against a local test server with the
// given extra flags and returns the exit code, plus the JSON summary
// written by -out (nil if none was written).
func thresholdRun(t *testing.T, extra ...string) (int, map[string]any) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	out := filepath.Join(dir, "summary.json")
	args := []string{
		"-target", srv.URL, "-protocol", "http1",
		"-vus", "2", "-duration", "300ms",
		"-no-report", "-audit-log", filepath.Join(dir, "audit.log"),
		"-out", out,
	}
	args = append(args, extra...)
	code := cmdRun(args)

	data, err := os.ReadFile(out)
	if err != nil {
		return code, nil
	}
	var summary map[string]any
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatalf("summary is not JSON: %v", err)
	}
	return code, summary
}

func TestCmdRun_ThresholdsPass_ExitsZero(t *testing.T) {
	code, summary := thresholdRun(t, "-threshold", "p95 < 10s", "-threshold", "error_rate < 1%")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if summary["thresholds_passed"] != true {
		t.Errorf("thresholds_passed = %v, want true", summary["thresholds_passed"])
	}
	if got := summary["thresholds"].([]any); len(got) != 2 {
		t.Errorf("thresholds has %d entries, want 2", len(got))
	}
}

func TestCmdRun_ThresholdBreached_ExitsThree(t *testing.T) {
	code, summary := thresholdRun(t, "-threshold", "fast: p95 < 1ns", "-threshold", "error_rate < 1%")
	if code != 3 || exitThresholdsBreached != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
	if summary["thresholds_passed"] != false {
		t.Errorf("thresholds_passed = %v, want false", summary["thresholds_passed"])
	}
	list := summary["thresholds"].([]any)
	first := list[0].(map[string]any)
	if first["name"] != "fast" || first["passed"] != false {
		t.Errorf("first threshold = %v, want the breached 'fast'", first)
	}
	if list[1].(map[string]any)["passed"] != true {
		t.Errorf("second threshold should still pass: %v", list[1])
	}
}

func TestCmdRun_NoThresholds_OutputUnchanged(t *testing.T) {
	code, summary := thresholdRun(t)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	for _, key := range []string{"thresholds", "thresholds_passed"} {
		if _, ok := summary[key]; ok {
			t.Errorf("a run without thresholds must not carry %q", key)
		}
	}
}

func TestCmdRun_BadThreshold_IsUsageError(t *testing.T) {
	for _, flag := range [][]string{
		{"-threshold", "nonsense"},
		{"-threshold", "p95 < 300"},
		{"-threshold", "latency < 1s"},
		{"-thresholds", filepath.Join(t.TempDir(), "missing.json")},
	} {
		code, _ := thresholdRun(t, flag...)
		if code != 2 {
			t.Errorf("%v: exit code = %d, want 2", flag, code)
		}
	}
}

func TestCmdRun_ThresholdsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gate.json")
	body := `{"suggested_thresholds": {"latency_p95": "10s", "error_rate": 0.05}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	code, summary := thresholdRun(t, "-thresholds", path)
	if code != 0 || summary["thresholds_passed"] != true {
		t.Fatalf("exit %d, summary %v", code, summary["thresholds_passed"])
	}

	// File and flags combine.
	code, _ = thresholdRun(t, "-thresholds", path, "-threshold", "p50 < 1ns")
	if code != 3 {
		t.Errorf("a breached flag next to a passing file: exit code = %d, want 3", code)
	}
}

func TestCmdRun_AuditLogRecordsThresholds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	auditPath := filepath.Join(t.TempDir(), "audit.log")

	code := cmdRun([]string{
		"-target", srv.URL, "-protocol", "http1", "-vus", "1", "-duration", "200ms",
		"-no-report", "-audit-log", auditPath, "-threshold", "p95 < 1ns",
	})
	if code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	line := string(data)
	if !strings.Contains(line, `"thresholds":["p95 \u003c 1ns"]`) && !strings.Contains(line, `"thresholds":["p95 < 1ns"]`) {
		t.Errorf("audit line does not record the threshold: %s", line)
	}
	if !strings.Contains(line, `"thresholds_passed":false`) {
		t.Errorf("audit line does not record the verdict: %s", line)
	}
}

func TestRunJSONL_FinishedEventCarriesThresholds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	cfg, err := parseRunArgs([]string{"-target", srv.URL, "-protocol", "http1", "-vus", "1", "-duration", "1100ms", "-threshold", "p95 < 10s"})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := runJSONL(cfg, &buf); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var last struct {
		Type string        `json:"type"`
		Data report.Result `json:"data"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatal(err)
	}
	if last.Type != "run_finished" || len(last.Data.Thresholds) != 1 || last.Data.ThresholdsPassed == nil || !*last.Data.ThresholdsPassed {
		t.Errorf("run_finished = %+v", last)
	}
}

func TestPrintResult_ShowsThresholds(t *testing.T) {
	passed := false
	r := &report.Result{
		Executor: "fixed-vus",
		Thresholds: []report.ThresholdResult{
			{Name: "fast", Metric: "p95", Operator: "<", Value: "1ms", Observed: "3ms", Passed: false},
			{Name: "low errors", Metric: "error_rate", Operator: "<", Value: "0.01", Observed: "0", Passed: true},
		},
		ThresholdsPassed: &passed,
	}
	var buf bytes.Buffer
	printResult(&buf, r)
	out := buf.String()
	for _, want := range []string{"FAIL  fast  (observed 3ms)", "PASS  low errors", "thresholds breached"} {
		if !strings.Contains(out, want) {
			t.Errorf("text summary missing %q:\n%s", want, out)
		}
	}

	var plain bytes.Buffer
	printResult(&plain, &report.Result{Executor: "fixed-vus"})
	if strings.Contains(plain.String(), "threshold") {
		t.Errorf("a run without thresholds must not print a thresholds block:\n%s", plain.String())
	}
}
