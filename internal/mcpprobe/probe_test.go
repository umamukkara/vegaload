package mcpprobe

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/mcp"
)

// serve runs a real internal/mcp server over in-memory pipes and
// returns the client's ends.
func serve(t *testing.T, tools []mcp.Tool) (io.Reader, io.Writer) {
	t.Helper()
	srvIn, clientOut := io.Pipe()
	clientIn, srvOut := io.Pipe()
	s := mcp.NewServer()
	for _, tool := range tools {
		s.Register(tool)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		_ = s.Serve(ctx, srvIn, srvOut)
		_ = srvOut.Close()
	}()
	return clientIn, clientOut
}

func TestHandshake_ListsToolsSorted(t *testing.T) {
	r, w := serve(t, mcp.NewTools("/nonexistent/vegaload"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	info, err := Handshake(ctx, r, w)
	if err != nil {
		t.Fatalf("Handshake: %v", err)
	}
	if info.ServerName != "vegaload" {
		t.Errorf("ServerName = %q, want vegaload", info.ServerName)
	}
	if missing := info.MissingTools(mcp.CoreToolNames); len(missing) != 0 {
		t.Errorf("missing tools: %v (got %v)", missing, info.Tools)
	}
	for i := 1; i < len(info.Tools); i++ {
		if info.Tools[i-1] > info.Tools[i] {
			t.Fatalf("tools not sorted: %v", info.Tools)
		}
	}
}

func TestHandshake_ServerClosesEarly(t *testing.T) {
	clientIn, srvOut := io.Pipe()
	_, clientOut := io.Pipe()
	_ = srvOut.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := Handshake(ctx, clientIn, discardWriter{clientOut})
	if err == nil {
		t.Fatal("expected an error when the server closes without answering")
	}
}

// discardWriter swallows writes so the test does not block on an
// unread pipe.
type discardWriter struct{ io.Writer }

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestProbe_CommandNotFound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := Probe(ctx, Command{Path: "/nonexistent/vegaload"}); err == nil {
		t.Fatal("expected an error for a missing command")
	}
}
