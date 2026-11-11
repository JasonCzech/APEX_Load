package api

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/convert/sharedvalue"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/importreview"
)

const maxImportExcludeActionUIDs = 10000

type importExcludeActionsRequest struct {
	Script                  *dsl.Script `json:"script"`
	ActionUIDs              []string    `json:"action_uids,omitempty"`
	ExcludeUnexportedBodies bool        `json:"exclude_unexported_bodies,omitempty"`
	// SourceSecretSessionID is the import's optional source-secret migration
	// session. Requirement IDs encode action positions, so the exclusion
	// re-keys the session's recorded values to the moved requirements.
	SourceSecretSessionID string `json:"source_secret_session_id,omitempty"`
}

type importExcludeActionsResponse struct {
	Script       *dsl.Script      `json:"script"`
	RemovedCount int              `json:"removed_count"`
	Preflight    *ReplayPreflight `json:"preflight,omitempty"`
	// SecretBindingRequirements replaces the script's whole requirement list:
	// discovered from the masked projection like the import response, with the
	// protected credential descriptors that still match merged in.
	SecretBindingRequirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	// Parameterization is re-derived from the edited draft. Suggestion IDs
	// encode action positions, so the import's list goes stale once an action
	// before them is removed. Converter-applied correlations are not repeated.
	Parameterization *paramize.Result `json:"parameterization"`
	// ValidationIssues is the edited draft's static validation. It is not the
	// import's converter warnings, which the exclusion leaves unchanged.
	ValidationIssues     []string   `json:"validation_issues,omitempty"`
	ImportDraftExpiresAt *time.Time `json:"import_draft_expires_at,omitempty"`
	// SharedValues is re-derived from the edited draft. Candidate IDs hash
	// each location's action path, so the import's list goes stale once an
	// action before them is removed.
	SharedValues *sharedvalue.Result `json:"shared_values"`
	// SourceSecretSessionID is present only when the request named a
	// source-secret session. That session is consumed: the value is its
	// re-keyed replacement, or "" when no recorded value survived (or the
	// session could not be re-keyed), so the client must drop the old ID.
	SourceSecretSessionID        *string    `json:"source_secret_session_id,omitempty"`
	SourceSecretSessionExpiresAt *time.Time `json:"source_secret_session_expires_at,omitempty"`
}

