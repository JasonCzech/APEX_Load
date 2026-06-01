package jmeter

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/convert/tmpl"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type ConvertOptions struct {
	ScriptName        string
	BaseURL           string
	IncludeAssertions bool
	ScenarioDraft     bool
	FileName          string
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
		},
		Partial: []string{
			"Simple controllers flattened into action order where safe",
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
	sourceFileName := firstNonEmpty(opts.FileName, "import.jmx")
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
		diagnostics.Add(diag.Warning, "JMeter completeness guard", firstNonEmpty(opts.FileName, "JMX plan"), fmt.Sprintf("%d sampler(s) were detected but not converted or explicitly skipped", gap))
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
			ds := parseCSVDataSet(child.Element)
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
		case "IfController", "WhileController", "ForeachController", "ForEachController", "LoopController", "InterleaveControl", "RandomController", "ThroughputController":
			diagnostics.Add(diag.Warning, "JMeter "+child.Element.Name, jmeterElementLocation(child.Element), "controller logic was flattened; child requests are imported in source order without original conditions or transaction semantics")
			*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, child.Element, child.Element.Name, "", jmeterElementLocation(child.Element), "controller", "review_only", "JMeter controller logic was flattened; child requests are imported in source order without original conditions."))
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

func countJMeterSamplerElements(nodes []*treeNode) (int, int) {
	total := 0
	skipped := 0
	for _, node := range nodes {
		if node == nil || node.Element == nil {
			continue
		}
		if isJMeterSamplerElement(node.Element.Name) {
			total++
			if node.Element.Name != "HTTPSamplerProxy" {
				skipped++
			}
		}
		childTotal, childSkipped := countJMeterSamplerElements(node.Children)
		total += childTotal
		skipped += childSkipped
	}
	return total, skipped
}

func appendJMeterActionSourceMappings(mappings *[]sourceview.Mapping, sourceFile string, node *treeNode, actionIndex int, action *dsl.Action) {
	if mappings == nil || node == nil || node.Element == nil || action == nil {
		return
	}
	*mappings = append(*mappings, jmeterSourceMapping(
		sourceFile,
		node.Element,
		node.Element.Name,
		fmt.Sprintf("actions[%d]", actionIndex),
		action.Name,
		"action",
		"converted",
		"JMeter HTTP sampler was converted into a generated APEX Load action.",
	))
	for index, extractor := range action.Extractors {
		*mappings = append(*mappings, jmeterSourceMapping(
			sourceFile,
			node.Element,
			"jmeter_extractor",
			fmt.Sprintf("actions[%d].extractors[%d]", actionIndex, index),
			extractor.Name,
			"extractor",
			"contributor",
			"JMeter post-processor was converted into a generated extractor.",
		))
	}
	for index, assertion := range action.Assertions {
		*mappings = append(*mappings, jmeterSourceMapping(
			sourceFile,
			node.Element,
			"jmeter_assertion",
			fmt.Sprintf("actions[%d].assertions[%d]", actionIndex, index),
			assertion.Type,
			"assertion",
			"contributor",
			"JMeter assertion or default assertion contributed to this generated assertion.",
		))
	}
	if action.ThinkTimeAfter {
		*mappings = append(*mappings, jmeterSourceMapping(
			sourceFile,
			node.Element,
			"jmeter_timer",
			fmt.Sprintf("actions[%d].think_time", actionIndex),
			"Think Time",
			"config",
			"contributor",
			"JMeter timer contributed to this generated think-time setting.",
		))
	}
}

func jmeterSourceMapping(sourceFile string, element *xmlNode, function string, targetPath string, targetName string, targetKind string, status string, notes string) sourceview.Mapping {
	startLine := 1
	endLine := 1
	if element != nil {
		startLine = element.StartLine
		endLine = element.EndLine
		if endLine == 0 {
			endLine = startLine
		}
	}
	return sourceview.Mapping{
		SourceFile: sourceFile,
		StartLine:  startLine,
		EndLine:    endLine,
		Function:   function,
		TargetPath: targetPath,
		TargetName: targetName,
		TargetKind: targetKind,
		Status:     status,
		Notes:      notes,
	}
}

func isJMeterSamplerElement(name string) bool {
	if name == "HTTPSamplerProxy" || strings.HasSuffix(name, "Sampler") {
		return true
	}
	switch name {
	case "JMSPublisher", "JMSSubscriber", "JavaSampler", "TCPSampler", "FTPSampler":
		return true
	default:
		return false
	}
}

func diagnoseJMeterChildren(nodes []*treeNode, diagnostics *diag.Collector, unsupported map[string]struct{}) {
	for _, child := range nodes {
		if diagnoseJMeterElement(child.Element, diagnostics) {
			unsupported[child.Element.Name] = struct{}{}
		}
		diagnoseJMeterChildren(child.Children, diagnostics, unsupported)
	}
}

func diagnoseJMeterElement(element *xmlNode, diagnostics *diag.Collector) bool {
	if element == nil || strings.TrimSpace(element.Name) == "" {
		return false
	}
	name := element.Name
	location := jmeterElementLocation(element)
	switch {
	case isJMeterScriptElement(name):
		diagnostics.Add(diag.Warning, "JMeter "+name, location, "JSR223/BeanShell/Groovy scripting is not executed by the importer; translate script logic before replay")
		return true
	case isKnownJMeterUnsupported(name):
		diagnostics.Add(diag.Warning, "JMeter "+name, location, "element is not converted by the HTTP importer and was preserved for import review")
		return true
	case isKnownJMeterSupported(name):
		return false
	default:
		diagnostics.Add(diag.Warning, "JMeter "+name, location, "unrecognized JMX element was not converted; review the source plan before replay")
		return true
	}
}

