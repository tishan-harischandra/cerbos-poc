package policyrelease

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// GiteaConfig configures a GiteaClient. Gitea never initiates an inbound
// connection into the application environment (§13.1); the controller is
// always the one dialling out to BaseURL.
type GiteaConfig struct {
	// BaseURL is the Gitea instance's base URL, e.g. "http://gitea:3000".
	BaseURL string
	// Repo is the "owner/name" repository holding root policies, schemas,
	// tests and the authorization catalog (§13.1).
	Repo string
	// Token is a Gitea access token used for authenticated requests. Empty
	// means unauthenticated, which the seeded compose repository allows.
	Token string
	// HTTPClient is the transport to use. A nil value selects
	// http.DefaultClient.
	HTTPClient *http.Client
}

// GiteaClient polls one Gitea repository for root policy release tags and
// fetches the exact commit each selected tag points at.
type GiteaClient struct {
	cfg GiteaConfig
}

// NewGiteaClient builds a GiteaClient from cfg.
func NewGiteaClient(cfg GiteaConfig) *GiteaClient {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	return &GiteaClient{cfg: cfg}
}

type giteaTag struct {
	Name      string `json:"name"`
	Protected bool   `json:"protected"`
	Commit    struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// ListTags returns every tag on the configured repository.
func (c *GiteaClient) ListTags(ctx context.Context) ([]Tag, error) {
	url := fmt.Sprintf("%s/api/v1/repos/%s/tags", strings.TrimRight(c.cfg.BaseURL, "/"), c.cfg.Repo)
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("policyrelease: listing tags: %w", err)
	}

	var raw []giteaTag
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("policyrelease: parsing tag list: %w", err)
	}

	tags := make([]Tag, 0, len(raw))
	for _, t := range raw {
		tags = append(tags, Tag{
			Name:      t.Name,
			Commit:    t.Commit.SHA,
			Protected: t.Protected,
		})
	}
	return tags, nil
}

// FetchArchive fetches the tarball of the exact commit ref. Passing a Git
// tag name here instead of Tag.Commit would resolve whatever the tag
// currently points at rather than the commit that was validated (§13.1).
func (c *GiteaClient) FetchArchive(ctx context.Context, commit string) ([]byte, error) {
	url := fmt.Sprintf("%s/api/v1/repos/%s/archive/%s.tar.gz", strings.TrimRight(c.cfg.BaseURL, "/"), c.cfg.Repo, commit)
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("policyrelease: fetching archive for commit %s: %w", commit, err)
	}
	return body, nil
}

func (c *GiteaClient) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "token "+c.cfg.Token)
	}

	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gitea returned %d: %s", resp.StatusCode, body)
	}
	return body, nil
}
