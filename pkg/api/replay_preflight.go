package api

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

type ReplayPreflight struct {
	Valid                bool                   `json:"valid"`
	ActionCount          int                    `json:"action_count"`
	ExpectedActionCount  int                    `json:"expected_action_count"`
	DataSourceCount      int                    `json:"data_source_count"`
	ParameterCount       int                    `json:"parameter_count"`
	BrowserRequired      bool                   `json:"browser_required"`
	MissingVariables     []string               `json:"missing_variables,omitempty"`
	RequiredSecrets      []string               `json:"required_secrets,omitempty"`
	MissingSecrets       []string               `json:"missing_secrets,omitempty"`
	RuntimePrerequisites []string               `json:"runtime_prerequisites,omitempty"`
	Issues               []string               `json:"issues,omitempty"`
	Warnings             []string               `json:"warnings,omitempty"`
	Checks               []ReplayPreflightCheck `json:"checks,omitempty"`
}

type ReplayPreflightCheck struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

var replayTemplateRefPattern = regexp.MustCompile(`\{\{\s*(var|env|data|param)\.([a-zA-Z0-9_.-]+)`)

const replayPreflightMaxControlDepth = 32

func buildReplayPreflight(script *dsl.Script, variables map[string]string, issues []string, warnings []string) *ReplayPreflight {
	reviewGateIssues := replayReviewGateIssues(script)
	actionCount := replayActionDefinitionCount(script)
	preflight := &ReplayPreflight{
		Valid:                len(issues) == 0,
		ActionCount:          actionCount,
		ExpectedActionCount:  replayExpectedActionCount(script),
		DataSourceCount:      len(scriptDataSources(script)),
		ParameterCount:       len(scriptParameters(script)),
		BrowserRequired:      script != nil && strings.EqualFold(strings.TrimSpace(script.Protocol), "browser"),
		RuntimePrerequisites: replayRuntimePrerequisites(script),
		Issues:               appendUniqueStrings(append([]string(nil), issues...), reviewGateIssues...),
		Warnings:             append([]string(nil), warnings...),
	}
	preflight.MissingVariables = replayMissingVariables(script, variables)
	preflight.RequiredSecrets, preflight.MissingSecrets = replaySecretRefs(script)
	dataWarnings := replayDataSourceWarnings(script)
	if len(dataWarnings) > 0 {
		preflight.Warnings = append(preflight.Warnings, dataWarnings...)
	}
	preflight.Valid = preflight.Valid && len(reviewGateIssues) == 0 && len(preflight.MissingVariables) == 0 && len(preflight.MissingSecrets) == 0 && len(dataWarnings) == 0
	preflight.Checks = []ReplayPreflightCheck{
		{
			ID:     "validation",
			Label:  "Script validation",
			Status: replayCheckStatus(len(issues) == 0, len(warnings) > 0),
			Detail: replayCheckDetail(len(issues), len(warnings), "validation issue", "validation warning"),
		},
		{
			ID:     "review_gates",
			Label:  "Review gates",
			Status: replayCheckStatus(len(reviewGateIssues) == 0, false),
			Detail: replayListDetail(reviewGateIssues, "No review-gated actions block replay."),
		},
		{
			ID:     "variables",
			Label:  "Variable readiness",
			Status: replayCheckStatus(len(preflight.MissingVariables) == 0, false),
			Detail: replayListDetail(preflight.MissingVariables, "All replay variables are seeded or extracted before use."),
		},
		{
			ID:     "secrets",
			Label:  "Secret bindings",
			Status: replayCheckStatus(len(preflight.MissingSecrets) == 0, len(preflight.RequiredSecrets) > 0),
			Detail: replaySecretDetail(preflight.RequiredSecrets, preflight.MissingSecrets),
		},
		{
			ID:     "data_sources",
			Label:  "Data source readiness",
			Status: replayCheckStatus(len(dataWarnings) == 0, len(scriptDataSources(script)) > 0),
			Detail: replayDataSourceDetail(script, dataWarnings),
		},
		{
			ID:     "runtime",
			Label:  "Runtime prerequisites",
			Status: replayCheckStatus(true, preflight.BrowserRequired),
			Detail: strings.Join(preflight.RuntimePrerequisites, "; "),
		},
	}
	if preflight.BrowserRequired {
		preflight.Checks = append(preflight.Checks, replayEvidenceCaptureCheck(script))
	}
	return preflight
}

