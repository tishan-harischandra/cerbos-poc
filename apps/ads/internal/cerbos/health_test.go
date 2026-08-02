package cerbos_test

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/cerbos"
)

func TestProbeSucceedsAgainstAServingCerbosGRPCEndpoint(t *testing.T) {
	target := startHealthServer(t, grpc_health_v1.HealthCheckResponse_SERVING)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := cerbos.NewGRPCProbe(target)(ctx); err != nil {
		t.Fatalf("probe against a serving endpoint returned %v, want nil", err)
	}
}

func TestProbeFailsWhenNothingIsListeningOnTheGRPCPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	target := listener.Addr().String()
	_ = listener.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cerbos.NewGRPCProbe(target)(ctx); err == nil {
		t.Fatal("probe against a closed port returned nil, want an error")
	}
}

func startHealthServer(t *testing.T, status grpc_health_v1.HealthCheckResponse_ServingStatus) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}

	srv := grpc.NewServer()
	healthSrv := health.NewServer()
	healthSrv.SetServingStatus("", status)
	grpc_health_v1.RegisterHealthServer(srv, healthSrv)

	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(srv.Stop)

	return listener.Addr().String()
}
