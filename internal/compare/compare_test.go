package compare

import (
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/report"
)

func baseResult() *report.Result {
	return &report.Result{
		Executor:  "fixed-vus",
		Elapsed:   10 * time.Second,
		Total:     1000,
		Failed:    10,
		ErrorRate: 0.01,
		Latency: report.Latency{
			Mean: 20 * time.Millisecond,
			P50:  18 * time.Millisecond,
			P90:  40 * time.Millisecond,
			P95:  50 * time.Millisecond,
			P99:  80 * time.Millisecond,
		},
	}
}

func TestCompare_NoRegression(t *testing.T) {
	base := baseResult()
	cand := baseResult()
	cand.ErrorRate = 0.005
	cand.Failed = 5
	cand.Latency.P95 = 45 * time.Millisecond

	got := Compare(base, cand, Options{})
	if got.Regressed {
		t.Fatalf("Regressed = true, want false; notes=%v", got.Notes)
	}
	if len(got.Notes) == 0 || got.Notes[0] != "no regressions against the baseline" {
		t.Errorf("Notes = %v, want reassuring note", got.Notes)
	}
}

func TestCompare_ErrorRateRegression(t *testing.T) {
	base := baseResult()
	cand := baseResult()
	cand.ErrorRate = 0.05
	cand.Failed = 50

	got := Compare(base, cand, Options{})
	if !got.Regressed {
		t.Fatal("expected Regressed true for higher error rate")
	}
	if m := metric(got, "error_rate"); m == nil || !m.Regressed {
		t.Errorf("error_rate metric = %+v, want Regressed true", m)
	}
}

func TestCompare_P95Regression(t *testing.T) {
	base := baseResult()
	cand := baseResult()
	cand.Latency.P95 = 80 * time.Millisecond

	got := Compare(base, cand, Options{})
	if !got.Regressed {
		t.Fatal("expected Regressed true for higher p95")
	}
	if m := metric(got, "latency.p95"); m == nil || !m.Regressed {
		t.Errorf("latency.p95 metric = %+v, want Regressed true", m)
	}
}

func TestCompare_P95RatioAllowsHeadroom(t *testing.T) {
	base := baseResult()
	cand := baseResult()
	cand.Latency.P95 = 55 * time.Millisecond // 10% over 50ms

	got := Compare(base, cand, Options{P95Ratio: 1.2})
	if got.Regressed {
		t.Fatalf("Regressed = true with 1.2 ratio and 10%% p95 rise; notes=%v", got.Notes)
	}
	if m := metric(got, "latency.p95"); m == nil || m.Regressed {
		t.Errorf("latency.p95 should not regress under ratio slack: %+v", m)
	}
}

func TestCompare_ErrorRateDeltaSlack(t *testing.T) {
	base := baseResult()
	cand := baseResult()
	cand.ErrorRate = 0.015 // +0.5pp

	got := Compare(base, cand, Options{ErrorRateDelta: 0.01})
	if got.Regressed {
		t.Fatalf("Regressed = true with 1pp slack and 0.5pp rise; notes=%v", got.Notes)
	}
}

func TestCompare_HigherP99AloneIsNotRegression(t *testing.T) {
	base := baseResult()
	cand := baseResult()
	cand.Latency.P99 = 200 * time.Millisecond

	got := Compare(base, cand, Options{})
	if got.Regressed {
		t.Fatalf("p99-only change should be informational, got Regressed; notes=%v", got.Notes)
	}
	if m := metric(got, "latency.p99"); m == nil || m.Delta <= 0 {
		t.Errorf("expected positive p99 delta, got %+v", m)
	}
}

func metric(r Result, name string) *Metric {
	for i := range r.Metrics {
		if r.Metrics[i].Name == name {
			return &r.Metrics[i]
		}
	}
	return nil
}

func withChecks(r *report.Result, checks ...report.CheckResult) *report.Result {
	r.Checks = checks
	return r
}

func chk(name string, pass, fail int64) report.CheckResult {
	return report.CheckResult{Name: name, Passes: pass, Fails: fail}
}

