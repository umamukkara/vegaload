//go:build !windows

package dialer

import (
	"context"
	"net"
)

// ContextDialer reports that the default HTTP dialer is fine here.
func ContextDialer() func(context.Context, string, string) (net.Conn, error) {
	return nil
}

// NetDial reports that the library's own dialer is fine here.
func NetDial() func(context.Context, string, string) (net.Conn, error) {
	return nil
}
