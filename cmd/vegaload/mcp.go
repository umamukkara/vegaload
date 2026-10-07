// mcp.go implements `vegaload mcp serve`, FR-MCP-02's stdio-transport
// MCP server. It is intentionally thin: internal/mcp holds the actual
// JSON-RPC engine and tool implementations, every one of which shells
// out back to this same compiled binary (see internal/mcp.RunCLI) —
// this file's only real job is resolving that binary's own path once
// and handing it to internal/mcp.NewTools, then running the server on
// stdin/stdout until the client disconnects.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/vegaload/vegaload/internal/mcp"
)

// cmdMCP dispatches vegaload's "mcp" command group: "serve" runs
// FR-MCP-02's stdio server, "eval" runs FR-MCP-04's versioned tool-
// calling eval suite against this same binary.
func cmdMCP(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: vegaload mcp <serve|eval>")
		return 2
	}
	switch args[0] {
	case "serve":
		return cmdMCPServe(args[1:])
	case "eval":
		return cmdMCPEval(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "vegaload mcp: unknown subcommand %q\n", args[0])
		fmt.Fprintln(os.Stderr, "Usage: vegaload mcp <serve|eval>")
		return 2
	}
}

func cmdMCPServe(args []string) int {
	fs := flag.NewFlagSet("mcp serve", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: vegaload mcp serve [-http addr [-token-env NAME] [-allow-origin URL]]")
		fmt.Fprintln(fs.Output(), "Runs an MCP server over stdio, exposing VegaLoad's CLI commands as tools")
		fmt.Fprintln(fs.Output(), "(create_scenario, run_test, get_results, suggest_thresholds, diagnose_failure, compare_reports, generate_from_spec, validate_scenario)")
		fmt.Fprintln(fs.Output(), "for an MCP-aware agent (Claude Code, Cursor, etc.) to call directly.")
		fmt.Fprintln(fs.Output(), "")
		fmt.Fprintln(fs.Output(), "By default it talks over stdio. With -http it listens on a TCP address instead:")
		fmt.Fprintln(fs.Output(), "  POST /mcp      one JSON-RPC message in, the JSON-RPC answer out")
		fmt.Fprintln(fs.Output(), "  GET  /sse      Server-Sent Events stream (MCP 2024-11-05), with POST /message")
		fmt.Fprintln(fs.Output(), "A non-loopback address needs a token (-token-env), sent as a Bearer header.")
		fs.PrintDefaults()
	}
	httpAddr := fs.String("http", "", "listen for MCP over HTTP/SSE on this address, for example 127.0.0.1:8765 (default: use stdio)")
	tokenEnv := fs.String("token-env", "", "name of an environment variable that holds the bearer token for -http")
	allowOrigin := fs.String("allow-origin", "", "extra browser Origin allowed to call the -http server (loopback origins are always allowed)")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "vegaload mcp serve: locating the vegaload binary: %v\n", err)
		return 1
	}

	server := mcp.NewServer()
	for _, t := range mcp.NewTools(exePath) {
		server.Register(t)
	}

	if *httpAddr != "" {
		return serveMCPHTTP(server, *httpAddr, *tokenEnv, *allowOrigin)
	}
	if *tokenEnv != "" || *allowOrigin != "" {
		fmt.Fprintln(os.Stderr, "vegaload mcp serve: -token-env and -allow-origin only apply with -http")
		return 2
	}

	if err := server.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "vegaload mcp serve: %v\n", err)
		return 1
	}
	return 0
}

// serveMCPHTTP runs the optional HTTP/SSE transport. It refuses to listen
// on a non-loopback address without a token, so the tools (which run
// load tests) are never open to the network by accident.
func serveMCPHTTP(server *mcp.Server, addr, tokenEnv, allowOrigin string) int {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vegaload mcp serve: -http %q: %v (use host:port, for example 127.0.0.1:8765)\n", addr, err)
		return 2
	}
	token := ""
	if tokenEnv != "" {
		token = cleanToken(os.Getenv(tokenEnv))
		if token == "" {
			fmt.Fprintf(os.Stderr, "vegaload mcp serve: environment variable %s is empty or not set\n", tokenEnv)
			return 2
		}
	}
	if token == "" && !mcp.IsLoopbackHost(host) {
		fmt.Fprintf(os.Stderr, "vegaload mcp serve: %s is not a loopback address, so a token is required (set one and pass -token-env NAME)\n", addr)
		return 2
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vegaload mcp serve: %v\n", err)
		return 1
	}
	// Every request context comes from ctx, so SIGINT or SIGTERM cancels the
	// requests that are running. That stops the `vegaload run` a tool call
	// started: the process must not exit and leave a load test running.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	httpSrv := &http.Server{
		Handler:           server.HTTPHandler(token, allowOrigin),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	fmt.Fprintf(os.Stderr, "vegaload mcp serve: listening on http://%s (POST /mcp, GET /sse)\n", ln.Addr())
	if token == "" {
		fmt.Fprintln(os.Stderr, "vegaload mcp serve: no token set; only local processes can reach this address")
	}

	errc := make(chan error, 1)
	go func() { errc <- httpSrv.Serve(ln) }()
	select {
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "vegaload mcp serve: %v\n", err)
			return 1
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			_ = httpSrv.Close() // something is still running: stop waiting for it
		}
	}
	return 0
}

// cleanToken trims spaces and line breaks from a token. A token read from a
// Kubernetes secret or a file often ends with a newline, which an HTTP header
// cannot carry: a client could then never send a matching value.
func cleanToken(s string) string {
	return strings.TrimSpace(s)
}
