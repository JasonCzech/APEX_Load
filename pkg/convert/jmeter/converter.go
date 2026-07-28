package jmeter

import (
	"fmt"
	"regexp"
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
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

type ConvertOptions struct {
	ScriptName        string
	BaseURL           string
	IncludeAssertions bool
	ScenarioDraft     bool
	FileName          string
	SidecarFiles      map[string][]byte
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
	defaultProtocol  string
	defaultDomain    string
	defaultPort      string
	defaultPath      string
	effectiveBaseURL string
	headers          dsl.HeaderList
	variableScope    *jmeterVariableScope
	provenVariables  map[string]struct{}
	csvSources       []dsl.DataSource
	timerMs          int
	assertions       []dsl.Assertion
	auths            []jmeterAuth
	defaultAuthSeen  bool
	requestReports   *[]requestimport.Report
	credentialNames  *credentialpolicy.NameRegistry
	importErr        *error
	transaction      string
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
	variableReview := newJMeterVariableReview()
	type variableSet struct {
		values map[string]string
		source credentialpolicy.SourceRef
	}
	planVariableSets := make([]variableSet, 0)
	for _, plan := range findTreeNodes(trees, "TestPlan") {
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

	threadGroups := findTreeNodes(trees, "ThreadGroup")
	if len(threadGroups) == 0 {
		threadGroups = trees
		result.Assumptions = append(result.Assumptions, "No Thread Group was found; the whole plan was converted into a single script.")
	}
	result.Stats.ThreadGroups = len(threadGroups)
	result.Stats.TotalSamplers, result.Stats.SkippedSamplers = countJMeterSamplerElements(trees)
	result.Summary.ThreadGroupCount = len(threadGroups)

	for i, tg := range threadGroups {
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
			scriptProtocol:  groupProtocol,
			requestReports:  &requestReports,
			credentialNames: credentialpolicy.NewNameRegistry(),
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
		result.Scripts = append(result.Scripts, script)
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
				if credential.SensitiveName(property.Name) &&
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
	for _, child := range node.Children {
		switch child.Element.Name {
		case "ConfigTestElement":
			applyHTTPDefaults(child.Element, ctx)
		case "HeaderManager":
			headers := parseHeaders(child.Element)
			for _, header := range headers {
				ctx.headers = append(ctx.headers, header)
				result.Stats.Headers++
			}
		case "CookieManager":
			enabled := true
			script.Config.HTTP.CookiesEnabled = &enabled
			setJMeterRuntimeParity(script, "cookieManager", "executed")
		case "CacheManager":
			applyJMeterCacheManager(child.Element, script)
		case "TestAction":
			if pauseMs := parseTestActionPauseMs(child); pauseMs > 0 {
				ctx.timerMs = pauseMs
				result.Stats.Timers++
				setJMeterRuntimeParity(script, "testActionPause", "executed")
				diagnostics.Add(diag.Info, "JMeter TestAction", jmeterElementLocation(child.Element), "pause action was mapped to think time on the next converted sampler")
			}
			continue
		case "AuthManager":
			auths := parseAuthManager(child.Element, diagnostics)
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
			vars := parseArguments(child.Element)
			ctx.variableScope = ctx.variableScope.withDefinitions(vars, credentialpolicy.SourceRef{
				Format:  "jmeter",
				Path:    sourceFileName,
				Pointer: jmeterElementLocation(child.Element),
			}, diagnostics)
			for k := range vars {
				variableNames[k] = struct{}{}
				result.Stats.Variables++
			}
		case "CSVDataSet":
			ds := parseCSVDataSet(child.Element, opts.SidecarFiles)
			if ds.Name != "" {
				ctx.csvSources = append(ctx.csvSources, ds)
				csvNames[ds.Name] = struct{}{}
				result.Stats.CSVSources++
			}
		case "ConstantTimer", "UniformRandomTimer", "GaussianRandomTimer", "PoissonRandomTimer", "ConstantThroughputTimer", "PreciseThroughputTimer":
			ctx.timerMs = parseTimerMs(child.Element, child.Element.Name)
			if isThroughputTimer(child.Element.Name) {
				diagnostics.Add(diag.Info, "JMeter "+child.Element.Name, jmeterElementLocation(child.Element), "throughput timer was approximated as fixed think time")
			}
			result.Stats.Timers++
		case "ResponseAssertion":
			assertions := parseResponseAssertions(child.Element, diagnostics)
			ctx.assertions = append(ctx.assertions, assertions...)
			result.Stats.Assertions += len(assertions)
		case "HTTPSamplerProxy":
			// A HeaderManager nested under a sampler's own hashTree applies to
			// that sampler alone in JMeter. Sampler children are not walked (see
			// the recursion guard at the end of this loop), and HeaderManager is
			// in the known-supported set, so it produced no diagnostic either:
			// the headers were silently dropped and the imported request went out
			// without them, auth headers included.
			samplerCtx := ctx
			if scoped := jmeterSamplerScopedHeaders(child); len(scoped) > 0 {
				next := *ctx
				next.headers = append(append(dsl.HeaderList(nil), ctx.headers...), scoped...)
				samplerCtx = &next
				result.Stats.Headers += len(scoped)
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
			diagnoseJMeterChildren(child.Children, diagnostics, unsupported)
		case "ThreadGroup", "TestPlan":
			walkThreadGroup(child, ctx, script, opts, result, unsupported, variableNames, csvNames, diagnostics, sourceFileName, sourceMappings)
			continue
		case "TransactionController":
			next := *ctx
			txName := attrOr(child.Element, "testname", "Transaction")
			if ctx.transaction != "" {
				txName = ctx.transaction + " / " + txName
			}
			next.transaction = txName
			walkThreadGroup(child, &next, script, opts, result, unsupported, variableNames, csvNames, diagnostics, sourceFileName, sourceMappings)
			continue
		case "OnceOnlyController":
			steps := collectJMeterChildActions(child, ctx, script, opts, result, unsupported, variableNames, csvNames, diagnostics, sourceFileName)
			if len(steps) == 0 {
				diagnostics.Add(diag.Info, "JMeter OnceOnlyController", jmeterElementLocation(child.Element), "OnceOnlyController contained no converted child samplers")
				continue
			}
			start := len(script.Init)
			script.Init = append(script.Init, steps...)
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
				steps := collectJMeterChildActions(child, ctx, script, opts, result, unsupported, variableNames, csvNames, diagnostics, sourceFileName)
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
				steps := collectJMeterChildActions(child, ctx, script, opts, result, unsupported, variableNames, csvNames, diagnostics, sourceFileName)
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
				steps := collectJMeterChildActions(child, ctx, script, opts, result, unsupported, variableNames, csvNames, diagnostics, sourceFileName)
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
		case "JDBCDataSource":
			if cfg, credentialRetained, ok := parseJMeterJDBCConnection(child.Element); ok {
				ctx.databaseConfig = cfg
				// Keep the complete authored JDBC source record even when every
				// property could be projected into the native DSN. Some JDBC
				// driver properties have no portable database/sql equivalent;
				// retaining the exact source lets an operator repair the
				// already-gated adapter without re-importing or re-entering a
				// password.
				preserveJMeterJDBCSource(script, child.Element)
				if credentialRetained {
					diagnostics.Add(diag.Warning, "JMeter JDBCDataSource", jmeterElementLocation(child.Element), "database credentials were retained in the durable destination and will be masked on ordinary reads; review the imported connection before replay")
				}
			}
			continue
		case "JDBCSampler":
			handleJMeterJDBCSampler(child, ctx, script, result, diagnostics, sourceFileName, sourceMappings)
			continue
		case "FTPSampler", "TCPSampler":
			handleJMeterNetworkSampler(child, ctx, script, result, diagnostics, sourceFileName, sourceMappings)
			continue
		case "JMSSampler", "JMSPublisher", "JMSSubscriber", "PublisherSampler", "SubscriberSampler":
			handleJMeterJMSSampler(child, script, result, diagnostics, sourceFileName, sourceMappings)
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

func collectJMeterChildActions(node *treeNode, ctx *convertContext, parentScript *dsl.Script, opts ConvertOptions, result *ConvertResult, unsupported map[string]struct{}, variableNames map[string]struct{}, csvNames map[string]struct{}, diagnostics *diag.Collector, sourceFileName string) []dsl.Action {
	if node == nil || len(node.Children) == 0 {
		return nil
	}
	next := *ctx
	next.provenVariables = jmeterProvenVariables(ctx)
	tempScript := &dsl.Script{Protocol: "http", Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{DefaultHeaders: map[string]string{}}}}
	var tempMappings []sourceview.Mapping
	walkThreadGroup(node, &next, tempScript, opts, result, unsupported, variableNames, csvNames, diagnostics, sourceFileName, &tempMappings)
	mergeJMeterRuntimeParity(parentScript, tempScript)
	return append([]dsl.Action(nil), tempScript.Actions...)
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

// jmeterSamplerScopedHeaders collects HeaderManager entries nested beneath one
// sampler. In JMeter a HeaderManager placed inside a sampler's hashTree applies
// to that sampler only, unlike a thread-group-level manager which applies to
// every sampler beneath it. Nested controllers are not descended into: their
// samplers are converted separately and would inherit the wrong scope.
func jmeterSamplerScopedHeaders(sampler *treeNode) dsl.HeaderList {
	if sampler == nil {
		return nil
	}
	var headers dsl.HeaderList
	for _, child := range sampler.Children {
		if child == nil || child.Element == nil {
			continue
		}
		if child.Element.Name != "HeaderManager" {
			continue
		}
		headers = append(headers, parseHeaders(child.Element)...)
	}
	return headers
}
