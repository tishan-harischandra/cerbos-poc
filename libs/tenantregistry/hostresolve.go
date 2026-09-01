package tenantregistry

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// HostResolver maps a request to the tenant it names (issue #83). A user
// reaches their hospital group by its own web address and never needs to
// know what a realm is; this is the one place that fact is decided.
//
// It is an interface, not a function, so a later change of resolution
// scheme - a path prefix, a header a trusted proxy sets, anything else -
// is a new implementation of this same contract. Every caller already
// depends on the interface, never on how it decides, so swapping the
// strategy touches this one module and no callers.
type HostResolver interface {
	// Resolve maps req.Host to the tenant it names, or reports an error
	// for a host that names no known tenant - missing, malformed, or
	// syntactically a tenant but not one this installation has registered.
	Resolve(host string) (Entry, error)
}

// hostSubdomainResolver is the default strategy (issue #83): the tenant is
// the host's first label, exactly the realm name - "tenant-a.example.test"
// names tenant-a. It never falls back to guessing a tenant a caller did
// not clearly name: an installation serving one realm still requires the
// browser to say which one, so a host that never worked correctly cannot
// start silently working differently the day a second tenant is added.
type hostSubdomainResolver struct {
	byTenant map[string]Entry
}

// NewHostResolver builds the default host-subdomain HostResolver from a
// tenant registry's own entries. A realm named more than once is a
// registry authoring error already caught by Parse; this constructor
// trusts entries to be that already-validated set.
func NewHostResolver(entries []Entry) HostResolver {
	byTenant := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		byTenant[entry.Realm] = entry
	}
	return &hostSubdomainResolver{byTenant: byTenant}
}

func (r *hostSubdomainResolver) Resolve(host string) (Entry, error) {
	tenant, err := subdomainTenant(host)
	if err != nil {
		return Entry{}, err
	}
	entry, ok := r.byTenant[tenant]
	if !ok {
		return Entry{}, fmt.Errorf("tenantregistry: %q does not name a tenant this installation serves", tenant)
	}
	return entry, nil
}

// subdomainTenant extracts the candidate tenant name from a request Host
// header, without consulting the registry: this is the pure half of
// resolution, so "what counts as a well-formed host" is unit-testable on
// its own.
func subdomainTenant(host string) (string, error) {
	if host == "" {
		return "", errors.New("tenantregistry: the request carries no Host header")
	}

	hostOnly := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostOnly = h
	}
	if hostOnly == "" {
		return "", fmt.Errorf("tenantregistry: %q names no host", host)
	}
	if net.ParseIP(hostOnly) != nil {
		return "", fmt.Errorf("tenantregistry: %q is an IP address, not a tenant subdomain", hostOnly)
	}

	labels := strings.Split(hostOnly, ".")
	tenant := labels[0]
	if len(labels) < 2 || tenant == "" {
		return "", fmt.Errorf("tenantregistry: %q has no tenant subdomain", hostOnly)
	}
	return tenant, nil
}
