package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A test run inside GitHub Actions must not write to the real job summary.
func init() { os.Unsetenv("GITHUB_STEP_SUMMARY") }

func TestCmdRun_JUnit_WritesCasesAndStillExitsThree(t *testing.T) {
	junit := filepath.Join(t.TempDir(), "junit.xml")
	code, _, _ := baselineRun(t, "-junit", junit, "-threshold", "fast: p50 < 0s", "-threshold", "ok: error_rate < 1")
	if code != 3 {
		t.Fatalf("exit %d, want 3", code)
	}
	data, err := os.ReadFile(junit)
	if err != nil {
		t.Fatalf("no JUnit file: %v", err)
	}
	var doc struct {
		Tests    int `xml:"tests,attr"`
		Failures int `xml:"failures,attr"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil || doc.Tests != 2 || doc.Failures != 1 {
		t.Fatalf("err=%v doc=%+v\n%s", err, doc, data)
	}
}

func TestCmdRun_JUnit_UnwritablePathIsAWarningNotAFailure(t *testing.T) {
	code, _, _ := baselineRun(t, "-junit", filepath.Join(t.TempDir(), "no", "such", "dir", "junit.xml"))
	if code != 0 {
		t.Fatalf("exit %d, want 0: a report-writing failure must not fail the run", code)
	}
}

func TestCmdRun_StepSummary_AppendedInsideGitHubActions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "step.md")
	t.Setenv("GITHUB_STEP_SUMMARY", p)
	code, _, _ := baselineRun(t, "-threshold", "ok: error_rate < 1")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	data, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(data), "## VegaLoad") || !strings.Contains(string(data), "| PASS | ok |") {
		t.Fatalf("err=%v summary=%s", err, data)
	}
}

func TestCmdRun_StepSummary_NotWrittenOutsideActionsOrWhenTurnedOff(t *testing.T) {
	p := filepath.Join(t.TempDir(), "step.md")
	t.Setenv("GITHUB_STEP_SUMMARY", p)
	baselineRun(t, "-no-step-summary")
	if _, err := os.Stat(p); err == nil {
		t.Error("-no-step-summary still wrote the summary")
	}
	t.Setenv("GITHUB_STEP_SUMMARY", "")
	baselineRun(t)
	if _, err := os.Stat(p); err == nil {
		t.Error("wrote a summary with GITHUB_STEP_SUMMARY empty")
	}
}
