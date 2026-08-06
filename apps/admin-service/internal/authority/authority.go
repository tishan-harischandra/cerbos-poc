// Package authority checks that an administrator may act on the tenant and
// hospital a request targets, before any write touches the database (§9.4:
// "Administrator authority validated against the target tenant and hospital
// before any write").
//
// It knows nothing about roles or permissions in the business sense: that is
// Cerbos policy's job, for business resources. An administrator's authority
// over the *administration* surface is a narrower, simpler fact - which
// tenant and hospital their own token scopes them to - and this package is
// the one place that fact is checked.
package authority

import "errors"

// ErrUnauthorized means the principal's token does not scope them to the
// tenant, or the hospital, the request names.
var ErrUnauthorized = errors.New("authority: the administrator's token does not scope them to this tenant or hospital")

// Principal is the administrator's own tenant and hospital scope, as their
// verified token attested it - never as a request body claimed it.
type Principal struct {
	TenantID   string
	HospitalID string
}

// Validate reports whether principal may act on targetTenant and, when
// targetHospital is non-empty, targetHospital too. An empty targetHospital
// means the operation is tenant-scoped only (the role-matrix endpoints);
// hospital is not checked in that case.
func Validate(principal Principal, targetTenant, targetHospital string) error {
	if targetTenant == "" || principal.TenantID == "" || principal.TenantID != targetTenant {
		return ErrUnauthorized
	}
	if targetHospital != "" && principal.HospitalID != targetHospital {
		return ErrUnauthorized
	}
	return nil
}
