package grpc

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/vegaload/vegaload/internal/protocol/grpc/grpctest"
)

const healthCheck = "/grpc.health.v1.Health/Check"

func newConn(t *testing.T, addr string) *Conn {
	t.Helper()
	c, err := NewConn(addr, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestConn_JSONThroughReflection(t *testing.T) {
	srv := grpctest.Start(t, true)
	c := newConn(t, srv.Addr)
	rep, err := c.Do(context.Background(), Call{Method: healthCheck, Body: []byte(`{"service":"up"}`), Timeout: 5 * time.Second})
	if err != nil || rep.Err != nil {
		t.Fatalf("err = %v, reply err = %v", err, rep.Err)
	}
	if rep.Code != codes.OK || !strings.Contains(string(rep.Body), `"status":"SERVING"`) {
		t.Errorf("reply = %+v body %s", rep, rep.Body)
	}
	if rep.BytesSent == 0 || rep.BytesReceived == 0 {
		t.Errorf("bytes = %d/%d", rep.BytesSent, rep.BytesReceived)
	}

	// The second call reuses what the first learned. The leading slash is optional.
	rep, err = c.Do(context.Background(), Call{Method: healthCheck[1:], Body: []byte(`{"service":"down"}`)})
	if err != nil || !strings.Contains(string(rep.Body), "NOT_SERVING") {
		t.Errorf("second call: %v %s", err, rep.Body)
	}
	// An empty body is an empty message.
	rep, err = c.Do(context.Background(), Call{Method: healthCheck})
	if err != nil || !strings.Contains(string(rep.Body), "SERVING") {
		t.Errorf("empty body: %v %s", err, rep.Body)
	}
}

func TestConn_ServerErrorIsAReply(t *testing.T) {
	srv := grpctest.Start(t, true)
	c := newConn(t, srv.Addr)
	rep, err := c.Do(context.Background(), Call{Method: healthCheck, Body: []byte(`{"service":"nope"}`)})
	if err != nil {
		t.Fatalf("a NotFound answer is a reply, not an error: %v", err)
	}
	if rep.Err == nil || rep.Code != codes.NotFound {
		t.Errorf("reply = %+v", rep)
	}
}

func TestConn_SetupMistakesAreErrors(t *testing.T) {
	srv := grpctest.Start(t, true)
	c := newConn(t, srv.Addr)
	cases := map[string]Call{
		"bad method name":    {Method: "Check"},
		"unknown service":    {Method: "/no.Such/Method"},
		"unknown method":     {Method: "/grpc.health.v1.Health/Nope"},
		"streaming method":   {Method: "/grpc.health.v1.Health/Watch"},
		"bad json":           {Method: healthCheck, Body: []byte(`{"service":`)},
		"unknown json field": {Method: healthCheck, Body: []byte(`{"nope":1}`)},
		"bad encoding":       {Method: healthCheck, Encoding: "xml"},
		"bad base64":         {Method: healthCheck, Encoding: EncodingBase64, Body: []byte("%%%")},
	}
	for name, call := range cases {
		if _, err := c.Do(context.Background(), call); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestConn_Base64NeedsNoReflection(t *testing.T) {
	srv := grpctest.Start(t, false)
	c := newConn(t, srv.Addr)
	// HealthCheckRequest{service: ""} is zero bytes. The reply
	// HealthCheckResponse{status: SERVING} is 08 01.
	rep, err := c.Do(context.Background(), Call{Method: healthCheck, Encoding: EncodingBase64})
	if err != nil || rep.Err != nil {
		t.Fatalf("err = %v, %v", err, rep.Err)
	}
	if got := string(rep.Body); got != base64.StdEncoding.EncodeToString([]byte{0x08, 0x01}) {
		t.Errorf("body = %q", got)
	}

	_, err = c.Do(context.Background(), Call{Method: healthCheck, Body: []byte(`{}`)})
	if err == nil || !strings.Contains(err.Error(), "does not offer reflection") {
		t.Errorf("JSON without reflection: err = %v", err)
	}
}

func TestConn_HeadersAreSent(t *testing.T) {
	srv := grpctest.Start(t, true)
	c := newConn(t, srv.Addr)
	if _, err := c.Do(context.Background(), Call{Method: healthCheck, Headers: map[string]string{"x-token": "abc"}}); err != nil {
		t.Fatal(err)
	}
	if v, ok := srv.LastHeader("x-token"); !ok || v != "abc" {
		t.Errorf("header = %q, %v", v, ok)
	}
}

func TestConn_UnreachableServerIsAReply(t *testing.T) {
	c := newConn(t, "127.0.0.1:1")
	rep, err := c.Do(context.Background(), Call{Method: healthCheck, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("an unreachable server is a failed call, not a setup error: %v", err)
	}
	if rep.Err == nil || rep.Code == codes.OK {
		t.Errorf("reply = %+v", rep)
	}
}

func TestSplitMethod(t *testing.T) {
	s, m, err := splitMethod("/a.b.C/D")
	if err != nil || s != "a.b.C" || m != "D" {
		t.Errorf("got %q %q %v", s, m, err)
	}
	for _, bad := range []string{"", "/", "x", "/x/", "//m"} {
		if _, _, err := splitMethod(bad); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

func TestConn_OlderReflectionProtocol(t *testing.T) {
	srv := grpctest.StartAlphaOnly(t)
	c := newConn(t, srv.Addr)
	rep, err := c.Do(context.Background(), Call{Method: healthCheck, Body: []byte(`{"service":"up"}`)})
	if err != nil || !strings.Contains(string(rep.Body), "SERVING") {
		t.Errorf("v1alpha only: err = %v body %s", err, rep.Body)
	}
}
