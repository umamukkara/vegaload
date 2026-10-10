package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	var token, user string
	mux.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var in map[string]any
		if json.Unmarshal(b, &in) != nil || in["password"] != "s3cret-pw" || in["plan"] != nil {
			http.Error(w, "bad login body: "+string(b), 400)
			return
		}
		token = "fresh-" + fmt.Sprint(time.Now().UnixNano())
		user = "user-" + fmt.Sprint(time.Now().UnixNano())
		fmt.Fprintf(w, `{"token":%q,"userId":%q}`, token, user)
	})
	mux.HandleFunc("/api/orders", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			if r.Header.Get("Authorization") != "Bearer "+token || r.URL.Query().Get("user") != user ||
				r.Header.Get("Cookie") != "sid=1" || r.URL.Query().Get("api_key") != "k e/y" ||
				strings.Contains(r.Header.Get("Authorization"), "eyJ") ||
				r.URL.Query().Get("user") == "550e8400-e29b-41d4-a716-446655440000" {
				http.Error(w, "missing carried value", 401)
				return
			}
			w.Write([]byte("[]"))
			return
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != "sku=A1&qty=2&csrf_token=zzz" {
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
	t.Setenv("VL_COOKIE", "sid=1")
	t.Setenv("VL_PASSWORD", "s3cret-pw")
	code, outText, errText := validate(t, "-secret-env", "VL_API_KEY,VL_COOKIE,VL_PASSWORD", out)
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
	if err := json.Unmarshal(data, &doc); err != nil || doc.Requests != 7 || len(doc.Env) != 3 {
		t.Errorf("doc = %+v, err = %v, raw = %q", doc, err, data)
	}
}

func TestImportHAR_CarriesFreshPageValues(t *testing.T) {
	var rid, sid, view string
	mux := http.NewServeMux()
	mux.HandleFunc("/form", func(w http.ResponseWriter, r *http.Request) {
		rid = "7c9e6679-7425-40de-944b-" + fmt.Sprintf("%012d", time.Now().UnixNano()%1e12)
		sid = "sid-" + fmt.Sprint(time.Now().UnixNano())
		view = "view-" + fmt.Sprint(time.Now().UnixNano())
		w.Header().Add("Set-Cookie", "sid="+sid+"; Path=/")
		w.Header().Add("Set-Cookie", "theme=dark; Expires=Wed, 21 Oct 2015 07:28:00 GMT")
		w.Header().Set("X-Request-Id", rid)
		fmt.Fprintf(w, `<form><input id="viewstate" type="hidden" value="%s"></form>`, view)
	})
	mux.HandleFunc("/save", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.URL.Query().Get("rid") != rid || !strings.Contains(r.Header.Get("Cookie"), "sid="+sid) ||
			string(b) != "viewstate="+view || strings.Contains(string(b), "recordedviewstatevalue0001") ||
			strings.Contains(r.Header.Get("Cookie"), "recorded-session-value-0001") ||
			r.URL.Query().Get("rid") == "7c9e6679-7425-40de-944b-e07fc1f90ae7" {
			http.Error(w, "not the fresh values: "+r.URL.RawQuery+" cookie="+r.Header.Get("Cookie")+" body="+string(b), 400)
			return
		}
		w.Write([]byte("ok"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	viewRec := "recordedviewstatevalue0001"
	sidRec := "recorded-session-value-0001"
	ridRec := "7c9e6679-7425-40de-944b-e07fc1f90ae7"
	har := `{"log":{"entries":[
		{"_resourceType":"document","request":{"method":"GET","url":"` + srv.URL + `/form","headers":[]},
		 "response":{"status":200,"headers":[
		   {"name":"x-request-id","value":"` + ridRec + `"},
		   {"name":"set-cookie","value":"sid=` + sidRec + `; Path=/"},
		   {"name":"set-cookie","value":"theme=dark; Expires=Wed, 21 Oct 2015 07:28:00 GMT"}
		 ],"content":{"mimeType":"text/html","text":"<form><input id=\"viewstate\" type=\"hidden\" value=\"` + viewRec + `\"></form>"}}},
		{"_resourceType":"xhr","request":{"method":"POST","url":"` + srv.URL + `/save?rid=` + ridRec + `","headers":[
		   {"name":"Cookie","value":"sid=` + sidRec + `; theme=dark"}
		 ],"postData":{"mimeType":"application/x-www-form-urlencoded","text":"viewstate=` + viewRec + `"}},
		 "response":{"status":200,"content":{"mimeType":"text/plain","text":"ok"}}}
	]}}`
	path := filepath.Join(t.TempDir(), "page.har")
	if err := os.WriteFile(path, []byte(har), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "page.vl.js")
	if code := cmdImport([]string{"har", path, "-o", out}); code != 0 {
		t.Fatalf("import exit %d", code)
	}
	script, _ := os.ReadFile(out)
	if !strings.Contains(string(script), `header(r1, "x-request-id")`) || !strings.Contains(string(script), `hidden(r1.body, "viewstate")`) {
		t.Fatalf("helpers were not generated:\n%s", script)
	}
	t.Setenv("VL_COOKIE", "theme=dark")
	code, outText, errText := validate(t, "-secret-env", "VL_COOKIE", out)
	if code != 0 {
		t.Fatalf("validate exit %d\nout: %s\nerr: %s\nscript:\n%s", code, outText, errText, script)
	}
}

func TestImportHAR_NothingToCarryStillValidates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	id := "550e8400-e29b-41d4-a716-446655440000"
	har := `{"log":{"entries":[{"_resourceType":"xhr","request":{"method":"GET","url":"` + srv.URL + `/item/` + id + `","headers":[]},"response":{"status":200,"content":{"mimeType":"text/plain","text":"ok"}}}]}}`
	path := filepath.Join(t.TempDir(), "one.har")
	if err := os.WriteFile(path, []byte(har), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "one.vl.js")
	if code := cmdImport([]string{"har", path, "-o", out}); code != 0 {
		t.Fatalf("import exit %d", code)
	}
	script, _ := os.ReadFile(out)
	if !strings.Contains(string(script), id) || !strings.Contains(string(script), "TODO") || strings.Contains(string(script), "const c1_") {
		t.Fatalf("an uncarried value should stay, with a TODO:\n%s", script)
	}
	code, outText, errText := validate(t, out)
	if code != 0 {
		t.Fatalf("validate exit %d\nout: %s\nerr: %s", code, outText, errText)
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
