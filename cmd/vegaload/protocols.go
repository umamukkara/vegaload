package main

import "strings"

// protocolNames lists the -protocol values protocolIteration accepts, in
// the order help text and error messages show them. Keep it in step with
// the switch in protocolIteration: a driver is wired in both places and
// nowhere else.
var protocolNames = []string{"http1", "http2", "grpc", "websocket"}

// protocolList renders protocolNames as "a, b, or c" for help text and
// error messages.
func protocolList() string {
	n := len(protocolNames)
	switch n {
	case 0:
		return ""
	case 1:
		return protocolNames[0]
	case 2:
		return protocolNames[0] + " or " + protocolNames[1]
	}
	return strings.Join(protocolNames[:n-1], ", ") + ", or " + protocolNames[n-1]
}
