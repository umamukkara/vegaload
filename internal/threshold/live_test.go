package threshold

import (
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/report"
)

func mustParse(t *testing.T, s string) Threshold {
	t.Helper()
	th, err := Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return th
}

func TestLive_Kinds(t *testing.T) {
	cases := map[string]LiveKind{
		"failed < 5":           LiveSticky,
		"failed <= 5":          LiveSticky,
		"max < 1s":             LiveSticky,
		"total < 100":          LiveSticky,
		"min > 1ms":            LiveSticky,
		"failed > 5":           LiveNone,
		"total >= 1000":        LiveNone,
		"min < 1s":             LiveNone,
		"rps >= 100":           LiveNone,
		"p95 < 300ms":          LiveSustained,
		"error_rate < 1%":      LiveSustained,
		"check_rate >= 99%":    LiveSustained,
		`p99{step="x"} < 1s`:   LiveSustained,
		`failed{step="x"} < 1`: LiveSticky,
		"mean < 1s":            LiveSustained,
	}
	for expr, want := range cases {
		if got := mustParse(t, expr).Live(); got != want {
			t.Errorf("%s: kind = %d, want %d", expr, got, want)
		}
	}
}

func TestLiveBreach_NeedsEvidence(t *testing.T) {
	slow := report.Latency{P95: time.Second}
	few := &report.Result{Total: 5, Latency: slow}
	enough := &report.Result{Total: 50, Latency: slow}

	p95 := mustParse(t, "p95 < 300ms")
	if _, b := p95.LiveBreach(few); b {
		t.Error("5 requests is too few to judge p95")
	}
	if obs, b := p95.LiveBreach(enough); !b || obs != "1s" {
		t.Errorf("50 slow requests: breached=%v observed=%q", b, obs)
	}

	failed := mustParse(t, "failed < 2")
	if _, b := failed.LiveBreach(&report.Result{Total: 3, Failed: 2}); !b {
		t.Error("two failures break failed < 2, even with few requests")
	}
	if _, b := failed.LiveBreach(&report.Result{Total: 3, Failed: 1}); b {
		t.Error("one failure does not break failed < 2")
	}

	// A step that has not run yet, or has too few runs, is not a breach.
	st := mustParse(t, `p95{step="login"} < 300ms`)
	if _, b := st.LiveBreach(enough); b {
		t.Error("a step that never ran is settled at the end, not live")
	}
	withStep := &report.Result{Total: 50, Steps: []report.StepResult{{Name: "login", Total: 30, Latency: slow}}}
	if _, b := st.LiveBreach(withStep); !b {
		t.Error("30 slow runs of the step should breach")
	}

	// No checks yet is not a breach.
	cr := mustParse(t, "check_rate >= 99%")
	if _, b := cr.LiveBreach(enough); b {
		t.Error("no checks yet is not a breach")
	}
	checks := &report.Result{Total: 50, Checks: []report.CheckResult{{Name: "a", Passes: 10, Fails: 20}}}
	if _, b := cr.LiveBreach(checks); !b {
		t.Error("a low check rate with 30 checks should breach")
	}

	// A threshold that is not live is never a live breach.
	if _, b := mustParse(t, "rps >= 1000000").LiveBreach(enough); b {
		t.Error("rps is judged at the end")
	}
}

func TestNotLive(t *testing.T) {
	ts := []Threshold{mustParse(t, "p95 < 1s"), mustParse(t, "rps >= 10"), mustParse(t, "total >= 5")}
	got := NotLive(ts)
	if len(got) != 2 || got[0].Metric != "rps" || got[1].Metric != "total" {
		t.Errorf("NotLive = %+v", got)
	}
}
