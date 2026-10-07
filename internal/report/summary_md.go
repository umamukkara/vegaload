package report

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// MarkdownSummary renders res as a short Markdown page: the key numbers
// and the verdicts (FR-RPT-05). `vegaload run` appends it to the file
// GitHub Actions names in GITHUB_STEP_SUMMARY, so it shows on the job's
// page. The verdicts are the words PASS and FAIL, not colour or symbols.
func MarkdownSummary(res *Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## VegaLoad: %s\n\n", mdCell(res.Executor))
	b.WriteString("| Requests | Failed | Error rate | p50 | p95 | p99 | Requests/s |\n|---:|---:|---:|---:|---:|---:|---:|\n")
	rps := 0.0
	if res.Elapsed > 0 {
		rps = float64(res.Total) / res.Elapsed.Seconds()
	}
	fmt.Fprintf(&b, "| %d | %d | %.2f%% | %s | %s | %s | %.1f |\n",
		res.Total, res.Failed, res.ErrorRate*100,
		res.Latency.P50.Round(time.Microsecond), res.Latency.P95.Round(time.Microsecond),
		res.Latency.P99.Round(time.Microsecond), rps)

	if len(res.Thresholds) > 0 {
		verdict := "all passed"
		if res.ThresholdsPassed == nil || !*res.ThresholdsPassed {
			verdict = "breached"
		}
		fmt.Fprintf(&b, "\n### Thresholds: %s\n\n| Result | Threshold | Observed |\n|---|---|---|\n", verdict)
		for _, t := range res.Thresholds {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", passWord(t.Passed), mdCell(t.Name), mdCell(t.Observed))
		}
	}
	if len(res.Checks) > 0 {
		rate, _ := res.CheckRate()
		fmt.Fprintf(&b, "\n### Checks: %.2f%% passed\n\n| Result | Check | Passed | Failed |\n|---|---|---:|---:|\n", rate*100)
		for _, c := range res.Checks {
			fmt.Fprintf(&b, "| %s | %s | %d | %d |\n", passWord(c.Fails == 0), mdCell(c.Name), c.Passes, c.Fails)
		}
	}
	if len(res.Steps) > 0 {
		b.WriteString("\n### Steps\n\n| Result | Step | Runs | Failed | Error rate | p50 | p95 | Max |\n|---|---|---:|---:|---:|---:|---:|---:|\n")
		for _, st := range res.Steps {
			fmt.Fprintf(&b, "| %s | %s | %d | %d | %.2f%% | %s | %s | %s |\n", passWord(st.Failed == 0), mdCell(st.Name), st.Total, st.Failed,
				st.ErrorRate*100, st.Latency.P50.Round(time.Microsecond), st.Latency.P95.Round(time.Microsecond), st.Latency.Max.Round(time.Microsecond))
		}
	}
	if bl := res.Baseline; bl != nil {
		verdict := "within the baseline"
		if !bl.Passed {
			verdict = "worse than the baseline"
		}
		fmt.Fprintf(&b, "\n### Baseline: %s\n\nCompared with `%s`, up to %g%% worse allowed.\n\n| Result | Metric | Baseline | This run |\n|---|---|---:|---:|\n",
			verdict, mdCell(bl.Path), bl.MaxRegressionPercent)
		for _, m := range bl.Metrics {
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", passWord(!m.Regressed), mdCell(m.Name), baselineValue(m.Baseline, m.Unit), baselineValue(m.Candidate, m.Unit))
		}
		for _, n := range bl.Notes {
			fmt.Fprintf(&b, "\n- %s\n", mdCell(n))
		}
	}
	return b.String()
}

// AppendStepSummary appends MarkdownSummary(res) to the file at path. A
// step summary file is shared by every step of a job, so it is appended
// to, never replaced.
func AppendStepSummary(path string, res *Result) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(MarkdownSummary(res) + "\n"); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func passWord(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

// mdCell makes s safe inside a Markdown table cell: a pipe would start a
// new cell, and a line break would end the row.
func mdCell(s string) string {
	s = strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
	return strings.ReplaceAll(s, "|", `\|`)
}