// handleExcludeImportDraftActions handles
// POST /api/v1/scripts/import/exclude-actions: removes execution-blocked
// imported requests (such as Chrome HAR requests whose bodies were not
// exported, including sub-requests inside http_sequence concurrency groups)
// from an import draft, remaps what refers to actions by position, refreshes
// replay_preflight, secret bindings and parameterization, and rotates any
// credential-preserving draft attestation on the server.
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
	if isCredentialPreservingImport(req.Script) && (importDraftAttestationToken(req.Script) != "" || isJMeterBatchChildDraft(req.Script)) {
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

	applyTarget := cloneScriptDefinition(req.Script)
	if mutationClaim != nil {
		applyTarget = cloneScriptDefinition(mutationClaim.Script)
	}
	if applyTarget == nil {
		writeError(w, http.StatusBadRequest, "failed to prepare script for action exclusion")
		return
	}
	priorSnapshot := cloneScriptDefinition(applyTarget)

	exclusion := excludeBlockedImportActionsFromScript(applyTarget, uidSet, req.ExcludeUnexportedBodies)
	if exclusion.removed == 0 {
		writeError(w, http.StatusUnprocessableEntity, "no matching execution-blocked actions were found in the draft")
		return
	}
	if len(applyTarget.Actions) == 0 || !hasNonMarkerAction(applyTarget.Actions) {
		writeError(w, http.StatusUnprocessableEntity, "script must retain at least one executable action in actions[]")
		return
	}
	// Source mappings and protected credential locations name actions by
	// position, so every action after a removed one moved.
	remapExcludedSourceReviewMappings(applyTarget, exclusion)
	if err := importreview.RefreshProtectedImportCredentialLocations(applyTarget); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "cannot refresh protected credential locations after excluding actions")
		return
	}

	resolver := s.runner.secretResolverForProject(applyTarget.ProjectID)
	if _, _, _, _, evalErr := evaluateRepairedScript(applyTarget, resolver); evalErr != nil {
		writeError(w, http.StatusUnprocessableEntity, "cannot evaluate draft readiness after excluding actions")
		return
	}
	// evaluateRepairedScript writes a fidelity report, which launch admission
	// treats as a promotion gate. A draft imported without one (every HAR and
	// Salesforce draft) must not gain one here: no later HAR review edit or
	// import commit refreshes it, so it would go stale and could hold a run.
	dropAddedImportFidelityReport(applyTarget, priorSnapshot)
	preflight := repairReplayPreflight(applyTarget, priorSnapshot, resolver)
	attachImportReplayPreflight(applyTarget, profile, preflight)

	projected := cloneScriptDefinition(applyTarget)
	if projected == nil {
		writeError(w, http.StatusInternalServerError, "failed to project the edited script")
		return
	}
	validationIssues := validateScript(projected)
	credentialPreserving := mutationClaim != nil
	requirements := importreview.EditedImportDraftSecretBindingRequirements(applyTarget, credentialPreserving)
	parameterization := filterTrustedImportParameterization(
		buildImportParameterization(cloneScriptDefinition(applyTarget), nil),
		[]*dsl.Script{cloneScriptDefinition(applyTarget)},
	)

	responseScript := projected
	var importDraftExpiresAt *time.Time
	if mutationClaim != nil {
		expiresAt, rotateErr := s.completeImportDraftMutation(r, mutationClaim, applyTarget)
		if rotateErr == nil {
			mutationClaim = nil
			responseScript = maskImportDraftForResponse(applyTarget)
			if !expiresAt.IsZero() {
				importDraftExpiresAt = &expiresAt
			}
		} else {
			stripImportDraftAttestation(responseScript)
		}
	}

	sharedValues := buildImportSharedValues([]*dsl.Script{cloneScriptDefinition(responseScript)})

	var sourceSessionID *string
	var sourceSessionExpiresAt *time.Time
	if strings.TrimSpace(req.SourceSecretSessionID) != "" {
		previous := importreview.EditedImportDraftSecretBindingRequirements(priorSnapshot, credentialPreserving)
		newID, expiresAt := s.rekeyExcludedImportSourceSecretSession(r, policyScript.ProjectID, req.SourceSecretSessionID, exclusion, previous, requirements)
		sourceSessionID = &newID
		if newID != "" {
			sourceSessionExpiresAt = &expiresAt
		}
	}

	s.recordAuditEvent(r, "script.import.exclude_actions", "success", "script", projected.Name, projected.ProjectID, map[string]any{
		"removed_count":             exclusion.removed,
		"exclude_unexported_bodies": req.ExcludeUnexportedBodies,
	})
	writeJSON(w, http.StatusOK, importExcludeActionsResponse{
		Script:                       responseScript,
		RemovedCount:                 exclusion.removed,
		Preflight:                    preflight,
		SecretBindingRequirements:    requirements,
		Parameterization:             parameterization,
		ValidationIssues:             validationIssues,
		ImportDraftExpiresAt:         importDraftExpiresAt,
		SharedValues:                 sharedValues,
		SourceSecretSessionID:        sourceSessionID,
		SourceSecretSessionExpiresAt: sourceSessionExpiresAt,
	})
}

