package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shopHAR returns the sample recording with its host replaced by the test
// server, over plain http, and writes it to a file.
func shopHAR(t *testing.T, srvURL string) string {
	t.Helper()
	data, err := os.ReadFile("../../internal/har/testdata/shop.har")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(data), "https://HOST", srvURL)
	text = strings.ReplaceAll(text, "HOST", strings.TrimPrefix(srvURL, "http://"))
	p := filepath.Join(t.TempDir(), "shop.har")
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// shopServer answers the way the sample recording expects, and refuses a
// request that is missing a secret the scenario must have supplied.
func shopServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html></html>")) })
	mux.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var in map[string]any
		if json.Unmarshal(b, &in) != nil || in["password"] != "s3cret-pw" || in["plan"] != nil {
			http.Error(w, "bad login body: "+string(b), 400)
			return
		}
		w.Write([]byte(`{"token":"t"}`))
	})
	mux.HandleFunc("/api/orders", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			if r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("Cookie") != "sid=1" ||
				r.URL.Query().Get("api_key") != "k e/y" {
				http.Error(w, "missing secret", 401)
				return
			}
			w.Write([]byte("[]"))
			return
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != "sku=A1&qty=2&csrf_token=c%2Ft" {
			http.Error(w, "bad form body: "+string(b), 400)
			return
		}
		w.WriteHeader(201)
		w.Write([]byte("{}"))
	})
	mux.HandleFunc("/api/orders/42", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/", 302) })
	mux.HandleFunc("/api/upload", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("{}")) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestImportHAR_TheScenarioRuns(t *testing.T) {
	srv := shopServer(t)
	har := shopHAR(t, srv.URL)
	out := filepath.Join(t.TempDir(), "shop.vl.js")
	if code := cmdImport([]string{"har", har, "-o", out}); code != 0 {
		t.Fatalf("import exit %d", code)
	}
	script, _ := os.ReadFile(out)
	if strings.Contains(string(script), "s3cret-pw") {
		t.Fatal("a secret is in the file")
	}

	// Without the secrets the scenario says what to set.
	code, o1, e1 := validate(t, out)
	if code == 0 || !strings.Contains(o1+e1, "-secret-env VL_") {
		t.Fatalf("a scenario without its secrets must fail and say what to set (exit %d)\nout: %s\nerr: %s", code, o1, e1)
	}

	t.Setenv("VL_API_KEY", "k e/y")
	t.Setenv("VL_AUTHORIZATION", "Bearer tok")
	t.Setenv("VL_COOKIE", "sid=1")
	t.Setenv("VL_CSRF_TOKEN", "c/t")
	t.Setenv("VL_PASSWORD", "s3cret-pw")
	code, outText, errText := validate(t, "-secret-env", "VL_API_KEY,VL_AUTHORIZATION,VL_COOKIE,VL_CSRF_TOKEN,VL_PASSWORD", out)
	if code != 0 {
		t.Fatalf("validate exit %d\nout: %s\nerr: %s\nscript:\n%s", code, outText, errText, script)
	}
}

func TestImportHAR_FlagsMayComeAfterTheFile(t *testing.T) {
	srv := shopServer(t)
	har := shopHAR(t, srv.URL)
	out := filepath.Join(t.TempDir(), "x.vl.js")
	if code := cmdImport([]string{"har", har, "-max", "1", "-o", out}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	b, _ := os.ReadFile(out)
	if !strings.Contains(string(b), "1 of 12 recorded requests") {
		t.Errorf("-max after the file name was ignored:\n%s", b)
	}
}

func TestImportHAR_DefaultNameAndOverwrite(t *testing.T) {
	srv := shopServer(t)
	har := shopHAR(t, srv.URL)
	dir := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	if code := cmdImport([]string{"har", har}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "shop.vl.js")); err != nil {
		t.Fatalf("the default file name should be shop.vl.js: %v", err)
	}
	if code := cmdImport([]string{"har", har}); code != 1 {
		t.Errorf("an existing file must not be overwritten (exit %d)", code)
	}
	if code := cmdImport([]string{"har", har, "-force"}); code != 0 {
		t.Errorf("-force should overwrite (exit %d)", code)
	}
}

func TestImportHAR_JSONOutput(t *testing.T) {
	srv := shopServer(t)
	har := shopHAR(t, srv.URL)
	out := filepath.Join(t.TempDir(), "j.vl.js")
	r, w, _ := os.Pipe()
	stdout := os.Stdout
	os.Stdout = w
	code := cmdImport([]string{"har", har, "-o", out, "-output", "json"})
	w.Close()
	os.Stdout = stdout
	data, _ := io.ReadAll(r)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var doc struct {
		Requests int      `json:"requests"`
		Env      []string `json:"env"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || doc.Requests != 7 || len(doc.Env) != 5 {
		t.Errorf("doc = %+v, err = %v, raw = %q", doc, err, data)
	}
}

func TestImportHAR_UsageErrors(t *testing.T) {
	srv := shopServer(t)
	har := shopHAR(t, srv.URL)
	notHAR := writeScenario(t, "x.har", "nope")
	empty := writeScenario(t, "empty.har", `{"log":{"entries":[]}}`)
	cases := map[string][]string{
		"no format":      {},
		"unknown format": {"curl", har},
		"no file":        {"har"},
		"two files":      {"har", har, har},
		"missing file":   {"har", filepath.Join(t.TempDir(), "nope.har")},
		"not a HAR":      {"har", notHAR},
		"nothing left":   {"har", empty},
		"bad output":     {"har", har, "-output", "xml"},
		"negative max":   {"har", har, "-max", "-1"},
	}
	for name, args := range cases {
		if code := cmdImport(args); code == 0 {
			t.Errorf("%s: exit 0, want an error", name)
		}
	}
}
