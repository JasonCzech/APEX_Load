package importreview

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"regexp"
	"sort"
	"strings"
)

type ReplayPreflight struct {
	Valid                bool     `json:"valid"`
	ActionCount          int      `json:"action_count"`
	ExpectedActionCount  int      `json:"expected_action_count"`
	DataSourceCount      int      `json:"data_source_count"`
	ParameterCount       int      `json:"parameter_count"`
	BrowserRequired      bool     `json:"browser_required"`
	MissingVariables     []string `json:"missing_variables,omitempty"`
	BlockingVariables    []string `json:"blocking_variables,omitempty"`
	PlaceholderVariables []string `json:"placeholder_variables,omitempty"`
	// LateProducedVariables are missing variables that a step of the script
	// does write, only not on every path before their use: the producer is
	// out of order or conditional, not absent.
	LateProducedVariables []string `json:"late_produced_variables,omitempty"`
	// ParamBlockingVariables and ConfigBlockingVariables are the blocking
	// variables that an undeclared {{param.*}} use or a protocol configuration
	// reference blocks. No step can supply them, so a client words their fix
	// (declare a parameter, write a literal configuration value) apart from
	// producer order, even where an unrelated use of the name is late. A
	// computed preflight always sends both, empty when none, so a client can
	// trust them without analyzing the script; omitzero drops only the nil
	// lists of a preflight an older controller stamped without them.
	ParamBlockingVariables  []string               `json:"param_blocking_variables,omitzero"`
	ConfigBlockingVariables []string               `json:"config_blocking_variables,omitzero"`
	DataSourceIssues        []string               `json:"data_source_issues,omitempty"`
	RequiredSecrets         []string               `json:"required_secrets,omitempty"`
	MissingSecrets          []string               `json:"missing_secrets,omitempty"`
	RuntimePrerequisites    []string               `json:"runtime_prerequisites,omitempty"`
	Issues                  []string               `json:"issues,omitempty"`
	Warnings                []string               `json:"warnings,omitempty"`
	Checks                  []ReplayPreflightCheck `json:"checks,omitempty"`
}

type ReplayPreflightCheck struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

var ReplayTemplateRefPattern = regexp.MustCompile(`\{\{\s*(var|env|data|param|secret)\.([a-zA-Z0-9_.-]+)`)

const ReplayPreflightMaxControlDepth = 32

