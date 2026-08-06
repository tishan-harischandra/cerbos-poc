// Package netprobe provides readiness probes, mirroring
// apps/ads/internal/netprobe.
package netprobe

import (
	"context"
	"fmt"
	"net"
	"net/http"
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

// NewHTTPProbe returns a readiness probe that succeeds when a GET to url
// returns 2xx.
func NewHTTPProbe(url string) func(context.Context) error {
	client := &http.Client{}
	return func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("building a probe request to %s: %w", url, err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("probing %s: %w", url, err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("%s returned %d", url, resp.StatusCode)
		}
		return nil
	}
}
