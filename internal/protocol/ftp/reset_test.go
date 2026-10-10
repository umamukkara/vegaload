package ftp

import (
	"errors"
	"fmt"
	"net"
	"os"
	"testing"
)

// The text of a dead connection differs by operating system. The Windows
// text is what the hosted Windows runner returns for a write on a session
// that the server has closed.
func TestIsReset_KnownTextsOnEveryOS(t *testing.T) {
	win := &net.OpError{Op: "write", Net: "tcp", Err: os.NewSyscallError("wsasend", errors.New("An established connection was aborted by the software in your host machine."))}
	cases := map[string]error{
		"windows write":   win,
		"windows wrapped": fmt.Errorf("x: %w", win),
		"windows read":    errors.New("read tcp 127.0.0.1:1->127.0.0.1:2: wsarecv: An existing connection was forcibly closed by the remote host."),
		"unix reset":      errors.New("read tcp: connection reset by peer"),
		"unix pipe":       errors.New("write tcp: broken pipe"),
	}
	for name, err := range cases {
		if !isReset(err) {
			t.Errorf("%s: not seen as a dead connection", name)
		}
	}
	for name, err := range map[string]error{
		"plain":   errors.New("550 no such file"),
		"timeout": &net.OpError{Op: "read", Err: timeoutErr{}},
	} {
		if isReset(err) {
			t.Errorf("%s: wrongly seen as a dead connection", name)
		}
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }
