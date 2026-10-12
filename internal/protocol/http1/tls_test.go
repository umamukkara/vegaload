package http1

import (
	"context"
	"crypto/tls"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
	"github.com/vegaload/vegaload/internal/tlsconf/tlstest"
)

func TestTLSFiles_Errors(t *testing.T) {
	m := tlstest.New(t)
	for _, c := range tlstest.ErrorCases(t, m, "https://127.0.0.1:1", "http://127.0.0.1:1") {
		t.Run(c.Name, func(t *testing.T) {
			_, err := New(protocol.Target{URL: c.URL, Options: c.Opts, InsecureSkipVerify: c.Skip}, time.Second)
			if err == nil || !strings.HasPrefix(err.Error(), "http1:") || !strings.Contains(err.Error(), c.Want) {
				t.Fatalf("%v, want http1: ... %s", err, c.Want)
			}
			if c.Hide != "" && strings.Contains(err.Error(), c.Hide) {
				t.Fatalf("leaked %s: %v", c.Hide, err)
			}
		})
	}
	_, err := New(protocol.Target{
		URL:     "https://127.0.0.1:1",
		Options: map[string]string{"cert_file": m.ClientCertPath, "key_file": m.ClientKeyPath},
	}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
}

func TestTLSFiles_Handshake(t *testing.T) {
	m := tlstest.New(t)
	other := tlstest.New(t)
	line := tlstest.KeyLine(t, m.ClientKeyPath)
	url := serveHTTP(t, m.ServerConfig(false), okHandler)
	if res := httpDo(t, url, nil); res.Success || !strings.Contains(res.Err.Error(), "x509") {
		t.Fatalf("no ca: %+v", res.Err)
	}
	good := map[string]string{"ca_file": m.CACertPath, "tls_server_name": tlstest.Name}
	if res := httpDo(t, url, good); !res.Success {
		t.Fatal(res.Err)
	}
	wrong := httpDo(t, url, map[string]string{"ca_file": other.CACertPath, "tls_server_name": tlstest.Name})
	if wrong.Success || !strings.Contains(wrong.Err.Error(), "x509") || strings.Contains(wrong.Err.Error(), line) {
		t.Fatalf("wrong ca: %+v", wrong.Err)
	}
	if res := httpDo(t, url, map[string]string{"ca_file": m.CACertPath}); res.Success || !strings.Contains(res.Err.Error(), "certificate") {
		t.Fatalf("name: %+v", res.Err)
	}

	authURL := serveHTTP(t, m.ServerConfig(true), okHandler)
	if res := httpDo(t, authURL, good); res.Success {
		t.Fatal("client cert was not required")
	}
	pair := map[string]string{
		"ca_file": m.CACertPath, "tls_server_name": tlstest.Name,
		"cert_file": m.ClientCertPath, "key_file": m.ClientKeyPath,
	}
	if res := httpDo(t, authURL, pair); !res.Success {
		t.Fatal(res.Err)
	}
	bad := map[string]string{
		"ca_file": m.CACertPath, "tls_server_name": tlstest.Name,
		"cert_file": other.ClientCertPath, "key_file": other.ClientKeyPath,
	}
	if res := httpDo(t, authURL, bad); res.Success || strings.Contains(res.Err.Error(), line) {
		t.Fatalf("other ca pair: %+v", res.Err)
	}
}

func TestTLSFiles_DoKeepsFiles(t *testing.T) {
	m := tlstest.New(t)
	url := serveHTTP(t, m.ServerConfig(false), okHandler)
	opts := map[string]string{"ca_file": m.CACertPath, "tls_server_name": tlstest.Name}
	d, err := New(protocol.Target{URL: url, Options: opts}, 3*time.Second)
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

var okHandler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

func serveHTTP(t *testing.T, cfg *tls.Config, h http.Handler) string {
	t.Helper()
	ln := tlstest.Listen(t, cfg)
	srv := &http.Server{Handler: h}
	go srv.Serve(ln) //nolint:errcheck // the test closes it
	t.Cleanup(func() { _ = srv.Close() })
	return "https://" + ln.Addr().String()
}

func httpDo(t *testing.T, url string, opts map[string]string) protocol.Result {
	t.Helper()
	d, err := New(protocol.Target{URL: url, Options: opts}, 3*time.Second)
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
