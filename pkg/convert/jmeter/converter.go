package jmeter

import (
	"encoding/json"
	"fmt"
	"github.com/jasonczech/apexload/pkg/convert/convertresult"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/convert/uniquify"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

type ConvertOptions struct {
	ScriptName        string
	BaseURL           string
	IncludeAssertions bool
	ScenarioDraft     bool
	FileName          string
	SidecarFiles      map[string][]byte
	// CredentialRegistry lets a multi-document caller allocate logical secret
	// names across the complete batch instead of restarting identity per JMX.
	CredentialRegistry *credentialpolicy.NameRegistry
}

type ConvertResult struct {
	Scripts             []*dsl.Script                  `json:"scripts"`
	Script              *dsl.Script                    `json:"script,omitempty"`
	ScenarioDraft       *ScenarioDraft                 `json:"scenario_draft,omitempty"`
	Warnings            []string                       `json:"warnings,omitempty"`
	UnsupportedElements []string                       `json:"unsupported_elements,omitempty"`
	Unsupported         diag.Diagnostics               `json:"unsupported,omitempty"`
	Assumptions         []string                       `json:"assumptions,omitempty"`
	Findings            []importreview.Finding         `json:"findings,omitempty"`
	SecretRequirements  []credentialpolicy.Requirement `json:"secret_requirements,omitempty"`
	Stats               ConvertStats                   `json:"stats"`
	Summary             ImportSummary                  `json:"summary"`
	SourceReview        *sourceview.Review             `json:"source_review,omitempty"`
}

type ConvertStats struct {
	ThreadGroups     int `json:"thread_groups"`
	Samplers         int `json:"samplers"`
	TotalSamplers    int `json:"total_samplers,omitempty"`
	SkippedSamplers  int `json:"skipped_samplers,omitempty"`
	ConvertedActions int `json:"converted_actions"`
	Headers          int `json:"headers"`
	Variables        int `json:"variables"`
	CSVSources       int `json:"csv_sources"`
	Assertions       int `json:"assertions"`
	Timers           int `json:"timers"`
	Unsupported      int `json:"unsupported"`
	// Four-way classification of converted JMeter elements (samplers and
	// top-level controllers), derived from the source mappings. Contributors
	// (extractors/assertions/timers) and informational elements (listeners,
	// DebugSampler) are not counted here.
	Mapped          int `json:"mapped"`
	ReviewRequired  int `json:"review_required"`
	AdapterRequired int `json:"adapter_required"`
}

type ImportSummary struct {
	FileName             string   `json:"file_name,omitempty"`
	ThreadGroupCount     int      `json:"thread_group_count"`
	SamplerCount         int      `json:"sampler_count"`
	VariablesDiscovered  []string `json:"variables_discovered,omitempty"`
	CSVSourcesDiscovered []string `json:"csv_sources_discovered,omitempty"`
}

type ScenarioDraft struct {
	Name         string                 `json:"name"`
	Duration     string                 `json:"duration"`
	ScenarioMode string                 `json:"scenario_mode"`
	Groups       []ScenarioDraftGroup   `json:"groups"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

type ScenarioDraftGroup struct {
	Name           string `json:"name"`
	ScriptName     string `json:"script_name"`
	VUsers         int    `json:"vusers"`
	RampUp         string `json:"ramp_up,omitempty"`
	Initialization string `json:"initialization"`
}

type SupportMatrix struct {
	Supported   []string `json:"supported"`
	Partial     []string `json:"partial"`
	Unsupported []string `json:"unsupported"`
	Notes       []string `json:"notes,omitempty"`
}

type xmlNode struct {
	Name      string
	Attrs     map[string]string
	Text      string
	Children  []*xmlNode
	StartLine int
	EndLine   int
}

type treeNode struct {
	Element  *xmlNode
	Children []*treeNode
}

type convertContext struct {
	defaultProtocol        string
	defaultDomain          string
	defaultPort            string
	defaultPath            string
	effectiveBaseURL       string
	headers                dsl.HeaderList
	variableScope          *jmeterVariableScope
	provenVariables        map[string]struct{}
	actionNames            map[string]int
	csvSources             []dsl.DataSource
	timerMs                int
	oneShotTimerMs         int
	assertions             []dsl.Assertion
	auths                  []jmeterAuth
	defaultAuthSeen        bool
	requestReports         *[]requestimport.Report
	credentialNames        *credentialpolicy.NameRegistry
	credentialRequirements []credentialpolicy.Requirement
	importErr              *error
	transaction            string
	// scriptProtocol is the protocol resolved for the owning thread group from
	// its sampler mix. Native sampler converters only emit executable actions
	// when the script protocol matches; otherwise the sampler is review-gated.
	scriptProtocol string
	databaseConfig *dsl.DatabaseConfig
	networkConfig  *dsl.NetworkConfig
}

type jmeterAuth struct {
	URL        string
	Username   string
	Password   string
	Domain     string
	Mechanism  string
	Config     dsl.AuthConfig
	ReviewCode string
	SourceRef  string
}

func ConvertJMX(data []byte, opts ConvertOptions) (*ConvertResult, error) {
	root, err := parseXML(data)
	if err != nil {
		return nil, err
	}
	hashTree := firstChild(root, "hashTree")
	if hashTree == nil {
		return nil, fmt.Errorf("jmeter plan does not contain a root hashTree")
	}
	trees := buildTree(hashTree)
	if len(trees) == 0 {
		return nil, fmt.Errorf("jmeter plan is empty")
	}

	result := &ConvertResult{
		Scripts:     []*dsl.Script{},
		Warnings:    []string{},
		Assumptions: []string{"JMeter controller structure was flattened into script action order where safe."},
		Summary:     ImportSummary{FileName: opts.FileName},
	}
	importedAt := time.Now().UTC().Format(time.RFC3339)
	unsupported := map[string]struct{}{}
	variableNames := map[string]struct{}{}
	csvNames := map[string]struct{}{}
	var diagnostics diag.Collector
	sourceFileName := convertutil.FirstNonEmpty(opts.FileName, "import.jmx")
	sourceMappings := make([]sourceview.Mapping, 0)
	allRequirements := make([]credentialpolicy.Requirement, 0)
	allFindings := make([]importreview.Finding, 0)
	credentialNames := opts.CredentialRegistry
	if credentialNames == nil {
		credentialNames = credentialpolicy.NewNameRegistry()
	}
	variableReview := newJMeterVariableReview()
	type variableSet struct {
		values map[string]string
		source credentialpolicy.SourceRef
	}
	planVariableSets := make([]variableSet, 0)
	appendDisabledJMeterSelectionMappings(trees, sourceFileName, &sourceMappings)
	enabledPlans := findEnabledTreeNodes(trees, "TestPlan")
	for _, plan := range enabledPlans {
		values := parseArguments(plan.Element)
		if len(values) == 0 {
			continue
		}
		planVariableSets = append(planVariableSets, variableSet{
			values: values,
			source: credentialpolicy.SourceRef{
				Format:  "jmeter",
				Path:    sourceFileName,
				Pointer: jmeterElementLocation(plan.Element),
			},
		})
		for name := range values {
			variableNames[name] = struct{}{}
			result.Stats.Variables++
		}
	}

	threadGroups := findEnabledTreeNodes(trees, "ThreadGroup")
	if len(threadGroups) == 0 {
		threadGroups = enabledPlans
		if len(threadGroups) == 0 {
			threadGroups = enabledJMeterTreeRoots(trees)
		}
		if len(threadGroups) > 0 {
			result.Assumptions = append(result.Assumptions, "No enabled Thread Group was found; the enabled plan was converted into a single script.")
		} else {
			result.Warnings = append(result.Warnings, "No enabled JMeter plan or Thread Group was found; disabled source elements were retained only for review.")
		}
	}
	result.Stats.ThreadGroups = len(threadGroups)
	result.Stats.TotalSamplers, result.Stats.SkippedSamplers = countJMeterSamplerElements(trees)
	result.Summary.ThreadGroupCount = len(threadGroups)
	inheritedTimerMs := make(map[*treeNode]int, len(threadGroups))
	countedAncestorTimers := make(map[*treeNode]struct{})
	countedAncestorConfigs := make(map[*treeNode]struct{})
	for _, threadGroup := range threadGroups {
		ancestorTimers, _ := jmeterAncestorScopeTimers(trees, threadGroup)
		for _, timer := range ancestorTimers {
			if delayMs := parseTimerMs(timer.Element, timer.Element.Name); delayMs > 0 {
				inheritedTimerMs[threadGroup] += delayMs
			}
			if _, counted := countedAncestorTimers[timer]; counted {
				continue
			}
			countedAncestorTimers[timer] = struct{}{}
			result.Stats.Timers++
			if isThroughputTimer(timer.Element.Name) {
				diagnostics.Add(diag.Info, "JMeter "+timer.Element.Name, jmeterElementLocation(timer.Element), "throughput timer was approximated as fixed think time")
			}
		}
	}

	for i, tg := range threadGroups {
		mappingStart := len(sourceMappings)
		groupProtocol, heterogeneous := classifyThreadGroupProtocol(tg)
		requestReports := make([]requestimport.Report, 0)
		var importErr error
		variableScope := newJMeterVariableScope(variableReview)
		for _, set := range planVariableSets {
			variableScope = variableScope.withDefinitions(set.values, set.source, &diagnostics)
		}
		if tg.Element.Name != "TestPlan" {
			threadVariables := parseArguments(tg.Element)
			if len(threadVariables) > 0 {
				variableScope = variableScope.withDefinitions(threadVariables, credentialpolicy.SourceRef{
					Format:  "jmeter",
					Path:    sourceFileName,
					Pointer: jmeterElementLocation(tg.Element),
				}, &diagnostics)
				for name := range threadVariables {
					variableNames[name] = struct{}{}
					result.Stats.Variables++
				}
			}
		}
		ctx := convertContext{
			variableScope:   variableScope,
			provenVariables: make(map[string]struct{}),
			actionNames:     make(map[string]int),
			timerMs:         inheritedTimerMs[tg],
			scriptProtocol:  groupProtocol,
			requestReports:  &requestReports,
			credentialNames: credentialNames,
			importErr:       &importErr,
		}
		threadName := attrOr(tg.Element, "testname", fmt.Sprintf("Thread Group %d", i+1))
		scriptName := opts.ScriptName
		if scriptName != "" && len(threadGroups) > 1 {
			scriptName = fmt.Sprintf("%s_%d", scriptName, i+1)
		}
		if scriptName == "" {
			scriptName = sanitizeName(threadName)
			if len(threadGroups) > 1 {
				scriptName = fmt.Sprintf("%s_%d", scriptName, i+1)
			}
		}
		script := &dsl.Script{
			Name:          scriptName,
			Version:       "1.0.0",
			Protocol:      nativeScriptProtocol(groupProtocol),
			GeneratedBy:   "jmeter-import",
			GeneratedFrom: "jmeter",
			Config: dsl.ScriptConfig{
				HTTP:     dsl.HTTPConfig{DefaultHeaders: map[string]string{}},
				RunLogic: dsl.RunLogicConfig{AutoTransactions: "per_action"},
			},
			Actions: []dsl.Action{},
			Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
				Source:           "jmeter",
				SourceScriptName: threadName,
				Preserved: map[string]any{
					"file_name":          opts.FileName,
					"original_file_name": opts.FileName,
					"imported_at":        importedAt,
				},
			}},
		}
		ancestorConfigs, _ := jmeterAncestorScopeConfigs(trees, tg)
		for _, config := range ancestorConfigs {
			_, counted := countedAncestorConfigs[config]
			applyJMeterScopeConfig(
				config,
				&ctx,
				script,
				opts,
				result,
				variableNames,
				csvNames,
				&diagnostics,
				sourceFileName,
				!counted,
			)
			countedAncestorConfigs[config] = struct{}{}
		}
		if heterogeneous {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: mixed-protocol thread group converted as %s; samplers from other protocols were review-gated. Split them into separate scripts to run natively.", threadName, script.Protocol))
			result.Assumptions = append(result.Assumptions, "Mixed-protocol thread groups are not interleaved in one script; the dominant protocol is converted and other-protocol samplers are review-gated.")
		}

		walkThreadGroup(tg, &ctx, script, opts, result, unsupported, variableNames, csvNames, &diagnostics, sourceFileName, &sourceMappings)
		if importErr != nil {
			return nil, importErr
		}
		if script.Protocol == "http" {
			if script.Config.BaseURL == "" {
				script.Config.BaseURL = opts.BaseURL
			}
			if script.Config.BaseURL == "" {
				script.Config.BaseURL = ctx.effectiveBaseURL
			}
			if script.Config.BaseURL == "" {
				script.Config.BaseURL = detectBaseURL(script.Actions)
			}
			if script.Config.BaseURL == "" && ctx.defaultDomain != "" {
				script.Config.BaseURL = buildBaseURL(ctx.defaultProtocol, ctx.defaultDomain, ctx.defaultPort)
			}
			if resolution := ctx.variableScope.resolve(nil); resolution != nil {
				script.Config.BaseURL, _ = resolution.expandRuntimeValue(script.Config.BaseURL, nil)
			}
			if len(ctx.headers) > 0 {
				for _, header := range ctx.headers {
					setJMeterHeaderSummary(script.Config.HTTP.DefaultHeaders, header.Name, convertVariables(header.Value))
				}
			}
		}
		if len(script.Config.HTTP.DefaultHeaders) == 0 {
			script.Config.HTTP.DefaultHeaders = nil
		}
		finalizeNativeProtocolScript(script, &ctx, &diagnostics, &sourceMappings, sourceFileName, threadName)
		allRequirements = append(allRequirements, ctx.credentialRequirements...)
		attachJMeterDynamicFunctionParameters(script, &diagnostics, &sourceMappings, sourceFileName)
		if len(ctx.csvSources) > 0 {
			script.DataSources = append(script.DataSources, ctx.csvSources...)
		}
		if len(script.Actions) == 0 {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: no executable samplers were converted", threadName))
			continue
		}
		if script.Protocol == "http" {
			applySOAPProtocolHints(script, &result.Warnings, threadName)
			draftReport, draftErr := prepareJMeterHTTPDraft(script, sourceFileName)
			if draftErr != nil {
				return nil, draftErr
			}
			if len(draftReport.Findings) > 0 {
				requestReports = append(requestReports, draftReport)
			}
			configReport, configErr := projectJMeterHTTPConfigCredentials(script, &ctx, sourceFileName)
			if configErr != nil {
				return nil, configErr
			}
			if len(configReport.Requirements) > 0 || len(configReport.Findings) > 0 {
				requestReports = append(requestReports, configReport)
			}
			finalized, finalizeErr := requestimport.FinalizeScript(script, requestReports, requestimport.FinalizeOptions{
				UnresolvedBindings: unresolvedJMeterCredentialBindings(requestReports),
			})
			if finalizeErr != nil {
				return nil, fmt.Errorf("finalize JMeter HTTP script %q: %w", script.Name, finalizeErr)
			}
			script = finalized.Script
			allRequirements = append(allRequirements, finalized.Requirements...)
			allFindings = append(allFindings, finalized.Findings...)
			for _, item := range finalized.Diagnostics {
				diagnostics.Add(item.Severity, item.Element, item.Location, item.Detail)
			}
		}
		rebaseJMeterBatchMappingPaths(sourceMappings[mappingStart:], len(result.Scripts))
		result.Scripts = append(result.Scripts, script)
	}
	if len(result.Scripts) == 1 {
		unbaseJMeterSingleScriptMappingPaths(sourceMappings)
	}

	for name := range unsupported {
		result.UnsupportedElements = append(result.UnsupportedElements, name)
	}
	sort.Strings(result.UnsupportedElements)
	result.Stats.Unsupported = len(result.UnsupportedElements)
	result.Summary.SamplerCount = result.Stats.Samplers
	result.Summary.VariablesDiscovered = sortedKeys(variableNames)
	result.Summary.CSVSourcesDiscovered = sortedKeys(csvNames)
	if gap := result.Stats.TotalSamplers - result.Stats.ConvertedActions - result.Stats.SkippedSamplers; gap > 0 {
		diagnostics.Add(diag.Warning, "JMeter completeness guard", convertutil.FirstNonEmpty(opts.FileName, "JMX plan"), fmt.Sprintf("%d sampler(s) were detected but not converted or explicitly skipped", gap))
	}
	result.Unsupported = diagnostics.Items()
	result.Stats.Mapped, result.Stats.ReviewRequired, result.Stats.AdapterRequired = tallyJMeterCategories(sourceMappings)
	for _, script := range result.Scripts {
		if script.Metadata == nil {
			script.Metadata = &dsl.ScriptMetadata{}
		}
		if script.Metadata.Import == nil {
			script.Metadata.Import = &dsl.ImportMetadata{}
		}
		if script.Metadata.Import.Preserved == nil {
			script.Metadata.Import.Preserved = map[string]any{}
		}
		script.Metadata.Import.Preserved["warning_count"] = len(result.Warnings)
		script.Metadata.Import.Preserved["unsupported_count"] = len(result.UnsupportedElements)
	}
	if len(result.Scripts) > 0 {
		result.Script = result.Scripts[0]
	}
	if opts.ScenarioDraft && len(result.Scripts) > 0 {
		result.ScenarioDraft = buildScenarioDraft(threadGroups, result.Scripts)
	}
	SanitizeDisplayLabels(result.Scripts, result.ScenarioDraft)
	for _, script := range result.Scripts {
		uniquify.DeduplicateActionNames(script)
	}
	reconcileJMeterBatchSourceMappingNames(result.Scripts, sourceMappings)
	result.Findings = importreview.NormalizeFindings(allFindings)
	result.SecretRequirements = publicJMeterCredentialRequirements(allRequirements)
	reviewContent, residualSourceRedacted := redactJMeterResidualSource(string(data))
	reviewContent, variableSourceRedacted := redactJMeterCredentialValues(
		reviewContent,
		allRequirements,
		variableReview,
	)
	if (jmeterPreservedCredentialFinding(allFindings) &&
		len(allRequirements) == 0) ||
		jmeterJDBCCredentialSourceRetained(result.Scripts) {
		// Preserve-mode credentials intentionally remain in the runnable
		// destination, so there is no protected Requirement.Value list to drive
		// the historical value-by-value source scrubber. Omit the public source
		// text instead of risking disclosure through JMeter's sibling
		// Header.name/Header.value XML shape or entity-encoded values. Newlines
		// remain so source mappings keep their original line coordinates.
		reviewContent = "<!-- [JMETER SOURCE OMITTED: IMPORTED CREDENTIALS RETAINED IN DESTINATION] -->" +
			strings.Repeat("\n", strings.Count(reviewContent, "\n"))
		variableSourceRedacted = true
	}
	result.SourceReview = sourceview.Build(map[string]string{sourceFileName: reviewContent}, sourceMappings)
	if residualSourceRedacted || variableSourceRedacted {
		markJMeterSourceReviewRedacted(result.SourceReview, sourceFileName)
	}
	sanitizeJMeterResultReviewText(result)
	return result, nil
}

func rebaseJMeterBatchMappingPaths(mappings []sourceview.Mapping, scriptIndex int) {
	prefix := fmt.Sprintf("scripts[%d].", scriptIndex)
	for index := range mappings {
		path := strings.TrimSpace(mappings[index].TargetPath)
		if path == "" || strings.HasPrefix(path, "scripts[") {
			continue
		}
		mappings[index].TargetPath = prefix + path
	}
}

func unbaseJMeterSingleScriptMappingPaths(mappings []sourceview.Mapping) {
	const prefix = "scripts[0]."
	for index := range mappings {
		if strings.HasPrefix(mappings[index].TargetPath, prefix) {
			mappings[index].TargetPath = strings.TrimPrefix(mappings[index].TargetPath, prefix)
		}
	}
}

func reconcileJMeterBatchSourceMappingNames(
	scripts []*dsl.Script,
	mappings []sourceview.Mapping,
) {
	if len(scripts) == 0 || len(mappings) == 0 {
		return
	}
	namesByPath := make(map[string]string)
	for scriptIndex, script := range scripts {
		if script == nil {
			continue
		}
		prefix := ""
		if len(scripts) > 1 {
			prefix = fmt.Sprintf("scripts[%d].", scriptIndex)
		}
		_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
			if ref.Action != nil {
				namesByPath[prefix+ref.Path] = ref.Action.Name
			}
			return nil
		})
	}
	for index := range mappings {
		if name := namesByPath[strings.TrimSpace(mappings[index].TargetPath)]; name != "" {
			mappings[index].TargetName = name
		}
	}
}

var jmeterDynamicParameterPattern = regexp.MustCompile(`\{\{param\.(jmeter_(?:(?:global_)?counter(?:_[A-Za-z0-9_]+)?|thread_num))\}\}`)

func attachJMeterDynamicFunctionParameters(script *dsl.Script, diagnostics *diag.Collector, sourceMappings *[]sourceview.Mapping, sourceFileName string) {
	if script == nil {
		return
	}
	encoded, err := json.Marshal(script)
	if err != nil {
		return
	}
	existing := make(map[string]struct{}, len(script.Parameters))
	for _, parameter := range script.Parameters {
		existing[parameter.Name] = struct{}{}
	}
	names := make(map[string]struct{})
	for _, match := range jmeterDynamicParameterPattern.FindAllSubmatch(encoded, -1) {
		name := string(match[1])
		if _, exists := existing[name]; !exists {
			names[name] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(names))
	globalCounter := false
	for name := range names {
		ordered = append(ordered, name)
		globalCounter = globalCounter || strings.HasPrefix(name, "jmeter_global_counter")
	}
	sort.Strings(ordered)
	for _, name := range ordered {
		if name == "jmeter_thread_num" {
			script.Parameters = append(script.Parameters, dsl.Parameter{Name: name, Type: "vuser_id", Update: "once"})
			continue
		}
		script.Parameters = append(script.Parameters, dsl.Parameter{
			Name: name, Type: "counter", Update: "each_occurrence", Start: 1, Step: 1,
		})
	}
	if !globalCounter || hasJMeterReviewReason(script.Actions, "unsupported_jmeter_global_counter") {
		return
	}
	setJMeterRuntimeParity(script, "dynamicFunctions.globalCounter", "review_required")
	location := convertutil.FirstNonEmpty(sourceFileName, "JMX plan")
	actionIndex := appendJMeterSourceReviewPlaceholder(script, "cross-thread __counter", location, sourceFileName, "unsupported_jmeter_global_counter")
	if diagnostics != nil {
		diagnostics.Add(diag.Blocker, "JMeter __counter", location, "cross-thread __counter(FALSE, ...) cannot be reproduced by the per-VUser counter runtime; replace the preserved counter before replay")
	}
	if sourceMappings != nil {
		*sourceMappings = append(*sourceMappings, jmeterSourceMapping(
			sourceFileName,
			nil,
			"__counter(FALSE, ...)",
			fmt.Sprintf("actions[%d]", actionIndex),
			"Cross-thread counter review gate",
			"dynamic_function",
			"review_only",
			"JMeter cross-thread counter scope has no equivalent in the per-VUser parameter runtime.",
		))
	}
}

func jmeterPreservedCredentialFinding(findings []importreview.Finding) bool {
	for _, finding := range findings {
		if strings.TrimSpace(finding.Code) == "import_credential_preserved" {
			return true
		}
	}
	return false
}

func jmeterJDBCCredentialSourceRetained(scripts []*dsl.Script) bool {
	for _, script := range scripts {
		if script == nil || script.Metadata == nil ||
			script.Metadata.Import == nil {
			continue
		}
		sources, _ := script.Metadata.Import.Preserved[dsl.ImportPreservedJMeterJDBCSourcesKey].([]map[string]string)
		for _, source := range sources {
			if source["password"] != "" ||
				hasJDBCInlineCredentials(source["db_url"]) {
				return true
			}
			for _, property := range parseJMeterJDBCConnectionProperties(
				source["connection_properties"],
			) {
				if jmeterJDBCSensitivePropertyName(property.Name) &&
					property.Value != "" {
					return true
				}
			}
		}
	}
	return false
}

func walkThreadGroup(node *treeNode, ctx *convertContext, script *dsl.Script, opts ConvertOptions, result *ConvertResult, unsupported map[string]struct{}, variableNames map[string]struct{}, csvNames map[string]struct{}, diagnostics *diag.Collector, sourceFileName string, sourceMappings *[]sourceview.Mapping) {
	if ctx != nil && ctx.importErr != nil && *ctx.importErr != nil {
		return
	}
	// JMeter timers are configuration elements: every enabled timer directly in
	// this tree scope applies to every sampler in the scope, independent of where
	// the timer appears relative to the sampler. Add the scope total up front and
	// remove it on return so nested scopes inherit it without leaking it to later
	// siblings. Flow Control/TestAction pauses remain positional one-shot state.
	scopeTimerMs := jmeterDirectScopeTimerMs(node)
	ctx.timerMs += scopeTimerMs
	defer func() {
		ctx.timerMs -= scopeTimerMs
	}()
	// JMeter configuration elements apply to their entire tree scope regardless
	// of whether they appear before or after a sampler in the serialized JMX.
	// Preload them once before walking executable children. Ancestor scope is
	// bootstrapped separately when conversion starts directly at a selected
	// ThreadGroup.
	for _, config := range jmeterDirectScopeConfigs(node) {
		applyJMeterScopeConfig(
			config,
			ctx,
			script,
			opts,
			result,
			variableNames,
			csvNames,
			diagnostics,
			sourceFileName,
			true,
		)
	}
	for _, child := range node.Children {
		if child == nil || child.Element == nil {
			continue
		}
		if !jmeterElementEnabled(child.Element) {
			appendJMeterDisabledMapping(sourceMappings, sourceFileName, child.Element)
			continue
		}
		if isJMeterDirectScopeConfig(child.Element.Name) {
			continue
		}
		switch child.Element.Name {
		case "TestAction":
			if pauseMs := parseTestActionPauseMs(child); pauseMs > 0 {
				ctx.oneShotTimerMs += pauseMs
				result.Stats.Timers++
				setJMeterRuntimeParity(script, "testActionPause", "executed")
				diagnostics.Add(diag.Info, "JMeter TestAction", jmeterElementLocation(child.Element), "pause action was mapped to think time on the next converted sampler")
			}
			continue
		case "ConstantTimer", "UniformRandomTimer", "GaussianRandomTimer", "PoissonRandomTimer", "ConstantThroughputTimer", "PreciseThroughputTimer":
			if isThroughputTimer(child.Element.Name) {
				diagnostics.Add(diag.Info, "JMeter "+child.Element.Name, jmeterElementLocation(child.Element), "throughput timer was approximated as fixed think time")
			}
			result.Stats.Timers++
		case "HTTPSamplerProxy":
			// A HeaderManager nested under a sampler's own hashTree applies to
			// that sampler alone in JMeter. Sampler children are not walked (see
			// the recursion guard at the end of this loop), and HeaderManager is
			// in the known-supported set, so it produced no diagnostic either:
			// the headers were silently dropped and the imported request went out
			// without them, auth headers included.
			samplerCtx := ctx
			scopedHeaders, scopedTimerMs, scopedTimerCount, scopedAssertions := jmeterSamplerScope(child, diagnostics)
			if len(scopedHeaders) > 0 || scopedTimerCount > 0 || ctx.oneShotTimerMs > 0 || len(scopedAssertions) > 0 {
				next := *ctx
				next.headers = overlayJMeterHeaders(ctx.headers, scopedHeaders)
				next.timerMs = ctx.timerMs + ctx.oneShotTimerMs + scopedTimerMs
				next.oneShotTimerMs = 0
				next.assertions = append(append([]dsl.Assertion(nil), ctx.assertions...), scopedAssertions...)
				samplerCtx = &next
				result.Stats.Headers += len(scopedHeaders)
				result.Stats.Assertions += len(scopedAssertions)
				result.Stats.Timers += scopedTimerCount
			}
			action, warnings, convertErr := convertHTTPSampler(child, samplerCtx, opts, diagnostics)
			if convertErr != nil {
				if ctx.importErr != nil {
					*ctx.importErr = convertErr
				}
				return
			}
			result.Warnings = append(result.Warnings, warnings...)
			result.Stats.Samplers++
			if action != nil {
				actionIndex := len(script.Actions)
				script.Actions = append(script.Actions, *action)
				appendJMeterActionSourceMappings(sourceMappings, sourceFileName, child, actionIndex, action)
				result.Stats.ConvertedActions++
			}
			ctx.oneShotTimerMs = 0
			diagnoseJMeterChildren(child.Children, diagnostics, unsupported)
		case "ThreadGroup", "TestPlan":
			walkThreadGroup(child, ctx, script, opts, result, unsupported, variableNames, csvNames, diagnostics, sourceFileName, sourceMappings)
			continue
		case "TransactionController":
			next := *ctx
			next.csvSources = append([]dsl.DataSource(nil), ctx.csvSources...)
			txName := attrOr(child.Element, "testname", "Transaction")
			if ctx.transaction != "" {
				txName = ctx.transaction + " / " + txName
			}
			next.transaction = txName
			walkThreadGroup(child, &next, script, opts, result, unsupported, variableNames, csvNames, diagnostics, sourceFileName, sourceMappings)
			// TestAction is positional rather than controller-scoped. If the first
			// sampler lived inside this controller, propagate its consumption so the
			// pause is not replayed again by the next sibling sampler.
			ctx.oneShotTimerMs = next.oneShotTimerMs
			var csvConflicts []jmeterCSVSourceConflict
			ctx.csvSources, csvConflicts = mergeJMeterCSVSources(ctx.csvSources, next.csvSources)
			applyJMeterCSVSourceConflicts(script, result, diagnostics, sourceFileName, jmeterElementLocation(child.Element), csvConflicts)
			continue
		case "OnceOnlyController":
			steps, childMappings := collectJMeterChildActions(child, ctx, script, opts, result, unsupported, variableNames, csvNames, diagnostics, sourceFileName)
			if len(steps) == 0 {
				diagnostics.Add(diag.Info, "JMeter OnceOnlyController", jmeterElementLocation(child.Element), "OnceOnlyController contained no converted child samplers")
				continue
			}
			start := len(script.Init)
			script.Init = append(script.Init, steps...)
			*sourceMappings = append(*sourceMappings, rebaseJMeterChildMappings(childMappings, func(index int) string {
				return fmt.Sprintf("init[%d]", start+index)
			})...)
			setJMeterRuntimeParity(script, "onceOnlyController", "executed")
			targetPath := fmt.Sprintf("init[%d]", start)
			if len(steps) > 1 {
				targetPath = fmt.Sprintf("init[%d:%d]", start, len(script.Init)-1)
			}
			*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, child.Element, child.Element.Name, targetPath, jmeterElementLocation(child.Element), "controller", "converted", fmt.Sprintf("JMeter OnceOnlyController was mapped to %d init action(s).", len(steps))))
			continue
		case "LoopController":
			loopCountStr := prop(child.Element, "LoopController.loops")
			if loopCountStr == "" {
				loopCountStr = attrOr(child.Element, "loops", "1")
			}
			if val, err := strconv.Atoi(loopCountStr); err == nil && val > 0 {
				steps, childMappings := collectJMeterChildActions(child, ctx, script, opts, result, unsupported, variableNames, csvNames, diagnostics, sourceFileName)
				actionIndex := len(script.Actions)
				script.Actions = append(script.Actions, dsl.Action{
					Name: strings.TrimSpace(convertutil.FirstNonEmpty(attrOr(child.Element, "testname", ""), "Loop")),
					Type: "control_loop",
					Control: &dsl.ControlFlow{
						Count: val,
						Steps: steps,
					},
					Metadata: map[string]any{
						"source":      "jmeter",
						"source_type": "LoopController",
					},
				})
				*sourceMappings = append(*sourceMappings, rebaseJMeterChildMappings(childMappings, func(index int) string {
					return fmt.Sprintf("actions[%d].control.steps[%d]", actionIndex, index)
				})...)
				*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, child.Element, child.Element.Name, fmt.Sprintf("actions[%d]", actionIndex), jmeterElementLocation(child.Element), "controller", "converted", fmt.Sprintf("JMeter LoopController was mapped to control_loop with count %d.", val)))
			} else if loopCountStr == "-1" || loopCountStr == "0" {
				diagnostics.Add(diag.Warning, "JMeter LoopController", jmeterElementLocation(child.Element), "infinite loops (-1) remain review-gated; children were imported once for review")
				actionIndex := appendJMeterSourceReviewPlaceholder(script, child.Element.Name, jmeterElementLocation(child.Element), sourceFileName, "flattened_infinite_loop")
				*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, child.Element, child.Element.Name, fmt.Sprintf("actions[%d]", actionIndex), jmeterElementLocation(child.Element), "controller", "review_only", "JMeter LoopController infinite loop was flattened as one review iteration."))
				if len(child.Children) > 0 {
					walkThreadGroup(child, ctx, script, opts, result, unsupported, variableNames, csvNames, diagnostics, sourceFileName, sourceMappings)
				}
			} else {
				diagnostics.Add(diag.Warning, "JMeter LoopController", jmeterElementLocation(child.Element), "dynamic loop count cannot be statically unrolled; children were imported once into a blocked draft")
				actionIndex := appendJMeterSourceReviewPlaceholder(script, child.Element.Name, jmeterElementLocation(child.Element), sourceFileName, "flattened_dynamic_loop")
				*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, child.Element, child.Element.Name, fmt.Sprintf("actions[%d]", actionIndex), jmeterElementLocation(child.Element), "controller", "review_only", "JMeter LoopController dynamic count was omitted and its children were flattened as one review iteration."))
				if len(child.Children) > 0 {
					walkThreadGroup(child, ctx, script, opts, result, unsupported, variableNames, csvNames, diagnostics, sourceFileName, sourceMappings)
				}
			}
			continue
		case "IfController":
			condition := prop(child.Element, "IfController.condition")
			if cond, ok := simpleJMeterCondition(condition); ok {
				steps, childMappings := collectJMeterChildActions(child, ctx, script, opts, result, unsupported, variableNames, csvNames, diagnostics, sourceFileName)
				actionIndex := len(script.Actions)
				script.Actions = append(script.Actions, dsl.Action{
					Name:    strings.TrimSpace(convertutil.FirstNonEmpty(attrOr(child.Element, "testname", ""), "If")),
					Type:    "control_if",
					Control: &dsl.ControlFlow{Condition: &cond, Steps: steps},
					Metadata: map[string]any{
						"source":             "jmeter",
						"source_type":        "IfController",
						"source_condition":   condition,
						"condition_original": condition,
					},
				})
				*sourceMappings = append(*sourceMappings, rebaseJMeterChildMappings(childMappings, func(index int) string {
					return fmt.Sprintf("actions[%d].control.steps[%d]", actionIndex, index)
				})...)
				*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, child.Element, child.Element.Name, fmt.Sprintf("actions[%d]", actionIndex), jmeterElementLocation(child.Element), "controller", "converted", "JMeter IfController was mapped to control_if."))
			} else {
				diagnostics.Add(diag.Warning, "JMeter IfController", jmeterElementLocation(child.Element), "conditional execution could not be translated safely; the condition was omitted and children were flattened into a blocked draft")
				actionIndex := appendJMeterSourceReviewPlaceholder(script, child.Element.Name, jmeterElementLocation(child.Element), sourceFileName, "flattened_dynamic_condition")
				*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, child.Element, child.Element.Name, fmt.Sprintf("actions[%d]", actionIndex), jmeterElementLocation(child.Element), "controller", "review_only", "JMeter IfController condition was omitted and its children were flattened for review."))
				if len(child.Children) > 0 {
					walkThreadGroup(child, ctx, script, opts, result, unsupported, variableNames, csvNames, diagnostics, sourceFileName, sourceMappings)
				}
			}
			continue
		case "WhileController":
			condition := prop(child.Element, "WhileController.condition")
			if cond, ok := simpleJMeterCondition(condition); ok {
				steps, childMappings := collectJMeterChildActions(child, ctx, script, opts, result, unsupported, variableNames, csvNames, diagnostics, sourceFileName)
				actionIndex := len(script.Actions)
				script.Actions = append(script.Actions, dsl.Action{
					Name: strings.TrimSpace(convertutil.FirstNonEmpty(attrOr(child.Element, "testname", ""), "While")),
					Type: "control_loop",
					Control: &dsl.ControlFlow{
						Condition: &cond,
						Steps:     steps,
						// JMeter while loops have no intrinsic bound; cap the
						// converted loop so a stale condition cannot hang VUsers.
						MaxIterations: jmeterWhileLoopSafetyCap,
					},
					Metadata: map[string]any{
						"source":             "jmeter",
						"source_type":        "WhileController",
						"source_condition":   condition,
						"condition_original": condition,
					},
				})
				*sourceMappings = append(*sourceMappings, rebaseJMeterChildMappings(childMappings, func(index int) string {
					return fmt.Sprintf("actions[%d].control.steps[%d]", actionIndex, index)
				})...)
				diagnostics.Add(diag.Info, "JMeter WhileController", jmeterElementLocation(child.Element), fmt.Sprintf("while loop was mapped to a condition-driven control_loop with a %d-iteration safety cap", jmeterWhileLoopSafetyCap))
				*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, child.Element, child.Element.Name, fmt.Sprintf("actions[%d]", actionIndex), jmeterElementLocation(child.Element), "controller", "converted", "JMeter WhileController was mapped to a condition-driven control_loop."))
				continue
			}
			diagnostics.Add(diag.Warning, "JMeter WhileController", jmeterElementLocation(child.Element), "while condition could not be translated safely; the condition was omitted and children were flattened into a blocked draft")
			actionIndex := appendJMeterSourceReviewPlaceholder(script, child.Element.Name, jmeterElementLocation(child.Element), sourceFileName, "flattened_dynamic_condition")
			*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, child.Element, child.Element.Name, fmt.Sprintf("actions[%d]", actionIndex), jmeterElementLocation(child.Element), "controller", "review_only", "JMeter WhileController condition was omitted and its children were flattened for review."))
		case "ForeachController", "ForEachController", "InterleaveControl", "RandomController", "ThroughputController":
			diagnostics.Add(diag.Warning, "JMeter "+child.Element.Name, jmeterElementLocation(child.Element), "controller logic was flattened; child requests are imported in source order without original conditions or transaction semantics")
			actionIndex := appendJMeterSourceReviewPlaceholder(script, child.Element.Name, jmeterElementLocation(child.Element), sourceFileName, "flattened_controller_behavior")
			*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, child.Element, child.Element.Name, fmt.Sprintf("actions[%d]", actionIndex), jmeterElementLocation(child.Element), "controller", "review_only", "JMeter controller logic was omitted; child requests are imported in source order for review."))
		case "DebugSampler":
			diagnostics.Add(diag.Info, "JMeter DebugSampler", jmeterElementLocation(child.Element), "DebugSampler was preserved as diagnostic metadata and does not block executable HTTP import")
			*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, child.Element, child.Element.Name, "", jmeterElementLocation(child.Element), "diagnostic", "ignored", "JMeter DebugSampler does not generate an executable APEX Load action."))
		case "JSR223Sampler", "BeanShellSampler", "BSFSampler":
			handleJMeterScriptSampler(child, script, result, diagnostics, sourceFileName, sourceMappings)
			continue
		case "JDBCSampler":
			actionStart := len(script.Actions)
			handleJMeterJDBCSampler(child, ctx, script, result, diagnostics, sourceFileName, sourceMappings)
			applyJMeterNativeSamplerTiming(child, appendedJMeterSamplerAction(script, actionStart), ctx, result, diagnostics)
			continue
		case "FTPSampler", "TCPSampler":
			actionStart := len(script.Actions)
			handleJMeterNetworkSampler(child, ctx, script, result, diagnostics, sourceFileName, sourceMappings)
			applyJMeterNativeSamplerTiming(child, appendedJMeterSamplerAction(script, actionStart), ctx, result, diagnostics)
			continue
		case "JMSSampler", "JMSPublisher", "JMSSubscriber", "PublisherSampler", "SubscriberSampler":
			handleJMeterJMSSampler(child, script, result, diagnostics, sourceFileName, sourceMappings)
			// JMS remains review-only until a provider is selected, but it is still
			// the positional target of a preceding TestAction. Consume that pause so
			// it cannot leak to a later executable sampler.
			applyJMeterNativeSamplerTiming(child, nil, ctx, result, diagnostics)
			continue
		case "hashTree":
		default:
			if name := strings.TrimSpace(child.Element.Name); name != "" {
				if isJMeterListenerElement(name) {
					diagnostics.Add(diag.Info, "JMeter "+name, jmeterElementLocation(child.Element), "listener is reporting-only and does not affect replay; preserved as metadata")
					*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, child.Element, child.Element.Name, "", jmeterElementLocation(child.Element), "diagnostic", "ignored", "JMeter listener is reporting-only and was preserved as metadata."))
				} else {
					unsupported[child.Element.Name] = struct{}{}
					diagnoseJMeterElement(child.Element, diagnostics)
					*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, child.Element, child.Element.Name, "", jmeterElementLocation(child.Element), "unsupported", "review_only", "JMeter element was not converted into executable APEX Load DSL."))
				}
			}
		}
		if len(child.Children) > 0 && child.Element.Name != "HTTPSamplerProxy" {
			walkThreadGroup(child, ctx, script, opts, result, unsupported, variableNames, csvNames, diagnostics, sourceFileName, sourceMappings)
		}
	}
}

func applyJMeterScopeConfig(
	config *treeNode,
	ctx *convertContext,
	script *dsl.Script,
	opts ConvertOptions,
	result *ConvertResult,
	variableNames map[string]struct{},
	csvNames map[string]struct{},
	diagnostics *diag.Collector,
	sourceFileName string,
	countStats bool,
) bool {
	if config == nil || config.Element == nil || ctx == nil || script == nil || result == nil {
		return false
	}
	element := config.Element
	switch element.Name {
	case "ConfigTestElement":
		applyHTTPDefaults(element, ctx)
	case "HeaderManager":
		headers := parseHeaders(element)
		ctx.headers = overlayJMeterHeaders(ctx.headers, headers)
		if countStats {
			result.Stats.Headers += len(headers)
		}
	case "CookieManager":
		enabled := true
		script.Config.HTTP.CookiesEnabled = &enabled
		setJMeterRuntimeParity(script, "cookieManager", "executed")
	case "CacheManager":
		applyJMeterCacheManager(element, script)
	case "AuthManager":
		auths := parseAuthManager(element, diagnostics)
		resolution := ctx.variableScope.resolve(diagnostics)
		provenVariables := jmeterProvenVariables(ctx)
		for index := range auths {
			auths[index].URL, _ = resolution.expandRuntimeValue(auths[index].URL, provenVariables)
		}
		ctx.auths = append(ctx.auths, auths...)
		for _, auth := range auths {
			if auth.URL == "" && !ctx.defaultAuthSeen {
				ctx.defaultAuthSeen = true
				if auth.Config.Type != "" &&
					authCapabilityFromJMeterMechanism(auth.Mechanism).Support == httpprotocol.AuthExecutable {
					script.Config.HTTP.Auth = auth.Config
				}
			}
		}
	case "Arguments":
		vars := parseArguments(element)
		ctx.variableScope = ctx.variableScope.withDefinitions(vars, credentialpolicy.SourceRef{
			Format:  "jmeter",
			Path:    sourceFileName,
			Pointer: jmeterElementLocation(element),
		}, diagnostics)
		for name := range vars {
			variableNames[name] = struct{}{}
			if countStats {
				result.Stats.Variables++
			}
		}
	case "CSVDataSet":
		source := parseCSVDataSet(element, opts.SidecarFiles)
		if source.Name != "" {
			ctx.csvSources = append(ctx.csvSources, source)
			csvNames[source.Name] = struct{}{}
			if countStats {
				result.Stats.CSVSources++
			}
		}
		if countStats {
			if shareMode := strings.TrimSpace(prop(element, "shareMode")); shareMode != "" &&
				!strings.EqualFold(shareMode, "shareMode.all") && !strings.EqualFold(shareMode, "all") {
				diagnostics.Add(diag.Warning, "JMeter CSVDataSet", jmeterElementLocation(element), "CSV shareMode has no equivalent and requires review")
			}
		}
		// stopThread only affects what happens after a finite CSV reaches EOF. With
		// recycling enabled EOF is never reached, so stopThread=true alone is not a
		// semantic gap and must not gate an otherwise runnable plan.
		if strings.EqualFold(strings.TrimSpace(prop(element, "recycle")), "false") {
			setJMeterRuntimeParity(script, "csvDataSet.eof", "review_required")
			diagnostics.Add(diag.Blocker, "JMeter CSVDataSet", jmeterElementLocation(element), "recycle=false EOF behavior (including the configured stopThread disposition) has no exact APEX data-source equivalent; execution is review-gated")
			if !hasJMeterReviewReason(script.Actions, "unsupported_csv_eof_semantics") {
				appendJMeterSourceReviewPlaceholder(script, "CSVDataSet EOF behavior", jmeterElementLocation(element), sourceFileName, "unsupported_csv_eof_semantics")
			}
		}
	case "ResponseAssertion":
		assertions := parseResponseAssertions(element, diagnostics)
		ctx.assertions = append(ctx.assertions, assertions...)
		if countStats {
			result.Stats.Assertions += len(assertions)
		}
	case "JDBCDataSource":
		if cfg, credentialRetained, ok := parseJMeterJDBCConnection(element); ok {
			credentialUnavailable, projectionErr := projectJMeterJDBCUnavailableCredential(
				cfg,
				element,
				ctx,
				sourceFileName,
				script.Name,
			)
			if projectionErr != nil {
				if ctx.importErr != nil && *ctx.importErr == nil {
					*ctx.importErr = projectionErr
				}
				return true
			}
			ctx.databaseConfig = cfg
			// Keep the complete authored JDBC source record even when every
			// property could be projected into the native DSN. Some JDBC driver
			// properties have no portable database/sql equivalent; retaining the
			// exact source lets an operator repair the already-gated adapter without
			// re-importing or re-entering a password.
			preserveJMeterJDBCSource(script, element)
			if credentialUnavailable {
				diagnostics.Add(diag.Blocker, "JMeter JDBCDataSource", jmeterElementLocation(element), "the JDBC source contains a disclosure placeholder instead of a recoverable credential; configure the generated database connection-string secret before replay")
			} else if credentialRetained {
				diagnostics.Add(diag.Warning, "JMeter JDBCDataSource", jmeterElementLocation(element), "database credentials were retained in the durable destination and will be masked on ordinary reads; review the imported connection before replay")
			}
		}
	default:
		return false
	}
	return true
}

func isJMeterDirectScopeConfig(name string) bool {
	switch name {
	case "ConfigTestElement", "HeaderManager", "CookieManager", "CacheManager", "AuthManager", "Arguments", "CSVDataSet", "ResponseAssertion", "JDBCDataSource":
		return true
	default:
		return false
	}
}

func jmeterDirectScopeConfigs(node *treeNode) []*treeNode {
	if node == nil {
		return nil
	}
	configs := make([]*treeNode, 0)
	for _, child := range node.Children {
		if child == nil || child.Element == nil || !jmeterElementEnabled(child.Element) ||
			!isJMeterDirectScopeConfig(child.Element.Name) {
			continue
		}
		configs = append(configs, child)
	}
	return configs
}

func jmeterAncestorScopeConfigs(nodes []*treeNode, target *treeNode) ([]*treeNode, bool) {
	for _, node := range nodes {
		if node == nil || node.Element == nil || !jmeterElementEnabled(node.Element) {
			continue
		}
		if node == target {
			return nil, true
		}
		childConfigs, found := jmeterAncestorScopeConfigs(node.Children, target)
		if !found {
			continue
		}
		return append(jmeterDirectScopeConfigs(node), childConfigs...), true
	}
	return nil, false
}

func jmeterDirectScopeTimerMs(node *treeNode) int {
	if node == nil {
		return 0
	}
	total := 0
	for _, child := range node.Children {
		if child == nil || child.Element == nil || !jmeterElementEnabled(child.Element) {
			continue
		}
		switch child.Element.Name {
		case "ConstantTimer", "UniformRandomTimer", "GaussianRandomTimer", "PoissonRandomTimer", "ConstantThroughputTimer", "PreciseThroughputTimer":
			if delayMs := parseTimerMs(child.Element, child.Element.Name); delayMs > 0 {
				total += delayMs
			}
		}
	}
	return total
}

func jmeterAncestorScopeTimers(nodes []*treeNode, target *treeNode) ([]*treeNode, bool) {
	for _, node := range nodes {
		if node == nil || node.Element == nil || !jmeterElementEnabled(node.Element) {
			continue
		}
		if node == target {
			return nil, true
		}
		childTimers, found := jmeterAncestorScopeTimers(node.Children, target)
		if !found {
			continue
		}
		return append(jmeterDirectScopeTimers(node), childTimers...), true
	}
	return nil, false
}

func jmeterDirectScopeTimers(node *treeNode) []*treeNode {
	if node == nil {
		return nil
	}
	var timers []*treeNode
	for _, child := range node.Children {
		if child == nil || child.Element == nil || !jmeterElementEnabled(child.Element) {
			continue
		}
		switch child.Element.Name {
		case "ConstantTimer", "UniformRandomTimer", "GaussianRandomTimer", "PoissonRandomTimer", "ConstantThroughputTimer", "PreciseThroughputTimer":
			timers = append(timers, child)
		}
	}
	return timers
}

func collectJMeterChildActions(node *treeNode, ctx *convertContext, parentScript *dsl.Script, opts ConvertOptions, result *ConvertResult, unsupported map[string]struct{}, variableNames map[string]struct{}, csvNames map[string]struct{}, diagnostics *diag.Collector, sourceFileName string) ([]dsl.Action, []sourceview.Mapping) {
	if node == nil || len(node.Children) == 0 {
		return nil, nil
	}
	next := *ctx
	next.csvSources = append([]dsl.DataSource(nil), ctx.csvSources...)
	next.provenVariables = jmeterProvenVariables(ctx)
	tempScript := &dsl.Script{Protocol: "http", Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{DefaultHeaders: map[string]string{}}}}
	var tempMappings []sourceview.Mapping
	walkThreadGroup(node, &next, tempScript, opts, result, unsupported, variableNames, csvNames, diagnostics, sourceFileName, &tempMappings)
	ctx.oneShotTimerMs = next.oneShotTimerMs
	var csvConflicts []jmeterCSVSourceConflict
	ctx.csvSources, csvConflicts = mergeJMeterCSVSources(ctx.csvSources, next.csvSources)
	applyJMeterCSVSourceConflicts(parentScript, result, diagnostics, sourceFileName, jmeterElementLocation(node.Element), csvConflicts)
	mergeJMeterRuntimeParity(parentScript, tempScript)
	if len(tempScript.Init) > 0 {
		gateIndex := len(parentScript.Init)
		parentScript.Init = append(parentScript.Init, jmeterNestedOnceReviewGate(node.Element, sourceFileName))
		result.Stats.ReviewRequired++
		start := len(parentScript.Init)
		parentScript.Init = append(parentScript.Init, tempScript.Init...)
		tempMappings = rebaseJMeterInitMappings(tempMappings, func(index int) string {
			return fmt.Sprintf("init[%d]", start+index)
		})
		tempMappings = append(tempMappings, jmeterSourceMapping(
			sourceFileName,
			node.Element,
			"Nested OnceOnlyController",
			fmt.Sprintf("init[%d]", gateIndex),
			jmeterElementLocation(node.Element),
			"controller",
			"review_only",
			"Nested OnceOnlyController actions were hoisted to init behind a durable execution gate because ordering inside repeated control flow cannot be reproduced exactly.",
		))
		setJMeterRuntimeParity(parentScript, "onceOnlyController", "partial")
		diagnostics.Add(diag.Warning, "JMeter nested OnceOnlyController", jmeterElementLocation(node.Element), "nested OnceOnlyController children were preserved in script init because the DSL has no once-per-VUser controller nested inside repeated control flow; review ordering relative to sibling steps")
	}
	return append([]dsl.Action(nil), tempScript.Actions...), tempMappings
}

func jmeterNestedOnceReviewGate(element *xmlNode, sourceFileName string) dsl.Action {
	location := jmeterElementLocation(element)
	action := dsl.Action{
		Name:  "Review nested OnceOnlyController ordering",
		Type:  "review_required",
		Value: "Review the hoisted once-only init actions and their ordering, then remove this gate before replay.",
		Metadata: map[string]any{
			"source":      "jmeter",
			"source_type": "OnceOnlyController",
		},
	}
	importreview.ApplyToAction(&action, []importreview.Finding{{
		Code:       "import_review_required",
		Severity:   diag.Blocker,
		Element:    "JMeter nested OnceOnlyController",
		Detail:     "nested once-only actions were preserved in script init, but their ordering relative to repeated sibling actions cannot be reproduced exactly",
		Location:   location,
		Source:     "jmeter",
		ReasonCode: "nested_once_only_ordering",
		SourceRef:  sourceFileName + "#" + location,
		Resolution: "verify_hoisted_once_only_ordering_and_remove_gate",
		Gate:       importreview.GateExecution,
	}})
	return action
}

type jmeterCSVSourceConflict struct {
	OriginalName string
	RenamedName  string
}

func mergeJMeterCSVSources(destination, additional []dsl.DataSource) ([]dsl.DataSource, []jmeterCSVSourceConflict) {
	out := append([]dsl.DataSource(nil), destination...)
	var conflicts []jmeterCSVSourceConflict
	for _, source := range additional {
		if jmeterCSVSourceExists(out, source) {
			continue
		}
		if !jmeterCSVSourceNameExists(out, source.Name) {
			out = append(out, source)
			continue
		}
		// A nested scope can legally shadow a CSVDataSet with a differently
		// configured source of the same test name. The flat APEX data-source
		// model cannot express that scope. Preserve both records under stable,
		// unique names and review-gate execution instead of silently collapsing
		// the nested source into the outer one.
		originalName := source.Name
		for suffix := 2; ; suffix++ {
			candidate := fmt.Sprintf("%s_scoped_%d", originalName, suffix)
			source.Name = candidate
			if jmeterCSVSourceExists(out, source) {
				break
			}
			if jmeterCSVSourceNameExists(out, candidate) {
				continue
			}
			out = append(out, source)
			conflicts = append(conflicts, jmeterCSVSourceConflict{OriginalName: originalName, RenamedName: candidate})
			break
		}
	}
	return out, conflicts
}

func jmeterCSVSourceNameExists(sources []dsl.DataSource, name string) bool {
	for _, existing := range sources {
		if existing.Name == name {
			return true
		}
	}
	return false
}

func jmeterCSVSourceExists(sources []dsl.DataSource, candidate dsl.DataSource) bool {
	for _, existing := range sources {
		if existing.Name != candidate.Name {
			continue
		}
		if existing.Type == candidate.Type &&
			existing.Path == candidate.Path &&
			existing.InlineData == candidate.InlineData &&
			existing.Encoding == candidate.Encoding &&
			existing.FileName == candidate.FileName &&
			existing.ContentType == candidate.ContentType &&
			existing.StorageRef == candidate.StorageRef &&
			existing.StagedAsset == candidate.StagedAsset &&
			existing.Strategy == candidate.Strategy &&
			existing.Delimiter == candidate.Delimiter &&
			existing.FirstRowHeader == candidate.FirstRowHeader &&
			slices.Equal(existing.Columns, candidate.Columns) {
			return true
		}
	}
	return false
}

func applyJMeterCSVSourceConflicts(script *dsl.Script, result *ConvertResult, diagnostics *diag.Collector, sourceFileName, location string, conflicts []jmeterCSVSourceConflict) {
	if script == nil || len(conflicts) == 0 {
		return
	}
	setJMeterRuntimeParity(script, "csvDataSet.scope", "review_required")
	if hasJMeterReviewReason(script.Actions, "conflicting_csv_data_source_scope") {
		return
	}
	pairs := make([]string, 0, len(conflicts))
	for _, conflict := range conflicts {
		pairs = append(pairs, fmt.Sprintf("%q as %q", conflict.OriginalName, conflict.RenamedName))
	}
	diagnostics.Add(diag.Blocker, "JMeter CSVDataSet scoped name conflict", location, "nested CSVDataSet definitions with the same name have different runtime semantics; preserved "+strings.Join(pairs, ", ")+" but the flat data-source model cannot reproduce JMeter scope")
	appendJMeterSourceReviewPlaceholder(script, "CSVDataSet scoped name conflict", location, sourceFileName, "conflicting_csv_data_source_scope")
	if result != nil {
		result.Stats.ReviewRequired++
	}
}

func rebaseJMeterChildMappings(mappings []sourceview.Mapping, target func(int) string) []sourceview.Mapping {
	return rebaseJMeterSectionMappings(mappings, "actions[", target)
}

func rebaseJMeterInitMappings(mappings []sourceview.Mapping, target func(int) string) []sourceview.Mapping {
	return rebaseJMeterSectionMappings(mappings, "init[", target)
}

func rebaseJMeterSectionMappings(mappings []sourceview.Mapping, prefix string, target func(int) string) []sourceview.Mapping {
	rebased := append([]sourceview.Mapping(nil), mappings...)
	for index := range rebased {
		path := rebased[index].TargetPath
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		end := strings.IndexByte(path, ']')
		if end < len(prefix) {
			continue
		}
		actionIndex, err := strconv.Atoi(path[len(prefix):end])
		if err != nil {
			continue
		}
		rebased[index].TargetPath = target(actionIndex) + path[end+1:]
	}
	return rebased
}

func hasJMeterReviewReason(actions []dsl.Action, reason string) bool {
	for _, action := range actions {
		if action.Metadata == nil {
			continue
		}
		if review, ok := action.Metadata["import_review"].(map[string]any); ok {
			if review["reason_code"] == reason {
				return true
			}
			switch reasons := review["reason_codes"].(type) {
			case []string:
				if slices.Contains(reasons, reason) {
					return true
				}
			case []any:
				for _, value := range reasons {
					if value == reason {
						return true
					}
				}
			}
		}
	}
	return false
}

func setJMeterRuntimeParity(script *dsl.Script, key string, status string) {
	if script == nil || strings.TrimSpace(key) == "" || strings.TrimSpace(status) == "" {
		return
	}
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{Source: "jmeter", Preserved: map[string]any{}}
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = map[string]any{}
	}
	parity, ok := script.Metadata.Import.Preserved["runtime_parity"].(map[string]string)
	if !ok {
		parity = map[string]string{}
	}
	parity[key] = status
	script.Metadata.Import.Preserved["runtime_parity"] = parity
}

func mergeJMeterRuntimeParity(parent *dsl.Script, child *dsl.Script) {
	if parent == nil || child == nil || child.Metadata == nil || child.Metadata.Import == nil || child.Metadata.Import.Preserved == nil {
		return
	}
	switch parity := child.Metadata.Import.Preserved["runtime_parity"].(type) {
	case map[string]string:
		for key, status := range parity {
			setJMeterRuntimeParity(parent, key, status)
		}
	case map[string]any:
		for key, status := range parity {
			if text, ok := status.(string); ok {
				setJMeterRuntimeParity(parent, key, text)
			}
		}
	}
}

func simpleJMeterCondition(raw string) (dsl.Condition, bool) {
	condition := strings.TrimSpace(raw)
	if condition == "" {
		return dsl.Condition{}, false
	}
	if match := regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`).FindStringSubmatch(condition); len(match) == 2 {
		return dsl.Condition{Left: "{{var." + match[1] + "}}", Op: "exists"}, true
	}
	if match := regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}\s*={2}\s*(.+)$`).FindStringSubmatch(condition); len(match) == 3 {
		return dsl.Condition{Left: "{{var." + match[1] + "}}", Op: "eq", Right: strings.Trim(strings.TrimSpace(match[2]), `"'`)}, true
	}
	if match := regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}\s*!=\s*(.+)$`).FindStringSubmatch(condition); len(match) == 3 {
		return dsl.Condition{Left: "{{var." + match[1] + "}}", Op: "ne", Right: strings.Trim(strings.TrimSpace(match[2]), `"'`)}, true
	}
	return dsl.Condition{}, false
}

