package keycloakbulkload

import (
	"crypto/sha1" //nolint:gosec // used only to derive a deterministic identifier, never for a security property
	"fmt"
)

// pgx.CopyFromSource implementations for each of the four tables BulkLoad
// writes to, one per batch. Each keeps its own cursor rather than
// materialising a second slice of rows: at DefaultBatchSize this is a small
// saving, but it is the same shape BulkLoad would need if a caller ever
// raised the batch size toward the full 600,000-user population.

type userEntityRows struct {
	realmID string
	created int64
	batch   []UserRecord
	i       int
}

func (r *userEntityRows) Next() bool {
	r.i++
	return r.i <= len(r.batch)
}

func (r *userEntityRows) Values() ([]any, error) {
	u := r.batch[r.i-1]
	return []any{
		u.ID, u.Email, true, true, r.realmID, u.Username,
		u.FirstName, u.LastName, r.created, 0,
	}, nil
}

func (r *userEntityRows) Err() error { return nil }

type userAttributeRows struct {
	batch []UserRecord
	i     int
	// attr toggles between the two attributes (tenant_id, hospital_id) this
	// package writes for every user before advancing to the next user.
	attr int
}

func (r *userAttributeRows) Next() bool {
	if r.attr == 0 && r.i < len(r.batch) {
		r.attr = 1
		r.i++
		return true
	}
	if r.attr == 1 {
		r.attr = 2
		return true
	}
	if r.i < len(r.batch) {
		r.attr = 1
		r.i++
		return true
	}
	return false
}

func (r *userAttributeRows) Values() ([]any, error) {
	u := r.batch[r.i-1]
	name, value := "tenant_id", u.TenantID
	if r.attr == 2 {
		name, value = "hospital_id", u.HospitalID
	}
	return []any{attributeRowID(u.ID, name), name, value, u.ID}, nil
}

func (r *userAttributeRows) Err() error { return nil }

// attributeRowID derives a deterministic id for one user_attribute row from
// the user id and attribute name, so the same population generated twice
// (issue #24's determinism criterion) produces byte-identical rows rather
// than merely the same logical content under a fresh random id each time.
// user_attribute.id is VARCHAR(36) - exactly a UUID's length - so the
// derived id is shaped like one (a hash, not a real UUID; nothing about
// Keycloak's schema requires RFC 4122 version bits, only the 36-character
// dashed form).
func attributeRowID(userID, attrName string) string {
	return deterministicID(userID, attrName)
}

// deterministicID hashes its parts into a 36-character, UUID-shaped string.
func deterministicID(parts ...string) string {
	h := sha1.New() //nolint:gosec // deterministic identifier derivation, not a security use
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	sum := h.Sum(nil)
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

type credentialRows struct {
	cred    SharedCredential
	created int64
	batch   []UserRecord
	i       int
}

func (r *credentialRows) Next() bool {
	r.i++
	return r.i <= len(r.batch)
}

func (r *credentialRows) Values() ([]any, error) {
	u := r.batch[r.i-1]
	return []any{
		deterministicID(u.ID, "credential"), "password", u.ID, r.created,
		r.cred.SecretData, r.cred.CredentialData, 10, 0,
	}, nil
}

func (r *credentialRows) Err() error { return nil }

type roleMappingRows struct {
	batch []UserRecord
	i     int
	j     int
}

func (r *roleMappingRows) Next() bool {
	for {
		if r.i >= len(r.batch) {
			return false
		}
		if r.j < len(r.batch[r.i].RoleIDs) {
			r.j++
			return true
		}
		r.i++
		r.j = 0
	}
}

func (r *roleMappingRows) Values() ([]any, error) {
	u := r.batch[r.i]
	return []any{u.RoleIDs[r.j-1], u.ID}, nil
}

func (r *roleMappingRows) Err() error { return nil }

// membershipRows writes one user_group_membership row per hospital in
// HospitalGroupIDs (issue #87), the same "flatten a per-user slice" shape
// roleMappingRows already uses for RoleIDs.
type membershipRows struct {
	batch []UserRecord
	i     int
	j     int
}

func (r *membershipRows) Next() bool {
	for {
		if r.i >= len(r.batch) {
			return false
		}
		if r.j < len(r.batch[r.i].HospitalGroupIDs) {
			r.j++
			return true
		}
		r.i++
		r.j = 0
	}
}

func (r *membershipRows) Values() ([]any, error) {
	u := r.batch[r.i]
	// UNMANAGED is what the Admin REST API itself writes for a member
	// added directly rather than through an invitation (measured
	// alongside the schema itself; see docs/MEASURED_FINDINGS.md).
	return []any{u.HospitalGroupIDs[r.j-1], u.ID, "UNMANAGED"}, nil
}

func (r *membershipRows) Err() error { return nil }
