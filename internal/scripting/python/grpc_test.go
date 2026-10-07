package python

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vegaload/vegaload/internal/protocol/grpc/grpctest"
)

func TestGRPC_CallReadsJSON(t *testing.T) {
	srv := grpctest.Start(t, true)
	must(t, nil, `
r = grpc.call("`+srv.Addr+`", method="/grpc.health.v1.Health/Check", body={"service": "up"})
assert r.ok, r.error
assert r.status == 0 and r.statusName == "OK", r.statusName
assert r.json.status == "SERVING", r.body
e = grpc.call("`+srv.Addr+`", method="/grpc.health.v1.Health/Check", body={"service": "nope"})
assert not e.ok and e.status == 5 and e.statusName == "NotFound", e.statusName
`)
}

func TestGRPC_HeadersAndBase64(t *testing.T) {
	srv := grpctest.Start(t, false)
	must(t, nil, `
r = grpc.call("`+srv.Addr+`", method="/grpc.health.v1.Health/Check", encoding="base64", headers={"x-token": "abc"})
assert r.ok, r.error
assert r.body == "CAE=", r.body
`)
	if v, ok := srv.LastHeader("x-token"); !ok || v != "abc" {
		t.Errorf("x-token = %q, %v", v, ok)
	}
}

func TestGRPC_SetupMistakesRaise(t *testing.T) {
	srv := grpctest.Start(t, true)
	cases := map[string]string{
		`grpc.call("` + srv.Addr + `", body={})`:                                                 "option method is required",
		`grpc.call("` + srv.Addr + `", method="/grpc.health.v1.Health/Check", colour="red")`:     "unknown option colour",
		`grpc.call("` + srv.Addr + `", method="/grpc.health.v1.Health/Watch")`:                   "streaming",
		`grpc.call("` + srv.Addr + `", method="/grpc.health.v1.Health/Check", body={"nope": 1})`: "not a valid",
	}
	for call, want := range cases {
		err := runIteration(t, nil, call)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want it to mention %q", call, err, want)
		}
	}
}

func TestMixedFlow_HTTPThenGRPCThenWebSocket(t *testing.T) {
	grpcSrv := grpctest.Start(t, true)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"service":"up"}`))
	}))
	defer api.Close()
	wsSrv := pyWsEchoServer(t)
	defer wsSrv.Close()

	must(t, nil, `
cfg = http.get("`+api.URL+`").json()
h = grpc.call("`+grpcSrv.Addr+`", method="/grpc.health.v1.Health/Check", body={"service": cfg["service"]})
assert h.ok and h.json.status == "SERVING", h.body
conn = ws.connect("`+pyWsURL(wsSrv.URL)+`")
conn.send(h.json.status)
assert conn.receive(2000) == "SERVING"
conn.close()
`)
}
