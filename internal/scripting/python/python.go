// Package python runs a scenario written as a Python file, by shelling
// out to a python3 interpreter the user has installed separately.
//
// This is VegaLoad's second-class scripting path, as AGENTS.md describes
// it: JavaScript/TypeScript (internal/scripting/js) is embedded and needs
// nothing beyond the vegaload binary itself, while Python here depends on
// a python3 executable already being on PATH. A scenario file defines a
// module-level function named iteration:
//
//	def iteration():
//	    # one iteration's worth of work
//	    pass
//
// Each VU gets its own python3 subprocess (started once, in NewVU, and
// reused for every iteration that VU runs) talking a tiny line-delimited
// JSON protocol over stdin/stdout — not a fresh process per iteration,
// which would make the per-request overhead dwarf whatever the scenario
// is actually measuring.
package python

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// pythonBin is the interpreter this package shells out to. It is a
// plain var, not a const, so a test (or a future CLI flag) can point it
// at a specific interpreter instead of whatever "python3" resolves to on
// PATH.
var pythonBin = "python3"

// harnessSource is run as `python3 -c harnessSource <script path>`. It
// loads the scenario module once, validates it, and then answers one
// line on stdin with one line on stdout per iteration — see the package
// doc comment for the protocol.
const harnessSource = `
import sys, json, runpy

def respond(ok, error=None):
    msg = {"ok": ok}
    if error is not None:
        msg["error"] = error
    sys.stdout.write(json.dumps(msg) + "\n")
    sys.stdout.flush()

def main():
    script_path = sys.argv[1]
    try:
        module_globals = runpy.run_path(script_path, run_name="__vegaload_scenario__")
    except BaseException as e:
        respond(False, "loading script: %s: %s" % (type(e).__name__, e))
        sys.exit(1)

    iteration = module_globals.get("iteration")
    if not callable(iteration):
        respond(False, "script has no callable iteration() function")
        sys.exit(1)

    respond(True)  # ready

    for line in sys.stdin:
        try:
            iteration()
            respond(True)
        except BaseException as e:
            respond(False, "%s: %s" % (type(e).__name__, e))

main()
`

// Script is a loaded, validated-at-load-time-only scenario file. Load
// itself only checks that the file exists and that an interpreter is
// available — the script's own correctness (does it define iteration?,
// does it even parse?) is checked once per VU, in NewVU, since that is
// where the interpreter that would tell us is actually started.
type Script struct {
	path string
}

// Load returns a Script for the scenario file at path, after confirming
// the file exists and that pythonBin is on PATH. It does not start an
// interpreter or touch the script's contents — see NewVU.
func Load(path string) (*Script, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("python: %w", err)
	}
	if _, err := exec.LookPath(pythonBin); err != nil {
		return nil, fmt.Errorf("python: %s not found on PATH (the Python scripting driver requires a python3 interpreter to be installed separately — see AGENTS.md): %w", pythonBin, err)
	}
	return &Script{path: path}, nil
}

// status is one line of the harness's stdout protocol.
type status struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

// VU is one virtual user's python3 subprocess.
type VU struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	stderr  *bytes.Buffer
}

// NewVU starts a fresh python3 subprocess for one virtual user and waits
// for its ready signal, which is also where a script that fails to
// import, or has no iteration() function, is caught — before any
// iteration is attempted, the same way js.Script.NewVU validates the
// default export up front.
func (s *Script) NewVU() (*VU, error) {
	cmd := exec.Command(pythonBin, "-u", "-c", harnessSource, s.path) //nolint:gosec // path and interpreter are operator-controlled, not request input

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("python: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("python: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("python: starting interpreter: %w", err)
	}

	vu := &VU{
		cmd:     cmd,
		stdin:   stdin,
		scanner: bufio.NewScanner(stdout),
		stderr:  &stderr,
	}

	st, err := vu.readStatus()
	if err != nil {
		_ = vu.Close()
		return nil, fmt.Errorf("python: %w", err)
	}
	if !st.OK {
		_ = vu.Close()
		return nil, fmt.Errorf("python: %s", st.Error)
	}

	return vu, nil
}

// readStatus reads and parses one protocol line from the interpreter.
func (v *VU) readStatus() (status, error) {
	if !v.scanner.Scan() {
		if err := v.scanner.Err(); err != nil {
			return status{}, fmt.Errorf("reading from interpreter: %w", err)
		}
		if msg := v.stderr.String(); msg != "" {
			return status{}, fmt.Errorf("interpreter exited unexpectedly: %s", msg)
		}
		return status{}, fmt.Errorf("interpreter exited before responding")
	}
	var st status
	if err := json.Unmarshal(v.scanner.Bytes(), &st); err != nil {
		return status{}, fmt.Errorf("parsing interpreter response %q: %w", v.scanner.Text(), err)
	}
	return st, nil
}

// Iteration runs the script's iteration() function once, in this VU's
// subprocess. It implements engine.IterationFunc's signature directly,
// so a *VU can be used anywhere the engine wants one.
//
// Unlike js.VU.Iteration, a cancelled ctx here does not interrupt
// iteration() mid-execution inside the subprocess — there is no
// in-process equivalent of Goja's Interrupt across a process boundary.
// It does stop Iteration from waiting on a response past ctx's
// deadline, returning promptly; the subprocess itself is only reclaimed
// when Close is called, which the executor is expected to do once the
// run ends.
func (v *VU) Iteration(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if _, err := io.WriteString(v.stdin, "\n"); err != nil {
		return fmt.Errorf("python: writing to interpreter: %w", err)
	}

	type result struct {
		st  status
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		st, err := v.readStatus()
		resCh <- result{st, err}
	}()

	select {
	case r := <-resCh:
		if r.err != nil {
			return fmt.Errorf("python: %w", r.err)
		}
		if !r.st.OK {
			return fmt.Errorf("python: %s", r.st.Error)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close signals the interpreter to exit (by closing its stdin, which
// ends the harness's read loop) and waits for the subprocess to exit.
func (v *VU) Close() error {
	_ = v.stdin.Close()
	return v.cmd.Wait()
}
