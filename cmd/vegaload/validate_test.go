package main

import (
	"bytes"
	"encoding/json"
	"github.com/vegaload/vegaload/internal/audit"
	"github.com/vegaload/vegaload/internal/report"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeScenario(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func okServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":7}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func validate(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	args = append([]string{"-audit-log", filepath.Join(t.TempDir(), "audit.log")}, args...)
	code := runValidate(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestValidate_ValidScenarioExitsZero(t *testing.T) {
	srv := okServer(t)
	p := writeScenario(t, "ok.vl.js", `export default function () {
  const r = http.get("`+srv.URL+`/");
  check(r, {"status is 200": (r) => r.status === 200});
}`)
	code, out, _ := validate(t, p)
	if code != 0 || !strings.Contains(out, "valid:") || !strings.Contains(out, "PASS  status is 200") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestValidate_JSONOutput(t *testing.T) {
	srv := okServer(t)
	p := writeScenario(t, "ok.vl.js", `export default function () { http.get("`+srv.URL+`/"); }`)
	code, out, _ := validate(t, "-output", "json", p)
	var res validateResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("not json: %q", out)
	}
	if code != 0 || !res.Valid || res.Scenario != p || res.Elapsed == "" {
		t.Fatalf("code=%d res=%+v", code, res)
	}
}

func TestValidate_SyntaxErrorIsLoadStage(t *testing.T) {
	p := writeScenario(t, "bad.vl.js", `export default function( {`)
	code, out, _ := validate(t, "-output", "json", p)
	var res validateResult
	_ = json.Unmarshal([]byte(out), &res)
	if code != 1 || res.Valid || res.Stage != "load" || res.Error == "" {
		t.Fatalf("code=%d res=%+v", code, res)
	}
}

func TestValidate_MissingFileIsLoadStage(t *testing.T) {
	code, out, _ := validate(t, filepath.Join(t.TempDir(), "nope.vl.js"))
	if code != 1 || !strings.Contains(out, "not valid") || !strings.Contains(out, "(load)") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestValidate_ThrowingIterationIsIterationStage(t *testing.T) {
	p := writeScenario(t, "throw.vl.js", `export default function () { throw new Error("boom"); }`)
	code, out, _ := validate(t, "-output", "json", p)
	var res validateResult
	_ = json.Unmarshal([]byte(out), &res)
	if code != 1 || res.Stage != "iteration" || !strings.Contains(res.Error, "boom") {
		t.Fatalf("code=%d res=%+v", code, res)
	}
}

func TestValidate_RefusesHostOutsideAllowlist(t *testing.T) {
	p := writeScenario(t, "far.vl.js", `export default function () { http.get("http://example.com/"); }`)
	code, out, _ := validate(t, p)
	if code != 1 || !strings.Contains(out, "not localhost or allowlisted") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestValidate_FailedCheckDoesNotMakeItInvalid(t *testing.T) {
	srv := okServer(t)
	p := writeScenario(t, "chk.vl.js", `export default function () {
  check(http.get("`+srv.URL+`/"), {"status is 500": (r) => r.status === 500});
}`)
	code, out, _ := validate(t, p)
	if code != 0 || !strings.Contains(out, "FAIL  status is 500") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestValidate_UsageErrorsExitTwo(t *testing.T) {
	for _, args := range [][]string{{}, {"a.js", "b.js"}, {"-output", "xml", "a.js"}} {
		if code, _, _ := validate(t, args...); code != 2 {
			t.Errorf("args %v: code %d, want 2", args, code)
		}
	}
}

func TestValidate_PythonScenario(t *testing.T) {
	if _, err := os.Stat("/usr/bin/python3"); err != nil {
		t.Skip("python3 not available")
	}
	srv := okServer(t)
	p := writeScenario(t, "ok.py", `def iteration():
    r = http.get("`+srv.URL+`/")
    check(r, {"status is 200": lambda r: r.status == 200})
`)
	code, out, _ := validate(t, p)
	if code != 0 || !strings.Contains(out, "PASS  status is 200") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

// readAudit returns the audit entries written to the log at path.
func readAudit(t *testing.T, path string) []audit.Entry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading audit log: %v", err)
	}
	var out []audit.Entry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e audit.Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("audit line %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

func validateAudited(t *testing.T, scenario string) audit.Entry {
	t.Helper()
	log := filepath.Join(t.TempDir(), "audit.log")
	var out, errb bytes.Buffer
	runValidate([]string{"-audit-log", log, scenario}, &out, &errb)
	entries := readAudit(t, log)
	if len(entries) != 1 {
		t.Fatalf("want 1 audit entry, got %d", len(entries))
	}
	return entries[0]
}

func TestValidate_AuditEntry_Valid(t *testing.T) {
	srv := okServer(t)
	p := writeScenario(t, "ok.vl.js", `export default function () { http.get("`+srv.URL+`/"); }`)
	e := validateAudited(t, p)
	if e.Outcome != "success" || e.Executor != "validate" || e.VUs != 1 || e.Total != 1 || e.Failed != 0 || e.Duration <= 0 || e.ScenarioPath != p {
		t.Fatalf("entry = %+v", e)
	}
}

func TestValidate_AuditEntry_LoadFailure(t *testing.T) {
	p := writeScenario(t, "bad.vl.js", `export default function( {`)
	e := validateAudited(t, p)
	if e.Outcome != "error" || e.Total != 0 || e.Executor != "validate" || !strings.HasPrefix(e.Error, "load:") {
		t.Fatalf("entry = %+v", e)
	}
}

func TestValidate_AuditEntry_RefusedHostIsAnIterationError(t *testing.T) {
	p := writeScenario(t, "far.vl.js", `export default function () { http.get("http://example.com/"); }`)
	e := validateAudited(t, p)
	if e.Outcome != "error" || e.Total != 0 || !strings.HasPrefix(e.Error, "iteration:") || !strings.Contains(e.Error, "not localhost or allowlisted") {
		t.Fatalf("entry = %+v", e)
	}
}

func TestRecordAudit_WarningNamesTheCommand(t *testing.T) {
	// An audit path that cannot be written: a directory.
	cfg := &runConfig{AuditLogPath: t.TempDir(), Trigger: "cli"}
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	recordAudit("validate", cfg, &report.Result{}, nil)
	w.Close()
	os.Stderr = old
	buf := new(bytes.Buffer)
	buf.ReadFrom(r)
	if !strings.HasPrefix(buf.String(), "vegaload validate: warning") {
		t.Fatalf("stderr = %q", buf.String())
	}
}
