package k8slease

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeAPIServer is enough of the coordination.k8s.io/v1 Lease endpoint to
// hold this adapter honest, and specifically enough to enforce the one thing
// the adapter's correctness rests on: an update carrying a stale
// resourceVersion is rejected with 409.
//
// A real cluster is the better test and this is not a substitute for running
// against one. It is what makes the contention and takeover cases run in
// every developer's checkout instead of only where a cluster happens to
// exist, which is the difference between a suite that catches a regression
// and one that is usually skipped.
type fakeAPIServer struct {
	server *httptest.Server

	mu      sync.Mutex
	leases  map[string]*storedLease
	version int
	// requireToken is the bearer token every request must present.
	requireToken string
	// conflicts counts rejected writes, so a test can prove the
	// compare-and-swap is doing something rather than never firing.
	conflicts int
	// rejectNext makes the next write lose its race, which is otherwise a
	// matter of two goroutines arriving in the right order.
	rejectNext bool
}

type storedLease struct {
	object json.RawMessage
	// version is this object's resourceVersion.
	version int
}

func newFakeAPIServer(t *testing.T, token string) *fakeAPIServer {
	t.Helper()
	fake := &fakeAPIServer{leases: map[string]*storedLease{}, requireToken: token}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.handle))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeAPIServer) url() string { return f.server.URL }

// rejectNextWrite makes the next create or update lose, as it would to a
// contender that got there first.
func (f *fakeAPIServer) rejectNextWrite() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rejectNext = true
}

// loseTheRace reports whether this write should be refused, consuming the
// instruction so only one write is affected.
func (f *fakeAPIServer) loseTheRace() bool {
	if !f.rejectNext {
		return false
	}
	f.rejectNext = false
	f.conflicts++
	return true
}

func (f *fakeAPIServer) conflictCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conflicts
}

func (f *fakeAPIServer) handle(w http.ResponseWriter, r *http.Request) {
	if got := r.Header.Get("Authorization"); got != "Bearer "+f.requireToken {
		// An adapter that forgot the ServiceAccount token would
		// otherwise pass every case here and fail only in a cluster.
		http.Error(w, `{"message":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	const prefix = "/apis/coordination.k8s.io/v1/namespaces/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, prefix)
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[1] != "leases" {
		http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
		return
	}
	name := ""
	if len(parts) > 2 {
		name = parts[2]
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	switch r.Method {
	case http.MethodPost:
		f.create(w, r)
	case http.MethodGet:
		f.read(w, name)
	case http.MethodPut:
		f.update(w, r, name)
	default:
		http.Error(w, `{"message":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (f *fakeAPIServer) create(w http.ResponseWriter, r *http.Request) {
	incoming, meta, ok := decode(w, r)
	if !ok {
		return
	}
	if f.loseTheRace() {
		http.Error(w, `{"message":"already exists"}`, http.StatusConflict)
		return
	}
	if _, exists := f.leases[meta.Name]; exists {
		f.conflicts++
		http.Error(w, `{"message":"already exists"}`, http.StatusConflict)
		return
	}
	f.version++
	f.leases[meta.Name] = &storedLease{object: f.stamp(incoming, f.version), version: f.version}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(f.leases[meta.Name].object)
}

func (f *fakeAPIServer) read(w http.ResponseWriter, name string) {
	stored, exists := f.leases[name]
	if !exists {
		http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(stored.object)
}

func (f *fakeAPIServer) update(w http.ResponseWriter, r *http.Request, name string) {
	incoming, meta, ok := decode(w, r)
	if !ok {
		return
	}
	stored, exists := f.leases[name]
	if !exists {
		http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
		return
	}
	if f.loseTheRace() {
		http.Error(w, `{"message":"the object has been modified"}`, http.StatusConflict)
		return
	}
	// The whole point of the fake: a write that did not see the current
	// state is refused, which is what stops two contenders both believing
	// they took the Lease.
	if meta.ResourceVersion != strconv.Itoa(stored.version) {
		f.conflicts++
		http.Error(w, `{"message":"the object has been modified"}`, http.StatusConflict)
		return
	}
	f.version++
	stored.object = f.stamp(incoming, f.version)
	stored.version = f.version
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(stored.object)
}

// stamp writes the server-assigned resourceVersion back into the object, as
// the API server does.
func (f *fakeAPIServer) stamp(object map[string]any, version int) json.RawMessage {
	metadata, _ := object["metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
		object["metadata"] = metadata
	}
	metadata["resourceVersion"] = strconv.Itoa(version)
	encoded, _ := json.Marshal(object)
	return encoded
}

type incomingMeta struct {
	Name            string
	ResourceVersion string
}

func decode(w http.ResponseWriter, r *http.Request) (map[string]any, incomingMeta, bool) {
	var object map[string]any
	if err := json.NewDecoder(r.Body).Decode(&object); err != nil {
		http.Error(w, `{"message":"bad request"}`, http.StatusBadRequest)
		return nil, incomingMeta{}, false
	}
	metadata, _ := object["metadata"].(map[string]any)
	name, _ := metadata["name"].(string)
	version, _ := metadata["resourceVersion"].(string)
	return object, incomingMeta{Name: name, ResourceVersion: version}, true
}
