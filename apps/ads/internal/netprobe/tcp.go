// Package netprobe provides transport-level readiness probes.
package netprobe

import (
	"context"
	"fmt"
	"net"
)

// NewTCPProbe returns a readiness probe that succeeds when target accepts a
// TCP connection.
func NewTCPProbe(target string) func(context.Context) error {
	dialer := &net.Dialer{}
	return func(ctx context.Context) error {
		conn, err := dialer.DialContext(ctx, "tcp", target)
		if err != nil {
			return fmt.Errorf("dialling %s: %w", target, err)
		}
		return conn.Close()
	}
}
