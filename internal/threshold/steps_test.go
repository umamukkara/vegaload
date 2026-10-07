package threshold

import (
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/report"
)

func stepResult() *report.Result {
	return &report.Result{
		Total: 10,
		Steps: []report.StepResult{
			{Name: "login", Total: 5, Failed: 1, ErrorRate: 0.2, Latency: report.Latency{P95: 250 * time.Millisecond, Mean: 100 * time.Millisecond}},
			{Name: "a:b", Total: 5, Latency: report.Latency{P95: 10 * time.Millisecond}},
		},
	}
}

func TestParse_StepSelector(t *testing.T) {
	cases := map[string]string{
		`p95{step="login"} < 300ms`:      "login",
		`p95{step='login'} < 300ms`:      "login",
		`p95{step=login} < 300ms`:        "login",
		`fast: p95{step="a:b"} < 300ms`:  "a:b",
		`error_rate{step="login"} < 1%`:  "login",
		`p95 { step = "login" } < 300ms`: "login",
	}
	for in, step := range cases {
		th, err := Parse(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if th.Step != step {
			t.Errorf("%s: step = %q, want %q", in, th.Step, step)
		}
	}
}

func TestParse_StepSelectorInvalid(t *testing.T) {
	for _, in := range []string{
		`p95{step=""} < 1s`,
		`p95{route="x"} < 1s`,
		`check_rate{step="x"} > 0.9`,
		`p95{step="a"b"} < 1s`,
		`p95{step="x" < 1s`,
	} {
		if _, err := Parse(in); err == nil {
			t.Errorf("%s: want an error", in)
		}
	}
}

func TestExpression_StepRoundTrips(t *testing.T) {
	th, err := Parse(`p95{step="login"} < 300ms`)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(th.Expression())
	if err != nil || again.Step != "login" {
		t.Fatalf("round trip of %q: %+v, %v", th.Expression(), again, err)
	}
}

func TestEvaluate_Step(t *testing.T) {
	cases := []struct {
		expr string
		pass bool
		obs  string
	}{
		{`p95{step="login"} < 300ms`, true, "250ms"},
		{`p95{step="login"} < 200ms`, false, "250ms"},
		{`error_rate{step="login"} < 10%`, false, "0.2"},
		{`failed{step="login"} <= 1`, true, "1"},
		{`total{step="a:b"} >= 5`, true, "5"},
		{`p95{step="ghost"} < 1s`, false, `no step named "ghost" ran`},
	}
	for _, c := range cases {
		th, err := Parse(c.expr)
		if err != nil {
			t.Fatal(err)
		}
		got := Evaluate([]Threshold{th}, stepResult())
		if got[0].Passed != c.pass || got[0].Observed != c.obs {
			t.Errorf("%s: got passed=%v observed=%q, want %v %q", c.expr, got[0].Passed, got[0].Observed, c.pass, c.obs)
		}
	}
}

func TestParseFile_Step(t *testing.T) {
	ts, err := ParseFile([]byte(`[{"metric":"p95","step":"login","operator":"<","value":"300ms"}]`))
	if err != nil || len(ts) != 1 || ts[0].Step != "login" {
		t.Fatalf("got %+v, %v", ts, err)
	}
	if !strings.Contains(ts[0].Expression(), `step="login"`) {
		t.Errorf("expression %q", ts[0].Expression())
	}
}
