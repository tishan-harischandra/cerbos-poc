package invalidation_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/invalidation"
	"github.com/tishan-harischandra/cerbos-poc/libs/permissionevents"
)

type fakeCache struct {
	invalidatedRoles     []string
	invalidatedRevisions []string
}

func (c *fakeCache) InvalidateRole(tenantID, roleID string) {
	c.invalidatedRoles = append(c.invalidatedRoles, tenantID+"/"+roleID)
}

func (c *fakeCache) InvalidateRevision(tenantID string) {
	c.invalidatedRevisions = append(c.invalidatedRevisions, tenantID)
}

type fakeMetrics struct {
	invalidationLatencies []time.Duration
	revocationLatencies   []time.Duration
}

func (m *fakeMetrics) ObserveInvalidationLatency(d time.Duration) {
	m.invalidationLatencies = append(m.invalidationLatencies, d)
}

func (m *fakeMetrics) ObserveRevocationLatency(d time.Duration) {
	m.revocationLatencies = append(m.revocationLatencies, d)
}

func marshalBatch(t *testing.T, events ...permissionevents.PermissionChanged) []byte {
	t.Helper()
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshaling a batch: %v", err)
	}
	return raw
}

// A ROLE event must invalidate exactly the named role in the named tenant,
// and always invalidate that tenant's cached revision too.
func TestHandleMessageInvalidatesTheNamedRoleAndTheTenantRevision(t *testing.T) {
	cache := &fakeCache{}
	handler := &invalidation.Handler{Cache: cache, Now: func() time.Time { return time.Unix(100, 0) }}

	batch := marshalBatch(t, permissionevents.PermissionChanged{
		TenantID:    "tenant-a",
		SubjectType: permissionevents.SubjectRole,
		SubjectID:   "role-doctor",
		Resource:    "patient_record",
		Action:      "read",
		Enabled:     true,
		OccurredAt:  time.Unix(99, 0),
	})

	if err := handler.HandleMessage(batch); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if len(cache.invalidatedRoles) != 1 || cache.invalidatedRoles[0] != "tenant-a/role-doctor" {
		t.Errorf("invalidated roles = %v, want [tenant-a/role-doctor]", cache.invalidatedRoles)
	}
	if len(cache.invalidatedRevisions) != 1 || cache.invalidatedRevisions[0] != "tenant-a" {
		t.Errorf("invalidated revisions = %v, want [tenant-a]", cache.invalidatedRevisions)
	}
}

// A USER event must invalidate the tenant's revision but must not call
// InvalidateRole - there is no per-user cache entry to drop by that method.
func TestHandleMessageForAUserEventDoesNotInvalidateARole(t *testing.T) {
	cache := &fakeCache{}
	handler := &invalidation.Handler{Cache: cache}

	batch := marshalBatch(t, permissionevents.PermissionChanged{
		TenantID:    "tenant-a",
		SubjectType: permissionevents.SubjectUser,
		SubjectID:   "user-1",
		Resource:    "patient_record",
		Action:      "read",
		Enabled:     true,
		OccurredAt:  time.Unix(99, 0),
	})

	if err := handler.HandleMessage(batch); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if len(cache.invalidatedRoles) != 0 {
		t.Errorf("invalidated roles = %v, want none for a USER event", cache.invalidatedRoles)
	}
	if len(cache.invalidatedRevisions) != 1 {
		t.Errorf("invalidated revisions = %v, want [tenant-a]", cache.invalidatedRevisions)
	}
}

