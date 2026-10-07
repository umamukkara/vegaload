package compare

import (
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/report"
)

func res(total int64, errRate float64, p95 time.Duration) *report.Result {
	return &report.Result{Total: total, ErrorRate: errRate, Elapsed: time.Second, Latency: report.Latency{P95: p95}}
}

func TestGate_P95WithinAndOverTheLimit(t *testing.T) {
	base := res(100, 0, 100*time.Millisecond)
	if g := Gate(base, res(100, 0, 110*time.Millisecond), "b.json", 10); !g.Passed {
		t.Errorf("10%% worse at a 10%% limit should pass: %+v", g)
	}
	g := Gate(base, res(100, 0, 111*time.Millisecond), "b.json", 10)
	if g.Passed || !strings.Contains(strings.Join(g.Notes, " "), "p95") {
		t.Errorf("11%% worse at a 10%% limit should fail on p95: %+v", g)
	}
}

func TestGate_ErrorRateIsRelativeToTheBaseline(t *testing.T) {
	base := res(100, 0.02, time.Millisecond)
	if g := Gate(base, res(100, 0.022, time.Millisecond), "b", 10); !g.Passed {
		t.Errorf("2.0%% -> 2.2%% at a 10%% limit should pass: %+v", g)
	}
	if g := Gate(base, res(100, 0.03, time.Millisecond), "b", 10); g.Passed {
		t.Errorf("2.0%% -> 3.0%% at a 10%% limit should fail: %+v", g)
	}
}

func TestGate_ZeroErrorBaselineAllowsNoErrors(t *testing.T) {
	base := res(100, 0, time.Millisecond)
	if g := Gate(base, res(100, 0.001, time.Millisecond), "b", 50); g.Passed {
		t.Errorf("any error against an error-free baseline should fail: %+v", g)
	}
}

func TestGate_ZeroPercentIsStrict(t *testing.T) {
	base := res(100, 0, 100*time.Millisecond)
	if g := Gate(base, res(100, 0, 100*time.Millisecond), "b", 0); !g.Passed {
		t.Errorf("identical run at 0%% should pass: %+v", g)
	}
	if g := Gate(base, res(100, 0, 101*time.Millisecond), "b", 0); g.Passed {
		t.Errorf("any p95 increase at 0%% should fail: %+v", g)
	}
}

func TestGate_NoRequestsFails(t *testing.T) {
	g := Gate(res(100, 0, time.Millisecond), &report.Result{}, "b", 10)
	if g.Passed || !strings.Contains(strings.Join(g.Notes, " "), "no requests") {
		t.Errorf("an empty run must not pass: %+v", g)
	}
}

func TestGate_AgreesWithCompare(t *testing.T) {
	base, cand := res(100, 0.02, 100*time.Millisecond), res(100, 0.05, 90*time.Millisecond)
	cmp := Compare(base, cand, GateOptions(base, 20))
	if g := Gate(base, cand, "b", 20); g.Passed == cmp.Regressed {
		t.Errorf("Gate and Compare disagree: gate %+v, compare %+v", g, cmp)
	}
	if len(Gate(base, cand, "b", 20).Metrics) != 2 {
		t.Errorf("want only the two gated metrics")
	}
}

func TestGate_IgnoresChecks(t *testing.T) {
	base := baseResult()
	base.Checks = []report.CheckResult{{Name: "ok", Passes: 100}}
	cand := baseResult()
	cand.Checks = []report.CheckResult{{Name: "ok", Passes: 10, Fails: 90}}
	if g := Gate(base, cand, "b.json", 10); !g.Passed {
		t.Errorf("-baseline judges p95 and the error rate, not checks; notes=%v", g.Notes)
	}
}
