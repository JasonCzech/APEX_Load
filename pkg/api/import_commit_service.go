package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/importsession"
)

// importSessionCapacityRetryAfterSeconds is the hint sent with a 429 when
// protected import session capacity is exhausted. Sessions are short-lived, so
// a short retry is usually enough.
const importSessionCapacityRetryAfterSeconds = 5

// importSessionClaimStatus maps a protected-import-session claim failure to its
// HTTP status. It is shared by every format that uses the commit service, so
// the classification is a cross-importer contract rather than a per-handler
// decision.
//
// Capacity exhaustion is transient and leaves the caller's session intact, so
// it must not read as an internal error — that told operators to file a bug
// when retrying was the correct response. Missing, expired, out-of-scope, and
// deleted-project failures deliberately collapse to one 404 so a caller cannot
// probe which of those applies.
func importSessionClaimStatus(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, importsession.ErrSessionClaimed):
		return http.StatusConflict
	case errors.Is(err, importsession.ErrSessionCapacity):
		return http.StatusTooManyRequests
	case errors.Is(err, os.ErrNotExist),
		errors.Is(err, importsession.ErrSessionExpired),
		errors.Is(err, importsession.ErrSessionScope),
		errors.Is(err, importsession.ErrProjectDeleted):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

// importCommitSpec contains the source-specific behavior for the otherwise
// identical create/replace workflow used by structured web API importers.
// Keeping this internal preserves every existing route and wire contract while
// ensuring binding, validation, preflight, persistence, and audit behavior do
// not drift between formats.
type importCommitSpec struct {
	sourceProfile         string
	auditProfile          string
	displayName           string
	allowedReplaceModes   []string
	actionKey             func(dsl.Action) string
	diff                  func(existing *dsl.Script, imported *dsl.Script, replaceMode string, actionKey func(dsl.Action) string) openAPIImportDiff
	merge                 func(existing *dsl.Script, imported *dsl.Script, actionKey func(dsl.Action) string) *dsl.Script
	mapCompatibleResponse func(map[string]any) map[string]any
}

// sharedImportCommitRequest adds protected-candidate-only overrides without
// changing any existing required request fields. The embedded compatibility
// request retains the established OpenAPI/Postman/AsyncAPI wire contract.
type sharedImportCommitRequest struct {
	openAPIImportCommitRequest
	SecretBindings      map[string]string `json:"secret_bindings,omitempty"`
	ParameterizationIDs []string          `json:"parameterization_selected_ids,omitempty"`
}

func (spec importCommitSpec) validate() error {
	if strings.TrimSpace(spec.sourceProfile) == "" ||
		strings.TrimSpace(spec.auditProfile) == "" ||
		strings.TrimSpace(spec.displayName) == "" {
		return errors.New("import commit source, audit profile, and display name are required")
	}
	if len(spec.allowedReplaceModes) == 0 ||
		spec.actionKey == nil ||
		spec.diff == nil ||
		spec.merge == nil ||
		spec.mapCompatibleResponse == nil {
		return fmt.Errorf("%s import commit specification is incomplete", spec.displayName)
	}
	seenModes := make(map[string]struct{}, len(spec.allowedReplaceModes))
	for _, rawMode := range spec.allowedReplaceModes {
		mode := strings.ToLower(strings.TrimSpace(rawMode))
		if mode == "" {
			return fmt.Errorf("%s import commit replace modes contain an empty value", spec.displayName)
		}
		if _, duplicate := seenModes[mode]; duplicate {
			return fmt.Errorf("%s import commit replace modes contain duplicate %q", spec.displayName, mode)
		}
		seenModes[mode] = struct{}{}
	}
	if _, ok := seenModes["full"]; !ok {
		return fmt.Errorf("%s import commit replace modes must include full", spec.displayName)
	}
	return nil
}

