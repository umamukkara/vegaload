package doctor

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/vegaload/vegaload/internal/hosts"
	"github.com/vegaload/vegaload/internal/mcpprobe"
)

// Env is everything the checks read from the machine. Tests replace the
// pieces they care about; DefaultEnv fills in the real ones.
type Env struct {
	Version string
	Exe     string // the running vegaload binary
	OS      string
	Arch    string
	Home    string
	Dir     string

	Getenv      func(string) string
	LookPath    func(string) (string, error)
	DirExists   func(string) bool // nil means the real file system
	LookupHost  func(ctx context.Context, host string) ([]string, error)
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	HTTPClient  *http.Client
	// RunCmd runs a command and returns its combined output, trimmed.
	RunCmd func(ctx context.Context, name string, args ...string) (string, error)
	// Probe starts an MCP server command and lists its tools.
	Probe func(ctx context.Context, c mcpprobe.Command) (mcpprobe.Info, error)
}

// DefaultEnv returns an Env wired to the real machine.
func DefaultEnv(version string) (Env, error) {
	exe, err := os.Executable()
	if err != nil {
		return Env{}, err
	}
	home, _ := os.UserHomeDir()
	dir, _ := os.Getwd()
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return Env{
		Version:     version,
		Exe:         exe,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Home:        home,
		Dir:         dir,
		Getenv:      os.Getenv,
		LookPath:    exec.LookPath,
		LookupHost:  func(ctx context.Context, h string) ([]string, error) { return net.DefaultResolver.LookupHost(ctx, h) },
		DialContext: dialer.DialContext,
		HTTPClient: &http.Client{
			Timeout: 8 * time.Second,
			// A redirect is an answer; doctor does not chase it.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		RunCmd: func(ctx context.Context, name string, args ...string) (string, error) {
			out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
			return strings.TrimSpace(string(out)), err
		},
		Probe: mcpprobe.Probe,
	}, nil
}

func (e Env) hostEnv() hosts.Env {
	return hosts.Env{OS: e.OS, Home: e.Home, Dir: e.Dir, LookPath: e.LookPath, DirExists: e.DirExists}
}
