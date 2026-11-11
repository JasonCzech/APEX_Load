package recordbridge

import (
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/importreview"
	"github.com/jasonczech/apexload/pkg/scrub"
	"net/http"
	"time"
)

type ApplySecretBindingsRequest struct {
	Script      *dsl.Script                           `json:"script"`
	ScriptIndex int                                   `json:"script_index,omitempty"`
	Bindings    []importreview.SecretBindingSelection `json:"bindings"`
}

type ImportParameterizeApplyRequest struct {
	Script      *dsl.Script           `json:"script"`
	Suggestions []paramize.Suggestion `json:"suggestions"`
	SelectedIDs []string              `json:"selected_ids"`
	// ConfirmHighRisk remains accepted for wire compatibility. Credential
	// suggestions are no longer selectable here; they use guided bindings.
	ConfirmHighRisk bool `json:"confirm_high_risk,omitempty"`
}

type ImportParameterizeApplyResponse struct {
	Script               *dsl.Script     `json:"script"`
	Diffs                []paramize.Diff `json:"diffs"`
	AppliedCount         int             `json:"applied_count"`
	Blocked              bool            `json:"blocked,omitempty"`
	BlockedReason        string          `json:"blocked_reason,omitempty"`
	Warnings             []string        `json:"warnings,omitempty"`
	ImportDraftExpiresAt *time.Time      `json:"import_draft_expires_at,omitempty"`
}

