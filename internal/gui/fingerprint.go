package gui

import (
	"encoding/json"
	"net/http"

	"github.com/yetone/magpie/internal/fingerprint"
	"github.com/yetone/magpie/internal/pluginrt"
)

// fingerprintRoutes exposes no upstream credentials and works only while the
// built-in plugin is on. The window's existing API origin guard applies.
func fingerprintRoutes(mux *http.ServeMux) {
	enabled := func(w http.ResponseWriter) bool {
		rt := pluginrt.Running()
		if rt == nil || !rt.Active("fingerprint") {
			http.Error(w, "model fingerprint plugin is off", http.StatusForbidden)
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/fingerprint/models", func(w http.ResponseWriter, r *http.Request) {
		if !enabled(w) {
			return
		}
		models, err := fingerprint.Models(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, map[string]any{"models": models})
	})
	mux.HandleFunc("GET /api/fingerprint/jobs", func(w http.ResponseWriter, r *http.Request) {
		if !enabled(w) {
			return
		}
		writeJSON(w, map[string]any{"jobs": fingerprint.Jobs(), "engine": fingerprint.EngineVersion})
	})
	mux.HandleFunc("POST /api/fingerprint/start", func(w http.ResponseWriter, r *http.Request) {
		if !enabled(w) {
			return
		}
		var in struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in); err != nil {
			http.Error(w, "invalid model selection", http.StatusBadRequest)
			return
		}
		job, err := fingerprint.Start(in.Model)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, job)
	})
	mux.HandleFunc("POST /api/fingerprint/cancel", func(w http.ResponseWriter, r *http.Request) {
		if !enabled(w) {
			return
		}
		var in struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in); err != nil {
			http.Error(w, "invalid job id", http.StatusBadRequest)
			return
		}
		job, err := fingerprint.Cancel(in.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, job)
	})
}
