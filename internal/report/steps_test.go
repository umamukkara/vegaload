package report

import (
	"strings"
	"testing"
	"time"
)

func TestCollector_RecordStep_TotalsAndPercentiles(t *testing.T) {
	c := NewCollector()
	for i := 1; i <= 100; i++ {
		c.RecordStep("login", time.Duration(i)*time.Millisecond, i > 90)
	}
	c.RecordStep("checkout", 5*time.Millisecond, false)

	res := c.Finish("fixed-vus", time.Second)
	if len(res.Steps) != 2 || res.Steps[0].Name != "login" || res.Steps[1].Name != "checkout" {
		t.Fatalf("steps = %+v, want login then checkout", res.Steps)
	}
	l := res.Steps[0]
	if l.Total != 100 || l.Failed != 10 || l.ErrorRate != 0.1 {
		t.Errorf("login = %+v", l)
	}
	if l.Latency.Min != time.Millisecond || l.Latency.Max != 100*time.Millisecond {
		t.Errorf("login min/max = %v/%v", l.Latency.Min, l.Latency.Max)
	}
	if l.Latency.P95 < 94*time.Millisecond || l.Latency.P95 > 96*time.Millisecond {
		t.Errorf("login p95 = %v, want about 95ms", l.Latency.P95)
	}
	if s, ok := res.Step("checkout"); !ok || s.Total != 1 {
		t.Errorf("Step(checkout) = %+v, %v", s, ok)
	}
	if _, ok := res.Step("nope"); ok {
		t.Error("Step(nope) should not exist")
	}
}

func TestCollector_NoSteps_OmitsThem(t *testing.T) {
	res := NewCollector().Finish("fixed-vus", time.Second)
	if res.Steps != nil {
		t.Errorf("steps = %+v, want nil", res.Steps)
	}
}

func TestCollector_RecordStep_CapsNames(t *testing.T) {
	c := NewCollector()
	for i := 0; i < MaxStepNames+20; i++ {
		c.RecordStep("s"+strings.Repeat("x", i), time.Millisecond, false)
	}
	res := c.Finish("fixed-vus", time.Second)
	if len(res.Steps) != MaxStepNames+1 {
		t.Fatalf("got %d steps, want %d", len(res.Steps), MaxStepNames+1)
	}
	last := res.Steps[len(res.Steps)-1]
	if last.Name != OtherStepsName || last.Total != 20 {
		t.Errorf("last = %+v, want %s with 20 runs", last, OtherStepsName)
	}
}

func TestCollector_RecordStep_NegativeDurationIsZero(t *testing.T) {
	c := NewCollector()
	c.RecordStep("a", -time.Second, false)
	if got := c.Finish("x", time.Second).Steps[0].Latency.Max; got != 0 {
		t.Errorf("max = %v, want 0", got)
	}
}

func TestStepsOutputs(t *testing.T) {
	c := NewCollector()
	c.RecordStep("login", 20*time.Millisecond, false)
	c.RecordStep("login", 40*time.Millisecond, true)
	res := c.Finish("fixed-vus", time.Second)

	md := MarkdownSummary(res)
	if !strings.Contains(md, "### Steps") || !strings.Contains(md, "| login | 2 | 1 |") {
		t.Errorf("markdown missing steps table:\n%s", md)
	}
	if h := renderHTML(res); !strings.Contains(h, "<h2>Steps</h2>") || !strings.Contains(h, "login") {
		t.Errorf("html missing steps section")
	}
}