func cloneReplayPreflight(preflight *ReplayPreflight) *ReplayPreflight {
	if preflight == nil {
		return nil
	}
	cloned := *preflight
	cloned.MissingVariables = append([]string(nil), preflight.MissingVariables...)
	cloned.RequiredSecrets = append([]string(nil), preflight.RequiredSecrets...)
	cloned.MissingSecrets = append([]string(nil), preflight.MissingSecrets...)
	cloned.RuntimePrerequisites = append([]string(nil), preflight.RuntimePrerequisites...)
	cloned.Issues = append([]string(nil), preflight.Issues...)
	cloned.Warnings = append([]string(nil), preflight.Warnings...)
	cloned.Checks = append([]ReplayPreflightCheck(nil), preflight.Checks...)
	return &cloned
}

func replayActionDefinitionCount(script *dsl.Script) int {
	if script == nil {
		return 0
	}
	return replayDefinitionCount(replayPhaseActions(script), 0)
}

func replayExpectedActionCount(script *dsl.Script) int {
	if script == nil {
		return 0
	}
	return replayExpectedCount(replayPhaseActions(script), 0)
}

func scriptDataSources(script *dsl.Script) []dsl.DataSource {
	if script == nil {
		return nil
	}
	return script.DataSources
}

func scriptParameters(script *dsl.Script) []dsl.Parameter {
	if script == nil {
		return nil
	}
	return script.Parameters
}

func replayRuntimePrerequisites(script *dsl.Script) []string {
	if script == nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(script.Protocol), "browser") {
		return []string{"Protocol adapter: " + strings.TrimSpace(script.Protocol)}
	}
	engine := strings.TrimSpace(script.Config.Browser.Engine)
	if engine == "" {
		engine = "chromium"
	}
	prereqs := []string{"Browser engine: " + engine}
	if profile := strings.TrimSpace(script.Config.Browser.DeviceProfile); profile != "" {
		prereqs = append(prereqs, "Device profile: "+profile)
	}
	capture := strings.TrimSpace(script.Config.Browser.CaptureScreenshots)
	if capture == "" {
		capture = "per_action"
	}
	prereqs = append(prereqs, "Screenshot capture: "+capture)
	return prereqs
}

func replayEvidenceCaptureCheck(script *dsl.Script) ReplayPreflightCheck {
	capture := "per_action"
	if script != nil {
		if configured := strings.TrimSpace(script.Config.Browser.CaptureScreenshots); configured != "" {
			capture = configured
		}
	}
	if strings.EqualFold(capture, "off") || strings.EqualFold(capture, "none") || strings.EqualFold(capture, "disabled") {
		return ReplayPreflightCheck{
			ID:     "evidence_capture",
			Label:  "Evidence capture",
			Status: "warn",
			Detail: "Screenshot capture is disabled; enable per_action or on_failure before using replay as promotion evidence.",
		}
	}
	return ReplayPreflightCheck{
		ID:     "evidence_capture",
		Label:  "Evidence capture",
		Status: "pass",
		Detail: "Replay will capture screenshot, DOM, console, and network evidence for browser review.",
	}
}

