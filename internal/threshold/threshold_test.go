package threshold

import (
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/report"
)

func TestParse_Valid(t *testing.T) {
	cases := []struct {
		in                      string
		name, metric, op, value string
	}{
		{"p95 < 300ms", "p95 < 300ms", "p95", "<", "300ms"},
		{"p95<300ms", "p95 < 300ms", "p95", "<", "300ms"},
		{"P99 <= 1.5s", "p99 <= 1.5s", "p99", "<=", "1.5s"},
		{"fast-api: p95 < 300ms", "fast-api", "p95", "<", "300ms"},
		{"error_rate < 0.01", "error_rate < 0.01", "error_rate", "<", "0.01"},
		{"error_rate < 1%", "error_rate < 0.01", "error_rate", "<", "0.01"},
		{"error_rate <= 0.5%", "error_rate <= 0.005", "error_rate", "<=", "0.005"},
		{"rps >= 120.5", "rps >= 120.5", "rps", ">=", "120.5"},
		{"failed < 5", "failed < 5", "failed", "<", "5"},
		{"total > 1000", "total > 1000", "total", ">", "1000"},
		{"  mean < 2s  ", "mean < 2s", "mean", "<", "2s"},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.in, err)
			continue
		}
		if got.Name != c.name || got.Metric != c.metric || got.Operator != c.op || got.Value != c.value {
			t.Errorf("Parse(%q) = %+v, want name=%q metric=%q op=%q value=%q", c.in, got, c.name, c.metric, c.op, c.value)
		}
	}
}

func TestParse_Invalid(t *testing.T) {
	cases := []struct{ in, wantErr string }{
		{"", "empty"},
		{"p95", "metric, operator and value"},
		{"p95 = 300ms", "metric, operator and value"},
		{"p95 == 300ms", "metric, operator and value"},
		{"latency < 300ms", "unknown metric"},
		{"p95 <", "missing value"},
		{"p95 < 300", "duration with a unit"},
		{"p95 < -1s", "negative"},
		{"error_rate < 5", "fraction"},
		{"error_rate < 101%", "percentage"},
		{"error_rate < abc", "fraction"},
		{"failed < 1.5", "whole number"},
		{"rps < fast", "number"},
		{": p95 < 1s", "name"},
	}
	for _, c := range cases {
		_, err := Parse(c.in)
		if err == nil {
			t.Errorf("Parse(%q): want an error containing %q, got none", c.in, c.wantErr)
			continue
		}
		if !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("Parse(%q) error = %q, want it to contain %q", c.in, err, c.wantErr)
		}
	}
}

func TestExpression_RoundTrips(t *testing.T) {
	for _, in := range []string{"p95 < 300ms", "fast-api: error_rate <= 0.01", "rps > 50"} {
		a, err := Parse(in)
		if err != nil {
			t.Fatal(err)
		}
		b, err := Parse(a.Expression())
		if err != nil {
			t.Fatalf("Parse(Expression(%q)) = %v", in, err)
		}
		if a != b {
			t.Errorf("round trip of %q changed it: %+v vs %+v", in, a, b)
		}
	}
}

func sampleResult() *report.Result {
	return &report.Result{
		Total:     1000,
		Failed:    20,
		ErrorRate: 0.02,
		Elapsed:   10 * time.Second,
		Latency: report.Latency{
			Min: 5 * time.Millisecond, Mean: 80 * time.Millisecond, Max: 900 * time.Millisecond,
			P50: 60 * time.Millisecond, P90: 150 * time.Millisecond, P95: 300 * time.Millisecond, P99: 600 * time.Millisecond,
		},
	}
}

