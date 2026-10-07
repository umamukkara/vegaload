package compare

import (
	"fmt"

	"github.com/vegaload/vegaload/internal/report"
)

// GateOptions turns "no more than pct percent worse than the baseline"
// into Compare's options. p95 may rise by pct percent. The error rate may
// rise by pct percent of the baseline's error rate, so a baseline with no
// errors allows none: use a -threshold on error_rate for an absolute limit.
func GateOptions(baseline *report.Result, pct float64) Options {
	return Options{
		P95Ratio:       1 + pct/100,
		ErrorRateDelta: baseline.ErrorRate * pct / 100,
		SkipChecks:     true,
	}
}

// Gate judges a finished run against a baseline report (FR-CLI-14). It
// uses Compare, so `vegaload run -baseline` and `vegaload compare` agree.
// A run that completed no requests fails the gate: with nothing measured,
// "no worse than the baseline" would pass trivially.
func Gate(baseline, candidate *report.Result, path string, pct float64) *report.BaselineResult {
	out := &report.BaselineResult{Path: path, MaxRegressionPercent: pct}
	cmp := Compare(baseline, candidate, GateOptions(baseline, pct))
	out.Passed = !cmp.Regressed
	for _, m := range cmp.Metrics {
		if m.Name == "error_rate" || m.Name == "latency.p95" {
			out.Metrics = append(out.Metrics, report.BaselineMetric{
				Name: m.Name, Baseline: m.Baseline, Candidate: m.Candidate, Unit: m.Unit, Regressed: m.Regressed,
			})
		}
	}
	if cmp.Regressed {
		out.Notes = cmp.Notes
	}
	if candidate.Total == 0 {
		out.Passed = false
		out.Notes = append(out.Notes, "no requests completed, so there is nothing to compare with the baseline")
	}
	if out.Passed {
		out.Notes = append(out.Notes, fmt.Sprintf("p95 and error rate are within %g%% of the baseline", pct))
	}
	return out
}
