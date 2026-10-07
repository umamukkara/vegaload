package eval

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestRawV2_KeepsEveryV1CaseUnchanged(t *testing.T) {
	v1, err := ParseSuite(RawV1())
	if err != nil {
		t.Fatal(err)
	}
	v2, err := ParseSuite(RawV2())
	if err != nil {
		t.Fatalf("parsing the embedded v2 suite: %v", err)
	}
	if v2.Version != "v2" {
		t.Errorf("Version = %q, want v2", v2.Version)
	}
	if len(v2.Cases) <= len(v1.Cases) {
		t.Fatalf("v2 has %d cases, v1 has %d: v2 should add cases", len(v2.Cases), len(v1.Cases))
	}
	for i, c := range v1.Cases {
		a, _ := json.Marshal(c)
		b, _ := json.Marshal(v2.Cases[i])
		if string(a) != string(b) {
			t.Errorf("v2 case %d differs from v1:\n v1 %s\n v2 %s", i, a, b)
		}
	}
}

func TestRaw_ByName(t *testing.T) {
	for _, name := range []string{"v1", "v2"} {
		if b, ok := Raw(name); !ok || len(b) == 0 {
			t.Errorf("Raw(%q) = (%d bytes, %v)", name, len(b), ok)
		}
	}
	if _, ok := Raw("v9"); ok {
		t.Error("Raw(\"v9\") should not exist")
	}
}

func TestRun_ExpectErrorContains(t *testing.T) {
	call := func(ctx context.Context, tool string, args json.RawMessage) (any, error) {
		return nil, errors.New("report_path is required")
	}
	results := Run(context.Background(), call, []Case{
		{Name: "match", Tool: "t", ExpectErrorContains: "report_path"},
		{Name: "no match", Tool: "t", ExpectErrorContains: "scenario_path"},
	})
	if !results[0].Passed {
		t.Errorf("match: %+v", results[0])
	}
	if results[1].Passed {
		t.Errorf("no match should fail: %+v", results[1])
	}
	ok := func(ctx context.Context, tool string, args json.RawMessage) (any, error) {
		return map[string]any{}, nil
	}
	if r := Run(context.Background(), ok, []Case{{Name: "x", Tool: "t", ExpectErrorContains: "a"}})[0]; r.Passed {
		t.Errorf("a call that succeeded must fail an expected error: %+v", r)
	}
}

func TestRun_ExpectFiles(t *testing.T) {
	dir := t.TempDir()
	have := filepath.Join(dir, "have.xml")
	if err := os.WriteFile(have, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	call := func(ctx context.Context, tool string, args json.RawMessage) (any, error) {
		return map[string]any{}, nil
	}
	results := Run(context.Background(), call, []Case{
		{Name: "present", Tool: "t", ExpectFiles: []string{have}},
		{Name: "absent", Tool: "t", ExpectFiles: []string{filepath.Join(dir, "none.xml")}},
	})
	if !results[0].Passed {
		t.Errorf("present: %+v", results[0])
	}
	if results[1].Passed || !strings.Contains(results[1].Detail, "none.xml") {
		t.Errorf("absent should fail and name the file: %+v", results[1])
	}
}

func TestCheckContains_DottedPaths(t *testing.T) {
	out := map[string]any{
		"result": map[string]any{"baseline": map[string]any{"passed": false}},
		"checks": []any{map[string]any{"name": "a", "passes": 1}, map[string]any{"name": "b"}},
		"a.b":    "literal",
	}
	ok, detail := checkContains(out, map[string]any{
		"result.baseline.passed": false, "checks.0.name": "a", "checks.1.name": "b", "a.b": "literal",
	})
	if !ok {
		t.Fatalf("dotted paths should match: %s", detail)
	}
	for _, key := range []string{"result.nope", "checks.5.name", "checks.x", "result.baseline.passed.deeper"} {
		if ok, _ := checkContains(out, map[string]any{key: 1}); ok {
			t.Errorf("path %q should not match", key)
		}
	}
	if ok, _ := checkContains(out, map[string]any{"result.baseline.passed": true}); ok {
		t.Error("a wrong value at a dotted path should fail")
	}
}
