package audit

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "audit.log")

	e1 := Entry{Time: time.Now(), Trigger: "cli", Target: "http://localhost:8080", Executor: "fixed-vus", VUs: 5, Outcome: "success", Total: 10, Failed: 0}
	e2 := Entry{Time: time.Now(), Trigger: "mcp:session-123", ScenarioPath: "scenario.vl.js", Executor: "ramp", Outcome: "error", Error: "boom"}

	if err := Append(path, e1); err != nil {
		t.Fatalf("Append #1: %v", err)
	}
	if err := Append(path, e2); err != nil {
		t.Fatalf("Append #2: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening audit log: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2 (one JSON object per Append call)", len(lines))
	}
	if !contains(lines[0], `"trigger":"cli"`) {
		t.Errorf("line 1 missing trigger=cli: %s", lines[0])
	}
	if !contains(lines[1], `"trigger":"mcp:session-123"`) {
		t.Errorf("line 2 missing trigger=mcp:session-123: %s", lines[1])
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (func() bool {
		for i := 0; i+len(substr) <= len(s); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}

func TestDefaultPath(t *testing.T) {
	p := DefaultPath()
	if p == "" {
		t.Error("DefaultPath returned empty string")
	}
}
