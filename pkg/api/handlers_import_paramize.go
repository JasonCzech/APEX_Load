package api

import (
	"net/http"
	"time"

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
	Script               *dsl.Script     `json:"script"`
	Diffs                []paramize.Diff `json:"diffs"`
	AppliedCount         int             `json:"applied_count"`
	Blocked              bool            `json:"blocked,omitempty"`
	BlockedReason        string          `json:"blocked_reason,omitempty"`
	Warnings             []string        `json:"warnings,omitempty"`
	ImportDraftExpiresAt *time.Time      `json:"import_draft_expires_at,omitempty"`
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

	var mutationClaim *importDraftAttestationClaim
	if isCredentialPreservingImport(req.Script) {
		var claimErr error
		mutationClaim, claimErr = s.claimImportDraftMutation(r, req.Script)
		if claimErr != nil {
			writeScriptImportResponse(w, http.StatusConflict, map[string]any{
				"error": "credential-preserving import draft is invalid or expired; run the import again",
				"code":  "import_draft_attestation_invalid",
			})
			return
		}
		defer func() {
			if mutationClaim != nil {
				s.releaseImportDraftAttestation(mutationClaim)
			}
		}()
	}

	// Project before deriving instructions. This accepts deferred [REDACTED]
	// placeholders while ensuring a caller cannot smuggle an inline credential
	// into client-controlled replacement instructions. When the exact draft was
	// authenticated above, the trusted instructions are applied to that exact
	// draft so unrelated imported credentials remain byte-identical.
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

	applyTarget := safeScript
	if mutationClaim != nil {
		if mutationClaim.Script == nil {
			writeError(w, http.StatusConflict, "credential-preserving import draft is invalid or expired; run the import again")
			return
		}
		applyTarget = mutationClaim.Script
	}
	applied, diffs, err := paramize.ApplySuggestions(applyTarget, trusted.Suggestions, selected)
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
	responseScript := projected
	var importDraftExpiresAt *time.Time
	if mutationClaim != nil {
		expiresAt, rotateErr := s.completeImportDraftMutation(r, mutationClaim, applied)
		if rotateErr != nil {
			writeScriptImportResponse(w, http.StatusServiceUnavailable, map[string]any{
				"error":     "credential-preserving import draft could not be secured; retry the import",
				"code":      "import_draft_attestation_unavailable",
				"retryable": true,
			})
			return
		}
		mutationClaim = nil
		responseScript = maskImportDraftForResponse(applied)
		if !expiresAt.IsZero() {
			importDraftExpiresAt = &expiresAt
		}
	}
	s.recordAuditEvent(r, "script.import.parameterize.apply", "success", "script", projected.Name, projected.ProjectID, map[string]any{
		"selected": len(req.SelectedIDs),
		"applied":  len(appliedIDs),
		"changes":  len(diffs),
	})

	response := map[string]any{
		"script":        responseScript,
		"diffs":         diffs,
		"applied_count": len(appliedIDs),
		"warnings":      warnings,
	}
	if importDraftExpiresAt != nil {
		response["import_draft_expires_at"] = importDraftExpiresAt
	}
	writeScriptImportResponse(w, http.StatusOK, response)
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
