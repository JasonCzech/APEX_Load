package dsl

import (
	"bytes"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/http/httpguts"

	"github.com/jasonczech/apexload/pkg/fileasset"
	"github.com/jasonczech/apexload/pkg/internal/bodyencoding"
	"github.com/jasonczech/apexload/pkg/protocol"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

// ValidateScriptIssues returns all validation issues for a script definition.
func ValidateScriptIssues(script *Script) []string {
	return validateScriptIssues(script, false)
}

// ValidateScriptIssuesWithPreservedImportCredentials validates a
// server-persisted import execution snapshot. It differs from ordinary
// validation only for credential-bearing Java VM arguments: exact imported
// argv is allowed, while every structural and process-control rule remains in
// force. Callers must use this only for a script loaded from a trusted import
// persistence/session path, never merely because client JSON carries an import
// metadata marker.
func ValidateScriptIssuesWithPreservedImportCredentials(script *Script) []string {
	return validateScriptIssues(script, true)
}

func validateScriptIssues(script *Script, allowPreservedJavaVMCredentials bool) []string {
	if script == nil {
		return []string{"script is required"}
	}
	// Run this iterative structural preflight before any protocol-specific or
	// recursive validators. Otherwise an API-sized but adversarial control tree
	// can amplify CPU/stack use in each validation pass.
	if issues := validateScriptStructureIssues(script); len(issues) != 0 {
		return issues
	}
	if strings.EqualFold(strings.TrimSpace(script.Protocol), "bruno") {
		return []string{BrunoRunnerRetiredMessage}
	}
	if issues := validateRunIdentityTemplateIssues(script); len(issues) != 0 {
		return issues
	}
	fileDataSourceIssues := validateFileDataSourceReferenceIssues(script)

	var issues []string
	if strings.TrimSpace(script.Name) == "" {
		issues = append(issues, "script name is required")
	}
	if strings.TrimSpace(script.Protocol) == "" {
		issues = append(issues, "script protocol is required")
	}
	if len(script.Actions) == 0 {
		issues = append(issues, "script must have at least one action")
	}
	if script.Protocol != "" && !protocolAllowsEmptyBaseURL(script.Protocol) && strings.TrimSpace(script.Config.BaseURL) == "" {
		issues = append(issues, "config.base_url is empty")
	}

	for i, action := range script.Init {
		issues = append(issues, validateActionForProtocolAtPath(script.Protocol, fmt.Sprintf("init[%d]", i), i, action)...)
	}
	for i, action := range script.Actions {
		issues = append(issues, validateActionForProtocol(script.Protocol, i, action)...)
	}
	for i, action := range script.Teardown {
		issues = append(issues, validateActionForProtocolAtPath(script.Protocol, fmt.Sprintf("teardown[%d]", i), i, action)...)
	}
	// Pacing, run-logic, and think-time limits are protocol-independent. Running
	// this only from messaging validators left HTTP/SOAP/browser scripts—and the
	// enterprise archive importer—without public runtime validation.
	issues = append(issues, validateExecutionResourceIssues(script)...)

	// Messaging protocols have additional configuration and action semantics,
	// but they still participate in the common init/action/teardown and shared
	// script validation above and below. Keeping these as additive validators
	// prevents an early return from skipping lifecycle, data-source, file,
	// extractor, and nested-control checks.
	switch ProtocolFamily(script.Protocol) {
	case "jms":
		issues = append(issues, validateJMSScriptIssues(script)...)
	case "kafka":
		issues = append(issues, validateKafkaScriptIssues(script)...)
	case "eventhubs":
		issues = append(issues, validateEventHubsScriptIssues(script)...)
	}

	if script.Protocol == "soap" {
		issues = append(issues, validateSOAPScriptIssues(script)...)
	}
	if script.Protocol == "java_http" {
		issues = append(issues, validateJavaHTTPScriptIssues(script)...)
	}
	if script.Protocol == "java_vuser" {
		issues = append(issues, validateJavaVuserScriptIssuesWithPolicy(script, allowPreservedJavaVMCredentials)...)
	}
	if script.Protocol == "database" {
		issues = append(issues, validateDatabaseScriptIssues(script)...)
	}
	if script.Protocol == "network" {
		issues = append(issues, validateNetworkScriptIssues(script)...)
	}
	if script.Protocol == "sap_gui" {
		issues = append(issues, validateSAPGUIScriptIssues(script)...)
	}

	issues = append(issues, validateDataSourceIssues(script)...)
	issues = append(issues, fileDataSourceIssues...)
	issues = append(issues, validateFileReferenceIssues(script)...)
	issues = append(issues, validateBrowserConfigIssues(script)...)
	issues = append(issues, validateBrowserEmulationIssues(script)...)
	issues = append(issues, validateWebSocketConfigIssues(script)...)
	issues = append(issues, validateGRPCConfigIssues(script)...)
	issues = append(issues, validateWebRuntimeIssues(script)...)
	issues = append(issues, validateHTTPHeaderContractIssues(script)...)
	issues = append(issues, validateHTTP3PolicyIssues(script)...)
	issues = append(issues, validateGoalConfigIssues(script.GoalConfig)...)

	return issues
}

var runIdentityTemplatePattern = regexp.MustCompile(`\{\{([^{}]*)\}\}`)

// Run metadata is supplied by the scheduler, not by authored script variables.
// Keep the namespace narrow so a typo cannot silently reach an HTTP target as
// a literal placeholder. Only fields the VUser actually interpolates may use
// run.id; identities, map keys, and pre-run configuration remain literal.
// Re-encoding the whole script as JSON here would hide normal resource
// validation for non-finite numeric inputs.
func validateRunIdentityTemplateIssues(script *Script) []string {
	seen := make(map[runTemplateVisit]struct{})
	switch inspectRunIdentityTemplates(reflect.ValueOf(script), seen, 0) {
	case runTemplateUnsupported:
		return []string{"unsupported run template; only {{run.id}} is available"}
	case runTemplateDepthExceeded:
		return []string{"run template inspection depth exceeded"}
	}
	// Other script/config fields are consumed before a run identity is bound,
	// sometimes directly by network protocol initialization.
	outsideRuntimeTemplates := *script
	outsideRuntimeTemplates.Init = nil
	outsideRuntimeTemplates.Actions = nil
	outsideRuntimeTemplates.Teardown = nil
	outsideRuntimeTemplates.Config.HTTP.DefaultHeaders = nil
	outsideRuntimeTemplates.Config.HTTP.DefaultHeaderList = nil
	if inspectRunIdentityTemplatesMatching(reflect.ValueOf(&outsideRuntimeTemplates), make(map[runTemplateVisit]struct{}), 0,
		isRunIDTemplate) == runTemplateUnsupported {
		return []string{runIDTemplateScopeIssue}
	}
	usesHTTPHeaders := false
	switch strings.ToLower(strings.TrimSpace(script.Protocol)) {
	case "", "http", "soap", "java_http":
		usesHTTPHeaders = true
	}
	for name := range script.Config.HTTP.DefaultHeaders {
		if runIDTemplateInString(name) {
			return []string{runIDTemplateScopeIssue}
		}
	}
	for _, header := range script.Config.HTTP.DefaultHeaderList {
		if runIDTemplateInString(header.Name) {
			return []string{runIDTemplateScopeIssue}
		}
	}
	if !usesHTTPHeaders && (runIDTemplateInValue(script.Config.HTTP.DefaultHeaders) || runIDTemplateInValue(script.Config.HTTP.DefaultHeaderList)) {
		return []string{runIDTemplateScopeIssue}
	}
	for _, actions := range [][]Action{script.Init, script.Actions, script.Teardown} {
		for _, action := range actions {
			switch inspectRunIDActionScope(action, 0) {
			case runTemplateUnsupported:
				return []string{runIDTemplateScopeIssue}
			case runTemplateDepthExceeded:
				return []string{"run template inspection depth exceeded"}
			}
		}
	}
	return nil
}

const runIDTemplateScopeIssue = "{{run.id}} is supported only in runtime-interpolated action values and HTTP default header values"

func isRunIDTemplate(expr string) bool { return expr == "run.id" }

func runIDTemplateInString(value string) bool {
	for _, match := range runIdentityTemplatePattern.FindAllStringSubmatch(value, -1) {
		if strings.TrimSpace(match[1]) == "run.id" {
			return true
		}
	}
	return false
}

func runIDTemplateInValue(value any) bool {
	return inspectRunIdentityTemplatesMatching(reflect.ValueOf(value), make(map[runTemplateVisit]struct{}), 0, isRunIDTemplate) == runTemplateUnsupported
}

func runIDTemplateInLiteralConditionField(condition *Condition) bool {
	return condition != nil && (runIDTemplateInString(condition.Expr) || runIDTemplateInString(condition.Op) ||
		strings.TrimSpace(condition.Expr) != "" && (runIDTemplateInString(condition.Left) || runIDTemplateInString(condition.Right)))
}

// inspectRunIDRuntimeAny mirrors VUser.interpolateAny: only strings, []string,
// []any, and map[string]any values are interpolated. Map keys and arbitrary
// typed containers are passed through unchanged.
func inspectRunIDRuntimeAny(value any, seen map[runTemplateVisit]struct{}, depth int) runTemplateScanResult {
	if depth > 128 {
		return runTemplateDepthExceeded
	}
	switch typed := value.(type) {
	case string, []string:
		return runTemplateValid
	case []any:
		for _, item := range typed {
			if result := inspectRunIDRuntimeAny(item, seen, depth+1); result != runTemplateValid {
				return result
			}
		}
	case map[string]any:
		reflected := reflect.ValueOf(typed)
		visit := runTemplateVisit{kind: reflect.Map, typeName: reflected.Type(), address: reflected.Pointer()}
		if _, visited := seen[visit]; visited {
			return runTemplateValid
		}
		seen[visit] = struct{}{}
		for key, item := range typed {
			if runIDTemplateInString(key) {
				return runTemplateUnsupported
			}
			if result := inspectRunIDRuntimeAny(item, seen, depth+1); result != runTemplateValid {
				return result
			}
		}
	default:
		return inspectRunIdentityTemplatesMatching(reflect.ValueOf(value), make(map[runTemplateVisit]struct{}), depth, isRunIDTemplate)
	}
	return runTemplateValid
}

func inspectRunIDActionScope(action Action, depth int) runTemplateScanResult {
	if depth > 128 {
		return runTemplateDepthExceeded
	}
	// Keep a copy containing only fields that resolveAction and the control
	// executor leave literal. Explicitly clear each interpolated field so a new
	// Action field defaults to disallowed until its runtime use is reviewed.
	inert := action
	inert.Target, inert.Selector, inert.Value, inert.Body = "", "", "", ""
	inert.Headers, inert.HeaderList, inert.Params, inert.Metadata = nil, nil, nil, nil
	inert.WaitFor, inert.Sync, inert.Attribution = nil, nil, nil
	inert.Extractors, inert.Assertions, inert.Auth, inert.SOAP, inert.Control = nil, nil, nil, nil, nil
	if runIDTemplateInValue(inert) {
		return runTemplateUnsupported
	}
	for name := range action.Headers {
		if runIDTemplateInString(name) {
			return runTemplateUnsupported
		}
	}
	for _, header := range action.HeaderList {
		if runIDTemplateInString(header.Name) {
			return runTemplateUnsupported
		}
	}
	for key, value := range action.Params {
		if runIDTemplateInString(key) {
			return runTemplateUnsupported
		}
		if strings.EqualFold(strings.TrimSpace(action.Type), "http_sequence") && key == "requests" {
			if result := inspectRunIDHTTPSequenceRequests(value, depth+1); result != runTemplateValid {
				return result
			}
			continue
		}
		if result := inspectRunIDRuntimeAny(value, make(map[runTemplateVisit]struct{}), depth+1); result != runTemplateValid {
			return result
		}
	}
	for key, value := range action.Metadata {
		if runIDTemplateInString(key) {
			return runTemplateUnsupported
		}
		if result := inspectRunIDRuntimeAny(value, make(map[runTemplateVisit]struct{}), depth+1); result != runTemplateValid {
			return result
		}
	}
	if action.WaitFor != nil && runIDTemplateInString(action.WaitFor.Type) ||
		action.Sync != nil && runIDTemplateInString(action.Sync.Type) ||
		action.Auth != nil && runIDTemplateInString(action.Auth.Type) {
		return runTemplateUnsupported
	}
	for _, extractor := range action.Extractors {
		extractor.Expression, extractor.DefaultValue = "", nil
		if runIDTemplateInValue(extractor) {
			return runTemplateUnsupported
		}
	}
	for _, assertion := range action.Assertions {
		if result := inspectRunIDRuntimeAny(assertion.Expected, make(map[runTemplateVisit]struct{}), depth+1); result != runTemplateValid {
			return result
		}
		assertion.Expected, assertion.Value = nil, ""
		if runIDTemplateInValue(assertion) {
			return runTemplateUnsupported
		}
	}
	if action.Control != nil {
		control := action.Control
		if runIDTemplateInLiteralConditionField(control.Condition) {
			return runTemplateUnsupported
		}
		if control.ForEach != nil {
			if runIDTemplateInString(control.ForEach.Source) || runIDTemplateInString(control.ForEach.Var) {
				return runTemplateUnsupported
			}
			if runIDTemplateInLiteralConditionField(control.ForEach.Condition) {
				return runTemplateUnsupported
			}
		}
		for _, branch := range control.Branches {
			if runIDTemplateInString(branch.Name) {
				return runTemplateUnsupported
			}
		}
		for _, switchCase := range control.Cases {
			if runIDTemplateInLiteralConditionField(switchCase.Condition) {
				return runTemplateUnsupported
			}
		}
		for _, branch := range control.ActionBranches() {
			for _, child := range branch.Actions {
				if result := inspectRunIDActionScope(child, depth+1); result != runTemplateValid {
					return result
				}
			}
		}
	}
	return runTemplateValid
}

func inspectRunIDHTTPSequenceRequests(raw any, depth int) runTemplateScanResult {
	var children []any
	switch typed := raw.(type) {
	case []any:
		children = typed
	case []Action:
		for _, child := range typed {
			children = append(children, child)
		}
	case []map[string]any:
		for _, child := range typed {
			children = append(children, child)
		}
	default:
		return inspectRunIDRuntimeAny(raw, make(map[runTemplateVisit]struct{}), depth)
	}
	for _, rawChild := range children {
		if !runIDTemplateInValue(rawChild) {
			continue
		}
		child, ok := rawChild.(Action)
		if !ok {
			encoded, err := json.Marshal(rawChild)
			if err != nil {
				return runTemplateUnsupported
			}
			decoder := json.NewDecoder(bytes.NewReader(encoded))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&child); err != nil {
				return runTemplateUnsupported
			}
		}
		if result := inspectRunIDActionScope(child, depth+1); result != runTemplateValid {
			return result
		}
	}
	return runTemplateValid
}

