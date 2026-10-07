package mcp

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// maxHTTPBody caps one JSON-RPC request body. A request carries tool
// arguments only, so this is generous.
const maxHTTPBody = 4 << 20

// HTTPHandler returns an http.Handler that serves this MCP server over
// HTTP (FR-MCP-02's optional HTTP/SSE transport). It offers two ways in:
//
//   - POST /mcp takes one JSON-RPC message and answers with the JSON-RPC
//     response in the body (202 with no body for a notification). This is
//     the simple path for scripts and for clients that do not need a stream.
//   - GET /sse opens a Server-Sent Events stream, the transport of MCP
//     protocol version 2024-11-05. The first event, "endpoint", names the
//     URL to POST messages to (/message?sessionId=...). Each response is
//     sent back on the stream as a "message" event.
//
// When token is not empty, every request must carry
// "Authorization: Bearer <token>". Requests with an Origin header that is
// not a loopback host are refused unless allowOrigin matches it, so a web
// page in the user's browser cannot reach a local server (DNS rebinding).
func (s *Server) HTTPHandler(token string, allowOrigin string) http.Handler {
	h := &httpServer{srv: s, token: token, allowOrigin: allowOrigin, sessions: map[string]*sseSession{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", h.handleMCP)
	mux.HandleFunc("/sse", h.handleSSE)
	mux.HandleFunc("/message", h.handleMessage)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, "ok")
	})
	return h.guard(mux)
}

type httpServer struct {
	srv         *Server
	token       string
	allowOrigin string

	mu       sync.Mutex
	sessions map[string]*sseSession
}

type sseSession struct {
	out  chan []byte
	done chan struct{}
}

// guard applies the Origin and token checks to every route except
// /healthz, which carries no data.
func (h *httpServer) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !h.originAllowed(origin) {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		if h.token != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") ||
				subtle.ConstantTimeCompare([]byte(got), []byte(h.token)) != 1 {
				w.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(w, "missing or wrong bearer token", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (h *httpServer) originAllowed(origin string) bool {
	if h.allowOrigin != "" && origin == h.allowOrigin {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return IsLoopbackHost(u.Hostname())
}

// IsLoopbackHost reports whether host is localhost or a loopback IP.
func IsLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return nil, false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxHTTPBody))
	if err != nil {
		http.Error(w, "request body too large or unreadable", http.StatusRequestEntityTooLarge)
		return nil, false
	}
	return body, true
}

func (h *httpServer) handleMCP(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	resp := h.srv.handleLine(r.Context(), body)
	if resp == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *httpServer) handleSSE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "use GET", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is not supported", http.StatusInternalServerError)
		return
	}
	id := newSessionID()
	sess := &sseSession{out: make(chan []byte, 16), done: make(chan struct{})}
	h.mu.Lock()
	h.sessions[id] = sess
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.sessions, id)
		h.mu.Unlock()
		close(sess.done)
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	fmt.Fprintf(w, "event: endpoint\ndata: /message?sessionId=%s\n\n", id)
	flusher.Flush()

	keepAlive := time.NewTicker(25 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-sess.out:
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", msg)
			flusher.Flush()
		case <-keepAlive.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			flusher.Flush()
		}
	}
}

func (h *httpServer) handleMessage(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("sessionId")
	h.mu.Lock()
	sess := h.sessions[id]
	h.mu.Unlock()
	if sess == nil {
		http.Error(w, "unknown or closed session", http.StatusNotFound)
		return
	}
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	w.WriteHeader(http.StatusAccepted)
	// The tool may run for a long time, so the answer goes out on the
	// stream and not in this response. The request context ends when
	// this handler returns, so the work uses its own context that ends
	// when the stream closes.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		defer cancel()
		go func() {
			select {
			case <-sess.done:
				cancel()
			case <-ctx.Done():
			}
		}()
		resp := h.srv.handleLine(ctx, body)
		if resp == nil {
			return
		}
		data, err := json.Marshal(resp)
		if err != nil {
			return
		}
		select {
		case sess.out <- data:
		case <-sess.done:
		}
	}()
}

func newSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
