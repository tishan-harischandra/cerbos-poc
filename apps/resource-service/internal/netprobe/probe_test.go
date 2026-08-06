package netprobe_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/resource-service/internal/netprobe"
)

func TestTCPProbeSucceedsWhenTheAddressAcceptsConnections(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := netprobe.NewTCPProbe(listener.Addr().String())(ctx); err != nil {
		t.Fatalf("probe against an open port returned %v, want nil", err)
	}
}

func TestTCPProbeFailsWhenTheAddressRefusesConnections(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	target := listener.Addr().String()
	_ = listener.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := netprobe.NewTCPProbe(target)(ctx); err == nil {
		t.Fatal("probe against a closed port returned nil, want an error")
	}
}

func TestHTTPProbeSucceedsOnA2xxResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := netprobe.NewHTTPProbe(server.URL)(ctx); err != nil {
		t.Fatalf("probe against a 200 response returned %v, want nil", err)
	}
}

func TestHTTPProbeFailsOnANon2xxResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := netprobe.NewHTTPProbe(server.URL)(ctx); err == nil {
		t.Fatal("probe against a 503 response returned nil, want an error")
	}
}

func TestHTTPProbeFailsWhenTheServerIsUnreachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := netprobe.NewHTTPProbe("http://127.0.0.1:1")(ctx); err == nil {
		t.Fatal("probe against an unreachable server returned nil, want an error")
	}
}
