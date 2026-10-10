package api

import (
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// maxImportTransportEdits bounds one Transport panel apply.
const maxImportTransportEdits = 10000

// importTransportApplyRequest carries the Transport panel's reviewed edits:
// each names a request path, its origin, and the policy the review showed.
type importTransportApplyRequest struct {
	Script *dsl.Script                   `json:"script"`
	Edits  []requestimport.TransportEdit `json:"edits"`
}

// handleApplyImportTransport handles
// POST /api/v1/scripts/import/transport/apply: an author's explicit HTTP
// version choice for the requests of one or more origins in an import draft.
// The trust model matches handleApplyImportParameterization: a credential-
// preserving draft is claimed, edited and re-attested on the server.
//
// OpenAPI-Responses: 400, 409, 422, 503
func (s *Server) handleApplyImportTransport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	var req importTransportApplyRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.Script == nil {
		writeError(w, http.StatusBadRequest, "script is required")
		return
	}
	if len(req.Edits) == 0 || len(req.Edits) > maxImportTransportEdits {
		writeError(w, http.StatusBadRequest, "edits must list between 1 and 10000 request changes")
		return
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
	policyScript := req.Script
	profile := genericImportDraftProfile(req.Script, "")
	if mutationClaim != nil {
		policyScript = mutationClaim.Script
		profile = normalizeGenericImportSourceProfile(mutationClaim.Profile)
	}
	if policyScript == nil || !s.requireDomainFeatureForProject(w, policyScript.ProjectID, featureIDForImportProfile(profile)) {
		return
	}

	applyTarget := maskScriptSecrets(req.Script)
	if mutationClaim != nil {
		applyTarget = mutationClaim.Script
	}
	if applyTarget == nil {
		writeError(w, http.StatusBadRequest, "failed to project script for transport edits")
		return
	}
	before := validateScript(maskScriptSecrets(applyTarget))
	changes, err := requestimport.ApplyTransportEdits(applyTarget, req.Edits)
	if errors.Is(err, requestimport.ErrStaleTransportEdit) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	projected := maskScriptSecrets(applyTarget)
	if projected == nil {
		writeError(w, http.StatusInternalServerError, "failed to project the edited script")
		return
	}
	warnings := validateScript(projected)
	// Refuse an edit that makes the draft invalid (exact HTTP/3 with a
	// custom proxy, for example); issues the draft already had are not new.
	var introduced []string
	for _, issue := range warnings {
		if !slices.Contains(before, issue) {
			introduced = append(introduced, issue)
		}
	}
	if len(introduced) > 0 {
		writeScriptImportResponse(w, http.StatusUnprocessableEntity, map[string]any{
			"error":             "the HTTP version choice is not valid for this script",
			"validation_issues": introduced,
		})
		return
	}

	responseScript := projected
	var importDraftExpiresAt *time.Time
	if mutationClaim != nil {
		expiresAt, rotateErr := s.completeImportDraftMutation(r, mutationClaim, applyTarget)
		if rotateErr != nil {
			writeScriptImportResponse(w, http.StatusServiceUnavailable, map[string]any{
				"error":     "credential-preserving import draft could not be secured; retry the import",
				"code":      "import_draft_attestation_unavailable",
				"retryable": true,
			})
			return
		}
		mutationClaim = nil
		responseScript = maskImportDraftForResponse(applyTarget)
		if !expiresAt.IsZero() {
			importDraftExpiresAt = &expiresAt
		}
	}
	resolved := 0
	for _, change := range changes {
		resolved += len(change.Resolved)
	}
	s.recordAuditEvent(r, "script.import.transport.apply", "success", "script", projected.Name, projected.ProjectID, map[string]any{
		"changes":  len(changes),
		"resolved": resolved,
	})
	response := map[string]any{
		"script":   responseScript,
		"changes":  changes,
		"warnings": warnings,
	}
	if importDraftExpiresAt != nil {
		response["import_draft_expires_at"] = importDraftExpiresAt
	}
	writeScriptImportResponse(w, http.StatusOK, response)
}
