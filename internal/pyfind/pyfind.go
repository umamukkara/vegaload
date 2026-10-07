// Package pyfind finds a Python 3 interpreter. On Linux and macOS that is
// "python3". On Windows there is usually no python3.exe: Python from
// python.org installs "python" and the "py" launcher, and Windows ships a
// "python3.exe" stub that only opens the Microsoft Store. So on Windows
// each candidate is also run once to check that it is a real Python 3.
package pyfind

import (
	"os/exec"
	"runtime"
)

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
		if goos == "windows" && works != nil && !works(in) {
			continue
		}
		return in, true
	}
	return Interpreter{}, false
}

// Runs reports whether in starts and is Python 3.
func Runs(in Interpreter) bool {
	args := append(append([]string{}, in.Args...), "-c", "import sys; sys.exit(0 if sys.version_info[0] == 3 else 1)")
	return exec.Command(in.Path, args...).Run() == nil //nolint:gosec // interpreter found on PATH, fixed arguments
}

// FindOnThisMachine finds a Python 3 interpreter on the machine running
// vegaload.
func FindOnThisMachine() (Interpreter, bool) {
	return Find(runtime.GOOS, exec.LookPath, Runs)
}