type runTemplateScanResult uint8

const (
	runTemplateValid runTemplateScanResult = iota
	runTemplateUnsupported
	runTemplateDepthExceeded
)

type runTemplateVisit struct {
	kind     reflect.Kind
	typeName reflect.Type
	address  uintptr
	length   int
}

func inspectRunIdentityTemplates(value reflect.Value, seen map[runTemplateVisit]struct{}, depth int) runTemplateScanResult {
	return inspectRunIdentityTemplatesMatching(value, seen, depth, func(expr string) bool {
		return strings.HasPrefix(expr, "run.") && expr != "run.id"
	})
}

func inspectRunIdentityTemplatesMatching(value reflect.Value, seen map[runTemplateVisit]struct{}, depth int, matches func(string) bool) runTemplateScanResult {
	// The structural preflight above bounds the action tree. Dynamic parameter
	// containers can still be cyclic, so stop there rather than recurse forever.
	if depth > 128 {
		return runTemplateDepthExceeded
	}
	if !value.IsValid() {
		return runTemplateValid
	}
	switch value.Kind() {
	case reflect.String:
		for _, match := range runIdentityTemplatePattern.FindAllStringSubmatch(value.String(), -1) {
			expr := strings.TrimSpace(match[1])
			if matches(expr) {
				return runTemplateUnsupported
			}
		}
	case reflect.Interface, reflect.Pointer:
		if value.IsNil() {
			return runTemplateValid
		}
		if value.Kind() == reflect.Pointer {
			visit := runTemplateVisit{kind: value.Kind(), typeName: value.Type(), address: value.Pointer()}
			if _, visited := seen[visit]; visited {
				return runTemplateValid
			}
			seen[visit] = struct{}{}
		}
		return inspectRunIdentityTemplatesMatching(value.Elem(), seen, depth+1, matches)
	case reflect.Map:
		if value.IsNil() {
			return runTemplateValid
		}
		visit := runTemplateVisit{kind: value.Kind(), typeName: value.Type(), address: value.Pointer()}
		if _, visited := seen[visit]; visited {
			return runTemplateValid
		}
		seen[visit] = struct{}{}
		iter := value.MapRange()
		for iter.Next() {
			if result := inspectRunIdentityTemplatesMatching(iter.Key(), seen, depth+1, matches); result != runTemplateValid {
				return result
			}
			if result := inspectRunIdentityTemplatesMatching(iter.Value(), seen, depth+1, matches); result != runTemplateValid {
				return result
			}
		}
	case reflect.Slice, reflect.Array:
		if value.Kind() == reflect.Slice {
			if value.IsNil() {
				return runTemplateValid
			}
			visit := runTemplateVisit{kind: value.Kind(), typeName: value.Type(), address: value.Pointer(), length: value.Len()}
			if _, visited := seen[visit]; visited {
				return runTemplateValid
			}
			seen[visit] = struct{}{}
		}
		for index := range value.Len() {
			if result := inspectRunIdentityTemplatesMatching(value.Index(index), seen, depth+1, matches); result != runTemplateValid {
				return result
			}
		}
	case reflect.Struct:
		for index := range value.NumField() {
			if value.Type().Field(index).PkgPath == "" {
				if result := inspectRunIdentityTemplatesMatching(value.Field(index), seen, depth+1, matches); result != runTemplateValid {
					return result
				}
			}
		}
	}
	return runTemplateValid
}

// validateScriptStructureIssues walks all lifecycle action trees without
// recursion and bounds both nesting and aggregate definitions. http_sequence
// children are included because their untyped request maps receive their own
// validation and runtime dispatch even though they are not Action values.
func validateScriptStructureIssues(script *Script) []string {
	type pendingAction struct {
		action Action
		depth  int
	}

	topLevelCount := len(script.Init) + len(script.Actions) + len(script.Teardown)
	if topLevelCount > MaxScriptActionDefinitions {
		return []string{fmt.Sprintf("script action/request definitions must not exceed %d", MaxScriptActionDefinitions)}
	}
	stack := make([]pendingAction, 0, topLevelCount)
	appendTopLevel := func(actions []Action) {
		for i := range actions {
			stack = append(stack, pendingAction{action: actions[i], depth: 1})
		}
	}
	appendTopLevel(script.Init)
	appendTopLevel(script.Actions)
	appendTopLevel(script.Teardown)

	definitions := 0
	for len(stack) > 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		definitions++
		if definitions > MaxScriptActionDefinitions {
			return []string{fmt.Sprintf("script action/request definitions must not exceed %d", MaxScriptActionDefinitions)}
		}
		if current.depth > MaxScriptActionDepth {
			return []string{fmt.Sprintf("script action tree nesting depth must not exceed %d", MaxScriptActionDepth)}
		}

		if strings.EqualFold(strings.TrimSpace(current.action.Type), "http_sequence") {
			requests := current.action.Params["requests"]
			switch typed := requests.(type) {
			case []any:
				definitions += len(typed)
			case []map[string]any:
				definitions += len(typed)
			}
			if definitions > MaxScriptActionDefinitions {
				return []string{fmt.Sprintf("script action/request definitions must not exceed %d", MaxScriptActionDefinitions)}
			}
		}

		if current.action.Control == nil {
			continue
		}
		branches := current.action.Control.ActionBranches()
		nestedCount := 0
		for _, branch := range branches {
			nestedCount += len(branch.Actions)
		}
		if definitions+len(stack)+nestedCount > MaxScriptActionDefinitions {
			return []string{fmt.Sprintf("script action/request definitions must not exceed %d", MaxScriptActionDefinitions)}
		}
		for _, branch := range branches {
			for _, action := range branch.Actions {
				stack = append(stack, pendingAction{action: action, depth: current.depth + 1})
			}
		}
	}
	return nil
}

var fileTemplateRefPattern = regexp.MustCompile(`\{\{\s*file\.([^{}\s]+)\s*\}\}`)

func protocolAllowsEmptyBaseURL(protocolName string) bool {
	switch ProtocolFamily(protocolName) {
	case "browser", "bruno", "database", "network", "jms", "kafka", "eventhubs", "sap_gui", "java_vuser":
		return true
	default:
		return false
	}
}

func validateRunLogicIssues(runLogic RunLogicConfig) []string {
	var issues []string
	if runLogic.Iterations < 0 {
		issues = append(issues, "config.run_logic.iterations must be zero or greater")
	} else if runLogic.Iterations > MaxRunIterations {
		issues = append(issues, fmt.Sprintf("config.run_logic.iterations must not exceed %d", MaxRunIterations))
	}
	if runLogic.GlobalIterations < 0 {
		issues = append(issues, "config.run_logic.global_iterations must be zero or greater")
	} else if runLogic.GlobalIterations > MaxRunIterations {
		issues = append(issues, fmt.Sprintf("config.run_logic.global_iterations must not exceed %d", MaxRunIterations))
	}
	if runLogic.MaxTransactions < 0 {
		issues = append(issues, "config.run_logic.max_transactions must be zero or greater")
	}
	if runLogic.MaxInFlight < 0 {
		issues = append(issues, "config.run_logic.max_in_flight must be zero or greater")
	} else if runLogic.MaxInFlight > MaxOpenWorkloadInFlight {
		issues = append(issues, fmt.Sprintf("config.run_logic.max_in_flight must not exceed %d", MaxOpenWorkloadInFlight))
	}
	return issues
}

// CanonicalWorkloadModel returns the runtime spelling for a workload model.
// Empty means the documented closed-model default. Validation rejects any
// other value, while canonicalization keeps case and surrounding whitespace
// from producing different validation and execution behavior.
func CanonicalWorkloadModel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "closed"
	}
	return value
}

// CanonicalPacingType returns the runtime spelling for a pacing type. Empty
// means the documented no-pacing default.
func CanonicalPacingType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "none"
	}
	return value
}

// ValidateWorkloadSettingsIssues validates the execution settings that decide
// whether the engine runs a closed VUser model or an open arrival model. It is
// exported so engine constructors can enforce the same contract for direct
// library callers instead of silently falling back to a different model.
func ValidateWorkloadSettingsIssues(runLogic RunLogicConfig, pacing PacingConfig) []string {
	issues := validateRunLogicIssues(runLogic)
	if mode := CanonicalTerminationMode(runLogic.TerminationMode); mode != TerminationDuration && mode != TerminationIterations {
		issues = append(issues, "termination_mode must be duration or iterations")
	}
	if runLogic.TerminationMode == TerminationIterations && (runLogic.Iterations <= 0 || CanonicalWorkloadModel(runLogic.WorkloadModel) != "closed" || runLogic.GlobalIterations != 0 || runLogic.MaxTransactions != 0) {
		issues = append(issues, "iteration termination requires positive per-user iterations, closed workload, and no shared execution limits")
	}
	workloadModel := CanonicalWorkloadModel(runLogic.WorkloadModel)
	if !isAllowedValue(workloadModel, "closed", "open") {
		issues = append(issues, "config.run_logic.workload_model must be closed or open")
	}
	issues = append(issues, validatePacingResourceIssues(pacing)...)
	if workloadModel == "open" {
		if CanonicalPacingType(pacing.Type) != "tps" {
			issues = append(issues, "open workloads require config.pacing.type to be tps")
		}
		if pacing.TPS <= 0 || math.IsNaN(pacing.TPS) || math.IsInf(pacing.TPS, 0) {
			issues = append(issues, "open workloads require config.pacing.tps to be finite and greater than zero")
		}
	}
	return issues
}

func validatePacingResourceIssues(pacing PacingConfig) []string {
	var issues []string
	if !isAllowedValue(CanonicalPacingType(pacing.Type), "none", "fixed_delay", "random_delay", "fixed_interval", "random_interval", "tps") {
		issues = append(issues, "config.pacing.type must be none, fixed_delay, random_delay, fixed_interval, random_interval, or tps")
	}
	for _, field := range []struct {
		name  string
		value int
	}{
		{name: "delay_ms", value: pacing.DelayMs},
		{name: "min_delay_ms", value: pacing.MinDelayMs},
		{name: "max_delay_ms", value: pacing.MaxDelayMs},
		{name: "interval_ms", value: pacing.IntervalMs},
		{name: "min_interval_ms", value: pacing.MinIntervalMs},
		{name: "max_interval_ms", value: pacing.MaxIntervalMs},
	} {
		if field.value < 0 || field.value > MaxConfiguredProtocolTimeoutMS {
			issues = append(issues, fmt.Sprintf("config.pacing.%s must be between 0 and %d", field.name, MaxConfiguredProtocolTimeoutMS))
		}
	}
	if pacing.MaxDelayMs > 0 && pacing.MinDelayMs > pacing.MaxDelayMs {
		issues = append(issues, "config.pacing.min_delay_ms must not exceed max_delay_ms")
	}
	if pacing.MaxIntervalMs > 0 && pacing.MinIntervalMs > pacing.MaxIntervalMs {
		issues = append(issues, "config.pacing.min_interval_ms must not exceed max_interval_ms")
	}
	if pacing.TPS < 0 {
		issues = append(issues, "config.pacing.tps must be zero or greater")
	} else if math.IsNaN(pacing.TPS) || math.IsInf(pacing.TPS, 0) || pacing.TPS > MaxPacingTPS {
		issues = append(issues, fmt.Sprintf("config.pacing.tps must be finite and must not exceed %d", MaxPacingTPS))
	}
	return issues
}