func normalizeImportCommitModes(req openAPIImportCommitRequest, spec importCommitSpec) (string, string, error) {
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "create"
	}
	replaceMode := strings.ToLower(strings.TrimSpace(req.ReplaceMode))
	if replaceMode == "" {
		replaceMode = "full"
	}
	if mode != "replace" && strings.TrimSpace(req.TargetScriptID) != "" {
		mode = "replace"
	}
	if mode != "create" && mode != "replace" {
		return "", "", errors.New("mode must be create or replace")
	}
	allowed := make([]string, 0, len(spec.allowedReplaceModes))
	allowedSet := make(map[string]struct{}, len(spec.allowedReplaceModes))
	for _, rawMode := range spec.allowedReplaceModes {
		mode := strings.ToLower(strings.TrimSpace(rawMode))
		allowed = append(allowed, mode)
		allowedSet[mode] = struct{}{}
	}
	if _, ok := allowedSet[replaceMode]; !ok {
		return "", "", fmt.Errorf("replace_mode must be %s", strings.Join(allowed, " or "))
	}
	return mode, replaceMode, nil
}

func (spec importCommitSpec) writeResponse(w http.ResponseWriter, status int, payload map[string]any) {
	writeJSON(w, status, spec.mapCompatibleResponse(payload))
}

func protectedImportCreateScriptID(profile, sessionID string) string {
	identity := "apexload:protected-import-create:v1:" +
		strings.ToLower(strings.TrimSpace(profile)) + ":" +
		strings.TrimSpace(sessionID)
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(identity)).String()
}

func (s *Server) applyProtectedImportFolderOverride(candidate *dsl.Script, override *string) (int, error) {
	if candidate == nil || override == nil {
		return 0, nil
	}
	folderID := strings.TrimSpace(*override)
	if folderID == "" {
		candidate.FolderID = nil
		return 0, nil
	}
	if s == nil || s.store == nil {
		return http.StatusServiceUnavailable, errors.New("script folder storage is unavailable")
	}
	folder, err := s.store.GetFolder(folderID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return http.StatusNotFound, errors.New("script folder is unavailable for this project")
		}
		return http.StatusInternalServerError, errors.New("failed to validate script folder")
	}
	if folder == nil || normalizeProjectID(folder.ProjectID) != normalizeProjectID(candidate.ProjectID) {
		return http.StatusNotFound, errors.New("script folder is unavailable for this project")
	}
	candidate.FolderID = &folderID
	return 0, nil
}

func finalizeImportCommitScript(script *dsl.Script, sourceProfile string) (*dsl.Script, *ReplayPreflight, error) {
	finalized, err := requestimport.FinalizeScript(script, nil, requestimport.FinalizeOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to finalize imported script: %w", err)
	}
	script = finalized.Script
	preflight := buildReplayPreflight(script, nil, nil, validationWarnings(script))
	attachImportReplayPreflight(script, sourceProfile, preflight)
	return script, preflight, nil
}

