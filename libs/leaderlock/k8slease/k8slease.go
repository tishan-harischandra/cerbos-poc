// Package k8slease is the leader election adapter backed by a
// coordination.k8s.io/v1 Lease.
//
// It is the right choice on Kubernetes for a reason that has nothing to do
// with the guarantee - the guarantee is the same lease the DATABASE adapter
// offers - and everything to do with operations: the election becomes an
// object an operator can read with kubectl, the control plane already keeps
// it available, and the database is left out of a decision that has nothing
// to do with data.
//
// It speaks to the API server over REST with the pod's own ServiceAccount
// token, the same way libs/cerbosclient and the Keycloak adapter speak to
// their peers. client-go would bring a very large dependency tree into this
// repository for four HTTP calls.
//
// # Expiry is judged locally, not from the holder's clock
//
// A Lease records when its holder last renewed, by the holder's clock. Two
// nodes whose clocks disagree would read the same object and reach different
// conclusions about whether it had expired. So a contender never subtracts
// the holder's timestamp from its own: it remembers when it first observed
// the current renewTime and measures the wait against its own monotonic
// clock. A lease is presumed expired only when this contender has watched it
// stay unchanged for longer than its whole duration.
//
// Nothing outside the provider factory may import this package.
package k8slease

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock"
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/lease"
)

// The files a pod's ServiceAccount is mounted at.
const (
	tokenFile     = "/var/run/secrets/kubernetes.io/serviceaccount/token" //nolint:gosec // a path, not a credential
	caFile        = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	namespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
)

// requestTimeout bounds one API server call.
const requestTimeout = 5 * time.Second

// Config describes the Lease this elector contends for.
type Config struct {
	// BaseURL is the API server. Empty means the in-cluster address from
	// KUBERNETES_SERVICE_HOST and KUBERNETES_SERVICE_PORT.
	BaseURL string
	// Namespace holds the Lease. Empty means the pod's own namespace.
	Namespace string
	// Token authenticates to the API server. Empty means the mounted
	// ServiceAccount token.
	Token string
	// HTTPClient overrides the client built from the mounted CA.
	HTTPClient *http.Client

	// Identity is written to holderIdentity, so `kubectl get lease` names
	// the pod that leads.
	Identity string

	TTL           time.Duration
	RenewInterval time.Duration
	RetryInterval time.Duration

	// Now is the clock this contender measures observation windows with.
	// Injected so a test does not have to wait out a real lease.
	Now func() time.Time

	// PauseRenewal reproduces a paused or killed leader for the contract
	// suite. Production leaves it nil.
	PauseRenewal <-chan struct{}
	// OnError, if set, receives backend failures.
	OnError func(error)
}

// Elector contends for one Lease object.
type Elector struct {
	cfg       Config
	http      *http.Client
	baseURL   string
	namespace string
	token     string

	election leaderlock.Name

	// resourceVersion is the version this contender last read. Sending it
	// back is what makes an update a compare-and-swap: two contenders
	// that read the same Lease cannot both write it.
	resourceVersion string
	// observedRenew is the renewTime this contender last saw, and
	// observedAt is when it saw it by its own clock.
	observedRenew string
	observedAt    time.Time
}

