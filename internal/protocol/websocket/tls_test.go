package websocket

import (
	"context"
	"crypto/tls"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	gws "github.com/gorilla/websocket"

	"github.com/vegaload/vegaload/internal/protocol"
	"github.com/vegaload/vegaload/internal/tlsconf/tlstest"
)

func TestTLSFiles_Errors(t *testing.T) {
	m := tlstest.New(t)
	for _, c := range tlstest.ErrorCases(t, m, "wss://127.0.0.1:1", "ws://127.0.0.1:1") {
		t.Run(c.Name, func(t *testing.T) {
			_, err := New(protocol.Target{URL: c.URL, Options: c.Opts, InsecureSkipVerify: c.Skip}, time.Second)
			if err == nil || !strings.HasPrefix(err.Error(), "websocket:") || !strings.Contains(err.Error(), c.Want) {
				t.Fatalf("%v, want websocket: ... %s", err, c.Want)
			}
			if c.Hide != "" && strings.Contains(err.Error(), c.Hide) {
				t.Fatalf("leaked %s: %v", c.Hide, err)
			}
		})
	}
}

func TestTLSFiles_Handshake(t *testing.T) {
	m := tlstest.New(t)
	other := tlstest.New(t)
	line := tlstest.KeyLine(t, m.ClientKeyPath)
	url := serveWS(t, m.ServerConfig(false))
	if res := wsDo(t, url, nil); res.Success || !strings.Contains(res.Err.Error(), "x509") {
		t.Fatalf("no ca: %+v", res.Err)
	}
	good := map[string]string{"ca_file": m.CACertPath, "tls_server_name": tlstest.Name}
	if res := wsDo(t, url, good); !res.Success {
		t.Fatal(res.Err)
	}
	wrong := wsDo(t, url, map[string]string{"ca_file": other.CACertPath, "tls_server_name": tlstest.Name})
	if wrong.Success || !strings.Contains(wrong.Err.Error(), "x509") || strings.Contains(wrong.Err.Error(), line) {
		t.Fatalf("wrong ca: %+v", wrong.Err)
	}
	if res := wsDo(t, url, map[string]string{"ca_file": m.CACertPath}); res.Success || !strings.Contains(res.Err.Error(), "certificate") {
		t.Fatalf("name: %+v", res.Err)
	}

	auth := serveWS(t, m.ServerConfig(true))
	if res := wsDo(t, auth, good); res.Success {
		t.Fatal("client cert was not required")
	}
	pair := map[string]string{
		"ca_file": m.CACertPath, "tls_server_name": tlstest.Name,
		"cert_file": m.ClientCertPath, "key_file": m.ClientKeyPath,
	}
	if res := wsDo(t, auth, pair); !res.Success {
		t.Fatal(res.Err)
	}
	bad := map[string]string{
		"ca_file": m.CACertPath, "tls_server_name": tlstest.Name,
		"cert_file": other.ClientCertPath, "key_file": other.ClientKeyPath,
	}
	if res := wsDo(t, auth, bad); res.Success || strings.Contains(res.Err.Error(), line) {
		t.Fatalf("other ca pair: %+v", res.Err)
	}
}

func TestTLSFiles_DoKeepsFiles(t *testing.T) {
	m := tlstest.New(t)
	url := serveWS(t, m.ServerConfig(false))
	d, err := New(protocol.Target{URL: url, Options: map[string]string{"ca_file": m.CACertPath, "tls_server_name": tlstest.Name}}, 3*time.Second)
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

func serveWS(t *testing.T, cfg *tls.Config) string {
	t.Helper()
	ln := tlstest.Listen(t, cfg)
	up := gws.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_, msg, err := c.ReadMessage()
		if err != nil {
			return
		}
		_ = c.WriteMessage(gws.TextMessage, msg)
	})}
	go srv.Serve(ln) //nolint:errcheck // the test closes it
	t.Cleanup(func() { _ = srv.Close() })
	return "wss://" + ln.Addr().String()
}

func wsDo(t *testing.T, url string, opts map[string]string) protocol.Result {
	t.Helper()
	d, err := New(protocol.Target{URL: url, Body: []byte("hi"), Options: opts}, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res, err := d.Do(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return res
}
