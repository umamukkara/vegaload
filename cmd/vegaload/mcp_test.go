package main

import "testing"

func TestCmdMCP_RequiresServeSubcommand(t *testing.T) {
	if code := cmdMCP(nil); code == 0 {
		t.Error("expected a non-zero exit code with no subcommand")
	}
	if code := cmdMCP([]string{"bogus"}); code == 0 {
		t.Error("expected a non-zero exit code for an unknown subcommand")
	}
}

func TestCmdMCPServe_Help(t *testing.T) {
	if code := cmdMCPServe([]string{"-h"}); code != 0 {
		t.Errorf("cmdMCPServe -h returned exit code %d, want 0", code)
	}
}

func TestMainRun_MCPUnknownSubcommand(t *testing.T) {
	if code := run([]string{"mcp", "bogus"}); code == 0 {
		t.Error("expected a non-zero exit code for \"vegaload mcp bogus\"")
	}
}