func isJMeterScriptElement(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "jsr223") || strings.Contains(lower, "beanshell") || strings.Contains(lower, "bsh") || strings.Contains(lower, "groovy")
}

func isKnownJMeterUnsupported(name string) bool {
	switch name {
	case "JSR223Sampler", "BeanShellSampler", "BSFSampler", "JDBCSampler", "JMSSampler", "JMSPublisher", "JMSSubscriber", "FTPSampler", "JavaSampler", "TCPSampler", "DebugSampler", "JSR223PreProcessor", "JSR223PostProcessor", "BeanShellPreProcessor", "BeanShellPostProcessor":
		return true
	default:
		return false
	}
}

func isKnownJMeterSupported(name string) bool {
	switch name {
	case "TestPlan", "ThreadGroup", "hashTree", "ConfigTestElement", "HeaderManager", "CookieManager", "AuthManager", "Arguments", "CSVDataSet", "ConstantTimer", "UniformRandomTimer", "GaussianRandomTimer", "PoissonRandomTimer", "ConstantThroughputTimer", "PreciseThroughputTimer", "ResponseAssertion", "HTTPSamplerProxy", "RegexExtractor", "JSONPostProcessor", "JMESPathExtractor", "HtmlExtractor", "XPathExtractor", "XPath2Extractor", "BoundaryExtractor":
		return true
	default:
		return false
	}
}

func jmeterElementLocation(element *xmlNode) string {
	if element == nil {
		return "JMX element"
	}
	name := attrOr(element, "testname", "")
	if name == "" {
		name = element.Name
	}
	return name
}

func convertHTTPSampler(node *treeNode, ctx *convertContext, opts ConvertOptions, diagnostics *diag.Collector) (*dsl.Action, []string) {
	element := node.Element
	name := sanitizeName(attrOr(element, "testname", "HTTP Request"))
	location := jmeterElementLocation(element)
	method := firstNonEmpty(prop(element, "HTTPSampler.method"), "GET")
	rawPath := convertVariablesWithDiagnostics(firstNonEmpty(prop(element, "HTTPSampler.path"), ctx.defaultPath, "/"), diagnostics, location)
	protocol := firstNonEmpty(prop(element, "HTTPSampler.protocol"), ctx.defaultProtocol)
	domain := convertVariablesWithDiagnostics(firstNonEmpty(prop(element, "HTTPSampler.domain"), ctx.defaultDomain), diagnostics, location)
	port := firstNonEmpty(prop(element, "HTTPSampler.port"), ctx.defaultPort)
	target := rawPath
	absoluteURL := rawPath
	if domain != "" {
		absolute := buildURL(protocol, domain, port, rawPath)
		absoluteURL = absolute
		target = makeRelative(absolute, firstNonEmpty(opts.BaseURL, buildBaseURL(protocol, domain, port)))
	}
	action := &dsl.Action{
		Name:       name,
		Type:       method,
		Method:     method,
		Target:     target,
		Headers:    map[string]string{},
		Assertions: append([]dsl.Assertion(nil), ctx.assertions...),
	}
	var warnings []string
	for k, v := range ctx.headers {
		action.Headers[k] = convertVariablesWithDiagnostics(v, diagnostics, location)
	}
	body, params := parseSamplerArguments(element, diagnostics, location)
	if body != "" {
		action.Body = convertVariablesWithDiagnostics(body, diagnostics, location)
	}
	if len(params) > 0 {
		action.Params = params
	}
	applySamplerFiles(action, element, diagnostics, location)
	if auth, ok := authForSampler(ctx.auths, absoluteURL, rawPath); ok {
		action.Auth = &auth
	}
	if timeout := samplerTimeoutMs(element); timeout > 0 {
		action.TimeoutMs = timeout
	}
	if redirects, ok := samplerRedirects(element); ok {
		action.MaxRedirects = &redirects
	}
	if ctx.transaction != "" {
		action.Name = sanitizeName(ctx.transaction + " " + action.Name)
		if action.Metadata == nil {
			action.Metadata = map[string]any{}
		}
		action.Metadata["transaction"] = ctx.transaction
	}
	if extractors, extractorWarnings := parseSamplerExtractors(node, diagnostics); len(extractors) > 0 {
		action.Extractors = extractors
		warnings = append(warnings, extractorWarnings...)
	} else if len(extractorWarnings) > 0 {
		warnings = append(warnings, extractorWarnings...)
	}
	if opts.IncludeAssertions && !hasStatusAssertion(action.Assertions) {
		action.Assertions = append(action.Assertions, dsl.Assertion{Type: "status_code", Expected: 200})
	}
	if ctx.timerMs > 0 {
		action.ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: float64(ctx.timerMs)}
		action.ThinkTimeAfter = true
	}
	if len(action.Headers) == 0 {
		action.Headers = nil
	}
	if domain == "" && !strings.HasPrefix(rawPath, "http://") && !strings.HasPrefix(rawPath, "https://") {
		warnings = append(warnings, fmt.Sprintf("%s: no domain/default host was found; target kept as %q", name, rawPath))
	}
	applySOAPSamplerHints(action)
	return action, warnings
}

