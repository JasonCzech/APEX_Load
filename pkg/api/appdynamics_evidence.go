package api

import (
	"net/http"
	"strconv"

	"github.com/jasonczech/apexload/pkg/appdynamics"
)

type RunMonitorEvidence struct {
	SourceID string `json:"source_id"`
	appdynamics.Diagnostic
}

func (s *Server) handleRunMonitorEvidence(w http.ResponseWriter, r *http.Request) {
	project, status, message := resolveTestProjectID(r, s.store)
	if status != 0 {
		writeError(w, status, message)
		return
	}
	store := s.runMonitorStore(w, project)
	if store == nil {
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	kind := r.URL.Query().Get("kind")
	if kind != "" && kind != "event" && kind != "violation" && kind != "snapshot" && kind != "log" && kind != "transaction" {
		writeError(w, http.StatusBadRequest, "unsupported evidence kind")
		return
	}
	page, err := loadCanonicalMonitorData(r.Context(), store, project, r.PathValue("id"), r.URL.Query().Get("source_id"), "diagnostic", r.URL.Query().Get("after_key"), limit)
	if err != nil {
		writeError(w, 503, "monitor evidence unavailable or corrupt")
		return
	}
	evidence := []RunMonitorEvidence{}
	for i, d := range page.Evidence {
		if kind == "" || d.Kind == kind {
			evidence = append(evidence, RunMonitorEvidence{SourceID: page.EvidenceSources[i], Diagnostic: d})
		}
	}
	writeJSON(w, 200, map[string]any{"evidence": evidence, "has_more": page.HasMore, "after_key": page.AfterKey, "after_sequence": page.AfterKey})
}
