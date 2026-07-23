package api

// Correlation rulesets: project-scoped, reusable catalogs of learned correlation
// rules. Rules are discovered from a script's recorded baseline (via
// pkg/convert/correlate) and applied to other scripts (via the import
// parameterization pass, pkg/convert/paramize). Rules are stored inline
// (single-table family, mirrors object_repository).

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	defaultCorrelationRulesetListLimit = 100
	// Each ruleset may carry up to 256 KiB of bounded rule JSON. Keep list
	// responses at the default page size even though the generic pagination
	// parser permits 1000 rows for smaller resource types.
	maxCorrelationRulesetListLimit = 100
)

// correlationRulesetUpdateRule is the editable, disclosure-safe rule shape
// accepted by the update endpoint. SampleValue is deliberately absent: learned
// literals are neither editable nor safe to accept back into durable storage.
// Keeping this type in the API package also lets the OpenAPI generator describe
// the nested rule contract instead of collapsing a storage-package selector to
// an opaque object.
type correlationRulesetUpdateRule struct {
	Name        string  `json:"name"`
	RuleID      string  `json:"rule_id,omitempty"`
	Source      string  `json:"source"`
	ExtractType string  `json:"extract_type"`
	Expression  string  `json:"expression"`
	Confidence  float64 `json:"confidence,omitempty"`
}

// correlationRulesetUpdateRequest exposes only editor-owned fields. ID,
// project ownership, source provenance, and timestamps other than the CAS
// precondition are resolved from the authoritative server-side row.
type correlationRulesetUpdateRequest struct {
	Name              string                         `json:"name"`
	Description       string                         `json:"description,omitempty"`
	Rules             []correlationRulesetUpdateRule `json:"rules"`
	ExpectedUpdatedAt time.Time                      `json:"expected_updated_at"`
}

type correlationRulesetResponse struct {
	ID          string                         `json:"id"`
	ProjectID   string                         `json:"project_id,omitempty"`
	Name        string                         `json:"name"`
	Description string                         `json:"description,omitempty"`
	Source      string                         `json:"source,omitempty"`
	Rules       []correlationRulesetUpdateRule `json:"rules"`
	CreatedAt   time.Time                      `json:"created_at"`
	UpdatedAt   time.Time                      `json:"updated_at"`
}

type correlationRulesetListResponse struct {
	CorrelationRulesets []correlationRulesetResponse `json:"correlation_rulesets"`
	Count               int                          `json:"count"`
	Limit               int                          `json:"limit"`
	Offset              int                          `json:"offset"`
}

type correlationRulesetApplyDiff struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}

type correlationRulesetApplyResponse struct {
	RulesetID    string                        `json:"ruleset_id"`
	ScriptID     string                        `json:"script_id"`
	MatchedRules int                           `json:"matched_rules"`
	Applied      bool                          `json:"applied"`
	Diffs        []correlationRulesetApplyDiff `json:"diffs"`
	PreviewToken string                        `json:"preview_token"`
}

type correlationRulesetDeleteResponse struct {
	Deleted string `json:"deleted"`
}

func (request correlationRulesetUpdateRequest) storageRules() []storage.CorrelationRule {
	rules := make([]storage.CorrelationRule, len(request.Rules))
	for index, rule := range request.Rules {
		rules[index] = storage.CorrelationRule{
			Name:        rule.Name,
			RuleID:      rule.RuleID,
			Source:      rule.Source,
			ExtractType: rule.ExtractType,
			Expression:  rule.Expression,
			Confidence:  rule.Confidence,
		}
	}
	return rules
}

func correlationRulesetAPIResponse(ruleset *storage.CorrelationRuleset) correlationRulesetResponse {
	if ruleset == nil {
		return correlationRulesetResponse{Rules: []correlationRulesetUpdateRule{}}
	}
	rules := make([]correlationRulesetUpdateRule, len(ruleset.Rules))
	for index, rule := range ruleset.Rules {
		rules[index] = correlationRulesetUpdateRule{
			Name:        rule.Name,
			RuleID:      rule.RuleID,
			Source:      rule.Source,
			ExtractType: rule.ExtractType,
			Expression:  rule.Expression,
			Confidence:  rule.Confidence,
		}
	}
	return correlationRulesetResponse{
		ID:          ruleset.ID,
		ProjectID:   ruleset.ProjectID,
		Name:        ruleset.Name,
		Description: ruleset.Description,
		Source:      ruleset.Source,
		Rules:       rules,
		CreatedAt:   ruleset.CreatedAt,
		UpdatedAt:   ruleset.UpdatedAt,
	}
}