func applySOAPProtocolHints(script *dsl.Script, warnings *[]string, threadName string) {
	if script == nil || len(script.Actions) == 0 {
		return
	}
	soapCount := 0
	soapVersion := ""
	for _, action := range script.Actions {
		if action.SOAP == nil {
			continue
		}
		soapCount++
		if version := detectSOAPVersionFromAction(action); version == "1.2" {
			soapVersion = "1.2"
		} else if soapVersion == "" {
			soapVersion = "1.1"
		}
	}
	if soapCount == 0 {
		return
	}
	if soapCount != len(script.Actions) {
		*warnings = append(*warnings, fmt.Sprintf("%s: detected %d SOAP-style samplers and %d non-SOAP samplers; keeping HTTP protocol because the imported thread group is mixed", threadName, soapCount, len(script.Actions)-soapCount))
		return
	}
	if soapVersion == "" {
		soapVersion = "1.1"
	}
	script.Protocol = "soap"
	script.Config.SOAP = dsl.SOAPConfig{
		Version: soapVersion,
		FaultHandling: dsl.SOAPFaultHandlingConfig{
			FailOnFault: true,
		},
	}
}

func applySOAPSamplerHints(action *dsl.Action) {
	if action == nil || !isSOAPAction(action) {
		return
	}
	if action.Method == "" || strings.EqualFold(action.Method, "GET") {
		action.Method = "POST"
		action.Type = "POST"
	}
	soapAction := strings.TrimSpace(firstHeaderValue(action.Headers, "SOAPAction"))
	operation := action.Name
	if soapAction != "" {
		operation = strings.Trim(soapAction, `"`)
	}
	bodyMode := "generated"
	if strings.TrimSpace(action.Body) != "" {
		bodyMode = "raw_xml"
	}
	action.SOAP = &dsl.SOAPActionConfig{
		Action:    strings.Trim(soapAction, `"`),
		Operation: operation,
		BodyMode:  bodyMode,
	}
	if action.Headers == nil {
		action.Headers = map[string]string{}
	}
	if firstHeaderValue(action.Headers, "Content-Type") == "" {
		if detectSOAPVersionFromAction(*action) == "1.2" {
			action.Headers["Content-Type"] = "application/soap+xml"
		} else {
			action.Headers["Content-Type"] = "text/xml"
		}
	}
}

func isSOAPAction(action *dsl.Action) bool {
	if action == nil {
		return false
	}
	if strings.TrimSpace(firstHeaderValue(action.Headers, "SOAPAction")) != "" {
		return true
	}
	contentType := strings.ToLower(strings.TrimSpace(firstHeaderValue(action.Headers, "Content-Type")))
	if strings.Contains(contentType, "application/soap+xml") {
		return true
	}
	body := strings.ToLower(strings.TrimSpace(action.Body))
	if strings.Contains(body, ":envelope") || strings.Contains(body, "<envelope") {
		return true
	}
	if strings.Contains(contentType, "text/xml") && body != "" {
		return strings.Contains(body, "soap") || strings.Contains(body, "<envelope")
	}
	return false
}

func detectSOAPVersionFromAction(action dsl.Action) string {
	contentType := strings.ToLower(strings.TrimSpace(firstHeaderValue(action.Headers, "Content-Type")))
	if strings.Contains(contentType, "application/soap+xml") {
		return "1.2"
	}
	body := strings.ToLower(strings.TrimSpace(action.Body))
	if strings.Contains(body, "http://www.w3.org/2003/05/soap-envelope") {
		return "1.2"
	}
	if isSOAPAction(&action) {
		return "1.1"
	}
	return ""
}

func firstHeaderValue(headers map[string]string, key string) string {
	for headerKey, value := range headers {
		if strings.EqualFold(headerKey, key) {
			return value
		}
	}
	return ""
}

func parseSamplerExtractors(node *treeNode, diagnostics *diag.Collector) ([]dsl.Extractor, []string) {
	var out []dsl.Extractor
	var warnings []string
	location := jmeterElementLocation(node.Element)
	for _, child := range node.Children {
		switch child.Element.Name {
		case "RegexExtractor":
			if ext := parseRegexExtractor(child.Element); ext.Name != "" && ext.Expression != "" {
				out = append(out, ext)
			}
		case "JSONPostProcessor":
			out = append(out, parseJSONExtractors(child.Element)...)
		case "JMESPathExtractor":
			if ext := parseJMESPathExtractor(child.Element); ext.Name != "" && ext.Expression != "" {
				out = append(out, ext)
			}
		case "HtmlExtractor":
			if ext := parseHTMLExtractor(child.Element); ext.Name != "" && ext.Expression != "" {
				out = append(out, ext)
			}
		case "XPathExtractor":
			if ext := parseXPathExtractor(child.Element); ext.Name != "" && ext.Expression != "" {
				out = append(out, ext)
			}
		case "XPath2Extractor":
			ext, warning := parseXPath2Extractor(child.Element)
			if warning != "" {
				warnings = append(warnings, warning)
				if diagnostics != nil {
					diagnostics.Add(diag.Warning, "JMeter XPath2Extractor", location, warning)
				}
			}
			if ext.Name != "" && ext.Expression != "" {
				out = append(out, ext)
			}
		case "BoundaryExtractor":
			if ext := parseBoundaryExtractor(child.Element); ext.Name != "" && ext.Expression != "" {
				out = append(out, ext)
			}
		}
	}
	if len(out) == 0 {
		out = nil
	}
	if len(warnings) == 0 {
		warnings = nil
	}
	return out, warnings
}