func BuildReplayPreflight(script *dsl.Script, variables map[string]string, issues []string, warnings []string, resolvers ...engine.SecretResolver) *ReplayPreflight {
	var secretResolver engine.SecretResolver
	if len(resolvers) > 0 {
		secretResolver = resolvers[0]
	}
	controllerManagedSecrets := secretResolver != nil
	reviewGateIssues := ReplayReviewGateIssues(script)
	messagingIssues, messagingWarnings, messagingChecks := ReplayMessagingPreflight(script)
	actionCount := ReplayActionDefinitionCount(script)
	preflight := &ReplayPreflight{
		Valid:                len(issues) == 0,
		ActionCount:          actionCount,
		ExpectedActionCount:  ReplayExpectedActionCount(script),
		DataSourceCount:      len(ScriptDataSources(script)),
		ParameterCount:       len(ScriptParameters(script)),
		BrowserRequired:      script != nil && strings.EqualFold(strings.TrimSpace(script.Protocol), "browser"),
		RuntimePrerequisites: ReplayRuntimePrerequisites(script),
		Issues:               AppendUniqueStrings(append([]string(nil), issues...), append(reviewGateIssues, messagingIssues...)...),
		Warnings:             append(append([]string(nil), warnings...), messagingWarnings...),
	}
	readiness := requestimport.AnalyzeVariableReadiness(script, variables)
	preflight.MissingVariables = readiness.Missing
	// Blocking variables refuse replay; placeholder variables are sent as their
	// placeholder text. They split MissingVariables so a client can list each
	// one without parsing the checks' display text.
	preflight.BlockingVariables = append([]string(nil), readiness.Blocking...)
	preflight.LateProducedVariables = append([]string(nil), readiness.LateProduced...)
	preflight.ParamBlockingVariables = append([]string{}, readiness.ParamBlocking...)
	preflight.ConfigBlockingVariables = append([]string{}, readiness.ConfigBlocking...)
	// HTTP replay, and SOAP and Java HTTP replay through the same adapter,
	// keeps a missing variable as its placeholder text, and the request that
	// sends it warns, as the load-test runtime does. Only references with no
	// placeholder fallback refuse replay; an importer that must never send a
	// placeholder stamps an execution gate instead.
	if placeholders := ReplayPlaceholderVariables(readiness); len(placeholders) > 0 {
		preflight.PlaceholderVariables = placeholders
		preflight.Warnings = append(preflight.Warnings, "variable(s) with no seed or preceding extractor are sent as placeholder text: "+strings.Join(placeholders, ", "))
	}
	preflight.RequiredSecrets, preflight.MissingSecrets = ReplaySecretRefs(script, secretResolver)
	dataWarnings := ReplayDataSourceWarnings(script)
	if len(dataWarnings) > 0 {
		preflight.Warnings = append(preflight.Warnings, dataWarnings...)
		// They are worded as warnings but refuse replay (see Valid below), so
		// they are also listed on their own for a client to show as blockers.
		preflight.DataSourceIssues = append([]string(nil), dataWarnings...)
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
	preflight.Valid = preflight.Valid && len(reviewGateIssues) == 0 && len(messagingIssues) == 0 && len(readiness.Blocking) == 0 && len(dataWarnings) == 0 &&
		(!controllerManagedSecrets || len(preflight.MissingSecrets) == 0)
	preflight.Checks = []ReplayPreflightCheck{
		{
			ID:     "validation",
			Label:  "Script validation",
			Status: ReplayCheckStatus(len(issues) == 0, len(warnings) > 0),
			Detail: ReplayCheckDetail(len(issues), len(warnings), "validation issue", "validation warning"),
		},
		{
			ID:     "review_gates",
			Label:  "Review gates",
			Status: ReplayCheckStatus(len(reviewGateIssues) == 0, false),
			Detail: ReplayListDetail(reviewGateIssues, "No review-gated actions block replay."),
		},
		{
			ID:     "variables",
			Label:  "Variable readiness",
			Status: ReplayCheckStatus(len(readiness.Blocking) == 0, len(preflight.MissingVariables) > 0),
			Detail: ReplayVariableDetail(readiness),
		},
		{
			ID:     "secrets",
			Label:  "Secret bindings",
			Status: ReplayCheckStatus(!controllerManagedSecrets || len(preflight.MissingSecrets) == 0, len(preflight.RequiredSecrets) > 0),
			Detail: ReplaySecretDetail(preflight.RequiredSecrets, preflight.MissingSecrets),
		},
		{
			ID:     "data_sources",
			Label:  "Data source readiness",
			Status: ReplayCheckStatus(len(dataWarnings) == 0, len(ScriptDataSources(script)) > 0),
			Detail: ReplayDataSourceDetail(script, dataWarnings),
		},
		{
			ID:     "runtime",
			Label:  "Runtime prerequisites",
			Status: "warn",
			Detail: strings.Join(preflight.RuntimePrerequisites, "; "),
		},
	}
	if preflight.BrowserRequired {
		preflight.Checks = append(preflight.Checks, ReplayEvidenceCaptureCheck(script))
	}
	preflight.Checks = append(preflight.Checks, messagingChecks...)
	return preflight
}

func ReplayMessagingPreflight(script *dsl.Script) (issues []string, warnings []string, checks []ReplayPreflightCheck) {
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
			Status: ReplayCheckStatus(len(issues) == 0, len(warnings) > 0 || kafka.Mock),
			Detail: ReplayListDetail(issues, strings.Join(detail, "; ")),
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
			Status: ReplayCheckStatus(len(issues) == 0, len(warnings) > 0 || jms.Mock),
			Detail: ReplayListDetail(issues, "JMS runtime configuration is ready."),
		})
	}
	return issues, warnings, checks
}