func validateCorrelationRuleset(ruleset *storage.CorrelationRuleset) (int, string) {
	if strings.TrimSpace(ruleset.Name) == "" {
		return http.StatusBadRequest, "correlation ruleset name is required"
	}
	return 0, ""
}

// correlationRulesetWithoutSamples returns a detached ruleset projection with
// every learned literal removed. A rule is identified and matched by its
// extractor structure (rule id/source/type/expression); SampleValue is neither
// required for matching nor safe to persist or serialize because discovery
// runs against a decrypted recording baseline.
func correlationRulesetWithoutSamples(ruleset *storage.CorrelationRuleset) (*storage.CorrelationRuleset, bool) {
	if ruleset == nil {
		return nil, false
	}
	clone := *ruleset
	clone.Rules = append([]storage.CorrelationRule(nil), ruleset.Rules...)
	changed := false
	for i := range clone.Rules {
		if clone.Rules[i].SampleValue != "" {
			changed = true
			clone.Rules[i].SampleValue = ""
		}
	}
	if clone.Rules == nil {
		clone.Rules = []storage.CorrelationRule{}
	}
	return &clone, changed
}

// disclosureSafeCorrelationRuleset scrubs legacy sample literals in-memory for
// the response only. Read paths (GET/list) are viewer-scoped and must not
// perform durable writes: persisting here both bypassed the audit trail and
// could surface an updated_at no stored row carries (the CAS helper stamps the
// projection before the guarded UPDATE), causing spurious 409s on later
// optimistic updates. Storage startup repair and every real write scrub the
// durable representation independently.
func disclosureSafeCorrelationRuleset(ruleset *storage.CorrelationRuleset) *storage.CorrelationRuleset {
	safe, _ := correlationRulesetWithoutSamples(ruleset)
	return safe
}

func (s *Server) loadCorrelationRuleset(w http.ResponseWriter, r *http.Request) (*storage.CorrelationRuleset, bool) {
	service := s.correlationRulesetDomainService()
	if !service.StorageConfigured() {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return nil, false
	}
	ruleset, err := service.GetRuleset(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "correlation ruleset not found")
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return nil, false
	}
	// Route authorization and this load are separate durable snapshots. A
	// delete/recreate of the same caller-controlled ID must not let a request
	// authorized for the old project's row observe or mutate its replacement.
	if authorizedProjectID, scoped := authorizedProjectFromRequest(r); scoped &&
		normalizeProjectID(ruleset.ProjectID) != authorizedProjectID {
		writeError(w, http.StatusNotFound, "correlation ruleset not found")
		return nil, false
	}
	return disclosureSafeCorrelationRuleset(ruleset), true
}

