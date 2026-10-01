package main

import (
	"os/exec"
	"runtime"
)

// openBrowser implements FR-RPT-02's auto-open: a best-effort use of
// the platform's standard "open this file" command. A failure here (no
// GUI, headless CI, xdg-open missing) is reported as a warning by the
// caller, never as a run failure — the report file itself was already
// written successfully either way.
func openBrowser(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}
