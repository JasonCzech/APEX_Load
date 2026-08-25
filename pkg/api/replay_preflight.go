package api

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
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

var replayTemplateRefPattern = regexp.MustCompile(`\{\{\s*(var|env|data|param|secret)\.([a-zA-Z0-9_.-]+)`)

const replayPreflightMaxControlDepth = 32

func buildReplayPreflight(script *dsl.Script, variables map[string]string, issues []string, warnings []string, resolvers ...engine.SecretResolver) *ReplayPreflight {
	var secretResolver engine.SecretResolver
	if len(resolvers) > 0 {
		secretResolver = resolvers[0]
	}
	controllerManagedSecrets := secretResolver != nil
	reviewGateIssues := replayReviewGateIssues(script)
	messagingIssues, messagingWarnings, messagingChecks := replayMessagingPreflight(script)
	actionCount := replayActionDefinitionCount(script)
	preflight := &ReplayPreflight{
		Valid:                len(issues) == 0,
		ActionCount:          actionCount,
		ExpectedActionCount:  replayExpectedActionCount(script),
		DataSourceCount:      len(scriptDataSources(script)),
		ParameterCount:       len(scriptParameters(script)),
		BrowserRequired:      script != nil && strings.EqualFold(strings.TrimSpace(script.Protocol), "browser"),
		RuntimePrerequisites: replayRuntimePrerequisites(script),
		Issues:               appendUniqueStrings(append([]string(nil), issues...), append(reviewGateIssues, messagingIssues...)...),
		Warnings:             append(append([]string(nil), warnings...), messagingWarnings...),
	}
	preflight.MissingVariables = replayMissingVariables(script, variables)
	preflight.RequiredSecrets, preflight.MissingSecrets = replaySecretRefs(script, secretResolver)
	dataWarnings := replayDataSourceWarnings(script)
	if len(dataWarnings) > 0 {
		preflight.Warnings = append(preflight.Warnings, dataWarnings...)
	}
	if len(preflight.MissingSecrets) > 0 {
		if controllerManagedSecrets {
			preflight.Warnings = append(preflight.Warnings, fmt.Sprintf("configure project secret(s) before replay: %s", strings.Join(preflight.MissingSecrets, ", ")))
		} else {
			// Import and authoring previews intentionally have no governed
			// project resolver. They remain saveable and show configure-later
			// guidance instead of pretending the legacy environment probe is a
			// complete project-secret status check.
			preflight.Warnings = append(preflight.Warnings, fmt.Sprintf("secret(s) require project configuration before execution: %s", strings.Join(preflight.MissingSecrets, ", ")))
		}
	}
	preflight.Valid = preflight.Valid && len(reviewGateIssues) == 0 && len(messagingIssues) == 0 && len(preflight.MissingVariables) == 0 && len(dataWarnings) == 0 &&
		(!controllerManagedSecrets || len(preflight.MissingSecrets) == 0)
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
			Status: replayCheckStatus(!controllerManagedSecrets || len(preflight.MissingSecrets) == 0, len(preflight.RequiredSecrets) > 0),
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
	preflight.Checks = append(preflight.Checks, messagingChecks...)
	return preflight
}

func replayMessagingPreflight(script *dsl.Script) (issues []string, warnings []string, checks []ReplayPreflightCheck) {
	if script == nil {
		return nil, nil, nil
	}
	switch strings.TrimSpace(script.Protocol) {
	case "kafka":
		kafka := script.Config.Kafka
		var detail []string
		if kafka.Mock {
			detail = append(detail, "mock broker enabled")
		} else {
			if len(kafka.Brokers) == 0 {
				issues = append(issues, "config.kafka.brokers is required when mock mode is disabled")
			}
			detail = append(detail, "real Kafka adapter required (-tags kafka_real)")
		}
		if strings.EqualFold(strings.TrimSpace(kafka.Profile), "eventhubs") && !kafka.Mock {
			if strings.EqualFold(strings.TrimSpace(kafka.SASLMechanism), "plain") || strings.TrimSpace(kafka.SASLMechanism) == "" {
				if strings.TrimSpace(kafka.SASLPassword) == "" && strings.TrimSpace(kafka.SASLPasswordSecret) == "" {
					issues = append(issues, "Event Hubs Kafka profile requires sasl_password_secret or sasl_password before live replay")
				}
			}
		}
		if ser := strings.ToLower(strings.TrimSpace(kafka.Serialization)); ser != "" && ser != "raw" {
			if strings.TrimSpace(kafka.SchemaRegistryURL) == "" && kafka.SchemaID <= 0 {
				issues = append(issues, "config.kafka.serialization requires schema_registry_url or schema_id")
			}
			if ser == "avro" && strings.TrimSpace(kafka.Schema) == "" {
				issues = append(issues, "config.kafka.serialization avro requires config.kafka.schema")
			}
			detail = append(detail, "serialization="+ser)
		}
		checks = append(checks, ReplayPreflightCheck{
			ID:     "messaging_kafka",
			Label:  "Kafka readiness",
			Status: replayCheckStatus(len(issues) == 0, len(warnings) > 0 || kafka.Mock),
			Detail: replayListDetail(issues, strings.Join(detail, "; ")),
		})
	case "jms-solace", "jms-servicebus", "jms-ibmmq":
		jms := script.Config.JMS
		if jms.Mock {
			warnings = append(warnings, "config.jms.mock is enabled; replay uses the in-memory JMS broker")
		} else {
			if strings.TrimSpace(jms.BrokerURL) == "" {
				issues = append(issues, "config.jms.broker_url is required when mock mode is disabled")
			}
		}
		checks = append(checks, ReplayPreflightCheck{
			ID:     "messaging_jms",
			Label:  "JMS readiness",
			Status: replayCheckStatus(len(issues) == 0, len(warnings) > 0 || jms.Mock),
			Detail: replayListDetail(issues, "JMS runtime configuration is ready."),
		})
	}
	return issues, warnings, checks
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
	return requestimport.MissingVariables(script, variables)
}

