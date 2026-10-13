//go:build windows

package dialer

import (
	"context"
	"net"
	"syscall"
	"time"
)

// soReuseUnicastPort is Windows' SO_REUSE_UNICASTPORT. It lets an
// outbound connect bind a local port that is still in TIME_WAIT.
const soReuseUnicastPort = 0x3007

func apply(d *net.Dialer) {
	if d == nil {
		return
	}
	prev := d.Control
	d.Control = func(network, address string, c syscall.RawConn) error {
		if err := reusePort(c); err != nil {
			return err
		}
		if prev != nil {
			return prev(network, address, c)
		}
		return nil
	}
}

func reusePort(c syscall.RawConn) error {
	var sockErr error
	err := c.Control(func(fd uintptr) {
		h := syscall.Handle(fd)
		if err := syscall.SetsockoptInt(h, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
			sockErr = err
			return
		}
		sockErr = syscall.SetsockoptInt(h, syscall.SOL_SOCKET, soReuseUnicastPort, 1)
	})
	if err != nil {
		return err
	}
	return sockErr
}

// ContextDialer is the dial function for an HTTP transport. It matches
// the standard transport's dialer (30s connect, 30s keep-alive) and adds
// port reuse. Callers on other systems should keep the default dialer;
// this file is Windows-only, and the other build returns nil.
// NetDial is the dial function for a WebSocket client, or nil where the
// library's own dialer is right. Unlike ContextDialer it sets no connect
// timeout of its own; the caller's context is the deadline.
func NetDial() func(context.Context, string, string) (net.Conn, error) {
	d := &net.Dialer{}
	apply(d)
	return d.DialContext
}

func ContextDialer() func(context.Context, string, string) (net.Conn, error) {
	d := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	apply(d)
	return d.DialContext
}
