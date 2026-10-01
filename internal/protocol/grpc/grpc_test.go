package grpc

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/vegaload/vegaload/internal/protocol"
)

// echoHandler is a grpc.StreamHandler registered as the server's
// UnknownServiceHandler, so it answers any method (there are no real
// services registered) by reading one raw-codec message and sending it
// straight back. This is the server-side counterpart to Driver's raw,
// no-proto-stubs approach.
func echoHandler(_ any, stream grpc.ServerStream) error {
	var req []byte
	if err := stream.RecvMsg(&req); err != nil {
		return err
	}
	return stream.SendMsg(req)
}

// newEchoServer starts an in-process plaintext gRPC server that echoes
// back whatever raw bytes it receives, for any method name.
func newEchoServer(t *testing.T) (addr string, stop func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	srv := grpc.NewServer(grpc.UnknownServiceHandler(echoHandler))
	go srv.Serve(lis) //nolint:errcheck // Stop() below ends the test either way

	return lis.Addr().String(), srv.Stop
}

func TestDriver_Do_EchoesOverPlaintext(t *testing.T) {
	addr, stop := newEchoServer(t)
	defer stop()

	d, err := New(protocol.Target{
		URL:    addr,
		Method: "/vegaload.test.Echo/Call",
		Body:   []byte("ping"),
	}, time.Second)
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
	if res.StatusCode != int(codes.OK) {
		t.Errorf("StatusCode = %d, want %d (OK)", res.StatusCode, codes.OK)
	}
	if res.BytesSent != 4 {
		t.Errorf("BytesSent = %d, want 4", res.BytesSent)
	}
	if res.BytesReceived != 4 {
		t.Errorf("BytesReceived = %d, want 4 (the server echoes the request back)", res.BytesReceived)
	}
}

func TestDriver_Do_ExplicitGRPCScheme(t *testing.T) {
	addr, stop := newEchoServer(t)
	defer stop()

	d, err := New(protocol.Target{
		URL:    "grpc://" + addr,
		Method: "/vegaload.test.Echo/Call",
	}, time.Second)
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
}

func TestDriver_Do_ConnectionErrorIsNotAFatalError(t *testing.T) {
	d, err := New(protocol.Target{
		URL:    "127.0.0.1:1",
		Method: "/vegaload.test.Echo/Call",
	}, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer d.Close()

	res, doErr := d.Do(context.Background())
	if doErr != nil {
		t.Fatalf("Do returned a fatal error for a connection failure: %v", doErr)
	}
	if res.Success {
		t.Error("expected Success false when the target is unreachable")
	}
	if res.Err == nil {
		t.Error("expected Result.Err to describe the failure")
	}
	if res.StatusCode == int(codes.OK) {
		t.Error("expected a non-OK status code for an unreachable target")
	}
}

func TestNew_FatalErrorOnMissingMethod(t *testing.T) {
	if _, err := New(protocol.Target{URL: "127.0.0.1:1"}, time.Second); err == nil {
		t.Error("expected a fatal error when Method is not set")
	}
}

func TestParseAddr(t *testing.T) {
	cases := []struct {
		name       string
		target     string
		wantAddr   string
		wantScheme string // "tls" or "plaintext"
		wantErr    bool
	}{
		{name: "bare host:port", target: "localhost:1234", wantAddr: "localhost:1234", wantScheme: "plaintext"},
		{name: "explicit grpc://", target: "grpc://localhost:1234", wantAddr: "localhost:1234", wantScheme: "plaintext"},
		{name: "explicit grpcs://", target: "grpcs://localhost:1234", wantAddr: "localhost:1234", wantScheme: "tls"},
		{name: "unsupported scheme", target: "http://localhost:1234", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			addr, creds, err := parseAddr(c.target, false)
			if c.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAddr returned error: %v", err)
			}
			if addr != c.wantAddr {
				t.Errorf("addr = %q, want %q", addr, c.wantAddr)
			}
			gotScheme := "plaintext"
			if creds.Info().SecurityProtocol == "tls" {
				gotScheme = "tls"
			}
			if gotScheme != c.wantScheme {
				t.Errorf("scheme = %q, want %q", gotScheme, c.wantScheme)
			}
		})
	}
}

func TestDriver_Name(t *testing.T) {
	d, err := New(protocol.Target{URL: "localhost:1234", Method: "/x/y"}, time.Second)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer d.Close()

	if d.Name() != "grpc" {
		t.Errorf("Name() = %q, want %q", d.Name(), "grpc")
	}
}
