package jmeter

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/stormrunner/pkg/dsl"
)

type ConvertOptions struct {
	ScriptName        string
	BaseURL           string
	IncludeAssertions bool
	ScenarioDraft     bool
	FileName          string
}

type ConvertResult struct {
	Scripts             []*dsl.Script  `json:"scripts"`
	Script              *dsl.Script    `json:"script,omitempty"`
	ScenarioDraft       *ScenarioDraft `json:"scenario_draft,omitempty"`
	Warnings            []string       `json:"warnings,omitempty"`
	UnsupportedElements []string       `json:"unsupported_elements,omitempty"`
	Assumptions         []string       `json:"assumptions,omitempty"`
	Stats               ConvertStats   `json:"stats"`
	Summary             ImportSummary  `json:"summary"`
}

type ConvertStats struct {
	ThreadGroups     int `json:"thread_groups"`
	Samplers         int `json:"samplers"`
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
	Name     string
	Attrs    map[string]string
	Text     string
	Children []*xmlNode
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
		},
		Unsupported: []string{
			"JSR223 / BeanShell / Groovy scripting",
			"JMeter plugin samplers, listeners, and controllers",
			"JMS samplers",
			"JDBC / FTP / SOAP / custom protocol plugins",
			"Complex conditional controller logic",
			"Full JMeter listener/reporting model",
			"Distributed engine/controller orchestration settings",
		},
		Notes: []string{
			"Phase 1 targets HTTP-oriented JMX plans and produces native Stormrunner assets.",
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

	threadGroups := findTreeNodes(trees, "ThreadGroup")
	if len(threadGroups) == 0 {
		threadGroups = trees
		result.Assumptions = append(result.Assumptions, "No Thread Group was found; the whole plan was converted into a single script.")
	}
	result.Stats.ThreadGroups = len(threadGroups)
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

		walkThreadGroup(tg, &ctx, script, opts, result, unsupported, variableNames, csvNames)
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
	return result, nil
}

func walkThreadGroup(node *treeNode, ctx *convertContext, script *dsl.Script, opts ConvertOptions, result *ConvertResult, unsupported map[string]struct{}, variableNames map[string]struct{}, csvNames map[string]struct{}) {
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
		case "ConstantTimer", "UniformRandomTimer":
			ctx.timerMs = parseTimerMs(child.Element, child.Element.Name)
			result.Stats.Timers++
		case "ResponseAssertion":
			assertions := parseResponseAssertions(child.Element)
			ctx.assertions = append(ctx.assertions, assertions...)
			result.Stats.Assertions += len(assertions)
		case "HTTPSamplerProxy":
			action, warnings := convertHTTPSampler(child, ctx, opts)
			result.Warnings = append(result.Warnings, warnings...)
			result.Stats.Samplers++
			if action != nil {
				script.Actions = append(script.Actions, *action)
				result.Stats.ConvertedActions++
			}
		case "ThreadGroup", "TestPlan":
			walkThreadGroup(child, ctx, script, opts, result, unsupported, variableNames, csvNames)
			continue
		case "hashTree":
		default:
			if strings.TrimSpace(child.Element.Name) != "" {
				unsupported[child.Element.Name] = struct{}{}
			}
		}
		if len(child.Children) > 0 && child.Element.Name != "HTTPSamplerProxy" {
			walkThreadGroup(child, ctx, script, opts, result, unsupported, variableNames, csvNames)
		}
	}
}

func convertHTTPSampler(node *treeNode, ctx *convertContext, opts ConvertOptions) (*dsl.Action, []string) {
	element := node.Element
	name := sanitizeName(attrOr(element, "testname", "HTTP Request"))
	method := firstNonEmpty(prop(element, "HTTPSampler.method"), "GET")
	rawPath := convertVariables(firstNonEmpty(prop(element, "HTTPSampler.path"), ctx.defaultPath, "/"))
	protocol := firstNonEmpty(prop(element, "HTTPSampler.protocol"), ctx.defaultProtocol)
	domain := convertVariables(firstNonEmpty(prop(element, "HTTPSampler.domain"), ctx.defaultDomain))
	port := firstNonEmpty(prop(element, "HTTPSampler.port"), ctx.defaultPort)
	target := rawPath
	if domain != "" {
		absolute := buildURL(protocol, domain, port, rawPath)
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
	for k, v := range ctx.headers {
		action.Headers[k] = convertVariables(v)
	}
	body, params := parseSamplerArguments(element)
	if body != "" {
		action.Body = convertVariables(body)
	}
	if len(params) > 0 {
		action.Params = params
	}
	if extractors := parseSamplerExtractors(node); len(extractors) > 0 {
		action.Extractors = extractors
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
	var warnings []string
	if domain == "" && !strings.HasPrefix(rawPath, "http://") && !strings.HasPrefix(rawPath, "https://") {
		warnings = append(warnings, fmt.Sprintf("%s: no domain/default host was found; target kept as %q", name, rawPath))
	}
	return action, warnings
}

func parseSamplerExtractors(node *treeNode) []dsl.Extractor {
	var out []dsl.Extractor
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
		case "BoundaryExtractor":
			if ext := parseBoundaryExtractor(child.Element); ext.Name != "" && ext.Expression != "" {
				out = append(out, ext)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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

func parseXPathExtractor(node *xmlNode) dsl.Extractor {
	return dsl.Extractor{
		Name:       strings.TrimSpace(prop(node, "XPathExtractor.refname")),
		Source:     "body",
		Type:       "xpath",
		Expression: strings.TrimSpace(prop(node, "XPathExtractor.xpathQuery")),
	}
}

func parseXML(data []byte) (*xmlNode, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
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
			node := &xmlNode{Name: t.Name.Local, Attrs: map[string]string{}}
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
	delay := parseInt(firstNonEmpty(prop(node, "ConstantTimer.delay"), prop(node, "RandomTimer.delay")))
	if elementName == "UniformRandomTimer" {
		rangeMs := parseInt(prop(node, "RandomTimer.range"))
		if rangeMs > 0 {
			return delay + rangeMs/2
		}
	}
	return delay
}

func parseResponseAssertions(node *xmlNode) []dsl.Assertion {
	var out []dsl.Assertion
	for _, valueNode := range childProps(node, "") {
		text := strings.TrimSpace(valueNode.Text)
		if text != "" {
			out = append(out, dsl.Assertion{Type: "body_contains", Value: convertVariables(text)})
		}
	}
	if len(out) == 0 {
		for _, child := range node.Children {
			if child.Name == "collectionProp" {
				for _, grand := range child.Children {
					text := strings.TrimSpace(grand.Text)
					if text != "" {
						out = append(out, dsl.Assertion{Type: "body_contains", Value: convertVariables(text)})
					}
				}
			}
		}
	}
	return out
}

func parseSamplerArguments(node *xmlNode) (string, map[string]any) {
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
			params[name] = convertVariables(value)
		}
	}
	if len(params) == 0 {
		return "", nil
	}
	return "", params
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