func TestCompare_CheckPassRateDropIsARegression(t *testing.T) {
	base := withChecks(baseResult(), chk("ok", 100, 0), chk("id", 99, 1))
	cand := withChecks(baseResult(), chk("ok", 90, 10), chk("id", 99, 1))
	got := Compare(base, cand, Options{})
	if !got.Regressed {
		t.Fatalf("a check that fell from 100%% to 90%% should regress; notes=%v", got.Notes)
	}
	if len(got.Checks) != 2 || !got.Checks[0].Regressed || got.Checks[1].Regressed {
		t.Errorf("checks = %+v, want only the first regressed", got.Checks)
	}
	if m := metric(got, "check_rate"); m == nil || m.Regressed || m.Candidate >= m.Baseline {
		t.Errorf("overall check_rate = %+v, want a lower rate that is not itself a verdict", m)
	}
}

func TestCompare_CheckSlackAllowsASmallDrop(t *testing.T) {
	base := withChecks(baseResult(), chk("ok", 100, 0))
	cand := withChecks(baseResult(), chk("ok", 98, 2))
	if got := Compare(base, cand, Options{}); !got.Regressed {
		t.Error("strict compare should fail a 2 point drop")
	}
	if got := Compare(base, cand, Options{CheckRateDelta: 0.05}); got.Regressed {
		t.Errorf("a 5 point slack should allow a 2 point drop; notes=%v", got.Notes)
	}
}

func TestCompare_ImprovedChecksAreFine(t *testing.T) {
	base := withChecks(baseResult(), chk("ok", 90, 10))
	cand := withChecks(baseResult(), chk("ok", 100, 0))
	if got := Compare(base, cand, Options{}); got.Regressed {
		t.Errorf("a better pass rate must not regress; notes=%v", got.Notes)
	}
}

func TestCompare_NewAndRemovedChecksAreNotRegressions(t *testing.T) {
	base := withChecks(baseResult(), chk("old", 100, 0), chk("same", 100, 0))
	cand := withChecks(baseResult(), chk("same", 100, 0), chk("fresh", 50, 50))
	got := Compare(base, cand, Options{})
	if got.Regressed {
		t.Fatalf("new or removed checks must not regress; notes=%v", got.Notes)
	}
	if m := metric(got, "check_rate"); m == nil || m.Baseline != 1 || m.Candidate != 1 {
		t.Errorf("the overall rate should cover only the shared check: %+v", m)
	}
	status := map[string]string{}
	for _, c := range got.Checks {
		status[c.Name] = c.Status
	}
	if status["old"] != "removed" || status["fresh"] != "new" || status["same"] != "both" {
		t.Errorf("statuses = %v", status)
	}
}

func TestCompare_NoChecksLeavesTheResultAsBefore(t *testing.T) {
	got := Compare(baseResult(), baseResult(), Options{})
	if len(got.Checks) != 0 || metric(got, "check_rate") != nil {
		t.Errorf("runs without checks should add nothing: %+v", got.Checks)
	}
}

func TestCompare_ChecksOnOneSideOnlyHasNoOverallRate(t *testing.T) {
	got := Compare(baseResult(), withChecks(baseResult(), chk("ok", 1, 0)), Options{})
	if metric(got, "check_rate") != nil {
		t.Error("an overall rate needs checks in both runs")
	}
	if got.Regressed {
		t.Error("a check that is only in the candidate must not regress")
	}
}

func TestCompare_SkipChecksIgnoresThem(t *testing.T) {
	base := withChecks(baseResult(), chk("ok", 100, 0))
	cand := withChecks(baseResult(), chk("ok", 10, 90))
	got := Compare(base, cand, Options{SkipChecks: true})
	if got.Regressed || len(got.Checks) != 0 {
		t.Errorf("SkipChecks should leave checks out: %+v", got)
	}
}

func TestCompare_ARunCountChangeAloneIsNotARegression(t *testing.T) {
	// "ok" holds 100% but ran 100 times, then 10 times; "bad" stays at 0%.
	// A rate weighted by run count would fall from about 91% to about 9%.
	base := withChecks(baseResult(), chk("ok", 100, 0), chk("bad", 0, 10))
	cand := withChecks(baseResult(), chk("ok", 10, 0), chk("bad", 0, 100))
	got := Compare(base, cand, Options{})
	if got.Regressed {
		t.Fatalf("no check's own pass rate fell, so this must not regress; notes=%v", got.Notes)
	}
	if m := metric(got, "check_rate"); m == nil || m.Baseline != 0.5 || m.Candidate != 0.5 {
		t.Errorf("overall rate should be the plain average: %+v", m)
	}
}
