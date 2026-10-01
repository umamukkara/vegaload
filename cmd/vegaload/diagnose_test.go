package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/diagnose"
	"github.com/vegaload/vegaload/internal/report"
)

func TestCmdDiagnose_ExitsZero(t *testing.T) {
	if code := cmdDiagnose(nil); code != 0 {
		t.Errorf("cmdDiagnose returned exit code %d, want 0", code)
	}
}

func TestCmdDiagnose_OutputJSON(t *testing.T) {
	if code := cmdDiagnose([]string{"-output", "json"}); code != 0 {
		t.Errorf("cmdDiagnose -output json returned exit code %d, want 0", code)
	}
}

func TestCmdDiagnose_InvalidOutputMode(t *testing.T) {
	if code := cmdDiagnose([]string{"-output", "bogus"}); code == 0 {
		t.Error("expected a non-zero exit code for an invalid -output mode")
	}
}

func TestCmdDiagnose_ReportPathText(t *testing.T) {
	path := writeTestReport(t)
	if code := cmdDiagnose([]string{path}); code != 0 {
		t.Errorf("cmdDiagnose <report> returned exit code %d, want 0", code)
	}
}

func TestCmdDiagnose_ReportPathJSON(t *testing.T) {
	path := writeTestReport(t)
	if code := cmdDiagnose([]string{"-output", "json", "-no-llm", path}); code != 0 {
		t.Errorf("cmdDiagnose -output json <report> returned exit code %d, want 0", code)
	}
}

func TestCmdDiagnose_ReportPathMissing(t *testing.T) {
	if code := cmdDiagnose([]string{"/nonexistent/report.json"}); code == 0 {
		t.Error("expected a non-zero exit code for a missing report file")
	}
}

func TestCmdDiagnose_ReportPathNotJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not-a-report.json")
	if err := writeFile(path, "not json at all"); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	if code := cmdDiagnose([]string{path}); code == 0 {
		t.Error("expected a non-zero exit code for a non-JSON report file")
	}
}

func TestCmdDiagnose_TooManyArgs(t *testing.T) {
	if code := cmdDiagnose([]string{"a", "b"}); code == 0 {
		t.Error("expected a non-zero exit code for more than one positional argument")
	}
}

func TestNarratePrompt_IncludesNotes(t *testing.T) {
	f := diagnoseFindingsForTest()
	prompt := narratePrompt("report.json", f)
	if !containsAll(prompt, f.Notes) {
		t.Errorf("expected prompt to include every note, got: %s", prompt)
	}
}

func TestMainRun_UnknownCommand(t *testing.T) {
	if code := run([]string{"bogus"}); code == 0 {
		t.Error("expected a non-zero exit code for an unknown command")
	}
}

func TestMainRun_NoArgs(t *testing.T) {
	if code := run([]string{}); code == 0 {
		t.Error("expected a non-zero exit code with no arguments")
	}
}

func TestMainRun_Version(t *testing.T) {
	if code := run([]string{"version"}); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

// --- test helpers ---

func writeTestReport(t *testing.T) string {
	t.Helper()
	res := &report.Result{
		Executor:  "constant-vus",
		StartedAt: time.Now(),
		Elapsed:   2 * time.Second,
		Total:     100,
		Failed:    5,
		ErrorRate: 0.05,
		Latency: report.Latency{
			Min: 1 * time.Millisecond, Mean: 10 * time.Millisecond, Max: 50 * time.Millisecond,
			P50: 9 * time.Millisecond, P90: 20 * time.Millisecond, P95: 25 * time.Millisecond, P99: 40 * time.Millisecond,
		},
		TimeSeries: []report.Point{
			{Offset: 0, Requests: 50, Failed: 1, RPS: 50, ErrorRate: 0.02},
			{Offset: time.Second, Requests: 50, Failed: 4, RPS: 50, ErrorRate: 0.08},
		},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	if err := report.WriteJSON(path, res); err != nil {
		t.Fatalf("report.WriteJSON: %v", err)
	}
	return path
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func diagnoseFindingsForTest() diagnose.Findings {
	return diagnose.Analyze(&report.Result{
		Total:     100,
		Failed:    5,
		ErrorRate: 0.05,
	})
}

func containsAll(haystack string, needles []string) bool {
	for _, n := range needles {
		if !strings.Contains(haystack, n) {
			return false
		}
	}
	return true
}
