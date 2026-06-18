package api

// Apply-to-groups for scenario runtime overrides.
//
// Copies one group's *effective* runtime profile (scenario-root defaults merged
// with that group's overrides) onto other groups in the same scenario. The copy
// is performed entirely server-side from the stored, unmasked values, so masked
// secrets are never echoed by the client, and fields that are not applicable to a
// target group's script protocol are skipped (with an inline summary) rather than
// written. Env and redaction rules are never copied across groups.

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ApplyRuntimeToGroupsRequest is the apply-to-groups request body.
type ApplyRuntimeToGroupsRequest struct {
	SourceGroup  int      `json:"source_group"`
	TargetGroups []int    `json:"target_groups"`
	Fields       []string `json:"fields"`
}

// ApplyRuntimeToGroupsSkip records fields skipped for a target group because they
// are not applicable to that group's script protocol.
type ApplyRuntimeToGroupsSkip struct {
	Group    int      `json:"group"`
	Protocol string   `json:"protocol,omitempty"`
	Fields   []string `json:"fields"`
}

// ApplyRuntimeToGroupsResponse is the apply-to-groups result: the updated (masked)
// scenario plus a summary of what was applied and skipped.
type ApplyRuntimeToGroupsResponse struct {
	Scenario *StoredScenario            `json:"scenario"`
	Applied  int                        `json:"applied"`
	Skipped  []ApplyRuntimeToGroupsSkip `json:"skipped,omitempty"`
}

// runtimeOverrideFieldKeys is the set of Phase 1 override fields apply-to-groups can
// copy. Env/RedactionRules are intentionally excluded — secrets and PII rules stay
// per-group and are never copied across groups. The set widens with the contract in
// Phase 2.
var runtimeOverrideFieldKeys = map[string]bool{
	"iterations":        true,
	"pacing":            true,
	"think_time":        true,
	"error_handling":    true,
	"browser":           true,
	"log_level":         true,
	"auto_transactions": true,
	"base_url":          true,
	"log":               true,
	"run_logic":         true,
	"http":              true,
	"web_runtime":       true,
	"browser_config":    true,
}

func (s *Server) handleApplyScenarioRuntimeToGroups(w http.ResponseWriter, r *http.Request) {
	var req ApplyRuntimeToGroupsRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	resp, ok, err := s.runner.ApplyScenarioRuntimeToGroups(r.PathValue("id"), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "scenario not found")
		return
	}
	resp.Scenario = maskStoredScenario(resp.Scenario)
	s.recordAuditEvent(r, "scenario.runtime.apply_to_groups", "success", "scenario", resp.Scenario.ID, resp.Scenario.ProjectID, map[string]any{"applied": resp.Applied, "source_group": req.SourceGroup})
	writeJSON(w, http.StatusOK, resp)
}

// ApplyScenarioRuntimeToGroups copies the source group's effective runtime profile
// onto the requested target groups, persists the scenario, and returns the updated
// (unmasked) scenario plus an apply/skip summary. ok=false means the scenario was
// not found.
func (tr *TestRunner) ApplyScenarioRuntimeToGroups(id string, req ApplyRuntimeToGroupsRequest) (*ApplyRuntimeToGroupsResponse, bool, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	stored, ok := tr.scenarios[id]
	if !ok {
		return nil, false, nil
	}
	n := len(stored.Scripts)
	if n == 0 {
		return nil, true, fmt.Errorf("scenario has no groups")
	}
	if req.SourceGroup < 0 || req.SourceGroup >= n {
		return nil, true, fmt.Errorf("source_group %d out of range [0,%d)", req.SourceGroup, n)
	}
	fields := normalizeRuntimeFieldKeys(req.Fields)
	if len(fields) == 0 {
		return nil, true, fmt.Errorf("at least one applicable runtime field is required")
	}

	// Effective source profile = scenario-root base merged with the source group's
	// override — the same value the editor previews as the group's effective runtime.
	effective := mergeRuntimeSettings(stored.RuntimeSettings, stored.Scripts[req.SourceGroup].RuntimeSettings)

	refs := make([]ScenarioScriptRef, len(stored.Scripts))
	copy(refs, stored.Scripts)

	skipped := []ApplyRuntimeToGroupsSkip{}
	applied := 0
	seenTargets := map[int]bool{}
	for _, target := range req.TargetGroups {
		if target < 0 || target >= n || target == req.SourceGroup || seenTargets[target] {
			continue
		}
		seenTargets[target] = true
		proto := refs[target].Protocol
		applicable := make([]string, 0, len(fields))
		var skippedFields []string
		for _, f := range fields {
			if fieldApplicableForProtocol(f, proto) {
				applicable = append(applicable, f)
			} else {
				skippedFields = append(skippedFields, f)
			}
		}
		if len(skippedFields) > 0 {
			skipped = append(skipped, ApplyRuntimeToGroupsSkip{Group: target, Protocol: proto, Fields: skippedFields})
		}
		projected := projectRuntimeFields(effective, applicable)
		if projected == nil {
			continue
		}
		refs[target].RuntimeSettings = mergeRuntimeSettings(refs[target].RuntimeSettings, projected)
		applied++
	}

	updated := *stored
	updated.Scripts = refs
	updated.UpdatedAt = time.Now().UTC()

	if tr.scenarioStore != nil {
		if err := tr.scenarioStore.SaveScenario(storedScenarioToStorage(&updated), storedScenarioRefsToStorage(&updated)); err != nil {
			return nil, true, err
		}
	}
	tr.scenarios[id] = &updated

	return &ApplyRuntimeToGroupsResponse{
		Scenario: tr.enrichScenarioSummaryLocked(&updated),
		Applied:  applied,
		Skipped:  skipped,
	}, true, nil
}

