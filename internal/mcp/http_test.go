package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func post(t *testing.T, url, body string, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func TestHTTP_PostMCP(t *testing.T) {
	ts := httptest.NewServer(newTestServer().HTTPHandler("", ""))
	defer ts.Close()

	resp := post(t, ts.URL+"/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var out struct {
		Result struct {
			Tools []struct{ Name string } `json:"tools"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Result.Tools) != 2 {
		t.Errorf("tools = %v, want echo and fail", out.Result.Tools)
	}
}

func TestHTTP_NotificationGets202(t *testing.T) {
	ts := httptest.NewServer(newTestServer().HTTPHandler("", ""))
	defer ts.Close()
	resp := post(t, ts.URL+"/mcp", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want 202", resp.StatusCode)
	}
}

func TestHTTP_GetOnMCPIsRejected(t *testing.T) {
	ts := httptest.NewServer(newTestServer().HTTPHandler("", ""))
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

func TestHTTP_TokenRequired(t *testing.T) {
	ts := httptest.NewServer(newTestServer().HTTPHandler("s3cret", ""))
	defer ts.Close()
	body := `{"jsonrpc":"2.0","id":1,"method":"ping"}`

	for name, hdr := range map[string]map[string]string{
		"none":  nil,
		"wrong": {"Authorization": "Bearer nope"},
		"bare":  {"Authorization": "s3cret"},
	} {
		resp := post(t, ts.URL+"/mcp", body, hdr)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", name, resp.StatusCode)
		}
	}
	resp := post(t, ts.URL+"/mcp", body, map[string]string{"Authorization": "Bearer s3cret"})
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("right token: status = %d, want 200", resp.StatusCode)
	}
}

func TestHTTP_HealthzNeedsNoToken(t *testing.T) {
	ts := httptest.NewServer(newTestServer().HTTPHandler("s3cret", ""))
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestHTTP_OriginCheck(t *testing.T) {
	ts := httptest.NewServer(newTestServer().HTTPHandler("", "https://app.example.com"))
	defer ts.Close()
	body := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	cases := map[string]int{
		"http://localhost:3000":   200,
		"http://127.0.0.1":        200,
		"https://app.example.com": 200,
		"https://evil.example":    403,
		"null":                    403,
	}
	for origin, want := range cases {
		resp := post(t, ts.URL+"/mcp", body, map[string]string{"Origin": origin})
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("Origin %q: status = %d, want %d", origin, resp.StatusCode, want)
		}
	}
	// No Origin header (a command-line client) is fine.
	resp := post(t, ts.URL+"/mcp", body, nil)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("no Origin: status = %d", resp.StatusCode)
	}
}

// readEvent reads one SSE event (event name and data) from r.
func readEvent(t *testing.T, r *bufio.Reader) (string, string) {
	t.Helper()
	var name, data string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("reading stream: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if name != "" || data != "" {
				return name, data
			}
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		}
	}
}

func TestHTTP_SSEFlow(t *testing.T) {
	ts := httptest.NewServer(newTestServer().HTTPHandler("", ""))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/sse")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
	rd := bufio.NewReader(resp.Body)
	name, endpoint := readEvent(t, rd)
	if name != "endpoint" || !strings.HasPrefix(endpoint, "/message?sessionId=") {
		t.Fatalf("first event = %q %q", name, endpoint)
	}

	post1 := post(t, ts.URL+endpoint, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"echo","arguments":{"a":1}}}`, nil)
	post1.Body.Close()
	if post1.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /message status = %d, want 202", post1.StatusCode)
	}

	done := make(chan [2]string, 1)
	go func() {
		n, d := readEvent(t, rd)
		done <- [2]string{n, d}
	}()
	select {
	case ev := <-done:
		if ev[0] != "message" {
			t.Fatalf("event = %q", ev[0])
		}
		var out struct {
			ID     int `json:"id"`
			Result struct {
				Content []struct{ Text string } `json:"content"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(ev[1]), &out); err != nil {
			t.Fatalf("data %q: %v", ev[1], err)
		}
		if out.ID != 7 || len(out.Result.Content) != 1 || !strings.Contains(out.Result.Content[0].Text, `"a": 1`) {
			t.Errorf("unexpected response: %s", ev[1])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no response on the stream")
	}
}

func TestHTTP_MessageUnknownSession(t *testing.T) {
	ts := httptest.NewServer(newTestServer().HTTPHandler("", ""))
	defer ts.Close()
	resp := post(t, ts.URL+"/message?sessionId=nope", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestIsLoopbackHost(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "127.0.0.1": true, "::1": true,
		"0.0.0.0": false, "": false, "10.0.0.5": false, "example.com": false,
	} {
		if got := IsLoopbackHost(host); got != want {
			t.Errorf("IsLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}

// A request that is running when the server's base context ends (SIGTERM in
// `vegaload mcp serve -http`) must be cancelled, so the child process of the
// tool call is killed and no load test keeps running.
func TestHTTP_BaseContextCancelStopsRunningTool(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	s := NewServer()
	s.Register(Tool{Name: "block", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	}})
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ts := httptest.NewUnstartedServer(s.HTTPHandler("", ""))
	ts.Config.BaseContext = func(net.Listener) context.Context { return base }
	ts.Start()
	defer ts.Close()

	go func() {
		resp, err := http.Post(ts.URL+"/mcp", "application/json",
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"block"}}`))
		if err == nil {
			resp.Body.Close()
		}
	}()
	<-started
	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the running tool was not cancelled when the base context ended")
	}
}

// The same for a tool started through the SSE stream: ending the base context
// ends the stream, and that cancels the tool.
func TestHTTP_BaseContextCancelStopsSSETool(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	s := NewServer()
	s.Register(Tool{Name: "block", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	}})
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ts := httptest.NewUnstartedServer(s.HTTPHandler("", ""))
	ts.Config.BaseContext = func(net.Listener) context.Context { return base }
	ts.Start()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/sse")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, endpoint := readEvent(t, bufio.NewReader(resp.Body))
	post1 := post(t, ts.URL+endpoint, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"block"}}`, nil)
	post1.Body.Close()
	<-started
	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the SSE tool was not cancelled when the base context ended")
	}
}
