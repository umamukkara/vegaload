//go:build !windows

package dialer

import "net"

func apply(d *net.Dialer) {}
