package adsclient_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/resource-service/internal/adsclient"
)

type wireRequest struct {
	Resources []struct {
		Kind    string   `json:"kind"`
		ID      string   `json:"id"`
		Actions []string `json:"actions"`
	} `json:"resources"`
}

// The chunking requirement (issue #9): a Check call naming more resources
// than the ADS can take in one HTTP request must still succeed, split into
// several bounded requests, rather than fail outright.
func TestCheckChunksRequestsPastTheLimit(t *testing.T) {
	var calls int32
	var maxBatchSeen int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)

		var req wireRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding the request the client sent: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if int32(len(req.Resources)) > atomic.LoadInt32(&maxBatchSeen) {
			atomic.StoreInt32(&maxBatchSeen, int32(len(req.Resources)))
		}
		if len(req.Resources) > adsclient.MaxResourcesPerRequest {
			t.Errorf("one HTTP request carried %d resources, want at most %d",
				len(req.Resources), adsclient.MaxResourcesPerRequest)
		}

		resources := make([]map[string]any, 0, len(req.Resources))
		for _, res := range req.Resources {
			actions := map[string]any{}
			for _, action := range res.Actions {
				actions[action] = map[string]any{"allowed": true, "source": "ROLE"}
			}
			resources = append(resources, map[string]any{
				"kind": res.Kind, "id": res.ID, "permissionRevision": 1, "actions": actions,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"cerbosCallId": "call-1", "resources": resources})
	}))
	defer server.Close()

	client := adsclient.New(server.URL)

	count := adsclient.MaxResourcesPerRequest + 20
	checks := make([]adsclient.ResourceCheck, 0, count)
	for i := 0; i < count; i++ {
		checks = append(checks, adsclient.ResourceCheck{
			Kind: "condition", ID: "condition-" + string(rune('a'+i%26)) + itoa(i),
			Actions: []string{"read"},
		})
	}

	decisions, err := client.Check(context.Background(), "test-token", checks)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(decisions) != count {
		t.Fatalf("decisions = %d, want %d", len(decisions), count)
	}
	if calls < 2 {
		t.Errorf("the ADS received %d HTTP requests for %d resources, want at least 2 chunked requests",
			calls, count)
	}
	if maxBatchSeen > int32(adsclient.MaxResourcesPerRequest) {
		t.Errorf("the largest chunk sent was %d, want at most %d", maxBatchSeen, adsclient.MaxResourcesPerRequest)
	}
}

func TestCheckMakesOneRequestForASmallBatch(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"cerbosCallId": "call-1",
			"resources": []map[string]any{
				{"kind": "condition", "id": "condition-1", "permissionRevision": 1,
					"actions": map[string]any{"read": map[string]any{"allowed": true, "source": "ROLE"}}},
			},
		})
	}))
	defer server.Close()

	client := adsclient.New(server.URL)
	_, err := client.Check(context.Background(), "test-token", []adsclient.ResourceCheck{
		{Kind: "condition", ID: "condition-1", Actions: []string{"read"}},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