// New returns the adapter.
func New(cfg Config) (*Elector, error) {
	if cfg.Identity == "" {
		return nil, errors.New("k8slease: an identity is required, or a Lease names nobody")
	}
	if cfg.TTL <= 0 {
		return nil, errors.New("k8slease: a ttl is required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	elector := &Elector{cfg: cfg, namespace: cfg.Namespace, token: cfg.Token}

	elector.baseURL = strings.TrimRight(cfg.BaseURL, "/")
	if elector.baseURL == "" {
		host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
		if host == "" || port == "" {
			return nil, errors.New("k8slease: no API server address: set a base url, or run in a cluster")
		}
		elector.baseURL = fmt.Sprintf("https://%s:%s", host, port)
	}
	if elector.namespace == "" {
		namespace, err := os.ReadFile(namespaceFile)
		if err != nil {
			return nil, fmt.Errorf("k8slease: reading the pod's namespace: %w", err)
		}
		elector.namespace = strings.TrimSpace(string(namespace))
	}
	if elector.token == "" {
		token, err := os.ReadFile(tokenFile)
		if err != nil {
			return nil, fmt.Errorf("k8slease: reading the ServiceAccount token: %w", err)
		}
		elector.token = strings.TrimSpace(string(token))
	}

	if cfg.HTTPClient != nil {
		elector.http = cfg.HTTPClient
	} else {
		client, err := inClusterClient()
		if err != nil {
			return nil, err
		}
		elector.http = client
	}
	return elector, nil
}

func inClusterClient() (*http.Client, error) {
	authority, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("k8slease: reading the cluster CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(authority) {
		return nil, errors.New("k8slease: the mounted cluster CA held no certificate")
	}
	return &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
	}, nil
}

// Run contends for the Lease the election name maps to. The name is used
// verbatim as the object name, which is why the port restricts names to what
// a Kubernetes object accepts.
func (e *Elector) Run(ctx context.Context, election leaderlock.Name, onElected func(context.Context)) error {
	if err := election.Validate(); err != nil {
		return err
	}
	e.election = election

	return lease.Run(ctx, lease.Config{
		TTL:           e.cfg.TTL,
		RenewInterval: e.cfg.RenewInterval,
		RetryInterval: e.cfg.RetryInterval,
		PauseRenewal:  e.cfg.PauseRenewal,
		OnError:       e.cfg.OnError,
	}, e, onElected)
}

// Acquire creates the Lease, renews our own, or takes over one this
// contender has watched go stale.
func (e *Elector) Acquire(ctx context.Context) (bool, error) {
	current, found, err := e.get(ctx)
	if err != nil {
		return false, err
	}
	if !found {
		created, err := e.create(ctx)
		if err != nil {
			return false, err
		}
		return created, nil
	}

	if current.Spec.HolderIdentity == e.cfg.Identity {
		return e.put(ctx, current)
	}

	// Somebody else holds it. Only this contender's own observation of
	// how long the record has stood still may decide it is stale.
	if current.Spec.RenewTime != e.observedRenew {
		e.observedRenew = current.Spec.RenewTime
		e.observedAt = e.cfg.Now()
		return false, nil
	}
	if e.cfg.Now().Sub(e.observedAt) < e.leaseDuration(current) {
		return false, nil
	}
	return e.put(ctx, current)
}

// Renew extends a Lease this contender still holds.
func (e *Elector) Renew(ctx context.Context) (bool, error) {
	current, found, err := e.get(ctx)
	if err != nil {
		return false, err
	}
	if !found || current.Spec.HolderIdentity != e.cfg.Identity {
		// The object was deleted, or a rival took it. Either way this
		// instance no longer leads, and that is an answer rather than
		// a failure.
		return false, nil
	}
	return e.put(ctx, current)
}

// Release hands the Lease over at once by clearing the holder, rather than
// deleting the object: an operator's `kubectl get lease` keeps showing the
// election, and its history, across a rolling restart.
func (e *Elector) Release(ctx context.Context) error {
	current, found, err := e.get(ctx)
	if err != nil || !found {
		return err
	}
	if current.Spec.HolderIdentity != e.cfg.Identity {
		return nil
	}

	current.Spec.HolderIdentity = ""
	// An expired renewTime is what makes the next contender take it
	// immediately instead of waiting out a duration nobody is using.
	current.Spec.RenewTime = formatTime(e.cfg.Now().Add(-2 * e.cfg.TTL))
	body, err := json.Marshal(current)
	if err != nil {
		return fmt.Errorf("k8slease: encoding the released Lease: %w", err)
	}
	_, err = e.do(ctx, http.MethodPut, e.objectPath(), body)
	return err
}

// leaseSpec is the part of coordination.k8s.io/v1 Lease this adapter uses.
type leaseSpec struct {
	HolderIdentity       string `json:"holderIdentity,omitempty"`
	LeaseDurationSeconds int    `json:"leaseDurationSeconds,omitempty"`
	AcquireTime          string `json:"acquireTime,omitempty"`
	RenewTime            string `json:"renewTime,omitempty"`
}

type leaseMeta struct {
	Name            string `json:"name"`
	Namespace       string `json:"namespace,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

type leaseObject struct {
	APIVersion string    `json:"apiVersion"`
	Kind       string    `json:"kind"`
	Metadata   leaseMeta `json:"metadata"`
	Spec       leaseSpec `json:"spec"`
}

func (e *Elector) get(ctx context.Context) (*leaseObject, bool, error) {
	body, err := e.do(ctx, http.MethodGet, e.objectPath(), nil)
	if errors.Is(err, errNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var object leaseObject
	if err := json.Unmarshal(body, &object); err != nil {
		return nil, false, fmt.Errorf("k8slease: decoding the Lease: %w", err)
	}
	e.resourceVersion = object.Metadata.ResourceVersion
	return &object, true, nil
}

func (e *Elector) create(ctx context.Context) (bool, error) {
	now := e.cfg.Now()
	object := leaseObject{
		APIVersion: "coordination.k8s.io/v1",
		Kind:       "Lease",
		Metadata:   leaseMeta{Name: string(e.election), Namespace: e.namespace},
		Spec: leaseSpec{
			HolderIdentity:       e.cfg.Identity,
			LeaseDurationSeconds: e.ttlSeconds(),
			AcquireTime:          formatTime(now),
			RenewTime:            formatTime(now),
		},
	}
	body, err := json.Marshal(object)
	if err != nil {
		return false, fmt.Errorf("k8slease: encoding the Lease: %w", err)
	}
	if _, err := e.do(ctx, http.MethodPost, e.collectionPath(), body); err != nil {
		if errors.Is(err, errConflict) {
			// Another contender created it first. Not an error: it
			// simply won this round.
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// put writes our claim back with the resourceVersion we read, so the API
// server rejects the write if anybody else changed the object first. That
// rejection is the whole of this adapter's mutual exclusion.
func (e *Elector) put(ctx context.Context, current *leaseObject) (bool, error) {
	now := e.cfg.Now()
	taking := current.Spec.HolderIdentity != e.cfg.Identity

	current.Spec.HolderIdentity = e.cfg.Identity
	current.Spec.LeaseDurationSeconds = e.ttlSeconds()
	current.Spec.RenewTime = formatTime(now)
	if taking || current.Spec.AcquireTime == "" {
		current.Spec.AcquireTime = formatTime(now)
	}

	body, err := json.Marshal(current)
	if err != nil {
		return false, fmt.Errorf("k8slease: encoding the Lease: %w", err)
	}
	if _, err := e.do(ctx, http.MethodPut, e.objectPath(), body); err != nil {
		if errors.Is(err, errConflict) {
			// Somebody wrote between our read and our write, so we
			// did not get it. Nothing is wrong.
			return false, nil
		}
		return false, err
	}
	return true, nil
}

var (
	errNotFound = errors.New("k8slease: the Lease does not exist")
	errConflict = errors.New("k8slease: the Lease changed underneath this write")
)

func (e *Elector) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, e.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("k8slease: building the request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+e.token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := e.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: k8slease: %s %s: %w", leaderlock.ErrBackendUnavailable, method, path, err)
	}
	defer func() { _ = response.Body.Close() }()

	payload, err := readAll(response)
	if err != nil {
		return nil, err
	}
	switch {
	case response.StatusCode == http.StatusNotFound:
		return nil, errNotFound
	case response.StatusCode == http.StatusConflict:
		return nil, errConflict
	case response.StatusCode >= 400:
		return nil, fmt.Errorf("%w: k8slease: %s %s: %s: %s",
			leaderlock.ErrBackendUnavailable, method, path, response.Status, strings.TrimSpace(string(payload)))
	}
	return payload, nil
}

// readAll bounds how much of an API server response is read. A Lease is a
// small object; anything enormous here is a proxy or an error page, and
// reading it into memory unbounded would be the bug rather than the symptom.
func readAll(response *http.Response) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: k8slease: reading the response: %w", leaderlock.ErrBackendUnavailable, err)
	}
	return payload, nil
}

// maxResponseBytes is generous for a Lease and small enough that a wrong
// endpoint cannot exhaust memory.
const maxResponseBytes = 1 << 20

func (e *Elector) collectionPath() string {
	return fmt.Sprintf("/apis/coordination.k8s.io/v1/namespaces/%s/leases", e.namespace)
}

func (e *Elector) objectPath() string {
	return e.collectionPath() + "/" + string(e.election)
}

// leaseDuration is how long the current holder asked to be trusted for. Using
// the holder's own figure rather than ours means a fleet mid-rollout, with two
// different ttls configured, still agrees about when a lease is stale.
func (e *Elector) leaseDuration(current *leaseObject) time.Duration {
	if current.Spec.LeaseDurationSeconds > 0 {
		return time.Duration(current.Spec.LeaseDurationSeconds) * time.Second
	}
	return e.cfg.TTL
}

// ttlSeconds is leaseDurationSeconds, which the API expects as a whole
// number. A sub-second lease rounds up to one second rather than to zero,
// which would mean "expired the moment it was written".
func (e *Elector) ttlSeconds() int {
	seconds := int(e.cfg.TTL / time.Second)
	if e.cfg.TTL%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	return seconds
}

// formatTime writes a MicroTime, which is what the Lease spec's time fields
// are: RFC 3339 with microsecond precision.
func formatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000Z")
}