func validateThinkTimeResourceIssues(label string, think ThinkTimeConfig) []string {
	var issues []string
	if think.Type != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(think.Type)), "none", "fixed", "recorded", "random_percentage", "gaussian", "normal", "uniform") {
		issues = append(issues, fmt.Sprintf("%s.type must be none, fixed, recorded, random_percentage, gaussian, normal, or uniform", label))
	}
	for _, field := range []struct {
		name  string
		value float64
		max   float64
	}{
		{name: "min_percent", value: think.MinPercent, max: 100 * MaxThinkTimeMultiplier},
		{name: "max_percent", value: think.MaxPercent, max: 100 * MaxThinkTimeMultiplier},
		{name: "mean_ms", value: think.MeanMs, max: MaxConfiguredProtocolTimeoutMS},
		{name: "std_ms", value: think.StdMs, max: MaxConfiguredProtocolTimeoutMS},
		{name: "min_ms", value: think.MinMs, max: MaxConfiguredProtocolTimeoutMS},
		{name: "max_ms", value: think.MaxMs, max: MaxConfiguredProtocolTimeoutMS},
		{name: "limit_ms", value: think.LimitMs, max: MaxConfiguredProtocolTimeoutMS},
	} {
		if math.IsNaN(field.value) || math.IsInf(field.value, 0) || field.value < 0 || field.value > field.max {
			issues = append(issues, fmt.Sprintf("%s.%s must be finite and between 0 and %.0f", label, field.name, field.max))
		}
	}
	// A zero Go value is indistinguishable from an omitted JSON field and is
	// retained as the compatibility sentinel for inherit/default 1. Authoring
	// contracts use exclusiveMinimum=0, so every explicit multiplier is positive.
	if math.IsNaN(think.Multiplier) || math.IsInf(think.Multiplier, 0) ||
		think.Multiplier < 0 || think.Multiplier > MaxThinkTimeMultiplier {
		issues = append(issues, fmt.Sprintf("%s.multiplier must be omitted (defaults/inherits 1) or finite, greater than 0, and at most %d", label, MaxThinkTimeMultiplier))
	}
	if think.MinPercent > think.MaxPercent {
		issues = append(issues, fmt.Sprintf("%s.min_percent must not exceed max_percent", label))
	}
	if think.MaxMs > 0 && think.MinMs > think.MaxMs {
		issues = append(issues, fmt.Sprintf("%s.min_ms must not exceed max_ms", label))
	}
	return issues
}

func validateErrorHandlingResourceIssues(config ErrorHandlingConfig) []string {
	var issues []string
	if config.MaxRetries < 0 || config.MaxRetries > MaxErrorRetries {
		issues = append(issues, fmt.Sprintf("config.error_handling.max_retries must be between 0 and %d", MaxErrorRetries))
	}
	if config.RetryDelayMs < 0 || config.RetryDelayMs > MaxConfiguredProtocolTimeoutMS {
		issues = append(issues, fmt.Sprintf("config.error_handling.retry_delay_ms must be between 0 and %d", MaxConfiguredProtocolTimeoutMS))
	}
	if config.RetryBackoff != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(config.RetryBackoff)), "fixed", "exponential") {
		issues = append(issues, "config.error_handling.retry_backoff must be fixed or exponential")
	}
	return issues
}

func validateExecutionResourceIssues(script *Script) []string {
	if script == nil {
		return nil
	}
	issues := validateParameterIssues(script)
	issues = append(issues, ValidateJMeterCompatibility(script)...)
	issues = append(issues, validateCSVExports(script)...)
	issues = append(issues, ValidatePropertyAnalytics(script.Config.Results.PropertyAnalytics)...)
	issues = append(issues, ValidateWorkloadSettingsIssues(script.Config.RunLogic, script.Config.Pacing)...)
	issues = append(issues, validateThinkTimeResourceIssues("config.think_time", script.Config.ThinkTime)...)
	issues = append(issues, validateErrorHandlingResourceIssues(script.Config.ErrorHandling)...)
	issues = append(issues, validateMessageDetailResourceIssues(script.Config.Results.MessageDetail)...)
	issues = append(issues, validateProtocolMixExpectation(script.Config.Results.ProtocolMix)...)
	for i, action := range script.Init {
		issues = append(issues, validateControlResourceIssues(fmt.Sprintf("init[%d]", i), action)...)
	}
	for i, action := range script.Actions {
		issues = append(issues, validateControlResourceIssues(fmt.Sprintf("actions[%d]", i), action)...)
	}
	for i, action := range script.Teardown {
		issues = append(issues, validateControlResourceIssues(fmt.Sprintf("teardown[%d]", i), action)...)
	}
	return issues
}

func validateProtocolMixExpectation(expectation *ProtocolMixExpectation) []string {
	if expectation == nil {
		return nil
	}
	var issues []string
	for _, ratio := range []struct {
		name  string
		value *float64
	}{{"max_fallback_ratio", expectation.MaxFallbackRatio}, {"max_quic_failure_ratio", expectation.MaxQUICFailureRatio}} {
		if ratio.value != nil && (*ratio.value < 0 || *ratio.value > 1) {
			issues = append(issues, "config.results.protocol_mix."+ratio.name+" must be between 0 and 1")
		}
	}
	if expectation.MinSamples < 0 || expectation.MinSamples > 1000000 {
		issues = append(issues, "config.results.protocol_mix.min_samples must be between 0 and 1000000")
	}
	return issues
}

func validateMessageDetailResourceIssues(config *MessageDetailConfig) []string {
	if config == nil {
		return nil
	}
	issues := make([]string, 0)
	mode := strings.ToLower(strings.TrimSpace(config.Mode))
	switch mode {
	case "off", "errors", "sampled", "adaptive":
	default:
		issues = append(issues, "config.results.message_detail.mode must be off, errors, sampled, or adaptive")
	}
	if config.SampleRate < 0 || config.SampleRate > 1 {
		issues = append(issues, "config.results.message_detail.sample_rate must be between 0 and 1")
	}
	if mode == "sampled" && config.SampleRate <= 0 {
		issues = append(issues, "config.results.message_detail.sample_rate must be greater than 0 in sampled mode")
	}
	if config.MaxItems < 0 || config.MaxItems > 4096 {
		issues = append(issues, "config.results.message_detail.max_items must be between 0 and 4096")
	}
	if config.MaxItemBytes < 0 || config.MaxItemBytes > 16<<20 {
		issues = append(issues, "config.results.message_detail.max_item_bytes must be between 0 and 16777216")
	}
	if config.MaxTotalBytes < 0 || config.MaxTotalBytes > 1<<30 {
		issues = append(issues, "config.results.message_detail.max_total_bytes must be between 0 and 1073741824")
	}
	if config.MaxItemBytes > 0 && config.MaxTotalBytes > 0 && config.MaxTotalBytes < config.MaxItemBytes {
		issues = append(issues, "config.results.message_detail.max_total_bytes must not be smaller than max_item_bytes")
	}
	return issues
}

func validateControlResourceIssues(path string, action Action) []string {
	issues := validateThinkTimeResourceIssues(path+".think_time", action.ThinkTime)
	if action.Control == nil {
		return issues
	}
	if action.Control.Count > MaxControlLoopIterations {
		issues = append(issues, fmt.Sprintf("%s: control count must not exceed %d", path, MaxControlLoopIterations))
	}
	if action.Control.MaxIterations > MaxControlLoopIterations {
		issues = append(issues, fmt.Sprintf("%s: control max_iterations must not exceed %d", path, MaxControlLoopIterations))
	}
	for i, nested := range action.Control.Steps {
		issues = append(issues, validateControlResourceIssues(fmt.Sprintf("%s.control.steps[%d]", path, i), nested)...)
	}
	for i, nested := range action.Control.Else {
		issues = append(issues, validateControlResourceIssues(fmt.Sprintf("%s.control.else[%d]", path, i), nested)...)
	}
	for caseIndex, switchCase := range action.Control.Cases {
		for i, nested := range switchCase.Steps {
			issues = append(issues, validateControlResourceIssues(fmt.Sprintf("%s.control.cases[%d].steps[%d]", path, caseIndex, i), nested)...)
		}
	}
	for i, nested := range action.Control.Default {
		issues = append(issues, validateControlResourceIssues(fmt.Sprintf("%s.control.default[%d]", path, i), nested)...)
	}
	return issues
}

// (e.g. "loadrunner_transaction").
const legacyActionTypePrefix = "loadrunner_"

// CanonicalActionType normalizes a DSL action type, mapping legacy pre-rename
// "loadrunner_*" engine action types to their current "source_*" equivalents so
// scripts stored before the enterprise-archive rename continue to validate and
// execute. Types without the legacy prefix are returned unchanged.
func CanonicalActionType(actionType string) string {
	if strings.HasPrefix(actionType, legacyActionTypePrefix) {
		return "source_" + strings.TrimPrefix(actionType, legacyActionTypePrefix)
	}
	return actionType
}

func isEngineActionType(actionType string) bool {
	spec, ok := actionCatalogIndex()[CanonicalActionType(actionType)]
	return ok && spec.EngineAction()
}

// validateActionForProtocol dispatches a single action to the engine or the
// protocol-specific validator. It is used for top-level actions and, recursively,
// for the nested bodies of control_if / control_loop blocks.
func validateActionForProtocol(protocol string, index int, action Action) []string {
	return validateActionForProtocolAtPath(protocol, fmt.Sprintf("actions[%d]", index), index, action)
}

func validateActionForProtocolAtPath(protocol string, path string, index int, action Action) []string {
	if strings.EqualFold(strings.TrimSpace(action.Type), "bruno_run") {
		return []string{fmt.Sprintf("%s: %s", path, BrunoRunnerRetiredMessage)}
	}
	var issues []string
	protocol = ProtocolFamily(protocol)
	if protocol == "http" && isWebSocketStepType(action.Type) {
		// WebSocket steps inside an HTTP script follow the websocket rules.
		return validateHTTPScriptWebSocketStep(path, index, action)
	}
	if action.HTTPVersion.IsPresent() {
		if err := action.HTTPVersion.Validate(); err != nil {
			issues = append(issues, actionLabel(path, action)+": "+err.Error())
		}
		if (protocol != "http" && protocol != "soap" && protocol != "java_http") || isEngineActionType(action.Type) || CanonicalActionType(action.Type) == "http_oauth2_token" || strings.EqualFold(action.Type, "http_sequence") {
			issues = append(issues, actionLabel(path, action)+": http_version is supported only on HTTP request leaves")
		}
	}
	if strings.TrimSpace(action.Name) == "" {
		issues = append(issues, fmt.Sprintf("%s: missing name", path))
	}
	if action.Attribution != nil {
		if len(action.Attribution.HierarchyPath) > 2048 {
			issues = append(issues, fmt.Sprintf("%s: attribution hierarchy_path must not exceed 2048 bytes", actionLabel(path, action)))
		}
		if len(action.Attribution.PageName) > 512 {
			issues = append(issues, fmt.Sprintf("%s: attribution page_name must not exceed 512 bytes", actionLabel(path, action)))
		}
	}
	issues = append(issues, validateBodyFileIssues(path, action)...)
	issues = append(issues, validateMultipartBodyIssues(protocol, path, action)...)
	issues = append(issues, validateBase64BodyEncodingIssues(path, action)...)
	issues = append(issues, validateCEscapedBodyEncodingIssues(path, action)...)
	issues = append(issues, validateExtractorIssues(protocol, path, action)...)
	if protocol == "http" || protocol == "soap" || protocol == "java_http" {
		issues = append(issues, validateHTTPActionBoundsIssues(path, action)...)
	}
	if strings.TrimSpace(action.Type) == "" {
		return append(issues, fmt.Sprintf("%s: missing type", actionLabel(path, action)))
	}
	if isEngineActionType(action.Type) {
		return append(issues, validateEngineActionIssues(protocol, path, index, action)...)
	}
	rebased := func(nestedIssues []string) []string {
		return rebaseActionIssues(fmt.Sprintf("actions[%d]", index), path, nestedIssues)
	}
	protocolFamily := ProtocolFamily(protocol)
	if protocolFamily == "jms" || protocolFamily == "kafka" || protocolFamily == "eventhubs" {
		spec, ok := protocolActionSpec(protocolFamily, action.Type)
		if !ok {
			label := map[string]string{"jms": "JMS", "kafka": "Kafka", "eventhubs": "Event Hubs"}[protocolFamily]
			return append(issues, fmt.Sprintf("%s: unsupported %s action type %q", actionLabel(path, action), label, action.Type))
		}
		if spec.Validate != nil {
			return append(issues, rebased(spec.Validate(index, action))...)
		}
		return issues
	}
	switch protocol {
	case "browser":
		return append(issues, rebased(validateBrowserActionIssues(index, action))...)
	case "websocket":
		return append(issues, rebased(validateWebSocketActionIssues(index, action))...)
	case "grpc":
		return append(issues, rebased(validateGRPCActionIssues(index, action))...)
	case "database":
		return append(issues, rebased(validateDatabaseActionIssues(index, action))...)
	case "network":
		return append(issues, rebased(validateNetworkActionIssues(index, action))...)
	case "sap_gui":
		return append(issues, rebased(validateSAPGUIActionIssues(index, action))...)
	case "java_vuser":
		return append(issues, rebased(validateJavaVuserActionIssues(index, action))...)
	}
	if strings.EqualFold(strings.TrimSpace(action.Type), "http_sequence") {
		return append(issues, rebased(validateHTTPSequenceActionIssues(index, action))...)
	}
	if CanonicalActionType(action.Type) == "http_oauth2_token" {
		return issues
	}
	if step, ok := action.Params[httpprotocol.HTMLStepParam]; ok {
		// An HTML-mode step resolves its URL from the page at replay.
		return append(issues, validateHTMLStepIssues(actionLabel(path, action), protocol, step)...)
	}
	if action.Target == "" {
		issues = append(issues, fmt.Sprintf("%s: missing target", actionLabel(path, action)))
	}
	return issues
}

