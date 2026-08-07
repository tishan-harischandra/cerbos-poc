package policyrelease_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/policyrelease"
)

func TestGiteaClient_ListTags_ParsesProtectionAndCommit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/authz/root-policy/tags" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{"name": "root-v1.4.0", "protected": true, "commit": {"sha": "bbb"}},
			{"name": "root-v1.3.0", "protected": true, "commit": {"sha": "aaa"}}
		]`))
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
	if len(tags) != 2 {
		t.Fatalf("len(tags) = %d, want 2", len(tags))
	}
	if tags[0].Name != "root-v1.4.0" || tags[0].Commit != "bbb" || !tags[0].Protected {
		t.Fatalf("tags[0] = %+v", tags[0])
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
