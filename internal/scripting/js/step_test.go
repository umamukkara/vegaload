package js

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
	script, err := Load(writeScript(t, "scenario.js", src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	vu, err := script.NewVU(nil, 5*time.Second)
	if err != nil {
		t.Fatalf("NewVU: %v", err)
	}
	rec := &fakeSteps{}
	vu.SetStepRecorder(rec)
	return rec, vu.Iteration(context.Background())
}

func TestStep_RecordsAndReturnsValue(t *testing.T) {
	rec, err := runSteps(t, `
		export default function () {
			const v = step("login", () => 42);
			if (v !== 42) throw new Error("step should return the function's value");
			step("outer", () => { step("inner", () => 1); });
		}
	`)
	if err != nil {
		t.Fatalf("Iteration: %v", err)
	}
	if got := strings.Join(rec.got, ","); got != "login=ok,inner=ok,outer=ok" {
		t.Errorf("recorded %q", got)
	}
}

func TestStep_ThrowIsRecordedAndRethrown(t *testing.T) {
	rec, err := runSteps(t, `
		export default function () {
			step("pay", () => { throw new Error("card declined"); });
		}
	`)
	if err == nil || !strings.Contains(err.Error(), "card declined") {
		t.Fatalf("err = %v, want the original error", err)
	}
	if got := strings.Join(rec.got, ","); got != "pay=fail" {
		t.Errorf("recorded %q", got)
	}
}

func TestStep_CaughtThrowStillFailsTheStep(t *testing.T) {
	rec, err := runSteps(t, `
		export default function () {
			try { step("pay", () => { throw new Error("x"); }); } catch (e) {}
			step("after", () => 1);
		}
	`)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rec.got, ","); got != "pay=fail,after=ok" {
		t.Errorf("recorded %q", got)
	}
}

func TestStep_BadArguments(t *testing.T) {
	for _, call := range []string{`step("x")`, `step("", () => 1)`, `step(() => 1)`} {
		_, err := runSteps(t, "export default function () { "+call+"; }")
		if err == nil || !strings.Contains(err.Error(), "step: want step(name, fn)") {
			t.Errorf("%s: err = %v", call, err)
		}
	}
}

func TestStep_WithoutRecorderStillWorks(t *testing.T) {
	script, err := Load(writeScript(t, "scenario.js", `export default function () { step("a", () => 1); }`))
	if err != nil {
		t.Fatal(err)
	}
	vu, err := script.NewVU(nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := vu.Iteration(context.Background()); err != nil {
		t.Fatal(err)
	}
}