// rekeyExcludedImportSourceSecretSession consumes the import's source-secret
// session and registers a replacement whose recorded values follow their
// requirements to the IDs they have after the exclusion. It marks those
// requirements source_value_available. A value whose requirement was removed,
// or cannot be matched to exactly one moved requirement, is dropped; values
// keyed to requirements outside this draft carry over unchanged. It returns
// the replacement session ID, or "" when there is none.
func (s *Server) rekeyExcludedImportSourceSecretSession(
	r *http.Request,
	scriptProjectID, sessionID string,
	exclusion *importActionExclusion,
	previous, current []SecretBindingRequirement,
) (string, time.Time) {
	projectID := normalizeProjectID(scriptProjectID)
	if authorizedProjectID, scoped := authorizedProjectFromRequest(r); scoped {
		if projectID != "" && projectID != authorizedProjectID {
			return "", time.Time{}
		}
		projectID = authorizedProjectID
	}
	session, err := s.consumeSourceSecretSession(r, projectID, sessionID)
	if err != nil || session == nil {
		return "", time.Time{}
	}
	defer clearSourceSecretSession(session)

	moved := excludedImportRequirementIDMoves(exclusion, previous, current)
	values := make(map[string]string, len(session.Values))
	for id, value := range session.Values {
		if newID, tracked := moved[id]; tracked {
			if newID != "" {
				values[newID] = value
			}
			continue
		}
		values[id] = value
	}
	if len(values) == 0 {
		return "", time.Time{}
	}
	newSessionID, err := s.registerSourceSecretSession(r, session.ProjectID, values, session.FeatureIDs...)
	if err != nil || newSessionID == "" {
		clear(values)
		return "", time.Time{}
	}
	for index := range current {
		if _, available := values[current[index].ID]; available {
			current[index].SourceValueAvailable = true
		}
	}
	clear(values)
	return newSessionID, time.Now().UTC().Add(sourceSecretSessionTTL)
}

// excludedImportRequirementIDMoves maps every requirement ID of the draft
// before the exclusion to its ID after it, or to "" when its location was
// removed or no single requirement with the same script, label, context and
// base suggested name now sits at the moved location.
func excludedImportRequirementIDMoves(exclusion *importActionExclusion, previous, current []SecretBindingRequirement) map[string]string {
	type location struct {
		path       string
		occurrence int
	}
	currentIDs := make(map[string]struct{}, len(current))
	byLocation := make(map[location][]SecretBindingRequirement, len(current))
	for _, requirement := range current {
		currentIDs[requirement.ID] = struct{}{}
		key := location{requirement.Path, requirement.Occurrence}
		byLocation[key] = append(byLocation[key], requirement)
	}
	moves := make(map[string]string, len(previous))
	for _, requirement := range previous {
		if _, unchanged := currentIDs[requirement.ID]; unchanged {
			moves[requirement.ID] = requirement.ID
			continue
		}
		moves[requirement.ID] = ""
		if requirement.Path == "" {
			continue
		}
		newPath, removed := excludedImportPointerMove(exclusion, requirement.Path)
		if removed {
			continue
		}
		candidates := byLocation[location{newPath, requirement.Occurrence}]
		if len(candidates) != 1 {
			continue
		}
		match := candidates[0]
		if match.ScriptIndex != requirement.ScriptIndex || match.ScriptID != requirement.ScriptID ||
			match.ScriptName != requirement.ScriptName || match.Label != requirement.Label ||
			match.Context != requirement.Context ||
			allocatedSecretNameBase(match.SuggestedName) != allocatedSecretNameBase(requirement.SuggestedName) {
			continue
		}
		moves[requirement.ID] = match.ID
	}
	return moves
}

// allocatedSecretNameBase strips the "_2", "_3", ... suffix the suggestion
// registry adds to de-duplicate colliding suggested names. Suffixes follow
// traversal order, so removing an earlier action renumbers every later
// collision; a moved requirement keeps its base name but not its suffix.
func allocatedSecretNameBase(name string) string {
	underscore := strings.LastIndexByte(name, '_')
	if underscore <= 0 || underscore == len(name)-1 {
		return name
	}
	for _, digit := range name[underscore+1:] {
		if digit < '0' || digit > '9' {
			return name
		}
	}
	return name[:underscore]
}

