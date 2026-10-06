// Package mcpprobe is a minimal MCP client for health checks. It starts
// an MCP server command over stdio, performs the initialize handshake,
// and lists the server's tools. `vegaload doctor` uses it to prove that
// the exact command an agent host has configured really starts and
// answers.
//
// It speaks the same newline-delimited JSON-RPC 2.0 as internal/mcp and
// knows nothing about what any tool does.
package mcpprobe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// protocolVersion is the MCP revision this probe offers. Servers that
// speak a different one answer with their own, which is fine here.
const protocolVersion = "2024-11-05"

// Info is what a successful probe learned about the server.
type Info struct {
	ServerName      string
	ProtocolVersion string
	Tools           []string // sorted
}

// Command is an MCP server launch command, as an agent host would run it.
type Command struct {
	Path string
	Args []string
	Env  map[string]string // added to the current environment
	Dir  string            // working directory; empty for the current one
}

// Probe starts c, runs the handshake, and stops the process. Errors
// include the tail of the server's stderr, which is where a crashing
// server explains itself.
func Probe(ctx context.Context, c Command) (Info, error) {
	cmd := exec.CommandContext(ctx, c.Path, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = os.Environ()
	for k, v := range c.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Info{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Info{}, err
	}
	var stderr tailBuffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return Info{}, fmt.Errorf("starting %s: %w", c.Path, err)
	}
	info, probeErr := Handshake(ctx, stdout, stdin)
	_ = stdin.Close()
	waitDone := make(chan struct{})
	go func() { _ = cmd.Wait(); close(waitDone) }()
	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		<-waitDone
	}
	if probeErr != nil {
		if tail := strings.TrimSpace(stderr.String()); tail != "" {
			return info, fmt.Errorf("%w (server stderr: %s)", probeErr, tail)
		}
		return info, probeErr
	}
	return info, nil
}

// Handshake runs initialize and tools/list over an already-connected
// stream. It is split from Probe so tests can drive it in process.
func Handshake(ctx context.Context, r io.Reader, w io.Writer) (Info, error) {
	lines := make(chan []byte)
	scanErr := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		for sc.Scan() {
			b := bytes.TrimSpace(sc.Bytes())
			if len(b) == 0 {
				continue
			}
			cp := make([]byte, len(b))
			copy(cp, b)
			select {
			case lines <- cp:
			case <-ctx.Done():
				return
			}
		}
		scanErr <- sc.Err()
		close(lines)
	}()

	send := func(v any) error {
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = w.Write(append(data, '\n'))
		return err
	}

	// call sends a request and waits for the response with the same ID.
	// Lines that are not JSON-RPC responses (log noise, notifications)
	// are skipped, since some servers print to stdout before they are ready.
	call := func(id int, method string, params any) (json.RawMessage, error) {
		if err := send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
			return nil, fmt.Errorf("sending %s: %w", method, err)
		}
		for {
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("waiting for %s: %w", method, ctx.Err())
			case line, ok := <-lines:
				if !ok {
					select {
					case err := <-scanErr:
						if err != nil {
							return nil, err
						}
					default:
					}
					return nil, fmt.Errorf("server closed the connection before answering %s", method)
				}
				var resp struct {
					ID     json.RawMessage `json:"id"`
					Result json.RawMessage `json:"result"`
					Error  *struct {
						Code    int    `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				if err := json.Unmarshal(line, &resp); err != nil || len(resp.ID) == 0 {
					continue
				}
				if string(resp.ID) != fmt.Sprint(id) {
					continue
				}
				if resp.Error != nil {
					return nil, fmt.Errorf("%s failed: %s (code %d)", method, resp.Error.Message, resp.Error.Code)
				}
				return resp.Result, nil
			}
		}
	}

	var info Info
	raw, err := call(1, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "vegaload-doctor", "version": "1"},
	})
	if err != nil {
		return info, err
	}
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(raw, &init); err != nil {
		return info, fmt.Errorf("initialize returned an unreadable result: %w", err)
	}
	info.ServerName = init.ServerInfo.Name
	info.ProtocolVersion = init.ProtocolVersion

	if err := send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		return info, fmt.Errorf("sending notifications/initialized: %w", err)
	}

	raw, err = call(2, "tools/list", map[string]any{})
	if err != nil {
		return info, err
	}
	var list struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return info, fmt.Errorf("tools/list returned an unreadable result: %w", err)
	}
	for _, t := range list.Tools {
		info.Tools = append(info.Tools, t.Name)
	}
	sort.Strings(info.Tools)
	return info, nil
}

// MissingTools returns the names in want that info does not list.
func (i Info) MissingTools(want []string) []string {
	have := map[string]bool{}
	for _, t := range i.Tools {
		have[t] = true
	}
	var missing []string
	for _, w := range want {
		if !have[w] {
			missing = append(missing, w)
		}
	}
	return missing
}

// tailBuffer keeps the last few KB written to it.
type tailBuffer struct{ b []byte }

const tailMax = 2048

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > tailMax {
		t.b = t.b[len(t.b)-tailMax:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.b) }