func CloneReplayPreflight(preflight *ReplayPreflight) *ReplayPreflight {
	if preflight == nil {
		return nil
	}
	cloned := *preflight
	cloned.MissingVariables = append([]string(nil), preflight.MissingVariables...)
	cloned.BlockingVariables = append([]string(nil), preflight.BlockingVariables...)
	cloned.PlaceholderVariables = append([]string(nil), preflight.PlaceholderVariables...)
	cloned.LateProducedVariables = append([]string(nil), preflight.LateProducedVariables...)
	// A computed preflight's empty kinds stay non-nil, so the clone still
	// sends them; an older stamp's nil kinds stay nil.
	cloned.ParamBlockingVariables = cloneKindList(preflight.ParamBlockingVariables)
	cloned.ConfigBlockingVariables = cloneKindList(preflight.ConfigBlockingVariables)
	cloned.DataSourceIssues = append([]string(nil), preflight.DataSourceIssues...)
	cloned.RequiredSecrets = append([]string(nil), preflight.RequiredSecrets...)
	cloned.MissingSecrets = append([]string(nil), preflight.MissingSecrets...)
	cloned.RuntimePrerequisites = append([]string(nil), preflight.RuntimePrerequisites...)
	cloned.Issues = append([]string(nil), preflight.Issues...)
	cloned.Warnings = append([]string(nil), preflight.Warnings...)
	cloned.Checks = append([]ReplayPreflightCheck(nil), preflight.Checks...)
	return &cloned
}

func cloneKindList(values []string) []string {
	if values == nil {
		return nil
	}
	return append(make([]string, 0, len(values)), values...)
}

func ReplayActionDefinitionCount(script *dsl.Script) int {
	if script == nil {
		return 0
	}
	return ReplayDefinitionCount(ReplayPhaseActions(script), 0)
}

func ReplayExpectedActionCount(script *dsl.Script) int {
	if script == nil {
		return 0
	}
	return ReplayExpectedCount(ReplayPhaseActions(script), 0)
}

func ScriptDataSources(script *dsl.Script) []dsl.DataSource {
	if script == nil {
		return nil
	}
	return script.DataSources
}

func ScriptParameters(script *dsl.Script) []dsl.Parameter {
	if script == nil {
		return nil
	}
	return script.Parameters
}

func ReplayRuntimePrerequisites(script *dsl.Script) []string {
	if script == nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(script.Protocol), "browser") {
		switch strings.TrimSpace(script.Protocol) {
		case "sap_gui":
			return []string{"SAP GUI execution is unavailable: no bundled runtime adapter. Save a reference artifact for review."}
		case "java_vuser":
			return []string{"Java runner availability and authoring approval are not verified by static checks. Execution requires an approved draft and a compatible governed JVM runner."}
		default:
			return []string{"Required protocol: " + strings.TrimSpace(script.Protocol), "Runner availability and target connectivity are checked when debugging starts."}
		}
	}
	engine := strings.TrimSpace(script.Config.Browser.Engine)
	if engine == "" {
		engine = "chromium"
	}
	prereqs := []string{"Browser engine: " + engine, "Browser runtime availability is checked when debugging starts."}
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

