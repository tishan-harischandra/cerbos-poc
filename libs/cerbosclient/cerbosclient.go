// Package cerbosclient is the ADS' transport to the Cerbos PDP.
//
// It carries a request to the PDP and maps the response back onto the leaves
// that were asked about. It reads decisions; it never makes them. Nothing here
// inspects permissionContext or orders grants against revokes, because that
// ordering is policy and policy lives in Cerbos (§6.3, ADR-003).
package cerbosclient

import (
	"context"
	"errors"
	"fmt"

	"github.com/cerbos/cerbos-sdk-go/cerbos"
	effectv1 "github.com/cerbos/cerbos/api/genpb/cerbos/effect/v1"
	requestv1 "github.com/cerbos/cerbos/api/genpb/cerbos/request/v1"
	svcv1 "github.com/cerbos/cerbos/api/genpb/cerbos/svc/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// EvaluationRole is the single synthetic role every policy rule binds to.
//
// The ADS injects it after token verification; a token that presents it (or any
// other `sys:`-prefixed role) is rejected upstream. Sending exactly one role is
// what makes precedence deterministic: Cerbos resolves deny over allow within a
// role, but an allow from a second role would defeat a deny from the first
// (§6.4, ADR-003).
const EvaluationRole = "sys:permission-evaluator"

// DefaultPolicyVersion matches the version in the committed resource policies.
const DefaultPolicyVersion = "v1"

// Config configures the connection to the PDP.
type Config struct {
	// Address is the PDP's gRPC host:port.
	Address string
	// PlaintextTLS disables transport security. The PDP is reachable only on
	// the internal network and is never published to the host, so the compose
	// topology runs without TLS.
	PlaintextTLS bool
	// PolicyVersion is the resource policy version to evaluate against.
	// Empty means DefaultPolicyVersion.
	PolicyVersion string
}

// Principal is the identity a decision is requested for. Roles are not part of
// this type: the client always sends EvaluationRole and nothing else.
type Principal struct {
	ID   string
	Attr map[string]any
}

// ResourceRef identifies one resource instance. It is comparable so that a Leaf
// can be used as a map key.
type ResourceRef struct {
	Kind string
	ID   string
}

// ResourceCheck is one resource, its server-loaded attributes, and the actions
// being asked about.
type ResourceCheck struct {
	Resource ResourceRef
	Attr     map[string]any
	Actions  []string
}

// Request is one batched authorization question.
type Request struct {
	Principal Principal
	Resources []ResourceCheck
}

// Leaf is a single resource-action pair, the unit a decision is returned for.
type Leaf struct {
	Resource ResourceRef
	Action   string
}

// Decision is the PDP's answer for one leaf. Only an explicit EFFECT_ALLOW
// counts as allowed (§11.3).
type Decision struct {
	Allowed bool
}

// Result is the outcome of one Check.
type Result struct {
	Decisions map[Leaf]Decision
	// CallID is the PDP's cerbosCallId, logged alongside the application
	// correlation ID so a decision can be traced back into the PDP's audit
	// log (§11.3).
	CallID string
}

// Client holds one long-lived gRPC channel to the PDP.
//
// A Client is safe for concurrent use and is created once per process. Dialling
// per request would put a TCP handshake in front of every authorization
// decision.
//
// The channel is owned here rather than by the Cerbos SDK's own constructor,
// which offers no way to close it. The SDK is still used to build and validate
// requests; only the transport is ours.
type Client struct {
	conn          *grpc.ClientConn
	pdp           svcv1.CerbosServiceClient
	policyVersion string
}

// New prepares a Client ready for concurrent use. The channel connects lazily
// on the first Check and is then reused for the life of the Client.
func New(cfg Config) (*Client, error) {
	if cfg.Address == "" {
		return nil, errors.New("cerbosclient: no PDP address configured")
	}

	transport := credentials.NewTLS(nil)
	if cfg.PlaintextTLS {
		transport = insecure.NewCredentials()
	}

	conn, err := grpc.NewClient(cfg.Address, grpc.WithTransportCredentials(transport))
	if err != nil {
		return nil, fmt.Errorf("cerbosclient: preparing a channel to %s: %w", cfg.Address, err)
	}

	version := cfg.PolicyVersion
	if version == "" {
		version = DefaultPolicyVersion
	}

	return &Client{
		conn:          conn,
		pdp:           svcv1.NewCerbosServiceClient(conn),
		policyVersion: version,
	}, nil
}

// Close releases the channel.
func (c *Client) Close() error {
	return c.conn.Close()
}

// Check asks the PDP about every action on every resource in one call and
// returns a decision per leaf.
func (c *Client) Check(ctx context.Context, req Request) (Result, error) {
	if len(req.Resources) == 0 {
		return Result{}, errors.New("cerbosclient: no resources to check")
	}

	principal := cerbos.NewPrincipal(req.Principal.ID, EvaluationRole).
		WithAttributes(req.Principal.Attr)
	// Err() reports attribute values the wire format cannot carry. The SDK
	// records them here and otherwise drops them silently, which is how a
	// missing permissionContext once turned into a PDP that denied everything.
	if err := principal.Err(); err != nil {
		return Result{}, fmt.Errorf("cerbosclient: encoding principal attributes: %w", err)
	}
	if err := principal.Validate(); err != nil {
		return Result{}, fmt.Errorf("cerbosclient: invalid principal: %w", err)
	}

	batch := cerbos.NewResourceBatch()
	for _, check := range req.Resources {
		if len(check.Actions) == 0 {
			return Result{}, fmt.Errorf("cerbosclient: no actions for resource %s/%s",
				check.Resource.Kind, check.Resource.ID)
		}
		resource := cerbos.NewResource(check.Resource.Kind, check.Resource.ID).
			WithPolicyVersion(c.policyVersion).
			WithAttributes(check.Attr)
		if err := resource.Err(); err != nil {
			return Result{}, fmt.Errorf("cerbosclient: encoding attributes of %s/%s: %w",
				check.Resource.Kind, check.Resource.ID, err)
		}
		batch = batch.Add(resource, check.Actions...)
	}
	if err := batch.Validate(); err != nil {
		return Result{}, fmt.Errorf("cerbosclient: invalid resource batch: %w", err)
	}

	response, err := c.pdp.CheckResources(ctx, &requestv1.CheckResourcesRequest{
		Principal: principal.Proto(),
		Resources: batch.Batch,
	})
	if err != nil {
		return Result{}, fmt.Errorf("cerbosclient: checking resources: %w", err)
	}

	decisions := make(map[Leaf]Decision)
	for _, entry := range response.GetResults() {
		ref := ResourceRef{
			Kind: entry.GetResource().GetKind(),
			ID:   entry.GetResource().GetId(),
		}
		for action, effect := range entry.GetActions() {
			decisions[Leaf{Resource: ref, Action: action}] = Decision{
				Allowed: effect == effectv1.Effect_EFFECT_ALLOW,
			}
		}
	}

	return Result{Decisions: decisions, CallID: response.GetCerbosCallId()}, nil
}
