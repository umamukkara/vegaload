// Package js runs a scenario written as a JavaScript or TypeScript file,
// using the embedded Goja ECMAScript runtime — no Node.js, no network
// fetch of a runtime, and no separate build step for the author. A
// scenario file exports a default function the same way a k6 script
// does:
//
//	export default function () {
//	  // one iteration's worth of work
//	}
//
// A .ts file is accepted too: it is transpiled to JavaScript (types
// stripped, ESM import/export rewritten to a form this package can read
// back out) with esbuild before Goja ever sees it. esbuild is a
// committed dependency of this binary, not an external tool — nothing
// about this package requires Node, npm, or network access to work.
//
// AGENTS.md's first rule is that a test is always a real file; this
// package's whole job is turning that file into something the engine can
// call for a VU, and nothing else — it knows nothing about HTTP, gRPC, or
// WebSocket (the protocol drivers are wired in separately, in cmd/vegaload).
package js

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dop251/goja"
	"github.com/evanw/esbuild/pkg/api"
)

// Script is a loaded, compiled scenario file, ready to be instantiated
// into any number of VUs.
type Script struct {
	program *goja.Program
}

// Load reads and compiles the scenario file at path. A .ts, .tsx, or .jsx
// file is transpiled first; anything else is treated as plain
// JavaScript. Load returns an error for anything that stops every VU
// identically — a missing file, a syntax error, a transpile failure — so
// the caller can fail the whole run immediately instead of discovering
// it only when the first VU starts.
func Load(path string) (*Script, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("js: reading %s: %w", path, err)
	}

	code, err := transpile(path, src)
	if err != nil {
		return nil, fmt.Errorf("js: transpiling %s: %w", path, err)
	}

	program, err := goja.Compile(path, code, false)
	if err != nil {
		return nil, fmt.Errorf("js: compiling %s: %w", path, err)
	}

	return &Script{program: program}, nil
}

// transpile converts src to CommonJS JavaScript that Goja can run and
// this package can pull a default export back out of. It always runs
// src through esbuild, even plain .js: esbuild's CommonJS output format
// is what turns `export default function(){}` into an `exports.default`
// assignment this package's VU setup can read, so a .js file using ESM
// export syntax (the only form this package documents) needs the same
// pass a .ts file does.
func transpile(path string, src []byte) (string, error) {
	result := api.Transform(string(src), api.TransformOptions{
		Loader:     loaderFor(path),
		Format:     api.FormatCommonJS,
		Target:     api.ES2020,
		Sourcefile: filepath.Base(path),
	})
	if len(result.Errors) > 0 {
		msgs := make([]string, len(result.Errors))
		for i, e := range result.Errors {
			msgs[i] = e.Text
		}
		return "", errors.New(strings.Join(msgs, "; "))
	}
	return string(result.Code), nil
}

func loaderFor(path string) api.Loader {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts":
		return api.LoaderTS
	case ".tsx":
		return api.LoaderTSX
	case ".jsx":
		return api.LoaderJSX
	default:
		return api.LoaderJS
	}
}

// VU is one virtual user's instance of a Script: its own Goja runtime
// running its own copy of the script's top-level state, and the default
// export function pulled out of it. A goja.Runtime is not safe for
// concurrent use, so the engine must create one VU per virtual user via
// NewVU — never share a single VU across goroutines.
type VU struct {
	vm *goja.Runtime
	fn goja.Callable
}

// NewVU creates a fresh VU from the script: a new runtime, the script's
// top-level code run once (so top-level state like a counter declared
// with let/const starts fresh for this VU), and its default export
// resolved and validated.
func (s *Script) NewVU() (*VU, error) {
	vm := goja.New()

	exportsObj := vm.NewObject()
	moduleObj := vm.NewObject()
	if err := moduleObj.Set("exports", exportsObj); err != nil {
		return nil, fmt.Errorf("js: %w", err)
	}
	if err := vm.Set("exports", exportsObj); err != nil {
		return nil, fmt.Errorf("js: %w", err)
	}
	if err := vm.Set("module", moduleObj); err != nil {
		return nil, fmt.Errorf("js: %w", err)
	}
	if err := vm.Set("console", newConsole(vm)); err != nil {
		return nil, fmt.Errorf("js: %w", err)
	}

	if _, err := vm.RunProgram(s.program); err != nil {
		return nil, fmt.Errorf("js: running script: %w", err)
	}

	// esbuild's CommonJS output does not assign properties onto the
	// `exports` object this package provided — it builds its own
	// exports object internally and reassigns `module.exports` to it
	// wholesale (`module.exports = __toCommonJS(stdin_exports)`), which
	// is why the default export has to be read back off moduleObj
	// rather than off the original exportsObj.
	finalExports := moduleObj.Get("exports")
	exportsAsObject, ok := finalExports.(*goja.Object)
	if !ok {
		return nil, fmt.Errorf("js: script's module.exports is not an object")
	}
	def := exportsAsObject.Get("default")
	if def == nil || goja.IsUndefined(def) {
		return nil, fmt.Errorf("js: script has no default export function (expected `export default function() { ... }`)")
	}
	fn, ok := goja.AssertFunction(def)
	if !ok {
		return nil, fmt.Errorf("js: default export is not a function")
	}

	return &VU{vm: vm, fn: fn}, nil
}

// newConsole builds a minimal console object (just log, matching what a
// scenario author reaches for first) backed by the Go standard log
// output, so a script's console.log calls show up without needing a
// real Node-style console implementation.
func newConsole(vm *goja.Runtime) *goja.Object {
	c := vm.NewObject()
	logFn := func(call goja.FunctionCall) goja.Value {
		parts := make([]string, len(call.Arguments))
		for i, a := range call.Arguments {
			parts[i] = a.String()
		}
		fmt.Println(strings.Join(parts, " "))
		return goja.Undefined()
	}
	_ = c.Set("log", logFn)
	return c
}

// Iteration runs the script's default export once. It implements
// engine.IterationFunc's signature (func(context.Context) error)
// directly, so a *VU can be used anywhere the engine wants one, without
// an adapter: `executor.Run(ctx, vu.Iteration, recorder)`.
//
// If ctx is cancelled while the function is running, Iteration
// interrupts the runtime and returns promptly instead of waiting for a
// runaway script to finish on its own.
func (v *VU) Iteration(ctx context.Context) error {
	v.vm.ClearInterrupt()

	done := make(chan struct{})
	defer close(done)
	if ctx != nil {
		go func() {
			select {
			case <-ctx.Done():
				v.vm.Interrupt(ctx.Err())
			case <-done:
			}
		}()
	}

	_, err := v.fn(goja.Undefined())
	if err == nil {
		return nil
	}

	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) {
		return fmt.Errorf("js: %w", ctx.Err())
	}

	var jsErr *goja.Exception
	if errors.As(err, &jsErr) {
		return fmt.Errorf("js: %s", jsErr.Value().String())
	}
	return fmt.Errorf("js: %w", err)
}

// Close releases v's resources. A goja.Runtime needs no explicit
// teardown, so this is a no-op; it exists so *VU satisfies the same
// Iteration-plus-Close shape as python.VU, letting callers (such as
// cmd/vegaload's VU pool) treat either scripting runtime the same way.
func (v *VU) Close() error { return nil }
