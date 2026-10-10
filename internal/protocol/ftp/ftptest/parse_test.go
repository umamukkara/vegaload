package ftptest

import "testing"

func TestParseCommand(t *testing.T) {
	cmd, arg, err := parseCommand("user anonymous")
	if err != nil || cmd != "USER" || arg != "anonymous" {
		t.Fatalf("got %s %q %v", cmd, arg, err)
	}
	cmd, arg, err = parseCommand("RETR /pub/a.bin")
	if err != nil || cmd != "RETR" || arg != "/pub/a.bin" {
		t.Fatalf("got %s %q %v", cmd, arg, err)
	}
	if _, _, err := parseCommand("NOOP\x00"); err == nil {
		t.Fatal("NUL was accepted")
	}
}
