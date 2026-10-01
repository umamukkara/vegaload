package eval

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestParseSuite(t *testing.T) {
	data := []byte(`{"version":"v1","cases":[{"name":"a","tool":"t","arguments":{"x":1}}]}`)
	s, err := ParseSuite(data)
	if err != nil {
		t.Fatalf("ParseSuite: %v", err)
	}
	if s.Version != "v1" {
		t.Errorf("Version = %q, want v1", s.Version)
	}
	if len(s.Cases) != 1 || s.Cases[0].Name != "a" || s.Cases[0].Tool != "t" {
		t.Errorf("unexpected cases: %+v", s.Cases)
	}
}

func TestParseSuite_InvalidJSON(t *testing.T) {
	if _, err := ParseSuite([]byte("not json")); err == nil {
		t.Error("expected an error for invalid JSON")
	}
}

func TestRawV1_EmbedsRealSuite(t *testing.T) {
	// RawV1 should at least parse as a Suite with a non-empty case list,
	// even before any "${WORKDIR}" substitution — it's just JSON text at
	// that point.
	s, err := ParseSuite(RawV1())
	if err != nil {
		t.Fatalf("parsing the embedded v1 suite: %v", err)
	}
	if s.Version != "v1" {
		t.Errorf("Version = %q, want v1", s.Version)
	}
	if len(s.Cases) == 0 {
		t.Error("expected at least one case in the embedded v1 suite")
	}
}

func TestRun_ExpectError(t *testing.T) {
	cases := []Case{
		{Name: "wants an error, gets one", Tool: "t", ExpectError: true},
		{Name: "wants an error, gets none", Tool: "t", ExpectError: true},
	}
	call := func(ctx context.Context, tool string, args json.RawMessage) (any, error) {
		if tool != "t" {
			t.Fatalf("unexpected tool %q", tool)
		}
		return nil, nil
	}
	calls := 0
	wrapped := func(ctx context.Context, tool string, args json.RawMessage) (any, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("boom")
		}
		return call(ctx, tool, args)
	}
	results := Run(context.Background(), wrapped, cases)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if !results[0].Passed {
		t.Errorf("case 0: expected Passed, got %+v", results[0])
	}
	if results[1].Passed {
		t.Errorf("case 1: expected failure (no error returned), got %+v", results[1])
	}
}

func TestRun_UnexpectedError(t *testing.T) {
	cases := []Case{{Name: "unexpected failure", Tool: "t"}}
	call := func(ctx context.Context, tool string, args json.RawMessage) (any, error) {
		return nil, errors.New("boom")
	}
	results := Run(context.Background(), call, cases)
	if results[0].Passed {
		t.Error("expected failure when the call returns an unexpected error")
	}
	if results[0].Detail == "" {
		t.Error("expected a non-empty Detail explaining the failure")
	}
}

func TestRun_ExpectContains(t *testing.T) {
	call := func(ctx context.Context, tool string, args json.RawMessage) (any, error) {
		return map[string]any{"total": 100, "failed": 0, "extra": "ignored"}, nil
	}
	cases := []Case{
		{Name: "matches", Tool: "t", ExpectContains: map[string]any{"total": 100, "failed": 0}},
		{Name: "mismatches", Tool: "t", ExpectContains: map[string]any{"total": 200}},
		{Name: "missing field", Tool: "t", ExpectContains: map[string]any{"nope": 1}},
	}
	results := Run(context.Background(), call, cases)
	if !results[0].Passed {
		t.Errorf("case 0: expected Passed, got %+v", results[0])
	}
	if results[1].Passed {
		t.Errorf("case 1: expected mismatch failure, got %+v", results[1])
	}
	if results[2].Passed {
		t.Errorf("case 2: expected missing-field failure, got %+v", results[2])
	}
}

func TestCheckContains_EmptyWantAlwaysPasses(t *testing.T) {
	ok, detail := checkContains("anything, even a non-object", nil)
	if !ok || detail != "" {
		t.Errorf("checkContains with no expectations = (%v, %q), want (true, \"\")", ok, detail)
	}
}

func TestCheckContains_NonObjectResult(t *testing.T) {
	ok, detail := checkContains([]int{1, 2, 3}, map[string]any{"x": 1})
	if ok {
		t.Error("expected failure for a non-object result with expectations")
	}
	if detail == "" {
		t.Error("expected a non-empty Detail")
	}
}
