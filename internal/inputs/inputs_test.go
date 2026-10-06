package inputs

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/vegaload/vegaload/internal/secrets"
)

func write(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadSet_CSVRowsAreStringsKeyedByHeader(t *testing.T) {
	s, err := LoadSet("users", write(t, "users.csv", "name,city\nann,Pune\nbob,\"New Delhi, IN\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Len() != 2 || s.rows[1]["city"] != "New Delhi, IN" || s.rows[0]["name"] != "ann" {
		t.Fatalf("rows = %v", s.rows)
	}
}

func TestLoadSet_CSVWithBOMAndSpacedHeader(t *testing.T) {
	s, err := LoadSet("u", write(t, "u.csv", "\xef\xbb\xbfname , city\nann,Pune\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s.rows[0]["name"] != "ann" || s.rows[0]["city"] != "Pune" {
		t.Fatalf("rows = %v", s.rows)
	}
}

func TestLoadSet_JSONKeepsTypes(t *testing.T) {
	s, err := LoadSet("u", write(t, "u.json", `[{"id":1,"ok":true,"tags":["a"]},{"id":2}]`))
	if err != nil {
		t.Fatal(err)
	}
	if s.Len() != 2 || s.rows[0]["id"] != float64(1) || s.rows[0]["ok"] != true {
		t.Fatalf("rows = %v", s.rows)
	}
}

func TestLoadSet_Errors(t *testing.T) {
	cases := map[string]struct{ name, body, want string }{
		"empty csv":        {"a.csv", "", "no rows"},
		"header only":      {"a.csv", "name,city\n", "no rows"},
		"ragged row":       {"a.csv", "a,b\n1\n", "line 2 has 1 fields"},
		"blank header":     {"a.csv", "a,\n1,2\n", "column 2"},
		"json not objects": {"a.json", `[1,2]`, "array of objects"},
		"json not array":   {"a.json", `{"a":1}`, "array of objects"},
		"empty json array": {"a.json", `[]`, "no rows"},
		"wrong extension":  {"a.txt", "x", ".csv or .json"},
	}
	for name, c := range cases {
		_, err := LoadSet("a", write(t, c.name, c.body))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to contain %q", name, err, c.want)
		}
	}
	if _, err := LoadSet("a", filepath.Join(t.TempDir(), "missing.csv")); err == nil {
		t.Error("a missing file should be an error")
	}
}

func TestSet_NextIsInOrderAndWraps(t *testing.T) {
	s, _ := LoadSet("u", write(t, "u.csv", "n\n1\n2\n3\n"))
	var got []string
	for i := 0; i < 7; i++ {
		got = append(got, s.Next()["n"].(string))
	}
	if strings.Join(got, "") != "1231231" {
		t.Fatalf("got %v", got)
	}
}

func TestSet_NextGivesEachCallerADifferentRow(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("n\n")
	for i := 0; i < 200; i++ {
		sb.WriteString("r" + string(rune('A'+i%26)) + string(rune('a'+i/26)) + "\n")
	}
	s, _ := LoadSet("u", write(t, "u.csv", sb.String()))
	seen := sync.Map{}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				seen.Store(s.Next()["n"], true)
			}
		}()
	}
	wg.Wait()
	n := 0
	seen.Range(func(_, _ any) bool { n++; return true })
	if n != 200 {
		t.Fatalf("8 callers x 25 calls should read all 200 rows exactly once, saw %d distinct", n)
	}
}

func TestSet_RandomReturnsRowsFromTheFile(t *testing.T) {
	s, _ := LoadSet("u", write(t, "u.csv", "n\na\nb\n"))
	for i := 0; i < 50; i++ {
		if n := s.Random()["n"]; n != "a" && n != "b" {
			t.Fatalf("got %v", n)
		}
	}
}

func TestParseData_NamingAndErrors(t *testing.T) {
	dir := t.TempDir()
	users := filepath.Join(dir, "users.csv")
	os.WriteFile(users, []byte("a\n1\n"), 0o644)
	other := filepath.Join(dir, "other.json")
	os.WriteFile(other, []byte(`[{"a":1}]`), 0o644)
	odd := filepath.Join(dir, "my-users.csv")
	os.WriteFile(odd, []byte("a\n1\n"), 0o644)

	got, err := ParseData([]string{users, "o=" + other})
	if err != nil || got["users"] == nil || got["o"] == nil || len(got) != 2 {
		t.Fatalf("err=%v got=%v", err, got)
	}
	if _, err := ParseData([]string{odd}); err == nil || !strings.Contains(err.Error(), "name=path") {
		t.Errorf("a file name that is not an identifier needs a name, err = %v", err)
	}
	if got, err := ParseData([]string{"people=" + odd}); err != nil || got["people"] == nil {
		t.Errorf("naming it fixes that: err=%v", err)
	}
	if _, err := ParseData([]string{users, "users=" + other}); err == nil || !strings.Contains(err.Error(), "two files") {
		t.Errorf("duplicate names, err = %v", err)
	}
}