// validateHTMLStepIssues checks params.html_step of an HTML-mode step.
func validateHTMLStepIssues(label, protocol string, raw any) []string {
	if protocol != "http" {
		return []string{fmt.Sprintf("%s: params.html_step is supported only by the http protocol", label)}
	}
	step, ok := raw.(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("%s: params.html_step must be an object", label)}
	}
	text := func(key string) string {
		value, _ := step[key].(string)
		return strings.TrimSpace(value)
	}
	kind := strings.ToLower(text("kind"))
	var issues []string
	switch kind {
	case "link":
		if text("text") == "" {
			issues = append(issues, fmt.Sprintf("%s: params.html_step link requires text", label))
		}
	case "image", "image_check":
		if text("alt") == "" && text("src") == "" {
			issues = append(issues, fmt.Sprintf("%s: params.html_step %s requires alt or src", label, kind))
		}
	case "form":
		for _, key := range []string{"fields", "hidden", "submit"} {
			if value, present := step[key]; present {
				items, ok := value.([]any)
				if !ok {
					issues = append(issues, fmt.Sprintf("%s: params.html_step %s must be a list of {name, value}", label, key))
					continue
				}
				for _, item := range items {
					entry, ok := item.(map[string]any)
					name, _ := entry["name"].(string)
					if !ok || strings.TrimSpace(name) == "" {
						issues = append(issues, fmt.Sprintf("%s: params.html_step %s entries need a name", label, key))
						break
					}
				}
			}
		}
	default:
		issues = append(issues, fmt.Sprintf("%s: params.html_step kind must be link, image, image_check or form (got %q)", label, kind))
	}
	if ordinal, present := step["ordinal"]; present {
		whole := false
		switch number := ordinal.(type) {
		case float64:
			whole = number >= 0 && number == float64(int(number))
		case int:
			whole = number >= 0
		case int64:
			whole = number >= 0
		}
		if !whole {
			issues = append(issues, fmt.Sprintf("%s: params.html_step ordinal must be a whole number", label))
		}
	}
	return issues
}

func validateBase64BodyEncodingIssues(path string, action Action) []string {
	if !strings.EqualFold(strings.TrimSpace(action.BodyEncoding), "base64") || strings.TrimSpace(action.Body) == "" {
		return nil
	}
	label := actionLabel(path, action)
	body := strings.TrimSpace(action.Body)
	if strings.Contains(body, "{{") || strings.Contains(body, "}}") {
		if IsTrustedFullBodyBase64Template(body) {
			return nil
		}
		return []string{fmt.Sprintf("%s: body_encoding base64 cannot contain partial or untrusted templates; use literal base64 or a full-body {{env.*}}/{{secret.*}} template", label)}
	}
	if _, err := base64.StdEncoding.DecodeString(body); err != nil {
		return []string{fmt.Sprintf("%s: body_encoding base64 body must be valid standard base64: %v", label, err)}
	}
	return nil
}

// validateCEscapedBodyEncodingIssues checks the authored escapes of a
// c_escaped body. Templates are resolved before decoding and their values are
// sent verbatim, so only the text outside {{...}} must decode.
func validateCEscapedBodyEncodingIssues(path string, action Action) []string {
	if !bodyencoding.IsCEscaped(action.BodyEncoding) || action.Body == "" {
		return nil
	}
	if _, err := bodyencoding.DecodeCEscapes(runIdentityTemplatePattern.ReplaceAllString(action.Body, "")); err != nil {
		return []string{fmt.Sprintf("%s: body_encoding c_escaped body has an invalid escape: %v", actionLabel(path, action), err)}
	}
	return nil
}

func validateBodyFileIssues(path string, action Action) []string {
	if strings.TrimSpace(action.BodyFile) == "" {
		return nil
	}
	label := actionLabel(path, action)
	issues := make([]string, 0, 2)
	if strings.TrimSpace(action.Body) != "" {
		issues = append(issues, fmt.Sprintf("%s: body and body_file cannot both be set", label))
	}
	issues = append(issues, fmt.Sprintf("%s: body_file is only supported when loading scripts from a local file; inline body is required for API uploads", label))
	return issues
}

func validateDataSourceIssues(script *Script) []string {
	if script == nil || len(script.DataSources) == 0 {
		return nil
	}
	issues := make([]string, 0)
	seen := make(map[string]struct{}, len(script.DataSources))
	for i, source := range script.DataSources {
		label := fmt.Sprintf("data_sources[%d]", i)
		name := strings.TrimSpace(source.Name)
		if name == "" {
			issues = append(issues, label+": name is required")
		} else {
			if _, ok := seen[name]; ok {
				issues = append(issues, fmt.Sprintf("%s %q: duplicate name", label, name))
			}
			seen[name] = struct{}{}
		}
		sourceType := strings.ToLower(strings.TrimSpace(source.Type))
		if staged := strings.TrimSpace(source.StagedAsset); staged != "" {
			if staged != source.StagedAsset || !validStagedDataAssetName(staged) {
				issues = append(issues, fmt.Sprintf("%s %q: staged_asset must be an unpadded name of at most 256 bytes without path separators", label, name))
			}
			if strings.TrimSpace(source.InlineData) != "" || strings.TrimSpace(source.Path) != "" || strings.TrimSpace(source.StorageRef) != "" {
				issues = append(issues, fmt.Sprintf("%s %q: staged_asset cannot be combined with inline_data, path, or storage_ref", label, name))
			}
		}
		if !source.FirstRowHeader && len(source.Columns) > 0 && strings.TrimSpace(source.InlineData) != "" {
			first := strings.SplitN(strings.ReplaceAll(source.InlineData, "\r\n", "\n"), "\n", 2)[0]
			delimiter := ","
			if source.Delimiter == "\t" || strings.EqualFold(source.Delimiter, "tab") {
				delimiter = "\t"
			}
			cells := strings.Split(first, delimiter)
			if len(cells) == len(source.Columns) {
				matches := true
				for index := range cells {
					matches = matches && strings.EqualFold(strings.TrimSpace(cells[index]), strings.TrimSpace(source.Columns[index]))
				}
				if matches {
					issues = append(issues, fmt.Sprintf("%s %q: first record matches declared columns while first_row_header is false", label, name))
				}
			}
		}
		switch sourceType {
		case "", "csv", "dat":
			continue
		case "file":
			if !fileDataSourceHasContent(source) {
				issues = append(issues, fmt.Sprintf("%s %q: file data source requires inline_data, path, or storage_ref", label, name))
			}
			switch strings.ToLower(strings.TrimSpace(source.Encoding)) {
			case "", "text", "base64":
			default:
				issues = append(issues, fmt.Sprintf("%s %q: file data source encoding must be text or base64", label, name))
			}
		default:
			issues = append(issues, fmt.Sprintf("%s %q: unsupported type %q", label, name, source.Type))
		}
	}
	return issues
}

