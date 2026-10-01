// Package protocol defines the shared interface every wire protocol
// driver (HTTP/1.1, HTTP/2, gRPC, WebSocket) implements, plus the request
// and result types they exchange with the engine.
//
// This package, and the driver packages beneath it, are the only places
// in VegaLoad that know what HTTP, gRPC, or a WebSocket handshake actually
// look like on the wire. internal/engine calls a driver's Do method
// through this interface and otherwise knows nothing protocol-specific —
// see AGENTS.md's module boundaries.
package protocol

import (
	"context"
)

// Target describes what a driver connects to and how, in terms generic
// enough to cover any of the four wire protocols VegaLoad supports. Each
// driver interprets the fields relevant to it and ignores the rest — for
// example Method is meaningless to the WebSocket and gRPC drivers.
type Target struct {
	// URL is the target address: an http(s):// URL for the HTTP
	// drivers, a ws(s):// URL for WebSocket, or a host:port for gRPC.
	URL string
	// Method is the HTTP verb (GET, POST, ...) for the HTTP drivers,
	// defaulting to GET if empty; for the gRPC driver it is instead the
	// full RPC method, e.g. "/package.Service/Method", which has no
	// default and must be set. Ignored by WebSocket.
	Method string
	// Headers are sent as request headers (HTTP) or connection
	// metadata (gRPC), depending on the driver.
	Headers map[string]string
	// Body is the request payload, if any.
	Body []byte
	// InsecureSkipVerify disables TLS certificate verification for
	// drivers that speak TLS. It exists for testing against targets
	// with self-signed or internal-CA certificates; it is false (verify
	// normally) by default and a scenario author has to opt in.
	InsecureSkipVerify bool
}

// Result is what a driver reports after one Do call — enough detail for
// internal/report (Phase 1) to build percentiles and pass/fail counts
// without needing to know which protocol produced it.
type Result struct {
	// Success is whether this iteration counts as a pass. A driver
	// decides this itself: the HTTP drivers treat 2xx/3xx as success
	// unless configured otherwise.
	Success bool
	// StatusCode is protocol-specific (HTTP status, gRPC status code,
	// ...). Zero if not applicable.
	StatusCode int
	// BytesSent and BytesReceived are best-effort byte counts for the
	// iteration. Zero if a driver doesn't track them.
	BytesSent     int64
	BytesReceived int64
	// Err describes why Success is false. It is informational only —
	// unlike Do's returned error, a non-nil Err does not stop the run.
	Err error
}

// Protocol is implemented by each wire protocol driver. The engine package
// calls Do once per iteration and Close once the driver is no longer
// needed; it holds no protocol-specific knowledge of its own.
type Protocol interface {
	// Name identifies the driver, e.g. "http1", "grpc".
	Name() string

	// Do performs one iteration against the configured target. Return
	// a non-nil error only for a failure that should stop the whole
	// run, such as a misconfigured target — a failed individual
	// request is a Result with Success: false and Err set, not a
	// returned error.
	Do(ctx context.Context) (Result, error)

	// Close releases any resources the driver holds (connections,
	// clients). Called once, after every VU using this driver instance
	// has stopped.
	Close() error
}
