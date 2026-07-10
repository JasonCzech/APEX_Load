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

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

const defaultCorrelationRulesetListLimit = 100

func validateCorrelationRuleset(ruleset *storage.CorrelationRuleset) (int, string) {
	if strings.TrimSpace(ruleset.Name) == "" {
		return http.StatusBadRequest, "correlation ruleset name is required"
	}
	return 0, ""
}

func (s *Server) loadCorrelationRuleset(w http.ResponseWriter, r *http.Request) (*storage.CorrelationRuleset, bool) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return nil, false
	}
	ruleset, err := s.store.GetCorrelationRuleset(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "correlation ruleset not found")
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return nil, false
	}
	return ruleset, true
}

func (s *Server) handleCreateCorrelationRuleset(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
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
	if err := s.store.SaveCorrelationRuleset(&ruleset); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "correlation_ruleset.create", "success", "correlation_ruleset", ruleset.ID, ruleset.ProjectID, map[string]any{
		"name": ruleset.Name, "rules": len(ruleset.Rules),
	})
	writeJSON(w, http.StatusCreated, ruleset)
}

func (s *Server) handleListCorrelationRulesets(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseLimitOffsetQuery(r, defaultCorrelationRulesetListLimit)
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"correlation_rulesets": []*storage.CorrelationRuleset{}, "count": 0, "limit": limit, "offset": offset})
		return
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	rulesets, err := s.store.ListCorrelationRulesets(projectID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rulesets == nil {
		rulesets = []*storage.CorrelationRuleset{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"correlation_rulesets": rulesets, "count": len(rulesets), "limit": limit, "offset": offset})
}

func (s *Server) handleGetCorrelationRuleset(w http.ResponseWriter, r *http.Request) {
	ruleset, ok := s.loadCorrelationRuleset(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, ruleset)
}

func (s *Server) handleUpdateCorrelationRuleset(w http.ResponseWriter, r *http.Request) {
	existing, ok := s.loadCorrelationRuleset(w, r)
	if !ok {
		return
	}
	var req storage.CorrelationRuleset
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req.ID = existing.ID
	req.ProjectID = existing.ProjectID
	req.CreatedAt = existing.CreatedAt
	if status, msg := validateCorrelationRuleset(&req); status != 0 {
		writeError(w, status, msg)
		return
	}
	if err := s.store.SaveCorrelationRuleset(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "correlation_ruleset.update", "success", "correlation_ruleset", req.ID, req.ProjectID, map[string]any{"rules": len(req.Rules)})
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) handleDeleteCorrelationRuleset(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	id := r.PathValue("id")
	projectID := ""
	if ruleset, err := s.store.GetCorrelationRuleset(id); err == nil && ruleset != nil {
		projectID = ruleset.ProjectID
	}
	if err := s.store.DeleteCorrelationRuleset(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "correlation_ruleset.delete", "success", "correlation_ruleset", id, projectID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

// handleDiscoverCorrelationRulesetFromScript runs correlation detection over a
// script's stored recorded baseline and persists the discovered correlations as
// a new project-scoped, reusable ruleset.
func (s *Server) handleDiscoverCorrelationRulesetFromScript(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
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
	stored, ok := s.runner.GetScript(scriptID)
	if !ok || stored == nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	record, err := s.store.GetScriptRecording(scriptID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load recorded baseline")
		return
	}
	if record == nil || len(record.Observations) == 0 {
		writeError(w, http.StatusBadRequest, "script has no recorded baseline to learn correlations from")
		return
	}

	correlations := correlate.Detect(baselineObservations(record))
	rules := make([]storage.CorrelationRule, 0, len(correlations))
	for _, c := range correlations {
		rules = append(rules, storage.CorrelationRule{
			Name:        c.Name,
			RuleID:      c.RuleID,
			Source:      c.Source,
			ExtractType: c.ExtractType,
			Expression:  c.Expression,
			// Detection runs on the raw baseline, but the sample value is
			// persisted at rest and returned to the client; scrub it so a
			// credential-shaped sample never leaks. Rule matching keys on
			// source/type/expression (see ruleMatchesSuggestion), not the
			// sample, so redaction here does not affect correlation.
			SampleValue: scrub.Value(c.SampleValue, scrub.Policy{}),
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
	if err := s.store.SaveCorrelationRuleset(ruleset); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "correlation_ruleset.discover", "success", "correlation_ruleset", ruleset.ID, ruleset.ProjectID, map[string]any{
		"name": ruleset.Name, "rules": len(ruleset.Rules), "source_script_id": scriptID,
	})
	writeJSON(w, http.StatusCreated, ruleset)
}

// handleApplyCorrelationRulesetToScript matches a ruleset's rules against a
// target script's baseline-derived correlation suggestions and returns a preview
// (suggested extractors + value-templating diffs). With apply=true the change is
// written into a new version of the script.
func (s *Server) handleApplyCorrelationRulesetToScript(w http.ResponseWriter, r *http.Request) {
	ruleset, ok := s.loadCorrelationRuleset(w, r)
	if !ok {
		return
	}
	var req struct {
		ScriptID string `json:"script_id"`
		Apply    bool   `json:"apply,omitempty"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	scriptID := strings.TrimSpace(req.ScriptID)
	if scriptID == "" {
		writeError(w, http.StatusBadRequest, "script_id is required")
		return
	}
	stored, ok := s.runner.GetScript(scriptID)
	if !ok || stored == nil || stored.Script == nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	// The route is authorized against the ruleset's project, but the target
	// script is named only in the request body. Reject a cross-project target so
	// an editor on the ruleset's project cannot read or rewrite a script that
	// lives in another project (GetScript/UpdateScript are not project-scoped).
	if normalizeProjectID(stored.ProjectID) != normalizeProjectID(ruleset.ProjectID) {
		writeError(w, http.StatusForbidden, "the target script belongs to a different project than the ruleset")
		return
	}
	record, err := s.store.GetScriptRecording(scriptID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load recorded baseline")
		return
	}
	if record == nil || len(record.Observations) == 0 {
		writeError(w, http.StatusBadRequest, "the target script has no recorded baseline to locate correlations in")
		return
	}

	result := paramize.Suggest(stored.Script, paramize.Options{
		Observations:            baselineObservations(record),
		ObservationIDsZeroBased: true,
		EnableCorrelation:       true,
	})

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

	applied := false
	if req.Apply && len(matched) > 0 {
		if _, ok := s.runner.UpdateScript(scriptID, clone); ok {
			applied = true
			s.recordAuditEvent(r, "correlation_ruleset.apply", "success", "script", scriptID, ruleset.ProjectID, map[string]any{
				"ruleset_id": ruleset.ID, "matched": len(matched),
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ruleset_id":    ruleset.ID,
		"script_id":     scriptID,
		"matched_rules": len(matched),
		"applied":       applied,
		"suggestions":   matched,
		"diffs":         diffs,
	})
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