func parseBoundaryExtractor(node *xmlNode) dsl.Extractor {
	source := parseExtractorSource(prop(node, "BoundaryExtractor.useHeaders"))
	if source == "" {
		return dsl.Extractor{}
	}

	left := prop(node, "BoundaryExtractor.lboundary")
	right := prop(node, "BoundaryExtractor.rboundary")
	if strings.TrimSpace(left) == "" && strings.TrimSpace(right) == "" {
		return dsl.Extractor{}
	}

	return dsl.Extractor{
		Name:       strings.TrimSpace(prop(node, "BoundaryExtractor.refname")),
		Source:     source,
		Type:       "boundary",
		Expression: left + "|||" + right,
	}
}

func parseRegexExtractor(node *xmlNode) dsl.Extractor {
	source := parseExtractorSource(prop(node, "RegexExtractor.useHeaders"))
	if source == "" {
		return dsl.Extractor{}
	}

	return dsl.Extractor{
		Name:       strings.TrimSpace(prop(node, "RegexExtractor.refname")),
		Source:     source,
		Type:       "regex",
		Expression: strings.TrimSpace(prop(node, "RegexExtractor.regex")),
	}
}

func parseExtractorSource(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "", "false":
		return "body"
	case "true":
		return "header"
	default:
		return ""
	}
}

func parseJSONExtractors(node *xmlNode) []dsl.Extractor {
	names := splitDelimitedValues(firstNonEmpty(
		prop(node, "JSONPostProcessor.referenceNames"),
		prop(node, "JSONPostProcessor.reference_names"),
	))
	expressions := splitDelimitedValues(firstNonEmpty(
		prop(node, "JSONPostProcessor.jsonPathExprs"),
		prop(node, "JSONPostProcessor.json_path_exprs"),
	))
	count := min(len(names), len(expressions))
	if count == 0 {
		return nil
	}
	out := make([]dsl.Extractor, 0, count)
	for i := 0; i < count; i++ {
		if names[i] == "" || expressions[i] == "" {
			continue
		}
		out = append(out, dsl.Extractor{
			Name:       names[i],
			Source:     "body",
			Type:       "jsonpath",
			Expression: expressions[i],
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseJMESPathExtractor(node *xmlNode) dsl.Extractor {
	return dsl.Extractor{
		Name:       strings.TrimSpace(prop(node, "JMESExtractor.referenceName")),
		Source:     "body",
		Type:       "jmespath",
		Expression: strings.TrimSpace(prop(node, "JMESExtractor.jmesPathExpr")),
	}
}

func parseHTMLExtractor(node *xmlNode) dsl.Extractor {
	selector := strings.TrimSpace(prop(node, "HtmlExtractor.expr"))
	attribute := strings.TrimSpace(prop(node, "HtmlExtractor.attribute"))
	if selector == "" {
		return dsl.Extractor{}
	}

	expression := selector
	if attribute != "" {
		expression += "|||" + attribute
	}

	return dsl.Extractor{
		Name:       strings.TrimSpace(prop(node, "HtmlExtractor.refname")),
		Source:     "body",
		Type:       "css",
		Expression: expression,
	}
}

const xpathFragmentExpressionPrefix = "__apexload_xpath_fragment__:"

const xpathExpressionConfigPrefix = "__apexload_xpath_config__:"

type xpathExpressionConfig struct {
	Expression string            `json:"expression"`
	Fragment   bool              `json:"fragment,omitempty"`
	Namespaces map[string]string `json:"namespaces,omitempty"`
}

func parseXPathExtractor(node *xmlNode) dsl.Extractor {
	expression := strings.TrimSpace(prop(node, "XPathExtractor.xpathQuery"))
	fragment := strings.EqualFold(strings.TrimSpace(prop(node, "XPathExtractor.fragment")), "true")

	return dsl.Extractor{
		Name:       strings.TrimSpace(prop(node, "XPathExtractor.refname")),
		Source:     "body",
		Type:       "xpath",
		Expression: encodeXPathExpression(expression, fragment, nil),
	}
}

func parseXPath2Extractor(node *xmlNode) (dsl.Extractor, string) {
	name := strings.TrimSpace(prop(node, "XPathExtractor2.refname"))
	expression := strings.TrimSpace(prop(node, "XPathExtractor2.xpathQuery"))
	namespaceConfig := strings.TrimSpace(prop(node, "XPathExtractor2.namespaces"))
	fragment := strings.EqualFold(strings.TrimSpace(prop(node, "XPathExtractor2.fragment")), "true")

	namespaces, err := parseXPathNamespaces(namespaceConfig)
	if err != nil {
		warning := fmt.Sprintf("XPath2 extractor %q was skipped because its namespace bindings were invalid: %v", name, err)
		if name == "" {
			warning = fmt.Sprintf("XPath2 extractor was skipped because its namespace bindings were invalid: %v", err)
		}
		return dsl.Extractor{}, warning
	}

	return dsl.Extractor{
		Name:       name,
		Source:     "body",
		Type:       "xpath",
		Expression: encodeXPathExpression(expression, fragment, namespaces),
	}, ""
}

func encodeXPathExpression(expression string, fragment bool, namespaces map[string]string) string {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return expression
	}
	if len(namespaces) == 0 {
		if !fragment {
			return expression
		}
		return xpathFragmentExpressionPrefix + expression
	}
	payload, err := json.Marshal(xpathExpressionConfig{
		Expression: expression,
		Fragment:   fragment,
		Namespaces: namespaces,
	})
	if err != nil {
		return expression
	}
	return xpathExpressionConfigPrefix + base64.RawStdEncoding.EncodeToString(payload)
}

func parseXPathNamespaces(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	namespaces := map[string]string{}
	for _, field := range strings.Fields(raw) {
		parts := strings.SplitN(field, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("expected prefix=uri pair, got %q", field)
		}
		prefix := strings.TrimSpace(parts[0])
		uri := strings.TrimSpace(parts[1])
		if prefix == "" || uri == "" {
			return nil, fmt.Errorf("expected prefix=uri pair, got %q", field)
		}
		namespaces[prefix] = uri
	}
	if len(namespaces) == 0 {
		return nil, nil
	}
	return namespaces, nil
}

func parseXML(data []byte) (*xmlNode, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	content := string(data)
	var stack []*xmlNode
	for {
		tok, err := decoder.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, fmt.Errorf("parse JMX XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			node := &xmlNode{Name: t.Name.Local, Attrs: map[string]string{}, StartLine: sourceview.LineForOffset(content, int(decoder.InputOffset()))}
			for _, attr := range t.Attr {
				node.Attrs[attr.Name.Local] = attr.Value
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, node)
			}
			stack = append(stack, node)
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].Text += string(t)
			}
		case xml.EndElement:
			if len(stack) == 0 {
				continue
			}
			stack[len(stack)-1].EndLine = sourceview.LineForOffset(content, int(decoder.InputOffset()))
			if len(stack) == 1 {
				return stack[0], nil
			}
			stack = stack[:len(stack)-1]
		}
	}
	return nil, fmt.Errorf("empty JMX XML")
}

