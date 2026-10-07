package threshold

import "github.com/vegaload/vegaload/internal/report"

// LiveKind says how far a threshold can be judged while a run is still in
// progress (FR-CLI-13, -abort-on-breach).
type LiveKind int

const (
	// LiveNone thresholds are judged only on the finished run. An example
	// is rps, which is an average over the whole run, or "total >= 1000",
	// which cannot be broken until the run is over.
	LiveNone LiveKind = iota
	// LiveSticky thresholds cannot recover once broken. The number of
	// failed requests only goes up, so "failed < 5" is broken for good the
	// moment the fifth one fails. The same holds for "max" and "total"
	// with an upper limit, and for "min" with a lower limit.
	LiveSticky
	// LiveSustained thresholds are statistics, such as p95 or error_rate.
	// They move up and down, so a breach early in the run may go away. A
	// run is stopped on one only when it stays broken (see the watcher in
	// cmd/vegaload).
	LiveSustained
)

// MinLiveSamples is how many requests (or checks, or runs of a step) a
// statistic needs before a live judgement counts. A p95 of ten requests
// says very little.
const MinLiveSamples = 20

// Live reports how far t can be judged during a run.
func (t Threshold) Live() LiveKind {
	upper := t.Operator == "<" || t.Operator == "<="
	lower := t.Operator == ">" || t.Operator == ">="
	switch t.Metric {
	case "failed", "total", "max":
		if upper {
			return LiveSticky
		}
		return LiveNone
	case "min":
		if lower {
			return LiveSticky
		}
		return LiveNone
	case "p50", "p90", "p95", "p99", "mean", "error_rate", "check_rate":
		return LiveSustained
	}
	return LiveNone
}

// LiveBreach judges t on a snapshot of a run in progress. It reports
// breached only when there is enough evidence: a step that has not run
// yet, or too few samples for a statistic, is not a breach. Those are
// settled on the finished run, as before.
func (t Threshold) LiveBreach(res *report.Result) (observed string, breached bool) {
	kind := t.Live()
	if kind == LiveNone {
		return "", false
	}
	need := int64(1)
	if kind == LiveSustained {
		need = MinLiveSamples
	}
	switch {
	case t.Step != "":
		s, ok := res.Step(t.Step)
		if !ok || s.Total < need {
			return "", false
		}
	case t.Metric == "check_rate":
		var total int64
		for _, c := range res.Checks {
			total += c.Passes + c.Fails
		}
		if total < need {
			return "", false
		}
	default:
		if res.Total < need {
			return "", false
		}
	}
	r := Evaluate([]Threshold{t}, res)[0]
	return r.Observed, !r.Passed
}

// NotLive returns the thresholds that cannot be judged during a run, so
// the caller can say they wait for the end.
func NotLive(ts []Threshold) []Threshold {
	var out []Threshold
	for _, t := range ts {
		if t.Live() == LiveNone {
			out = append(out, t)
		}
	}
	return out
}
