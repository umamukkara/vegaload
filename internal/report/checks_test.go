package report

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCollector_RecordCheck_CountsPerName(t *testing.T) {
	c := NewCollector()
	c.RecordCheck("status is 200", true)
	c.RecordCheck("status is 200", true)
	c.RecordCheck("status is 200", false)
	c.RecordCheck("has id", true)

	res := c.Finish("fixed-vus", time.Second)
	if len(res.Checks) != 2 {
		t.Fatalf("got %d checks, want 2: %+v", len(res.Checks), res.Checks)
	}
	// First-seen order is kept.
	if res.Checks[0].Name != "status is 200" || res.Checks[0].Passes != 2 || res.Checks[0].Fails != 1 {
		t.Errorf("first check = %+v, want status is 200 with 2 passes and 1 fail", res.Checks[0])
	}
	if res.Checks[1].Name != "has id" || res.Checks[1].Passes != 1 || res.Checks[1].Fails != 0 {
		t.Errorf("second check = %+v", res.Checks[1])
	}
}

func TestCollector_RecordCheck_Concurrent(t *testing.T) {
	c := NewCollector()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				c.RecordCheck("ok", j%2 == 0)
			}
		}()
	}
	wg.Wait()
	res := c.Finish("fixed-vus", time.Second)
	if got := res.Checks[0].Passes + res.Checks[0].Fails; got != 5000 {
		t.Errorf("recorded %d checks, want 5000", got)
	}
}

func TestResult_CheckRate(t *testing.T) {
	res := &Result{}
	if _, any := res.CheckRate(); any {
		t.Error("a result with no checks must report that there is no rate")
	}
	res.Checks = []CheckResult{{Name: "a", Passes: 3, Fails: 1}, {Name: "b", Passes: 4, Fails: 0}}
	rate, any := res.CheckRate()
	if !any || rate != 7.0/8.0 {
		t.Errorf("CheckRate = %v, %v; want 0.875, true", rate, any)
	}
}

func TestNoChecks_JSONUnchanged(t *testing.T) {
	res := NewCollector().Finish("fixed-vus", time.Second)
	if res.Checks != nil {
		t.Errorf("Checks = %v, want nil when the scenario made none", res.Checks)
	}
}

func TestChecksSection_ShowsEachCheck(t *testing.T) {
	res := &Result{Checks: []CheckResult{
		{Name: "status <200>", Passes: 9, Fails: 1},
		{Name: "has id", Passes: 10},
	}}
	out := checksSection(res)
	for _, want := range []string{"Checks", "95.00% passed", "FAIL", "PASS", "status &lt;200&gt;", "has id"} {
		if !strings.Contains(out, want) {
			t.Errorf("checks section is missing %q:\n%s", want, out)
		}
	}
	if checksSection(&Result{}) != "" {
		t.Error("a run with no checks must render no checks section")
	}
}

func TestCollector_RecordCheck_CapsDistinctNames(t *testing.T) {
	c := NewCollector()
	for i := 0; i < MaxCheckNames+50; i++ {
		c.RecordCheck(fmt.Sprintf("id %d", i), i%2 == 0)
	}
	got := c.snapshotChecks()
	if len(got) != MaxCheckNames+1 {
		t.Fatalf("got %d check rows, want %d", len(got), MaxCheckNames+1)
	}
	other := got[len(got)-1]
	if other.Name != OtherChecksName || other.Passes+other.Fails != 50 {
		t.Fatalf("overflow bucket = %+v", other)
	}
	var total int64
	for _, r := range got {
		total += r.Passes + r.Fails
	}
	if total != int64(MaxCheckNames+50) {
		t.Fatalf("total checks %d: counts must not be lost", total)
	}
}

func TestBaselineSection_ShowsVerdictAndEscapesPath(t *testing.T) {
	res := &Result{Baseline: &BaselineResult{
		Path: "<b>.json", MaxRegressionPercent: 10, Passed: false,
		Metrics: []BaselineMetric{{Name: "latency.p95", Baseline: 1e8, Candidate: 2e8, Unit: "ns", Regressed: true}},
		Notes:   []string{"no requests <completed>"},
	}}
	got := baselineSection(res)
	for _, want := range []string{"Worse than the baseline", "FAIL", "latency.p95", "&lt;b&gt;.json", "100ms", "200ms", "no requests &lt;completed&gt;"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if baselineSection(&Result{}) != "" {
		t.Error("no baseline should render nothing")
	}
}
