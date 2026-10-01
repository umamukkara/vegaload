package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdNew_CreatesJSByDefault(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd) //nolint:errcheck
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if code := cmdNew([]string{"myscenario"}); code != 0 {
		t.Fatalf("cmdNew returned exit code %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "myscenario.vl.js")); err != nil {
		t.Errorf("expected myscenario.vl.js to exist: %v", err)
	}
}

func TestCmdNew_PythonFlag(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd) //nolint:errcheck
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if code := cmdNew([]string{"-python", "myscenario"}); code != 0 {
		t.Fatalf("cmdNew returned exit code %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "myscenario.py")); err != nil {
		t.Errorf("expected myscenario.py to exist: %v", err)
	}
}

func TestCmdNew_RefusesToOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd) //nolint:errcheck
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if code := cmdNew([]string{"myscenario"}); code != 0 {
		t.Fatalf("first cmdNew returned exit code %d", code)
	}
	if code := cmdNew([]string{"myscenario"}); code == 0 {
		t.Error("expected the second cmdNew (no -force) to fail since the file already exists")
	}
	if code := cmdNew([]string{"-force", "myscenario"}); code != 0 {
		t.Errorf("cmdNew with -force returned exit code %d, want 0", code)
	}
}

const testOpenAPISpec = `{
  "info": {"title": "Widgets API"},
  "servers": [{"url": "https://api.example.com"}],
  "paths": {"/widgets": {"get": {"summary": "List widgets"}}}
}`

func TestCmdNew_FromOpenAPI(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd) //nolint:errcheck
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	specPath := filepath.Join(dir, "spec.json")
	if err := os.WriteFile(specPath, []byte(testOpenAPISpec), 0o644); err != nil {
		t.Fatalf("writing spec: %v", err)
	}

	if code := cmdNew([]string{"-from-openapi", specPath, "widgets"}); code != 0 {
		t.Fatalf("cmdNew -from-openapi returned exit code %d", code)
	}
	data, err := os.ReadFile(filepath.Join(dir, "widgets.vegaload-plan.md"))
	if err != nil {
		t.Fatalf("expected widgets.vegaload-plan.md to exist: %v", err)
	}
	if !strings.Contains(string(data), "vegaload run -target") {
		t.Errorf("expected a run command in the generated runbook, got:\n%s", data)
	}
}

func TestCmdNew_FromOpenAPI_JSONOutput(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd) //nolint:errcheck
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	specPath := filepath.Join(dir, "spec.json")
	if err := os.WriteFile(specPath, []byte(testOpenAPISpec), 0o644); err != nil {
		t.Fatalf("writing spec: %v", err)
	}

	if code := cmdNew([]string{"-from-openapi", specPath, "-output", "json", "widgets"}); code != 0 {
		t.Fatalf("cmdNew -from-openapi -output json returned exit code %d", code)
	}
}

func TestCmdNew_FromOpenAPI_BadSpec(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd) //nolint:errcheck
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	specPath := filepath.Join(dir, "spec.json")
	if err := os.WriteFile(specPath, []byte("not json"), 0o644); err != nil {
		t.Fatalf("writing spec: %v", err)
	}

	if code := cmdNew([]string{"-from-openapi", specPath}); code == 0 {
		t.Error("expected a non-zero exit code for an invalid OpenAPI spec")
	}
}

func TestWithExt(t *testing.T) {
	cases := map[string]string{
		"scenario":         "scenario.vl.js",
		"scenario.vl.js":   "scenario.vl.js",
		"myscenario.py":    "myscenario.py",
		"myscenario.vl.ts": "myscenario.vl.ts",
	}
	for in, want := range cases {
		if got := withExt(in, ".vl.js"); got != want {
			t.Errorf("withExt(%q, \".vl.js\") = %q, want %q", in, got, want)
		}
	}
}
