// Package cerbos holds the ADS side of the Cerbos PDP connection.
//
// This package carries no permission precedence logic. Precedence is decided
// exclusively inside Cerbos policy via the sys:permission-evaluator role.
package cerbos

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// NewGRPCProbe returns a readiness probe that checks the Cerbos PDP answers
// the standard gRPC health service at target.
func NewGRPCProbe(target string) func(context.Context) error {
	return func(ctx context.Context) error {
		conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("dialling cerbos at %s: %w", target, err)
		}
		defer func() { _ = conn.Close() }()

		resp, err := grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
		if err != nil {
			return fmt.Errorf("cerbos health check at %s: %w", target, err)
		}
		if resp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
			return fmt.Errorf("cerbos at %s reports %s", target, resp.GetStatus())
		}
		return nil
	}
}
