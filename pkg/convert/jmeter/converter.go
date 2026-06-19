package jmeter

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
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
	Scripts             []*dsl.Script      `json:"scripts"`
	Script              *dsl.Script        `json:"script,omitempty"`
	ScenarioDraft       *ScenarioDraft     `json:"scenario_draft,omitempty"`
	Warnings            []string           `json:"warnings,omitempty"`
	UnsupportedElements []string           `json:"unsupported_elements,omitempty"`
	Unsupported         diag.Diagnostics   `json:"unsupported,omitempty"`
	Assumptions         []string           `json:"assumptions,omitempty"`
	Stats               ConvertStats       `json:"stats"`
	Summary             ImportSummary      `json:"summary"`
	SourceReview        *sourceview.Review `json:"source_review,omitempty"`
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
	defaultProtocol string
	defaultDomain   string
	defaultPort     string
	defaultPath     string
	headers         map[string]string
	variables       map[string]string
	csvSources      []dsl.DataSource
	timerMs         int
	assertions      []dsl.Assertion
	auths           []jmeterAuth
	transaction     string
}

type jmeterAuth struct {
	URL       string
	Username  string
	Password  string
	Domain    string
	Mechanism string
	Config    dsl.AuthConfig
}

func GetSupportMatrix() SupportMatrix {
	return SupportMatrix{
		Supported: []string{
			"Test Plan",
			"Thread Group",
			"HTTP Request Defaults",
			"HTTP Header Manager",
			"HTTP Cookie Manager",
			"HTTP Cache Manager",
			"User Defined Variables",
			"CSV Data Set Config",
			"HTTP Request samplers",
			"Constant Timer",
			"Uniform Random Timer",
			"Response Assertions",
			"Regex Extractor",
			"JSON Extractor",
			"JMESPath Extractor",
			"CSS/JQuery Extractor",
			"XPath Extractor",
			"Boundary Extractor",
			"Simple If Controller",
			"Fixed Loop Controller",
			"Once Only Controller",
			"Test Action pauses",
		},
		Partial: []string{
			"Simple controllers mapped to native control_if/control_loop where safe",
			"Variable substitution across URLs, headers, and bodies",
			"Scenario drafts derived from thread groups",
			"Timer behavior approximated as fixed think time when necessary",
			"SOAP-shaped HTTP Request samplers are promoted to the SOAP protocol when an imported thread group is entirely SOAP-oriented",
			"XPath2 Extractor mapped into APEX Load XPath when queries fit the shared runtime, including fragment output and custom namespace bindings",
		},
		Unsupported: []string{
			"JSR223 / BeanShell / Groovy scripting",
			"JMeter plugin samplers, listeners, and controllers",
			"JMS samplers",
			"JDBC / FTP / dedicated SOAP plugin samplers / custom protocol plugins",
			"Complex conditional controller logic",
			"Full JMeter listener/reporting model",
			"Distributed engine/controller orchestration settings",
		},
		Notes: []string{
			"Phase 1 targets HTTP-oriented JMX plans and can promote SOAP-shaped HTTP samplers into native APEX Load SOAP scripts.",
			"Unsupported constructs are reported in the import review instead of being silently ignored.",
		},
	}
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

	threadGroups := findTreeNodes(trees, "ThreadGroup")
	if len(threadGroups) == 0 {
		threadGroups = trees
		result.Assumptions = append(result.Assumptions, "No Thread Group was found; the whole plan was converted into a single script.")
	}
	result.Stats.ThreadGroups = len(threadGroups)
	result.Stats.TotalSamplers, result.Stats.SkippedSamplers = countJMeterSamplerElements(trees)
	result.Summary.ThreadGroupCount = len(threadGroups)

	for i, tg := range threadGroups {
		ctx := convertContext{headers: map[string]string{}, variables: map[string]string{}}
		threadName := attrOr(tg.Element, "testname", fmt.Sprintf("Thread Group %d", i+1))
		scriptName := opts.ScriptName
		if scriptName == "" {
			scriptName = sanitizeName(threadName)
			if len(threadGroups) > 1 {
				scriptName = fmt.Sprintf("%s_%d", scriptName, i+1)
			}
		}
		script := &dsl.Script{
			Name:          scriptName,
			Version:       "1.0.0",
			Protocol:      "http",
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

		walkThreadGroup(tg, &ctx, script, opts, result, unsupported, variableNames, csvNames, &diagnostics, sourceFileName, &sourceMappings)
		if script.Config.BaseURL == "" {
			script.Config.BaseURL = opts.BaseURL
		}
		if script.Config.BaseURL == "" {
			script.Config.BaseURL = detectBaseURL(script.Actions)
		}
		if script.Config.BaseURL == "" && ctx.defaultDomain != "" {
			script.Config.BaseURL = buildBaseURL(ctx.defaultProtocol, ctx.defaultDomain, ctx.defaultPort)
		}
		if len(ctx.headers) > 0 {
			for k, v := range ctx.headers {
				script.Config.HTTP.DefaultHeaders[k] = convertVariables(v)
			}
		}
		if len(script.Config.HTTP.DefaultHeaders) == 0 {
			script.Config.HTTP.DefaultHeaders = nil
		}
		if len(ctx.csvSources) > 0 {
			script.DataSources = append(script.DataSources, ctx.csvSources...)
		}
		if len(script.Actions) == 0 {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: no HTTP samplers were converted", threadName))
			continue
		}
		applySOAPProtocolHints(script, &result.Warnings, threadName)
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
	result.SourceReview = sourceview.Build(map[string]string{sourceFileName: string(data)}, sourceMappings)
	return result, nil
}

func walkThreadGroup(node *treeNode, ctx *convertContext, script *dsl.Script, opts ConvertOptions, result *ConvertResult, unsupported map[string]struct{}, variableNames map[string]struct{}, csvNames map[string]struct{}, diagnostics *diag.Collector, sourceFileName string, sourceMappings *[]sourceview.Mapping) {
	for _, child := range node.Children {
		switch child.Element.Name {
		case "ConfigTestElement":
			applyHTTPDefaults(child.Element, ctx)
		case "HeaderManager":
			headers := parseHeaders(child.Element)
			for k, v := range headers {
				ctx.headers[k] = v
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
			ctx.auths = append(ctx.auths, auths...)
			for _, auth := range auths {
				if auth.URL == "" && script.Config.HTTP.Auth.Type == "" {
					script.Config.HTTP.Auth = auth.Config
				}
			}
		case "Arguments":
			vars := parseArguments(child.Element)
			for k, v := range vars {
				ctx.variables[k] = v
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
			action, warnings := convertHTTPSampler(child, ctx, opts, diagnostics)
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
				*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, child.Element, child.Element.Name, "", jmeterElementLocation(child.Element), "controller", "review_only", "JMeter LoopController infinite loop (-1) was flattened as 1 iteration."))
				if len(child.Children) > 0 {
					walkThreadGroup(child, ctx, script, opts, result, unsupported, variableNames, csvNames, diagnostics, sourceFileName, sourceMappings)
				}
			} else {
				diagnostics.Add(diag.Warning, "JMeter LoopController", jmeterElementLocation(child.Element), "dynamic loop count '"+loopCountStr+"' cannot be statically unrolled; flattening as 1 iteration")
				*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, child.Element, child.Element.Name, "", jmeterElementLocation(child.Element), "controller", "review_only", "JMeter LoopController dynamic loop count '"+loopCountStr+"' was flattened as 1 iteration."))
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
				diagnostics.Add(diag.Warning, "JMeter IfController", jmeterElementLocation(child.Element), fmt.Sprintf("conditional execution is review-gated; children were flattened (condition was: %s)", condition))
				*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, child.Element, child.Element.Name, "", jmeterElementLocation(child.Element), "controller", "review_only", fmt.Sprintf("JMeter IfController was flattened; original condition: %s", condition)))
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
			diagnostics.Add(diag.Warning, "JMeter WhileController", jmeterElementLocation(child.Element), fmt.Sprintf("while condition could not be translated statically; children were flattened (condition was: %s)", condition))
			*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, child.Element, child.Element.Name, "", jmeterElementLocation(child.Element), "controller", "review_only", fmt.Sprintf("JMeter WhileController was flattened; original condition: %s", condition)))
		case "ForeachController", "ForEachController", "InterleaveControl", "RandomController", "ThroughputController":
			diagnostics.Add(diag.Warning, "JMeter "+child.Element.Name, jmeterElementLocation(child.Element), "controller logic was flattened; child requests are imported in source order without original conditions or transaction semantics")
			*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, child.Element, child.Element.Name, "", jmeterElementLocation(child.Element), "controller", "review_only", "JMeter controller logic was flattened; child requests are imported in source order without original conditions."))
		case "DebugSampler":
			diagnostics.Add(diag.Info, "JMeter DebugSampler", jmeterElementLocation(child.Element), "DebugSampler was preserved as diagnostic metadata and does not block executable HTTP import")
			*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, child.Element, child.Element.Name, "", jmeterElementLocation(child.Element), "diagnostic", "ignored", "JMeter DebugSampler does not generate an executable APEX Load action."))
		case "hashTree":
		default:
			if strings.TrimSpace(child.Element.Name) != "" {
				unsupported[child.Element.Name] = struct{}{}
				diagnoseJMeterElement(child.Element, diagnostics)
				*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, child.Element, child.Element.Name, "", jmeterElementLocation(child.Element), "unsupported", "review_only", "JMeter element was not converted into executable APEX Load DSL."))
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
