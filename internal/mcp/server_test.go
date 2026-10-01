package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func newTestServer() *Server {
	s := NewServer()
	s.Register(Tool{
		Name:        "echo",
		Description: "echoes its input",
		InputSchema: map[string]any{"type": "object"},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in map[string]any
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, err
			}
			return in, nil
		},
	})
	s.Register(Tool{
		Name:        "fail",
		Description: "always fails",
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			return nil, fmt.Errorf("boom")
		},
	})
	return s
}

func serveOneLine(t *testing.T, s *Server, line string) map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(line+"\n"), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response %q: %v", out.String(), err)
	}
	return resp
}

func TestServer_Initialize(t *testing.T) {
	s := newTestServer()
	resp := serveOneLine(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected a result object, got %v", resp)
	}
	if result["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion = %v, want %v", result["protocolVersion"], protocolVersion)
	}
}

func TestServer_ToolsList(t *testing.T) {
	s := newTestServer()
	resp := serveOneLine(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	result := resp["result"].(map[string]any)
	tools := result["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d: %v", len(tools), tools)
	}
}

func TestServer_ToolsCall_Success(t *testing.T) {
	s := newTestServer()
	resp := serveOneLine(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"x":1}}}`)
	result := resp["result"].(map[string]any)
	content := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("expected one content block, got %v", content)
	}
	block := content[0].(map[string]any)
	if !strings.Contains(block["text"].(string), `"x": 1`) {
		t.Errorf("expected echoed input in text, got %q", block["text"])
	}
	if _, isErr := result["isError"]; isErr {
		t.Error("did not expect isError on a successful call")
	}
}

func TestServer_ToolsCall_HandlerError(t *testing.T) {
	s := newTestServer()
	resp := serveOneLine(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"fail","arguments":{}}}`)
	result := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("expected isError: true, got %v", result)
	}
	content := result["content"].([]any)
	block := content[0].(map[string]any)
	if !strings.Contains(block["text"].(string), "boom") {
		t.Errorf("expected the handler's error message in text, got %q", block["text"])
	}
}

func TestServer_ToolsCall_UnknownTool(t *testing.T) {
	s := newTestServer()
	resp := serveOneLine(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nope","arguments":{}}}`)
	result := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("expected isError: true for an unknown tool, got %v", result)
	}
}

func TestServer_UnknownMethod(t *testing.T) {
	s := newTestServer()
	resp := serveOneLine(t, s, `{"jsonrpc":"2.0","id":1,"method":"bogus"}`)
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected an error object, got %v", resp)
	}
	if int(errObj["code"].(float64)) != codeMethodNotFound {
		t.Errorf("code = %v, want %d", errObj["code"], codeMethodNotFound)
	}
}

func TestServer_Notification_NoResponse(t *testing.T) {
	s := newTestServer()
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("expected no response to a notification, got %q", out.String())
	}
}

func TestServer_MultipleLines(t *testing.T) {
	s := newTestServer()
	var out bytes.Buffer
	input := `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"ping"}` + "\n"
	if err := s.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 response lines, got %d: %q", len(lines), out.String())
	}
}

func TestServer_ParseError(t *testing.T) {
	s := newTestServer()
	resp := serveOneLine(t, s, `not json`)
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected an error object for invalid JSON, got %v", resp)
	}
	if int(errObj["code"].(float64)) != codeParseError {
		t.Errorf("code = %v, want %d", errObj["code"], codeParseError)
	}
}
