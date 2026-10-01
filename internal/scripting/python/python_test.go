package python

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func skipIfNoPython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath(pythonBin); err != nil {
		t.Skipf("%s not on PATH, skipping", pythonBin)
	}
}

func writeScript(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.py")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("writing test script: %v", err)
	}
	return path
}

func TestLoad_And_NewVU_RunsIterations(t *testing.T) {
	skipIfNoPython(t)
	path := writeScript(t, `
def iteration():
    pass
`)

	script, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	vu, err := script.NewVU()
	if err != nil {
		t.Fatalf("NewVU returned error: %v", err)
	}
	defer vu.Close()

	for i := 0; i < 3; i++ {
		if err := vu.Iteration(context.Background()); err != nil {
			t.Fatalf("Iteration #%d returned error: %v", i, err)
		}
	}
}

func TestIteration_PropagatesPythonException(t *testing.T) {
	skipIfNoPython(t)
	path := writeScript(t, `
def iteration():
    raise ValueError("boom")
`)

	script, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	vu, err := script.NewVU()
	if err != nil {
		t.Fatalf("NewVU returned error: %v", err)
	}
	defer vu.Close()

	err = vu.Iteration(context.Background())
	if err == nil {
		t.Fatal("expected Iteration to return an error for a raised exception")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "boom")
	}

	// The interpreter survives one failed iteration and keeps serving
	// the protocol for the next one.
	if err := vu.Iteration(context.Background()); err == nil {
		t.Fatal("expected the second iteration to also raise")
	}
}

func TestIteration_RecoversAfterFailedIteration(t *testing.T) {
	skipIfNoPython(t)
	path := writeScript(t, `
calls = 0

def iteration():
    global calls
    calls += 1
    if calls == 1:
        raise ValueError("first call fails")
`)

	script, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	vu, err := script.NewVU()
	if err != nil {
		t.Fatalf("NewVU returned error: %v", err)
	}
	defer vu.Close()

	if err := vu.Iteration(context.Background()); err == nil {
		t.Fatal("expected the first iteration to fail")
	}
	if err := vu.Iteration(context.Background()); err != nil {
		t.Fatalf("expected the second iteration to succeed, got: %v", err)
	}
}

func TestNewVU_MissingIterationFunction(t *testing.T) {
	skipIfNoPython(t)
	path := writeScript(t, `
def helper():
    pass
`)

	script, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if _, err := script.NewVU(); err == nil {
		t.Fatal("expected NewVU to return an error when there is no iteration() function")
	}
}

func TestNewVU_ScriptFailsToImport(t *testing.T) {
	skipIfNoPython(t)
	path := writeScript(t, `this is not valid python {{{`)

	script, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if _, err := script.NewVU(); err == nil {
		t.Fatal("expected NewVU to return an error when the script fails to import")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	skipIfNoPython(t)
	if _, err := Load(filepath.Join(t.TempDir(), "does-not-exist.py")); err == nil {
		t.Fatal("expected Load to return an error for a missing file")
	}
}

func TestLoad_InterpreterNotFound(t *testing.T) {
	path := writeScript(t, "def iteration():\n    pass\n")

	orig := pythonBin
	pythonBin = "vegaload-nonexistent-interpreter"
	defer func() { pythonBin = orig }()

	if _, err := Load(path); err == nil {
		t.Fatal("expected Load to return an error when the interpreter is not on PATH")
	}
}

func TestIteration_RespectsContextCancellation(t *testing.T) {
	skipIfNoPython(t)
	path := writeScript(t, `
import time

def iteration():
    time.sleep(2)
`)

	script, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	vu, err := script.NewVU()
	if err != nil {
		t.Fatalf("NewVU returned error: %v", err)
	}
	defer vu.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- vu.Iteration(ctx) }()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected Iteration to return an error when its context is cancelled")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Iteration did not return promptly after context cancellation")
	}
}

func TestVU_Close(t *testing.T) {
	skipIfNoPython(t)
	path := writeScript(t, "def iteration():\n    pass\n")

	script, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	vu, err := script.NewVU()
	if err != nil {
		t.Fatalf("NewVU returned error: %v", err)
	}

	if err := vu.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
}