// excludedImportPointerMove returns where a JSON pointer into the draft
// ("/actions/7/headers/Authorization") points after the exclusion, and
// whether the action holding it was removed. A pointer outside every visited
// action is unchanged.
func excludedImportPointerMove(exclusion *importActionExclusion, pointer string) (string, bool) {
	if exclusion == nil || !strings.HasPrefix(pointer, "/") {
		return pointer, false
	}
	tokens := strings.Split(pointer[1:], "/")
	var walk strings.Builder
	matchedTokens := -1
	matched := ""
	for index, token := range tokens {
		if index == 0 {
			walk.WriteString(token)
			continue
		}
		if isDecimalPointerToken(token) {
			walk.WriteString("[" + token + "]")
			if movedTo, visited := exclusion.paths[walk.String()]; visited {
				matchedTokens = index + 1
				matched = movedTo
			}
			continue
		}
		walk.WriteString("." + token)
	}
	if matchedTokens < 0 {
		return pointer, false
	}
	if matched == "" {
		return "", true
	}
	// Action paths are built only from section names, field names and
	// indices, so each '.', '[' and ']' is a separator.
	prefix := "/" + strings.NewReplacer("[", "/", "]", "", ".", "/").Replace(matched)
	if matchedTokens == len(tokens) {
		return prefix, false
	}
	return prefix + "/" + strings.Join(tokens[matchedTokens:], "/"), false
}

// dropAddedImportFidelityReport removes the fidelity report a readiness
// evaluation added to draft when prior, the draft before the edit, had none,
// along with any import metadata the evaluation created only to hold it.
func dropAddedImportFidelityReport(draft, prior *dsl.Script) {
	if draft == nil || draft.Metadata == nil || draft.Metadata.Import == nil {
		return
	}
	if prior != nil && prior.Metadata != nil && prior.Metadata.Import != nil {
		if _, had := prior.Metadata.Import.Preserved["fidelity_report"]; had {
			return
		}
	}
	delete(draft.Metadata.Import.Preserved, "fidelity_report")
	switch {
	case prior == nil || prior.Metadata == nil:
		if len(draft.Metadata.Import.Preserved) == 0 {
			draft.Metadata.Import = nil
		}
	case prior.Metadata.Import == nil:
		if len(draft.Metadata.Import.Preserved) == 0 {
			draft.Metadata.Import = nil
		}
	case prior.Metadata.Import.Preserved == nil && len(draft.Metadata.Import.Preserved) == 0:
		draft.Metadata.Import.Preserved = nil
	}
}

// importActionExclusion removes execution-blocked actions from a draft and
// records where every surviving action moved.
type importActionExclusion struct {
	uids             map[string]struct{}
	unexportedBodies bool
	removed          int
	// paths maps the old path of each visited action ("actions[5]",
	// "actions[2].params.requests[1]") to its new path, or to "" when it
	// was removed, so position-keyed references can follow it.
	paths map[string]string
}

// excludedActionNode is a surviving action: its index before the exclusion
// and the nested lists it holds, whose own positions may have changed.
type excludedActionNode struct {
	oldIndex int
	lists    []excludedActionList
}

// excludedActionList is a nested action list of a surviving action, by its
// path relative to that action before and after the exclusion (a control
// case can move when an emptied case before it is dropped).
type excludedActionList struct {
	oldPath, newPath string
	kept             []excludedActionNode
}

func excludeBlockedImportActionsFromScript(script *dsl.Script, uidSet map[string]struct{}, excludeUnexportedBodies bool) *importActionExclusion {
	exclusion := &importActionExclusion{uids: uidSet, unexportedBodies: excludeUnexportedBodies, paths: map[string]string{}}
	if script == nil {
		return exclusion
	}
	for _, section := range []struct {
		name    string
		actions *[]dsl.Action
	}{{"init", &script.Init}, {"actions", &script.Actions}, {"teardown", &script.Teardown}} {
		var kept []excludedActionNode
		*section.actions, kept = exclusion.filterActions(*section.actions, section.name)
		exclusion.assignPaths(section.name, section.name, kept)
	}
	return exclusion
}

