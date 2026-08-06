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

// Handler serves the resource catalog and capability impact endpoints.
type Handler struct {
	// Resources is loaded once at startup from the same catalog directory
	// the write path validates permission leaves against (§12.2) - the
	// browser never gets a second, divergent copy of the catalog.
	Resources          []capabilitycatalog.ResourceEntry
	RootPolicyRevision string
	// Capabilities is the full committed composite UI-capability set
	// (issue #10), the same definitions the write path validates
	// permission leaves against - CapabilityImpact is a read over this,
	// never a second, hand-maintained index (§6.1: "must not use an
	// action-to-UI-capability list as the authoritative mapping").
	Capabilities []capabilitycatalog.UiCapabilityDefinition
}

type resourceView struct {
	ResourceKey string                          `json:"resourceKey"`
	DisplayName string                          `json:"displayName"`
	Domain      string                          `json:"domain"`
	Actions     []capabilitycatalog.ActionEntry `json:"actions"`
}

type capabilityView struct {
	Key     string `json:"key"`
	Module  string `json:"module"`
	Context string `json:"context"`
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

// CapabilityImpact handles
// GET /admin/authz/resources/{resource}/actions/{action}/capabilities
// (issue #18, §9.1): every composite UI capability whose expression
// references the named resource-action, so the role matrix save flow can
// show what changing this permission may affect before the change is
// committed. Always 200 with a (possibly empty) list - a resource-action
// nothing depends on is not an error condition.
func (h *Handler) CapabilityImpact(w http.ResponseWriter, r *http.Request) {
	if _, ok := tokenauth.From(r.Context()); !ok {
		writeError(w, http.StatusUnauthorized, "no verified identity on this request")
		return
	}

	resource := r.PathValue("resource")
	action := r.PathValue("action")
	matches := capabilitycatalog.CapabilitiesReferencing(h.Capabilities, resource, action)

	views := make([]capabilityView, 0, len(matches))
	for _, m := range matches {
		views = append(views, capabilityView{Key: m.Key, Module: m.Module, Context: m.Context})
	}
	writeJSON(w, http.StatusOK, map[string]any{"capabilities": views})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
