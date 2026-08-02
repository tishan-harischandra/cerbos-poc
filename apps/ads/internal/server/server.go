// Package server exposes the Assignment Data Service (ADS) HTTP surface.
package server

import (
	"context"
	"encoding/json"
	"net/http"
)

// Dependency is a downstream the ADS needs before it can serve traffic.
type Dependency struct {
	Name  string
	Probe func(context.Context) error
}

// Config holds the collaborators the ADS HTTP surface depends on.
type Config struct {
	Dependencies []Dependency
}

// New builds the ADS HTTP handler.
func New(cfg Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		results := make(map[string]string, len(cfg.Dependencies))
		ready := true
		for _, dep := range cfg.Dependencies {
			if err := dep.Probe(r.Context()); err != nil {
				results[dep.Name] = err.Error()
				ready = false
				continue
			}
			results[dep.Name] = "ok"
		}

		status, code := "ready", http.StatusOK
		if !ready {
			status, code = "unavailable", http.StatusServiceUnavailable
		}
		writeJSON(w, code, map[string]any{"status": status, "dependencies": results})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
