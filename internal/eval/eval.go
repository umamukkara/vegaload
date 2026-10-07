// Package eval implements FR-MCP-04's versioned MCP tool-calling
// evaluation suite: a fixed set of {tool, arguments, expected outcome}
// cases, embedded into the binary and versioned by directory (v1, and
// any v2+ added later without touching v1's cases) so a suite someone
// has pinned to doesn't silently change underneath them.
//
// The suite tests the tool layer itself — the same thing
// `vegaload mcp eval` runs in CI and the same thing a human could sanity
// check after upgrading — not an LLM's tool-choosing behavior; evaluating
// whether a particular model picks the right tool for a prompt is a
// separate, model-dependent exercise this package deliberately leaves
// out, and the cases are written against the compiled vegaload binary
// via the same internal/mcp.Tool.Handler contract (see cmd/vegaload's
// eval.go), not against an LLM's choices.
package eval

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// rawV1 is v1's cases, unmodified — WORKDIR tokens and all. cmd/vegaload
// substitutes "${WORKDIR}" for a real scratch directory before parsing,
// since several cases need a real, writable file path; RawV1 exposes
// the untouched bytes so that substitution can happen first.
//
//go:embed v1/cases.json
var rawV1 []byte

// RawV1 returns the v1 suite's raw JSON bytes, before any "${WORKDIR}"
// substitution.
func RawV1() []byte {
	return rawV1
}

// rawV2 is v2's cases, with the same "${WORKDIR}" tokens as v1. v2 keeps
// every v1 case as it was and adds cases for the tools and options added
// since: validate_scenario, the baseline gate, JUnit output and checks.
//
//go:embed v2/cases.json
var rawV2 []byte

// RawV2 returns the v2 suite's raw JSON bytes, before any "${WORKDIR}"
// substitution.
func RawV2() []byte {
	return rawV2
}

// Raw returns the raw JSON of the named suite ("v1" or "v2"), and false
// when there is no such suite.
func Raw(name string) ([]byte, bool) {
	switch name {
	case "v1":
		return rawV1, true
	case "v2":
		return rawV2, true
	}
	return nil, false
}

// Case is one eval case: call Tool with Arguments and check the result
// against ExpectError/ExpectContains.
type Case struct {
	Name      string          `json:"name"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`

	// ExpectError, when true, means the call must return a non-nil
	// error; ExpectContains is ignored in that case.
	ExpectError bool `json:"expect_error,omitempty"`

	// ExpectErrorContains, when set, also requires the error message to
	// contain this text. It implies ExpectError (v2).
	ExpectErrorContains string `json:"expect_error_contains,omitempty"`

	// ExpectFiles lists paths that must exist after the call (v2). A
	// case uses it for files a tool writes, such as a JUnit report.
	ExpectFiles []string `json:"expect_files,omitempty"`

	// ExpectContains lists fields the JSON-decoded result must contain
	// with exactly these values. A key is a top-level field name. In v2 a
	// key may also be a dotted path into nested objects and arrays, such
	// as "baseline.passed" or "checks.0.name" (a number picks an array
	// item). There is no general JSON-diff.
	ExpectContains map[string]any `json:"expect_contains,omitempty"`
}

// Suite is a versioned set of Cases.
type Suite struct {
	Version string `json:"version"`
	Cases   []Case `json:"cases"`
}

// ParseSuite decodes data (typically RawV1()'s bytes, after any
// "${WORKDIR}" substitution) into a Suite.
func ParseSuite(data []byte) (Suite, error) {
	var s Suite
	if err := json.Unmarshal(data, &s); err != nil {
		return Suite{}, fmt.Errorf("eval: parsing suite: %w", err)
	}
	return s, nil
}

// Caller invokes one MCP tool by name with raw JSON arguments — the
// same signature internal/mcp.Tool.Handler uses, so cmd/vegaload/eval.go
// can pass a Caller built directly from mcp.NewTools without this
// package importing internal/mcp (keeping a one-way dependency: mcp
// tools are evaluated by this package, this package is never called by
// them).
type Caller func(ctx context.Context, tool string, arguments json.RawMessage) (any, error)

// CaseResult is one Case's outcome.
type CaseResult struct {
	Name   string
	Passed bool
	Detail string // empty when Passed; otherwise why it failed
}

// Run calls call for every case and checks its outcome, returning one
// CaseResult per case in order.
func Run(ctx context.Context, call Caller, cases []Case) []CaseResult {
	results := make([]CaseResult, 0, len(cases))
	for _, c := range cases {
		results = append(results, runCase(ctx, call, c))
	}
	return results
}

func runCase(ctx context.Context, call Caller, c Case) CaseResult {
	out, err := call(ctx, c.Tool, c.Arguments)

	if c.ExpectError || c.ExpectErrorContains != "" {
		if err == nil {
			return CaseResult{Name: c.Name, Passed: false, Detail: "expected an error but the call succeeded"}
		}
		if c.ExpectErrorContains != "" && !strings.Contains(err.Error(), c.ExpectErrorContains) {
			return CaseResult{Name: c.Name, Passed: false, Detail: fmt.Sprintf("error %q does not contain %q", err.Error(), c.ExpectErrorContains)}
		}
		return CaseResult{Name: c.Name, Passed: true}
	}
	if err != nil {
		return CaseResult{Name: c.Name, Passed: false, Detail: fmt.Sprintf("unexpected error: %v", err)}
	}

	ok, detail := checkContains(out, c.ExpectContains)
	if !ok {
		return CaseResult{Name: c.Name, Passed: false, Detail: detail}
	}
	for _, f := range c.ExpectFiles {
		if _, statErr := os.Stat(f); statErr != nil {
			return CaseResult{Name: c.Name, Passed: false, Detail: fmt.Sprintf("expected file %s was not written", f)}
		}
	}
	return CaseResult{Name: c.Name, Passed: true}
}

// checkContains verifies that out, once round-tripped through JSON,
// contains every key in want with an equal value (compared by their own
// JSON encoding, so 42 and 42.0 are treated the same way the suite's
// JSON numbers already are).
func checkContains(out any, want map[string]any) (bool, string) {
	if len(want) == 0 {
		return true, ""
	}
	data, err := json.Marshal(out)
	if err != nil {
		return false, fmt.Sprintf("encoding result: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		return false, fmt.Sprintf("result is not a JSON object: %v", err)
	}
	for key, wantVal := range want {
		gotVal, ok := got[key]
		if !ok {
			gotVal, ok = lookupPath(got, key)
		}
		if !ok {
			return false, fmt.Sprintf("missing field %q", key)
		}
		gotJSON, _ := json.Marshal(gotVal)
		wantJSON, _ := json.Marshal(wantVal)
		if string(gotJSON) != string(wantJSON) {
			return false, fmt.Sprintf("field %q = %s, want %s", key, gotJSON, wantJSON)
		}
	}
	return true, ""
}

// lookupPath follows a dotted path such as "baseline.passed" or
// "checks.0.name" through nested objects and arrays. A path segment that
// is a number picks an array item. It reports false when any step is
// missing.
func lookupPath(root map[string]any, path string) (any, bool) {
	var cur any = root
	for _, seg := range strings.Split(path, ".") {
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[seg]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(v) {
				return nil, false
			}
			cur = v[i]
		default:
			return nil, false
		}
	}
	return cur, true
}