func validStagedDataAssetName(name string) bool {
	if name == "" || len(name) > 256 || strings.ContainsAny(name, "/\\") {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validateFileDataSourceReferenceIssues(script *Script) []string {
	if script == nil {
		return nil
	}
	issues := make([]string, 0, 2)
	for i, source := range script.DataSources {
		label := fmt.Sprintf("data_sources[%d]", i)
		name := strings.TrimSpace(source.Name)
		sourceLabel := "data source"
		if strings.EqualFold(strings.TrimSpace(source.Type), "file") {
			sourceLabel = "file data source"
		}
		if path := strings.TrimSpace(source.Path); path != "" {
			if _, err := fileasset.ValidatePathForProject(path, script.ProjectID); err != nil {
				issues = append(issues, fmt.Sprintf("%s %q: %s path is not an allowed file asset: %v", label, name, sourceLabel, err))
			}
		}
		if ref := strings.TrimSpace(source.StorageRef); ref != "" {
			if _, err := fileasset.ValidateStorageRefForProject(ref, script.ProjectID); err != nil {
				issues = append(issues, fmt.Sprintf("%s %q: %s storage_ref is not an allowed file asset: %v", label, name, sourceLabel, err))
			}
		}
	}
	return issues
}

func validateFileReferenceIssues(script *Script) []string {
	if script == nil {
		return nil
	}
	fileSources := make(map[string]DataSource, len(script.DataSources))
	for _, source := range script.DataSources {
		if strings.EqualFold(strings.TrimSpace(source.Type), "file") && strings.TrimSpace(source.Name) != "" {
			fileSources[strings.TrimSpace(source.Name)] = source
		}
	}
	issues := make([]string, 0)
	for i, action := range script.Init {
		issues = append(issues, validateActionFileReferences(fmt.Sprintf("init[%d]", i), action, fileSources)...)
	}
	for i, action := range script.Actions {
		issues = append(issues, validateActionFileReferences(fmt.Sprintf("actions[%d]", i), action, fileSources)...)
	}
	for i, action := range script.Teardown {
		issues = append(issues, validateActionFileReferences(fmt.Sprintf("teardown[%d]", i), action, fileSources)...)
	}
	return issues
}

func validateActionFileReferences(path string, action Action, fileSources map[string]DataSource) []string {
	refs := collectActionFileReferences(action)
	if len(refs) == 0 {
		return nil
	}
	issues := make([]string, 0)
	seen := make(map[string]struct{}, len(refs))
	label := actionLabel(path, action)
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		source, ok := fileSources[ref]
		if !ok {
			issues = append(issues, fmt.Sprintf("%s: references unknown file asset %q", label, ref))
			continue
		}
		if !fileDataSourceHasContent(source) {
			issues = append(issues, fmt.Sprintf("%s: references file asset %q without inline_data, path, or storage_ref", label, ref))
		}
	}
	return issues
}

func collectActionFileReferences(action Action) []string {
	refs := make([]string, 0)
	refs = appendFileTemplateRefs(refs, action.Target)
	refs = appendFileTemplateRefs(refs, action.Selector)
	refs = appendFileTemplateRefs(refs, action.Value)
	refs = appendFileTemplateRefs(refs, action.Body)
	for _, value := range action.BodyMultipart.MultipartTemplateStrings() {
		refs = appendFileTemplateRefs(refs, value)
	}
	refs = appendFileTemplateRefsFromStringMap(refs, action.Headers)
	for _, header := range action.HeaderList {
		refs = appendFileTemplateRefs(refs, header.Name)
		refs = appendFileTemplateRefs(refs, header.Value)
	}
	refs = appendFileTemplateRefsFromAny(refs, action.Params)
	if action.Auth != nil {
		refs = appendFileTemplateRefs(refs, action.Auth.Username)
		refs = appendFileTemplateRefs(refs, action.Auth.Password)
		refs = appendFileTemplateRefs(refs, action.Auth.Token)
		refs = appendFileTemplateRefs(refs, action.Auth.Domain)
	}
	return refs
}

func appendFileTemplateRefsFromStringMap(refs []string, values map[string]string) []string {
	for key, value := range values {
		refs = appendFileTemplateRefs(refs, key)
		refs = appendFileTemplateRefs(refs, value)
	}
	return refs
}

func appendFileTemplateRefsFromAny(refs []string, value any) []string {
	switch typed := value.(type) {
	case string:
		return appendFileTemplateRefs(refs, typed)
	case []any:
		for _, item := range typed {
			refs = appendFileTemplateRefsFromAny(refs, item)
		}
	case []map[string]any:
		for _, item := range typed {
			refs = appendFileTemplateRefsFromAny(refs, item)
		}
	case map[string]any:
		for key, item := range typed {
			refs = appendFileTemplateRefs(refs, key)
			refs = appendFileTemplateRefsFromAny(refs, item)
		}
	}
	return refs
}

func appendFileTemplateRefs(refs []string, value string) []string {
	for _, match := range fileTemplateRefPattern.FindAllStringSubmatch(value, -1) {
		if len(match) > 1 {
			refs = append(refs, strings.TrimSpace(match[1]))
		}
	}
	return refs
}

func fileDataSourceHasContent(source DataSource) bool {
	return strings.TrimSpace(source.InlineData) != "" ||
		strings.TrimSpace(source.Path) != "" ||
		strings.TrimSpace(source.StorageRef) != "" ||
		strings.TrimSpace(source.StagedAsset) != ""
}

// extractorSourceTypeAllowlistApplies reports whether the HTTP body/header
// source+type allowlist should be enforced for a protocol. The allowlist
// vocabulary (body/header sources; jsonpath/css/xpath/regex/... types) is
// HTTP-specific; other protocol runtimes support their own extractor sources
// and types (Kafka key/partition/offset, JMS property/message_id, database
// rows/scalar/row_count, network records/record_count, gRPC/WebSocket json,
// ...), so enforcing the HTTP allowlist against them would reject extractors
// those runtimes and the dashboard editor both support.
func extractorSourceTypeAllowlistApplies(protocol string) bool {
	switch ProtocolFamily(protocol) {
	case "browser", "bruno", "websocket", "grpc", "database", "network", "sap_gui", "java_vuser", "jms", "kafka", "eventhubs":
		return false
	}
	return true
}

// validateExtractorIssues checks the optional extractor behavior fields so
// typos fail validation instead of silently misbehaving at runtime. The
// source/type allowlist is HTTP-specific and only enforced for HTTP-family
// protocols (see extractorSourceTypeAllowlistApplies).
func validateExtractorIssues(protocol string, path string, action Action) []string {
	var issues []string
	label := actionLabel(path, action)
	if len(action.Extractors) > MaxActionExtractors {
		issues = append(issues, fmt.Sprintf("%s: extractors must not exceed %d", label, MaxActionExtractors))
		return issues
	}
	enforceSourceType := extractorSourceTypeAllowlistApplies(protocol)
	for i, ext := range action.Extractors {
		if len(ext.Expression) > httpprotocol.MaxExtractorExpressionBytes && enforceSourceType {
			issues = append(issues, fmt.Sprintf("%s: extractors[%d] expression must not exceed %d bytes", label, i, httpprotocol.MaxExtractorExpressionBytes))
		}
		switch ext.OnMiss {
		case "", "ignore", "fail", "empty", "default":
		default:
			issues = append(issues, fmt.Sprintf("%s: extractors[%d] on_miss must be \"\", \"ignore\", \"empty\", \"default\", or \"fail\" (got %q)", label, i, ext.OnMiss))
		}
		if ext.OnMiss == "default" && !enforceSourceType {
			issues = append(issues, fmt.Sprintf("%s: extractors[%d] on_miss default is supported only by HTTP-family protocols", label, i))
		}
		if ext.OnMiss == "default" && ext.DefaultValue == nil {
			issues = append(issues, fmt.Sprintf("%s: extractors[%d] default_value is required for on_miss default", label, i))
		}
		if ext.DefaultValue != nil && (ext.OnMiss != "default" || len(*ext.DefaultValue) > httpprotocol.MaxExtractorExpressionBytes) {
			issues = append(issues, fmt.Sprintf("%s: extractors[%d] default_value requires on_miss default and a bounded value", label, i))
		}
		switch ext.Mode {
		case "":
		case "all":
			if !enforceSourceType {
				issues = append(issues, fmt.Sprintf("%s: extractors[%d] mode \"all\" is supported only by HTTP-family protocols", label, i))
			}
		default:
			issues = append(issues, fmt.Sprintf("%s: extractors[%d] mode must be \"\" or \"all\" (got %q)", label, i, ext.Mode))
		}
		switch ext.Convert {
		case "", "url_encode", "url_decode", "html_decode", "html_url_encode":
		default:
			issues = append(issues, fmt.Sprintf("%s: extractors[%d] convert must be \"\", \"url_encode\", \"url_decode\", \"html_decode\", or \"html_url_encode\" (got %q)", label, i, ext.Convert))
		}
		if enforceSourceType {
			switch strings.TrimSpace(ext.Source) {
			case "", "body":
				if !isAllowedValue(strings.TrimSpace(ext.Type), "jsonpath", "json_path", "jmespath", "css", "xpath", "regex", "regex_count", "regex_ordinal", "boundary", "sse_concat", "sse_event") {
					issues = append(issues, fmt.Sprintf("%s: extractors[%d] type %q is not supported for body extraction", label, i, ext.Type))
				}
			case "header":
				if !isAllowedValue(strings.TrimSpace(ext.Type), "", "header", "regex", "regex_count", "regex_ordinal", "boundary") {
					issues = append(issues, fmt.Sprintf("%s: extractors[%d] type %q is not supported for header extraction", label, i, ext.Type))
				}
			case "all", "cookies":
				if !isAllowedValue(strings.TrimSpace(ext.Type), "regex", "regex_count", "regex_ordinal", "boundary") {
					surface := map[string]string{"all": "headers-and-body", "cookies": "cookie"}[strings.TrimSpace(ext.Source)]
					issues = append(issues, fmt.Sprintf("%s: extractors[%d] type %q is not supported for %s extraction (use regex, regex_count, regex_ordinal or boundary)", label, i, ext.Type, surface))
				}
			case "url", "status":
				if !isAllowedValue(strings.TrimSpace(ext.Type), "", "regex", "regex_count", "regex_ordinal", "boundary") {
					issues = append(issues, fmt.Sprintf("%s: extractors[%d] type %q is not supported for %s extraction (use \"\" for the whole value, regex, regex_count, regex_ordinal or boundary)", label, i, ext.Type, strings.TrimSpace(ext.Source)))
				}
			default:
				issues = append(issues, fmt.Sprintf("%s: extractors[%d] source must be \"\", \"body\", \"header\", \"all\", \"cookies\", \"url\", or \"status\" (got %q)", label, i, ext.Source))
			}
		}
		issues = append(issues, validateExtractorScopeIssues(label, i, ext, enforceSourceType)...)
		issues = append(issues, validateExtractorDFEIssues(label, i, ext, enforceSourceType)...)
		if ext.SaveOffset < 0 {
			issues = append(issues, fmt.Sprintf("%s: extractors[%d] save_offset must be zero or greater", label, i))
		}
		if ext.SaveLength < 0 {
			issues = append(issues, fmt.Sprintf("%s: extractors[%d] save_length must be zero or greater", label, i))
		}
	}
	return issues
}

// validateExtractorScopeIssues checks an HTTP extractor's response scope.
func validateExtractorScopeIssues(label string, index int, ext Extractor, httpFamily bool) []string {
	scope := ext.Scope
	if scope == nil {
		return nil
	}
	if !httpFamily {
		return []string{fmt.Sprintf("%s: extractors[%d] scope is supported only by HTTP-family protocols", label, index)}
	}
	var issues []string
	for _, field := range [][2]string{{"redirects", scope.Redirects}, {"resources", scope.Resources}} {
		field, value := field[0], field[1]
		if !isAllowedValue(strings.TrimSpace(value), "", "include", "exclude") {
			issues = append(issues, fmt.Sprintf("%s: extractors[%d] scope.%s must be \"include\" or \"exclude\" (got %q)", label, index, field, value))
		}
	}
	for _, field := range [][2]string{{"request_url", scope.RequestURL}, {"content_type", scope.ContentType}} {
		field, pattern := field[0], field[1]
		if len(pattern) > httpprotocol.MaxExtractorExpressionBytes {
			issues = append(issues, fmt.Sprintf("%s: extractors[%d] scope.%s exceeds %d bytes", label, index, field, httpprotocol.MaxExtractorExpressionBytes))
		}
	}
	if len(scope.HeaderNames) > 0 {
		switch strings.TrimSpace(ext.Source) {
		case "header", "all", "cookies":
		default:
			issues = append(issues, fmt.Sprintf("%s: extractors[%d] scope.header_names applies only to the header, all or cookies sources", label, index))
		}
		if len(scope.HeaderNames) > 64 {
			issues = append(issues, fmt.Sprintf("%s: extractors[%d] scope.header_names lists more than 64 headers", label, index))
		}
		for _, name := range scope.HeaderNames {
			if !httpguts.ValidHeaderFieldName(strings.TrimSpace(name)) {
				issues = append(issues, fmt.Sprintf("%s: extractors[%d] scope.header_names has an invalid header name %q", label, index, name))
			}
		}
	}
	return issues
}

// validateExtractorDFEIssues checks an HTTP extractor's data-format chain.
func validateExtractorDFEIssues(label string, index int, ext Extractor, httpFamily bool) []string {
	if len(ext.DFEs) == 0 {
		return nil
	}
	if !httpFamily {
		return []string{fmt.Sprintf("%s: extractors[%d] dfes are supported only by HTTP-family protocols", label, index)}
	}
	var issues []string
	if len(ext.DFEs) > httpprotocol.MaxExtractorDFEs {
		issues = append(issues, fmt.Sprintf("%s: extractors[%d] dfes lists more than %d transforms", label, index, httpprotocol.MaxExtractorDFEs))
	}
	for _, name := range ext.DFEs {
		if !httpprotocol.ExtractorDFETypes[strings.ToLower(strings.TrimSpace(name))] {
			issues = append(issues, fmt.Sprintf("%s: extractors[%d] dfes has unsupported transform %q (use base64, gzip, json_to_xml, xml_to_json, url_decode, url_encode, html_unescape or html_escape)", label, index, name))
		}
	}
	return issues
}

func validateEngineActionIssues(protocol string, path string, index int, action Action) []string {
	switch action.Type {
	case "live_table":
		return rebaseActionIssues(fmt.Sprintf("actions[%d]", index), path, validateLiveTableAction(index, action))
	case "source_verification":
		if protocol != "http" {
			return []string{fmt.Sprintf("%s: positional source verification requires the HTTP protocol", path)}
		}
		return rebaseActionIssues(fmt.Sprintf("action[%d]", index), path, validateVerificationAction(index, action))
	case "rendezvous":
		return rebaseActionIssues(fmt.Sprintf("actions[%d]", index), path, validateRendezvousActionIssues(index, action))
	case "ws_on_message", "control_if", "control_switch", "control_loop", "control_parallel":
		return validateControlFlowIssues(protocol, path, action)
	case "http_async_start", "http_async_stop", "http_sync":
		return validateHTTPAsyncIssues(protocol, path, action)
	case "error_message":
		label := actionLabel(path, action)
		if strings.TrimSpace(action.Value) == "" {
			if format, ok := actionParamStringValue(action.Params, "format"); ok && format != "" {
				return nil
			}
			return []string{fmt.Sprintf("%s: error_message requires value or params.format", label)}
		}
	}
	return nil
}

// validateControlFlowIssues validates a control_if / control_switch / control_loop block and
// recursively validates its nested step bodies against the script protocol.
func validateControlFlowIssues(protocol string, path string, action Action) []string {
	label := actionLabel(path, action)
	if action.Type == "ws_on_message" && (action.Control == nil || len(action.Control.Steps) == 0 || len(action.Control.Steps) > 64) {
		return []string{label + ": ws_on_message requires 1..64 control.steps"}
	}
	if action.Control == nil {
		return []string{fmt.Sprintf("%s: %s requires a control block", label, action.Type)}
	}
	var issues []string
	if action.Control.Count < 0 {
		issues = append(issues, fmt.Sprintf("%s: control count must be zero or greater", label))
	}
	if action.Control.Count > MaxControlLoopIterations {
		issues = append(issues, fmt.Sprintf("%s: control count must not exceed %d", label, MaxControlLoopIterations))
	}
	if action.Control.MaxIterations < 0 {
		issues = append(issues, fmt.Sprintf("%s: control max_iterations must be zero or greater", label))
	}
	if action.Control.MaxIterations > MaxControlLoopIterations {
		issues = append(issues, fmt.Sprintf("%s: control max_iterations must not exceed %d", label, MaxControlLoopIterations))
	}
	issues = append(issues, validateConditionIssues(label, action.Control.Condition)...)
	switch action.Type {
	case "control_parallel":
		if len(action.Control.Branches) < 2 {
			issues = append(issues, fmt.Sprintf("%s: control_parallel requires at least two branches", label))
		}
		if action.Control.Concurrency < 0 || action.Control.Concurrency > 16 {
			issues = append(issues, fmt.Sprintf("%s: control_parallel concurrency must be between 0 and 16", label))
		}
		if action.Control.Condition != nil || len(action.Control.Steps) > 0 || len(action.Control.Else) > 0 || len(action.Control.Cases) > 0 || len(action.Control.Default) > 0 || action.Control.Count != 0 || action.Control.ForEach != nil || action.Control.DoWhile {
			issues = append(issues, fmt.Sprintf("%s: control_parallel accepts only branches, concurrency, and fail_fast", label))
		}
		seen := make(map[string]struct{}, len(action.Control.Branches))
		for branchIndex, branch := range action.Control.Branches {
			branchLabel := fmt.Sprintf("%s.control.branches[%d]", path, branchIndex)
			name := strings.TrimSpace(branch.Name)
			if name == "" {
				issues = append(issues, fmt.Sprintf("%s: name is required", branchLabel))
			} else if len(name) > 128 {
				issues = append(issues, fmt.Sprintf("%s: name must not exceed 128 bytes", branchLabel))
			} else if _, exists := seen[strings.ToLower(name)]; exists {
				issues = append(issues, fmt.Sprintf("%s: branch name %q is duplicated", branchLabel, name))
			} else {
				seen[strings.ToLower(name)] = struct{}{}
			}
			if len(branch.Steps) == 0 {
				issues = append(issues, fmt.Sprintf("%s: steps must not be empty", branchLabel))
			}
			issues = append(issues, validateParallelBranchRuntimeSafety(branch.Steps, branchLabel+".steps")...)
		}
	case "control_loop":
		// Loop mode precedence: for_each > count > condition.
		if action.Control.ForEach != nil {
			if strings.TrimSpace(action.Control.ForEach.Source) == "" {
				issues = append(issues, fmt.Sprintf("%s: control_loop for_each requires a source", label))
			}
			issues = append(issues, validateConditionIssues(label, action.Control.ForEach.Condition)...)
		} else if action.Control.Count <= 0 && action.Control.Condition == nil {
			issues = append(issues, fmt.Sprintf("%s: control_loop requires a for_each source, a positive count, or a condition", label))
		}
	case "control_if":
		if len(action.Control.Steps) == 0 && len(action.Control.Else) == 0 {
			issues = append(issues, fmt.Sprintf("%s: control_if requires steps or else", label))
		}
	case "control_switch":
		if len(action.Control.Cases) == 0 {
			issues = append(issues, fmt.Sprintf("%s: control_switch requires at least one case", label))
		}
		if action.Control.Condition != nil || len(action.Control.Steps) > 0 || len(action.Control.Else) > 0 || action.Control.Count != 0 || action.Control.ForEach != nil || action.Control.DoWhile {
			issues = append(issues, fmt.Sprintf("%s: control_switch accepts only cases and default", label))
		}
		for caseIndex, switchCase := range action.Control.Cases {
			caseLabel := fmt.Sprintf("%s.control.cases[%d]", path, caseIndex)
			if switchCase.Condition == nil {
				issues = append(issues, fmt.Sprintf("%s: condition is required", caseLabel))
			} else {
				issues = append(issues, validateConditionIssues(caseLabel, switchCase.Condition)...)
			}
			if len(switchCase.Steps) == 0 {
				issues = append(issues, fmt.Sprintf("%s: steps must not be empty", caseLabel))
			}
		}
	}
	for j, nested := range action.Control.Steps {
		issues = append(issues, validateActionForProtocolAtPath(protocol, fmt.Sprintf("%s.control.steps[%d]", path, j), j, nested)...)
	}
	for j, nested := range action.Control.Else {
		issues = append(issues, validateActionForProtocolAtPath(protocol, fmt.Sprintf("%s.control.else[%d]", path, j), j, nested)...)
	}
	for caseIndex, switchCase := range action.Control.Cases {
		for j, nested := range switchCase.Steps {
			issues = append(issues, validateActionForProtocolAtPath(protocol, fmt.Sprintf("%s.control.cases[%d].steps[%d]", path, caseIndex, j), j, nested)...)
		}
	}
	for j, nested := range action.Control.Default {
		issues = append(issues, validateActionForProtocolAtPath(protocol, fmt.Sprintf("%s.control.default[%d]", path, j), j, nested)...)
	}
	for branchIndex, branch := range action.Control.Branches {
		for j, nested := range branch.Steps {
			issues = append(issues, validateActionForProtocolAtPath(protocol, fmt.Sprintf("%s.control.branches[%d].steps[%d]", path, branchIndex, j), j, nested)...)
		}
	}
	return issues
}

// validateParallelBranchRuntimeSafety rejects control constructs whose state or
// cancellation semantics are not defined across independently initialized
// protocol branches. This is an admission rule; the engine repeats the check as
// defense in depth for callers that construct scripts without validation.
func validateParallelBranchRuntimeSafety(actions []Action, path string) []string {
	return ValidateParallelBranchRuntimeSafety(actions, path)
}

// ValidateParallelBranchRuntimeSafety is shared by authoring and execution.
// Loop control must be owned by a loop inside the same isolated branch.
func ValidateParallelBranchRuntimeSafety(actions []Action, path string) []string {
	return validateParallelBranchScope(actions, path, 0)
}
func validateParallelBranchScope(actions []Action, path string, loopDepth int) []string {
	var issues []string
	for index, action := range actions {
		actionPath := fmt.Sprintf("%s[%d]", path, index)
		actionType := CanonicalActionType(strings.ToLower(strings.TrimSpace(action.Type)))
		switch actionType {
		case "control_parallel", "rendezvous", "http_async_start", "http_async_stop", "http_sync":
			issues = append(issues, fmt.Sprintf("%s: %s is not permitted inside control_parallel", actionPath, actionType))
		}
		if (actionType == "control_break" || actionType == "control_continue") && loopDepth == 0 {
			issues = append(issues, fmt.Sprintf("%s: %s is not permitted without a branch-local control_loop", actionPath, actionType))
		}
		if action.Control == nil {
			continue
		}
		for _, branch := range action.Control.ActionBranches() {
			depth := loopDepth
			if actionType == "control_loop" && branch.Path == "steps" {
				depth++
			}
			issues = append(issues, validateParallelBranchScope(branch.Actions, actionPath+".control."+branch.Path, depth)...)
		}
	}
	return issues
}

func validateConditionIssues(label string, condition *Condition) []string {
	if condition == nil {
		return nil
	}
	if expr := strings.TrimSpace(condition.Expr); expr != "" {
		if err := ValidateConditionExpression(expr); err != nil {
			return []string{fmt.Sprintf("%s: condition expr is invalid: %v", label, err)}
		}
		return nil
	}
	op := strings.ToLower(strings.TrimSpace(condition.Op))
	if op == "" {
		op = "eq"
	}
	if !isAllowedValue(op, "eq", "==", "equals", "ne", "!=", "not_equals", "contains", "not_contains", "gt", "lt", "gte", "lte", ">", "<", ">=", "<=", "exists", "truthy", "not_exists", "falsy", "empty") {
		return []string{fmt.Sprintf("%s: condition op %q is not supported", label, condition.Op)}
	}
	if strings.TrimSpace(condition.Left) == "" {
		return []string{fmt.Sprintf("%s: condition left operand is required", label)}
	}
	switch op {
	case "exists", "truthy", "not_exists", "falsy", "empty":
		return nil
	default:
		if strings.TrimSpace(condition.Right) == "" {
			return []string{fmt.Sprintf("%s: condition right operand is required for op %q", label, condition.Op)}
		}
	}
	return nil
}

func actionLabel(path string, action Action) string {
	if strings.TrimSpace(action.Name) == "" {
		return path
	}
	return fmt.Sprintf("%s %q", path, action.Name)
}

func rebaseActionIssues(oldPath, newPath string, issues []string) []string {
	if len(issues) == 0 {
		return nil
	}
	if oldPath == newPath {
		return issues
	}
	rebased := make([]string, len(issues))
	for i, issue := range issues {
		if strings.HasPrefix(issue, oldPath) {
			rebased[i] = newPath + strings.TrimPrefix(issue, oldPath)
		} else {
			rebased[i] = newPath + ": " + issue
		}
	}
	return rebased
}

func validateGoalConfigIssues(goal *GoalConfig) []string {
	if goal == nil {
		return nil
	}

	var issues []string
	if goal.P95LatencyMs != nil && *goal.P95LatencyMs <= 0 {
		issues = append(issues, "goal_config.p95_latency_ms must be greater than zero")
	}
	if goal.MaxErrorPct != nil && (*goal.MaxErrorPct < 0 || *goal.MaxErrorPct > 100) {
		issues = append(issues, "goal_config.max_error_pct must be between 0 and 100")
	}
	if goal.MaxAvgServerProcessingMs != nil && *goal.MaxAvgServerProcessingMs <= 0 {
		issues = append(issues, "goal_config.max_avg_server_processing_ms must be greater than zero")
	}
	if goal.MaxAvgTransferMs != nil && *goal.MaxAvgTransferMs <= 0 {
		issues = append(issues, "goal_config.max_avg_transfer_ms must be greater than zero")
	}
	if goal.MinVUsers != nil && *goal.MinVUsers < 1 {
		issues = append(issues, "goal_config.min_vusers must be at least 1")
	}
	if goal.MaxVUsers != nil && *goal.MaxVUsers < 1 {
		issues = append(issues, "goal_config.max_vusers must be at least 1")
	}
	if goal.MinVUsers != nil && goal.MaxVUsers != nil && *goal.MinVUsers > *goal.MaxVUsers {
		issues = append(issues, "goal_config.min_vusers cannot exceed goal_config.max_vusers")
	}
	if goal.MaxErrorCount != nil && *goal.MaxErrorCount < 1 {
		issues = append(issues, "goal_config.max_error_count must be at least 1")
	}
	if goal.SLABreachWindow != "" {
		if _, err := time.ParseDuration(goal.SLABreachWindow); err != nil {
			issues = append(issues, fmt.Sprintf("goal_config.sla_breach_window is invalid: %v", err))
		}
	}
	if goal.StopOnSLABreach && goal.P95LatencyMs == nil && goal.MaxErrorPct == nil && goal.MaxAvgServerProcessingMs == nil && goal.MaxAvgTransferMs == nil {
		issues = append(issues, "goal_config.stop_on_sla_breach requires a latency, error, server processing, or transfer threshold")
	}
	if goal.StopOnMaxErrors && goal.MaxErrorCount == nil {
		issues = append(issues, "goal_config.stop_on_max_errors requires goal_config.max_error_count")
	}
	if len(goal.TransactionRules) > 128 {
		issues = append(issues, "goal_config.transaction_rules cannot contain more than 128 rules")
	}
	metrics := map[string]bool{
		"average_duration_ms": true, "p90_duration_ms": true, "p95_duration_ms": true, "p99_duration_ms": true, "max_acknowledgement_ms": true,
		"min_duration_ms": true, "max_duration_ms": true, "error_count": true,
		"error_rate_pct": true, "minimum_count": true, "bytes_sent": true,
		"bytes_received": true, "tps": true,
		"max_consumer_lag_ms": true, "max_consumer_offset_lag": true,
	}
	comparators := map[string]bool{"lt": true, "lte": true, "gt": true, "gte": true, "eq": true, "neq": true}
	for index, rule := range goal.TransactionRules {
		path := fmt.Sprintf("goal_config.transaction_rules[%d]", index)
		if rule.Transaction == "" || strings.TrimSpace(rule.Transaction) != rule.Transaction {
			issues = append(issues, path+".transaction must be a non-empty exact name without surrounding whitespace")
		} else if len(rule.Transaction) > 256 {
			issues = append(issues, path+".transaction exceeds 256 bytes")
		}
		if rule.Population != "" && rule.Population != "transaction" && rule.Population != "action" {
			issues = append(issues, path+".population must be transaction or action")
		}
		for name, value := range map[string]string{"protocol": rule.Protocol, "group": rule.Group} {
			if len(value) > 256 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") {
				issues = append(issues, path+"."+name+" must be a bounded exact identity")
			}
		}
		if !metrics[rule.Metric] {
			issues = append(issues, fmt.Sprintf("%s.metric %q is not supported", path, rule.Metric))
		}
		if !comparators[rule.Comparator] {
			issues = append(issues, fmt.Sprintf("%s.comparator %q is not supported", path, rule.Comparator))
		}
		if math.IsNaN(rule.Threshold) || math.IsInf(rule.Threshold, 0) || rule.Threshold < 0 {
			issues = append(issues, path+".threshold must be a finite non-negative number")
		}
		if rule.Severity != "" && rule.Severity != "fail" && rule.Severity != "warning" {
			issues = append(issues, path+".severity must be fail or warning")
		}
	}

	return issues
}

