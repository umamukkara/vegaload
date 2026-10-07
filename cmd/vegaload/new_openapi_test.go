package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/vegaload/vegaload/internal/report"
)

// itemsServer is a tiny API with the shape of the spec below. Reading,
// updating or deleting an id it never created is a 404, so a scenario that
// does not carry the created id into the later calls fails.
func itemsServer(t *testing.T) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	items := map[int]bool{}
	next := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("X-Api-Key") != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var id int
		switch {
		case r.URL.Path == "/items" && r.Method == http.MethodPost:
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["name"] == nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			next++
			items[next] = true
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"id": %d, "name": "x"}`, next)
		case r.URL.Path == "/items" && r.Method == http.MethodGet:
			if r.URL.Query().Get("limit") == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			fmt.Fprint(w, `[]`)
		case strings.HasPrefix(r.URL.Path, "/items/"):
			if _, err := fmt.Sscanf(r.URL.Path, "/items/%d", &id); err != nil || !items[id] {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if r.Method == http.MethodDelete {
				delete(items, id)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			fmt.Fprint(w, `{}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func itemsSpec(serverURL string) string {
	return `{
  "info": {"title": "Items"},
  "servers": [{"url": "` + serverURL + `"}],
  "security": [{"key": []}],
  "components": {"securitySchemes": {"key": {"type": "apiKey", "in": "header", "name": "X-Api-Key"}}},
  "paths": {
    "/items": {
      "get": {"operationId": "listItems", "parameters": [{"name": "limit", "in": "query", "required": true, "schema": {"type": "integer"}}]},
      "post": {"operationId": "createItem", "responses": {"201": {"description": "created"}},
        "requestBody": {"content": {"application/json": {"schema": {"type": "object", "properties": {"name": {"type": "string"}}}}}}}
    },
    "/items/{itemId}": {
      "get": {"operationId": "getItem", "parameters": [{"name": "itemId", "in": "path", "required": true, "schema": {"type": "integer"}}]},
      "delete": {"operationId": "deleteItem", "parameters": [{"name": "itemId", "in": "path", "required": true, "schema": {"type": "integer"}}]}
    }
  }
}`
}

func TestCmdNew_FromOpenAPI_WritesBothFiles(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd) //nolint:errcheck
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(dir, "spec.json")
	if err := os.WriteFile(spec, []byte(itemsSpec("http://127.0.0.1:1")), 0o644); err != nil {
		t.Fatal(err)
	}

	if code := cmdNew([]string{"-from-openapi", spec, "shop"}); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	js, err := os.ReadFile("shop.vl.js")
	if err != nil || !strings.Contains(string(js), `step("createItem"`) {
		t.Fatalf("shop.vl.js: %v\n%s", err, js)
	}
	if _, err := os.Stat("shop.vegaload-plan.md"); err != nil {
		t.Errorf("the runbook should still be written: %v", err)
	}

	// Without -force, neither file is overwritten.
	if code := cmdNew([]string{"-from-openapi", spec, "shop"}); code == 0 {
		t.Error("a second run without -force should refuse")
	}
	// -python writes a Python scenario, and a name with an extension still
	// gives the runbook a clean name.
	if code := cmdNew([]string{"-from-openapi", spec, "-python", "-force", "shop2.py"}); code != 0 {
		t.Fatalf("-python exit code %d", code)
	}
	if _, err := os.Stat("shop2.py"); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat("shop2.vegaload-plan.md"); err != nil {
		t.Error(err)
	}
}

// The generated scenario runs against a real server and passes: the id the
// create returned reaches the get and the delete (FR-CLI-16).
func TestCmdRun_GeneratedScenario_JavaScript(t *testing.T) {
	runGenerated(t, false)
}

func TestCmdRun_GeneratedScenario_Python(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	runGenerated(t, true)
}

func runGenerated(t *testing.T, python bool) {
	t.Helper()
	srv := itemsServer(t)
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd) //nolint:errcheck
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(dir, "spec.json")
	if err := os.WriteFile(spec, []byte(itemsSpec(srv.URL)), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"-from-openapi", spec}
	scenario := "shop.vl.js"
	if python {
		args = append(args, "-python")
		scenario = "shop.py"
	}
	args = append(args, "shop") // flags go before the name
	if code := cmdNew(args); code != 0 {
		t.Fatalf("cmdNew exit code %d", code)
	}

	out := filepath.Join(dir, "summary.json")
	_ = cmdRun([]string{
		"-vus", "2", "-duration", "500ms", "-no-report",
		"-audit-log", filepath.Join(dir, "audit.log"), "-out", out,
		scenario,
	})
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("no summary: %v", err)
	}
	var res report.Result
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatal(err)
	}
	// Without the key the server answers 401 and the scenario fails at its
	// first step. That proves the credential is needed and is passed.
	if res.Failed == 0 {
		t.Fatal("without API_KEY every iteration should fail with 401")
	}

	t.Setenv("API_KEY", "secret")
	code := cmdRun([]string{
		"-vus", "2", "-duration", "500ms", "-no-report", "-secret-env", "API_KEY",
		"-audit-log", filepath.Join(dir, "audit.log"), "-out", out,
		"-threshold", "error_rate < 1%", "-threshold", `error_rate{step="getItem"} < 1%`,
		"-threshold", `failed{step="deleteItem"} < 1`,
		scenario,
	})
	if code != 0 {
		data, _ := os.ReadFile(out)
		t.Fatalf("exit code = %d, want 0\n%s", code, data)
	}
	data, _ = os.ReadFile(out)
	res = report.Result{}
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"createItem", "listItems", "getItem", "deleteItem"} {
		if s, ok := res.Step(name); !ok || s.Total == 0 || s.Failed != 0 {
			t.Errorf("step %s = %+v, %v", name, s, ok)
		}
	}
}
