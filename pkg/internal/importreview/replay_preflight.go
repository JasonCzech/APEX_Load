package importreview

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dsl/varflow"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol/eventhubs"
	"github.com/jasonczech/apexload/pkg/protocol/kafka"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// The real Kafka and Event Hubs network clients are compiled in only with the
// kafka_real and eventhubs_real build tags (the enterprise image). Without
// them the protocol still loads, so mock replay works, but every non-mock
// send fails at run time. The capability probes do no network I/O.
var (
	realKafkaAdapterCompiled = func() bool {
		return slices.Contains((&kafka.Kafka{}).RuntimeCapabilities().Features, kafka.FeatureRealSeekV1)
	}
	realEventHubsAdapterCompiled = func() bool {
		return (&eventhubs.EventHubs{}).RuntimePauseSupported()
	}
)

const (
	kafkaAdapterMissingWarning = "This APEX Load build has no real Kafka client (it was built without the kafka_real tag), " +
		"so sends to the configured brokers fail when the script runs. Run it on an enterprise build, " +
		"or turn on config.kafka.mock for a local shakeout."
	eventHubsAdapterMissingWarning = "This APEX Load build has no real Azure Event Hubs client (it was built without the " +
		"eventhubs_real tag), so sends to the configured namespace fail when the script runs. Run it on an " +
		"enterprise build, or turn on config.eventhubs.mock for a local shakeout."
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

	// RequiredEnvironment are the {{env.*}} names the script reads, and
	// UnreadableEnvironment those a script cannot read in this controller's
	// process: an always-blocked name, one missing from
	// APEXLOAD_SCRIPT_ENV_ALLOWLIST, or one not set. They warn; they do not
	// refuse replay.
	RequiredEnvironment   []string `json:"required_environment,omitempty"`
	UnreadableEnvironment []string `json:"unreadable_environment,omitempty"`
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
	unverifiedAuraVars := unverifiedAuraTokenVariables(script, variables)
	if len(unverifiedAuraVars) > 0 {
		preflight.Warnings = append(preflight.Warnings, "aura.token cookie (__Host-ERIC_*) is expected from /one/one.app at live replay (unverified in sanitized HAR capture): "+strings.Join(unverifiedAuraVars, ", "))
	}
	preflight.RequiredSecrets, preflight.MissingSecrets = ReplaySecretRefs(script, secretResolver)
	dataWarnings := ReplayDataSourceWarnings(script)
	if len(dataWarnings) > 0 {
		preflight.Warnings = append(preflight.Warnings, dataWarnings...)
		// They are worded as warnings but refuse replay (see Valid below), so
		// they are also listed on their own for a client to show as blockers.
		preflight.DataSourceIssues = append([]string(nil), dataWarnings...)
	}
	preflight.Warnings = append(preflight.Warnings, ReplaySecretWarnings(script, preflight.MissingSecrets, controllerManagedSecrets)...)
	preflight.Warnings = append(preflight.Warnings, ReplayInactiveProxySecretNotes(script, secretResolver)...)
	var environmentDetail string
	preflight.RequiredEnvironment, preflight.UnreadableEnvironment, environmentDetail = ReplayEnvironmentRefs(script, controllerManagedSecrets)
	if len(preflight.UnreadableEnvironment) > 0 {
		preflight.Warnings = append(preflight.Warnings, ReplayUnreadableEnvironmentWarning+environmentDetail+". "+replayEnvironmentRemedy)
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
			Status: ReplayCheckStatus(len(readiness.Blocking) == 0, len(preflight.MissingVariables) > 0 || len(unverifiedAuraVars) > 0),
			Detail: replayVariableDetailWithUnverifiedAura(readiness, unverifiedAuraVars),
		},
		{
			ID:     "secrets",
			Label:  "Secret bindings",
			Status: ReplayCheckStatus(!controllerManagedSecrets || len(preflight.MissingSecrets) == 0, len(preflight.RequiredSecrets) > 0),
			Detail: ReplaySecretCheckDetail(script, preflight.RequiredSecrets, preflight.MissingSecrets),
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
	if len(preflight.RequiredEnvironment) > 0 {
		detail := "Readable: " + strings.Join(preflight.RequiredEnvironment, ", ")
		if !controllerManagedSecrets {
			detail = "Referenced: " + strings.Join(preflight.RequiredEnvironment, ", ") + " (checked when a replay or debug starts on the controller that runs it)"
		}
		if len(preflight.UnreadableEnvironment) > 0 {
			detail = "Not readable here: " + environmentDetail + ". " + replayEnvironmentRemedy
		}
		preflight.Checks = append(preflight.Checks, ReplayPreflightCheck{
			ID:     "environment",
			Label:  "Environment variables",
			Status: ReplayCheckStatus(true, len(preflight.UnreadableEnvironment) > 0),
			Detail: detail,
		})
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
			if realKafkaAdapterCompiled() {
				detail = append(detail, "real Kafka client available")
			} else {
				warnings = append(warnings, kafkaAdapterMissingWarning)
			}
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
			Detail: ReplayListDetail(issues, strings.Join(slices.Concat(warnings, detail), "; ")),
		})
	case "eventhubs":
		mock := script.Config.EventHubs.Mock
		detail := "real Azure Event Hubs client available"
		switch {
		case mock:
			detail = "mock Event Hubs enabled"
		case !realEventHubsAdapterCompiled():
			warnings = append(warnings, eventHubsAdapterMissingWarning)
			detail = eventHubsAdapterMissingWarning
		}
		checks = append(checks, ReplayPreflightCheck{
			ID:     "messaging_eventhubs",
			Label:  "Event Hubs readiness",
			Status: ReplayCheckStatus(true, len(warnings) > 0 || mock),
			Detail: detail,
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
	cloned.RequiredEnvironment = append([]string(nil), preflight.RequiredEnvironment...)
	cloned.UnreadableEnvironment = append([]string(nil), preflight.UnreadableEnvironment...)
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

func replayVariableDetailWithUnverifiedAura(readiness requestimport.VariableReadiness, unverifiedAuraVars []string) string {
	if len(unverifiedAuraVars) == 0 {
		return ReplayVariableDetail(readiness)
	}
	if len(readiness.Missing) == 0 {
		return "Expected at live replay (unverified in sanitized HAR capture): " + strings.Join(unverifiedAuraVars, ", ") + "."
	}
	return ReplayVariableDetail(readiness) + "; expected at live replay (unverified in capture): " + strings.Join(unverifiedAuraVars, ", ")
}

func unverifiedAuraTokenVariables(script *dsl.Script, variables map[string]string) []string {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved == nil {
		return nil
	}
	rawReview, ok := script.Metadata.Import.Preserved["salesforce_review"]
	if !ok || rawReview == nil {
		return nil
	}
	candidateNames := make(map[string]struct{})
	addReviewCorrelations := func(list []salesforce.CorrelationCandidate) {
		for _, item := range list {
			if strings.TrimSpace(item.RuleID) == correlate.RuleSalesforceAuraTokenUncapturedCookie && strings.TrimSpace(item.Name) != "" {
				candidateNames[strings.TrimSpace(item.Name)] = struct{}{}
			}
		}
	}
	switch typed := rawReview.(type) {
	case *salesforce.Review:
		if typed != nil && typed.NativePreview != nil {
			addReviewCorrelations(typed.NativePreview.Correlations)
		}
	case salesforce.Review:
		if typed.NativePreview != nil {
			addReviewCorrelations(typed.NativePreview.Correlations)
		}
	case map[string]any:
		collectMapCorrs := func(raw any) {
			items, _ := raw.([]any)
			for _, elem := range items {
				m, _ := elem.(map[string]any)
				ruleID, _ := m["rule_id"].(string)
				name, _ := m["name"].(string)
				if strings.TrimSpace(ruleID) == correlate.RuleSalesforceAuraTokenUncapturedCookie && strings.TrimSpace(name) != "" {
					candidateNames[strings.TrimSpace(name)] = struct{}{}
				}
			}
		}
		collectMapCorrs(typed["correlations"])
		if np, ok := typed["native_preview"].(map[string]any); ok {
			collectMapCorrs(np["correlations"])
		}
	}
	if len(candidateNames) == 0 {
		return nil
	}
	extractedInScript := make(map[string]struct{})
	for _, action := range ReplayOrderedActions(script) {
		for _, ext := range action.Extractors {
			if name := strings.TrimSpace(ext.Name); name != "" {
				extractedInScript[name] = struct{}{}
			}
		}
	}

	var out []string
	for name := range candidateNames {
		if _, ok := extractedInScript[name]; !ok {
			continue
		}
		if variables != nil {
			if val, seeded := variables[name]; seeded && strings.TrimSpace(val) != "" {
				continue
			}
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ReplayUnreadableEnvironmentWarning begins the warning BuildReplayPreflight
// writes for {{env.*}} names a script cannot read here.
const ReplayUnreadableEnvironmentWarning = "environment variable(s) scripts cannot read: "

const replayEnvironmentRemedy = "Set each where the script runs (this controller for replay and debugging, " +
	"every load generator for a test) and list it in " + engine.EnvScriptEnvAllowlist + "; a name that is always " +
	"blocked is never read from a process or pod variable, so supply it as a test's runtime environment override " +
	"or rename it, here and in the {{env.*}} reference"

// ReplayEnvironmentRefs lists the {{env.*}} names the script's executable
// steps and configuration read, those a script cannot read, and a detail
// naming each unreadable one with its reason. It reports names, never values.
//
// Only an always-blocked name is unreadable everywhere. Whether an allowed
// name is listed and set depends on the process that runs the script, so
// those reasons are reported only when onThisController: a replay or debug
// start, which runs here. An import or authoring preview stores its preflight
// with the script, where another controller's state would read as current.
func ReplayEnvironmentRefs(script *dsl.Script, onThisController bool) (required []string, unreadable []string, detail string) {
	required = replayEnvironmentNames(script)
	reasons := make([]string, 0)
	for _, name := range required {
		var reason string
		switch status := engine.ScriptEnvKeyStatusOf(name); {
		case status == engine.ScriptEnvKeyReadable:
			continue
		case status == engine.ScriptEnvKeyBlockedName:
			reason = "the name is always blocked from the process environment (it names a password, token, secret, key or credential, or a cloud or secret-store namespace)"
		case !onThisController:
			continue
		case status == engine.ScriptEnvKeyNotAllowlisted:
			reason = "not listed in " + engine.EnvScriptEnvAllowlist
		default:
			reason = "not set"
		}
		unreadable = append(unreadable, name)
		reasons = append(reasons, name+" ("+reason+")")
	}
	return required, unreadable, strings.Join(reasons, ", ")
}

// replayEnvironmentNames collects {{env.*}} names from what runs: each step's
// own fields (not its metadata, which records provenance, and not a step an
// import left for review), base64 bodies decoded, and the configuration.
func replayEnvironmentNames(script *dsl.Script) []string {
	if script == nil {
		return nil
	}
	names := make(map[string]struct{})
	collect := func(text string) {
		for _, match := range ReplayTemplateRefPattern.FindAllStringSubmatch(text, -1) {
			if len(match) >= 3 && match[1] == "env" {
				if name := strings.TrimSpace(match[2]); name != "" {
					names[name] = struct{}{}
				}
			}
		}
	}
	var scan func(actions []dsl.Action, depth int)
	scan = func(actions []dsl.Action, depth int) {
		for _, action := range actions {
			if payload, err := json.Marshal(action); err == nil {
				var shape map[string]any
				if json.Unmarshal(payload, &shape) == nil && (IsNonExecutableReviewAction(shape) || IsDurablyImportReviewGatedAction(shape)) {
					continue
				}
			}
			// A control step arrives as its shell (its condition, without the
			// steps it holds, which are listed on their own); a parallel
			// block's branches are walked here, step by step.
			// Metadata records provenance, except entries a messaging send
			// carries as headers or properties, which are interpolated.
			executable := action
			executable.Metadata = nil
			for key, value := range action.Metadata {
				if sent, _ := varflow.MetadataReads(action, key); sent {
					if executable.Metadata == nil {
						executable.Metadata = map[string]any{}
					}
					executable.Metadata[key] = value
				}
			}
			if action.Control != nil && len(action.Control.Branches) > 0 {
				control := *action.Control
				control.Branches = nil
				executable.Control = &control
				if depth < ReplayPreflightMaxControlDepth {
					for _, branch := range action.Control.Branches {
						scan(FlattenReplayActionsForReadiness(branch.Steps, depth+1), depth+1)
					}
				}
			}
			if payload, err := json.Marshal(executable); err == nil {
				collect(string(payload))
			}
			if strings.EqualFold(strings.TrimSpace(action.BodyEncoding), "base64") {
				if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(action.Body)); err == nil {
					collect(string(decoded))
				}
			}
		}
	}
	scan(ReplayOrderedActions(script), 0)
	if payload, err := json.Marshal(script.Config); err == nil {
		collect(string(payload))
	}
	return SortedSetKeys(names)
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
	// Required, not referenced: a proxy credential the runtime never sends
	// for the current proxy settings is neither required nor missing
	// (ReplayInactiveProxySecretNotes reports it without blocking).
	secretNames, err := engine.RequiredSecretNames(script)
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

// Replay secret warning prefixes. Missing secrets used only by an active proxy
// credential are worded by dsl.ProxyCredentialSecretReference.BlockerMessage
// instead.
const (
	ReplayMissingSecretsWarning      = "configure project secret(s) before replay: "
	ReplayUnconfiguredSecretsWarning = "secret(s) require project configuration before execution: "
)

// ReplaySecretWarnings words missing required secrets. A name only an active
// custom (or browser) proxy credential uses gets the proxy-specific blocker
// text, which says to bind a secret or change the proxy setting; every other
// name keeps the generic configure line. Controller-managed checks (a governed
// resolver) say "before replay"; import and authoring previews, which have no
// resolver and stay saveable, say "before execution".
func ReplaySecretWarnings(script *dsl.Script, missing []string, controllerManaged bool) []string {
	if len(missing) == 0 {
		return nil
	}
	proxyMessages, others := engine.MissingSecretProxyBlockers(script, missing)
	warnings := append([]string(nil), proxyMessages...)
	if len(others) > 0 {
		prefix := ReplayUnconfiguredSecretsWarning
		if controllerManaged {
			prefix = ReplayMissingSecretsWarning
		}
		warnings = append(warnings, prefix+strings.Join(others, ", "))
	}
	return warnings
}

// ReplayInactiveProxySecretNotes returns the non-blocking notes for unbound
// secrets referenced only from proxy credential fields the runtime does not
// send (an HTTP proxy in No proxy, System or PAC mode, or a browser proxy
// without a server). The reference stays in the script so it is ready if the
// proxy later becomes custom; until then it never blocks. A resolver checks
// binding the way ReplaySecretRefs does; without one the legacy environment
// probe answers.
func ReplayInactiveProxySecretNotes(script *dsl.Script, resolvers ...engine.SecretResolver) []string {
	if script == nil {
		return nil
	}
	var inactive []dsl.ProxyCredentialSecretReference
	for _, ref := range dsl.ProxyCredentialSecretReferences(script) {
		if !ref.Active {
			inactive = append(inactive, ref)
		}
	}
	if len(inactive) == 0 {
		return nil
	}
	requiredNames, err := engine.RequiredSecretNames(script)
	if err != nil {
		return nil
	}
	required := make(map[string]struct{}, len(requiredNames))
	for _, name := range requiredNames {
		required[name] = struct{}{}
	}
	var candidates []string
	for _, ref := range inactive {
		if _, ok := required[ref.Name]; !ok {
			candidates = append(candidates, ref.Name)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	var resolver engine.SecretResolver
	if len(resolvers) > 0 {
		resolver = resolvers[0]
	}
	unbound := make(map[string]struct{})
	if resolver != nil {
		ctx, cancel := engine.WithSecretResolutionBudget(context.Background())
		_, unresolved, resolutionErr := engine.ResolveSecretNames(ctx, candidates, resolver)
		cancel()
		if resolutionErr != nil {
			unresolved = candidates
		}
		for _, name := range unresolved {
			unbound[name] = struct{}{}
		}
	} else {
		for _, name := range candidates {
			if !ReplaySecretAvailable(script.ProjectID, name) {
				unbound[name] = struct{}{}
			}
		}
	}
	var notes []string
	for _, ref := range inactive {
		if _, ok := unbound[ref.Name]; ok {
			notes = append(notes, ref.InactiveNote())
		}
	}
	return notes
}

// ReplaySecretCheckDetail is the secrets check detail: the proxy-specific
// blocker for a missing name only an active proxy credential uses, then the
// generic ReplaySecretDetail wording for the rest. A name referenced only by
// an inactive proxy credential field is not required, but the script still
// references it, so the detail names it as not needed instead of claiming
// the script has no secret references.
func ReplaySecretCheckDetail(script *dsl.Script, required []string, missing []string) string {
	inactive := replayInactiveProxySecretDetail(script, required)
	if len(required) == 0 && inactive != "" {
		return "No required project secrets. " + inactive
	}
	detail := replaySecretRequiredDetail(script, required, missing)
	if inactive != "" {
		detail += "; " + inactive
	}
	return detail
}

func replaySecretRequiredDetail(script *dsl.Script, required []string, missing []string) string {
	if len(required) == 0 || len(missing) == 0 {
		return ReplaySecretDetail(required, missing)
	}
	proxyMessages, others := engine.MissingSecretProxyBlockers(script, missing)
	if len(proxyMessages) == 0 {
		return ReplaySecretDetail(required, missing)
	}
	parts := append([]string(nil), proxyMessages...)
	if len(others) > 0 {
		parts = append(parts, "Missing: "+strings.Join(others, ", "))
	}
	return strings.Join(parts, "; ")
}

// replayInactiveProxySecretDetail names the secrets referenced only by proxy
// credential fields the runtime does not send ("" when there are none).
func replayInactiveProxySecretDetail(script *dsl.Script, required []string) string {
	requiredSet := make(map[string]struct{}, len(required))
	for _, name := range required {
		requiredSet[name] = struct{}{}
	}
	var httpNames, browserNames []string
	for _, ref := range dsl.ProxyCredentialSecretReferences(script) {
		if ref.Active {
			continue
		}
		if _, ok := requiredSet[ref.Name]; ok {
			continue
		}
		if ref.Browser {
			browserNames = append(browserNames, ref.Name)
		} else {
			httpNames = append(httpNames, ref.Name)
		}
	}
	var parts []string
	if names := sortedUniqueNames(httpNames); len(names) > 0 {
		parts = append(parts, "Not needed for the current Proxy mode: "+strings.Join(names, ", "))
	}
	if names := sortedUniqueNames(browserNames); len(names) > 0 {
		parts = append(parts, "Not needed without a browser proxy server: "+strings.Join(names, ", "))
	}
	return strings.Join(parts, "; ")
}

func sortedUniqueNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	out := append([]string(nil), names...)
	slices.Sort(out)
	return slices.Compact(out)
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
	actions := make([]dsl.Action, 0, len(script.Actions))
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
