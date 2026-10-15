package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/sharedvalue"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type importSharedValuesApplyRequest struct {
	// Scripts is the whole import draft. A collection-wide value spans scripts,
	// so unlike the parameterization endpoint this one applies across the batch
	// in a single round trip.
	Scripts []*dsl.Script `json:"scripts"`
	// Script is the single-script convenience form used by importers that
	// produce exactly one draft.
	Script *dsl.Script `json:"script,omitempty"`
	// Values maps candidate ID to the operator-entered value. Candidate bodies
	// are never accepted: the server re-derives them from the scripts it holds.
	Values map[string]string `json:"values"`
	// Bindings supplies an input the source left unset per virtual user
	// instead: a data pool column ({{data.alias.column}}) or a value generated
	// each iteration. Keyed by candidate ID, like Values.
	Bindings map[string]sharedvalue.Binding `json:"bindings,omitempty"`
}

// handleApplyImportSharedValues handles
// POST /api/v1/scripts/import/shared-values/apply.
//
// It writes operator-supplied collection-wide values into every affected script
// of an import draft before the draft is saved. It is importer-agnostic: any
// import response's shared_values block can be replayed through it.
//
// The trust model matches handleApplyImportParameterization exactly. The browser
// supplies candidate IDs and values; the server re-derives what those IDs mean
// from its own projection of the scripts, so a caller can neither invent a
// replacement instruction nor reach a credential-shaped candidate.
func (s *Server) handleApplyImportSharedValues(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	var req importSharedValuesApplyRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	scripts := req.Scripts
	if len(scripts) == 0 && req.Script != nil {
		scripts = []*dsl.Script{req.Script}
	}
	if len(scripts) == 0 {
		writeError(w, http.StatusBadRequest, "scripts is required")
		return
	}
	for _, script := range scripts {
		if script == nil {
			writeError(w, http.StatusBadRequest, "scripts must not contain a null entry")
			return
		}
	}
	if len(req.Values) == 0 && len(req.Bindings) == 0 {
		writeError(w, http.StatusBadRequest, "values or bindings is required")
		return
	}
	for id := range req.Bindings {
		if strings.TrimSpace(req.Values[id]) != "" {
			writeError(w, http.StatusBadRequest, "an input takes either a value for every user or a binding, not both")
			return
		}
	}

	// Claim every credential-preserving draft up front. A collection-wide value
	// touches several scripts at once, so a partial claim would leave some
	// drafts mutated and others not.
	claims := make([]*importDraftAttestationClaim, len(scripts))
	released := false
	releaseAll := func() {
		if released {
			return
		}
		released = true
		for _, claim := range claims {
			if claim != nil {
				s.releaseImportDraftAttestation(claim)
			}
		}
	}
	defer releaseAll()

	for index, script := range scripts {
		if !isCredentialPreservingImport(script) {
			continue
		}
		claim, err := s.claimImportDraftMutation(r, script)
		if err != nil {
			if writeImportDraftBusy(w, err) {
				return
			}
			writeScriptImportResponse(w, http.StatusConflict, map[string]any{
				"error": "credential-preserving import draft is invalid or expired; run the import again",
				"code":  "import_draft_attestation_invalid",
			})
			return
		}
		if claim != nil && claim.Script == nil {
			writeScriptImportResponse(w, http.StatusConflict, map[string]any{
				"error": "credential-preserving import draft is invalid or expired; run the import again",
				"code":  "import_draft_attestation_invalid",
			})
			return
		}
		claims[index] = claim
	}

	checkedFeatures := map[string]struct{}{}
	for index, script := range scripts {
		policyScript := script
		profile := genericImportDraftProfile(script, "")
		if claims[index] != nil {
			policyScript = claims[index].Script
			profile = normalizeGenericImportSourceProfile(claims[index].Profile)
		}
		featureID := featureIDForImportProfile(profile)
		key := policyScript.ProjectID + "\x00" + featureID
		if _, checked := checkedFeatures[key]; checked {
			continue
		}
		if !s.requireDomainFeatureForProject(w, policyScript.ProjectID, featureID) {
			return
		}
		checkedFeatures[key] = struct{}{}
	}

	// Project before deriving instructions. A caller cannot smuggle an inline
	// credential into a replacement, and when the exact draft was authenticated
	// above the mutation lands on that draft so unrelated imported credentials
	// stay byte-identical.
	safeScripts := make([]*dsl.Script, len(scripts))
	applyTargets := make([]*dsl.Script, len(scripts))
	for index, script := range scripts {
		safe := maskScriptSecrets(script)
		if safe == nil {
			writeError(w, http.StatusBadRequest, "failed to project script for shared values")
			return
		}
		safeScripts[index] = safe
		if claims[index] != nil {
			applyTargets[index] = claims[index].Script
		} else {
			applyTargets[index] = safe
		}
	}

	trusted := trustedSharedValueCandidates(safeScripts)
	trustedByID := make(map[string]sharedvalue.Candidate, len(trusted))
	for _, candidate := range trusted {
		trustedByID[candidate.ID] = candidate
	}
	for id := range req.Values {
		if _, ok := trustedByID[id]; !ok {
			writeError(w, http.StatusConflict,
				"selected shared values are stale or unavailable; refresh the import preview")
			return
		}
	}
	for id, binding := range req.Bindings {
		candidate, ok := trustedByID[id]
		if !ok {
			writeError(w, http.StatusConflict,
				"selected input bindings are stale or unavailable; refresh the import preview")
			return
		}
		if candidate.Kind != sharedvalue.KindSourceVariable {
			writeError(w, http.StatusBadRequest, "bindings can only supply an input the source left unset")
			return
		}
		if err := binding.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	diffs, err := sharedvalue.Apply(applyTargets, trusted, req.Values)
	if err != nil {
		if errors.Is(err, sharedvalue.ErrCredentialShaped) {
			writeError(w, http.StatusUnprocessableEntity,
				"credential-shaped shared values must use guided credential bindings")
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "failed to apply shared values: "+err.Error())
		return
	}

	if err := applyImportInputBindings(applyTargets, trusted, req.Values, req.Bindings); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "failed to bind inputs: "+err.Error())
		return
	}

	applied := map[string]bool{}
	for _, diff := range diffs {
		if diff.CandidateID != "" {
			applied[diff.CandidateID] = true
		}
	}
	for id := range req.Bindings {
		applied[id] = true
	}

	// Validate every script before touching any capability, so a validation
	// failure cannot leave half the batch rotated.
	warnings := []string{}
	for _, target := range applyTargets {
		projected := maskScriptSecrets(target)
		if projected == nil {
			writeError(w, http.StatusInternalServerError, "failed to project script after applying shared values")
			return
		}
		warnings = append(warnings, validateScript(projected)...)
	}

	// Rotate the whole batch at once. Rotating per script in a loop would leave
	// the drafts ahead of a mid-loop failure with their old capabilities
	// consumed and their new ones only in the response we are about to discard,
	// making those drafts unsaveable.
	expiresAt, replacementsReady, rotateErr := s.completeImportDraftMutations(r, claims, applyTargets)
	if rotateErr != nil && !replacementsReady {
		writeScriptImportResponse(w, http.StatusServiceUnavailable, map[string]any{
			"error":     "credential-preserving import draft could not be secured; retry the import",
			"code":      "import_draft_attestation_unavailable",
			"retryable": true,
		})
		return
	}
	if rotateErr != nil {
		warnings = append(warnings, "Replacement import-draft capabilities were issued, but cleanup of one or more previous capabilities is still pending.")
	}

	responseScripts := make([]*dsl.Script, 0, len(applyTargets))
	// Parallel to scripts by position: script names are not unique across a
	// batch, so keying expiries by name would drop one draft's capability.
	expiries := make([]*time.Time, len(applyTargets))
	haveExpiry := false
	for index, target := range applyTargets {
		responseScript := maskScriptSecrets(target)
		if claims[index] != nil {
			claims[index] = nil
			responseScript = maskImportDraftForResponse(target)
			if !expiresAt[index].IsZero() {
				stamp := expiresAt[index]
				expiries[index] = &stamp
				haveExpiry = true
			}
		}
		if responseScript == nil {
			writeError(w, http.StatusInternalServerError, "failed to project script after applying shared values")
			return
		}
		responseScripts = append(responseScripts, responseScript)
	}
	releaseAll()

	// Re-derive the candidates from the mutated scripts. Applying a value
	// changes what the collection now shares, so returning the pre-apply block
	// would leave the review panel showing values the scripts no longer hold —
	// and would invite a second apply against IDs the server can no longer
	// re-derive.
	response := map[string]any{
		"scripts":       responseScripts,
		"diffs":         sanitizeSharedValueDiffs(diffs),
		"applied_count": len(applied),
		"warnings":      warnings,
		"shared_values": buildImportSharedValues(responseScripts),
	}
	if haveExpiry {
		response["import_draft_expires_at"] = expiries
	}

	projectID := ""
	name := ""
	if len(responseScripts) > 0 && responseScripts[0] != nil {
		projectID = responseScripts[0].ProjectID
		name = responseScripts[0].Name
	}
	s.recordAuditEvent(r, "script.import.shared_values.apply", "success", "script", name, projectID, map[string]any{
		"selected": len(req.Values),
		"applied":  len(applied),
		"scripts":  len(responseScripts),
		"changes":  len(diffs),
	})

	writeScriptImportResponse(w, http.StatusOK, response)
}

