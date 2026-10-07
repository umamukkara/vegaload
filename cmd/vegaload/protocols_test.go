package main

import (
	"strings"
	"testing"

	"github.com/vegaload/vegaload/internal/protocol"
)

func TestProtocolList(t *testing.T) {
	old := protocolNames
	defer func() { protocolNames = old }()

	cases := []struct {
		names []string
		want  string
	}{
		{nil, ""},
		{[]string{"a"}, "a"},
		{[]string{"a", "b"}, "a or b"},
		{[]string{"a", "b", "c"}, "a, b, or c"},
	}
	for _, c := range cases {
		protocolNames = c.names
		if got := protocolList(); got != c.want {
			t.Errorf("protocolList(%v) = %q, want %q", c.names, got, c.want)
		}
	}
}

// Every name in protocolNames must be wired into protocolIteration.
// A name that is listed but not wired would be offered to users and then
// refused as "unknown".
func TestProtocolNames_AreAllWired(t *testing.T) {
	for _, name := range protocolNames {
		_, closeFn, err := protocolIteration(name, wireTestTarget(name), 0)
		if err != nil && strings.Contains(err.Error(), "unknown -protocol") {
			t.Errorf("protocol %q is listed but not wired: %v", name, err)
		}
		if closeFn != nil {
			_ = closeFn()
		}
	}
}

// wireTestTarget returns a target address the named driver accepts at
// construction time. Nothing connects: New only checks the address.
func wireTestTarget(name string) protocol.Target {
	switch name {
	case "grpc":
		return protocol.Target{URL: "localhost:1", Method: "/p.S/M"}
	case "websocket":
		return protocol.Target{URL: "ws://localhost:1/"}
	}
	return protocol.Target{URL: "http://localhost:1/"}
}