// jmeterWhileLoopSafetyCap bounds converted WhileController loops: JMeter
// whiles are unbounded, but a replayed loop whose exit condition never fires
// must not hang a VUser.
const jmeterWhileLoopSafetyCap = 100

// jmeterSamplerScope collects the HeaderManager, timer and ResponseAssertion
// entries nested beneath one sampler. In JMeter such an element placed inside a
// sampler's hashTree applies to that sampler only, unlike a thread-group-level
// element which applies to every sampler beneath it. Nested controllers are not
// descended into: their samplers are converted separately and would inherit the
// wrong scope. Disabled children are skipped so an element JMeter would never
// run cannot alter the imported request.
func jmeterSamplerScope(sampler *treeNode, diagnostics *diag.Collector) (dsl.HeaderList, int, int, []dsl.Assertion) {
	if sampler == nil {
		return nil, 0, 0, nil
	}
	var headers dsl.HeaderList
	timerMs, timerCount := jmeterSamplerTimerScope(sampler, diagnostics)
	var assertions []dsl.Assertion
	for _, child := range sampler.Children {
		if child == nil || child.Element == nil || !jmeterElementEnabled(child.Element) {
			continue
		}
		switch child.Element.Name {
		case "HeaderManager":
			headers = append(headers, parseHeaders(child.Element)...)
		case "ResponseAssertion":
			assertions = append(assertions, parseResponseAssertions(child.Element, diagnostics)...)
		}
	}
	return headers, timerMs, timerCount, assertions
}

