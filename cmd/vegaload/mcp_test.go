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

func TestCmdMCPServe_HTTPNeedsTokenOffLoopback(t *testing.T) {
	if code := cmdMCPServe([]string{"-http", "0.0.0.0:0"}); code != 2 {
		t.Errorf("exit code = %d, want 2 (no token on a non-loopback address)", code)
	}
}

func TestCmdMCPServe_HTTPBadAddress(t *testing.T) {
	if code := cmdMCPServe([]string{"-http", "nonsense"}); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

func TestCmdMCPServe_TokenEnvMustBeSet(t *testing.T) {
	t.Setenv("VEGALOAD_TEST_EMPTY_TOKEN", "")
	if code := cmdMCPServe([]string{"-http", "127.0.0.1:0", "-token-env", "VEGALOAD_TEST_EMPTY_TOKEN"}); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

func TestCmdMCPServe_TokenFlagsNeedHTTP(t *testing.T) {
	if code := cmdMCPServe([]string{"-token-env", "X"}); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}