func ReplayEvidenceCaptureCheck(script *dsl.Script) ReplayPreflightCheck {
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

func ReplayPlaceholderVariables(readiness requestimport.VariableReadiness) []string {
	blocking := make(map[string]struct{}, len(readiness.Blocking))
	for _, name := range readiness.Blocking {
		blocking[name] = struct{}{}
	}
	var placeholders []string
	for _, name := range readiness.Missing {
		if _, ok := blocking[name]; !ok {
			placeholders = append(placeholders, name)
		}
	}
	return placeholders
}

func ReplayVariableDetail(readiness requestimport.VariableReadiness) string {
	if len(readiness.Missing) == 0 {
		return "All replay variables are seeded or extracted before use."
	}
	var parts []string
	if len(readiness.Blocking) > 0 {
		parts = append(parts, "Unresolved: "+strings.Join(readiness.Blocking, ", "))
	}
	if placeholders := ReplayPlaceholderVariables(readiness); len(placeholders) > 0 {
		parts = append(parts, "Sent as placeholder text: "+strings.Join(placeholders, ", "))
	}
	return strings.Join(parts, "; ")
}

func ReplaySecretRefs(script *dsl.Script, resolvers ...engine.SecretResolver) ([]string, []string) {
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
		for _, action := range ReplayOrderedActions(script) {
			secretNames = append(secretNames, ReplayTemplateRefs(action, "secret")...)
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
		return SortedSetKeys(required), SortedSetKeys(missing)
	}
	for _, ref := range secretNames {
		if _, checked := checkedSecrets[ref]; checked {
			continue
		}
		checkedSecrets[ref] = struct{}{}
		required[ref] = struct{}{}
		available := false
		available = ReplaySecretAvailable(script.ProjectID, ref)
		if !available {
			missing[ref] = struct{}{}
		}
	}
	return SortedSetKeys(required), SortedSetKeys(missing)
}

func ReplaySecretAvailable(projectID, name string) bool {
	return engine.ProjectScriptSecretEnvAvailable(projectID, name)
}

func ReplayDataSourceWarnings(script *dsl.Script) []string {
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
				warnings = append(warnings, fmt.Sprintf("file data source %q has no inline_data, path, or storage_ref", NonEmptyLabel(name, "unnamed")))
			}
			if strings.Contains(source.InlineData, "APEXLOAD_OPENAPI_FILE_PLACEHOLDER") {
				warnings = append(warnings, fmt.Sprintf("file data source %q contains a generated OpenAPI placeholder; upload the real file before replay", NonEmptyLabel(name, "unnamed")))
			}
			if source.InlineData == requestimport.MissingFilePlaceholder {
				warnings = append(warnings, fmt.Sprintf("file data source %q requires an import file binding before replay", NonEmptyLabel(name, "unnamed")))
			}
			continue
		}
		if strings.TrimSpace(source.Path) == "" && strings.TrimSpace(source.InlineData) == "" {
			warnings = append(warnings, fmt.Sprintf("data source %q has no path or inline data", NonEmptyLabel(name, "unnamed")))
		}
	}
	for _, parameter := range script.Parameters {
		sourceName := strings.TrimSpace(parameter.DataSource)
		if sourceName == "" {
			continue
		}
		if _, ok := sourceNames[sourceName]; !ok {
			warnings = append(warnings, fmt.Sprintf("parameter %q references missing data source %q", NonEmptyLabel(parameter.Name, "unnamed"), sourceName))
		}
	}
	sort.Strings(warnings)
	return warnings
}

func ReplayPhaseActions(script *dsl.Script) []dsl.Action {
	if script == nil {
		return nil
	}
	actions := make([]dsl.Action, 0, len(script.Init)+len(script.Actions)+len(script.Teardown))
	actions = append(actions, script.Init...)
	actions = append(actions, script.Actions...)
	actions = append(actions, script.Teardown...)
	return actions
}

func ReplayOrderedActions(script *dsl.Script) []dsl.Action {
	return FlattenReplayActionsForReadiness(ReplayPhaseActions(script), 0)
}

func FlattenReplayActionsForReadiness(actions []dsl.Action, depth int) []dsl.Action {
	ordered := make([]dsl.Action, 0, len(actions))
	for _, action := range actions {
		switch strings.ToLower(strings.TrimSpace(action.Type)) {
		case "control_if":
			ordered = append(ordered, ReplayControlShell(action))
			if action.Control != nil && depth < ReplayPreflightMaxControlDepth {
				ordered = append(ordered, FlattenReplayActionsForReadiness(action.Control.Steps, depth+1)...)
				ordered = append(ordered, FlattenReplayActionsForReadiness(action.Control.Else, depth+1)...)
			}
		case "control_switch":
			ordered = append(ordered, ReplayControlShell(action))
			if action.Control != nil && depth < ReplayPreflightMaxControlDepth {
				for _, branch := range action.Control.ActionBranches() {
					ordered = append(ordered, FlattenReplayActionsForReadiness(branch.Actions, depth+1)...)
				}
			}
		case "control_loop":
			ordered = append(ordered, ReplayControlShell(action))
			if action.Control != nil && depth < ReplayPreflightMaxControlDepth {
				ordered = append(ordered, FlattenReplayActionsForReadiness(action.Control.Steps, depth+1)...)
			}
		default:
			ordered = append(ordered, action)
		}
	}
	return ordered
}