func jmeterSamplerTimerScope(sampler *treeNode, diagnostics *diag.Collector) (int, int) {
	if sampler == nil {
		return 0, 0
	}
	var timerMs int
	var timerCount int
	for _, child := range sampler.Children {
		if child == nil || child.Element == nil || !jmeterElementEnabled(child.Element) {
			continue
		}
		switch child.Element.Name {
		case "ConstantTimer", "UniformRandomTimer", "GaussianRandomTimer", "PoissonRandomTimer", "ConstantThroughputTimer", "PreciseThroughputTimer":
			if delayMs := parseTimerMs(child.Element, child.Element.Name); delayMs > 0 {
				timerMs += delayMs
			}
			timerCount++
			if diagnostics != nil && isThroughputTimer(child.Element.Name) {
				diagnostics.Add(diag.Info, "JMeter "+child.Element.Name, jmeterElementLocation(child.Element), "throughput timer was approximated as fixed think time")
			}
		}
	}
	return timerMs, timerCount
}

func appendedJMeterSamplerAction(script *dsl.Script, actionStart int) *dsl.Action {
	if script == nil || actionStart < 0 || len(script.Actions) != actionStart+1 {
		return nil
	}
	return &script.Actions[actionStart]
}

func applyJMeterNativeSamplerTiming(sampler *treeNode, action *dsl.Action, ctx *convertContext, result *ConvertResult, diagnostics *diag.Collector) {
	scopedTimerMs, scopedTimerCount := jmeterSamplerTimerScope(sampler, diagnostics)
	if result != nil {
		result.Stats.Timers += scopedTimerCount
	}
	if ctx == nil {
		return
	}
	if action != nil {
		totalTimerMs := ctx.timerMs + ctx.oneShotTimerMs + scopedTimerMs
		if totalTimerMs > 0 {
			action.ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: float64(totalTimerMs)}
			action.ThinkTimeAfter = true
		}
	}
	ctx.oneShotTimerMs = 0
}

