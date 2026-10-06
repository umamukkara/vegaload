package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/vegaload/vegaload/internal/report"
	"github.com/vegaload/vegaload/internal/secrets"
)

const apiKey = "sk-live-4f9a1c7e"

// recordingServer remembers each request's body and API key header.
type recordingServer struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []string
	keys   []string
}

func newRecordingServer(t *testing.T) *recordingServer {
	t.Helper()
	rs := &recordingServer{}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rs.mu.Lock()
		rs.bodies = append(rs.bodies, string(b))
		rs.keys = append(rs.keys, r.Header.Get("X-Api-Key"))
		rs.mu.Unlock()
	}))
	t.Cleanup(rs.Close)
	return rs
}

func writeData(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "users.csv")
	if err := os.WriteFile(p, []byte("name\nann\nbob\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func inputsScenario(url string) string {
	return `export default function () {
  const row = data.users.next();
  const res = http.post("` + url + `/", {
    body: JSON.stringify({ name: row.name, region: env.REGION }),
    headers: { "X-Api-Key": env.API_KEY },
  });
  check(res, { ["status ok for key " + env.API_KEY]: (r) => r.status === 200 });
}`
}

func TestCmdRun_DataAndEnvFeedTheScenario(t *testing.T) {
	secrets.Reset()
	t.Cleanup(secrets.Reset)
	t.Setenv("REGION", "south")
	t.Setenv("API_KEY", apiKey)
	rs := newRecordingServer(t)

	dir := t.TempDir()
	scenario := filepath.Join(dir, "s.vl.js")
	os.WriteFile(scenario, []byte(inputsScenario(rs.URL)), 0o644)
	out := filepath.Join(dir, "summary.json")
	auditPath := filepath.Join(dir, "audit.log")
	code := cmdRun([]string{"-vus", "2", "-duration", "300ms", "-no-report", "-audit-log", auditPath, "-out", out,
		"-data", writeData(t), "-env", "REGION", "-secret-env", "API_KEY", scenario})
	if code != 0 {
		t.Fatalf("exit %d", code)
	}

	rs.mu.Lock()
	defer rs.mu.Unlock()
	if len(rs.bodies) < 4 {
		t.Fatalf("expected several requests, got %d", len(rs.bodies))
	}
	// Two users send at the same time, so arrival order is not row order.
	// Rows are handed out in turn, so the two names stay about even.
	counts := map[string]int{}
	for i, b := range rs.bodies {
		name := "ann"
		if strings.Contains(b, `"name":"bob"`) {
			name = "bob"
		} else if !strings.Contains(b, `"name":"ann"`) {
			t.Fatalf("request %d body = %s, want a name from the data file", i, b)
		}
		counts[name]++
		if !strings.Contains(b, `"region":"south"`) {
			t.Fatalf("request %d body = %s, want region south", i, b)
		}
		if rs.keys[i] != apiKey {
			t.Fatalf("the script must still get the real key, request %d sent %q", i, rs.keys[i])
		}
	}
	if d := counts["ann"] - counts["bob"]; counts["ann"] == 0 || counts["bob"] == 0 || d > 2 || d < -2 {
		t.Fatalf("rows should alternate, got %v", counts)
	}

	// The secret must not appear in anything VegaLoad wrote.
	for _, p := range []string{out, auditPath} {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), apiKey) {
			t.Errorf("%s contains the secret:\n%s", filepath.Base(p), data)
		}
	}
	var res report.Result
	data, _ := os.ReadFile(out)
	_ = json.Unmarshal(data, &res)
	if len(res.Checks) != 1 || res.Checks[0].Name != "status ok for key [redacted]" {
		t.Errorf("checks = %+v", res.Checks)
	}
}

func TestCmdValidate_ErrorTextHasSecretsRemoved(t *testing.T) {
	secrets.Reset()
	t.Cleanup(secrets.Reset)
	t.Setenv("API_KEY", apiKey)
	p := writeScenario(t, "leak.vl.js", `export default function () { throw new Error("rejected key " + env.API_KEY); }`)
	auditPath := filepath.Join(t.TempDir(), "audit.log")
	code, out, _ := validate(t, "-audit-log", auditPath, "-secret-env", "API_KEY", "-output", "json", p)
	if code != 1 || strings.Contains(out, apiKey) || !strings.Contains(out, "rejected key [redacted]") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if data, _ := os.ReadFile(auditPath); strings.Contains(string(data), apiKey) || !strings.Contains(string(data), "[redacted]") {
		t.Fatalf("audit = %s", data)
	}
}

func TestCmdValidate_PythonReadsDataAndEnv(t *testing.T) {
	if _, err := os.Stat("/usr/bin/python3"); err != nil {
		t.Skip("python3 not available")
	}
	secrets.Reset()
	t.Cleanup(secrets.Reset)
	t.Setenv("REGION", "south")
	p := writeScenario(t, "in.py", `def iteration():
    row = data.users.next()
    check(row, {"name is ann": lambda r: r["name"] == "ann", "region is south": lambda r: env.REGION == "south"})
`)
	code, out, _ := validate(t, "-data", writeData(t), "-env", "REGION", p)
	if code != 0 || !strings.Contains(out, "PASS  name is ann") || !strings.Contains(out, "PASS  region is south") {
		t.Fatalf("code=%d out=%s", code, out)
	}
}

func TestInputsFlags_UsageErrorsExitTwo(t *testing.T) {
	secrets.Reset()
	t.Cleanup(secrets.Reset)
	t.Setenv("PRESENT_VAR", "x")
	good := writeData(t)
	scenario := writeScenario(t, "s.vl.js", `export default function () {}`)
	cases := map[string][]string{
		"unset -env":            {"-env", "VL_NEVER_SET_VAR", scenario},
		"unset -secret-env":     {"-secret-env", "VL_NEVER_SET_VAR", scenario},
		"missing data file":     {"-data", filepath.Join(t.TempDir(), "nope.csv"), scenario},
		"bad data extension":    {"-data", writeScenario(t, "d.txt", "x"), scenario},
		"data without scenario": {"-data", good, "-target", "http://127.0.0.1:1", "-protocol", "http1"},
		"env without scenario":  {"-env", "PRESENT_VAR", "-target", "http://127.0.0.1:1", "-protocol", "http1"},
	}
	for name, args := range cases {
		if code := cmdRun(append([]string{"-no-report", "-audit-log", filepath.Join(t.TempDir(), "a.log")}, args...)); code != 2 {
			t.Errorf("run, %s: exit %d, want 2", name, code)
		}
	}
	if code, _, _ := validate(t, "-env", "VL_NEVER_SET_VAR", scenario); code != 2 {
		t.Errorf("validate with an unset variable: exit %d, want 2", code)
	}
}
