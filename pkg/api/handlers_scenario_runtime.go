package api

// Apply-to-groups for scenario runtime overrides.
//
// Copies one group's *effective* runtime profile (engine defaults, stored source
// script config, scenario-root defaults, then group overrides) onto other groups
// in the same scenario. The copy is performed entirely server-side from the
// stored, unmasked values, so masked secrets are never echoed by the client, and
// fields that are not applicable to a target group's script protocol are skipped
// (with an inline summary) rather than written. Env and redaction rules are never
// copied across groups.

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
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
		status := http.StatusBadRequest
		if errors.Is(err, errScenarioConcurrentChange) {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
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
	// Validate the complete target set before materializing or copying any
	// runtime fields. A request is one atomic copy operation: silently dropping
	// an invalid target would otherwise persist a partial update to the valid
	// targets that happened to precede it.
	for requestIndex, target := range req.TargetGroups {
		if target < 0 || target >= n {
			return nil, true, fmt.Errorf("target_groups[%d] %d out of range [0,%d)", requestIndex, target, n)
		}
	}
	fields := normalizeRuntimeFieldKeys(req.Fields)
	if len(fields) == 0 {
		return nil, true, fmt.Errorf("at least one applicable runtime field is required")
	}

	// Materialize the same effective source profile the dashboard previews and the
	// runtime executes: engine defaults -> stored source script -> scenario root ->
	// source group. Starting at the scenario layer would make an inheriting source
	// appear empty and either clear the target or leave it on the target script's
	// unrelated defaults.
	sourceRef := stored.Scripts[req.SourceGroup]
	sourceStored, scriptFound := tr.scripts[sourceRef.ScriptID]
	if !scriptFound || sourceStored == nil || sourceStored.Script == nil {
		return nil, true, fmt.Errorf("source group script not found: %s", sourceRef.ScriptID)
	}
	effective := runtimeSettingsFromScriptConfig(dsl.DefaultScriptConfig())
	effective = mergeRuntimeSettings(effective, runtimeSettingsFromScriptConfig(sourceStored.Script.Config))
	effective = mergeRuntimeSettings(effective, stored.RuntimeSettings)
	effective = mergeRuntimeSettings(effective, sourceRef.RuntimeSettings)

	refs := cloneScenarioScriptRefs(stored.Scripts)

	skipped := []ApplyRuntimeToGroupsSkip{}
	applied := 0
	seenTargets := map[int]bool{}
	for _, target := range req.TargetGroups {
		if target == req.SourceGroup || seenTargets[target] {
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
		if len(applicable) == 0 {
			continue
		}
		// Apply-to-groups is a copy operation, not an overlay. A selected field
		// that is absent from the source group's effective profile must therefore
		// clear a target override for that field. Using mergeRuntimeSettings here
		// retained stale target values because nil/empty source fields were skipped.
		refs[target].RuntimeSettings = replaceRuntimeFields(refs[target].RuntimeSettings, effective, applicable)
		applied++
	}

	updated := *stored
	updated.Scripts = refs
	if err := tr.validateScenarioRuntimeMutationLocked(&updated); err != nil {
		return nil, true, fmt.Errorf("applied runtime settings are invalid: %w", err)
	}

	if tr.scenarioStore != nil {
		toSave := storedScenarioToStorage(&updated)
		scripts := storedScenarioRefsToStorage(&updated)
		if store, ok := tr.scenarioStore.(conditionalScenarioPersistence); ok {
			persisted, err := store.UpdateScenarioWithDataPoolBindingsIfUnchanged(
				toSave,
				scripts,
				append([]storage.ScenarioDataPoolBinding(nil), stored.DataPools...),
				stored.UpdatedAt,
			)
			if err != nil {
				return nil, true, err
			}
			if !persisted {
				return nil, true, errScenarioConcurrentChange
			}
		} else if err := tr.scenarioStore.SaveScenario(toSave, scripts); err != nil {
			return nil, true, err
		}
		updated.UpdatedAt = toSave.UpdatedAt
	} else {
		updated.UpdatedAt = time.Now().UTC()
	}
	tr.scenarios[id] = &updated
	tr.contentRevision++

	return &ApplyRuntimeToGroupsResponse{
		Scenario: tr.enrichScenarioSummaryLocked(&updated),
		Applied:  applied,
		Skipped:  skipped,
	}, true, nil
}

// runtimeSettingsFromScriptConfig projects concrete DSL configuration sections
// into the runtime-override shape used by mergeRuntimeSettings. A zero section is
// omitted because it means "use engine behavior" in ScriptConfig; the engine
// layer itself is projected from dsl.DefaultScriptConfig and therefore supplies
// every concrete default. Canonical sections are emitted without their legacy
// aliases so section precedence remains unambiguous.
func runtimeSettingsFromScriptConfig(config dsl.ScriptConfig) *RuntimeSettingsOverride {
	projected := &RuntimeSettingsOverride{BaseURL: config.BaseURL}
	if !reflect.ValueOf(config.Pacing).IsZero() {
		pacing := config.Pacing
		projected.Pacing = &pacing
	}
	if !reflect.ValueOf(config.ThinkTime).IsZero() {
		thinkTime := config.ThinkTime
		projected.ThinkTime = &thinkTime
	}
	if !reflect.ValueOf(config.ErrorHandling).IsZero() {
		errorHandling := config.ErrorHandling
		projected.ErrorHandling = &errorHandling
	}
	if !reflect.ValueOf(config.Log).IsZero() {
		projected.Log = cloneLogConfig(&config.Log)
	}
	if !reflect.ValueOf(config.RunLogic).IsZero() {
		projected.RunLogic = cloneRunLogicConfig(&config.RunLogic)
	}
	if !reflect.ValueOf(config.HTTP).IsZero() {
		projected.HTTP = cloneViaJSON(&config.HTTP)
	}
	if !reflect.ValueOf(config.WebRuntime).IsZero() {
		projected.WebRuntime = cloneViaJSON(&config.WebRuntime)
	}
	if !reflect.ValueOf(config.Browser).IsZero() {
		projected.BrowserConfig = cloneViaJSON(&config.Browser)
	}
	if runtimeSettingsOverrideEmpty(projected) {
		return nil
	}
	return projected
}

// validateScenarioRuntimeMutationLocked compiles the complete post-copy scenario
// before persistence. Applicability filtering alone cannot catch coupled settings
// such as open workload requiring TPS pacing or browser recorded-resource policy.
func (tr *TestRunner) validateScenarioRuntimeMutationLocked(scenario *StoredScenario) error {
	if scenario == nil {
		return fmt.Errorf("scenario is required")
	}
	entries := make([]CreateGroupEntry, 0, len(scenario.Scripts))
	for _, ref := range scenario.Scripts {
		entries = append(entries, CreateGroupEntry{
			Name:            ref.Name,
			ScriptID:        ref.ScriptID,
			Role:            ref.Role,
			VUsers:          ref.VUsers,
			RampUp:          ref.RampUp,
			RampDown:        ref.RampDown,
			Stages:          cloneLoadStages(ref.Stages),
			Iterations:      ref.Iterations,
			InitStrategy:    ref.Initialization,
			RuntimeSettings: cloneRuntimeSettings(ref.RuntimeSettings),
			Assignment:      cloneGeneratorAssignmentSettings(ref.Assignment),
		})
	}
	groups, err := normalizeCreateTestGroups(CreateTestRequest{Groups: entries})
	if err != nil {
		return err
	}
	for index := range groups {
		groups[index].runtimeSecretScope = scenario.ID
	}
	testGroups, engineGroups, _, err := tr.compileManagedTestGroupsForModeWithDataSourcesLocked(
		groups,
		scenario.RuntimeSettings,
		nil,
		normalizeScenarioMode(scenario.ScenarioMode),
	)
	if err != nil {
		return err
	}
	if review := buildRuntimeSettingsScenarioReview(testGroups, engineGroups); review != nil && review.Status == "blocked" {
		return fmt.Errorf("runtime settings blocked: %s", review.Summary)
	}
	return nil
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

// replaceRuntimeFields returns a deep copy of target with each selected field
// replaced by the corresponding field from source. Unlike mergeRuntimeSettings,
// this intentionally copies nil and empty values so an inherited/default source
// value clears a stale target override. Env and redaction rules are not selectable
// and consequently remain local to the target group.
func replaceRuntimeFields(target, source *RuntimeSettingsOverride, fields []string) *RuntimeSettingsOverride {
	if len(fields) == 0 {
		return cloneRuntimeSettings(target)
	}

	next := cloneRuntimeSettings(target)
	if next == nil {
		next = &RuntimeSettingsOverride{}
	}
	src := cloneRuntimeSettings(source)
	if src == nil {
		src = &RuntimeSettingsOverride{}
	}

	for _, field := range fields {
		switch field {
		case "iterations":
			replaceRuntimeIterations(next, src)
		case "pacing":
			next.Pacing = src.Pacing
		case "think_time":
			next.ThinkTime = src.ThinkTime
		case "error_handling":
			next.ErrorHandling = src.ErrorHandling
		case "browser":
			replaceRuntimeBrowser(next, src)
		case "log_level":
			replaceRuntimeLogLevel(next, src)
		case "auto_transactions":
			replaceRuntimeAutoTransactions(next, src)
		case "base_url":
			next.BaseURL = src.BaseURL
		case "log":
			next.Log = src.Log
		case "run_logic":
			next.RunLogic = src.RunLogic
		case "http":
			next.HTTP = src.HTTP
		case "web_runtime":
			next.WebRuntime = src.WebRuntime
		case "browser_config":
			next.BrowserConfig = src.BrowserConfig
		}
	}

	if runtimeSettingsOverrideEmpty(next) {
		return nil
	}
	return next
}

// The compact fields below have legacy top-level aliases plus canonical section
// representations. A section wins during execution, so copying only the alias can
// be a no-op when either side already carries that section. These helpers copy the
// effective value into the representation that will actually execute while keeping
// unselected section siblings intact.
func replaceRuntimeIterations(target, source *RuntimeSettingsOverride) {
	value := 0
	present := false
	if source.RunLogic != nil {
		value = source.RunLogic.Iterations
		present = value > 0
	} else if source.Iterations != nil {
		value = *source.Iterations
		present = true
	}
	if target.RunLogic != nil {
		target.RunLogic = cloneRunLogicConfig(target.RunLogic)
		target.RunLogic.Iterations = value
		target.Iterations = nil
		return
	}
	if present {
		target.Iterations = &value
	} else {
		target.Iterations = nil
	}
}

func replaceRuntimeLogLevel(target, source *RuntimeSettingsOverride) {
	value := source.LogLevel
	if source.Log != nil {
		value = source.Log.Level
	}
	if target.Log != nil {
		target.Log = cloneLogConfig(target.Log)
		target.Log.Level = value
		target.LogLevel = ""
		return
	}
	target.LogLevel = value
}

func replaceRuntimeAutoTransactions(target, source *RuntimeSettingsOverride) {
	value := source.AutoTransactions
	if source.RunLogic != nil {
		value = source.RunLogic.AutoTransactions
	}
	if target.RunLogic != nil {
		target.RunLogic = cloneRunLogicConfig(target.RunLogic)
		target.RunLogic.AutoTransactions = value
		target.AutoTransactions = ""
		return
	}
	target.AutoTransactions = value
}

func replaceRuntimeBrowser(target, source *RuntimeSettingsOverride) {
	var effective *RuntimeBrowserOverride
	if source.BrowserConfig != nil {
		effective = &RuntimeBrowserOverride{
			Headless:      cloneBoolPointer(source.BrowserConfig.Headless),
			DeviceProfile: source.BrowserConfig.DeviceProfile,
			TouchMode:     cloneBoolPointer(source.BrowserConfig.TouchMode),
			Orientation:   source.BrowserConfig.Orientation,
		}
	} else if source.Browser != nil {
		effective = cloneRuntimeSettings(&RuntimeSettingsOverride{Browser: source.Browser}).Browser
	}
	if effective != nil && effective.Headless == nil && effective.DeviceProfile == "" && effective.TouchMode == nil && effective.Orientation == "" {
		effective = nil
	}
	if target.BrowserConfig != nil {
		target.BrowserConfig = cloneViaJSON(target.BrowserConfig)
		if effective == nil {
			target.BrowserConfig.Headless = nil
			target.BrowserConfig.DeviceProfile = ""
			target.BrowserConfig.TouchMode = nil
			target.BrowserConfig.Orientation = ""
		} else {
			target.BrowserConfig.Headless = cloneBoolPointer(effective.Headless)
			target.BrowserConfig.DeviceProfile = effective.DeviceProfile
			target.BrowserConfig.TouchMode = cloneBoolPointer(effective.TouchMode)
			target.BrowserConfig.Orientation = effective.Orientation
		}
		target.Browser = nil
		return
	}
	target.Browser = effective
}

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func runtimeSettingsOverrideEmpty(settings *RuntimeSettingsOverride) bool {
	return settings == nil ||
		(settings.Iterations == nil && settings.Pacing == nil && settings.ThinkTime == nil &&
			settings.ErrorHandling == nil && settings.Browser == nil && settings.LogLevel == "" &&
			settings.AutoTransactions == "" && settings.BaseURL == "" && settings.Log == nil &&
			settings.RunLogic == nil && settings.HTTP == nil && settings.WebRuntime == nil &&
			settings.BrowserConfig == nil && len(settings.Env) == 0 && len(settings.RedactionRules) == 0)
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