// applySuggestedImportCommitSecretBindings keeps every request body on the
// shared byte-preserving credential path. maskScriptSecrets is intentionally
// broad because it also projects arbitrary metadata and free text; it must not
// reinterpret JSON, XML, form, multipart, GraphQL, encoded, or raw wire bytes.
//
// Structured projectors rewrite only exact credential occurrences. Unsafe
// typed/mixed content is rejected or retained as a disclosure-safe blocked
// draft, while unrecognized raw/file bytes remain byte-identical.
func applySuggestedImportCommitSecretBindings(
	script *dsl.Script,
	scriptIndex int,
	sourceProfile string,
) (*dsl.Script, []SecretBindingRequirement, error) {
	if script == nil {
		return nil, nil, errors.New("script is required")
	}
	working := cloneScriptDefinition(script)
	if working == nil {
		return nil, nil, errors.New("failed to clone import script")
	}

	dynamicOnly := cloneScriptDefinition(working)
	if dynamicOnly == nil {
		return nil, nil, errors.New("failed to analyze import script variables")
	}
	// Script parameters are static import material. Only a prior extractor or
	// OAuth output proves that a {{var.*}} credential is dynamic at this action.
	dynamicOnly.Parameters = nil
	dynamicReadiness := requestimport.AnalyzeVariableReadiness(dynamicOnly, nil)

	reservedNames, err := engine.ReferencedSecretNames(working)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect existing secret references: %w", err)
	}
	registry := credentialpolicy.NewNameRegistry(reservedNames...)
	protectedBodies := make(map[string]string)
	if err := dslwalk.Walk(working, func(ref dslwalk.SurfaceRef) error {
		body, ok, err := decodeImportCommitProjectionBody(ref)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		options := credentialpolicy.Options{
			Mode:        credentialpolicy.SecretReference,
			Source:      credentialpolicy.SourceRef{Format: sourceProfile, Path: ref.Path, Pointer: "/body"},
			RequestKey:  ref.Path,
			NamePrefix:  firstNonEmpty(ref.Name(), "request"),
			Registry:    registry,
			ProvenVars:  importCommitProvenBodyVariables(body.text(), ref.Path, dynamicReadiness),
			TargetField: "body",
		}
		projected, result, err := credentialpolicy.ProjectRequestBody(
			body.wire,
			importCommitSurfaceContentType(ref),
			options,
		)
		for index := range result.Requirements {
			result.Requirements[index].Value = ""
		}
		if err != nil {
			return fmt.Errorf("project request body at %s: %w", ref.Path, err)
		}
		if result.Disposition == credentialpolicy.Rejected || projected == nil {
			codes := importCommitCredentialFindingCodes(result, credentialpolicy.Rejected)
			if len(codes) == 0 {
				codes = []string{"credential_body_projection_rejected"}
			}
			return fmt.Errorf("request body at %s was rejected: %s", ref.Path, strings.Join(codes, ", "))
		}
		if result.Disposition == credentialpolicy.BlockedDraft {
			applyImportCommitBodyReviewGate(ref, sourceProfile, result)
		}
		protectedBodies[ref.Path] = body.encode(projected)
		// Keep the body out of the generic free-text projection. It is restored
		// by stable traversal path after every other executable field is masked
		// and auto-bound through the existing compatibility workflow.
		setImportCommitSurfaceBody(ref, "")
		return nil
	}); err != nil {
		return nil, nil, err
	}

	masked, requirements, err := applySuggestedSecretBindings(working, scriptIndex)
	if err != nil {
		return nil, requirements, err
	}
	if len(protectedBodies) == 0 {
		return masked, requirements, nil
	}
	restored := 0
	if err := dslwalk.Walk(masked, func(ref dslwalk.SurfaceRef) error {
		body, ok := protectedBodies[ref.Path]
		if !ok {
			return nil
		}
		setImportCommitSurfaceBody(ref, body)
		restored++
		return nil
	}); err != nil {
		return nil, requirements, err
	}
	if restored != len(protectedBodies) {
		return nil, requirements, errors.New("request body projection lost an action surface")
	}
	return masked, requirements, nil
}

func importCommitSurfaceBody(ref dslwalk.SurfaceRef) (string, bool) {
	if ref.Action != nil {
		return ref.Action.Body, strings.TrimSpace(ref.Action.Body) != ""
	}
	if ref.Request == nil {
		return "", false
	}
	body, ok := ref.Request["body"].(string)
	return body, ok && strings.TrimSpace(body) != ""
}

func setImportCommitSurfaceBody(ref dslwalk.SurfaceRef, body string) {
	if ref.Action != nil {
		ref.Action.Body = body
		return
	}
	if ref.Request != nil {
		ref.Request["body"] = body
	}
}