func replayMissingVariables(script *dsl.Script, variables map[string]string) []string {
	if script == nil {
		return nil
	}
	available := make(map[string]struct{}, len(variables)+len(script.Parameters))
	for key := range variables {
		if trimmed := strings.TrimSpace(key); trimmed != "" {
			available[trimmed] = struct{}{}
		}
	}
	for _, parameter := range script.Parameters {
		if trimmed := strings.TrimSpace(parameter.Name); trimmed != "" {
			available[trimmed] = struct{}{}
		}
	}
	missing := make(map[string]struct{})
	for _, action := range replayOrderedActions(script) {
		for _, ref := range replayTemplateRefs(action, "var") {
			if _, ok := available[ref]; !ok {
				missing[ref] = struct{}{}
			}
		}
		for _, extractor := range action.Extractors {
			if trimmed := strings.TrimSpace(extractor.Name); trimmed != "" {
				available[trimmed] = struct{}{}
			}
		}
	}
	return sortedSetKeys(missing)
}

func replaySecretRefs(script *dsl.Script) ([]string, []string) {
	if script == nil {
		return nil, nil
	}
	required := make(map[string]struct{})
	missing := make(map[string]struct{})
	for _, action := range replayOrderedActions(script) {
		for _, ref := range replayTemplateRefs(action, "env") {
			required[ref] = struct{}{}
			if _, ok := os.LookupEnv(ref); !ok {
				missing[ref] = struct{}{}
			}
		}
	}
	return sortedSetKeys(required), sortedSetKeys(missing)
}

func replayDataSourceWarnings(script *dsl.Script) []string {
	if script == nil {
		return nil
	}
	var warnings []string
	sourceNames := make(map[string]struct{}, len(script.DataSources))
	for _, source := range script.DataSources {
		name := strings.TrimSpace(source.Name)
		if name != "" {
			sourceNames[name] = struct{}{}
		}
		if strings.TrimSpace(source.Path) == "" && strings.TrimSpace(source.InlineData) == "" {
			warnings = append(warnings, fmt.Sprintf("data source %q has no path or inline data", nonEmptyLabel(name, "unnamed")))
		}
	}
	for _, parameter := range script.Parameters {
		sourceName := strings.TrimSpace(parameter.DataSource)
		if sourceName == "" {
			continue
		}
		if _, ok := sourceNames[sourceName]; !ok {
			warnings = append(warnings, fmt.Sprintf("parameter %q references missing data source %q", nonEmptyLabel(parameter.Name, "unnamed"), sourceName))
		}
	}
	sort.Strings(warnings)
	return warnings
}

func replayPhaseActions(script *dsl.Script) []dsl.Action {
	if script == nil {
		return nil
	}
	actions := make([]dsl.Action, 0, len(script.Init)+len(script.Actions)+len(script.Teardown))
	actions = append(actions, script.Init...)
	actions = append(actions, script.Actions...)
	actions = append(actions, script.Teardown...)
	return actions
}

func replayOrderedActions(script *dsl.Script) []dsl.Action {
	return flattenReplayActionsForReadiness(replayPhaseActions(script), 0)
}

func flattenReplayActionsForReadiness(actions []dsl.Action, depth int) []dsl.Action {
	ordered := make([]dsl.Action, 0, len(actions))
	for _, action := range actions {
		switch strings.ToLower(strings.TrimSpace(action.Type)) {
		case "control_if":
			ordered = append(ordered, replayControlShell(action))
			if action.Control != nil && depth < replayPreflightMaxControlDepth {
				ordered = append(ordered, flattenReplayActionsForReadiness(action.Control.Steps, depth+1)...)
				ordered = append(ordered, flattenReplayActionsForReadiness(action.Control.Else, depth+1)...)
			}
		case "control_loop":
			ordered = append(ordered, replayControlShell(action))
			if action.Control != nil && depth < replayPreflightMaxControlDepth {
				ordered = append(ordered, flattenReplayActionsForReadiness(action.Control.Steps, depth+1)...)
			}
		default:
			ordered = append(ordered, action)
		}
	}
	return ordered
}

