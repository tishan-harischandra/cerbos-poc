// Package permissionevents defines the §10.2 PermissionChanged wire shape.
//
// It exists so the producer (the admin-service, building an outbox payload)
// and the consumer (the ADS, invalidating cache keys) agree on the shape
// without either importing the other. Neither side gets to invent a field:
// the whole point of a targeted invalidation event is that the consumer can
// act on it without any interpretation.
package permissionevents

import "time"

// SubjectType names what changed: a canonical role's grant, or a single
// user's override.
type SubjectType string

const (
	SubjectRole SubjectType = "ROLE"
	SubjectUser SubjectType = "USER"
)

// EventTypePermissionChanged is the only eventType this package currently
// defines.
const EventTypePermissionChanged = "PermissionChanged"

// PermissionChanged is one row of the §10.2 event shape: naming exactly the
// cache key that changed, so a consumer invalidates only that key rather
// than falling back to "invalidate everything" on every change.
type PermissionChanged struct {
	EventID        string      `json:"eventId"`
	EventType      string      `json:"eventType"`
	InstallationID string      `json:"installationId,omitempty"`
	TenantID       string      `json:"tenantId"`
	HospitalID     string      `json:"hospitalId,omitempty"`
	SubjectType    SubjectType `json:"subjectType"`
	SubjectID      string      `json:"subjectId"`
	Resource       string      `json:"resource"`
	Action         string      `json:"action"`
	// Enabled is the permission's new state. It is what lets a consumer
	// tell a revocation (enabled=false) apart from a grant - §10.3 requires
	// revocation latency to be measured separately from general
	// convergence, and a consumer cannot do that from the shape alone
	// without this field.
	Enabled    bool      `json:"enabled"`
	Revision   int64     `json:"revision"`
	OccurredAt time.Time `json:"occurredAt"`
}
