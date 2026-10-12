package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/compare"
	"github.com/vegaload/vegaload/internal/report"
)

func writeCompareFixture(t *testing.T, dir, name string, res *report.Result) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := report.WriteJSON(path, res); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	return path
}

func TestCmdCompare_OkAndRegressed(t *testing.T) {
	dir := t.TempDir()
	base := &report.Result{
		Executor:  "fixed-vus",
		Elapsed:   10 * time.Second,
		Total:     100,
		Failed:    0,
		ErrorRate: 0,
		Latency:   report.Latency{P95: 50 * time.Millisecond, P50: 20 * time.Millisecond},
	}
	ok := *base
	worse := *base
	worse.ErrorRate = 0.1
	worse.Failed = 10

	baseline := writeCompareFixture(t, dir, "baseline.json", base)
	candidateOK := writeCompareFixture(t, dir, "ok.json", &ok)
	candidateBad := writeCompareFixture(t, dir, "bad.json", &worse)

	if code := cmdCompare([]string{baseline, candidateOK}); code != 0 {
		t.Errorf("compare ok pair exited %d, want 0", code)
	}
	if code := cmdCompare([]string{"-output", "json", baseline, candidateBad}); code != 1 {
		t.Errorf("compare regressed pair exited %d, want 1", code)
	}
}

func TestCmdCompare_UsageErrors(t *testing.T) {
	if code := cmdCompare([]string{}); code != 2 {
		t.Errorf("no args exited %d, want 2", code)
	}
	if code := cmdCompare([]string{"-output", "bogus", "a.json", "b.json"}); code != 2 {
		t.Errorf("bad -output exited %d, want 2", code)
	}
	if code := cmdCompare([]string{"/nonexistent/a.json", "/nonexistent/b.json"}); code != 1 {
		t.Errorf("missing files exited %d, want 1", code)
	}
}

func TestCmdCompare_ChecksFixtures(t *testing.T) {
	dir := filepath.Join("..", "..", "examples", "scenarios", "compare")
	base := filepath.Join(dir, "checks-baseline.json")
	if code := cmdCompare([]string{base, filepath.Join(dir, "checks-ok.json")}); code != 0 {
		t.Errorf("same checks exited %d, want 0", code)
	}
	bad := filepath.Join(dir, "checks-regressed.json")
	if code := cmdCompare([]string{base, bad}); code != 1 {
		t.Errorf("a check that fell exited %d, want 1", code)
	}
	if code := cmdCompare([]string{"-check-rate-delta", "0.2", base, bad}); code != 0 {
		t.Errorf("with 20 points of slack exited %d, want 0", code)
	}
}

func TestCmdCompare_ReportWithoutProtocols(t *testing.T) {
	dir := t.TempDir()
	old := []byte(`{"executor":"fixed-vus","total":10,"failed":0,"error_rate":0,"elapsed_ns":10000000000,"latency":{"p95_ns":50000000,"p50_ns":20000000}}`)
	base := filepath.Join(dir, "old-base.json")
	cand := filepath.Join(dir, "old-cand.json")
	if err := os.WriteFile(base, old, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cand, old, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadReportJSON(base)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Protocols != nil || loaded.Total != 10 {
		t.Fatalf("old report = total %d protocols %+v", loaded.Total, loaded.Protocols)
	}
	if code := cmdCompare([]string{base, cand}); code != 0 {
		t.Errorf("compare of reports with no protocols exited %d, want 0", code)
	}
}

func TestCmdCompare_PrintsProtocolLinesWithoutGating(t *testing.T) {
	dir := t.TempDir()
	base := &report.Result{
		Elapsed: 10 * time.Second, Total: 100, ErrorRate: 0,
		Latency:   report.Latency{P95: 50 * time.Millisecond},
		Protocols: []report.ProtocolResult{{Name: "ftp", Total: 10, ErrorRate: 0, Latency: report.Latency{P95: 10 * time.Millisecond}}},
	}
	cand := &report.Result{
		Elapsed: 10 * time.Second, Total: 100, ErrorRate: 0,
		Latency: report.Latency{P95: 50 * time.Millisecond},
		Protocols: []report.ProtocolResult{
			{Name: "ftp", Total: 10, ErrorRate: 0.2, Latency: report.Latency{P95: 80 * time.Millisecond}},
			{Name: "redis", Total: 1, ErrorRate: 0, Latency: report.Latency{P95: time.Millisecond}},
		},
	}
	baseline := writeCompareFixture(t, dir, "baseline.json", base)
	candidate := writeCompareFixture(t, dir, "candidate.json", cand)
	if code := cmdCompare([]string{baseline, candidate}); code != 0 {
		t.Fatalf("a worse protocol p95 exited %d, want 0", code)
	}
	diff := compare.Compare(base, cand, compare.Options{})
	var buf bytes.Buffer
	printCompare(&buf, diff)
	text := buf.String()
	if !strings.Contains(text, "ftp  p95 10ms -> 80ms") || !strings.Contains(text, "redis  only in candidate") {
		t.Fatalf("protocol lines:\n%s", text)
	}
}
