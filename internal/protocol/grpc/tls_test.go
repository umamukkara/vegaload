package grpc

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
	"github.com/vegaload/vegaload/internal/protocol/grpc/grpctest"
	"github.com/vegaload/vegaload/internal/tlsconf"
	"github.com/vegaload/vegaload/internal/tlsconf/tlstest"
)

const healthMethod = "/grpc.health.v1.Health/Check"

func TestTLSFiles_Errors(t *testing.T) {
	m := tlstest.New(t)
	for _, c := range tlstest.ErrorCases(t, m, "grpcs://127.0.0.1:1", "grpc://127.0.0.1:1") {
		t.Run(c.Name, func(t *testing.T) {
			_, err := New(protocol.Target{URL: c.URL, Method: healthMethod, Options: c.Opts, InsecureSkipVerify: c.Skip}, time.Second)
			if err == nil || !strings.HasPrefix(err.Error(), "grpc:") || !strings.Contains(err.Error(), c.Want) {
				t.Fatalf("%v, want grpc: ... %s", err, c.Want)
			}
			if c.Hide != "" && strings.Contains(err.Error(), c.Hide) {
				t.Fatalf("leaked %s: %v", c.Hide, err)
			}
		})
	}
	if _, err := New(protocol.Target{URL: "grpc://127.0.0.1:1", Options: map[string]string{"ca_file": m.CACertPath}}, time.Second); err == nil || !strings.Contains(err.Error(), "Method") {
		t.Fatalf("missing method: %v", err)
	}
}

func TestTLSFiles_Handshake(t *testing.T) {
	m := tlstest.New(t)
	other := tlstest.New(t)
	line := tlstest.KeyLine(t, m.ClientKeyPath)
	s := grpctest.StartTLS(t, m.ServerConfig(false))
	url := "grpcs://" + s.Addr
	if res := grpcDo(t, url, nil); res.Success || !strings.Contains(res.Err.Error(), "x509") {
		t.Fatalf("no ca: %+v", res.Err)
	}
	good := map[string]string{"ca_file": m.CACertPath, "tls_server_name": tlstest.Name}
	if res := grpcDo(t, url, good); !res.Success {
		t.Fatal(res.Err)
	}
	wrong := grpcDo(t, url, map[string]string{"ca_file": other.CACertPath, "tls_server_name": tlstest.Name})
	if wrong.Success || !strings.Contains(wrong.Err.Error(), "x509") || strings.Contains(wrong.Err.Error(), line) {
		t.Fatalf("wrong ca: %+v", wrong.Err)
	}
	if res := grpcDo(t, url, map[string]string{"ca_file": m.CACertPath}); res.Success || !strings.Contains(res.Err.Error(), "certificate") {
		t.Fatalf("name: %+v", res.Err)
	}

	auth := grpctest.StartTLS(t, m.ServerConfig(true))
	authURL := "grpcs://" + auth.Addr
	if res := grpcDo(t, authURL, good); res.Success {
		t.Fatal("client cert was not required")
	}
	pair := map[string]string{
		"ca_file": m.CACertPath, "tls_server_name": tlstest.Name,
		"cert_file": m.ClientCertPath, "key_file": m.ClientKeyPath,
	}
	if res := grpcDo(t, authURL, pair); !res.Success {
		t.Fatal(res.Err)
	}
	bad := map[string]string{
		"ca_file": m.CACertPath, "tls_server_name": tlstest.Name,
		"cert_file": other.ClientCertPath, "key_file": other.ClientKeyPath,
	}
	if res := grpcDo(t, authURL, bad); res.Success || strings.Contains(res.Err.Error(), line) {
		t.Fatalf("other ca pair: %+v", res.Err)
	}
}

func TestTLSFiles_CallKeepsFiles(t *testing.T) {
	m := tlstest.New(t)
	s := grpctest.StartTLS(t, m.ServerConfig(false))
	conn, err := NewConnWithFiles("grpcs://"+s.Addr, false, tlsconf.Files{CAFile: m.CACertPath, ServerName: tlstest.Name})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := os.Remove(m.CACertPath); err != nil {
		t.Fatal(err)
	}
	rep, err := conn.Do(context.Background(), Call{Method: healthMethod, Encoding: EncodingBase64})
	if err != nil || rep.Err != nil {
		t.Fatalf("err=%v rep=%v", err, rep.Err)
	}
}

func grpcDo(t *testing.T, url string, opts map[string]string) protocol.Result {
	t.Helper()
	d, err := New(protocol.Target{URL: url, Method: healthMethod, Options: opts}, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	res, err := d.Do(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return res
}
