package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func coreChecks(env Env) []Check {
	return []Check{
		{ID: "core.version", Category: "core", Name: "CLI version", Run: func(ctx context.Context) Result {
			msg := fmt.Sprintf("vegaload %s (%s/%s)", env.Version, env.OS, env.Arch)
			if strings.HasSuffix(env.Version, "-dev") {
				msg += ", development build"
			}
			return result(Pass, msg)
		}},
		{ID: "core.path", Category: "core", Name: "On the PATH", Run: func(ctx context.Context) Result { return checkPath(env) }},
		{ID: "core.install", Category: "core", Name: "Install method", Run: func(ctx context.Context) Result {
			return result(Pass, installMethod(env))
		}},
		{ID: "core.dirs", Category: "core", Name: "Writable folders", Run: func(ctx context.Context) Result { return checkDirs(env) }},
		{ID: "core.python", Category: "core", Name: "Python (optional)", Run: func(ctx context.Context) Result {
			p, err := env.LookPath("python3")
			if err != nil {
				return result(Skip, "python3 not found. JavaScript and TypeScript scenarios work without it; Python scenarios need it.")
			}
			return result(Pass, "python3 at "+p)
		}},
	}
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func checkPath(env Env) Result {
	found, err := env.LookPath("vegaload")
	if err != nil {
		r := result(Warn, "vegaload is not on your PATH, so a bare `vegaload` command will not work")
		r.Detail = []string{"running binary: " + env.Exe}
		r.Fix = "Install with `brew install vegaload/tap/vegaload`, or add " + filepath.Dir(env.Exe) + " to your PATH. Hosts that use an absolute path in their MCP config are not affected."
		return r
	}
	if realPath(found) != realPath(env.Exe) {
		r := result(Warn, "`vegaload` on the PATH is a different binary from the one running now")
		r.Detail = []string{"on PATH: " + found, "running:  " + env.Exe}
		r.Fix = "Run the one you intend to use by its full path, or remove the extra copy."
		return r
	}
	return result(Pass, found)
}

func installMethod(env Env) string {
	exe := env.Exe
	switch {
	case strings.Contains(exe, "/Cellar/") || strings.Contains(exe, "/homebrew/") || strings.Contains(exe, "/linuxbrew/"):
		return "installed with Homebrew (" + exe + ")"
	case strings.Contains(exe, "/go/bin/"):
		return "installed with go install (" + exe + ")"
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "running inside a container (" + exe + ")"
	}
	return "standalone binary (" + exe + ")"
}

// writable reports whether files can be created in dir. A directory
// that does not exist yet counts as writable if its nearest existing
// parent is, since VegaLoad creates it on first use.
func writable(dir string) (bool, string) {
	cur := dir
	for {
		fi, err := os.Stat(cur)
		if err == nil {
			if !fi.IsDir() {
				return false, cur + " is a file, not a folder"
			}
			f, err := os.CreateTemp(cur, ".vegaload-doctor-*")
			if err != nil {
				return false, err.Error()
			}
			name := f.Name()
			_ = f.Close()
			_ = os.Remove(name)
			return true, cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return false, "no existing parent folder"
		}
		cur = parent
	}
}

func checkDirs(env Env) Result {
	var problems, notes []string
	vl := filepath.Join(env.Home, ".vegaload")
	if ok, why := writable(vl); !ok {
		problems = append(problems, fmt.Sprintf("%s (audit log): %s", vl, why))
	} else {
		notes = append(notes, vl+" (audit log)")
	}
	if ok, why := writable(env.Dir); !ok {
		problems = append(problems, fmt.Sprintf("%s (reports): %s", env.Dir, why))
	} else {
		notes = append(notes, env.Dir+" (reports)")
	}
	if len(problems) > 0 {
		r := result(Fail, "VegaLoad cannot write to every folder it needs")
		r.Detail = problems
		r.Fix = "Fix the folder permissions, or run from a folder you can write to."
		return r
	}
	r := result(Pass, "audit log and report folders are writable")
	r.Detail = notes
	return r
}