func buildTree(hashTree *xmlNode) []*treeNode {
	var out []*treeNode
	for i := 0; i < len(hashTree.Children); i++ {
		elem := hashTree.Children[i]
		if elem.Name == "hashTree" {
			continue
		}
		tree := &treeNode{Element: elem}
		if i+1 < len(hashTree.Children) && hashTree.Children[i+1].Name == "hashTree" {
			tree.Children = buildTree(hashTree.Children[i+1])
			i++
		}
		out = append(out, tree)
	}
	return out
}

func findTreeNodes(nodes []*treeNode, name string) []*treeNode {
	var out []*treeNode
	for _, node := range nodes {
		if node.Element.Name == name {
			out = append(out, node)
		}
		out = append(out, findTreeNodes(node.Children, name)...)
	}
	return out
}

func prop(node *xmlNode, name string) string {
	if node == nil {
		return ""
	}
	if node.Attrs["name"] == name {
		return strings.TrimSpace(node.Text)
	}
	for _, child := range node.Children {
		if child.Attrs["name"] == name {
			return strings.TrimSpace(child.Text)
		}
		if got := prop(child, name); got != "" {
			return got
		}
	}
	return ""
}

func childProps(node *xmlNode, name string) []*xmlNode {
	var out []*xmlNode
	for _, child := range node.Children {
		if child.Attrs["name"] == name {
			out = append(out, child)
		}
		out = append(out, childProps(child, name)...)
	}
	return out
}

func applyHTTPDefaults(node *xmlNode, ctx *convertContext) {
	if node.Attrs["guiclass"] != "HttpDefaultsGui" && prop(node, "HTTPSampler.domain") == "" {
		return
	}
	ctx.defaultProtocol = firstNonEmpty(prop(node, "HTTPSampler.protocol"), ctx.defaultProtocol)
	ctx.defaultDomain = firstNonEmpty(prop(node, "HTTPSampler.domain"), ctx.defaultDomain)
	ctx.defaultPort = firstNonEmpty(prop(node, "HTTPSampler.port"), ctx.defaultPort)
	ctx.defaultPath = firstNonEmpty(prop(node, "HTTPSampler.path"), ctx.defaultPath)
}

func parseHeaders(node *xmlNode) map[string]string {
	headers := map[string]string{}
	for _, element := range childProps(node, "Header.name") {
		parent := parentElement(node, element)
		if parent == nil {
			continue
		}
		name := prop(parent, "Header.name")
		value := prop(parent, "Header.value")
		if name != "" {
			headers[name] = value
		}
	}
	return headers
}

func parseAuthManager(node *xmlNode, diagnostics *diag.Collector) []jmeterAuth {
	var auths []jmeterAuth
	location := jmeterElementLocation(node)
	for _, element := range descendantsByAttr(node, "elementType", "Authorization") {
		username := convertVariablesWithDiagnostics(firstNonEmpty(prop(element, "Authorization.username"), prop(element, "username")), diagnostics, location)
		password := convertVariablesWithDiagnostics(firstNonEmpty(prop(element, "Authorization.password"), prop(element, "password")), diagnostics, location)
		mechanism := strings.TrimSpace(firstNonEmpty(prop(element, "Authorization.mechanism"), prop(element, "Authorization.Mechanism"), prop(element, "mechanism")))
		auth := jmeterAuth{
			URL:       convertVariablesWithDiagnostics(firstNonEmpty(prop(element, "Authorization.url"), prop(element, "url")), diagnostics, location),
			Username:  username,
			Password:  password,
			Domain:    convertVariablesWithDiagnostics(firstNonEmpty(prop(element, "Authorization.domain"), prop(element, "domain")), diagnostics, location),
			Mechanism: mechanism,
		}
		auth.Config = authConfigFromJMeterAuth(auth, diagnostics, location)
		if auth.Config.Type == "" {
			continue
		}
		auths = append(auths, auth)
	}
	return auths
}