func replayControlShell(action dsl.Action) dsl.Action {
	if action.Control == nil {
		return action
	}
	clone := action
	control := *action.Control
	control.Steps = nil
	control.Else = nil
	clone.Control = &control
	return clone
}

func replayDefinitionCount(actions []dsl.Action, depth int) int {
	var count int
	for _, action := range actions {
		switch strings.ToLower(strings.TrimSpace(action.Type)) {
		case "control_if":
			if action.Control != nil && depth < replayPreflightMaxControlDepth {
				count += replayDefinitionCount(action.Control.Steps, depth+1)
				count += replayDefinitionCount(action.Control.Else, depth+1)
			}
		case "control_loop":
			if action.Control != nil && depth < replayPreflightMaxControlDepth {
				count += replayDefinitionCount(action.Control.Steps, depth+1)
			}
		default:
			count++
		}
	}
	return count
}

func replayExpectedCount(actions []dsl.Action, depth int) int {
	var count int
	for _, action := range actions {
		switch strings.ToLower(strings.TrimSpace(action.Type)) {
		case "control_if":
			if action.Control == nil || depth >= replayPreflightMaxControlDepth {
				continue
			}
			thenCount := replayExpectedCount(action.Control.Steps, depth+1)
			elseCount := replayExpectedCount(action.Control.Else, depth+1)
			if elseCount > thenCount {
				count += elseCount
			} else {
				count += thenCount
			}
		case "control_loop":
			if action.Control == nil || depth >= replayPreflightMaxControlDepth {
				continue
			}
			bodyCount := replayExpectedCount(action.Control.Steps, depth+1)
			passes := action.Control.Count
			if passes <= 0 {
				passes = 1
			}
			count += bodyCount * passes
		default:
			count++
		}
	}
	return count
}

func replayTemplateRefs(action dsl.Action, kind string) []string {
	payload, err := json.Marshal(action)
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{})
	for _, match := range replayTemplateRefPattern.FindAllStringSubmatch(string(payload), -1) {
		if len(match) < 3 || match[1] != kind {
			continue
		}
		if ref := strings.TrimSpace(match[2]); ref != "" {
			seen[ref] = struct{}{}
		}
	}
	return sortedSetKeys(seen)
}

func sortedSetKeys(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func replayCheckStatus(pass bool, warn bool) string {
	if !pass {
		return "fail"
	}
	if warn {
		return "warn"
	}
	return "pass"
}

func replayCheckDetail(primaryCount int, secondaryCount int, primaryLabel string, secondaryLabel string) string {
	parts := make([]string, 0, 2)
	if primaryCount > 0 {
		parts = append(parts, fmt.Sprintf("%d %s%s", primaryCount, primaryLabel, replayPluralSuffix(primaryCount)))
	}
	if secondaryCount > 0 {
		parts = append(parts, fmt.Sprintf("%d %s%s", secondaryCount, secondaryLabel, replayPluralSuffix(secondaryCount)))
	}
	if len(parts) == 0 {
		return "Ready"
	}
	return strings.Join(parts, "; ")
}

func replayListDetail(values []string, empty string) string {
	if len(values) == 0 {
		return empty
	}
	return strings.Join(values, ", ")
}

func replaySecretDetail(required []string, missing []string) string {
	if len(required) == 0 {
		return "No environment secret references detected."
	}
	if len(missing) > 0 {
		return "Missing: " + strings.Join(missing, ", ")
	}
	return "Required: " + strings.Join(required, ", ")
}

func replayDataSourceDetail(script *dsl.Script, warnings []string) string {
	if len(warnings) > 0 {
		return strings.Join(warnings, "; ")
	}
	count := len(scriptDataSources(script))
	if count == 0 {
		return "No data sources configured."
	}
	return fmt.Sprintf("%d data source%s ready", count, replayPluralSuffix(count))
}

func replayPluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func nonEmptyLabel(value string, fallback string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return fallback
}