func (s *Server) handleCreateCorrelationRuleset(w http.ResponseWriter, r *http.Request) {
	service := s.correlationRulesetDomainService()
	if !service.StorageConfigured() {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var ruleset storage.CorrelationRuleset
	if !decodeJSONBody(w, r, &ruleset) {
		return
	}
	if status, msg := validateCorrelationRuleset(&ruleset); status != 0 {
		writeError(w, status, msg)
		return
	}
	ruleset.ID = "correlation-ruleset-" + uuid.New().String()
	ruleset.ProjectID = normalizeProjectID(ruleset.ProjectID)
	safeRuleset, _ := correlationRulesetWithoutSamples(&ruleset)
	ruleset = *safeRuleset
	if err := service.SaveRuleset(&ruleset); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "correlation_ruleset.create", "success", "correlation_ruleset", ruleset.ID, ruleset.ProjectID, map[string]any{
		"name": ruleset.Name, "rules": len(ruleset.Rules),
	})
	response := correlationRulesetAPIResponse(&ruleset)
	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) handleListCorrelationRulesets(w http.ResponseWriter, r *http.Request) {
	service := s.correlationRulesetDomainService()
	limit, offset := parseLimitOffsetQuery(r, defaultCorrelationRulesetListLimit)
	if limit > maxCorrelationRulesetListLimit {
		limit = maxCorrelationRulesetListLimit
	}
	if !service.StorageConfigured() {
		response := correlationRulesetListResponse{
			CorrelationRulesets: []correlationRulesetResponse{},
			Limit:               limit,
			Offset:              offset,
		}
		writeJSON(w, http.StatusOK, response)
		return
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	rulesets, err := service.ListRulesets(projectID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rulesets == nil {
		rulesets = []*storage.CorrelationRuleset{}
	}
	for i := range rulesets {
		rulesets[i] = disclosureSafeCorrelationRuleset(rulesets[i])
	}
	response := correlationRulesetListResponse{
		CorrelationRulesets: make([]correlationRulesetResponse, len(rulesets)),
		Count:               len(rulesets),
		Limit:               limit,
		Offset:              offset,
	}
	for index, ruleset := range rulesets {
		response.CorrelationRulesets[index] = correlationRulesetAPIResponse(ruleset)
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleGetCorrelationRuleset(w http.ResponseWriter, r *http.Request) {
	ruleset, ok := s.loadCorrelationRuleset(w, r)
	if !ok {
		return
	}
	response := correlationRulesetAPIResponse(ruleset)
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleUpdateCorrelationRuleset(w http.ResponseWriter, r *http.Request) {
	service := s.correlationRulesetDomainService()
	existing, ok := s.loadCorrelationRuleset(w, r)
	if !ok {
		return
	}
	var payload correlationRulesetUpdateRequest
	if !decodeJSONBody(w, r, &payload) {
		return
	}
	if payload.ExpectedUpdatedAt.IsZero() {
		writeError(w, http.StatusBadRequest, "expected_updated_at is required")
		return
	}
	req := storage.CorrelationRuleset{
		ID:          existing.ID,
		ProjectID:   existing.ProjectID,
		Name:        payload.Name,
		Description: payload.Description,
		Source:      existing.Source,
		Rules:       payload.storageRules(),
		CreatedAt:   existing.CreatedAt,
	}
	safeReq, _ := correlationRulesetWithoutSamples(&req)
	req = *safeReq
	if status, msg := validateCorrelationRuleset(&req); status != 0 {
		writeError(w, status, msg)
		return
	}
	updated, err := service.UpdateRulesetIfUnchanged(&req, payload.ExpectedUpdatedAt)
	if err != nil {
		if errors.Is(err, errCorrelationRulesetOptimisticUpdateUnsupported) {
			writeError(w, http.StatusNotImplemented, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !updated {
		writeError(w, http.StatusConflict, "correlation ruleset changed while the update was in flight; reload and retry")
		return
	}
	s.recordAuditEvent(r, "correlation_ruleset.update", "success", "correlation_ruleset", req.ID, req.ProjectID, map[string]any{"rules": len(req.Rules)})
	response := correlationRulesetAPIResponse(&req)
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleDeleteCorrelationRuleset(w http.ResponseWriter, r *http.Request) {
	service := s.correlationRulesetDomainService()
	if !service.StorageConfigured() {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	id := r.PathValue("id")
	ruleset, err := service.GetRuleset(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "correlation ruleset not found")
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if !requireAuthorizedProjectMatch(w, r, ruleset.ProjectID) {
		return
	}
	projectID := authorizedProjectOr(r, ruleset.ProjectID)
	deleted, err := service.DeleteRulesetIfUnchanged(ruleset, projectID)
	if err != nil {
		if errors.Is(err, errCorrelationRulesetGuardedDeleteUnsupported) {
			writeError(w, http.StatusNotImplemented, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !deleted {
		writeError(w, http.StatusConflict, "correlation ruleset changed while deletion was in flight; reload and retry")
		return
	}
	s.recordAuditEvent(r, "correlation_ruleset.delete", "success", "correlation_ruleset", id, projectID, nil)
	response := correlationRulesetDeleteResponse{Deleted: id}
	writeJSON(w, http.StatusOK, response)
}

// handleDiscoverCorrelationRulesetFromScript runs correlation detection over a
// script's stored recorded baseline and persists the discovered correlations as
// a new project-scoped, reusable ruleset.
func (s *Server) handleDiscoverCorrelationRulesetFromScript(w http.ResponseWriter, r *http.Request) {
	service := s.correlationRulesetDomainService()
	if !service.StorageConfigured() {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var req struct {
		ScriptID string `json:"script_id"`
		Name     string `json:"name,omitempty"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	scriptID := strings.TrimSpace(req.ScriptID)
	if scriptID == "" {
		writeError(w, http.StatusBadRequest, "script_id is required")
		return
	}
	stored, ok := service.GetScript(scriptID)
	if !ok || stored == nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	scriptID = stored.ID
	if !requireAuthorizedProjectMatch(w, r, stored.ProjectID) {
		return
	}
	record, err := service.GetScriptRecording(scriptID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load recorded baseline")
		return
	}
	if record == nil || len(record.Observations) == 0 {
		writeError(w, http.StatusBadRequest, "script has no recorded baseline to learn correlations from")
		return
	}
	if err := validateRecordingActionGraph(record, stored.Script); err != nil {
		writeError(w, http.StatusConflict, "recorded baseline does not match the current script action graph; save and capture a new recording before discovering a ruleset")
		return
	}

	baseline, err := baselineObservations(record)
	if err != nil {
		writeError(w, http.StatusConflict, "recorded correlation baseline is unavailable; capture a new recording with baseline encryption configured")
		return
	}
	if !recordingBaselineCompleteForAutomation(record, baseline) {
		writeError(w, http.StatusConflict, "recorded correlation baseline is incomplete or inconsistent; capture a new recording before discovering a ruleset")
		return
	}
	correlations, err := correlate.DetectChecked(baseline)
	if err != nil {
		if errors.Is(err, correlate.ErrCorrelationWorkLimit) {
			writeError(w, http.StatusUnprocessableEntity, "recorded correlation baseline exceeded deterministic work limits")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to analyze recorded correlation baseline")
		return
	}
	rules := make([]storage.CorrelationRule, 0, len(correlations))
	for _, c := range correlations {
		rules = append(rules, storage.CorrelationRule{
			Name:        c.Name,
			RuleID:      c.RuleID,
			Source:      c.Source,
			ExtractType: c.ExtractType,
			Expression:  c.Expression,
			Confidence:  c.Confidence,
		})
	}
	if len(rules) == 0 {
		writeError(w, http.StatusBadRequest, "no correlations were detected in the recorded baseline")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = strings.TrimSpace(stored.Name) + " correlations"
	}
	ruleset := &storage.CorrelationRuleset{
		ID:        "correlation-ruleset-" + uuid.New().String(),
		ProjectID: normalizeProjectID(stored.ProjectID),
		Name:      name,
		Source:    "script:" + scriptID,
		Rules:     rules,
	}
	if err := service.SaveRuleset(ruleset); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "correlation_ruleset.discover", "success", "correlation_ruleset", ruleset.ID, ruleset.ProjectID, map[string]any{
		"name": ruleset.Name, "rules": len(ruleset.Rules), "source_script_id": scriptID,
	})
	response := correlationRulesetAPIResponse(ruleset)
	writeJSON(w, http.StatusCreated, response)
}

// handleApplyCorrelationRulesetToScript matches a ruleset's rules against a
// target script's baseline-derived correlation suggestions and returns a preview
// (suggested extractors + value-templating diffs). With apply=true the change is
// written into a new version of the script.
func (s *Server) handleApplyCorrelationRulesetToScript(w http.ResponseWriter, r *http.Request) {
	service := s.correlationRulesetDomainService()
	ruleset, ok := s.loadCorrelationRuleset(w, r)
	if !ok {
		return
	}
	var req struct {
		ScriptID     string `json:"script_id"`
		Apply        bool   `json:"apply,omitempty"`
		PreviewToken string `json:"preview_token,omitempty"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	scriptID := strings.TrimSpace(req.ScriptID)
	if scriptID == "" {
		writeError(w, http.StatusBadRequest, "script_id is required")
		return
	}
	stored, ok := service.GetScript(scriptID)
	if !ok || stored == nil || stored.Script == nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	scriptID = stored.ID
	// The route is authorized against the ruleset's project, but the target
	// script is named only in the request body. Reject a cross-project target so
	// an editor on the ruleset's project cannot read or rewrite a script that
	// lives in another project (GetScript/UpdateScript are not project-scoped).
	if normalizeProjectID(stored.ProjectID) != normalizeProjectID(ruleset.ProjectID) {
		// A body-referenced target is not part of the route authorization lookup.
		// Keep foreign and absent IDs indistinguishable to callers authorized only
		// for the ruleset's project.
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	record, err := service.GetScriptRecording(scriptID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load recorded baseline")
		return
	}
	if record == nil || len(record.Observations) == 0 {
		writeError(w, http.StatusBadRequest, "the target script has no recorded baseline to locate correlations in")
		return
	}
	if err := validateRecordingActionGraph(record, stored.Script); err != nil {
		writeError(w, http.StatusConflict, "recorded baseline does not match the current script action graph; save and capture a new recording before applying this ruleset")
		return
	}
	originalUpdatedAt := stored.Script.UpdatedAt
	originalActionGraphDigest, err := scriptCorrelationActionGraphDigest(stored.Script)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to bind correlation ruleset apply to the current script")
		return
	}

	baseline, err := baselineObservations(record)
	if err != nil {
		writeError(w, http.StatusConflict, "recorded correlation baseline is unavailable; capture a new recording with baseline encryption configured")
		return
	}
	if !recordingBaselineCompleteForAutomation(record, baseline) {
		writeError(w, http.StatusConflict, "recorded correlation baseline is incomplete or inconsistent; capture a new recording before applying this ruleset")
		return
	}
	if _, err := correlate.DetectChecked(baseline); err != nil {
		if errors.Is(err, correlate.ErrCorrelationWorkLimit) {
			writeError(w, http.StatusUnprocessableEntity, "recorded correlation baseline exceeded deterministic work limits")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to analyze recorded correlation baseline")
		return
	}
	result := paramize.Suggest(stored.Script, paramize.Options{
		Observations:            baseline,
		ObservationIDsZeroBased: true,
		EnableCorrelation:       true,
	})
	if rejectIncompleteCorrelationSuggestions(w, result) {
		return
	}

	selected := map[string]bool{}
	matched := make([]paramize.Suggestion, 0)
	for _, suggestion := range result.Suggestions {
		for _, rule := range ruleset.Rules {
			if ruleMatchesSuggestion(rule, suggestion) {
				selected[suggestion.ID] = true
				matched = append(matched, suggestion)
				break
			}
		}
	}

	clone, diffs, err := paramize.ApplySuggestions(stored.Script, result.Suggestions, selected)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The preview token is optional: it only guards the two-step
	// preview-then-apply protocol. A one-shot apply (no token supplied)
	// recomputes the preview in this same request and applies against exactly
	// that state, with the UpdateScriptIfUnchanged CAS (script updated_at +
	// action-graph digest captured above) still rejecting concurrent edits, so
	// the previous single-call API contract keeps working. Token signing being
	// unavailable (recording-baseline key not configured) degrades to an
	// omitted token instead of failing the preview outright.
	previewToken, tokenErr := correlationRulesetPreviewToken(ruleset, stored, record, originalActionGraphDigest, matched, diffs)
	if tokenErr != nil {
		previewToken = ""
	}

	applied := false
	if req.Apply && strings.TrimSpace(req.PreviewToken) != "" && !correlationRulesetPreviewTokenMatches(req.PreviewToken, previewToken) {
		// A supplied token binds the apply to a previously seen preview; a
		// mismatch means the ruleset/script/recording state changed since.
		writeError(w, http.StatusConflict, "correlation ruleset preview is stale; preview again before applying")
		return
	}
	if req.Apply && len(matched) > 0 {
		ok, err := service.UpdateScriptIfUnchangedAndRulesetCurrent(
			scriptID,
			clone,
			ruleset.ProjectID,
			originalUpdatedAt,
			originalActionGraphDigest,
			ruleset.ID,
			ruleset.UpdatedAt,
		)
		if err != nil {
			if errors.Is(err, errScriptConcurrentChange) {
				writeError(w, http.StatusConflict, err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to save parameterized script: "+err.Error())
			return
		}
		if !ok {
			// A false CAS result means the authoritative target disappeared or
			// otherwise stopped matching the snapshot after the preview work.
			// Never return a successful commit-shaped response with
			// applied=false: clients would present the operation as complete.
			writeError(w, http.StatusConflict, "target script changed or disappeared while the correlation ruleset apply was in flight; reload and retry")
			return
		}
		applied = true
		s.recordAuditEvent(r, "correlation_ruleset.apply", "success", "script", scriptID, ruleset.ProjectID, map[string]any{
			"ruleset_id": ruleset.ID, "matched": len(matched),
		})
	}

	response := correlationRulesetApplyResponse{
		RulesetID:    ruleset.ID,
		ScriptID:     scriptID,
		MatchedRules: len(matched),
		Applied:      applied,
		Diffs:        disclosureSafeCorrelationDiffs(diffs),
		PreviewToken: previewToken,
	}
	writeJSON(w, http.StatusOK, response)
}

// rejectIncompleteCorrelationSuggestions keeps ruleset preview/apply
// fail-closed when the checked detector succeeds but a later deterministic
// correlation stage (for example exact encoded-source validation) exhausts its
// own work budget. Paramize reports that condition as a warning and deliberately
// returns no partial correlation suggestions; treating that result as a clean
// zero-match preview would hide rules that should have been evaluated.
func rejectIncompleteCorrelationSuggestions(w http.ResponseWriter, result paramize.Result) bool {
	if len(result.Warnings) == 0 {
		return false
	}
	writeError(w, http.StatusUnprocessableEntity, "recorded correlation baseline exceeded deterministic work limits")
	return true
}

// disclosureSafeCorrelationDiffs keeps the useful structural preview while
// withholding before/after values and suggestion identifiers. Correlation
// suggestions are derived from the decrypted baseline, so those strings can be
// live session or credential material and must never cross the API boundary.
func disclosureSafeCorrelationDiffs(diffs []paramize.Diff) []correlationRulesetApplyDiff {
	result := make([]correlationRulesetApplyDiff, 0, len(diffs))
	for _, diff := range diffs {
		result = append(result, correlationRulesetApplyDiff{
			Kind: diff.Kind,
			Path: diff.Path,
		})
	}
	return result
}

// recordingBaselineCompleteForAutomation requires an authoritative one-to-one
// match between the original recording count, the disclosure-safe structural
// projection, and the authenticated raw sidecar. Diagnose endpoints may compare
// an explicitly reported prefix, but ruleset discover/apply are durable
// automation: using a retained prefix can persist extractors while leaving
// later consumers unparameterized. A missing legacy count or any projection
// mismatch is therefore ambiguous and fails closed.
func recordingBaselineCompleteForAutomation(record *storage.ScriptRecordingRecord, baseline []correlate.Observation) bool {
	if record == nil || record.RequestCount <= 0 || record.RequestCount != len(baseline) || len(record.Observations) != len(baseline) {
		return false
	}
	for index := range baseline {
		if record.Observations[index].ID != baseline[index].ID {
			return false
		}
	}
	return true
}

// ruleMatchesSuggestion reports whether a learned rule corresponds to a
// correlation suggestion generated for a target script (same detection rule +
// expression, or the same extractor triple).
func ruleMatchesSuggestion(rule storage.CorrelationRule, suggestion paramize.Suggestion) bool {
	if suggestion.Kind != paramize.KindCorrelation || suggestion.Extractor == nil {
		return false
	}
	if rule.RuleID != "" && rule.RuleID == suggestion.RuleID {
		return rule.Expression == "" || rule.Expression == suggestion.Extractor.Expression
	}
	return rule.Source == suggestion.Extractor.Source &&
		rule.ExtractType == suggestion.Extractor.Type &&
		rule.Expression == suggestion.Extractor.Expression
}
