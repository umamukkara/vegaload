// Package dialer adjusts outbound connections for the machine they run on.
//
// Windows keeps a closed connection's local port for minutes and refuses
// a new connection that asks for that port. A load test opens and closes
// enough connections to hit that limit, after which every later dial on
// the machine fails at once. Apply tells Windows it may reuse such a port.
// On other systems Apply changes nothing.
package dialer

import "net"

// Apply sets the socket options a dialer needs. It is safe to call on a
// nil dialer. A dialer that already has a Control keeps it, and the port
// reuse option runs first.
func Apply(d *net.Dialer) {
	apply(d)
}
