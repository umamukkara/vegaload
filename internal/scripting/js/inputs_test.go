package js

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/inputs"
	"github.com/vegaload/vegaload/internal/secrets"
)

func twoRows(t *testing.T) *inputs.Inputs {
	t.Helper()
	dir := t.TempDir()
	p := dir + "/users.csv"
	if err := os.WriteFile(p, []byte("name,city\nann,Pune\nbob,Goa\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sets, err := inputs.ParseData([]string{p})
	if err != nil {
		t.Fatal(err)
	}
	return inputs.New(map[string]string{"REGION": "south"}, sets)
}

func runWithInputs(t *testing.T, in *inputs.Inputs, src string) (*fakeRecorder, error) {
	t.Helper()
	script, err := Load(writeScript(t, "scenario.js", src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	vu, err := script.NewVU(nil, 5*time.Second, WithInputs(in))
	if err != nil {
		return nil, err
	}
	rec := &fakeRecorder{}
	vu.SetCheckRecorder(rec)
	return rec, vu.Iteration(context.Background())
}

func TestInputs_EnvAndDataAreGlobals(t *testing.T) {
	rec, err := runWithInputs(t, twoRows(t), `
export default function () {
  const a = data.users.next();
  const b = data.users.next();
  const c = data.users.next();
  check(1, {
    ["region=" + env.REGION]: true,
    ["rows=" + data.users.length]: true,
    ["order=" + a.name + "," + b.name + "," + c.name]: true,
    ["city=" + a.city]: true,
    ["random ok"]: ["ann", "bob"].indexOf(data.users.random().name) >= 0,
  });
}`)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(rec.got, " ")
	for _, want := range []string{"region=south=pass", "rows=2=pass", "order=ann,bob,ann=pass", "city=Pune=pass", "random ok=pass"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestInputs_TopLevelCodeCanReadEnv(t *testing.T) {
	rec, err := runWithInputs(t, twoRows(t), `
const region = env.REGION;
export default function () { check(1, {["top=" + region]: true}); }`)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rec.got, " "); !strings.Contains(got, "top=south=pass") {
		t.Fatalf("got %q", got)
	}
}

func TestInputs_NotExposedIsUndefinedAndNoInputsMeansEmpty(t *testing.T) {
	t.Setenv("VL_NOT_EXPOSED", "leak")
	rec, err := runWithInputs(t, nil, `
export default function () {
  check(1, {
    "not exposed": env.VL_NOT_EXPOSED === undefined,
    "no data": Object.keys(data).length === 0,
  });
}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rec.got, " "); !strings.Contains(got, "not exposed=pass") || !strings.Contains(got, "no data=pass") {
		t.Fatalf("got %q", got)
	}
}

func TestConsoleLog_RedactsSecrets(t *testing.T) {
	secrets.Reset()
	t.Cleanup(secrets.Reset)
	secrets.Register("tok-12345")

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	_, err := runWithInputs(t, nil, `export default function () { console.log("key is tok-12345 ok"); }`)
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "tok-12345") || !strings.Contains(string(out), "key is [redacted] ok") {
		t.Fatalf("console output = %q", out)
	}
}

func TestInputs_ChangingARowDoesNotChangeTheNextOne(t *testing.T) {
	rec, err := runWithInputs(t, twoRows(t), `
export default function () {
  const a = data.users.next();
  a.name = "changed";
  data.users.next();
  const again = data.users.next(); // wraps to the first row
  check(1, { ["again=" + again.name]: true });
}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rec.got, " "); !strings.Contains(got, "again=ann=pass") {
		t.Fatalf("got %q", got)
	}
}