// Revocation latency (§10.3) is reported only for an event that disabled a
// permission, never for a grant, and every event contributes to the general
// invalidation-latency metric regardless.
func TestHandleMessageReportsRevocationLatencyOnlyForADisabledPermission(t *testing.T) {
	cache := &fakeCache{}
	metrics := &fakeMetrics{}
	handler := &invalidation.Handler{
		Cache: cache, Metrics: metrics,
		Now: func() time.Time { return time.Unix(105, 0) },
	}

	batch := marshalBatch(t,
		permissionevents.PermissionChanged{
			TenantID: "tenant-a", SubjectType: permissionevents.SubjectRole, SubjectID: "role-doctor",
			Resource: "patient_record", Action: "read", Enabled: true, OccurredAt: time.Unix(100, 0),
		},
		permissionevents.PermissionChanged{
			TenantID: "tenant-a", SubjectType: permissionevents.SubjectRole, SubjectID: "role-doctor",
			Resource: "patient_record", Action: "delete", Enabled: false, OccurredAt: time.Unix(102, 0),
		},
	)

	if err := handler.HandleMessage(batch); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if len(metrics.invalidationLatencies) != 2 {
		t.Fatalf("got %d invalidation-latency observations, want 2 (one per event)",
			len(metrics.invalidationLatencies))
	}
	if len(metrics.revocationLatencies) != 1 {
		t.Fatalf("got %d revocation-latency observations, want 1 (only the disabled event)",
			len(metrics.revocationLatencies))
	}
	if metrics.revocationLatencies[0] != 3*time.Second {
		t.Errorf("revocation latency = %s, want 3s (105s - 102s)", metrics.revocationLatencies[0])
	}
}

// A message that is not a valid PermissionChanged batch must be reported as
// an error, not silently swallowed.
func TestHandleMessageRejectsAMalformedBatch(t *testing.T) {
	handler := &invalidation.Handler{Cache: &fakeCache{}}
	if err := handler.HandleMessage([]byte("not json")); err == nil {
		t.Fatal("HandleMessage accepted a malformed batch")
	}
}

type fakeReader struct {
	values [][]byte
	i      int
}

func (r *fakeReader) ReadMessageValue(ctx context.Context) ([]byte, error) {
	if r.i >= len(r.values) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	value := r.values[r.i]
	r.i++
	return value, nil
}

// Consumer.Run must process every message in order and then return once the
// context is cancelled, rather than looping forever on an exhausted reader.
func TestConsumerRunProcessesEveryMessageThenStopsOnCancellation(t *testing.T) {
	cache := &fakeCache{}
	handler := &invalidation.Handler{Cache: cache}
	reader := &fakeReader{values: [][]byte{
		marshalBatch(t, permissionevents.PermissionChanged{
			TenantID: "tenant-a", SubjectType: permissionevents.SubjectRole, SubjectID: "role-doctor",
		}),
		marshalBatch(t, permissionevents.PermissionChanged{
			TenantID: "tenant-b", SubjectType: permissionevents.SubjectRole, SubjectID: "role-nurse",
		}),
	}}
	consumer := &invalidation.Consumer{Reader: reader, Handler: handler}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		consumer.Run(ctx)
		close(done)
	}()

	deadline := time.After(time.Second)
	for {
		if len(cache.invalidatedRevisions) == 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Run did not process both messages in time")
		case <-time.After(time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}

// A handling error for one message must not stop the loop from processing
// the next.
func TestConsumerRunReportsAHandlingErrorAndContinues(t *testing.T) {
	cache := &fakeCache{}
	handler := &invalidation.Handler{Cache: cache}
	reader := &fakeReader{values: [][]byte{
		[]byte("not json"),
		marshalBatch(t, permissionevents.PermissionChanged{
			TenantID: "tenant-a", SubjectType: permissionevents.SubjectRole, SubjectID: "role-doctor",
		}),
	}}

	var reportedErrors []error
	consumer := &invalidation.Consumer{
		Reader: reader, Handler: handler,
		OnError: func(err error) { reportedErrors = append(reportedErrors, err) },
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		consumer.Run(ctx)
		close(done)
	}()

	deadline := time.After(time.Second)
	for {
		if len(cache.invalidatedRevisions) == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Run did not recover from the malformed message and process the next")
		case <-time.After(time.Millisecond):
		}
	}
	if len(reportedErrors) != 1 {
		t.Errorf("got %d reported errors, want 1 for the malformed message", len(reportedErrors))
	}

	cancel()
	<-done
}
