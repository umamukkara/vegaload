// Package grpctest runs a small in-process gRPC server for tests in other
// packages. It serves the standard health service, which has real message
// types, and it can offer server reflection or not.
package grpctest

import (
	"context"
	"net"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	reflectionv1alpha "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
)

// Server is a running test server.
type Server struct {
	// Addr is host:port.
	Addr string

	mu      sync.Mutex
	headers []metadata.MD
}

// Start serves grpc.health.v1.Health on localhost. The service "up" is
// SERVING and "down" is NOT_SERVING. The empty name is SERVING. Any other
// name answers NotFound. With reflection true the server also offers server
// reflection. The server stops when the test ends.
func Start(t *testing.T, reflect bool) *Server {
	t.Helper()
	if reflect {
		return start(t, "both")
	}
	return start(t, "none")
}

// StartAlphaOnly is Start with reflection, but only the older v1alpha
// protocol, as some servers still offer.
func StartAlphaOnly(t *testing.T) *Server {
	t.Helper()
	return start(t, "alpha")
}

func start(t *testing.T, mode string) *Server {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("grpctest: %v", err)
	}
	s := &Server{Addr: lis.Addr().String()}
	srv := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			s.mu.Lock()
			s.headers = append(s.headers, md)
			s.mu.Unlock()
		}
		return h(ctx, req)
	}))
	hs := health.NewServer()
	hs.SetServingStatus("up", healthpb.HealthCheckResponse_SERVING)
	hs.SetServingStatus("down", healthpb.HealthCheckResponse_NOT_SERVING)
	healthpb.RegisterHealthServer(srv, hs)
	switch mode {
	case "both":
		reflection.Register(srv)
	case "alpha":
		reflectionv1alpha.RegisterServerReflectionServer(srv, reflection.NewServer(reflection.ServerOptions{Services: srv}))
	}
	go srv.Serve(lis) //nolint:errcheck // Stop below ends it
	t.Cleanup(srv.Stop)
	return s
}

// LastHeader returns the last value the server saw for a metadata key, and
// whether it saw one.
func (s *Server) LastHeader(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.headers) - 1; i >= 0; i-- {
		if v := s.headers[i].Get(key); len(v) > 0 {
			return v[0], true
		}
	}
	return "", false
}
