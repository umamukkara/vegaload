package js

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeScript creates a temporary file containing src and returns its
// path. name picks the extension, which is what Load uses to decide
// whether to treat the file as TypeScript.
func writeScript(t *testing.T, name, src string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("writing test script: %v", err)
	}
	return path
}

func TestLoad_JS_DefaultExportRuns(t *testing.T) {
	path := writeScript(t, "scenario.js", `
		export default function () {
			// one iteration, nothing to do
		}
	`)

	script, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	vu, err := script.NewVU()
	if err != nil {
		t.Fatalf("NewVU returned error: %v", err)
	}
	if err := vu.Iteration(context.Background()); err != nil {
		t.Fatalf("Iteration returned error: %v", err)
	}
}

func TestLoad_TS_StripsTypesAndRuns(t *testing.T) {
	path := writeScript(t, "scenario.ts", `
		function add(a: number, b: number): number {
			return a + b;
		}
		export default function (): void {
			const sum: number = add(1, 2);
			if (sum !== 3) {
				throw new Error("arithmetic is broken: " + sum);
			}
		}
	`)

	script, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	vu, err := script.NewVU()
	if err != nil {
		t.Fatalf("NewVU returned error: %v", err)
	}
	if err := vu.Iteration(context.Background()); err != nil {
		t.Fatalf("Iteration returned error: %v", err)
	}
}

func TestIteration_PropagatesThrownError(t *testing.T) {
	path := writeScript(t, "scenario.js", `
		export default function () {
			throw new Error("boom");
		}
	`)

	script, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	vu, err := script.NewVU()
	if err != nil {
		t.Fatalf("NewVU returned error: %v", err)
	}

	err = vu.Iteration(context.Background())
	if err == nil {
		t.Fatal("expected Iteration to return an error for a thrown exception")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "boom")
	}
}

func TestNewVU_MissingDefaultExport(t *testing.T) {
	path := writeScript(t, "scenario.js", `
		function helper() {}
	`)

	script, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if _, err := script.NewVU(); err == nil {
		t.Fatal("expected NewVU to return an error when there is no default export")
	}
}

func TestLoad_SyntaxError(t *testing.T) {
	path := writeScript(t, "scenario.js", `this is not valid javascript {{{`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected Load to return an error for invalid syntax")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "does-not-exist.js")); err == nil {
		t.Fatal("expected Load to return an error for a missing file")
	}
}

func TestVU_IsolatedPerInstance(t *testing.T) {
	path := writeScript(t, "scenario.js", `
		let calls = 0;
		globalThis.calls = calls;
		export default function () {
			calls++;
			globalThis.calls = calls;
		}
	`)

	script, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	vu1, err := script.NewVU()
	if err != nil {
		t.Fatalf("NewVU returned error: %v", err)
	}
	vu2, err := script.NewVU()
	if err != nil {
		t.Fatalf("NewVU returned error: %v", err)
	}

	if err := vu1.Iteration(context.Background()); err != nil {
		t.Fatalf("vu1 Iteration returned error: %v", err)
	}
	if err := vu1.Iteration(context.Background()); err != nil {
		t.Fatalf("vu1 Iteration returned error: %v", err)
	}
	if err := vu2.Iteration(context.Background()); err != nil {
		t.Fatalf("vu2 Iteration returned error: %v", err)
	}

	got1 := vu1.vm.Get("calls").ToInteger()
	got2 := vu2.vm.Get("calls").ToInteger()
	if got1 != 2 {
		t.Errorf("vu1's calls = %d, want 2", got1)
	}
	if got2 != 1 {
		t.Errorf("vu2's calls = %d, want 1 (vu1 and vu2 must not share state)", got2)
	}
}

func TestIteration_InterruptsOnContextCancellation(t *testing.T) {
	path := writeScript(t, "scenario.js", `
		export default function () {
			while (true) {
				// spin forever unless interrupted
			}
		}
	`)

	script, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	vu, err := script.NewVU()
	if err != nil {
		t.Fatalf("NewVU returned error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- vu.Iteration(ctx) }()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected Iteration to return an error when interrupted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Iteration did not return after context cancellation")
	}
}
