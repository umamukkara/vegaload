package report

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleResult() *Result {
	no := false
	return &Result{
		Executor: "fixed-vus", Elapsed: 2 * time.Second, Total: 100, Failed: 2, ErrorRate: 0.02,
		Latency: Latency{P50: time.Millisecond, P95: 20 * time.Millisecond, P99: 30 * time.Millisecond},
		Thresholds: []ThresholdResult{
			{Name: "p95 < 300ms", Metric: "p95", Operator: "<", Value: "300ms", Observed: "20ms", Passed: true},
			{Name: "fast <&>", Metric: "p50", Operator: "<", Value: "1us", Observed: "1ms", Passed: false},
		},
		ThresholdsPassed: &no,
		Checks: []CheckResult{
			{Name: "status is 200", Passes: 98, Fails: 2},
			{Name: "has id", Passes: 100, Fails: 0},
		},
		Baseline: &BaselineResult{Path: "base.json", MaxRegressionPercent: 10, Passed: false, Notes: []string{"p95 rose"}},
	}
}

func TestJUnitXML_OneCasePerThresholdCheckAndBaseline(t *testing.T) {
	data, err := JUnitXML(sampleResult())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "<?xml") {
		t.Errorf("missing XML header: %.40s", data)
	}
	var got junitSuites
	if err := xml.Unmarshal(data, &got); err != nil {
		t.Fatalf("not valid XML: %v\n%s", err, data)
	}
	if got.Tests != 5 || got.Failures != 3 || len(got.Suites) != 1 {
		t.Fatalf("tests=%d failures=%d suites=%d", got.Tests, got.Failures, len(got.Suites))
	}
	s := got.Suites[0]
	if s.Tests != 5 || s.Failures != 3 || s.Time != "2.000" {
		t.Errorf("suite = %+v", s)
	}
	want := map[string]bool{ // name -> failed
		"p95 < 300ms": false, "fast <&>": true, "status is 200": true, "has id": false, "within baseline base.json": true,
	}
	for _, c := range s.Cases {
		failed, ok := want[c.Name]
		if !ok {
			t.Errorf("unexpected case %q", c.Name)
			continue
		}
		if (c.Failure != nil) != failed {
			t.Errorf("case %q: failure=%v, want failed=%v", c.Name, c.Failure, failed)
		}
		delete(want, c.Name)
	}
	if len(want) != 0 {
		t.Errorf("missing cases: %v", want)
	}
}

func TestJUnitXML_FailureTextSaysWhatHappened(t *testing.T) {
	data, _ := JUnitXML(sampleResult())
	for _, want := range []string{"observed 1ms", "2 of 100 failed (98.00% passed)", "worse than the baseline by more than 10%", "p95 rose",
		`classname="vegaload.thresholds"`, `classname="vegaload.checks"`, `classname="vegaload.baseline"`, "&lt;&amp;&gt;"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("missing %q in\n%s", want, data)
		}
	}
}

func TestJUnitXML_NoGatesIsAnEmptySuite(t *testing.T) {
	data, err := JUnitXML(&Result{Executor: "fixed-vus"})
	if err != nil {
		t.Fatal(err)
	}
	var got junitSuites
	if err := xml.Unmarshal(data, &got); err != nil || got.Tests != 0 || got.Failures != 0 {
		t.Fatalf("err=%v got=%+v", err, got)
	}
}

func TestWriteJUnit_WritesAFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "junit.xml")
	if err := WriteJUnit(p, sampleResult()); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(p); err != nil || !strings.Contains(string(data), "<testsuites") {
		t.Fatalf("err=%v data=%s", err, data)
	}
}
