package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vegaload/vegaload/internal/doctor"
	"github.com/vegaload/vegaload/internal/hosts"
	"github.com/vegaload/vegaload/internal/mcp"
	"github.com/vegaload/vegaload/internal/mcpprobe"
)

// fakeMachine replaces the parts of the environment that would start the
// test binary or a real MCP server: the version query and the handshake.
func fakeMachine(e *doctor.Env) {
	e.RunCmd = func(ctx context.Context, name string, args ...string) (string, error) {
		return "vegaload " + e.Version, nil
	}
	e.Probe = func(ctx context.Context, c mcpprobe.Command) (mcpprobe.Info, error) {
		return mcpprobe.Info{Tools: append([]string(nil), mcp.CoreToolNames...)}, nil
	}
}

// doctorRun runs the doctor command against an isolated project and home
// directory, with the MCP probe faked so the test needs no built binary.
func doctorRun(t *testing.T, args ...string) (code int, stdout string, dir, home string) {
	t.Helper()
	dir = t.TempDir()
	home = t.TempDir()
	out, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = out.Close() }) // Windows cannot delete an open file
	errf, err := os.CreateTemp(t.TempDir(), "err")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = errf.Close() })
	args = append([]string{"-dir", dir}, args...)
	code = runDoctor(args, out, errf, func(e *doctor.Env) {
		e.Home = home
		fakeMachine(e)
	})
	b, _ := os.ReadFile(out.Name())
	stdout = string(b)
	if code == 2 {
		e, _ := os.ReadFile(errf.Name())
		stdout += string(e)
	}
	return
}

func TestDoctor_CLIOnlyJSON(t *testing.T) {
	code, out, _, _ := doctorRun(t, "-host", "none", "-only", "core.version,core.dirs", "-output", "json")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	var rep struct {
		Summary struct{ Pass, Fail int }
		Results []struct{ ID, Status string }
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if len(rep.Results) != 2 || rep.Summary.Pass != 2 {
		t.Errorf("report = %+v", rep)
	}
}

func TestDoctor_FailsThenFixesMissingCursorSetup(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	out, _ := os.CreateTemp(t.TempDir(), "out")
	t.Cleanup(func() { _ = out.Close() }) // Windows cannot delete an open file
	errf, _ := os.CreateTemp(t.TempDir(), "err")
	t.Cleanup(func() { _ = errf.Close() })
	mutate := func(e *doctor.Env) {
		e.Home = home
		fakeMachine(e)
	}
	base := []string{"-dir", dir, "-host", "cursor", "-only", "host.cursor"}

	if code := runDoctor(base, out, errf, mutate); code != 1 {
		t.Fatalf("first run exit = %d, want 1", code)
	}
	if code := runDoctor(append(base, "-fix"), out, errf, mutate); code != 0 {
		b, _ := os.ReadFile(out.Name())
		t.Fatalf("-fix exit = %d:\n%s", code, b)
	}
	st := hosts.Inspect(hosts.MCPConfig{Path: filepath.Join(dir, ".cursor", "mcp.json")})
	if st.Entry == nil {
		t.Fatal("-fix did not register the server")
	}
	if code := runDoctor(base, out, errf, mutate); code != 0 {
		t.Errorf("run after fix exit = %d, want 0", code)
	}
}

func TestDoctor_AgreesWithInit(t *testing.T) {
	// A project set up by `vegaload init` must pass every host check.
	dir := t.TempDir()
	if code := cmdInit([]string{"-dir", dir}); code != 0 {
		t.Fatalf("init exit %d", code)
	}
	code, out, _, _ := doctorRun(t, "-dir", dir, "-host", "cursor,claude-code", "-only", "host", "-strict")
	if code != 0 {
		t.Errorf("doctor disagrees with init (exit %d):\n%s", code, out)
	}
}

func TestDoctor_DryRunPrintsPlanAndWritesNothing(t *testing.T) {
	code, out, dir, _ := doctorRun(t, "-host", "claude-code", "-only", "host.claude-code", "-fix", "-dry-run")
	if code != 1 {
		t.Errorf("exit = %d, want 1 (nothing was fixed)", code)
	}
	if !strings.Contains(out, "--fix would:") {
		t.Errorf("no plan in output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".mcp.json")); !os.IsNotExist(err) {
		t.Error("dry run wrote a file")
	}
}

func TestDoctor_UsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"-output", "xml"},
		{"-dry-run"},
		{"-smoke"},
		{"-host", "emacs"},
	} {
		if code, out, _, _ := doctorRun(t, args...); code != 2 {
			t.Errorf("%v: exit = %d, want 2\n%s", args, code, out)
		}
	}
}
