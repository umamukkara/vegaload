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
