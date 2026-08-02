package netprobe_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/netprobe"
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
