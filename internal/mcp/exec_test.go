package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeBinary writes an executable shell script to a temp dir that
// prints stdout/stderr and exits with exitCode, standing in for the
// real vegaload binary so RunCLI's behavior can be tested without
// actually building or running vegaload itself.
func fakeBinary(t *testing.T, stdout string, stderr string, exitCode int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a /bin/sh script, which Windows cannot run")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-vegaload")
	var script strings.Builder
	script.WriteString("#!/bin/sh\n")
	if stdout != "" {
		fmt.Fprintf(&script, "printf '%%s' %s\n", shQuote(stdout))
	}
	if stderr != "" {
		fmt.Fprintf(&script, "printf '%%s' %s 1>&2\n", shQuote(stderr))
	}
	fmt.Fprintf(&script, "exit %d\n", exitCode)
	if err := os.WriteFile(path, []byte(script.String()), 0o755); err != nil {
		t.Fatalf("writing fake binary: %v", err)
	}
	return path
}

// shQuote wraps s in single quotes for safe use inside the generated
// shell script, escaping any single quote s itself contains.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func TestRunCLI_Success(t *testing.T) {
	path := fakeBinary(t, `{"ok":true}`, "", 0)
	out, err := RunCLI(context.Background(), path, "new", "-output", "json")
	if err != nil {
		t.Fatalf("RunCLI returned error: %v", err)
	}
	if strings.TrimSpace(string(out)) != `{"ok":true}` {
		t.Errorf("stdout = %q, want %q", out, `{"ok":true}`)
	}
}

func TestRunCLI_NonZeroExitIncludesStderr(t *testing.T) {
	path := fakeBinary(t, "", "something went wrong", 1)
	_, err := RunCLI(context.Background(), path, "run")
	if err == nil {
		t.Fatal("expected an error for a non-zero exit")
	}
	if !strings.Contains(err.Error(), "something went wrong") {
		t.Errorf("expected stderr in the error, got: %v", err)
	}
}

func TestRunCLI_MissingBinary(t *testing.T) {
	_, err := RunCLI(context.Background(), "/nonexistent/vegaload-binary", "version")
	if err == nil {
		t.Fatal("expected an error for a missing binary")
	}
}