func actionParamIntValue(params map[string]any, key string) (int, bool) {
	if params == nil {
		return 0, false
	}
	value, ok := params[key]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		if typed > int64(maxIntValue()) || typed < int64(minIntValue()) {
			return 0, false
		}
		return int(typed), true
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed || typed >= float64(maxIntValue()) || typed < float64(minIntValue()) {
			return 0, false
		}
		return int(typed), true
	case float32:
		converted := float64(typed)
		if math.IsNaN(converted) || math.IsInf(converted, 0) || math.Trunc(converted) != converted || converted >= float64(maxIntValue()) || converted < float64(minIntValue()) {
			return 0, false
		}
		return int(typed), true
	default:
		return 0, false
	}
}

func maxIntValue() int { return int(^uint(0) >> 1) }

func minIntValue() int { return -maxIntValue() - 1 }

func actionParamFloatValue(params map[string]any, key string) (float64, bool) {
	if params == nil {
		return 0, false
	}
	value, ok := params[key]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	default:
		return 0, false
	}
}

func actionParamStringValue(params map[string]any, key string) (string, bool) {
	if params == nil {
		return "", false
	}
	value, ok := params[key]
	if !ok {
		return "", false
	}
	typed, ok := value.(string)
	if !ok {
		return "", false
	}
	trimmed := strings.TrimSpace(typed)
	if trimmed == "" {
		return "", false
	}
	return trimmed, true
}

func actionParamString(params map[string]any, key string) string {
	value, _ := actionParamStringValue(params, key)
	return value
}

// ValidationError carries every static finding of a script, as the API's
// validation routes report them, so the parser and the routes agree on what
// is wrong (AUTH-2 parity). Error joins them with "; ".
type ValidationError struct {
	Issues []string
}

func (e *ValidationError) Error() string { return strings.Join(e.Issues, "; ") }

// ValidateScriptError returns every validation issue as a *ValidationError,
// or nil for a valid script.
func ValidateScriptError(script *Script) error {
	issues := ValidateScriptIssues(script)
	if len(issues) == 0 {
		return nil
	}
	return &ValidationError{Issues: issues}
}

// visitScriptActions walks every executable section and nested control-flow
// branch. Structural validation runs before protocol validation and bounds both
// action count and nesting depth, so this traversal cannot be driven unbounded
// by a submitted script.
func visitScriptActions(script *Script, visit func(path string, action Action)) {
	var walk func(string, []Action)
	walk = func(section string, actions []Action) {
		for index, action := range actions {
			path := fmt.Sprintf("%s[%d]", section, index)
			visit(path, action)
			if action.Control == nil {
				continue
			}
			for _, branch := range action.Control.ActionBranches() {
				walk(path+".control."+branch.Path, branch.Actions)
			}
		}
	}
	walk("init", script.Init)
	walk("actions", script.Actions)
	walk("teardown", script.Teardown)
}