func (s *Service) HandleApplyScriptSecretBindings(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var req ApplySecretBindingsRequest
	if err := decoder.Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	var mutationClaim *DraftClaim
	if importreview.IsCredentialPreservingImport(req.Script) && importreview.ImportDraftAttestationToken(req.Script) != "" {
		var claimErr error
		mutationClaim, claimErr = s.Conversion.Drafts.Claim(r, req.Script)
		if claimErr != nil {
			if s.Conversion.Drafts.WriteClaimError(w, claimErr) {
				return
			}
			WriteJSON(w, http.StatusConflict, map[string]any{
				"error": "credential-preserving import draft is invalid or expired; run the import again",
				"code":  "import_draft_attestation_invalid",
			})
			return
		}
		defer func() {
			if mutationClaim != nil {
				mutationClaim.Release()
			}
		}()
	}
	mutationTarget := req.Script
	if mutationClaim != nil {
		if mutationClaim.Script == nil {
			s.writeError(w, http.StatusConflict, "credential-preserving import draft is invalid or expired; run the import again")
			return
		}
		mutationTarget = mutationClaim.Script
	}
	resolved, remaining, err := importreview.ApplyCredentialPreservingSecretBindings(mutationTarget, req.ScriptIndex, req.Bindings)
	if err != nil {
		var stale *importreview.StaleSecretBindingRequirementsError
		if errors.As(err, &stale) {
			WriteJSON(w, http.StatusConflict, map[string]any{
				"error":                       stale.Error(),
				"code":                        importreview.SecretBindingRequirementsChangedCode,
				"secret_binding_requirements": stale.Requirements(),
			})
			return
		}
		var unavailable *importreview.SecretBindingRebindUnavailableError
		if errors.As(err, &unavailable) {
			WriteJSON(w, http.StatusBadRequest, map[string]any{
				"error":          scrub.Value(unavailable.Error(), scrub.Policy{}),
				"code":           importreview.SecretBindingRebindUnavailableCode,
				"requirement_id": unavailable.Requirement().ID,
				"path":           unavailable.Requirement().Path,
				// A review that predates the rebind flag refreshes from these.
				"secret_binding_requirements": unavailable.Requirements(),
			})
			return
		}
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if remaining == nil {
		remaining = []importreview.SecretBindingRequirement{}
	}
	var expiresAt any
	if mutationClaim != nil {
		rotatedExpiry, rotateErr := mutationClaim.Complete(r, resolved)
		if rotateErr != nil {
			importreview.StripImportDraftAttestation(resolved)
		} else {
			mutationClaim = nil
			if !rotatedExpiry.IsZero() {
				expiresAt = rotatedExpiry
			}
		}
	}
	response := map[string]any{
		"script":                      importreview.CloneScriptDefinition(resolved),
		"secret_binding_requirements": remaining,
	}
	if expiresAt != nil {
		response["import_draft_expires_at"] = expiresAt
	}
	s.writeImportResponse(w, http.StatusOK, response)
}

func (s *Service) HandleApplyImportParameterization(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	var req ImportParameterizeApplyRequest
	if !s.decodeJSONBody(w, r, &req) {
		return
	}
	if req.Script == nil {
		s.writeError(w, http.StatusBadRequest, "script is required")
		return
	}
	if len(req.SelectedIDs) == 0 {
		s.writeError(w, http.StatusBadRequest, "selected_ids is required")
		return
	}

	selected := make(map[string]bool, len(req.SelectedIDs))
	for _, id := range req.SelectedIDs {
		selected[id] = true
	}

	var mutationClaim *DraftClaim
	if importreview.IsCredentialPreservingImport(req.Script) && importreview.ImportDraftAttestationToken(req.Script) != "" {
		var claimErr error
		mutationClaim, claimErr = s.Conversion.Drafts.Claim(r, req.Script)
		if claimErr != nil {
			if s.Conversion.Drafts.WriteClaimError(w, claimErr) {
				return
			}
			s.writeImportResponse(w, http.StatusConflict, map[string]any{
				"error": "credential-preserving import draft is invalid or expired; run the import again",
				"code":  "import_draft_attestation_invalid",
			})
			return
		}
		defer func() {
			if mutationClaim != nil {
				mutationClaim.Release()
			}
		}()
	}
	policyScript := req.Script
	profile := importProfile(req.Script)
	if mutationClaim != nil {
		policyScript = mutationClaim.Script
		profile = mutationClaim.Profile
	}
	if policyScript == nil || !s.requireImportFeature(w, policyScript.ProjectID, profile) {
		return
	}

	applyTarget := importreview.CloneScriptDefinition(req.Script)
	if mutationClaim != nil {
		if mutationClaim.Script == nil {
			s.writeError(w, http.StatusConflict, "credential-preserving import draft is invalid or expired; run the import again")
			return
		}
		applyTarget = mutationClaim.Script
	}
	if applyTarget == nil {
		s.writeError(w, http.StatusBadRequest, "failed to prepare script for parameterization")
		return
	}

	trusted := importreview.BuildTrustedImportParameterization(applyTarget)
	trustedByID := make(map[string]bool, len(trusted.Suggestions))
	trustedDescriptors := make(map[string]paramize.Suggestion, len(trusted.Suggestions))
	for _, suggestion := range trusted.Suggestions {
		trustedByID[suggestion.ID] = true
		trustedDescriptors[suggestion.ID] = suggestion
	}
	for id := range selected {
		if !trustedByID[id] {
			s.writeError(w, http.StatusConflict, "selected parameterization suggestions are stale or unavailable; refresh the import preview")
			return
		}
	}

	for _, descriptor := range req.Suggestions {
		if selected[descriptor.ID] && !importreview.MatchesTrustedAuraDescriptor(descriptor, trustedDescriptors[descriptor.ID]) {
			s.writeError(w, http.StatusConflict, "selected Aura parameterization descriptor is stale or invalid; refresh the import preview")
			return
		}
	}

	applySuggestions, valid := importreview.RebindTrustedAuraParameterization(applyTarget, trusted.Suggestions, selected)
	if !valid {
		s.writeError(w, http.StatusConflict, "selected Aura parameterization no longer matches the authoritative import draft; refresh the import preview")
		return
	}
	applied, diffs, err := paramize.ApplySuggestions(applyTarget, applySuggestions, selected)
	if err != nil {
		s.writeError(w, http.StatusUnprocessableEntity, "failed to apply parameterization: "+err.Error())
		return
	}
	responseScript := importreview.CloneScriptDefinition(applied)
	if responseScript == nil {
		s.writeError(w, http.StatusInternalServerError, "failed to clone parameterized script")
		return
	}

	appliedIDs := map[string]bool{}
	for _, diff := range diffs {
		if diff.SuggestionID != "" {
			appliedIDs[diff.SuggestionID] = true
		}
	}

	warnings := importreview.ValidateScript(responseScript)
	var importDraftExpiresAt *time.Time
	if mutationClaim != nil {
		expiresAt, rotateErr := mutationClaim.Complete(r, applied)
		if rotateErr != nil {
			importreview.StripImportDraftAttestation(responseScript)
		} else {
			mutationClaim = nil
			responseScript = importreview.CloneScriptDefinition(applied)
			if !expiresAt.IsZero() {
				importDraftExpiresAt = &expiresAt
			}
		}
	} else {
		importreview.StripImportDraftAttestation(responseScript)
	}
	s.audit(r, "script.import.parameterize.apply", "success", "script", responseScript.Name, responseScript.ProjectID, map[string]any{
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
	s.writeImportResponse(w, http.StatusOK, response)
}
