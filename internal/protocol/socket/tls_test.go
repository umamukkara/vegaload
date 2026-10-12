package socket

import (
	"context"
	"crypto/tls"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
	"github.com/vegaload/vegaload/internal/tlsconf/tlstest"
)

func TestTLSFiles_Errors(t *testing.T) {
	m := tlstest.New(t)
	for _, c := range tlstest.ErrorCases(t, m, "tcp://127.0.0.1:1", "tcp://127.0.0.1:1") {
		t.Run("tcp/"+c.Name, func(t *testing.T) {
			opts := clone(c.Opts)
			if !strings.Contains(c.Name, "off") && c.Name != "tls off" {
				opts["tls"] = "true"
			}
			if c.Name == "tls off" || strings.HasSuffix(c.Name, "off") {
				delete(opts, "tls")
			}
			_, err := NewTCP(protocol.Target{URL: c.URL, Options: opts, InsecureSkipVerify: c.Skip}, time.Second)
			if err == nil || !strings.HasPrefix(err.Error(), "tcp:") || !strings.Contains(err.Error(), c.Want) {
				t.Fatalf("%v, want tcp: ... %s", err, c.Want)
			}
			if c.Hide != "" && strings.Contains(err.Error(), c.Hide) {
				t.Fatalf("leaked %s: %v", c.Hide, err)
			}
		})
	}
	for _, key := range []string{"ca_file", "cert_file", "key_file", "tls_server_name"} {
		_, err := NewUDP(protocol.Target{
			URL: "udp://127.0.0.1:1", Body: []byte("x"),
			Options: map[string]string{key: m.CACertPath},
		}, time.Second)
		if err == nil || !strings.Contains(err.Error(), "needs TLS, but TLS is off") {
			t.Fatalf("%s: %v", key, err)
		}
	}
}

func TestTLSFiles_Handshake(t *testing.T) {
	m := tlstest.New(t)
	other := tlstest.New(t)
	line := tlstest.KeyLine(t, m.ClientKeyPath)
	url := serveTCP(t, m.ServerConfig(false))
	if res := dialTLS(t, url, map[string]string{"tls": "true"}); res.Success || !strings.Contains(res.Err.Error(), "x509") {
		t.Fatalf("no ca: %+v", res.Err)
	}
	good := map[string]string{"tls": "true", "ca_file": m.CACertPath, "tls_server_name": tlstest.Name}
	if res := dialTLS(t, url, good); !res.Success {
		t.Fatal(res.Err)
	}
	wrong := dialTLS(t, url, map[string]string{"tls": "true", "ca_file": other.CACertPath, "tls_server_name": tlstest.Name})
	if wrong.Success || !strings.Contains(wrong.Err.Error(), "x509") || strings.Contains(wrong.Err.Error(), line) {
		t.Fatalf("wrong ca: %+v", wrong.Err)
	}
	if res := dialTLS(t, url, map[string]string{"tls": "true", "ca_file": m.CACertPath}); res.Success || !strings.Contains(res.Err.Error(), "certificate") {
		t.Fatalf("name: %+v", res.Err)
	}

	auth := serveTCP(t, m.ServerConfig(true))
	if res := dialTLS(t, auth, good); res.Success {
		t.Fatal("client cert was not required")
	}
	pair := map[string]string{
		"tls": "true", "ca_file": m.CACertPath, "tls_server_name": tlstest.Name,
		"cert_file": m.ClientCertPath, "key_file": m.ClientKeyPath,
	}
	if res := dialTLS(t, auth, pair); !res.Success {
		t.Fatal(res.Err)
	}
	bad := map[string]string{
		"tls": "true", "ca_file": m.CACertPath, "tls_server_name": tlstest.Name,
		"cert_file": other.ClientCertPath, "key_file": other.ClientKeyPath,
	}
	if res := dialTLS(t, auth, bad); res.Success || strings.Contains(res.Err.Error(), line) {
		t.Fatalf("other ca pair: %+v", res.Err)
	}
}

func TestTLSFiles_DoKeepsFiles(t *testing.T) {
	m := tlstest.New(t)
	url := serveTCP(t, m.ServerConfig(false))
	d, err := NewTCP(protocol.Target{URL: url, Options: map[string]string{"tls": "true", "ca_file": m.CACertPath, "tls_server_name": tlstest.Name}}, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(m.CACertPath); err != nil {
		t.Fatal(err)
	}
	res, err := d.Do(context.Background())
	if err != nil || !res.Success {
		t.Fatalf("err=%v res=%+v", err, res.Err)
	}
}

func clone(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func serveTCP(t *testing.T, cfg *tls.Config) string {
	t.Helper()
	ln := tlstest.Listen(t, cfg)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				if tc, ok := c.(*tls.Conn); ok {
					if err := tc.Handshake(); err != nil {
						return
					}
				}
				_, _ = c.Write([]byte("P"))
				_, _ = io.Copy(io.Discard, c)
			}()
		}
	}()
	return "tcp://" + ln.Addr().String()
}

func dialTLS(t *testing.T, url string, opts map[string]string) protocol.Result {
	t.Helper()
	opts = clone(opts)
	opts["read"] = "1"
	d, err := NewTCP(protocol.Target{URL: url, Body: []byte("x"), Options: opts}, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res, err := d.Do(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return res
}