func validateParameterIssues(script *Script) []string {
	if len(script.Parameters) == 0 {
		return nil
	}

	var issues []string
	dataSourcesByName := make(map[string]DataSource, len(script.DataSources))
	for _, source := range script.DataSources {
		dataSourcesByName[strings.ToLower(strings.TrimSpace(source.Name))] = source
	}
	parametersByName := make(map[string]Parameter, len(script.Parameters))
	for _, param := range script.Parameters {
		name := strings.TrimSpace(param.Name)
		if name == "" {
			continue
		}
		normalized := strings.ToLower(name)
		if _, exists := parametersByName[normalized]; !exists {
			parametersByName[normalized] = param
		}
	}

	seen := make(map[string]struct{}, len(script.Parameters))
	for i, param := range script.Parameters {
		label := fmt.Sprintf("parameters[%d]", i)
		name := strings.TrimSpace(param.Name)
		if name == "" {
			issues = append(issues, fmt.Sprintf("%s: name is required", label))
		} else {
			normalized := strings.ToLower(name)
			if _, ok := seen[normalized]; ok {
				issues = append(issues, fmt.Sprintf("%s %q: duplicate name", label, name))
			}
			seen[normalized] = struct{}{}
		}

		paramType := CanonicalParameterType(param.Type)
		if paramType == "" {
			issues = append(issues, fmt.Sprintf("%s %q: type is required", label, name))
			continue
		}
		if !isAllowedValue(paramType, "file", "list", "random_number", "date_time", "timestamp", "counter", "current_iteration", "vuser_id", "host", "time", "uuid", "random_string") {
			issues = append(issues, fmt.Sprintf("%s %q: unsupported type %q", label, name, param.Type))
		}

		if param.Update != "" && !isAllowedValue(param.Update, "once", "each_iteration", "each_occurrence") {
			issues = append(issues, fmt.Sprintf("%s %q: update must be once, each_iteration, or each_occurrence", label, name))
		}
		if param.Selection != "" && !isAllowedValue(param.Selection, "sequential", "random", "unique") {
			issues = append(issues, fmt.Sprintf("%s %q: selection must be sequential, random, or unique", label, name))
		}
		if param.OnExhaustion != "" && !isAllowedValue(param.OnExhaustion, "wrap", "empty", "error", "last") {
			issues = append(issues, fmt.Sprintf("%s %q: on_exhaustion must be wrap, empty, error, or last", label, name))
		}
		if param.StartRow < 0 || param.StartRow > MaxParameterRowIndex {
			issues = append(issues, fmt.Sprintf("%s %q: start_row must be between 0 and %d", label, name, MaxParameterRowIndex))
		}
		if param.AutoAllocate && (param.Selection != "unique" || (param.Type != "file" && param.Type != "list")) {
			issues = append(issues, fmt.Sprintf("%s %q: auto_allocate requires a unique file/list parameter", label, name))
		}
		if param.BlockSize < 0 || param.BlockSize > MaxParameterRowIndex {
			issues = append(issues, fmt.Sprintf("%s %q: block_size must be between 0 and %d", label, name, MaxParameterRowIndex))
		}
		if param.PadWidth < 0 || param.PadWidth > MaxGeneratedParameterBytes {
			issues = append(issues, fmt.Sprintf("%s %q: pad_width must be between 0 and %d", label, name, MaxGeneratedParameterBytes))
		}
		if len(param.Charset) > MaxParameterCharsetBytes {
			issues = append(issues, fmt.Sprintf("%s %q: charset must not exceed %d bytes", label, name, MaxParameterCharsetBytes))
		}
		if len(param.Values) > MaxParameterValues {
			issues = append(issues, fmt.Sprintf("%s %q: values must not exceed %d entries", label, name, MaxParameterValues))
		}
		if param.OffsetSeconds < -MaxParameterOffsetSeconds || param.OffsetSeconds > MaxParameterOffsetSeconds {
			issues = append(issues, fmt.Sprintf("%s %q: offset_seconds must be between %d and %d", label, name, -MaxParameterOffsetSeconds, MaxParameterOffsetSeconds))
		}
		if sameLineAs := strings.TrimSpace(param.SameLineAs); sameLineAs != "" {
			reference, ok := parametersByName[strings.ToLower(sameLineAs)]
			if !ok {
				issues = append(issues, fmt.Sprintf("%s %q: same_line_as references unknown parameter %q", label, name, sameLineAs))
			} else if strings.EqualFold(strings.TrimSpace(reference.Name), name) {
				issues = append(issues, fmt.Sprintf("%s %q: same_line_as cannot reference itself", label, name))
			} else if paramType != "file" && paramType != "list" {
				issues = append(issues, fmt.Sprintf("%s %q: same_line_as requires a file or list parameter", label, name))
			} else if referenceType := strings.ToLower(strings.TrimSpace(reference.Type)); referenceType != "file" && referenceType != "list" {
				// same_line_as uses the row number the referenced parameter
				// picked, so it must reference a row-based parameter. Like
				// LoadRunner, the row number may index another data file.
				issues = append(issues, fmt.Sprintf("%s %q: same_line_as must reference a file or list parameter", label, name))
			}
		}

		switch paramType {
		case "file":
			if strings.TrimSpace(param.DataSource) == "" {
				issues = append(issues, fmt.Sprintf("%s %q: data_source is required for file parameters", label, name))
			}
			if strings.TrimSpace(param.Column) == "" {
				issues = append(issues, fmt.Sprintf("%s %q: column is required for file parameters", label, name))
			}
			issues = append(issues, validateParameterDataSourceBinding(label, name, param, dataSourcesByName)...)
		case "list":
			if len(param.Values) == 0 && strings.TrimSpace(param.DataSource) == "" {
				issues = append(issues, fmt.Sprintf("%s %q: values or data_source is required for list parameters", label, name))
			}
			if strings.TrimSpace(param.DataSource) != "" && strings.TrimSpace(param.Column) == "" {
				issues = append(issues, fmt.Sprintf("%s %q: column is required when data_source is set", label, name))
			}
			if strings.TrimSpace(param.DataSource) != "" {
				issues = append(issues, validateParameterDataSourceBinding(label, name, param, dataSourcesByName)...)
			}
		case "random_number":
			if param.Min > param.Max {
				issues = append(issues, fmt.Sprintf("%s %q: min must be less than or equal to max", label, name))
			}
		case "date_time", "time":
			validateParameterTimezone(&issues, label, name, param.Timezone)
		case "timestamp":
			validateParameterTimezone(&issues, label, name, param.Timezone)
			if param.Format != "" && !isAllowedValue(param.Format, "unix", "unix_ms", "iso8601", "rfc3339", "rfc3339_nano") {
				issues = append(issues, fmt.Sprintf("%s %q: format must be unix, unix_ms, iso8601, rfc3339, or rfc3339_nano", label, name))
			}
		case "counter":
			if param.Step == 0 {
				issues = append(issues, fmt.Sprintf("%s %q: step must be non-zero for counter parameters", label, name))
			}
			if param.Selection == "unique" && (param.BlockSize <= 0 || param.Step != 1 || param.End != 0 || param.AutoAllocate) {
				issues = append(issues, fmt.Sprintf("%s %q: unique counter requires an explicit positive block_size, step=1, and no end or automatic allocation", label, name))
			}
		case "random_string":
			if param.Length <= 0 {
				issues = append(issues, fmt.Sprintf("%s %q: length must be greater than zero for random_string parameters", label, name))
			}
			if param.Length > MaxGeneratedParameterBytes {
				issues = append(issues, fmt.Sprintf("%s %q: length must not exceed %d", label, name, MaxGeneratedParameterBytes))
			}
		}
	}

	return issues
}

func validateParameterDataSourceBinding(label, name string, param Parameter, sources map[string]DataSource) []string {
	sourceName := strings.TrimSpace(param.DataSource)
	column := strings.TrimSpace(param.Column)
	if sourceName == "" || column == "" {
		return nil
	}
	source, ok := sources[strings.ToLower(sourceName)]
	if !ok {
		return []string{fmt.Sprintf("%s %q: data_source %q does not exist", label, name, sourceName)}
	}
	if !source.FirstRowHeader && len(source.Columns) > 0 && parameterColumnsMatchRecord(source.Columns, parameterDataSourceFirstRecord(source)) {
		return []string{fmt.Sprintf("%s %q: data_source %q first record matches its columns but first_row_header is false", label, name, sourceName)}
	}
	// Whether the referenced COLUMN exists is deliberately not checked here. It is
	// a data-source FIT problem, not a structural one: the script still parses and
	// stores, and the fit report already raises it (driving authoring health to
	// blocked). Reporting it structurally would make ValidateScriptIssues reject
	// scripts that run today, including inside generator-side execution staging.
	return nil
}

func parameterColumnsMatchRecord(columns, record []string) bool {
	if len(columns) == 0 || len(columns) != len(record) {
		return false
	}
	for index := range columns {
		if !strings.EqualFold(strings.TrimSpace(columns[index]), strings.TrimSpace(record[index])) {
			return false
		}
	}
	return true
}

// ScoreDataSourceDelimiter counts delimiter-separated fields across the first
// lines of content so a delimiter can be sniffed from the data itself.
func ScoreDataSourceDelimiter(content string, delimiter rune) int {
	score := 0
	for index, line := range strings.Split(content, "\n") {
		if index >= 20 {
			break
		}
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		count := strings.Count(line, string(delimiter))
		if count > 0 {
			score += count + 1
		}
	}
	return score
}

// DetectDataSourceDelimiter returns the delimiter that best explains content, or
// 0 when neither comma nor tab appears.
func DetectDataSourceDelimiter(content string) rune {
	commaScore := ScoreDataSourceDelimiter(content, ',')
	tabScore := ScoreDataSourceDelimiter(content, '\t')
	if tabScore == 0 && commaScore == 0 {
		return 0
	}
	if tabScore > commaScore {
		return '\t'
	}
	return ','
}

// ResolveDataSourceDelimiter is the single source of truth for how a data
// source's delimiter is chosen. Validation and the runtime loader MUST agree:
// when they disagree the validator parses a header row as one field and rejects
// scripts the engine runs correctly.
func ResolveDataSourceDelimiter(source DataSource, content string) rune {
	if source.Delimiter == "\t" {
		return '\t'
	}
	delimiter := strings.TrimSpace(source.Delimiter)
	switch strings.ToLower(delimiter) {
	case "":
		// Older enterprise imports omitted an explicit comma because CSV used
		// to be the implicit default. Detect self-contained inline data before
		// applying the legacy .dat-as-tab fallback when the source declares a
		// tabular schema. Sources with neither a header nor multiple declared
		// columns retain the historical tab default, avoiding guesses from
		// commas in single-column values.
		if source.FirstRowHeader || len(source.Columns) > 1 {
			if detected := DetectDataSourceDelimiter(content); detected != 0 {
				return detected
			}
		}
		if source.Type == "dat" {
			return '\t'
		}
		return ','
	case "auto":
		if detected := DetectDataSourceDelimiter(content); detected != 0 {
			return detected
		}
		if source.Type == "dat" {
			return '\t'
		}
		return ','
	case `\t`, "tab":
		return '\t'
	case "comma":
		return ','
	default:
		return []rune(delimiter)[0]
	}
}

func parameterDataSourceFirstRecord(source DataSource) []string {
	reader := csv.NewReader(strings.NewReader(source.InlineData))
	reader.Comma = ResolveDataSourceDelimiter(source, source.InlineData)
	reader.LazyQuotes = true
	record, err := reader.Read()
	if err != nil {
		return nil
	}
	return record
}

func validateParameterTimezone(issues *[]string, label, name, timezone string) {
	if strings.TrimSpace(timezone) == "" {
		return
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		*issues = append(*issues, fmt.Sprintf("%s %q: invalid timezone %q", label, name, timezone))
	}
}