// filterActions drops the excluded actions of one list, whose path before the
// exclusion is listPath, and returns the survivors with their old positions.
// A container is dropped only when the exclusion emptied it, never because it
// was empty already.
func (x *importActionExclusion) filterActions(actions []dsl.Action, listPath string) ([]dsl.Action, []excludedActionNode) {
	if len(actions) == 0 {
		return actions, nil
	}
	removedBefore := x.removed
	out := make([]dsl.Action, 0, len(actions))
	nodes := make([]excludedActionNode, 0, len(actions))
	for index, action := range actions {
		path := fmt.Sprintf("%s[%d]", listPath, index)
		if x.excludes(action.Metadata) {
			x.exclude(path)
			continue
		}
		node := excludedActionNode{oldIndex: index}
		actionRemovedBefore := x.removed
		if action.Params != nil {
			if rawReqs, hasReqs := action.Params["requests"]; hasReqs {
				filteredReqs, kept, handled := x.filterSequenceRequests(rawReqs, path+".params.requests")
				if handled && x.removed > actionRemovedBefore {
					if len(kept) == 0 {
						x.drop(path)
						continue
					}
					paramsCopy := make(map[string]any, len(action.Params))
					for k, v := range action.Params {
						paramsCopy[k] = v
					}
					paramsCopy["requests"] = filteredReqs
					action.Params = paramsCopy
				}
				if handled {
					node.lists = append(node.lists, excludedActionList{oldPath: "params.requests", newPath: "params.requests", kept: kept})
				}
			}
		}
		if action.Control != nil {
			ctrl := *action.Control
			controlRemovedBefore := x.removed
			for _, branch := range []struct {
				name    string
				actions *[]dsl.Action
			}{{"steps", &ctrl.Steps}, {"else", &ctrl.Else}, {"default", &ctrl.Default}} {
				var kept []excludedActionNode
				*branch.actions, kept = x.filterActions(*branch.actions, path+".control."+branch.name)
				node.lists = append(node.lists, excludedActionList{oldPath: "control." + branch.name, newPath: "control." + branch.name, kept: kept})
			}
			if len(ctrl.Cases) > 0 {
				cases := make([]dsl.SwitchCase, 0, len(ctrl.Cases))
				for caseIndex, switchCase := range ctrl.Cases {
					oldPath := fmt.Sprintf("control.cases[%d].steps", caseIndex)
					caseRemovedBefore := x.removed
					var kept []excludedActionNode
					switchCase.Steps, kept = x.filterActions(switchCase.Steps, path+"."+oldPath)
					if len(switchCase.Steps) == 0 && x.removed > caseRemovedBefore {
						continue
					}
					node.lists = append(node.lists, excludedActionList{oldPath: oldPath, newPath: fmt.Sprintf("control.cases[%d].steps", len(cases)), kept: kept})
					cases = append(cases, switchCase)
				}
				ctrl.Cases = cases
			}
			if len(ctrl.Branches) > 0 {
				branches := make([]dsl.ParallelBranch, 0, len(ctrl.Branches))
				for branchIndex, branch := range ctrl.Branches {
					oldPath := fmt.Sprintf("control.branches[%d].steps", branchIndex)
					branchRemovedBefore := x.removed
					var kept []excludedActionNode
					branch.Steps, kept = x.filterActions(branch.Steps, path+"."+oldPath)
					if len(branch.Steps) == 0 && x.removed > branchRemovedBefore {
						continue
					}
					node.lists = append(node.lists, excludedActionList{oldPath: oldPath, newPath: fmt.Sprintf("control.branches[%d].steps", len(branches)), kept: kept})
					branches = append(branches, branch)
				}
				ctrl.Branches = branches
			}
			action.Control = &ctrl
			if x.removed > controlRemovedBefore {
				emptied := false
				switch strings.ToLower(strings.TrimSpace(action.Type)) {
				case "control_if", "control_loop":
					emptied = len(ctrl.Steps) == 0 && len(ctrl.Else) == 0
				case "control_switch":
					emptied = len(ctrl.Cases) == 0 && len(ctrl.Default) == 0
				case "control_parallel":
					emptied = len(ctrl.Branches) == 0
				}
				if emptied {
					x.drop(path)
					continue
				}
			}
		}
		out = append(out, action)
		nodes = append(nodes, node)
	}
	if x.removed > removedBefore {
		out, nodes = x.pruneEmptyTransactionMarkers(out, nodes, listPath)
	}
	return out, nodes
}

