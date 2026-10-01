package mcp

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// RunCLI runs the vegaload binary at exePath with args and returns its
// stdout. This is the only way any tool in this package touches
// vegaload's actual behavior: it shells out to the same binary a human
// would run from their own shell, which is what structurally guarantees
// AGENTS.md's module-boundary rule ("the MCP layer must not call
// core-engine functions directly") rather than relying on code-review
// discipline to keep it true. exePath is resolved once at server
// startup (cmd/vegaload/mcp.go calls os.Executable()) and threaded
// through every tool's closure — this function takes it as a parameter,
// rather than calling os.Executable() itself, purely so tests can point
// it at a stand-in script instead of the real binary.
//
// A non-zero exit is reported as an error that includes stderr, since
// every vegaload subcommand writes its own error messages there.
func RunCLI(ctx context.Context, exePath string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, exePath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("vegaload %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
