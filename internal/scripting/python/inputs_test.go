package python

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/inputs"
)

func twoRows(t *testing.T) *inputs.Inputs {
	t.Helper()
	p := t.TempDir() + "/users.csv"
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
	skipIfNoPython(t)
	script, err := Load(writeScript(t, src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	vu, err := script.NewVU(nil, 5*time.Second, WithInputs(in))
	if err != nil {
		return nil, err
	}
	defer vu.Close()
	rec := &fakeRecorder{}
	vu.SetCheckRecorder(rec)
	return rec, vu.Iteration(context.Background())
}

func TestInputs_EnvAndDataAreGlobals(t *testing.T) {
	rec, err := runWithInputs(t, twoRows(t), `
def iteration():
    a = data.users.next()
    b = data.users.next()
    c = data.users.next()
    check(1, {
        "region=" + env.REGION: True,
        "get=" + env.get("REGION") + env.get("NOPE", "-"): True,
        "item=" + env["REGION"]: True,
        "rows=%d" % len(data.users): True,
        "order=%s,%s,%s" % (a["name"], b["name"], c["name"]): True,
        "random ok": data.users.random()["name"] in ("ann", "bob"),
    })
`)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(rec.got, " ")
	for _, want := range []string{"region=south=pass", "get=south-=pass", "item=south=pass", "rows=2=pass", "order=ann,bob,ann=pass", "random ok=pass"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestInputs_TopLevelCodeCanReadEnv(t *testing.T) {
	rec, err := runWithInputs(t, twoRows(t), `
region = env.REGION

def iteration():
    check(1, {"top=" + region: True})
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rec.got, " "); !strings.Contains(got, "top=south=pass") {
		t.Fatalf("got %q", got)
	}
}

func TestInputs_NotExposedAndUnknownDataFile(t *testing.T) {
	t.Setenv("VL_NOT_EXPOSED", "leak")
	_, err := runWithInputs(t, nil, `
def iteration():
    env.VL_NOT_EXPOSED
`)
	if err == nil || !strings.Contains(err.Error(), "-env or -secret-env") {
		t.Fatalf("an unexposed variable should say how to expose it, err = %v", err)
	}
	_, err = runWithInputs(t, twoRows(t), `
def iteration():
    data.nope.next()
`)
	if err == nil || !strings.Contains(err.Error(), "-data") {
		t.Fatalf("an unknown data file should say how to add it, err = %v", err)
	}
}

func TestInputs_HTTPAtImportTimeIsRefusedNotFatal(t *testing.T) {
	_, err := runWithInputs(t, nil, `
try:
    http.get("http://127.0.0.1:1/")
    loaded = "no"
except Exception as e:
    loaded = "refused"

def iteration():
    assert loaded == "refused", loaded
`)
	if err != nil {
		t.Fatal(err)
	}
}
