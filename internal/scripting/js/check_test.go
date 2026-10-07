package js

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRecorder records every check() outcome, in order.
type fakeRecorder struct {
	mu  sync.Mutex
	got []string
}

func (f *fakeRecorder) RecordCheck(name string, passed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state := "fail"
	if passed {
		state = "pass"
	}
	f.got = append(f.got, name+"="+state)
}

func runOnce(t *testing.T, src string) (*fakeRecorder, error) {
	t.Helper()
	script, err := Load(writeScript(t, "scenario.js", src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	vu, err := script.NewVU(nil, 5*time.Second)
	if err != nil {
		t.Fatalf("NewVU: %v", err)
	}
	rec := &fakeRecorder{}
	vu.SetCheckRecorder(rec)
	return rec, vu.Iteration(context.Background())
}

func TestCheck_RecordsPassAndFail(t *testing.T) {
	rec, err := runOnce(t, `
		export default function () {
			const ok = check({status: 200}, {
				"status is 200": (r) => r.status === 200,
				"status is 201": (r) => r.status === 201,
			});
			if (ok !== false) throw new Error("check should return false when a test fails");
		}
	`)
	if err != nil {
		t.Fatalf("Iteration: %v", err)
	}
	want := "status is 200=pass,status is 201=fail"
	if got := strings.Join(rec.got, ","); got != want {
		t.Errorf("recorded %q, want %q", got, want)
	}
}

func TestCheck_AllPassReturnsTrue(t *testing.T) {
	_, err := runOnce(t, `
		export default function () {
			if (check(1, {"one": (v) => v === 1}) !== true) throw new Error("want true");
		}
	`)
	if err != nil {
		t.Fatalf("Iteration: %v", err)
	}
}

func TestCheck_ThrowingTestCountsAsFailed_AndRunContinues(t *testing.T) {
	rec, err := runOnce(t, `
		export default function () {
			check({}, {"explodes": (r) => r.missing.field});
			check({}, {"after": () => true});
		}
	`)
	if err != nil {
		t.Fatalf("a throwing check test must not fail the iteration: %v", err)
	}
	if got := strings.Join(rec.got, ","); got != "explodes=fail,after=pass" {
		t.Errorf("recorded %q", got)
	}
}

func TestCheck_AcceptsBooleans(t *testing.T) {
	rec, err := runOnce(t, `
		export default function () {
			check(null, {"flag is set": true, "flag is clear": false});
		}
	`)
	if err != nil {
		t.Fatalf("Iteration: %v", err)
	}
	if got := strings.Join(rec.got, ","); got != "flag is set=pass,flag is clear=fail" {
		t.Errorf("recorded %q", got)
	}
}

func TestCheck_BadArguments(t *testing.T) {
	for name, body := range map[string]string{
		"no tests":         `check(1)`,
		"test is a string": `check(1, {"x": "yes"})`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runOnce(t, "export default function () { "+body+" }")
			if err == nil || !strings.Contains(err.Error(), "check") {
				t.Errorf("error = %v, want one that mentions check", err)
			}
		})
	}
}

func TestCheck_WithoutRecorderStillWorks(t *testing.T) {
	script, err := Load(writeScript(t, "scenario.js", `
		export default function () { check(1, {"a": () => true}); }
	`))
	if err != nil {
		t.Fatal(err)
	}
	vu, err := script.NewVU(nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := vu.Iteration(context.Background()); err != nil {
		t.Fatalf("Iteration with no recorder: %v", err)
	}
}

// A test that the end of the run interrupts is cut off, not failed. It
// must not be counted, or a slow machine shows a false failed check.
func TestCheck_InterruptedByRunEnd_IsNotCounted(t *testing.T) {
	script, err := Load(writeScript(t, "scenario.js", `
		export default function () {
			check(1, {"spins": () => { while (true) {} }});
		}
	`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	vu, err := script.NewVU(nil, 5*time.Second)
	if err != nil {
		t.Fatalf("NewVU: %v", err)
	}
	rec := &fakeRecorder{}
	vu.SetCheckRecorder(rec)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := vu.Iteration(ctx); err == nil {
		t.Fatal("the iteration should end with an error when the run ends")
	}
	if len(rec.got) != 0 {
		t.Errorf("recorded %v, want nothing for an interrupted check", rec.got)
	}
}

// laggingCtx is a context whose deadline has passed but whose timer has
// not fired yet: Err is nil and Done never closes.
type laggingCtx struct {
	context.Context
	dl time.Time
}

func (c laggingCtx) Deadline() (time.Time, bool) { return c.dl, true }
func (c laggingCtx) Done() <-chan struct{}       { return nil }
func (c laggingCtx) Err() error                  { return nil }

const mixedChecks = `
	export default function () {
		check(1, {
			"returns false": () => false,
			"throws": () => { throw new Error("late"); },
			"a plain false": false,
			"passes": () => true,
		});
	}
`

// The first test fails after the deadline, so the run stops there. A test
// that passed before it stays counted. Tests after it are not run.
const orderedChecks = `
	export default function () {
		check(1, {
			"passes first": () => true,
			"fails late": () => false,
			"never counted": () => true,
		});
	}
`

func TestCheck_FailuresAfterDeadlineAreNotCounted_EvenBeforeTheTimerFires(t *testing.T) {
	script, err := Load(writeScript(t, "scenario.js", orderedChecks))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	vu, err := script.NewVU(nil, 5*time.Second)
	if err != nil {
		t.Fatalf("NewVU: %v", err)
	}
	rec := &fakeRecorder{}
	vu.SetCheckRecorder(rec)
	ctx := laggingCtx{Context: context.Background(), dl: time.Now().Add(-time.Millisecond)}
	if err := vu.Iteration(ctx); err != nil {
		t.Fatalf("Iteration: %v", err)
	}
	if len(rec.got) != 1 || rec.got[0] != "passes first=pass" {
		t.Errorf("recorded %v, want only the test that passed before the cut-off", rec.got)
	}
}

func TestCheck_FailuresBeforeDeadlineAreStillCounted(t *testing.T) {
	script, err := Load(writeScript(t, "scenario.js", mixedChecks))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	vu, err := script.NewVU(nil, 5*time.Second)
	if err != nil {
		t.Fatalf("NewVU: %v", err)
	}
	rec := &fakeRecorder{}
	vu.SetCheckRecorder(rec)
	ctx := laggingCtx{Context: context.Background(), dl: time.Now().Add(time.Hour)}
	if err := vu.Iteration(ctx); err != nil {
		t.Fatalf("Iteration: %v", err)
	}
	if len(rec.got) != 4 {
		t.Errorf("recorded %v, want all 4 checks", rec.got)
	}
}