func authConfigFromJMeterAuth(auth jmeterAuth, diagnostics *diag.Collector, location string) dsl.AuthConfig {
	mechanism := strings.ToLower(strings.TrimSpace(auth.Mechanism))
	switch {
	case strings.Contains(mechanism, "bearer") || strings.Contains(mechanism, "token"):
		return dsl.AuthConfig{Type: "bearer", Token: firstNonEmpty(auth.Password, auth.Username, "{{env.API_TOKEN}}")}
	case strings.Contains(mechanism, "digest"):
		return dsl.AuthConfig{Type: "digest", Username: firstNonEmpty(auth.Username, "{{env.API_USERNAME}}"), Password: firstNonEmpty(auth.Password, "{{env.API_PASSWORD}}"), Domain: auth.Domain}
	case mechanism == "" || strings.Contains(mechanism, "basic") || strings.Contains(mechanism, "kerberos"):
		if strings.Contains(mechanism, "kerberos") && diagnostics != nil {
			diagnostics.Add(diag.Warning, "JMeter AuthManager", location, "Kerberos auth was approximated as basic credentials; configure native auth before replay")
		}
		return dsl.AuthConfig{Type: "basic", Username: firstNonEmpty(auth.Username, "{{env.API_USERNAME}}"), Password: firstNonEmpty(auth.Password, "{{env.API_PASSWORD}}")}
	default:
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "JMeter AuthManager", location, "auth mechanism "+auth.Mechanism+" was not converted")
		}
		return dsl.AuthConfig{}
	}
}

func authForSampler(auths []jmeterAuth, absoluteURL string, rawPath string) (dsl.AuthConfig, bool) {
	for _, auth := range auths {
		if auth.Config.Type == "" || strings.TrimSpace(auth.URL) == "" {
			continue
		}
		if authMatchesURL(auth.URL, absoluteURL) || authMatchesURL(auth.URL, rawPath) {
			config := auth.Config
			return config, true
		}
	}
	return dsl.AuthConfig{}, false
}

func authMatchesURL(authURL string, candidate string) bool {
	authURL = strings.TrimRight(strings.TrimSpace(authURL), "/")
	candidate = strings.TrimRight(strings.TrimSpace(candidate), "/")
	if authURL == "" || candidate == "" {
		return false
	}
	return strings.HasPrefix(candidate, authURL)
}

func parseArguments(node *xmlNode) map[string]string {
	vars := map[string]string{}
	for _, arg := range descendantsByAttr(node, "elementType", "Argument") {
		name := prop(arg, "Argument.name")
		value := prop(arg, "Argument.value")
		if name != "" {
			vars[name] = value
		}
	}
	return vars
}

func parseCSVDataSet(node *xmlNode) dsl.DataSource {
	filename := prop(node, "filename")
	variableNames := splitCSVNames(prop(node, "variableNames"))
	name := sanitizeName(firstNonEmpty(attrOr(node, "testname", ""), path.Base(filename), "csv_data"))
	strategy := "sequential"
	if strings.EqualFold(prop(node, "randomOrder"), "true") {
		strategy = "random"
	}
	return dsl.DataSource{
		Name:           name,
		Type:           "csv",
		Path:           filename,
		Strategy:       strategy,
		Delimiter:      firstNonEmpty(prop(node, "delimiter"), ","),
		FirstRowHeader: len(variableNames) == 0,
		Columns:        variableNames,
	}
}

func parseTimerMs(node *xmlNode, elementName string) int {
	delay := parseInt(firstNonEmpty(
		prop(node, "ConstantTimer.delay"),
		prop(node, "RandomTimer.delay"),
		prop(node, "GaussianRandomTimer.delay"),
		prop(node, "PoissonRandomTimer.delay"),
	))
	if elementName == "UniformRandomTimer" {
		rangeMs := parseInt(prop(node, "RandomTimer.range"))
		if rangeMs > 0 {
			return delay + rangeMs/2
		}
	}
	if elementName == "GaussianRandomTimer" {
		rangeMs := parseInt(prop(node, "GaussianRandomTimer.range"))
		if rangeMs > 0 {
			return delay + rangeMs/2
		}
	}
	if elementName == "PoissonRandomTimer" {
		rangeMs := parseInt(prop(node, "PoissonRandomTimer.range"))
		if rangeMs > 0 {
			return delay + rangeMs/2
		}
	}
	if isThroughputTimer(elementName) {
		if throughput := parseFloat(firstNonEmpty(prop(node, "throughput"), jmeterNamedValue(node, "throughput"), prop(node, "ConstantThroughputTimer.throughput"), prop(node, "PreciseThroughputTimer.throughput"))); throughput > 0 {
			period := parseInt(firstNonEmpty(prop(node, "throughputPeriod"), jmeterNamedValue(node, "throughputPeriod"), prop(node, "PreciseThroughputTimer.throughputPeriod")))
			if period <= 0 {
				period = 60
			}
			return int((float64(period) * 1000) / throughput)
		}
	}
	return delay
}

func isThroughputTimer(elementName string) bool {
	return elementName == "ConstantThroughputTimer" || elementName == "PreciseThroughputTimer"
}

