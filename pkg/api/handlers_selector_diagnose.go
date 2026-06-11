package api

import (
	"encoding/json"
	"net/http"

	"github.com/jasonczech/apexload/pkg/selector"
)

type selectorDiagnoseRequest struct {
	Steps []selector.ReplayStep `json:"steps"`
}

type selectorDiagnoseResponse struct {
	Suggestions []selector.RepairSuggestion `json:"suggestions"`
	Blocking    bool                        `json:"blocking"`
	Counts      selectorDiagnoseCounts      `json:"counts"`
}

type selectorDiagnoseCounts struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
}

// handleDiagnoseSelectors runs selector repair analysis over browser replay
// outcomes: failed selectors with viable ranked alternates, self-healed
// fallbacks worth promoting, and weakly-anchored selectors that pass today but
// deserve a dedicated test hook. Mirrors POST /api/v1/correlations/diagnose.
func (s *Server) handleDiagnoseSelectors(w http.ResponseWriter, r *http.Request) {
	var req selectorDiagnoseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Steps) == 0 {
		writeError(w, http.StatusBadRequest, "steps are required")
		return
	}

	suggestions := selector.Diagnose(req.Steps)
	counts := selectorDiagnoseCounts{}
	for _, suggestion := range suggestions {
		switch suggestion.Severity {
		case selector.SeverityError:
			counts.Errors++
		case selector.SeverityWarning:
			counts.Warnings++
		}
	}

	writeJSON(w, http.StatusOK, selectorDiagnoseResponse{
		Suggestions: suggestions,
		Blocking:    selector.HasBlockingRepairs(suggestions),
		Counts:      counts,
	})
}
