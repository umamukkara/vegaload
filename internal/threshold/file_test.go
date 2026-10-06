package threshold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFile_List(t *testing.T) {
	ts, err := ParseFile([]byte(`[
		{"name": "fast", "metric": "p95", "operator": "<", "value": "300ms"},
		{"metric": "error_rate", "operator": "<=", "value": 0.01}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 2 || ts[0].Name != "fast" || ts[1].Name != "error_rate <= 0.01" {
		t.Fatalf("got %+v", ts)
	}
}

func TestParseFile_Wrapped(t *testing.T) {
	ts, err := ParseFile([]byte(`{"thresholds": [{"metric": "p99", "operator": "<", "value": "1s"}]}`))
	if err != nil || len(ts) != 1 || ts[0].Metric != "p99" {
		t.Fatalf("got %+v, %v", ts, err)
	}
}

func TestParseFile_SuggestedShape(t *testing.T) {
	ts, err := ParseFile([]byte(`{"latency_p95": "300ms", "error_rate": 0.015}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 2 || ts[0].Expression() != "p95 <= 300ms" || ts[1].Expression() != "error_rate <= 0.015" {
		t.Fatalf("got %+v", ts)
	}
}

func TestParseFile_DiagnoseOutput(t *testing.T) {
	// `vegaload diagnose -output json` nests the suggestion under a key
	// and carries other fields; the whole output should load as-is.
	ts, err := ParseFile([]byte(`{"findings": [], "suggested_thresholds": {"latency_p95": "120ms", "error_rate": 0.01}}`))
	if err != nil || len(ts) != 2 {
		t.Fatalf("got %+v, %v", ts, err)
	}
}

func TestParseFile_RealDiagnoseOutput(t *testing.T) {
	// The exact shape `vegaload diagnose -output json` prints.
	in := `{"report_path":"r.json","findings":{"notes":["ok"]},"suggested_thresholds":{"latency_p95":"60ms","error_rate":0.01,"error_rate_pct":"1.0%","threshold_flags":["p95 <= 60ms","error_rate <= 0.01"]}}`
	ts, err := ParseFile([]byte(in))
	if err != nil || len(ts) != 2 || ts[0].Expression() != "p95 <= 60ms" {
		t.Fatalf("got %+v, %v", ts, err)
	}
}

func TestParseFile_Errors(t *testing.T) {
	cases := []struct{ in, wantErr string }{
		{``, "empty"},
		{`not json`, "not valid JSON"},
		{`[]`, "no thresholds"},
		{`{}`, "no thresholds"},
		{`{"latency_p99": "1s"}`, "latency_p99"},
		{`[{"metric": "p95", "operator": "<"}]`, "value"},
		{`[{"metric": "nope", "operator": "<", "value": "1s"}]`, "unknown metric"},
	}
	for _, c := range cases {
		_, err := ParseFile([]byte(c.in))
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("ParseFile(%q) error = %v, want it to contain %q", c.in, err, c.wantErr)
		}
	}
}

func TestLoadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.json")
	if err := os.WriteFile(path, []byte(`[{"metric":"p95","operator":"<","value":"1s"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	ts, err := LoadFile(path)
	if err != nil || len(ts) != 1 {
		t.Fatalf("got %+v, %v", ts, err)
	}
	if _, err := LoadFile(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("want an error for a missing file")
	}
}