func jmeterNamedValue(node *xmlNode, key string) string {
	if node == nil {
		return ""
	}
	for _, child := range node.Children {
		if child.Name == "name" && strings.TrimSpace(child.Text) == key {
			for _, sibling := range node.Children {
				if sibling.Name == "value" {
					return strings.TrimSpace(sibling.Text)
				}
			}
		}
		if value := jmeterNamedValue(child, key); value != "" {
			return value
		}
	}
	return ""
}

func parseResponseAssertions(node *xmlNode, diagnostics *diag.Collector) []dsl.Assertion {
	field := strings.TrimSpace(prop(node, "Assertion.test_field"))
	if field == "" {
		field = "Assertion.response_data"
	}
	testType := parseInt(prop(node, "Assertion.test_type"))
	location := jmeterElementLocation(node)
	values := assertionTestStrings(node)
	if len(values) == 0 {
		return nil
	}
	if field == "Assertion.response_message" || field == "Assertion.response_headers" {
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "JMeter ResponseAssertion", location, "assertion field "+field+" has no native APEX Load assertion mapping and was not converted")
		}
		return nil
	}
	negated := testType&4 != 0
	if strings.EqualFold(field, "Assertion.response_code") {
		out := make([]dsl.Assertion, 0, len(values))
		for _, value := range values {
			if negated {
				if diagnostics != nil {
					diagnostics.Add(diag.Warning, "JMeter ResponseAssertion", location, "negative response-code assertion "+value+" cannot be represented natively and was skipped")
				}
				continue
			}
			status, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				if diagnostics != nil {
					diagnostics.Add(diag.Warning, "JMeter ResponseAssertion", location, "response-code assertion "+value+" is not an integer and was skipped")
				}
				continue
			}
			out = append(out, dsl.Assertion{Type: "status_code", Expected: status})
		}
		return out
	}
	switch field {
	case "Assertion.response_data", "Assertion.response_data_as_document":
	default:
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "JMeter ResponseAssertion", location, "assertion field "+field+" was approximated as a response body assertion")
		}
	}
	assertionType := "body_contains"
	if negated {
		assertionType = "body_not_contains"
	}
	if testType&(1|8) != 0 && diagnostics != nil {
		diagnostics.Add(diag.Info, "JMeter ResponseAssertion", location, "regex or equality assertion semantics were approximated with body contains checks")
	}
	var out []dsl.Assertion
	for _, text := range values {
		out = append(out, dsl.Assertion{Type: assertionType, Value: convertVariablesWithDiagnostics(text, diagnostics, location)})
	}
	return out
}

func assertionTestStrings(node *xmlNode) []string {
	var values []string
	for _, child := range node.Children {
		if child.Name != "collectionProp" || !strings.Contains(strings.ToLower(child.Attrs["name"]), "test_strings") {
			continue
		}
		for _, grand := range child.Children {
			text := strings.TrimSpace(grand.Text)
			if text != "" {
				values = append(values, text)
			}
		}
	}
	if len(values) > 0 {
		return values
	}
	for _, valueNode := range childProps(node, "") {
		text := strings.TrimSpace(valueNode.Text)
		if text != "" {
			values = append(values, text)
		}
	}
	return values
}

func parseSamplerArguments(node *xmlNode, diagnostics *diag.Collector, location string) (string, map[string]any) {
	rawBody := strings.EqualFold(prop(node, "HTTPSampler.postBodyRaw"), "true")
	args := descendantsByAttr(node, "elementType", "HTTPArgument")
	if len(args) == 0 {
		return "", nil
	}
	if rawBody {
		for _, arg := range args {
			if value := prop(arg, "Argument.value"); value != "" {
				return value, nil
			}
		}
	}
	params := map[string]any{}
	for _, arg := range args {
		name := prop(arg, "Argument.name")
		value := prop(arg, "Argument.value")
		if name != "" {
			params[name] = convertVariablesWithDiagnostics(value, diagnostics, location)
		}
	}
	if len(params) == 0 {
		return "", nil
	}
	return "", params
}

func applySamplerFiles(action *dsl.Action, node *xmlNode, diagnostics *diag.Collector, location string) {
	files := descendantsByAttr(node, "elementType", "HTTPFileArg")
	if len(files) == 0 {
		return
	}
	if action.Params == nil {
		action.Params = map[string]any{}
	}
	if action.Headers == nil {
		action.Headers = map[string]string{}
	}
	if firstHeaderValue(action.Headers, "Content-Type") == "" {
		action.Headers["Content-Type"] = "multipart/form-data"
	}
	for _, file := range files {
		paramName := firstNonEmpty(prop(file, "File.paramname"), prop(file, "HTTPFileArg.paramname"), "file")
		filePath := convertVariablesWithDiagnostics(firstNonEmpty(prop(file, "File.path"), prop(file, "HTTPFileArg.path")), diagnostics, location)
		mimeType := firstNonEmpty(prop(file, "File.mimetype"), prop(file, "HTTPFileArg.mimetype"), "application/octet-stream")
		placeholder := "{{file." + sanitizeName(paramName) + "}}"
		entry := map[string]any{
			"value":        placeholder,
			"content_type": mimeType,
		}
		if filePath != "" {
			entry["file_name"] = jmeterBaseName(filePath)
			entry["source_path"] = filePath
		}
		action.Params[paramName] = entry
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "JMeter HTTPFileArg", location, "file upload parameter "+paramName+" uses placeholder "+placeholder+"; provide governed file data before replay")
		}
	}
}

func samplerTimeoutMs(node *xmlNode) int {
	connectTimeout := parseInt(prop(node, "HTTPSampler.connect_timeout"))
	responseTimeout := parseInt(prop(node, "HTTPSampler.response_timeout"))
	if responseTimeout > 0 {
		return responseTimeout
	}
	return connectTimeout
}