func TestResolveEnv_PlainSecretAndErrors(t *testing.T) {
	secrets.Reset()
	t.Cleanup(secrets.Reset)
	t.Setenv("VL_TEST_PLAIN", "visible")
	t.Setenv("VL_TEST_KEY", "s3cr3t-value")
	t.Setenv("VL_TEST_OTHER", "also-visible")

	got, err := ResolveEnv([]string{"VL_TEST_PLAIN, VL_TEST_OTHER"}, []string{"VL_TEST_KEY"})
	if err != nil || got["VL_TEST_PLAIN"] != "visible" || got["VL_TEST_OTHER"] != "also-visible" || got["VL_TEST_KEY"] != "s3cr3t-value" {
		t.Fatalf("err=%v got=%v", err, got)
	}
	if out := secrets.Redact("a visible s3cr3t-value b"); out != "a visible [redacted] b" {
		t.Errorf("only the secret is redacted, got %q", out)
	}
	if _, err := ResolveEnv([]string{"VL_TEST_NOT_SET_ANYWHERE"}, nil); err == nil || !strings.Contains(err.Error(), "not set") {
		t.Errorf("unset variable, err = %v", err)
	}
	if _, err := ResolveEnv([]string{"bad-name"}, nil); err == nil {
		t.Error("a bad variable name should be an error")
	}
}

func TestNew_NilWhenNothingToExpose(t *testing.T) {
	if New(nil, nil) != nil {
		t.Fatal("no variables and no files should give nil")
	}
	in := New(map[string]string{"A": "1"}, nil)
	if v, ok := in.Env("A"); !ok || v != "1" {
		t.Fatalf("Env = %q %v", v, ok)
	}
	if _, ok := in.Env("B"); ok {
		t.Fatal("B was not exposed")
	}
	if _, ok := in.Dataset("x"); ok {
		t.Fatal("no data files")
	}
	if names := in.EnvNames(); len(names) != 1 || names[0] != "A" {
		t.Fatalf("EnvNames = %v", names)
	}
}

func TestNilInputsExposeNothing(t *testing.T) {
	var in *Inputs
	if _, ok := in.Env("A"); ok || in.EnvNames() != nil || in.DatasetNames() != nil {
		t.Fatal("a nil *Inputs should expose nothing")
	}
	if _, ok := in.Dataset("x"); ok {
		t.Fatal("a nil *Inputs has no data files")
	}
}

func TestSet_RowsAreCopiesSoAScriptCannotChangeTheFile(t *testing.T) {
	s, err := LoadSet("u", write(t, "u.json", `[{"name":"ann","tags":["a","b"],"addr":{"city":"Pune"}}]`))
	if err != nil {
		t.Fatal(err)
	}
	for _, get := range []func() map[string]any{s.Next, s.Random} {
		row := get()
		row["name"] = "changed"
		row["tags"].([]any)[0] = "changed"
		row["addr"].(map[string]any)["city"] = "changed"

		again := get()
		if again["name"] != "ann" || again["tags"].([]any)[0] != "a" || again["addr"].(map[string]any)["city"] != "Pune" {
			t.Fatalf("a change to one row leaked into the next draw: %v", again)
		}
	}
}

func TestSet_ConcurrentWritersDoNotRace(t *testing.T) {
	s, _ := LoadSet("u", write(t, "u.json", `[{"n":"a","x":{"y":1}}]`))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				row := s.Next()
				row["n"] = "w"
				row["x"].(map[string]any)["y"] = i
			}
		}()
	}
	wg.Wait() // run with -race
}

func TestLoadSet_NullJSONItemIsRejected(t *testing.T) {
	for _, body := range []string{`[null]`, `[{"a":1},null]`} {
		_, err := LoadSet("u", write(t, "u.json", body))
		if err == nil || !strings.Contains(err.Error(), "null") {
			t.Errorf("%s: err = %v, want it to mention null", body, err)
		}
	}
}
