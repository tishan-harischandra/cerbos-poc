// Package adsclient is the Administration Service's transport to the
// Assignment Data Service's simulation endpoints (issue #19).
//
// The simulator's whole point is answering "what would this decision be"
// through the real runtime path, so this package never decides anything
// itself: it only carries a request to the ADS's real /internal/authz/simulate
// and /internal/capabilities/simulate endpoints and returns what they
// answered, forwarding the calling administrator's own bearer token so
// the ADS's own authentication middleware verifies it exactly as it would
// for any other caller (§16.1).
package adsclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Client calls the ADS's simulation endpoints over HTTP.
type Client struct {
	baseURL string
	http    *http.Client
}

// New prepares a Client. baseURL is the ADS's base address, e.g.
// "http://ads:8080".
func New(baseURL string) *Client {
	return &Client{baseURL: baseURL, http: &http.Client{}}
}

// SimulateAccessRequest names the target principal and one resource-action
// explicitly, rather than deriving them from the caller's own token - the
// whole point of a simulator is evaluating as someone else.
type SimulateAccessRequest struct {
	TenantID    string         `json:"tenantId"`
	HospitalID  string         `json:"hospitalId"`
	PrincipalID string         `json:"principalId"`
	IdPRoles    []string       `json:"idpRoles"`
	Resource    SimulateTarget `json:"resource"`
	Action      string         `json:"action"`
}

// SimulateTarget is the resource being asked about, with sample
// attributes the administrator supplies directly (§9.4).
type SimulateTarget struct {
	Kind       string         `json:"kind"`
	ID         string         `json:"id"`
	Attributes map[string]any `json:"attributes"`
}

// SimulateAccessResponse is the ADS's answer for one resource-action.
type SimulateAccessResponse struct {
	CerbosCallID       string `json:"cerbosCallId"`
	PermissionRevision int64  `json:"permissionRevision"`
	Allowed            bool   `json:"allowed"`
	Source             string `json:"source"`
}

// SimulateAccess asks the ADS's real decision path what it would answer
// for the named principal, resource and action.
func (c *Client) SimulateAccess(ctx context.Context, token string, req SimulateAccessRequest) (SimulateAccessResponse, error) {
	var resp SimulateAccessResponse
	if err := c.post(ctx, token, "/internal/authz/simulate", req, &resp); err != nil {
		return SimulateAccessResponse{}, err
	}
	return resp, nil
}

// SimulateCapabilitiesRequest names the target principal and the
// composite capabilities to evaluate, with sample resource attributes
// supplied directly per targetRef rather than resolved from a real
// resource (§9.4).
type SimulateCapabilitiesRequest struct {
	Module           string                    `json:"module"`
	CapabilityKeys   []string                  `json:"capabilityKeys"`
	TenantID         string                    `json:"tenantId"`
	HospitalID       string                    `json:"hospitalId"`
	PrincipalID      string                    `json:"principalId"`
	IdPRoles         []string                  `json:"idpRoles"`
	SampleAttributes map[string]map[string]any `json:"sampleAttributes"`
}

// LeafDecision is one permission leaf's decision in the full requirement
// tree the capability simulator exposes (§12.4).
type LeafDecision struct {
	Resource string `json:"resource"`
	Action   string `json:"action"`
	Target   string `json:"target"`
	Allowed  bool   `json:"allowed"`
	Reason   string `json:"reason"`
}

// SimulateCapabilitiesResponse is the ADS's capability simulation answer:
// the same snapshot shape the runtime endpoint serves, plus the complete
// per-leaf requirement tree.
type SimulateCapabilitiesResponse struct {
	AuthorizationRevision     int64                       `json:"authorizationRevision"`
	RootPolicyRevision        string                      `json:"rootPolicyRevision"`
	CapabilityCatalogRevision string                      `json:"capabilityCatalogRevision"`
	Capabilities              map[string]CapabilityResult `json:"capabilities"`
	RequirementTree           []LeafDecision              `json:"requirementTree"`
}

// CapabilityResult is one capability's simulated decision.
type CapabilityResult struct {
	Allowed            bool                `json:"allowed"`
	Reason             string              `json:"reason,omitempty"`
	FailedRequirements []FailedRequirement `json:"failedRequirements,omitempty"`
}

// FailedRequirement is one denied leaf that explains a capability's
// simulated denial.
type FailedRequirement struct {
	Resource string `json:"resource"`
	Action   string `json:"action"`
	Target   string `json:"target"`
	Reason   string `json:"reason"`
}

// SimulateCapabilities asks the ADS's real capability evaluation path
// what it would answer for the named principal and sample context.
func (c *Client) SimulateCapabilities(ctx context.Context, token string, req SimulateCapabilitiesRequest) (SimulateCapabilitiesResponse, error) {
	var resp SimulateCapabilitiesResponse
	if err := c.post(ctx, token, "/internal/capabilities/simulate", req, &resp); err != nil {
		return SimulateCapabilitiesResponse{}, err
	}
	return resp, nil
}

func (c *Client) post(ctx context.Context, token, path string, requestBody, responseBody any) error {
	body, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("adsclient: encoding the request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("adsclient: building the request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("adsclient: calling the ADS: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("adsclient: reading the ADS response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("adsclient: the ADS returned %d: %s", resp.StatusCode, respBody)
	}

	if err := json.Unmarshal(respBody, responseBody); err != nil {
		return fmt.Errorf("adsclient: decoding the ADS response: %w", err)
	}
	return nil
}
