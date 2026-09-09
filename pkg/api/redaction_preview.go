package api

import (
	"github.com/jasonczech/apexload/pkg/engine"
	"net/http"
)

type RedactionPreviewRequest struct {
	Sample string                 `json:"sample"`
	Rules  []engine.RedactionRule `json:"rules"`
}

// A stateless preview uses exactly the runtime compiler and scrubber. It never
// reads project data, stores the sample, or changes the saved policy.
func (s *Server) handleRedactionPreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req RedactionPreviewRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if len(req.Sample) > 16384 {
		writeError(w, http.StatusBadRequest, "Sample must be at most 16 KiB")
		return
	}
	config, err := engine.NewRedactionConfig(req.Rules)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"preview": config.ScrubText(req.Sample), "saved": false})
}