func overlayJMeterHeaders(inherited, local dsl.HeaderList) dsl.HeaderList {
	if len(local) == 0 {
		return append(dsl.HeaderList(nil), inherited...)
	}
	localNames := make(map[string]struct{}, len(local))
	for _, header := range local {
		localNames[strings.ToLower(strings.TrimSpace(header.Name))] = struct{}{}
	}
	result := make(dsl.HeaderList, 0, len(inherited)+len(local))
	for _, header := range inherited {
		if _, shadowed := localNames[strings.ToLower(strings.TrimSpace(header.Name))]; !shadowed {
			result = append(result, header)
		}
	}
	// Keep every occurrence from the winning manager. Repeated header fields can
	// be wire-significant; only entries inherited from an outer scope are
	// shadowed.
	result = append(result, local...)
	return result
}

func jmeterElementEnabled(element *xmlNode) bool {
	if element == nil {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(element.Attrs["enabled"]), "false")
}

// Accounting reports this conversion's completeness in the terms every importer
// shares. See convertresult.Accounting.
func (result *ConvertResult) Accounting() convertresult.Accounting {
	if result == nil {
		return convertresult.Accounting{}
	}
	return convertresult.New(
		result.Stats.TotalSamplers,
		result.Stats.ConvertedActions,
		result.Stats.SkippedSamplers,
		result.SourceReview.ReviewOnlyActionCount(),
	)
}
