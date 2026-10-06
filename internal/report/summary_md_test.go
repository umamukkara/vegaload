package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarkdownSummary_KeyNumbersAndVerdicts(t *testing.T) {
	r := sampleResult()
	r.Thresholds[1].Name = "a|b"
	got := MarkdownSummary(r)
	for _, want := range []string{
		"## VegaLoad: fixed-vus", "| 100 | 2 | 2.00% |", "50.0 |",
		"### Thresholds: breached", "| PASS | p95 < 300ms |", `| FAIL | a\|b |`,
		"### Checks: 99.00% passed", "| FAIL | status is 200 | 98 | 2 |", "| PASS | has id | 100 | 0 |",
		"### Baseline: worse than the baseline", "- p95 rose",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}

func TestMarkdownSummary_OmitsSectionsThatDoNotApply(t *testing.T) {
	got := MarkdownSummary(&Result{Executor: "fixed-vus", Total: 5})
	for _, no := range []string{"Thresholds", "Checks", "Baseline"} {
		if strings.Contains(got, no) {
			t.Errorf("did not expect %q in\n%s", no, got)
		}
	}
}

func TestAppendStepSummary_KeepsWhatIsAlreadyThere(t *testing.T) {
	p := filepath.Join(t.TempDir(), "summary.md")
	if err := os.WriteFile(p, []byte("earlier step\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AppendStepSummary(p, sampleResult()); err != nil {
		t.Fatal(err)
	}
	if err := AppendStepSummary(p, sampleResult()); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if !strings.HasPrefix(string(data), "earlier step\n") || strings.Count(string(data), "## VegaLoad") != 2 {
		t.Fatalf("got:\n%s", data)
	}
}