// normalizeRuntimeFieldKeys lower-cases, de-dupes, and filters the requested field
// list down to recognized override field keys.
func normalizeRuntimeFieldKeys(fields []string) []string {
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, f := range fields {
		key := strings.TrimSpace(strings.ToLower(f))
		if runtimeOverrideFieldKeys[key] && !seen[key] {
			out = append(out, key)
			seen[key] = true
		}
	}
	return out
}

// projectRuntimeFields returns a clone of src carrying only the requested fields.
// Env and redaction rules are always dropped (never copied across groups). Returns
// nil if no requested field is present in src.
func projectRuntimeFields(src *RuntimeSettingsOverride, fields []string) *RuntimeSettingsOverride {
	if src == nil || len(fields) == 0 {
		return nil
	}
	requested := map[string]bool{}
	for _, f := range fields {
		requested[f] = true
	}
	clone := cloneRuntimeSettings(src)
	if clone == nil {
		return nil
	}
	if !requested["iterations"] {
		clone.Iterations = nil
	}
	if !requested["pacing"] {
		clone.Pacing = nil
	}
	if !requested["think_time"] {
		clone.ThinkTime = nil
	}
	if !requested["error_handling"] {
		clone.ErrorHandling = nil
	}
	if !requested["browser"] {
		clone.Browser = nil
	}
	if !requested["log_level"] {
		clone.LogLevel = ""
	}
	if !requested["auto_transactions"] {
		clone.AutoTransactions = ""
	}
	if !requested["base_url"] {
		clone.BaseURL = ""
	}
	if !requested["log"] {
		clone.Log = nil
	}
	if !requested["run_logic"] {
		clone.RunLogic = nil
	}
	if !requested["http"] {
		clone.HTTP = nil
	}
	if !requested["web_runtime"] {
		clone.WebRuntime = nil
	}
	if !requested["browser_config"] {
		clone.BrowserConfig = nil
	}
	clone.Env = nil
	clone.RedactionRules = nil
	if clone.Iterations == nil && clone.Pacing == nil && clone.ThinkTime == nil &&
		clone.ErrorHandling == nil && clone.Browser == nil && clone.LogLevel == "" &&
		clone.AutoTransactions == "" && clone.BaseURL == "" &&
		clone.Log == nil && clone.RunLogic == nil &&
		clone.HTTP == nil && clone.WebRuntime == nil && clone.BrowserConfig == nil {
		return nil
	}
	return clone
}

// fieldApplicableForProtocol reports whether a runtime override field is meaningful
// for a group running the given script protocol. Phase 1: browser device/runtime
// settings apply only to the browser protocol; every other field is universal.
// Phase 2 extends this into the full field-to-protocol applicability matrix; both the
// apply path here and the editor's "Not applicable" rows must consume the same
// predicate to avoid drift.
func fieldApplicableForProtocol(field, protocol string) bool {
	switch field {
	case "browser", "browser_config":
		return protocolFamilyIsBrowser(protocol)
	case "http":
		// HTTP transport (which now also hosts browser-emulation settings in the
		// editor) applies to HTTP-family protocols AND the browser protocol.
		return isHTTPProtocol(protocol) || protocolFamilyIsBrowser(protocol)
	case "web_runtime":
		return protocolFamilyIsBrowser(protocol) || isHTTPProtocol(protocol)
	default:
		// iterations, pacing, think_time, error_handling, log, run_logic, log_level,
		// auto_transactions, base_url are universal.
		return true
	}
}

func protocolFamilyIsBrowser(protocol string) bool {
	return strings.EqualFold(strings.TrimSpace(protocol), "browser")
}

// isHTTPProtocol reports whether a script protocol uses HTTP transport, so the HTTP
// and web-runtime override sections are applicable.
func isHTTPProtocol(protocol string) bool {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "http", "https", "soap", "java_http", "web_http", "web":
		return true
	}
	return false
}
