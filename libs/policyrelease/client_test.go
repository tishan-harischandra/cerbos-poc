package policyrelease_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/policyrelease"
)

// The fixtures below are the real shapes Gitea 1.23 returns: /tags never
// carries a "protected" field (confirmed against a live 1.22/1.23
// instance - Gitea's own Tag struct has no such field, in any version),
// and protection status lives only in the separate /tag_protections
// resource, matched against tag names by name_pattern.
func TestGiteaClient_ListTags_MatchesProtectionPatternsAgainstTagNames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/repos/authz/root-policy/tags":
			w.Write([]byte(`[
				{"name": "root-v1.4.0", "commit": {"sha": "bbb"}},
				{"name": "root-v1.3.0", "commit": {"sha": "aaa"}},
				{"name": "scratch-tag", "commit": {"sha": "ccc"}}
			]`))
		case "/api/v1/repos/authz/root-policy/tag_protections":
			w.Write([]byte(`[{"id": 1, "name_pattern": "root-v*"}]`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	client := policyrelease.NewGiteaClient(policyrelease.GiteaConfig{
		BaseURL: server.URL,
		Repo:    "authz/root-policy",
	})

	tags, err := client.ListTags(context.Background())
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(tags) != 3 {
		t.Fatalf("len(tags) = %d, want 3", len(tags))
	}
	if tags[0].Name != "root-v1.4.0" || tags[0].Commit != "bbb" || !tags[0].Protected {
		t.Fatalf("tags[0] = %+v, want protected root-v1.4.0", tags[0])
	}
	if tags[2].Name != "scratch-tag" || tags[2].Protected {
		t.Fatalf("tags[2] = %+v, want unprotected scratch-tag", tags[2])
	}
}

func TestGiteaClient_FetchArchive_FetchesExactCommitTarball(t *testing.T) {
	const body = "fake-tarball-bytes"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/authz/root-policy/archive/bbb.tar.gz" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		w.Write([]byte(body))
	}))
	defer server.Close()

	client := policyrelease.NewGiteaClient(policyrelease.GiteaConfig{
		BaseURL: server.URL,
		Repo:    "authz/root-policy",
	})

	data, err := client.FetchArchive(context.Background(), "bbb")
	if err != nil {
		t.Fatalf("FetchArchive: %v", err)
	}
	if string(data) != body {
		t.Fatalf("data = %q, want %q", data, body)
	}
}
