// Package server exposes the policy controller's readiness/health surface.
package server

import (
	"encoding/json"
	"net/http"
	"sync"
)

// Status is the controller's current leadership and last-run state,
// updated by the poll loop and read by the HTTP handlers below. Safe for
// concurrent use.
type Status struct {
	mu           sync.RWMutex
	leader       bool
	lastRevision string
	lastOK       bool
	lastError    string
}

// NewStatus builds an empty Status.
func NewStatus() *Status {
	return &Status{}
}

// SetLeader records whether this instance currently holds the leader-election
// lock.
func (s *Status) SetLeader(leader bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leader = leader
}

// SetLastResult records the outcome of the most recent release pipeline run.
func (s *Status) SetLastResult(revision string, ok bool, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRevision = revision
	s.lastOK = ok
	s.lastError = errMsg
}

func (s *Status) snapshot() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return map[string]any{
		"leader":       s.leader,
		"lastRevision": s.lastRevision,
		"lastOK":       s.lastOK,
		"lastError":    s.lastError,
	}
}

// New builds the controller's HTTP handler. Readiness reflects the HTTP
// surface being up, not the outcome of the last release: a passive instance
// that never wins the leader-election lock is still ready, and a leader
// whose last release failed validation must keep reporting ready so it can
// keep polling and try the next tag.
func New(status *Status) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, status.snapshot())
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
