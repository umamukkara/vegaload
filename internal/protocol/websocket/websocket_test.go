package websocket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/vegaload/vegaload/internal/protocol"
)

var upgrader = websocket.Upgrader{}

// newEchoServer starts an httptest server that upgrades every connection
// to WebSocket and echoes back whatever message it receives.
func newEchoServer(t *testing.T, onRequest func(r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if onRequest != nil {
			onRequest(r)
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		defer conn.Close()
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, msg)
	}))
	return srv
}

func toWS(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}

func TestDriver_Do_EchoRoundTrip(t *testing.T) {
	srv := newEchoServer(t, nil)
	defer srv.Close()

	d, err := New(protocol.Target{URL: toWS(srv.URL), Body: []byte("ping")}, time.Second)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer d.Close()

	res, err := d.Do(context.Background())
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	if !res.Success {
		t.Errorf("expected Success true, got Result %+v", res)
	}
	if res.BytesSent != 4 {
		t.Errorf("BytesSent = %d, want 4", res.BytesSent)
	}
	if res.BytesReceived != 4 {
		t.Errorf("BytesReceived = %d, want 4 (the server echoes the message back)", res.BytesReceived)
	}
}

func TestDriver_Do_NoBodyIsAConnectProbe(t *testing.T) {
	srv := newEchoServer(t, nil)
	defer srv.Close()

	d, err := New(protocol.Target{URL: toWS(srv.URL)}, time.Second)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer d.Close()

	res, err := d.Do(context.Background())
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	if !res.Success {
		t.Errorf("expected Success true for a bare connect probe, got Result %+v", res)
	}
	if res.BytesSent != 0 || res.BytesReceived != 0 {
		t.Errorf("expected zero bytes for a connect probe, got %+v", res)
	}
}

func TestDriver_Do_SendsHandshakeHeaders(t *testing.T) {
	var gotHeader string
	srv := newEchoServer(t, func(r *http.Request) {
		gotHeader = r.Header.Get("X-VegaLoad")
	})
	defer srv.Close()

	d, err := New(protocol.Target{
		URL:     toWS(srv.URL),
		Headers: map[string]string{"X-VegaLoad": "test"},
		Body:    []byte("ping"),
	}, time.Second)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer d.Close()

	if _, err := d.Do(context.Background()); err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	if gotHeader != "test" {
		t.Errorf("server saw X-VegaLoad header %q, want %q", gotHeader, "test")
	}
}

func TestDriver_Do_ConnectionErrorIsNotAFatalError(t *testing.T) {
	d, err := New(protocol.Target{URL: "ws://127.0.0.1:1"}, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer d.Close()

	res, doErr := d.Do(context.Background())
	if doErr != nil {
		t.Fatalf("Do returned a fatal error for a connection failure: %v", doErr)
	}
	if res.Success {
		t.Error("expected Success false when the connection fails")
	}
	if res.Err == nil {
		t.Error("expected Result.Err to describe the connection failure")
	}
}

func TestNew_FatalErrorOnUnsupportedScheme(t *testing.T) {
	if _, err := New(protocol.Target{URL: "http://example.invalid"}, time.Second); err == nil {
		t.Error("expected a fatal error for a non-ws(s) scheme")
	}
}

func TestNew_FatalErrorOnMalformedURL(t *testing.T) {
	if _, err := New(protocol.Target{URL: "://not a url"}, time.Second); err == nil {
		t.Error("expected a fatal error for a malformed target URL")
	}
}

func TestDriver_Name(t *testing.T) {
	d, err := New(protocol.Target{URL: "ws://example.invalid"}, time.Second)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer d.Close()

	if d.Name() != "websocket" {
		t.Errorf("Name() = %q, want %q", d.Name(), "websocket")
	}
}
