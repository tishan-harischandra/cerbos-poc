// Package server exposes the Assignment Data Service (ADS) HTTP surface.
package server

import (
	"encoding/json"
	"net/http"
)

// Config holds the collaborators the ADS HTTP surface depends on.
type Config struct{}

// New builds the ADS HTTP handler.
func New(cfg Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