func importCommitSurfaceContentType(ref dslwalk.SurfaceRef) string {
	if ref.Action != nil {
		if len(ref.Action.HeaderList) > 0 {
			value := ""
			for _, header := range ref.Action.HeaderList {
				if strings.EqualFold(strings.TrimSpace(header.Name), "Content-Type") {
					value = header.Value
				}
			}
			return value
		}
		return importCommitHeaderMapContentType(ref.Action.Headers)
	}
	if ref.Request == nil {
		return ""
	}
	if raw, exists := ref.Request["header_list"]; exists {
		payload, err := json.Marshal(raw)
		if err == nil {
			var headers dsl.HeaderList
			if json.Unmarshal(payload, &headers) == nil && len(headers) > 0 {
				value := ""
				for _, header := range headers {
					if strings.EqualFold(strings.TrimSpace(header.Name), "Content-Type") {
						value = header.Value
					}
				}
				return value
			}
		}
	}
	raw, exists := ref.Request["headers"]
	if !exists {
		return ""
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return ""
	}
	var headers map[string]string
	if json.Unmarshal(payload, &headers) != nil {
		return ""
	}
	return importCommitHeaderMapContentType(headers)
}

func importCommitHeaderMapContentType(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	value := ""
	for _, key := range keys {
		if strings.EqualFold(strings.TrimSpace(key), "Content-Type") {
			value = headers[key]
		}
	}
	return value
}

func importCommitProvenBodyVariables(
	body string,
	actionPath string,
	readiness requestimport.VariableReadiness,
) map[string]struct{} {
	references := portableScriptTemplateReferences(body)
	if len(references) == 0 {
		return nil
	}
	missing := make(map[string]struct{}, len(readiness.ByAction[actionPath]))
	for _, name := range readiness.ByAction[actionPath] {
		missing[name] = struct{}{}
	}
	proven := make(map[string]struct{})
	for _, reference := range references {
		expression := strings.TrimSpace(reference[2 : len(reference)-2])
		name, ok := strings.CutPrefix(expression, "var.")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			continue
		}
		if _, unavailable := missing[name]; unavailable {
			continue
		}
		proven[name] = struct{}{}
	}
	if len(proven) == 0 {
		return nil
	}
	return proven
}

func importCommitCredentialFindingCodes(
	result credentialpolicy.Result,
	disposition credentialpolicy.Disposition,
) []string {
	var codes []string
	for _, finding := range result.Findings {
		if finding.Disposition == disposition && strings.TrimSpace(finding.Code) != "" {
			codes = append(codes, finding.Code)
		}
	}
	sort.Strings(codes)
	return codes
}

func applyImportCommitBodyReviewGate(
	ref dslwalk.SurfaceRef,
	sourceProfile string,
	result credentialpolicy.Result,
) {
	codes := importCommitCredentialFindingCodes(result, credentialpolicy.BlockedDraft)
	if len(codes) == 0 {
		codes = []string{"credential_body_review_required"}
	}
	findings := make([]importreview.Finding, 0, len(codes))
	for _, code := range codes {
		findings = append(findings, importreview.Finding{
			Code:       "import_review_required",
			Severity:   diag.Blocker,
			Element:    firstNonEmpty(ref.Name(), "request body"),
			Detail:     "the imported request body requires review before execution",
			Location:   ref.Path + ".body",
			ActionPath: ref.Path,
			Source:     sourceProfile,
			ReasonCode: code,
			SourceRef:  ref.Path + ".body",
			Resolution: "replace_behavior_and_remove_gate",
			Gate:       importreview.GateExecution,
		})
	}
	if ref.Action != nil {
		importreview.ApplyToAction(ref.Action, findings)
		return
	}
	action := dsl.Action{Name: ref.Name(), Metadata: ref.Metadata()}
	importreview.ApplyToAction(&action, findings)
	ref.SetMetadata(action.Metadata)
}

