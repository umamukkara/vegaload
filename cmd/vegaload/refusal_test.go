package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vegaload/vegaload/internal/report"
	"github.com/vegaload/vegaload/internal/safety"
)

// A -protocol upload that the write flag stops never becomes a recorded call.
func TestCmdRun_RefusedFTPUploadIsNotRecorded(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "summary.json")
	cmdRun([]string{
		"-vus", "1", "-duration", "200ms", "-no-report",
		"-audit-log", filepath.Join(dir, "audit.log"), "-out", out,
		"-target", "ftp://127.0.0.1:9/", "-protocol", "ftp",
		"-opt", "mode=upload", "-opt", "path=/up-{id}.bin", "-opt", "size=1",
	})
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var res report.Result
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatal(err)
	}
	if res.Total == 0 {
		t.Fatal("the run made no iterations")
	}
	if res.Protocols != nil {
		t.Fatalf("refused upload was recorded: %+v", res.Protocols)
	}
}

func TestHostRefusal_NotLocalhost(t *testing.T) {
	err := buildSafetyCheck(&runConfig{})("example.com")
	if !safety.IsHostRefusal(err) {
		t.Fatalf("not localhost: %v", err)
	}
}

func TestHostRefusal_Allowlisted(t *testing.T) {
	err := buildSafetyCheck(&runConfig{AllowTargets: []string{"example.com"}})("example.com")
	if err != nil || safety.IsHostRefusal(err) {
		t.Fatalf("allowlisted host: %v", err)
	}
}