func TestEvaluate(t *testing.T) {
	cases := []struct {
		expr         string
		wantPass     bool
		wantObserved string
	}{
		{"p95 < 301ms", true, "300ms"},
		{"p95 < 300ms", false, "300ms"}, // equal to the limit: "<" fails
		{"p95 <= 300ms", true, "300ms"}, // equal to the limit: "<=" passes
		{"p99 < 500ms", false, "600ms"},
		{"mean < 100ms", true, "80ms"},
		{"error_rate < 1%", false, "0.02"},
		{"error_rate <= 0.02", true, "0.02"},
		{"rps >= 100", true, "100.00"},
		{"rps > 100", false, "100.00"},
		{"failed < 21", true, "20"},
		{"total >= 1000", true, "1000"},
	}
	for _, c := range cases {
		th, err := Parse(c.expr)
		if err != nil {
			t.Fatal(err)
		}
		got := Evaluate([]Threshold{th}, sampleResult())
		if len(got) != 1 {
			t.Fatalf("%s: got %d results", c.expr, len(got))
		}
		if got[0].Passed != c.wantPass {
			t.Errorf("%s: passed = %v, want %v (observed %s)", c.expr, got[0].Passed, c.wantPass, got[0].Observed)
		}
		if got[0].Observed != c.wantObserved {
			t.Errorf("%s: observed = %q, want %q", c.expr, got[0].Observed, c.wantObserved)
		}
	}
}

func TestEvaluate_NoRequestsFailsEverything(t *testing.T) {
	ths, err := ParseAll([]string{"p95 < 300ms", "error_rate < 1%", "failed < 1"})
	if err != nil {
		t.Fatal(err)
	}
	got := Evaluate(ths, &report.Result{})
	if AllPassed(got) {
		t.Fatal("a run with no completed requests must not pass its thresholds")
	}
	for _, r := range got {
		if r.Passed || r.Observed != "no requests completed" {
			t.Errorf("%s: %+v", r.Name, r)
		}
	}
}

func TestAllPassed(t *testing.T) {
	if !AllPassed(nil) {
		t.Error("no thresholds: nothing to breach, want true")
	}
	if AllPassed([]report.ThresholdResult{{Passed: true}, {Passed: false}}) {
		t.Error("one failure: want false")
	}
}

func TestCheckRate_ParseAndEvaluate(t *testing.T) {
	th, err := Parse("checks: check_rate >= 99%")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if th.Metric != "check_rate" || th.Operator != ">=" {
		t.Fatalf("parsed %+v", th)
	}

	res := &report.Result{Total: 10, Checks: []report.CheckResult{{Name: "a", Passes: 98, Fails: 2}}}
	got := Evaluate([]Threshold{th}, res)
	if got[0].Passed || got[0].Observed != "0.98" {
		t.Errorf("98%% of checks passing should fail a 99%% threshold: %+v", got[0])
	}

	res.Checks[0] = report.CheckResult{Name: "a", Passes: 100}
	if got := Evaluate([]Threshold{th}, res); !got[0].Passed {
		t.Errorf("100%% of checks passing should pass: %+v", got[0])
	}
}

func TestCheckRate_NoChecksFails(t *testing.T) {
	th, _ := Parse("check_rate >= 50%")
	got := Evaluate([]Threshold{th}, &report.Result{Total: 10})
	if got[0].Passed || got[0].Observed != "no checks were made" {
		t.Errorf("a run with no checks must fail check_rate: %+v", got[0])
	}
}

func TestCheckRate_BadValueNamesTheMetric(t *testing.T) {
	_, err := Parse("check_rate >= 150%")
	if err == nil || !strings.Contains(err.Error(), "check_rate") {
		t.Errorf("error = %v, want one that names check_rate", err)
	}
}

func TestCheckRate_ZeroRequestsSaysNoChecks(t *testing.T) {
	ths, err := ParseAll([]string{"check_rate >= 99%"})
	if err != nil {
		t.Fatal(err)
	}
	got := Evaluate(ths, &report.Result{})
	if got[0].Passed || got[0].Observed != "no checks were made" {
		t.Fatalf("got %+v", got[0])
	}
}
