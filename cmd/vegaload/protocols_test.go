package main

import (
	"strings"
	"testing"

	"github.com/vegaload/vegaload/internal/mcp"
	"github.com/vegaload/vegaload/internal/protocol"
)

func TestJoinOr(t *testing.T) {
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
		if got := joinOr(c.names); got != c.want {
			t.Errorf("joinOr(%v) = %q, want %q", c.names, got, c.want)
		}
	}
}

// The drivers table is the only place a protocol is wired, so every entry
// must build, and the help text must list exactly the table's names.
func TestDrivers_EveryEntryBuilds(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range drivers {
		if seen[d.name] {
			t.Errorf("protocol %q is listed twice", d.name)
		}
		seen[d.name] = true

		iter, closeFn, err := protocolIteration(d.name, wireTestTarget(d.name), 0)
		if err != nil {
			t.Errorf("protocol %q does not build: %v", d.name, err)
			continue
		}
		if iter == nil || closeFn == nil {
			t.Errorf("protocol %q returned a nil iteration or close", d.name)
			continue
		}
		_ = closeFn()
	}
	for _, name := range protocolNames() {
		if !strings.Contains(protocolList(), name) {
			t.Errorf("help text %q does not list %q", protocolList(), name)
		}
	}
}

func TestProtocolIteration_UnknownProtocol(t *testing.T) {
	_, _, err := protocolIteration("nope", protocol.Target{URL: "x"}, 0)
	if err == nil || !strings.Contains(err.Error(), "unknown -protocol") || !strings.Contains(err.Error(), "http1") {
		t.Fatalf("err = %v, want it to name the choices", err)
	}
}

// A typo in -opt is an error, not ignored. This holds for every driver:
// a key it did not declare is refused.
func TestProtocolIteration_UnknownOptionIsRefused(t *testing.T) {
	for _, d := range drivers {
		tg := wireTestTarget(d.name)
		tg.Options = map[string]string{"definitely-not-a-key": "1"}
		_, _, err := protocolIteration(d.name, tg, 0)
		if err == nil || !strings.Contains(err.Error(), "unknown option definitely-not-a-key") {
			t.Errorf("protocol %q: err = %v, want an unknown option error", d.name, err)
		}
	}
}

func TestProtocolIteration_DriverWithoutOptionsSaysSo(t *testing.T) {
	tg := wireTestTarget("http1")
	tg.Options = map[string]string{"read": "64"}
	_, _, err := protocolIteration("http1", tg, 0)
	if err == nil || !strings.Contains(err.Error(), "takes no options") {
		t.Fatalf("err = %v, want \"takes no options\"", err)
	}
}

// The MCP run_test tool describes the protocols in its schema. That text
// lives in another package, so this test keeps it in step with the table.
func TestMCPRunTestSchema_ListsEveryProtocol(t *testing.T) {
	var desc string
	for _, tool := range mcp.NewTools("vegaload") {
		if tool.Name != "run_test" {
			continue
		}
		props := tool.InputSchema["properties"].(map[string]any)
		desc = props["protocol"].(map[string]any)["description"].(string)
	}
	if desc == "" {
		t.Fatal("run_test has no protocol description")
	}
	for _, name := range protocolNames() {
		if !strings.Contains(desc, name) {
			t.Errorf("run_test protocol description %q does not mention %q", desc, name)
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
	case "tcp":
		return protocol.Target{URL: "tcp://localhost:1"}
	case "udp":
		return protocol.Target{URL: "udp://localhost:1", Body: []byte("x")}
	}
	return protocol.Target{URL: "http://localhost:1/"}
}

// A real option of a driver is accepted by the command's check.
func TestProtocolIteration_KnownOptionIsAccepted(t *testing.T) {
	tg := wireTestTarget("tcp")
	tg.Options = map[string]string{"read": "4", "expect": "ok"}
	if _, closeFn, err := protocolIteration("tcp", tg, 0); err != nil {
		t.Fatalf("known tcp options refused: %v", err)
	} else {
		_ = closeFn()
	}

	tg = wireTestTarget("udp")
	tg.Options = map[string]string{"read": "4"}
	if _, _, err := protocolIteration("udp", tg, 0); err == nil {
		t.Error("read is a tcp option and must be refused for udp")
	}
}
