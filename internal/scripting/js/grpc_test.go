package js

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vegaload/vegaload/internal/protocol/grpc/grpctest"
)

func TestGRPC_CallReadsJSON(t *testing.T) {
	srv := grpctest.Start(t, true)
	must(t, nil, assertFn+`
		const r = grpc.call("`+srv.Addr+`", {method: "/grpc.health.v1.Health/Check", body: {service: "up"}});
		assert(r.ok, "ok: " + r.error);
		assert(r.status === 0 && r.statusName === "OK", "status " + r.status + " " + r.statusName);
		assert(r.json.status === "SERVING", "json " + r.body);
		assert(JSON.parse(r.body).status === "SERVING", "body is JSON text");

		// body may also be JSON text.
		const t = grpc.call("`+srv.Addr+`", {method: "grpc.health.v1.Health/Check", body: '{"service":"down"}'});
		assert(t.json.status === "NOT_SERVING", t.body);

		// A server error is a reply, not an exception.
		const e = grpc.call("`+srv.Addr+`", {method: "/grpc.health.v1.Health/Check", body: {service: "nope"}});
		assert(!e.ok && e.status === 5 && e.statusName === "NotFound", "status " + e.status);
		assert(e.error.length > 0, "error text");
	`)
}

func TestGRPC_HeadersAndBase64(t *testing.T) {
	srv := grpctest.Start(t, false)
	must(t, nil, assertFn+`
		const r = grpc.call("`+srv.Addr+`", {method: "/grpc.health.v1.Health/Check", encoding: "base64", headers: {"x-token": "abc"}});
		assert(r.ok, r.error);
		assert(r.body === "CAE=", "body " + r.body);
	`)
	if v, ok := srv.LastHeader("x-token"); !ok || v != "abc" {
		t.Errorf("x-token = %q, %v", v, ok)
	}
}

func TestGRPC_SetupMistakesThrow(t *testing.T) {
	srv := grpctest.Start(t, true)
	cases := map[string]string{
		`grpc.call("` + srv.Addr + `", {body: {}})`:                                                "option method is required",
		`grpc.call("` + srv.Addr + `", {method: "/grpc.health.v1.Health/Check", colour: "red"})`:   "unknown option colour",
		`grpc.call("` + srv.Addr + `", {method: "/grpc.health.v1.Health/Check", body: {nope: 1}})`: "not a valid",
		`grpc.call("` + srv.Addr + `", {method: "/grpc.health.v1.Health/Watch"})`:                  "streaming",
		`tcp.send("127.0.0.1:1", {body: {a: 1}})`:                                                  "option body must be a string",
		`mqtt.publish("mqtt://x", {topic: "t", headers: {a: "b"}})`:                                "only grpc.call takes headers",
	}
	for call, want := range cases {
		err := runScript(t, nil, call+";")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want it to mention %q", call, err, want)
		}
	}
}

func TestGRPC_SafetyCheckRefusesTheHost(t *testing.T) {
	err := runScript(t, func(host string) error { return http.ErrNotSupported }, `grpc.call("example.com:443", {method: "/a.B/C"});`)
	if err == nil {
		t.Fatal("the safety check should refuse the host")
	}
}

// A real session mixes protocols: an HTTP call hands an id to a gRPC call,
// and a WebSocket gets the answer (FR-CLI-19).
func TestMixedFlow_HTTPThenGRPCThenWebSocket(t *testing.T) {
	grpcSrv := grpctest.Start(t, true)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"service":"up"}`))
	}))
	defer api.Close()
	wsSrv := wsEchoServer(t)
	defer wsSrv.Close()

	must(t, nil, assertFn+`
		const cfg = http.get("`+api.URL+`").json();
		const h = grpc.call("`+grpcSrv.Addr+`", {method: "/grpc.health.v1.Health/Check", body: {service: cfg.service}});
		assert(h.ok && h.json.status === "SERVING", "grpc " + h.body);
		const conn = ws.connect("`+wsURL(wsSrv.URL)+`");
		conn.send(h.json.status);
		assert(conn.receive(2000) === "SERVING", "ws echo");
		conn.close();
	`)
}
