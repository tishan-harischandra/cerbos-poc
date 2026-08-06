// Package adsclient is the resource service's transport to the Assignment
// Data Service's decision endpoint.
//
// The resource service is a policy enforcement point (issue #9): it loads a
// resource's trusted attributes itself, but it does not resolve role grants
// or user overrides, and it does not call the Cerbos PDP directly. That work
// already exists in the ADS (issues #4/#5), which assembles permissionContext
// and asks Cerbos on the caller's behalf. Calling it here rather than
// duplicating its resolution logic is what keeps precedence resolution in
// exactly one place (§21).
package adsclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// MaxResourcesPerRequest bounds how many resources one call to the ADS may
// ask about. The ADS batches its own call to Cerbos, which itself has a
// request size limit; chunking here is what turns "a list page larger than
// that limit" into several bounded calls rather than one failing call
// (issue #9's chunking requirement).
const MaxResourcesPerRequest = 100

// ResourceCheck is one resource and the actions to decide for it. Attributes
// must be the service's own server-loaded values, never anything taken from
// a caller's request body (§16.1).
type ResourceCheck struct {
	Kind       string
	ID         string
	Attributes map[string]any
	Actions    []string
}

// Decision is the outcome for one action, in the Appendix A shape: an
// explicit allow, and the source that produced it.
type Decision struct {
	Allowed bool   `json:"allowed"`
	Source  string `json:"source"`
}

// ResourceDecision is every requested action's decision for one resource.
type ResourceDecision struct {
	Kind               string              `json:"kind"`
	ID                 string              `json:"id"`
	PermissionRevision int64               `json:"permissionRevision"`
	Actions            map[string]Decision `json:"actions"`
}

// Client calls the ADS's POST /internal/authz/check endpoint over HTTP.
type Client struct {
	baseURL string
	http    *http.Client
}

// New prepares a Client. baseURL is the ADS's base address, e.g.
// "http://ads:8080".
func New(baseURL string) *Client {
	return &Client{baseURL: baseURL, http: &http.Client{}}
}

// Check asks the ADS to decide every action on every resource, forwarding
// token as the caller's own bearer token: the ADS re-verifies it and derives
// the principal, tenant and hospital from it itself, exactly as it does for
// any other caller (§16.1). Chunking is automatic: a call naming more than
// MaxResourcesPerRequest resources is split into several ADS calls and the
// results are merged, so a large list page fails to chunk rather than fails
// outright.
func (c *Client) Check(ctx context.Context, token string, checks []ResourceCheck) ([]ResourceDecision, error) {
	if len(checks) == 0 {
		return nil, nil
	}

	var merged []ResourceDecision
	for start := 0; start < len(checks); start += MaxResourcesPerRequest {
		end := min(start+MaxResourcesPerRequest, len(checks))
		decisions, err := c.checkOnce(ctx, token, checks[start:end])
		if err != nil {
			return nil, err
		}
		merged = append(merged, decisions...)
	}
	return merged, nil
}

func (c *Client) checkOnce(ctx context.Context, token string, checks []ResourceCheck) ([]ResourceDecision, error) {
	requestBody := wireRequest{Resources: make([]wireRequestResource, 0, len(checks))}
	for _, check := range checks {
		requestBody.Resources = append(requestBody.Resources, wireRequestResource{
			Kind:       check.Kind,
			ID:         check.ID,
			Attributes: check.Attributes,
			Actions:    check.Actions,
		})
	}

	body, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("adsclient: encoding the check request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/internal/authz/check", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("adsclient: building the check request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("adsclient: calling the ADS: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("adsclient: reading the ADS response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("adsclient: the ADS returned %d: %s", resp.StatusCode, respBody)
	}

	var wire wireResponse
	if err := json.Unmarshal(respBody, &wire); err != nil {
		return nil, fmt.Errorf("adsclient: decoding the ADS response: %w", err)
	}

	decisions := make([]ResourceDecision, 0, len(wire.Resources))
	for _, r := range wire.Resources {
		decisions = append(decisions, ResourceDecision{
			Kind:               r.Kind,
			ID:                 r.ID,
			PermissionRevision: r.PermissionRevision,
			Actions:            r.Actions,
		})
	}
	return decisions, nil
}

type wireRequest struct {
	Resources []wireRequestResource `json:"resources"`
}

type wireRequestResource struct {
	Kind       string         `json:"kind"`
	ID         string         `json:"id"`
	Attributes map[string]any `json:"attributes"`
	Actions    []string       `json:"actions"`
}

type wireResponse struct {
	CerbosCallID string                 `json:"cerbosCallId"`
	Resources    []wireResponseResource `json:"resources"`
}

type wireResponseResource struct {
	Kind               string              `json:"kind"`
	ID                 string              `json:"id"`
	PermissionRevision int64               `json:"permissionRevision"`
	Actions            map[string]Decision `json:"actions"`
}
