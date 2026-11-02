package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
)

const maxImportExcludeActionUIDs = 10000

type importExcludeActionsRequest struct {
	Script                  *dsl.Script `json:"script"`
	ActionUIDs              []string    `json:"action_uids,omitempty"`
	ExcludeUnexportedBodies bool        `json:"exclude_unexported_bodies,omitempty"`
}

type importExcludeActionsResponse struct {
	Script                    *dsl.Script                `json:"script"`
	RemovedCount              int                        `json:"removed_count"`
	Preflight                 *ReplayPreflight           `json:"preflight,omitempty"`
	SecretBindingRequirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	Warnings                  []string                   `json:"warnings,omitempty"`
	ImportDraftExpiresAt      *time.Time                 `json:"import_draft_expires_at,omitempty"`
}

// handleExcludeImportDraftActions handles
// POST /api/v1/scripts/import/exclude-actions: removes execution-blocked
// imported requests (such as Chrome HAR requests whose bodies were not
// exported, including sub-requests inside http_sequence concurrency groups)
// from an import draft, refreshes replay_preflight and secret bindings, and
// rotates any credential-preserving draft attestation on the server.
//
// OpenAPI-Responses: 400, 409, 422, 503
func (s *Server) handleExcludeImportDraftActions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, webAPIImportCommitMaxBodyBytes)
	var req importExcludeActionsRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.Script == nil {
		writeError(w, http.StatusBadRequest, "script is required")
		return
	}
	if len(req.ActionUIDs) > maxImportExcludeActionUIDs {
		writeError(w, http.StatusBadRequest, "action_uids exceeds maximum limit")
		return
	}
	uidSet := make(map[string]struct{}, len(req.ActionUIDs))
	for _, rawUID := range req.ActionUIDs {
		if trimmed := strings.TrimSpace(rawUID); trimmed != "" {
			uidSet[trimmed] = struct{}{}
		}
	}
	if len(uidSet) == 0 && !req.ExcludeUnexportedBodies {
		writeError(w, http.StatusBadRequest, "specify action_uids or exclude_unexported_bodies")
		return
	}

	var mutationClaim *importDraftAttestationClaim
	if isCredentialPreservingImport(req.Script) {
		var claimErr error
		mutationClaim, claimErr = s.claimImportDraftMutation(r, req.Script)
		if claimErr != nil {
			if writeImportDraftBusy(w, claimErr) {
				return
			}
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
		applyTarget = cloneScriptDefinition(mutationClaim.Script)
	}
	if applyTarget == nil {
		writeError(w, http.StatusBadRequest, "failed to prepare script for action exclusion")
		return
	}
	priorSnapshot := cloneScriptDefinition(applyTarget)

	removedCount := excludeBlockedImportActionsFromScript(applyTarget, uidSet, req.ExcludeUnexportedBodies)
	if removedCount == 0 {
		writeError(w, http.StatusUnprocessableEntity, "no matching execution-blocked actions were found in the draft")
		return
	}
	if len(applyTarget.Actions) == 0 || !hasNonMarkerAction(applyTarget.Actions) {
		writeError(w, http.StatusUnprocessableEntity, "script must retain at least one executable action in actions[]")
		return
	}

	resolver := s.runner.secretResolverForProject(applyTarget.ProjectID)
	_, requirements, _, _, evalErr := evaluateRepairedScript(applyTarget, resolver)
	if evalErr != nil {
		writeError(w, http.StatusUnprocessableEntity, "cannot evaluate draft readiness after excluding actions")
		return
	}
	preflight := repairReplayPreflight(applyTarget, priorSnapshot, resolver)
	attachImportReplayPreflight(applyTarget, profile, preflight)

	projected := maskScriptSecrets(applyTarget)
	if projected == nil {
		writeError(w, http.StatusInternalServerError, "failed to project the edited script")
		return
	}
	warnings := validateScript(projected)

	responseScript := projected
	var importDraftExpiresAt *time.Time
	if mutationClaim != nil {
		expiresAt, rotateErr := s.completeImportDraftMutation(r, mutationClaim, applyTarget)
		if rotateErr != nil {
			writeScriptImportResponse(w, http.StatusServiceUnavailable, map[string]any{
				"error":     "credential-preserving import draft could not be secured; retry the operation",
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

	s.recordAuditEvent(r, "script.import.exclude_actions", "success", "script", projected.Name, projected.ProjectID, map[string]any{
		"removed_count":             removedCount,
		"exclude_unexported_bodies": req.ExcludeUnexportedBodies,
	})
	writeJSON(w, http.StatusOK, importExcludeActionsResponse{
		Script:                    responseScript,
		RemovedCount:              removedCount,
		Preflight:                 preflight,
		SecretBindingRequirements: requirements,
		Warnings:                  warnings,
		ImportDraftExpiresAt:      importDraftExpiresAt,
	})
}

func excludeBlockedImportActionsFromScript(script *dsl.Script, uidSet map[string]struct{}, excludeUnexportedBodies bool) int {
	if script == nil {
		return 0
	}
	removed := 0
	var r int
	script.Init, r = filterBlockedActionSlice(script.Init, uidSet, excludeUnexportedBodies)
	removed += r
	script.Actions, r = filterBlockedActionSlice(script.Actions, uidSet, excludeUnexportedBodies)
	removed += r
	script.Teardown, r = filterBlockedActionSlice(script.Teardown, uidSet, excludeUnexportedBodies)
	removed += r
	return removed
}

func filterBlockedActionSlice(actions []dsl.Action, uidSet map[string]struct{}, excludeUnexportedBodies bool) ([]dsl.Action, int) {
	if len(actions) == 0 {
		return actions, 0
	}
	removed := 0
	out := make([]dsl.Action, 0, len(actions))
	for _, action := range actions {
		if shouldExcludeBlockedMetadata(action.Metadata, uidSet, excludeUnexportedBodies) {
			removed++
			continue
		}
		if action.Params != nil {
			if rawReqs, hasReqs := action.Params["requests"]; hasReqs {
				filteredReqs, subRemoved, remainingCount, handled := filterBlockedSequenceRequests(rawReqs, uidSet, excludeUnexportedBodies)
				if handled {
					removed += subRemoved
					if remainingCount == 0 {
						continue
					}
					paramsCopy := make(map[string]any, len(action.Params))
					for k, v := range action.Params {
						paramsCopy[k] = v
					}
					paramsCopy["requests"] = filteredReqs
					action.Params = paramsCopy
				}
			}
		}
		if action.Control != nil {
			ctrl := *action.Control
			var r int
			ctrl.Steps, r = filterBlockedActionSlice(ctrl.Steps, uidSet, excludeUnexportedBodies)
			removed += r
			ctrl.Else, r = filterBlockedActionSlice(ctrl.Else, uidSet, excludeUnexportedBodies)
			removed += r
			ctrl.Default, r = filterBlockedActionSlice(ctrl.Default, uidSet, excludeUnexportedBodies)
			removed += r
			if len(ctrl.Cases) > 0 {
				cases := make([]dsl.SwitchCase, 0, len(ctrl.Cases))
				for _, branch := range ctrl.Cases {
					var cr int
					branch.Steps, cr = filterBlockedActionSlice(branch.Steps, uidSet, excludeUnexportedBodies)
					removed += cr
					if len(branch.Steps) > 0 {
						cases = append(cases, branch)
					}
				}
				ctrl.Cases = cases
			}
			action.Control = &ctrl
			switch strings.ToLower(strings.TrimSpace(action.Type)) {
			case "control_if", "control_loop":
				if len(ctrl.Steps) == 0 && len(ctrl.Else) == 0 {
					continue
				}
			case "control_switch":
				if len(ctrl.Cases) == 0 && len(ctrl.Default) == 0 {
					continue
				}
			}
		}
		out = append(out, action)
	}
	if removed > 0 {
		out = pruneEmptyTransactionMarkers(out)
	}
	return out, removed
}

func pruneEmptyTransactionMarkers(actions []dsl.Action) []dsl.Action {
	for len(actions) >= 2 {
		changed := false
		next := make([]dsl.Action, 0, len(actions))
		for i := 0; i < len(actions); i++ {
			if i+1 < len(actions) &&
				isTransactionMarkerFunc(actions[i], "lr_start_transaction") &&
				isTransactionMarkerFunc(actions[i+1], "lr_end_transaction") &&
				dsl.TransactionMarkerName(actions[i]) == dsl.TransactionMarkerName(actions[i+1]) {
				changed = true
				i++
				continue
			}
			next = append(next, actions[i])
		}
		actions = next
		if !changed {
			break
		}
	}
	return actions
}

func isTransactionMarkerFunc(action dsl.Action, fn string) bool {
	if strings.TrimSpace(action.Type) != "source_transaction" {
		return false
	}
	got, _ := action.Params["function"].(string)
	return strings.TrimSpace(got) == fn
}

func hasNonMarkerAction(actions []dsl.Action) bool {
	for _, action := range actions {
		if strings.TrimSpace(action.Type) != "source_transaction" {
			return true
		}
	}
	return false
}

func filterBlockedSequenceRequests(raw any, uidSet map[string]struct{}, excludeUnexportedBodies bool) (any, int, int, bool) {
	switch typed := raw.(type) {
	case []dsl.Action:
		filtered, removed := filterBlockedActionSlice(typed, uidSet, excludeUnexportedBodies)
		return filtered, removed, len(filtered), true
	case []map[string]any:
		removed := 0
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			meta, _ := item["metadata"].(map[string]any)
			if shouldExcludeBlockedMetadata(meta, uidSet, excludeUnexportedBodies) {
				removed++
				continue
			}
			out = append(out, item)
		}
		return out, removed, len(out), true
	case []any:
		removed := 0
		out := make([]any, 0, len(typed))
		for _, elem := range typed {
			switch item := elem.(type) {
			case dsl.Action:
				if shouldExcludeBlockedMetadata(item.Metadata, uidSet, excludeUnexportedBodies) {
					removed++
					continue
				}
				out = append(out, item)
			case map[string]any:
				meta, _ := item["metadata"].(map[string]any)
				if shouldExcludeBlockedMetadata(meta, uidSet, excludeUnexportedBodies) {
					removed++
					continue
				}
				out = append(out, item)
			default:
				out = append(out, elem)
			}
		}
		return out, removed, len(out), true
	default:
		return raw, 0, 0, false
	}
}

func shouldExcludeBlockedMetadata(meta map[string]any, uidSet map[string]struct{}, excludeUnexportedBodies bool) bool {
	if len(meta) == 0 {
		return false
	}
	hasUnexportedBody := metadataHasExecutionBlocker(meta, sample.HARRequestBodyNotExportedCode)
	hasAnyBlocker := hasUnexportedBody || metadataHasAnyExecutionBlocker(meta)
	if !hasAnyBlocker {
		return false
	}
	if excludeUnexportedBodies && hasUnexportedBody {
		return true
	}
	if len(uidSet) > 0 {
		if uid, ok := meta["import_action_uid"].(string); ok {
			if _, matched := uidSet[strings.TrimSpace(uid)]; matched {
				return true
			}
		}
	}
	return false
}

func metadataHasExecutionBlocker(meta map[string]any, code string) bool {
	if len(meta) == 0 {
		return false
	}
	raw, err := json.Marshal(meta["execution_blockers"])
	if err == nil && strings.Contains(string(raw), code) {
		return true
	}
	rawFindings, err := json.Marshal(meta["import_review_findings"])
	return err == nil && strings.Contains(string(rawFindings), code)
}

func metadataHasAnyExecutionBlocker(meta map[string]any) bool {
	if len(meta) == 0 {
		return false
	}
	switch blockers := meta["execution_blockers"].(type) {
	case []string:
		if len(blockers) > 0 {
			return true
		}
	case []any:
		if len(blockers) > 0 {
			return true
		}
	}
	if rawFindings, ok := meta["import_review_findings"]; ok && rawFindings != nil {
		raw, err := json.Marshal(rawFindings)
		if err == nil && strings.Contains(string(raw), `"gate":"execution"`) {
			return true
		}
	}
	return false
}
