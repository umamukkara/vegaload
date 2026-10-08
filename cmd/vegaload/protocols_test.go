package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vegaload/vegaload/internal/mcp"
	"github.com/vegaload/vegaload/internal/protocol"
	"github.com/vegaload/vegaload/internal/protocol/grpc/grpctest"
	"github.com/vegaload/vegaload/internal/protocol/mysql/mysqltest"
	"github.com/vegaload/vegaload/internal/protocol/postgres/postgrestest"
	"github.com/vegaload/vegaload/internal/report"
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
	case "mqtt":
		return protocol.Target{URL: "mqtt://localhost:1", Options: map[string]string{"topic": "t"}}
	case "kafka":
		return protocol.Target{URL: "kafka://localhost:1", Options: map[string]string{"topic": "t"}}
	case "postgres":
		return protocol.Target{URL: "postgres://localhost:1/db", Body: []byte("select 1")}
	case "mysql":
		return protocol.Target{URL: "mysql://localhost:1/db", Body: []byte("select 1")}
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

// A scenario that mixes HTTP and gRPC runs end to end through `vegaload run`
// (FR-CLI-19).
func TestCmdRun_ScenarioCallsGRPC(t *testing.T) {
	srv := grpctest.Start(t, true)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"service":"up"}`))
	}))
	t.Cleanup(api.Close)

	dir := t.TempDir()
	scenario := filepath.Join(dir, "mixed.vl.js")
	src := `export default function () {
  const cfg = step("http", () => http.get("` + api.URL + `").json());
  step("grpc", () => {
    const r = grpc.call("` + srv.Addr + `", {method: "/grpc.health.v1.Health/Check", body: {service: cfg.service}});
    check(r, {"serving": (x) => x.ok && x.json.status === "SERVING"});
    if (!r.ok) throw new Error(r.error);
  });
}`
	if err := os.WriteFile(scenario, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "summary.json")
	code := cmdRun([]string{
		"-vus", "2", "-duration", "300ms", "-no-report",
		"-audit-log", filepath.Join(dir, "audit.log"), "-out", out,
		"-threshold", "check_rate >= 100%", "-threshold", `error_rate{step="grpc"} < 1%`,
		scenario,
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var res report.Result
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatal(err)
	}
	if s, ok := res.Step("grpc"); !ok || s.Total == 0 || s.Failed != 0 {
		t.Errorf("grpc step = %+v, %v", s, ok)
	}
}

// A PostgreSQL load test runs end to end through `vegaload run -protocol
// postgres`, and its checks (min_rows) fail the iteration when not met.
func TestCmdRun_PostgresProtocol(t *testing.T) {
	srv := postgrestest.Start(t, func(string) []postgrestest.Result {
		return []postgrestest.Result{{
			Columns: []postgrestest.Column{postgrestest.Int("n")},
			Rows:    [][]any{{"1"}, {"2"}},
		}}
	})
	run := func(minRows string) report.Result {
		dir := t.TempDir()
		out := filepath.Join(dir, "summary.json")
		cmdRun([]string{
			"-vus", "2", "-duration", "300ms", "-no-report",
			"-audit-log", filepath.Join(dir, "audit.log"), "-out", out,
			"-target", srv.URL(), "-protocol", "postgres", "-body", "select n from t",
			"-opt", "sslmode=disable", "-opt", "pool=2", "-opt", "min_rows=" + minRows,
		})
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		var res report.Result
		if err := json.Unmarshal(data, &res); err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := run("2"); res.Total == 0 || res.Failed != 0 {
		t.Errorf("min_rows=2: total %d failed %d, want all to pass", res.Total, res.Failed)
	}
	if res := run("3"); res.Total == 0 || res.Failed != res.Total {
		t.Errorf("min_rows=3: total %d failed %d, want all to fail", res.Total, res.Failed)
	}
	if n := srv.ConnCount(); n > 4 {
		t.Errorf("%d connections for two runs with pool=2, want the pool reused", n)
	}
}

// A MySQL load test runs end to end through `vegaload run -protocol mysql`,
// and its checks (min_rows) fail the iteration when not met.
func TestCmdRun_MySQLProtocol(t *testing.T) {
	srv := mysqltest.Start(t, func(string) []mysqltest.Result {
		return []mysqltest.Result{{
			Columns: []mysqltest.Column{mysqltest.Int("n")},
			Rows:    [][]any{{"1"}, {"2"}},
		}}
	})
	run := func(minRows string) report.Result {
		dir := t.TempDir()
		out := filepath.Join(dir, "summary.json")
		cmdRun([]string{
			"-vus", "2", "-duration", "300ms", "-no-report",
			"-audit-log", filepath.Join(dir, "audit.log"), "-out", out,
			"-target", srv.URL(), "-protocol", "mysql", "-body", "select n from t",
			"-opt", "tls=false", "-opt", "pool=2", "-opt", "min_rows=" + minRows,
		})
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		var res report.Result
		if err := json.Unmarshal(data, &res); err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := run("2"); res.Total == 0 || res.Failed != 0 {
		t.Errorf("min_rows=2: total %d failed %d, want all to pass", res.Total, res.Failed)
	}
	if res := run("3"); res.Total == 0 || res.Failed != res.Total {
		t.Errorf("min_rows=3: total %d failed %d, want all to fail", res.Total, res.Failed)
	}
	if n := srv.ConnCount(); n > 4 {
		t.Errorf("%d connections for two runs with pool=2, want the pool reused", n)
	}
}