func samplerRedirects(node *xmlNode) (int, bool) {
	follow, followOK := parseOptionalBool(prop(node, "HTTPSampler.follow_redirects"))
	auto, autoOK := parseOptionalBool(prop(node, "HTTPSampler.auto_redirects"))
	if !followOK && !autoOK {
		return 0, false
	}
	if follow || auto {
		return 10, true
	}
	return 0, true
}

func parseOptionalBool(value string) (bool, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return false, false
	}
	return strings.EqualFold(value, "true"), true
}

func jmeterBaseName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimRight(value, `/\`)
	if value == "" {
		return ""
	}
	if idx := strings.LastIndexAny(value, `/\`); idx >= 0 {
		return value[idx+1:]
	}
	return value
}

func parentElement(root *xmlNode, target *xmlNode) *xmlNode {
	for _, child := range root.Children {
		if child == target {
			return root
		}
		if found := parentElement(child, target); found != nil {
			return found
		}
	}
	return nil
}

func descendantsByAttr(node *xmlNode, key string, value string) []*xmlNode {
	var out []*xmlNode
	if node.Attrs[key] == value {
		out = append(out, node)
	}
	for _, child := range node.Children {
		out = append(out, descendantsByAttr(child, key, value)...)
	}
	return out
}

func firstChild(node *xmlNode, name string) *xmlNode {
	for _, child := range node.Children {
		if child.Name == name {
			return child
		}
	}
	return nil
}

var jmeterVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_.-]*)\}`)

func convertVariables(value string) string {
	return convertVariablesWithDiagnostics(value, nil, "")
}

func convertVariablesWithDiagnostics(value string, diagnostics *diag.Collector, location string) string {
	value = tmpl.TranslateDynamic(value, diagnostics, location)
	return jmeterVarPattern.ReplaceAllString(value, "{{var.$1}}")
}

func buildBaseURL(protocol, domain, port string) string {
	if domain == "" {
		return ""
	}
	if protocol == "" {
		protocol = "https"
	}
	if port != "" {
		return fmt.Sprintf("%s://%s:%s", protocol, domain, port)
	}
	return fmt.Sprintf("%s://%s", protocol, domain)
}

func buildURL(protocol, domain, port, rawPath string) string {
	base := buildBaseURL(protocol, domain, port)
	if strings.HasPrefix(rawPath, "http://") || strings.HasPrefix(rawPath, "https://") {
		return rawPath
	}
	if !strings.HasPrefix(rawPath, "/") {
		rawPath = "/" + rawPath
	}
	return base + rawPath
}

func makeRelative(rawURL, baseURL string) string {
	if baseURL == "" {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" {
		return rawURL
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return rawURL
	}
	if strings.EqualFold(u.Scheme, base.Scheme) && strings.EqualFold(u.Host, base.Host) {
		target := u.Path
		if target == "" {
			target = "/"
		}
		if u.RawQuery != "" {
			target += "?" + u.RawQuery
		}
		return target
	}
	return rawURL
}

func detectBaseURL(actions []dsl.Action) string {
	for _, action := range actions {
		u, err := url.Parse(action.Target)
		if err == nil && u.Scheme != "" && u.Host != "" {
			return u.Scheme + "://" + u.Host
		}
	}
	return ""
}

func buildScenarioDraft(threadGroups []*treeNode, scripts []*dsl.Script) *ScenarioDraft {
	draft := &ScenarioDraft{
		Name:         "Imported JMeter Scenario",
		Duration:     "5m",
		ScenarioMode: "vuser_driven",
		Groups:       []ScenarioDraftGroup{},
		Metadata:     map[string]interface{}{"source": "jmeter"},
	}
	for i, script := range scripts {
		tg := threadGroups[min(i, len(threadGroups)-1)]
		vusers := parseInt(firstNonEmpty(prop(tg.Element, "ThreadGroup.num_threads"), "1"))
		rampSeconds := parseInt(prop(tg.Element, "ThreadGroup.ramp_time"))
		durationSeconds := parseInt(prop(tg.Element, "ThreadGroup.duration"))
		if durationSeconds > 0 {
			draft.Duration = (time.Duration(durationSeconds) * time.Second).String()
		}
		draft.Groups = append(draft.Groups, ScenarioDraftGroup{
			Name:           attrOr(tg.Element, "testname", script.Name),
			ScriptName:     script.Name,
			VUsers:         max(vusers, 1),
			RampUp:         (time.Duration(rampSeconds) * time.Second).String(),
			Initialization: "at_runtime",
		})
	}
	return draft
}

func hasStatusAssertion(assertions []dsl.Assertion) bool {
	for _, assertion := range assertions {
		if assertion.Type == "status_code" {
			return true
		}
	}
	return false
}

func splitCSVNames(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func splitDelimitedValues(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ';' || r == ','
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func attrOr(node *xmlNode, key string, fallback string) string {
	if node == nil {
		return fallback
	}
	if value := strings.TrimSpace(node.Attrs[key]); value != "" {
		return value
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func parseInt(value string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(value))
	return n
}

func parseFloat(value string) float64 {
	n, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return n
}

func sanitizeName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "JMeter_Import"
	}
	replacer := regexp.MustCompile(`[^A-Za-z0-9_]+`)
	value = replacer.ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return "JMeter_Import"
	}
	return value
}
