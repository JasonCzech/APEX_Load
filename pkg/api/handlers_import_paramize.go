package api

import (
	"net/http"

	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type importParameterizeApplyRequest struct {
	Script      *dsl.Script           `json:"script"`
	Suggestions []paramize.Suggestion `json:"suggestions"`
	SelectedIDs []string              `json:"selected_ids"`
	// ConfirmHighRisk remains accepted for wire compatibility. Credential
	// suggestions are no longer selectable here; they use guided bindings.
	ConfirmHighRisk bool `json:"confirm_high_risk,omitempty"`
}

type importParameterizeApplyResponse struct {
	Script        *dsl.Script     `json:"script"`
	Diffs         []paramize.Diff `json:"diffs"`
	AppliedCount  int             `json:"applied_count"`
	Blocked       bool            `json:"blocked,omitempty"`
	BlockedReason string          `json:"blocked_reason,omitempty"`
	Warnings      []string        `json:"warnings,omitempty"`
}

// handleApplyImportParameterization handles
// POST /api/v1/scripts/import/parameterize/apply.
// It applies the user-approved subset of parameterization suggestions to an
// import draft and returns the mutated script plus per-change diffs. The
// endpoint is importer-agnostic: any import response's parameterization block
// can be replayed through it before the script is saved.
func (s *Server) handleApplyImportParameterization(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	var req importParameterizeApplyRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.Script == nil {
		writeError(w, http.StatusBadRequest, "script is required")
		return
	}
	if len(req.SelectedIDs) == 0 {
		writeError(w, http.StatusBadRequest, "selected_ids is required")
		return
	}

	selected := make(map[string]bool, len(req.SelectedIDs))
	for _, id := range req.SelectedIDs {
		selected[id] = true
	}
	for _, suggestion := range req.Suggestions {
		if selected[suggestion.ID] && suggestion.Kind == paramize.KindSecret {
			writeError(w, http.StatusUnprocessableEntity, "secret parameterization must use guided credential bindings")
			return
		}
	}

	// Project before deriving instructions. This accepts deferred [REDACTED]
	// placeholders while ensuring a caller cannot smuggle an inline credential
	// into the response or into client-controlled replacement instructions.
	safeScript := maskScriptSecrets(req.Script)
	if safeScript == nil {
		writeError(w, http.StatusBadRequest, "failed to project script for parameterization")
		return
	}
	trusted := buildTrustedImportParameterization(safeScript)
	trustedByID := make(map[string]bool, len(trusted.Suggestions))
	for _, suggestion := range trusted.Suggestions {
		trustedByID[suggestion.ID] = true
	}
	for id := range selected {
		if !trustedByID[id] {
			writeError(w, http.StatusConflict, "selected parameterization suggestions are stale or unavailable; refresh the import preview")
			return
		}
	}

	applied, diffs, err := paramize.ApplySuggestions(safeScript, trusted.Suggestions, selected)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "failed to apply parameterization: "+err.Error())
		return
	}
	projected := maskScriptSecrets(applied)
	if projected == nil {
		writeError(w, http.StatusInternalServerError, "failed to project parameterized script")
		return
	}
	diffs = sanitizeParameterizationDiffs(diffs)

	appliedIDs := map[string]bool{}
	for _, diff := range diffs {
		if diff.SuggestionID != "" {
			appliedIDs[diff.SuggestionID] = true
		}
	}

	warnings := validateScript(projected)
	s.recordAuditEvent(r, "script.import.parameterize.apply", "success", "script", projected.Name, projected.ProjectID, map[string]any{
		"selected": len(req.SelectedIDs),
		"applied":  len(appliedIDs),
		"changes":  len(diffs),
	})

	writeJSON(w, http.StatusOK, importParameterizeApplyResponse{
		Script:       projected,
		Diffs:        diffs,
		AppliedCount: len(appliedIDs),
		Warnings:     warnings,
	})
}

func sanitizeParameterizationDiffs(input []paramize.Diff) []paramize.Diff {
	output := make([]paramize.Diff, len(input))
	copy(output, input)
	for index := range output {
		output[index].Before = ""
		output[index].After = ""
	}
	return output
}
