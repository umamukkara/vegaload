package main

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/vegaload/vegaload/internal/scripting/js"
)

// The scenario files under examples/scenarios are documentation people
// copy and run. They need a live sample app to actually run, so this
// test only checks that each one is still valid: a JavaScript scenario
// must load, and a Python scenario must compile. It keeps an example
// from rotting when the scripting API changes.
func TestExampleScenariosAreValid(t *testing.T) {
	dir := filepath.Join("..", "..", "examples", "scenarios")

	jsFiles, err := filepath.Glob(filepath.Join(dir, "*.vl.js"))
	if err != nil {
		t.Fatal(err)
	}
	if len(jsFiles) == 0 {
		t.Fatal("found no JavaScript example scenarios; has the directory moved?")
	}
	for _, f := range jsFiles {
		if _, err := js.Load(f); err != nil {
			t.Errorf("%s: %v", filepath.Base(f), err)
		}
	}

	pyFiles, err := filepath.Glob(filepath.Join(dir, "*.py"))
	if err != nil {
		t.Fatal(err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Logf("python3 is not installed; skipping the %d Python example(s)", len(pyFiles))
		return
	}
	for _, f := range pyFiles {
		// -B keeps py_compile from leaving a __pycache__ directory.
		if out, err := exec.Command(python, "-B", "-c", "import sys; compile(open(sys.argv[1]).read(), sys.argv[1], 'exec')", f).CombinedOutput(); err != nil {
			t.Errorf("%s: %v\n%s", filepath.Base(f), err, out)
		}
	}
}
