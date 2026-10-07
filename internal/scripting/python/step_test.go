package python

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeSteps struct {
	mu  sync.Mutex
	got []string
}

func (f *fakeSteps) RecordStep(name string, d time.Duration, failed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state := "ok"
	if failed {
		state = "fail"
	}
	if d < 0 {
		state += "!neg"
	}
	f.got = append(f.got, name+"="+state)
}

func runSteps(t *testing.T, src string) (*fakeSteps, error) {
	t.Helper()
	skipIfNoPython(t)
	script, err := Load(writeScript(t, src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	vu, err := script.NewVU(nil, 5*time.Second)
	if err != nil {
		t.Fatalf("NewVU: %v", err)
	}
	defer vu.Close()
	rec := &fakeSteps{}
	vu.SetStepRecorder(rec)
	return rec, vu.Iteration(context.Background())
}

func TestStep_ContextManagerAndCallable(t *testing.T) {
	rec, err := runSteps(t, `
def iteration():
    with step("login"):
        pass
    v = step("fetch", lambda: 42)
    assert v == 42, "step should return the function's value"
    with step("outer"):
        with step("inner"):
            pass
`)
	if err != nil {
		t.Fatalf("Iteration: %v", err)
	}
	if got := strings.Join(rec.got, ","); got != "login=ok,fetch=ok,inner=ok,outer=ok" {
		t.Errorf("recorded %q", got)
	}
}

func TestStep_RaiseIsRecordedAndPropagates(t *testing.T) {
	rec, err := runSteps(t, `
def iteration():
    with step("pay"):
        raise RuntimeError("card declined")
`)
	if err == nil || !strings.Contains(err.Error(), "card declined") {
		t.Fatalf("err = %v, want the original error", err)
	}
	if got := strings.Join(rec.got, ","); got != "pay=fail" {
		t.Errorf("recorded %q", got)
	}
}

func TestStep_CaughtRaiseStillFailsTheStep(t *testing.T) {
	rec, err := runSteps(t, `
def iteration():
    try:
        with step("pay"):
            raise RuntimeError("x")
    except RuntimeError:
        pass
    with step("after"):
        pass
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rec.got, ","); got != "pay=fail,after=ok" {
		t.Errorf("recorded %q", got)
	}
}

func TestStep_BadArguments(t *testing.T) {
	_, err := runSteps(t, `
def iteration():
    step("")
`)
	if err == nil || !strings.Contains(err.Error(), "step: want step(name)") {
		t.Errorf("err = %v", err)
	}
}
