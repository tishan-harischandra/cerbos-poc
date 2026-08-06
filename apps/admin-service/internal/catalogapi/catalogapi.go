// Package catalogapi serves GET /admin/authz/resources (§9.4): the
// administration-facing resource/action catalog and the current root
// policy revision, the data the Admin Console's resource catalog and role
// matrix modules render (§9.1, §9.2).
package catalogapi

import (
	"encoding/json"
	"net/http"

	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
)

// Handler serves the resource catalog endpoint.
type Handler struct {
	// Resources is loaded once at startup from the same catalog directory
	// the write path validates permission leaves against (§12.2) - the
	// browser never gets a second, divergent copy of the catalog.
	Resources          []capabilitycatalog.ResourceEntry
	RootPolicyRevision string
}

type resourceView struct {
	ResourceKey string                          `json:"resourceKey"`
	DisplayName string                          `json:"displayName"`
	Domain      string                          `json:"domain"`
	Actions     []capabilitycatalog.ActionEntry `json:"actions"`
}

// Read handles GET /admin/authz/resources.
func (h *Handler) Read(w http.ResponseWriter, r *http.Request) {
	if _, ok := tokenauth.From(r.Context()); !ok {
		writeError(w, http.StatusUnauthorized, "no verified identity on this request")
		return
	}

	views := make([]resourceView, 0, len(h.Resources))
	for _, entry := range h.Resources {
		views = append(views, resourceView{
			ResourceKey: entry.ResourceKey, DisplayName: entry.DisplayName,
			Domain: entry.Domain, Actions: entry.Actions,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"resources":          views,
		"rootPolicyRevision": h.RootPolicyRevision,
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