func replaySecretRefs(script *dsl.Script, resolvers ...engine.SecretResolver) ([]string, []string) {
	if script == nil {
		return nil, nil
	}
	var resolver engine.SecretResolver
	if len(resolvers) > 0 {
		resolver = resolvers[0]
	}
	required := make(map[string]struct{})
	missing := make(map[string]struct{})
	checkedSecrets := make(map[string]struct{})
	secretNames, err := engine.ReferencedSecretNames(script)
	if err != nil {
		secretNames = nil
		for _, action := range replayOrderedActions(script) {
			secretNames = append(secretNames, replayTemplateRefs(action, "secret")...)
		}
	}
	if resolver != nil {
		ctx, cancel := engine.WithSecretResolutionBudget(context.Background())
		_, unresolved, resolutionErr := engine.ResolveSecretNames(ctx, secretNames, resolver)
		cancel()
		if resolutionErr != nil {
			unresolved = append([]string(nil), secretNames...)
		}
		for _, ref := range secretNames {
			required[ref] = struct{}{}
		}
		for _, ref := range unresolved {
			missing[ref] = struct{}{}
		}
		return sortedSetKeys(required), sortedSetKeys(missing)
	}
	for _, ref := range secretNames {
		if _, checked := checkedSecrets[ref]; checked {
			continue
		}
		checkedSecrets[ref] = struct{}{}
		required[ref] = struct{}{}
		available := false
		available = replaySecretAvailable(script.ProjectID, ref)
		if !available {
			missing[ref] = struct{}{}
		}
	}
	return sortedSetKeys(required), sortedSetKeys(missing)
}

func replaySecretAvailable(projectID, name string) bool {
	return engine.ProjectScriptSecretEnvAvailable(projectID, name)
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
		if strings.EqualFold(strings.TrimSpace(source.Type), "file") {
			if strings.TrimSpace(source.Path) == "" && strings.TrimSpace(source.InlineData) == "" && strings.TrimSpace(source.StorageRef) == "" {
				warnings = append(warnings, fmt.Sprintf("file data source %q has no inline_data, path, or storage_ref", nonEmptyLabel(name, "unnamed")))
			}
			if strings.Contains(source.InlineData, "APEXLOAD_OPENAPI_FILE_PLACEHOLDER") {
				warnings = append(warnings, fmt.Sprintf("file data source %q contains a generated OpenAPI placeholder; upload the real file before replay", nonEmptyLabel(name, "unnamed")))
			}
			if source.InlineData == requestimport.MissingFilePlaceholder {
				warnings = append(warnings, fmt.Sprintf("file data source %q requires an import file binding before replay", nonEmptyLabel(name, "unnamed")))
			}
			continue
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
		case "control_switch":
			ordered = append(ordered, replayControlShell(action))
			if action.Control != nil && depth < replayPreflightMaxControlDepth {
				for _, branch := range action.Control.ActionBranches() {
					ordered = append(ordered, flattenReplayActionsForReadiness(branch.Actions, depth+1)...)
				}
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
	control.Cases = append([]dsl.SwitchCase(nil), action.Control.Cases...)
	for index := range control.Cases {
		control.Cases[index].Steps = nil
	}
	control.Default = nil
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
		case "control_switch":
			if action.Control != nil && depth < replayPreflightMaxControlDepth {
				for _, branch := range action.Control.ActionBranches() {
					count += replayDefinitionCount(branch.Actions, depth+1)
				}
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
		case "control_switch":
			if action.Control == nil || depth >= replayPreflightMaxControlDepth {
				continue
			}
			maxBranchCount := 0
			for _, branch := range action.Control.ActionBranches() {
				branchCount := replayExpectedCount(branch.Actions, depth+1)
				if branchCount > maxBranchCount {
					maxBranchCount = branchCount
				}
			}
			count += maxBranchCount
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
		return "No project secret references detected."
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