func ReplayControlShell(action dsl.Action) dsl.Action {
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

func ReplayDefinitionCount(actions []dsl.Action, depth int) int {
	var count int
	for _, action := range actions {
		switch strings.ToLower(strings.TrimSpace(action.Type)) {
		case "control_if":
			if action.Control != nil && depth < ReplayPreflightMaxControlDepth {
				count += ReplayDefinitionCount(action.Control.Steps, depth+1)
				count += ReplayDefinitionCount(action.Control.Else, depth+1)
			}
		case "control_switch":
			if action.Control != nil && depth < ReplayPreflightMaxControlDepth {
				for _, branch := range action.Control.ActionBranches() {
					count += ReplayDefinitionCount(branch.Actions, depth+1)
				}
			}
		case "control_loop":
			if action.Control != nil && depth < ReplayPreflightMaxControlDepth {
				count += ReplayDefinitionCount(action.Control.Steps, depth+1)
			}
		default:
			count++
		}
	}
	return count
}

func ReplayExpectedCount(actions []dsl.Action, depth int) int {
	var count int
	for _, action := range actions {
		switch strings.ToLower(strings.TrimSpace(action.Type)) {
		case "control_if":
			if action.Control == nil || depth >= ReplayPreflightMaxControlDepth {
				continue
			}
			thenCount := ReplayExpectedCount(action.Control.Steps, depth+1)
			elseCount := ReplayExpectedCount(action.Control.Else, depth+1)
			if elseCount > thenCount {
				count += elseCount
			} else {
				count += thenCount
			}
		case "control_switch":
			if action.Control == nil || depth >= ReplayPreflightMaxControlDepth {
				continue
			}
			maxBranchCount := 0
			for _, branch := range action.Control.ActionBranches() {
				branchCount := ReplayExpectedCount(branch.Actions, depth+1)
				if branchCount > maxBranchCount {
					maxBranchCount = branchCount
				}
			}
			count += maxBranchCount
		case "control_loop":
			if action.Control == nil || depth >= ReplayPreflightMaxControlDepth {
				continue
			}
			bodyCount := ReplayExpectedCount(action.Control.Steps, depth+1)
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

func ReplayTemplateRefs(action dsl.Action, kind string) []string {
	payload, err := json.Marshal(action)
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{})
	for _, match := range ReplayTemplateRefPattern.FindAllStringSubmatch(string(payload), -1) {
		if len(match) < 3 || match[1] != kind {
			continue
		}
		if ref := strings.TrimSpace(match[2]); ref != "" {
			seen[ref] = struct{}{}
		}
	}
	return SortedSetKeys(seen)
}

func SortedSetKeys(values map[string]struct{}) []string {
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

func ReplayCheckStatus(pass bool, warn bool) string {
	if !pass {
		return "fail"
	}
	if warn {
		return "warn"
	}
	return "pass"
}

func ReplayCheckDetail(primaryCount int, secondaryCount int, primaryLabel string, secondaryLabel string) string {
	parts := make([]string, 0, 2)
	if primaryCount > 0 {
		parts = append(parts, fmt.Sprintf("%d %s%s", primaryCount, primaryLabel, ReplayPluralSuffix(primaryCount)))
	}
	if secondaryCount > 0 {
		parts = append(parts, fmt.Sprintf("%d %s%s", secondaryCount, secondaryLabel, ReplayPluralSuffix(secondaryCount)))
	}
	if len(parts) == 0 {
		return "Ready"
	}
	return strings.Join(parts, "; ")
}

func ReplayListDetail(values []string, empty string) string {
	if len(values) == 0 {
		return empty
	}
	return strings.Join(values, ", ")
}

func ReplaySecretDetail(required []string, missing []string) string {
	if len(required) == 0 {
		return "No project secret references detected."
	}
	if len(missing) > 0 {
		return "Missing: " + strings.Join(missing, ", ")
	}
	return "Required: " + strings.Join(required, ", ")
}

func ReplayDataSourceDetail(script *dsl.Script, warnings []string) string {
	if len(warnings) > 0 {
		return strings.Join(warnings, "; ")
	}
	count := len(ScriptDataSources(script))
	if count == 0 {
		return "No data sources configured."
	}
	return fmt.Sprintf("%d data source%s ready", count, ReplayPluralSuffix(count))
}

func ReplayPluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func NonEmptyLabel(value string, fallback string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return fallback
}
