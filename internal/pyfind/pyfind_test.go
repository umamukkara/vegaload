package pyfind

import (
	"errors"
	"reflect"
	"testing"
)

func lookup(have ...string) func(string) (string, error) {
	set := map[string]bool{}
	for _, h := range have {
		set[h] = true
	}
	return func(name string) (string, error) {
		if set[name] {
			return `C:\bin\` + name + ".exe", nil
		}
		return "", errors.New("not found")
	}
}

func TestNames(t *testing.T) {
	if got := Names("linux"); !reflect.DeepEqual(got, []string{"python3"}) {
		t.Errorf("linux names = %v", got)
	}
	if got := Names("darwin"); !reflect.DeepEqual(got, []string{"python3"}) {
		t.Errorf("darwin names = %v", got)
	}
	if got := Names("windows"); !reflect.DeepEqual(got, []string{"python3", "python", "py"}) {
		t.Errorf("windows names = %v", got)
	}
}

func TestFind_LinuxUsesPython3Only(t *testing.T) {
	if _, ok := Find("linux", lookup("python"), nil); ok {
		t.Error("linux should not fall back to plain python")
	}
	in, ok := Find("linux", lookup("python3"), nil)
	if !ok || in.Path != `C:\bin\python3.exe` || in.Args != nil {
		t.Errorf("got %+v, %v", in, ok)
	}
}

func TestFind_WindowsSkipsTheStoreStub(t *testing.T) {
	// python3 is the Store stub: it is on PATH but does not run.
	works := func(in Interpreter) bool { return in.Path != `C:\bin\python3.exe` }
	in, ok := Find("windows", lookup("python3", "python"), works)
	if !ok || in.Path != `C:\bin\python.exe` {
		t.Errorf("got %+v, %v; want python.exe", in, ok)
	}
}

func TestFind_WindowsLauncherGetsDashThree(t *testing.T) {
	in, ok := Find("windows", lookup("py"), func(Interpreter) bool { return true })
	if !ok || !reflect.DeepEqual(in.Args, []string{"-3"}) {
		t.Errorf("got %+v, %v; want py with -3", in, ok)
	}
}

func TestFind_NoneFound(t *testing.T) {
	if _, ok := Find("windows", lookup(), nil); ok {
		t.Error("nothing is on PATH, so nothing should be found")
	}
	if _, ok := Find("windows", lookup("python3"), func(Interpreter) bool { return false }); ok {
		t.Error("a candidate that does not run must be skipped")
	}
}