func validateWebRuntimeIssues(script *Script) []string {
	var issues []string
	if script.Config.WebSocket.CallbackIntervalMs < 0 || script.Config.WebSocket.CallbackIntervalMs > 60000 {
		issues = append(issues, "config.websocket.callback_interval_ms must be between 0 and 60000")
	}

	runLogic := script.Config.RunLogic
	if runLogic.BlockScheduling != "" && !isAllowedValue(runLogic.BlockScheduling, "sequential", "random", "percentage") {
		issues = append(issues, "config.run_logic.block_scheduling must be sequential, random, or percentage")
	}
	for index, step := range runLogic.ActionSequence {
		if !isAllowedValue(step, "init", "action", "teardown", "vuser_init", "vuser_end") {
			issues = append(issues, fmt.Sprintf("config.run_logic.action_sequence[%d] must be init, action, teardown, vuser_init, or vuser_end", index))
		}
	}
	issues = append(issues, validateRunLogicBlocks(script)...)

	for key := range script.Config.WebRuntime.AdditionalAttributes {
		if strings.TrimSpace(key) == "" {
			issues = append(issues, "config.web_runtime.additional_attributes keys must be non-empty")
			break
		}
	}

	// Source-IP pool (LR-style "IP spoofing" / multiple client IPs). Each entry binds
	// VUser connections to that local address, so it must parse as a real IP — an
	// invalid address would otherwise be silently dropped and the spoofing skipped.
	for index, raw := range script.Config.WebRuntime.SourceIPs {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			issues = append(issues, fmt.Sprintf("config.web_runtime.source_ips[%d] must not be empty", index))
			continue
		}
		if net.ParseIP(trimmed) == nil {
			issues = append(issues, fmt.Sprintf("config.web_runtime.source_ips[%d] %q is not a valid IP address", index, raw))
		}
	}

	if len(script.Config.Browser.SystemLanguage) > 64 || strings.ContainsAny(script.Config.Browser.SystemLanguage, "\r\n\x00") {
		issues = append(issues, "config.browser.system_language must be at most 64 characters without control characters")
	}
	for key, value := range map[string]int{"available_screen_width": script.Config.Browser.AvailableScreenWidth, "available_screen_height": script.Config.Browser.AvailableScreenHeight} {
		if value < 0 || value > 8192 {
			issues = append(issues, "config.browser."+key+" must be between 0 and 8192")
		}
	}
	logCfg := script.Config.Log
	if logCfg.ErrorBufferBytes < 0 || logCfg.ErrorBufferBytes > 1048576 {
		issues = append(issues, "config.log.error_buffer_bytes must be between 0 and 1048576")
	}
	if logCfg.MaxResponseBytes < 0 || logCfg.MaxResponseBytes > 1048576 {
		issues = append(issues, "config.log.max_response_bytes must be between 0 and 1048576")
	}
	for index, option := range logCfg.ExtendedOptions {
		if !isAllowedValue(option, "parameter_substitution", "data_returned", "advanced_trace") {
			issues = append(issues, fmt.Sprintf("config.log.extended_options[%d] must be parameter_substitution, data_returned, or advanced_trace", index))
		}
	}

	httpCfg := script.Config.HTTP
	if err := httpprotocol.ValidateVersion(httpCfg.Version); err != nil {
		issues = append(issues, fmt.Sprintf("config.http.version: %v", err))
	}
	issues = append(issues, validateHTTPQUICConfigIssues(httpCfg.QUIC)...)
	if httpCfg.MaxConnsPerHost < 0 || httpCfg.MaxConnsPerHost > httpprotocol.MaxConfiguredConnsPerHost {
		issues = append(issues, fmt.Sprintf("config.http.max_conns_per_host must be between 0 and %d", httpprotocol.MaxConfiguredConnsPerHost))
	}
	if httpCfg.MaxIdleConns < 0 || httpCfg.MaxIdleConns > httpprotocol.MaxConfiguredIdleConns {
		issues = append(issues, fmt.Sprintf("config.http.max_idle_conns must be between 0 and %d", httpprotocol.MaxConfiguredIdleConns))
	}
	httpCapabilities := httpprotocol.Capabilities()
	maxHTTPTimeoutMillis := httpCapabilities.MaxTimeoutMS
	for _, timeout := range []struct {
		name  string
		value int
	}{
		{name: "connect_timeout_ms", value: httpCfg.ConnectTimeoutMs},
		{name: "request_timeout_ms", value: httpCfg.RequestTimeoutMs},
		{name: "tls_timeout_ms", value: httpCfg.TLSTimeoutMs},
		{name: "idle_timeout_ms", value: httpCfg.IdleTimeoutMs},
		{name: "page_timeout_ms", value: httpCfg.PageTimeoutMs},
		{name: "challenge_delay_ms", value: httpCfg.AuthDelayMs},
	} {
		if err := httpprotocol.ValidateTimeoutMS(timeout.value); err != nil {
			issues = append(issues, fmt.Sprintf("config.http.%s must be between 0 and %d", timeout.name, maxHTTPTimeoutMillis))
		}
	}
	if err := httpprotocol.ValidateReplayCompatibilityConfig(map[string]any{
		"receive_buffer_bytes": httpCfg.ReceiveBufferBytes, "max_meta_refresh": httpCfg.MaxMetaRefresh, "content_check_error_limit": httpCfg.ContentCheckErrorLimit,
		"ip_version": httpCfg.IPVersion, "html_parse_mode": httpCfg.HTMLParseMode, "aura_recorded_failures": httpCfg.AuraRecordedFailures,
		"cache_max_unused_iterations": httpCfg.CacheMaxUnusedIterations,
		"cache_excluded_mime_types":   httpCfg.CacheExcludedMIMETypes,
		"html_excluded_mime_types":    httpCfg.HTMLExcludedMIMETypes,
	}); err != nil {
		issues = append(issues, "config.http: "+err.Error())
	}
	if err := httpprotocol.ValidateMaxRedirects(httpCfg.MaxRedirects); err != nil {
		issues = append(issues, fmt.Sprintf("config.http.max_redirects must be between -1 and %d", httpCapabilities.MaxRedirects))
	}
	if err := httpprotocol.ValidateAuthType(httpCfg.Auth.Type); err != nil {
		issues = append(issues, fmt.Sprintf("config.http.auth.type: %v", err))
	}
	if err := httpprotocol.ValidateAuthHosts(httpCfg.Auth.Hosts); err != nil {
		issues = append(issues, fmt.Sprintf("config.http.auth.hosts: %v", err))
	}
	if httpCfg.TLS.MinVersion != "" && !isAllowedValue(httpCfg.TLS.MinVersion, "1.0", "1.1", "1.2", "1.3") {
		issues = append(issues, "config.http.tls.min_version must be 1.0, 1.1, 1.2, or 1.3")
	}
	if httpCfg.TLS.MaxVersion != "" && !isAllowedValue(httpCfg.TLS.MaxVersion, "1.0", "1.1", "1.2", "1.3") {
		issues = append(issues, "config.http.tls.max_version must be 1.0, 1.1, 1.2, or 1.3")
	}
	minTLSVersionRank := httpTLSVersionRank(httpCfg.TLS.MinVersion)
	if strings.TrimSpace(httpCfg.TLS.MinVersion) == "" {
		minTLSVersionRank = httpTLSVersionRank("1.2")
	}
	maxTLSVersionRank := httpTLSVersionRank(httpCfg.TLS.MaxVersion)
	if minTLSVersionRank > maxTLSVersionRank && maxTLSVersionRank != 0 {
		issues = append(issues, "config.http.tls.max_version must not be lower than min_version")
	}
	if httpCfg.ProxyMode != "" && !isAllowedValue(httpCfg.ProxyMode, "none", "system", "custom", "pac") {
		issues = append(issues, "config.http.proxy_mode must be none, system, custom, or pac")
	}
	if httpCfg.ProxyMode == "pac" {
		u, err := url.Parse(httpCfg.Proxy.PACURL)
		if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || len(httpCfg.Proxy.PACURL) > 4096 {
			issues = append(issues, "config.http.proxy.pac_url requires an absolute HTTP(S) URL without userinfo")
		}
	}
	if httpCfg.ProxyMode == "custom" && strings.TrimSpace(httpCfg.Proxy.URL) == "" {
		issues = append(issues, "config.http.proxy.url is required when config.http.proxy_mode is custom")
	} else if httpCfg.ProxyMode == "custom" {
		proxyURL, err := url.Parse(strings.TrimSpace(httpCfg.Proxy.URL))
		if err != nil || proxyURL.Host == "" || !isAllowedValue(strings.ToLower(proxyURL.Scheme), "http", "https", "socks5", "socks5h") {
			issues = append(issues, "config.http.proxy.url must be an absolute http, https, socks5, or socks5h URL")
		}
	}

	browserCfg := script.Config.Browser
	if browserCfg.DownloadResources != "" && !isAllowedValue(browserCfg.DownloadResources, "none", "recorded", "all") {
		issues = append(issues, "config.browser.download_resources must be none, recorded, or all")
	}

	filters := script.Config.WebRuntime.DownloadFilters
	if filters.Mode != "" && !isAllowedValue(filters.Mode, "none", "allow_list", "block_list") {
		issues = append(issues, "config.web_runtime.download_filters.mode must be none, allow_list, or block_list")
	}
	if (filters.Mode == "allow_list" || filters.Mode == "block_list") && len(filters.Hosts) == 0 {
		issues = append(issues, "config.web_runtime.download_filters.hosts is required when download filter mode is allow_list or block_list")
	}

	for index, check := range script.Config.WebRuntime.ContentChecks {
		if (check.LeftBoundary == "") != (check.RightBoundary == "") || len(check.LeftBoundary) > 4096 || len(check.RightBoundary) > 4096 {
			issues = append(issues, fmt.Sprintf("content_checks[%d] requires paired boundaries of at most 4096 bytes", index))
		}
		if strings.TrimSpace(check.Text) == "" && (check.LeftBoundary == "" || check.RightBoundary == "") {
			issues = append(issues, fmt.Sprintf("config.web_runtime.content_checks[%d].text is required", index))
		}
		if check.Scope != "" && !isAllowedValue(check.Scope, "any_response", "main_document", "resource") {
			issues = append(issues, fmt.Sprintf("config.web_runtime.content_checks[%d].scope must be any_response, main_document, or resource", index))
		}
	}

	network := script.Config.WebRuntime.NetworkSimulation
	if network.Mode != "" && !isAllowedValue(network.Mode, "max", "preset", "custom") {
		issues = append(issues, "config.web_runtime.network_simulation.mode must be max, preset, or custom")
	}
	if network.Mode == "preset" && !isAllowedValue(network.Preset, "56k", "dsl", "t1", "10mbps") {
		issues = append(issues, "config.web_runtime.network_simulation.preset must be 56k, dsl, t1, or 10mbps when mode is preset")
	}
	if network.Mode == "custom" && network.BandwidthKbps <= 0 && network.LatencyMs <= 0 && network.JitterMs <= 0 && network.PacketLossPct <= 0 {
		issues = append(issues, "config.web_runtime.network_simulation custom mode requires bandwidth_kbps, latency_ms, jitter_ms, or packet_loss_pct")
	}
	if network.LatencyMs < 0 {
		issues = append(issues, "config.web_runtime.network_simulation.latency_ms must be zero or greater")
	} else if network.LatencyMs > int(browserprotocol.MaxConfiguredBrowserDelay/time.Millisecond) {
		issues = append(issues, fmt.Sprintf("config.web_runtime.network_simulation.latency_ms must not exceed %d", browserprotocol.MaxConfiguredBrowserDelay/time.Millisecond))
	}
	if network.BandwidthKbps < 0 {
		issues = append(issues, "config.web_runtime.network_simulation.bandwidth_kbps must be zero or greater")
	} else if network.BandwidthKbps > browserprotocol.MaxConfiguredNetworkKbps {
		issues = append(issues, fmt.Sprintf("config.web_runtime.network_simulation.bandwidth_kbps must not exceed %d", browserprotocol.MaxConfiguredNetworkKbps))
	}
	if network.JitterMs < 0 {
		issues = append(issues, "config.web_runtime.network_simulation.jitter_ms must be zero or greater")
	} else if network.JitterMs > int(httpprotocol.MaxConfiguredTimeout/time.Millisecond) {
		issues = append(issues, fmt.Sprintf("config.web_runtime.network_simulation.jitter_ms must not exceed %d", httpprotocol.MaxConfiguredTimeout/time.Millisecond))
	}
	if network.PacketLossPct < 0 || network.PacketLossPct > 100 {
		issues = append(issues, "config.web_runtime.network_simulation.packet_loss_pct must be between 0 and 100")
	}

	streaming := script.Config.WebRuntime.Streaming
	if streaming.Mode != "" && !isAllowedValue(streaming.Mode, "adaptive", "fixed") {
		issues = append(issues, "config.web_runtime.streaming.mode must be adaptive or fixed")
	}
	if streaming.BufferSizeSeconds < 0 {
		issues = append(issues, "config.web_runtime.streaming.buffer_size_seconds must be zero or greater")
	} else if streaming.BufferSizeSeconds > int(httpprotocol.MaxConfiguredStreamBuffer/time.Second) {
		issues = append(issues, fmt.Sprintf("config.web_runtime.streaming.buffer_size_seconds must not exceed %d", httpprotocol.MaxConfiguredStreamBuffer/time.Second))
	}
	if streaming.Mode == "fixed" && streaming.FixedBitrateKbps <= 0 {
		issues = append(issues, "config.web_runtime.streaming.fixed_bitrate_kbps must be greater than zero when mode is fixed")
	} else if streaming.FixedBitrateKbps > httpprotocol.MaxConfiguredStreamKbps {
		issues = append(issues, fmt.Sprintf("config.web_runtime.streaming.fixed_bitrate_kbps must not exceed %d", httpprotocol.MaxConfiguredStreamKbps))
	}
	if streaming.BandwidthEstimation != "" && !isAllowedValue(streaming.BandwidthEstimation, "rolling", "instant") {
		issues = append(issues, "config.web_runtime.streaming.bandwidth_estimation must be rolling or instant")
	}

	if err := protocol.ValidateTraceCarrier(script.Config.Observability.TraceCarrier); err != nil {
		issues = append(issues, "config.observability."+err.Error())
	}
	for index, metric := range script.Config.Observability.ExternalMetrics {
		prefix := fmt.Sprintf("config.observability.external_metrics[%d]", index)
		if strings.TrimSpace(metric.Name) == "" {
			issues = append(issues, prefix+".name is required")
		}
		if strings.TrimSpace(metric.URL) == "" {
			issues = append(issues, prefix+".url is required")
		} else if parsed, err := url.ParseRequestURI(metric.URL); err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			issues = append(issues, prefix+".url must be a valid http or https URL")
		}
		format := strings.TrimSpace(metric.Format)
		if format != "" && !isAllowedValue(format, "prometheus") {
			issues = append(issues, prefix+".format must be prometheus")
		}
		if strings.TrimSpace(metric.MetricName) == "" {
			issues = append(issues, prefix+".metric_name is required")
		}
	}

	for index, transform := range script.Config.WebRuntime.DataFormatting.Chain {
		transformType := strings.TrimSpace(transform.Type)
		if transformType == "" {
			issues = append(issues, fmt.Sprintf("config.web_runtime.data_formatting.chain[%d].type is required", index))
			continue
		}
		if !isAllowedDataFormattingTransform(transformType) {
			issues = append(issues, fmt.Sprintf("config.web_runtime.data_formatting.chain[%d].type %q is not supported", index, transform.Type))
		}
	}

	return issues
}

func httpTLSVersionRank(version string) int {
	switch strings.TrimSpace(version) {
	case "1.0":
		return 1
	case "1.1":
		return 2
	case "1.2":
		return 3
	case "1.3":
		return 4
	default:
		return 0
	}
}

func isAllowedValue(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func isAllowedDataFormattingTransform(value string) bool {
	return isAllowedValue(value,
		"base64",
		"gzip",
		"json_to_xml",
		"xml_to_json",
		"url_decode",
		"url_encode",
		"html_unescape",
		"html_escape",
	)
}
