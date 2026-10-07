// Package pyfind finds a Python 3 interpreter. On Linux and macOS that is
// "python3". On Windows there is usually no python3.exe: Python from
// python.org installs "python" and the "py" launcher, and Windows ships a
// "python3.exe" stub that only opens the Microsoft Store. So on Windows
// each candidate is also run once to check that it is a real Python 3.
package pyfind

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// runTimeout bounds the check run of one candidate. The Microsoft Store
// stub can open the Store and never return, so a candidate must not be
// allowed to hang the caller.
const runTimeout = 10 * time.Second

// Interpreter is a Python command: the program to run and any arguments
// that must come before the script's own (the "py" launcher needs "-3").
type Interpreter struct {
	Path string
	Args []string
}

// Names returns the commands to try for goos, best first.
func Names(goos string) []string {
	if goos == "windows" {
		return []string{"python3", "python", "py"}
	}
	return []string{"python3"}
}

// ArgsFor returns the arguments a command name needs before the script's
// own. Only the Windows "py" launcher needs one: it picks Python 3.
func ArgsFor(name string) []string {
	if name == "py" {
		return []string{"-3"}
	}
	return nil
}

// statSize returns the size of the file at path. It is a variable so tests
// can fake it.
var statSize = func(path string) (int64, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return fi.Size(), true
}

// IsStoreStub reports whether path looks like the Windows Store alias: a
// file of length zero. Running it opens the Microsoft Store, so it must
// never be started.
func IsStoreStub(path string) bool {
	size, ok := statSize(path)
	return ok && size == 0
}

// CheckArgs returns the full arguments for the one-off run that checks an
// interpreter is Python 3.
func CheckArgs(in Interpreter) []string {
	return append(append([]string{}, in.Args...), "-c", "import sys; sys.exit(0 if sys.version_info[0] == 3 else 1)")
}

// Find looks for a working interpreter for goos. lookPath is exec.LookPath
// in real use. works is called on Windows only, to run a candidate; if it
// is nil, any command found on PATH is accepted.
func Find(goos string, lookPath func(string) (string, error), works func(Interpreter) bool) (Interpreter, bool) {
	for _, name := range Names(goos) {
		p, err := lookPath(name)
		if err != nil {
			continue
		}
		in := Interpreter{Path: p, Args: ArgsFor(name)}
		if goos == "windows" {
			if IsStoreStub(p) {
				continue
			}
			if works != nil && !works(in) {
				continue
			}
		}
		return in, true
	}
	return Interpreter{}, false
}

// Runs reports whether in starts and is Python 3. It gives up after a
// short time, so a program that never returns cannot hang the caller.
func Runs(in Interpreter) bool {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	return exec.CommandContext(ctx, in.Path, CheckArgs(in)...).Run() == nil //nolint:gosec // interpreter found on PATH, fixed arguments
}

// FindOnThisMachine finds a Python 3 interpreter on the machine running
// vegaload.
func FindOnThisMachine() (Interpreter, bool) {
	return Find(runtime.GOOS, exec.LookPath, Runs)
}
