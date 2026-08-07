package policyrelease

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
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

// giteaTag mirrors Gitea's own Tag struct (modules/structs/repo_tag.go).
// It carries no protection status: Gitea has never put that on the tag
// list response, in any version - confirmed against a live 1.22/1.23
// instance. Protection is a separate resource, fetched by ListTagProtections
// below and matched against tag names ourselves.
type giteaTag struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// giteaTagProtection mirrors one entry of Gitea's TagProtection API
// response (modules/structs/repo_tag.go), introduced in Gitea 1.23
// (go-gitea/gitea#31295). Earlier Gitea versions 404 on this endpoint.
type giteaTagProtection struct {
	NamePattern string `json:"name_pattern"`
}

// ListTags returns every tag on the configured repository, with Protected
// set for any tag whose name matches one of the repository's configured
// tag protection patterns.
func (c *GiteaClient) ListTags(ctx context.Context) ([]Tag, error) {
	tagsURL := fmt.Sprintf("%s/api/v1/repos/%s/tags", strings.TrimRight(c.cfg.BaseURL, "/"), c.cfg.Repo)
	body, err := c.get(ctx, tagsURL)
	if err != nil {
		return nil, fmt.Errorf("policyrelease: listing tags: %w", err)
	}
	var rawTags []giteaTag
	if err := json.Unmarshal(body, &rawTags); err != nil {
		return nil, fmt.Errorf("policyrelease: parsing tag list: %w", err)
	}

	protectionsURL := fmt.Sprintf("%s/api/v1/repos/%s/tag_protections", strings.TrimRight(c.cfg.BaseURL, "/"), c.cfg.Repo)
	body, err = c.get(ctx, protectionsURL)
	if err != nil {
		return nil, fmt.Errorf("policyrelease: listing tag protections: %w", err)
	}
	var rawProtections []giteaTagProtection
	if err := json.Unmarshal(body, &rawProtections); err != nil {
		return nil, fmt.Errorf("policyrelease: parsing tag protection list: %w", err)
	}

	tags := make([]Tag, 0, len(rawTags))
	for _, t := range rawTags {
		tags = append(tags, Tag{
			Name:      t.Name,
			Commit:    t.Commit.SHA,
			Protected: matchesAnyPattern(t.Name, rawProtections),
		})
	}
	return tags, nil
}

// matchesAnyPattern reports whether name matches any tag protection
// pattern. Gitea's own matching (models/git/protected_tag.go) treats a
// pattern wrapped in "/.../" as a regular expression and anything else as
// a glob; the glob case is the only one this controller's ROOT_POLICY_TAG_PREFIX
// convention needs.
func matchesAnyPattern(name string, protections []giteaTagProtection) bool {
	for _, p := range protections {
		if len(p.NamePattern) > 1 && strings.HasPrefix(p.NamePattern, "/") && strings.HasSuffix(p.NamePattern, "/") {
			re, err := regexp.Compile(p.NamePattern[1 : len(p.NamePattern)-1])
			if err == nil && re.MatchString(name) {
				return true
			}
			continue
		}
		if ok, err := path.Match(p.NamePattern, name); err == nil && ok {
			return true
		}
	}
	return false
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