// applyImportInputBindings binds each input the source left unset that the
// operator chose to supply per virtual user, in every script that reads it,
// and records how every supplied input is supplied
// (script.metadata.import.inputs), so a later import can offer the same
// choice again.
func applyImportInputBindings(scripts []*dsl.Script, candidates []sharedvalue.Candidate, values map[string]string, bindings map[string]sharedvalue.Binding) error {
	for _, candidate := range candidates {
		if candidate.Kind != sharedvalue.KindSourceVariable {
			continue
		}
		binding, bound := bindings[candidate.ID]
		valued := strings.TrimSpace(values[candidate.ID]) != ""
		if !bound && !valued {
			continue
		}
		for _, index := range candidate.ScriptIndexes {
			if index < 0 || index >= len(scripts) || scripts[index] == nil {
				continue
			}
			script := scripts[index]
			input := dsl.ImportInput{Mode: "value"}
			if bound {
				if _, err := sharedvalue.Bind(script, candidate, binding); err != nil {
					return err
				}
				input = dsl.ImportInput{Mode: binding.Mode, Alias: binding.Alias, Column: binding.Column, Generator: binding.Generator, Digits: binding.Digits}
			}
			if script.Metadata == nil {
				script.Metadata = &dsl.ScriptMetadata{}
			}
			if script.Metadata.Import == nil {
				script.Metadata.Import = &dsl.ImportMetadata{}
			}
			if script.Metadata.Import.Inputs == nil {
				script.Metadata.Import.Inputs = map[string]dsl.ImportInput{}
			}
			script.Metadata.Import.Inputs[candidate.Name] = input
		}
	}
	return nil
}