// pruneEmptyTransactionMarkers drops start/end transaction marker pairs the
// exclusion left with nothing between them. A pair that was already adjacent
// before the exclusion (an intentional timing marker) is kept: like a
// container, it is dropped only when the exclusion emptied it.
func (x *importActionExclusion) pruneEmptyTransactionMarkers(actions []dsl.Action, nodes []excludedActionNode, listPath string) ([]dsl.Action, []excludedActionNode) {
	for len(actions) >= 2 {
		changed := false
		next := make([]dsl.Action, 0, len(actions))
		nextNodes := make([]excludedActionNode, 0, len(nodes))
		for i := 0; i < len(actions); i++ {
			if i+1 < len(actions) &&
				isTransactionMarkerFunc(actions[i], "lr_start_transaction") &&
				isTransactionMarkerFunc(actions[i+1], "lr_end_transaction") &&
				dsl.TransactionMarkerName(actions[i]) == dsl.TransactionMarkerName(actions[i+1]) &&
				nodes[i+1].oldIndex-nodes[i].oldIndex > 1 {
				changed = true
				x.drop(fmt.Sprintf("%s[%d]", listPath, nodes[i].oldIndex))
				x.drop(fmt.Sprintf("%s[%d]", listPath, nodes[i+1].oldIndex))
				i++
				continue
			}
			next = append(next, actions[i])
			nextNodes = append(nextNodes, nodes[i])
		}
		actions, nodes = next, nextNodes
		if !changed {
			break
		}
	}
	return actions, nodes
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

// filterSequenceRequests filters an http_sequence's requests, in whichever
// shape the draft holds them. handled is false for a shape it cannot read,
// which it leaves untouched.
func (x *importActionExclusion) filterSequenceRequests(raw any, listPath string) (filtered any, kept []excludedActionNode, handled bool) {
	switch typed := raw.(type) {
	case []dsl.Action:
		filtered, kept := x.filterActions(typed, listPath)
		return filtered, kept, true
	case []map[string]any:
		out := make([]map[string]any, 0, len(typed))
		for index, item := range typed {
			meta, _ := item["metadata"].(map[string]any)
			if x.excludes(meta) {
				x.exclude(fmt.Sprintf("%s[%d]", listPath, index))
				continue
			}
			out = append(out, item)
			kept = append(kept, excludedActionNode{oldIndex: index})
		}
		return out, kept, true
	case []any:
		out := make([]any, 0, len(typed))
		for index, elem := range typed {
			var meta map[string]any
			switch item := elem.(type) {
			case dsl.Action:
				meta = item.Metadata
			case map[string]any:
				meta, _ = item["metadata"].(map[string]any)
			}
			if x.excludes(meta) {
				x.exclude(fmt.Sprintf("%s[%d]", listPath, index))
				continue
			}
			out = append(out, elem)
			kept = append(kept, excludedActionNode{oldIndex: index})
		}
		return out, kept, true
	default:
		return raw, nil, false
	}
}

// excludes reports whether an action is removed: it carries an execution
// blocker (the plural execution_blockers or the legacy singular
// execution_blocker, as readiness reads them) and is either named by UID or,
// when unexported bodies are excluded, gated for its missing body.
func (x *importActionExclusion) excludes(meta map[string]any) bool {
	codes := executionReviewBlockerCodes(meta)
	if len(codes) == 0 {
		return false
	}
	if x.unexportedBodies && slices.Contains(codes, sample.HARRequestBodyNotExportedCode) {
		return true
	}
	if len(x.uids) > 0 {
		if uid, ok := meta["import_action_uid"].(string); ok {
			if _, matched := x.uids[strings.TrimSpace(uid)]; matched {
				return true
			}
		}
	}
	return false
}

// drop records that the action at path (its path before the exclusion) is
// gone, whether excluded or pruned as an emptied container or marker pair.
func (x *importActionExclusion) drop(path string) {
	x.paths[path] = ""
}

// exclude counts and records an excluded action.
func (x *importActionExclusion) exclude(path string) {
	x.removed++
	x.drop(path)
}

// assignPaths records the new path of every surviving action under a list
// whose path is oldList before the exclusion and newList after it.
func (x *importActionExclusion) assignPaths(oldList, newList string, nodes []excludedActionNode) {
	for position, node := range nodes {
		oldPath := fmt.Sprintf("%s[%d]", oldList, node.oldIndex)
		newPath := fmt.Sprintf("%s[%d]", newList, position)
		x.paths[oldPath] = newPath
		for _, list := range node.lists {
			x.assignPaths(oldPath+"."+list.oldPath, newPath+"."+list.newPath, list.kept)
		}
	}
}

// remapPath returns where path (a target path such as
// "actions[7].extractors[0]") points after the exclusion, and whether what
// it named was removed. A path outside every visited action is unchanged.
func (x *importActionExclusion) remapPath(path string) (string, bool) {
	for end := len(path) - 1; end >= 0; end-- {
		if path[end] != ']' {
			continue
		}
		if end+1 < len(path) && path[end+1] != '.' && path[end+1] != '[' {
			continue
		}
		moved, visited := x.paths[path[:end+1]]
		if !visited {
			continue
		}
		if moved == "" {
			return "", true
		}
		return moved + path[end+1:], false
	}
	return path, false
}

// excludedSourceMappingNote explains a source mapping whose generated action
// the exclusion removed.
const excludedSourceMappingNote = "The generated action was excluded from the import draft."

// remapExcludedSourceReviewMappings points the draft's source mappings at
// where their actions moved. A mapping whose action was removed keeps its
// source range but no longer targets the draft.
func remapExcludedSourceReviewMappings(script *dsl.Script, exclusion *importActionExclusion) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved == nil {
		return
	}
	remap := func(targetPath string) (string, bool, bool) {
		if strings.TrimSpace(targetPath) == "" {
			return targetPath, false, false
		}
		moved, removed := exclusion.remapPath(strings.TrimSpace(targetPath))
		return moved, removed, removed || moved != strings.TrimSpace(targetPath)
	}
	if review := scriptSourceReviewForUpdate(script); review != nil {
		for index := range review.Mappings {
			mapping := &review.Mappings[index]
			moved, removed, changed := remap(mapping.TargetPath)
			if !changed {
				continue
			}
			mapping.TargetPath = moved
			if removed {
				mapping.TargetKind = ""
				mapping.Status = "review_only"
				mapping.Notes = excludedSourceMappingNote
			}
		}
		return
	}
	review, _ := script.Metadata.Import.Preserved["source_review"].(map[string]any)
	mappings, _ := review["mappings"].([]any)
	for _, raw := range mappings {
		mapping, _ := raw.(map[string]any)
		targetPath, _ := mapping["target_path"].(string)
		moved, removed, changed := remap(targetPath)
		if !changed {
			continue
		}
		if removed {
			delete(mapping, "target_path")
			delete(mapping, "target_kind")
			mapping["status"] = "review_only"
			mapping["notes"] = excludedSourceMappingNote
			continue
		}
		mapping["target_path"] = moved
	}
}

func isDecimalPointerToken(token string) bool {
	if token == "" {
		return false
	}
	for _, r := range token {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
