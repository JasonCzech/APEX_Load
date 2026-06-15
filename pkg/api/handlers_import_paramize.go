package api

import (
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type importParameterizeApplyRequest struct {
	Script      *dsl.Script           `json:"script"`
	Suggestions []paramize.Suggestion `json:"suggestions"`
	SelectedIDs []string              `json:"selected_ids"`
	// ConfirmHighRisk acknowledges that selected high-risk suggestions
	// (secrets externalized to {{env.KEY}}) require runtime environment values
	// before the script can run. Without it, a selection containing high-risk
	// suggestions is blocked — mirroring the authoring correlation gate.
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

	if blockedNames := selectedHighRiskNames(req.Suggestions, selected); len(blockedNames) > 0 && !req.ConfirmHighRisk {
		writeJSON(w, http.StatusOK, importParameterizeApplyResponse{
			Script:        req.Script,
			Diffs:         []paramize.Diff{},
			Blocked:       true,
			BlockedReason: "high-risk suggestions require explicit confirmation (confirm_high_risk): " + strings.Join(blockedNames, ", "),
		})
		return
	}

	applied, diffs, err := paramize.ApplySuggestions(req.Script, req.Suggestions, selected)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "failed to apply parameterization: "+err.Error())
		return
	}

	appliedIDs := map[string]bool{}
	for _, diff := range diffs {
		if diff.SuggestionID != "" {
			appliedIDs[diff.SuggestionID] = true
		}
	}

	warnings := validateScript(applied)
	s.recordAuditEvent(r, "script.import.parameterize.apply", "success", "script", applied.Name, applied.ProjectID, map[string]any{
		"selected": len(req.SelectedIDs),
		"applied":  len(appliedIDs),
		"changes":  len(diffs),
	})

	writeJSON(w, http.StatusOK, importParameterizeApplyResponse{
		Script:       applied,
		Diffs:        diffs,
		AppliedCount: len(appliedIDs),
		Warnings:     warnings,
	})
}

func selectedHighRiskNames(suggestions []paramize.Suggestion, selected map[string]bool) []string {
	names := make([]string, 0)
	for _, suggestion := range suggestions {
		if suggestion.HighRisk && selected[suggestion.ID] {
			names = append(names, suggestion.VarName)
		}
	}
	return names
}