// OpenAPI-Responses: 200, 201
func (s *Server) handleImportCommit(w http.ResponseWriter, r *http.Request, spec importCommitSpec) {
	if err := spec.validate(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var sharedReq sharedImportCommitRequest
	req := &sharedReq.openAPIImportCommitRequest
	if !decodeJSONBody(w, r, &sharedReq) {
		return
	}
	script := &req.Script
	var candidateClaim *importsession.Claim
	var candidateLease *importCandidateClaimLease
	var candidateRequirements []credentialpolicy.Requirement
	defer func() {
		clearCredentialRequirementValues(candidateRequirements)
		if candidateClaim != nil {
			s.releaseImportCandidateClaim(candidateClaim)
		}
	}()
	if strings.TrimSpace(req.ImportSessionID) != "" {
		projectID := strings.TrimSpace(req.ProjectID)
		if projectID == "" {
			projectID = strings.TrimSpace(req.Script.ProjectID)
		}
		if authorized, scoped := authorizedProjectFromRequest(r); scoped {
			projectID = authorized
		}
		protectedCandidate, claim, err := s.claimProtectedImportCandidateSession(
			r,
			spec.sourceProfile,
			req.ImportSessionID,
			projectID,
		)
		if err != nil {
			// The status literals are repeated here rather than passed through
			// from importSessionClaimStatus because cmd/swaggergen infers each
			// route's documented responses from the writeError call sites.
			switch importSessionClaimStatus(err) {
			case http.StatusConflict:
				writeError(w, http.StatusConflict, "import session is already being committed")
			case http.StatusTooManyRequests:
				w.Header().Set("Retry-After", strconv.Itoa(importSessionCapacityRetryAfterSeconds))
				writeError(w, http.StatusTooManyRequests, "protected import session capacity is busy; retry shortly")
			case http.StatusNotFound:
				writeError(w, http.StatusNotFound, "import session not found or expired")
			default:
				slog.Error("failed to claim protected import session", "source", spec.sourceProfile, "error", err)
				writeError(w, http.StatusInternalServerError, "failed to open protected import session")
			}
			return
		}
		candidate := protectedCandidate.Script
		candidateRequirements = protectedCandidate.CredentialRequirements
		protectedCandidate.CredentialRequirements = nil
		candidateClaim = claim
		candidateLease = s.startImportCandidateClaimLease(claim)
		defer candidateLease.stop()
		if req.Name != nil {
			candidate.Name = strings.TrimSpace(*req.Name)
		}
		if status, err := s.applyProtectedImportFolderOverride(candidate, req.FolderID); err != nil {
			writeError(w, status, err.Error())
			return
		}
		candidate, err = applyProtectedImportCandidateOverrides(candidate, candidateRequirements, sharedReq)
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		script = candidate
	}
	safeScript, _, err := applySuggestedImportCommitSecretBindings(script, 0, spec.sourceProfile)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to prepare safe secret references: "+err.Error())
		return
	}
	script = safeScript
	stripProtectedImportCredentialMetadata(script)

	mode, replaceMode, err := normalizeImportCommitModes(*req, spec)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if mode == "create" {
		if candidateClaim != nil {
			// The protected session is the durable create identity. If the script
			// write commits but claim completion is interrupted, a recovered claim
			// must address the same create-only row instead of allocating a second
			// script ID.
			script.ID = protectedImportCreateScriptID(spec.sourceProfile, candidateClaim.ID)
		}
		script, preflight, err := finalizeImportCommitScript(script, spec.sourceProfile)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		diff := openAPIImportDiff{Mode: "create", Added: len(script.Actions)}
		if req.DryRun {
			spec.writeResponse(w, http.StatusOK, map[string]any{"dry_run": true, "diff": diff, "preflight": preflight})
			return
		}
		if candidateLease != nil {
			if err := candidateLease.assertOwned(); err != nil {
				writeError(w, http.StatusConflict, "import session claim was lost before commit")
				return
			}
		}
		stored, err := s.runner.CreateScriptDurable(script)
		if err != nil {
			if errors.Is(err, errScriptConcurrentChange) {
				writeError(w, http.StatusConflict, err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to save imported script: "+err.Error())
			return
		}
		if candidateClaim != nil {
			candidateLease.stop()
			if err := s.completeImportCandidateClaim(r.Context(), candidateClaim); err != nil {
				slog.Error("failed to finalize committed import session", "source", spec.sourceProfile, "session_id", req.ImportSessionID, "error", err)
			}
			candidateClaim = nil
		}
		s.recordAuditEvent(r, "script.import."+spec.auditProfile+".create", "success", "script", stored.ID, stored.ProjectID, map[string]any{"name": stored.Name})
		spec.writeResponse(w, http.StatusCreated, map[string]any{
			"scripts":       maskStoredScripts([]*StoredScript{stored}),
			"script":        maskScriptSecrets(stored.Script),
			"stored_script": maskStoredScript(stored),
			"replaced":      false,
			"dry_run":       false,
			"diff":          diff,
			"preflight":     preflight,
		})
		return
	}

	targetID := strings.TrimSpace(req.TargetScriptID)
	if targetID == "" {
		targetID = strings.TrimSpace(script.ID)
	}
	if targetID == "" {
		writeError(w, http.StatusBadRequest, "target_script_id is required when replacing an existing "+spec.displayName+" import")
		return
	}
	existing, ok := s.runner.GetScript(targetID)
	if !ok || existing == nil || existing.Script == nil {
		writeError(w, http.StatusNotFound, "target script not found")
		return
	}
	if candidateClaim != nil &&
		normalizeProjectID(existing.ProjectID) != normalizeProjectID(script.ProjectID) {
		// A protected candidate is scoped to the project captured at
		// inspection time. Do not let replace-mode identity preservation
		// silently relocate that server-owned candidate into another project,
		// even for an otherwise cross-project-capable caller. Collapse the
		// mismatch with the ordinary missing-target response.
		writeError(w, http.StatusNotFound, "target script not found")
		return
	}
	if !requireAuthorizedProjectMatch(w, r, existing.ProjectID) {
		return
	}
	expectedUpdatedAt := existing.Script.UpdatedAt
	expectedActionGraphDigest, err := scriptCorrelationActionGraphDigest(existing.Script)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fingerprint target script")
		return
	}

	next := script
	diff := spec.diff(existing.Script, script, replaceMode, spec.actionKey)
	diff.Mode = "replace"
	diff.ReplaceMode = replaceMode
	if replaceMode != "full" {
		next = spec.merge(existing.Script, script, spec.actionKey)
	}
	// Re-imports may replace content but may never relocate a script or erase
	// its durable identity/creation metadata.
	preserveReplacedScriptIdentity(next, existing.Script)
	next, preflight, err := finalizeImportCommitScript(next, spec.sourceProfile)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.DryRun {
		spec.writeResponse(w, http.StatusOK, map[string]any{"dry_run": true, "replaced": true, "diff": diff, "preflight": preflight})
		return
	}

	if candidateLease != nil {
		if err := candidateLease.assertOwned(); err != nil {
			writeError(w, http.StatusConflict, "import session claim was lost before commit")
			return
		}
	}
	updated, ok, err := s.runner.UpdateScriptDurableInProjectIfUnchanged(
		targetID,
		next,
		authorizedProjectOr(r, existing.ProjectID),
		expectedUpdatedAt,
		expectedActionGraphDigest,
	)
	if err != nil {
		if errors.Is(err, errScriptConcurrentChange) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to save imported script: "+err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "target script not found")
		return
	}
	if candidateClaim != nil {
		candidateLease.stop()
		if err := s.completeImportCandidateClaim(r.Context(), candidateClaim); err != nil {
			slog.Error("failed to finalize committed import session", "source", spec.sourceProfile, "session_id", req.ImportSessionID, "error", err)
		}
		candidateClaim = nil
	}
	s.recordAuditEvent(r, "script.import."+spec.auditProfile+".replace", "success", "script", updated.ID, updated.ProjectID, map[string]any{
		"name":         updated.Name,
		"replace_mode": replaceMode,
	})
	spec.writeResponse(w, http.StatusOK, map[string]any{
		"scripts":       maskStoredScripts([]*StoredScript{updated}),
		"script":        maskScriptSecrets(updated.Script),
		"stored_script": maskStoredScript(updated),
		"replaced":      true,
		"dry_run":       false,
		"diff":          diff,
		"preflight":     preflight,
	})
}

func applyProtectedImportCandidateOverrides(
	candidate *dsl.Script,
	protectedRequirements []credentialpolicy.Requirement,
	req sharedImportCommitRequest,
) (*dsl.Script, error) {
	if candidate == nil {
		return nil, errors.New("protected import candidate is invalid")
	}
	if len(req.SecretBindings) > 0 {
		protectedIDs := make(map[string]struct{}, len(protectedRequirements))
		for _, requirement := range protectedRequirements {
			if _, ok := credential.ParseReference(strings.TrimSpace(requirement.Replacement)); ok {
				protectedIDs[requirement.ID] = struct{}{}
			}
		}
		protectedSelections := make([]secretBindingSelection, 0, len(req.SecretBindings))
		legacySelections := make([]secretBindingSelection, 0, len(req.SecretBindings))
		for requirementID, secretName := range req.SecretBindings {
			selection := secretBindingSelection{
				RequirementID: requirementID,
				SecretName:    secretName,
			}
			if _, protected := protectedIDs[requirementID]; protected {
				protectedSelections = append(protectedSelections, selection)
			} else {
				legacySelections = append(legacySelections, selection)
			}
		}
		sort.Slice(protectedSelections, func(i, j int) bool {
			return protectedSelections[i].RequirementID < protectedSelections[j].RequirementID
		})
		sort.Slice(legacySelections, func(i, j int) bool {
			return legacySelections[i].RequirementID < legacySelections[j].RequirementID
		})
		if len(protectedSelections) > 0 {
			bound, unexpectedLegacy, _, err := applyProtectedImportCredentialBindings(candidate, protectedSelections)
			if err != nil || len(unexpectedLegacy) != 0 {
				if err == nil {
					err = errors.New("protected requirement id is stale or invalid")
				}
				return nil, fmt.Errorf("protected import credential bindings are stale or invalid: %w", err)
			}
			candidate = bound
		}
		if len(legacySelections) > 0 {
			bound, _, err := applySecretBindings(candidate, 0, legacySelections)
			if err != nil {
				return nil, fmt.Errorf("protected import credential bindings are stale or invalid: %w", err)
			}
			candidate = bound
		}
	}
	stripProtectedImportCredentialMetadata(candidate)
	if len(req.ParameterizationIDs) == 0 {
		return candidate, nil
	}
	selected := make(map[string]bool, len(req.ParameterizationIDs))
	for _, rawID := range req.ParameterizationIDs {
		id := strings.TrimSpace(rawID)
		if id == "" || selected[id] {
			return nil, errors.New("protected import parameterization selections contain an empty or duplicate id")
		}
		selected[id] = true
	}
	trusted := buildTrustedImportParameterization(candidate)
	available := make(map[string]struct{}, len(trusted.Suggestions))
	for _, suggestion := range trusted.Suggestions {
		available[suggestion.ID] = struct{}{}
	}
	for id := range selected {
		if _, ok := available[id]; !ok {
			return nil, errors.New("protected import parameterization selections are stale or unavailable")
		}
	}
	applied, _, err := paramize.ApplySuggestions(candidate, trusted.Suggestions, selected)
	if err != nil {
		return nil, fmt.Errorf("apply protected import parameterization: %w", err)
	}
	// The shared commit boundary immediately projects the complete candidate.
	// Deferring that projection keeps JSON request bodies on its byte-preserving
	// structured path; the selected suggestions themselves contain only trusted
	// runtime templates and introduce no caller-provided secret values.
	return applied, nil
}
