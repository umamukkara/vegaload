package python

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

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
	rec := &fakeRecorder{}
	vu.SetCheckRecorder(rec)
	return rec, vu.Iteration(context.Background())
}

func TestCheck_RecordsPassAndFail(t *testing.T) {
	rec, err := runOnce(t, `
def iteration():
    ok = check({"status": 200}, {
        "status is 200": lambda r: r["status"] == 200,
        "status is 201": lambda r: r["status"] == 201,
    })
    assert ok is False, "check should return False when a test fails"
`)
	if err != nil {
		t.Fatalf("Iteration: %v", err)
	}
	if got := strings.Join(rec.got, ","); got != "status is 200=pass,status is 201=fail" {
		t.Errorf("recorded %q", got)
	}
}

func TestCheck_RaisingTestCountsAsFailed_AndRunContinues(t *testing.T) {
	rec, err := runOnce(t, `
def iteration():
    check({}, {"explodes": lambda r: r["missing"]})
    check({}, {"after": lambda r: True})
`)
	if err != nil {
		t.Fatalf("a raising check test must not fail the iteration: %v", err)
	}
	if got := strings.Join(rec.got, ","); got != "explodes=fail,after=pass" {
		t.Errorf("recorded %q", got)
	}
}

func TestCheck_AcceptsBools(t *testing.T) {
	rec, err := runOnce(t, `
def iteration():
    check(None, {"set": True, "clear": False})
`)
	if err != nil {
		t.Fatalf("Iteration: %v", err)
	}
	if got := strings.Join(rec.got, ","); got != "set=pass,clear=fail" {
		t.Errorf("recorded %q", got)
	}
}

func TestCheck_BadArguments(t *testing.T) {
	_, err := runOnce(t, `
def iteration():
    check(1, {"x": "yes"})
`)
	if err == nil || !strings.Contains(err.Error(), "check") {
		t.Errorf("error = %v, want one that mentions check", err)
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

func runWithCtx(t *testing.T, ctx context.Context, src string) *fakeRecorder {
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
	rec := &fakeRecorder{}
	vu.SetCheckRecorder(rec)
	if err := vu.Iteration(ctx); err != nil {
		t.Fatalf("Iteration: %v", err)
	}
	return rec
}

const mixedChecks = `
def iteration():
    def boom(r):
        raise ValueError("late")
    check(1, {
        "returns false": lambda r: False,
        "throws": boom,
        "a plain false": False,
        "passes": lambda r: True,
    })
`

func TestCheck_FailuresAfterDeadlineAreNotCounted_EvenBeforeTheTimerFires(t *testing.T) {
	ctx := laggingCtx{Context: context.Background(), dl: time.Now().Add(-time.Millisecond)}
	rec := runWithCtx(t, ctx, mixedChecks)
	if got := strings.Join(rec.got, ","); got != "passes=pass" {
		t.Errorf("recorded %q, want only the passing check", got)
	}
}

func TestCheck_FailuresBeforeDeadlineAreStillCounted(t *testing.T) {
	ctx := laggingCtx{Context: context.Background(), dl: time.Now().Add(time.Hour)}
	rec := runWithCtx(t, ctx, mixedChecks)
	if len(rec.got) != 4 {
		t.Errorf("recorded %v, want all 4 checks", rec.got)
	}
}
