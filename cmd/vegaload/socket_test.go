package main

import (
	"bufio"
	"io"
	"net"
	"path/filepath"
	"testing"
)

// End to end: `vegaload run -protocol tcp` against a small server, with
// the reply check passing, then failing.
func TestCmdRun_TCPProtocol(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				if _, err := bufio.NewReader(c).ReadString('\n'); err == nil {
					_, _ = io.WriteString(c, "PONG\r\n")
				}
			}()
		}
	}()
	audit := filepath.Join(t.TempDir(), "audit.log")
	base := []string{"-target", "tcp://" + l.Addr().String(), "-protocol", "tcp", "-body", `PING\n`,
		"-vus", "2", "-duration", "300ms", "-no-report", "-audit-log", audit}

	if code := cmdRun(append(base, "-opt", "expect=PONG", "-threshold", "error_rate < 1%")); code != 0 {
		t.Errorf("matching reply: exit code = %d, want 0", code)
	}
	if code := cmdRun(append(base, "-opt", "expect=NOPE", "-threshold", "error_rate < 1%")); code != 3 {
		t.Errorf("wrong reply: exit code = %d, want 3", code)
	}
}

func TestCmdRun_UDPNeedsBody(t *testing.T) {
	code := cmdRun([]string{"-target", "udp://127.0.0.1:9", "-protocol", "udp", "-duration", "100ms", "-no-report"})
	if code == 0 {
		t.Error("udp without -body should fail")
	}
}

func TestCmdRun_UnknownOptionIsRefused(t *testing.T) {
	code := cmdRun([]string{"-target", "tcp://127.0.0.1:9", "-protocol", "tcp", "-opt", "raed=1", "-duration", "100ms", "-no-report"})
	if code == 0 {
		t.Error("a misspelled option should fail the run")
	}
}
