package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vegaload/vegaload/internal/report"
)

func TestCmdRun_NamedSteps_EndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	scenario := filepath.Join(dir, "steps.vl.js")
	src := `export default function () {
  step("login", () => { http.get("` + srv.URL + `/ok"); });
  step("checkout", () => {
    const r = http.get("` + srv.URL + `/bad");
    if (r.status !== 200) throw new Error("status " + r.status);
  });
}`
	if err := os.WriteFile(scenario, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "summary.json")
	code := cmdRun([]string{
		"-vus", "2", "-duration", "300ms", "-no-report",
		"-audit-log", filepath.Join(dir, "audit.log"), "-out", out,
		"-threshold", `p95{step="login"} < 10s`,
		"-threshold", `error_rate{step="checkout"} < 1%`,
		scenario,
	})
	if code != 3 {
		t.Fatalf("exit code = %d, want 3 (the checkout step always fails)", code)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var res report.Result
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatal(err)
	}
	login, ok := res.Step("login")
	if !ok || login.Total == 0 || login.Failed != 0 {
		t.Errorf("login = %+v, %v", login, ok)
	}
	co, ok := res.Step("checkout")
	if !ok || co.Total == 0 || co.Failed != co.Total || co.ErrorRate != 1 {
		t.Errorf("checkout = %+v, %v", co, ok)
	}
	if len(res.Thresholds) != 2 || !res.Thresholds[0].Passed || res.Thresholds[1].Passed {
		t.Errorf("thresholds = %+v", res.Thresholds)
	}
	if res.Thresholds[0].Step != "login" {
		t.Errorf("threshold step = %q", res.Thresholds[0].Step)
	}

	var buf bytes.Buffer
	printResult(&buf, &res)
	if s := buf.String(); !strings.Contains(s, "steps\n") || !strings.Contains(s, "login") || !strings.Contains(s, "checkout") {
		t.Errorf("text summary lacks steps:\n%s", s)
	}
}
