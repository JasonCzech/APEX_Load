package devweb

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"gopkg.in/yaml.v3"
)

type ImportRequest struct {
	Name      string         `json:"name,omitempty"`
	ProjectID string         `json:"project_id,omitempty"`
	Content   string         `json:"content"`
	Options   ConvertOptions `json:"options,omitempty"`
}

type ConvertOptions struct {
	BaseURL            string `json:"base_url,omitempty"`
	DefaultThinkTimeMs int    `json:"default_think_time_ms,omitempty"`
}

type ConvertStats struct {
	TotalSteps       int      `json:"total_steps"`
	ConvertedActions int      `json:"converted_actions"`
	HTTPRequests     int      `json:"http_requests"`
	WebSocketActions int      `json:"websocket_actions"`
	GRPCCalls        int      `json:"grpc_calls"`
	ThinkTimes       int      `json:"think_times"`
	Transactions     int      `json:"transactions,omitempty"`
	Extractors       int      `json:"extractors,omitempty"`
	Assertions       int      `json:"assertions,omitempty"`
	DataSources      int      `json:"data_sources,omitempty"`
	Skipped          int      `json:"skipped"`
	UnsupportedCalls []string `json:"unsupported_calls,omitempty"`
}

type ConvertResult struct {
	Script       *dsl.Script        `json:"script"`
	Warnings     []string           `json:"warnings,omitempty"`
	Unsupported  diag.Diagnostics   `json:"unsupported,omitempty"`
	Stats        ConvertStats       `json:"stats"`
	SourceReview *sourceview.Review `json:"source_review,omitempty"`
}

type parsedStep struct {
	Protocol  string
	Action    dsl.Action
	Function  string
	StartLine int
	EndLine   int
}

type convertContext struct {
	files              map[string]string
	sourceFileName     string
	parameterTemplates map[string]string
}

type devwebReviewPattern struct {
	Element   string
	Construct string
	Detail    string
	Regex     *regexp.Regexp
}

var (
	actionBlockStartPattern = regexp.MustCompile("(?s)load\\.(?:initialize|action|finalize)\\s*\\(\\s*[\"'`]([^\"'`]+)[\"'`]\\s*,\\s*(?:async\\s*)?function\\s*\\([^)]*\\)\\s*\\{")
	webRequestPattern       = regexp.MustCompile(`(?s)(?:(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*)?(?:new\s+)?load\.WebRequest\s*\(\s*\{(.*?)\}\s*\)\s*\.\s*(?:sendSync|send)\s*\(`)
	webSocketNewPattern     = regexp.MustCompile(`(?s)(?:const|let|var)?\s*([A-Za-z_$][\w$]*)?\s*=?\s*new\s+load\.WebSocket\s*\(\s*([^)]+)\)`)
	webSocketOpenPattern    = regexp.MustCompile(`(?s)([A-Za-z_$][\w$]*)\.open\s*\(`)
	webSocketSendPattern    = regexp.MustCompile(`(?s)([A-Za-z_$][\w$]*)\.send\s*\(\s*([^)]*)\)`)
	webSocketRecvPattern    = regexp.MustCompile(`(?s)([A-Za-z_$][\w$]*)\.receive\s*\(`)
	webSocketClosePattern   = regexp.MustCompile(`(?s)([A-Za-z_$][\w$]*)\.close\s*\(`)
	grpcInvokePattern       = regexp.MustCompile(`(?s)([A-Za-z_$][\w$]*)\.invoke\s*\(\s*([^)]*)\)`)
	thinkTimePattern        = regexp.MustCompile(`load\.thinkTime\s*\(\s*([0-9]+(?:\.[0-9]+)?)\s*\)`)
	webRequestCallPattern   = regexp.MustCompile(`load\.WebRequest\s*\(`)
	correlationPattern      = regexp.MustCompile(`(?i)\b(?:load\.)?(?:correlat\w*|extract\w*|web_reg_save_param|web\.regSaveParam)\b`)
	textCheckPattern        = regexp.MustCompile("(?s)([A-Za-z_$][\\w$]*)\\.textCheck\\s*\\(\\s*[\"'`]([^\"'`]+)[\"'`]\\s*\\)")
	transactionNewPattern   = regexp.MustCompile("(?s)(?:const|let|var)\\s+([A-Za-z_$][\\w$]*)\\s*=\\s*new\\s+load\\.Transaction\\s*\\(\\s*[\"'`]([^\"'`]+)[\"'`]\\s*\\)")
	reviewOnlyPatterns      = []devwebReviewPattern{
		{
			Element:   "DevWeb dynamic JavaScript",
			Construct: "dynamic JavaScript control flow",
			Detail:    "JavaScript control flow is not executed by the static importer; converted SDK calls are extracted without preserving branch or loop semantics",
			Regex:     regexp.MustCompile(`(?m)\b(?:if|switch|for|while)\s*\(`),
		},
		{
			Element:   "DevWeb custom module",
			Construct: "custom module",
			Detail:    "custom module imports are not executed by the importer; review helper logic and recreate required behavior before replay",
			Regex:     regexp.MustCompile("(?m)\\b(?:const|let|var)\\s+[A-Za-z_$][\\w$]*\\s*=\\s*require\\s*\\(|\\bimport\\s+(?:[^;]+?\\s+from\\s+)?[\"'`]"),
		},
		{
			Element:   "DevWeb async control flow",
			Construct: "async JavaScript control flow",
			Detail:    "Promise/timer control flow is not executed by the importer; verify ordering and timing before promoting the generated script",
			Regex:     regexp.MustCompile(`(?m)\b(?:Promise\.|setTimeout\s*\(|setInterval\s*\(|\.then\s*\(|\.catch\s*\()`),
		},
		{
			Element:   "DevWeb template literal",
			Construct: "dynamic template literal",
			Detail:    "template literals with JavaScript expressions are not evaluated by the importer; replace dynamic values with explicit APEX data or variables",
			Regex:     regexp.MustCompile("`[^`]*\\$\\{[^}]+\\}[^`]*`"),
		},
	}
)

func Convert(req ImportRequest) (*ConvertResult, error) {
	if strings.TrimSpace(req.Content) == "" {
		return nil, fmt.Errorf("content is required")
	}
	return convertSource(firstNonEmpty(req.Name, "Converted_DevWeb_Script"), firstNonEmpty(req.Name, "devweb")+".js", req.Content, req.Options)
}

func ConvertZip(zipData []byte, opts ConvertOptions) (*ConvertResult, error) {
	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, err
	}
	files := make(map[string]string)
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		body, err := readZipFile(file)
		if err != nil {
			return nil, err
		}
		files[filepath.ToSlash(file.Name)] = string(body)
	}
	name, sourceFile, content := selectDevWebMainSource(files)
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("no DevWeb .js file found in archive")
	}
	ctx := convertContext{files: files, sourceFileName: sourceFile}
	return convertSourceWithContext(firstNonEmpty(name, "Converted_DevWeb_Script"), sourceFile, content, opts, ctx)
}

func convertSource(name string, sourceFileName string, content string, opts ConvertOptions) (*ConvertResult, error) {
	return convertSourceWithContext(name, sourceFileName, content, opts, convertContext{sourceFileName: sourceFileName})
}

func convertSourceWithContext(name string, sourceFileName string, content string, opts ConvertOptions, ctx convertContext) (*ConvertResult, error) {
	result := &ConvertResult{
		Script: &dsl.Script{
			Name:          sanitizeName(name),
			Version:       "1.0.0",
			GeneratedBy:   "apexload-devweb-converter",
			GeneratedFrom: "devweb",
			CreatedAt:     time.Now(),
			Config: dsl.ScriptConfig{
				BaseURL: opts.BaseURL,
				ThinkTime: dsl.ThinkTimeConfig{
					Type:   "fixed",
					MeanMs: float64(firstPositive(opts.DefaultThinkTimeMs, 1000)),
				},
				Pacing:        dsl.PacingConfig{Type: "none"},
				RunLogic:      dsl.RunLogicConfig{Iterations: 0, AutoTransactions: "none"},
				ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: false},
				Log:           dsl.LogConfig{Level: "standard"},
			},
		},
	}
	var diagnostics diag.Collector
	sourceMappings := make([]sourceview.Mapping, 0)
	if ctx.sourceFileName == "" {
		ctx.sourceFileName = sourceFileName
	}
	if ctx.files == nil {
		ctx.files = map[string]string{sourceFileName: content}
	}
	if _, ok := ctx.files[sourceFileName]; !ok {
		ctx.files[sourceFileName] = content
	}

	applyDevWebZipMetadata(result, &ctx, &diagnostics)

	blocks := actionBlocks(content)
	if len(blocks) == 0 {
		blocks = []namedBlock{{Name: "DevWebAction", Body: content, StartLine: 1}}
		result.Warnings = append(result.Warnings, "no load.action blocks found; imported top-level DevWeb calls in source order")
	}

	var protocols []string
	for _, block := range blocks {
		steps := parseBlock(block, &result.Stats, &result.Warnings, &diagnostics, ctx)
		for _, step := range steps {
			protocols = append(protocols, step.Protocol)
			actionIndex := len(result.Script.Actions)
			result.Script.Actions = append(result.Script.Actions, step.Action)
			sourceMappings = append(sourceMappings, devwebSourceMapping(sourceFileName, step, fmt.Sprintf("actions[%d]", actionIndex)))
			result.Stats.ConvertedActions++
		}
	}
	if len(result.Script.Actions) == 0 && len(blocks) > 0 {
		diagnostics.Add(diag.Warning, "DevWeb load.action parser", result.Script.Name, "load.action block parsing could not preserve nested JavaScript objects; importing supported calls from full source for review")
		steps := parseBlock(namedBlock{Name: result.Script.Name, Body: content, StartLine: 1}, &result.Stats, &result.Warnings, &diagnostics, ctx)
		for _, step := range steps {
			protocols = append(protocols, step.Protocol)
			actionIndex := len(result.Script.Actions)
			result.Script.Actions = append(result.Script.Actions, step.Action)
			sourceMappings = append(sourceMappings, devwebSourceMapping(sourceFileName, step, fmt.Sprintf("actions[%d]", actionIndex)))
			result.Stats.ConvertedActions++
		}
	}
	if len(result.Script.Actions) == 0 {
		return nil, fmt.Errorf("no supported DevWeb SDK calls found")
	}

	result.Script.Protocol = dominantProtocol(protocols, &result.Warnings)
	if result.Script.Config.BaseURL == "" {
		result.Script.Config.BaseURL = detectBaseURL(result.Script.Actions)
	}
	if result.Script.Protocol == "websocket" {
		result.Script.Config.WebSocket.ReadTimeoutMs = 30000
		result.Script.Config.WebSocket.WriteTimeoutMs = 10000
	}
	if result.Script.Protocol == "grpc" {
		result.Script.Config.GRPC.UseReflection = true
	}
	if gap := result.Stats.TotalSteps - result.Stats.HTTPRequests - result.Stats.WebSocketActions - result.Stats.GRPCCalls - result.Stats.ThinkTimes - result.Stats.Skipped; gap > 0 {
		diagnostics.Add(diag.Warning, "DevWeb completeness guard", result.Script.Name, fmt.Sprintf("%d SDK construct(s) were detected but not converted or explicitly skipped", gap))
	}
	result.Unsupported = diagnostics.Items()
	result.SourceReview = sourceview.Build(ctx.files, sourceMappings)
	return result, nil
}

type namedBlock struct {
	Name      string
	Body      string
	StartLine int
}

func actionBlocks(content string) []namedBlock {
	matches := actionBlockStartPattern.FindAllStringSubmatchIndex(content, -1)
	blocks := make([]namedBlock, 0, len(matches))
	for _, match := range matches {
		name := content[match[2]:match[3]]
		bodyStart := match[1]
		bodyEnd := findMatchingJavaScriptBrace(content, bodyStart-1)
		if bodyEnd <= bodyStart {
			continue
		}
		body := content[bodyStart:bodyEnd]
		blocks = append(blocks, namedBlock{Name: name, Body: body, StartLine: sourceview.LineForOffset(content, bodyStart)})
	}
	return blocks
}

func findMatchingJavaScriptBrace(content string, open int) int {
	if open < 0 || open >= len(content) || content[open] != '{' {
		return -1
	}
	quote := rune(0)
	escaped := false
	depth := 0
	for pos, r := range content[open:] {
		index := open + pos
		if escaped {
			escaped = false
			continue
		}
		if quote != 0 {
			if r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"', '`':
			quote = r
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}

func parseBlock(block namedBlock, stats *ConvertStats, warnings *[]string, diagnostics *diag.Collector, ctx convertContext) []parsedStep {
	var steps []parsedStep
	requestVars := map[string]int{}
	for _, step := range convertedDevWebHelperInvocationActions(block, stats, diagnostics, ctx) {
		steps = append(steps, step)
		if requestVar := stringValue(step.Action.Metadata["devweb_request_variable"]); requestVar != "" {
			requestVars[requestVar] = len(steps) - 1
		}
	}
	webRequestMatches := webRequestPattern.FindAllStringSubmatchIndex(block.Body, -1)
	for _, match := range webRequestMatches {
		stats.TotalSteps++
		requestVar := ""
		if match[2] >= 0 && match[3] >= 0 {
			requestVar = strings.TrimSpace(block.Body[match[2]:match[3]])
		}
		objectLiteral := block.Body[match[4]:match[5]]
		action, ok := parseWebRequest(block.Name, objectLiteral, ctx)
		if !ok {
			stats.Skipped++
			stats.UnsupportedCalls = append(stats.UnsupportedCalls, "load.WebRequest")
			diagnostics.Add(diag.Warning, "DevWeb load.WebRequest", devwebLocation(block, match[0]), "WebRequest object literal could not be parsed; request was not converted")
			continue
		}
		if requestVar != "" {
			if action.Metadata == nil {
				action.Metadata = map[string]any{}
			}
			action.Metadata["devweb_request_variable"] = requestVar
		}
		if txName := activeDevWebTransaction(block.Body[:match[0]]); txName != "" {
			if action.Metadata == nil {
				action.Metadata = map[string]any{}
			}
			action.Metadata["source_transaction"] = txName
			action.Metadata["devweb_transaction"] = txName
			action.Name = sanitizeName(txName + "_" + action.Name)
		}
		stats.Extractors += len(action.Extractors)
		stats.HTTPRequests++
		steps = append(steps, parsedStep{Protocol: "http", Action: action, Function: "load.WebRequest", StartLine: devwebLine(block, match[0]), EndLine: devwebLine(block, match[1])})
		if requestVar != "" {
			requestVars[requestVar] = len(steps) - 1
		}
	}
	if calls := webRequestCallPattern.FindAllStringIndex(block.Body, -1); len(calls) > len(webRequestMatches) {
		for _, call := range calls {
			if webRequestCallCovered(call[0], webRequestMatches) {
				continue
			}
			stats.TotalSteps++
			stats.Skipped++
			stats.UnsupportedCalls = append(stats.UnsupportedCalls, "load.WebRequest")
			diagnostics.Add(diag.Warning, "DevWeb load.WebRequest", devwebLocation(block, call[0]), "WebRequest call does not use a statically parseable object literal; request was not converted")
		}
	}
	for _, match := range correlationPattern.FindAllStringIndex(block.Body, -1) {
		if isDevWebNativeExtractorAt(block.Body, match[0]) {
			continue
		}
		diagnostics.Add(diag.Warning, "DevWeb correlation helper", devwebLocation(block, match[0]), "correlation/extraction helper is not executed by the importer; recreate it with APEX Load extractors before replay")
		lineContent := extractLine(block.Body, match[0])
		steps = append(steps, parsedStep{
			Protocol: "http",
			Action: dsl.Action{
				Name:  actionName(block.Name, "correlation_review"),
				Type:  "review_required",
				Value: strings.TrimSpace(lineContent),
				Metadata: map[string]any{
					"source": "DevWeb correlation helper",
				},
			},
			Function:  "correlation",
			StartLine: devwebLine(block, match[0]),
			EndLine:   devwebLine(block, match[0]),
		})
	}
	for _, match := range textCheckPattern.FindAllStringSubmatchIndex(block.Body, -1) {
		if len(match) < 6 || match[2] < 0 || match[4] < 0 {
			continue
		}
		requestVar := block.Body[match[2]:match[3]]
		text := block.Body[match[4]:match[5]]
		index, ok := requestVars[requestVar]
		if !ok || index < 0 || index >= len(steps) {
			diagnostics.Add(diag.Warning, "DevWeb textCheck", devwebLocation(block, match[0]), "textCheck target "+requestVar+" was not associated with a converted WebRequest")
			continue
		}
		steps[index].Action.Assertions = append(steps[index].Action.Assertions, dsl.Assertion{
			Type:  "body_contains",
			Value: convertDevWebTemplate(text, ctx.parameterTemplates),
		})
		stats.Assertions++
	}
	for _, match := range transactionNewPattern.FindAllStringSubmatchIndex(block.Body, -1) {
		stats.Transactions++
		if len(match) >= 6 && match[4] >= 0 {
			txName := block.Body[match[4]:match[5]]
			diagnostics.Add(diag.Info, "DevWeb transaction", devwebLocation(block, match[0]), "transaction "+txName+" was preserved as generated action metadata")
		}
	}
	diagnoseReviewOnlyJavaScript(block, stats, diagnostics, &steps, ctx)

	socketVars := map[string]string{}
	for _, match := range webSocketNewPattern.FindAllStringSubmatch(block.Body, -1) {
		stats.TotalSteps++
		name := strings.TrimSpace(match[1])
		if name == "" {
			name = "ws"
		}
		target := jsString(match[2])
		socketVars[name] = target
		stats.WebSocketActions++
		steps = append(steps, parsedStep{Protocol: "websocket", Action: dsl.Action{
			Name:   actionName(block.Name, "connect"),
			Type:   "ws_connect",
			Target: target,
		}, Function: "load.WebSocket", StartLine: block.StartLine, EndLine: block.StartLine})
	}
	for _, match := range webSocketOpenPattern.FindAllStringSubmatch(block.Body, -1) {
		if _, exists := socketVars[match[1]]; exists {
			continue
		}
		stats.TotalSteps++
		stats.Skipped++
		stats.UnsupportedCalls = append(stats.UnsupportedCalls, match[1]+".open")
		*warnings = append(*warnings, "skipped WebSocket open without constructor URL for "+match[1])
		diagnostics.Add(diag.Warning, "DevWeb WebSocket open", block.Name, "WebSocket open without constructor URL for "+match[1]+" was skipped")
	}
	for _, match := range webSocketSendPattern.FindAllStringSubmatch(block.Body, -1) {
		if _, exists := socketVars[match[1]]; !exists {
			continue
		}
		stats.TotalSteps++
		stats.WebSocketActions++
		steps = append(steps, parsedStep{Protocol: "websocket", Action: dsl.Action{
			Name: actionName(block.Name, "send"),
			Type: "ws_send",
			Body: jsString(match[2]),
		}, Function: "WebSocket.send", StartLine: block.StartLine, EndLine: block.StartLine})
	}
	for _, match := range webSocketRecvPattern.FindAllStringSubmatch(block.Body, -1) {
		if _, exists := socketVars[match[1]]; !exists {
			continue
		}
		stats.TotalSteps++
		stats.WebSocketActions++
		steps = append(steps, parsedStep{Protocol: "websocket", Action: dsl.Action{
			Name:   actionName(block.Name, "receive"),
			Type:   "ws_receive",
			Params: map[string]any{"timeout_ms": 30000},
		}, Function: "WebSocket.receive", StartLine: block.StartLine, EndLine: block.StartLine})
	}
	for _, match := range webSocketClosePattern.FindAllStringSubmatch(block.Body, -1) {
		if _, exists := socketVars[match[1]]; !exists {
			continue
		}
		stats.TotalSteps++
		stats.WebSocketActions++
		steps = append(steps, parsedStep{Protocol: "websocket", Action: dsl.Action{
			Name: actionName(block.Name, "close"),
			Type: "ws_close",
		}, Function: "WebSocket.close", StartLine: block.StartLine, EndLine: block.StartLine})
	}
	for _, match := range grpcInvokePattern.FindAllStringSubmatch(block.Body, -1) {
		stats.TotalSteps++
		args := splitArgs(match[2])
		if len(args) < 2 {
			stats.Skipped++
			stats.UnsupportedCalls = append(stats.UnsupportedCalls, match[1]+".invoke")
			diagnostics.Add(diag.Warning, "DevWeb gRPC invoke", block.Name, "gRPC invoke without service/method arguments was skipped")
			continue
		}
		target := grpcTarget(args)
		body := "{}"
		if len(args) >= 3 {
			body = jsObjectLiteral(args[2])
		}
		stats.GRPCCalls++
		diagnostics.Add(diag.Warning, "DevWeb gRPC invoke", block.Name, "gRPC invoke was partially converted; proto files, metadata, and payload schema require review")
		steps = append(steps, parsedStep{Protocol: "grpc", Action: dsl.Action{
			Name:   actionName(block.Name, "grpc"),
			Type:   "grpc_unary",
			Target: target,
			Body:   body,
		}, Function: "grpc.invoke", StartLine: block.StartLine, EndLine: block.StartLine})
	}
	for _, match := range thinkTimePattern.FindAllStringSubmatch(block.Body, -1) {
		stats.TotalSteps++
		stats.ThinkTimes++
		if len(steps) > 0 {
			ms := secondsToMilliseconds(match[1])
			steps[len(steps)-1].Action.ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: ms}
			steps[len(steps)-1].Action.ThinkTimeAfter = true
		}
	}
	// Sort steps by start line to maintain source execution order across different protocols/patterns
	sortParsedSteps(steps)
	return steps
}

func diagnoseReviewOnlyJavaScript(block namedBlock, stats *ConvertStats, diagnostics *diag.Collector, steps *[]parsedStep, ctx convertContext) {
	for _, pattern := range reviewOnlyPatterns {
		for _, match := range pattern.Regex.FindAllStringIndex(block.Body, -1) {
			if pattern.Construct == "dynamic template literal" && isSafeDevWebTemplateLiteral(extractLine(block.Body, match[0]), ctx.parameterTemplates) {
				continue
			}
			stats.UnsupportedCalls = appendDevWebUnsupportedCall(stats.UnsupportedCalls, pattern.Construct)
			diagnostics.Add(diag.Warning, pattern.Element, devwebLocation(block, match[0]), pattern.Detail)
			lineContent := extractLine(block.Body, match[0])
			*steps = append(*steps, parsedStep{
				Protocol: "http",
				Action: dsl.Action{
					Name:  actionName(block.Name, "js_review"),
					Type:  "review_required",
					Value: strings.TrimSpace(lineContent),
					Metadata: map[string]any{
						"source": pattern.Construct,
					},
				},
				Function:  pattern.Construct,
				StartLine: devwebLine(block, match[0]),
				EndLine:   devwebLine(block, match[0]),
			})
		}
	}
}

func appendDevWebUnsupportedCall(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func webRequestCallCovered(offset int, matches [][]int) bool {
	for _, match := range matches {
		if len(match) >= 2 && offset >= match[0] && offset < match[1] {
			return true
		}
	}
	return false
}

func devwebLocation(block namedBlock, offset int) string {
	line := devwebLine(block, offset)
	return fmt.Sprintf("%s line %d", firstNonEmpty(block.Name, "DevWebAction"), line)
}

func devwebLine(block namedBlock, offset int) int {
	start := block.StartLine
	if start <= 0 {
		start = 1
	}
	if offset <= 0 {
		return start
	}
	if offset > len(block.Body) {
		offset = len(block.Body)
	}
	return start + strings.Count(block.Body[:offset], "\n")
}

func extractLine(content string, offset int) string {
	if offset < 0 || offset >= len(content) {
		return ""
	}
	start := strings.LastIndex(content[:offset], "\n")
	if start == -1 {
		start = 0
	} else {
		start++ // skip the newline
	}
	end := strings.Index(content[offset:], "\n")
	if end == -1 {
		end = len(content)
	} else {
		end += offset
	}
	return content[start:end]
}

func sortParsedSteps(steps []parsedStep) {
	for i := 0; i < len(steps); i++ {
		for j := i + 1; j < len(steps); j++ {
			if steps[i].StartLine > steps[j].StartLine {
				steps[i], steps[j] = steps[j], steps[i]
			}
		}
	}
}

func devwebSourceMapping(sourceFile string, step parsedStep, targetPath string) sourceview.Mapping {
	startLine := step.StartLine
	if startLine <= 0 {
		startLine = 1
	}
	endLine := step.EndLine
	if endLine < startLine {
		endLine = startLine
	}
	return sourceview.Mapping{
		SourceFile: sourceFile,
		StartLine:  startLine,
		EndLine:    endLine,
		Function:   firstNonEmpty(step.Function, step.Action.Type),
		TargetPath: targetPath,
		TargetName: step.Action.Name,
		TargetKind: "action",
		Status:     "converted",
		Notes:      "DevWeb SDK construct was converted into generated APEX Load DSL.",
	}
}

func parseWebRequest(blockName string, objectLiteral string, ctx convertContext) (dsl.Action, bool) {
	rawURL := objectStringField(objectLiteral, "url", ctx)
	if rawURL == "" {
		rawURL = objectStringField(objectLiteral, "URL", ctx)
	}
	if rawURL == "" {
		return dsl.Action{}, false
	}
	method := strings.ToUpper(firstNonEmpty(objectStringField(objectLiteral, "method", ctx), "GET"))
	body := objectStringField(objectLiteral, "body", ctx)
	if body == "" {
		body = objectStringField(objectLiteral, "bodyText", ctx)
	}
	headers := objectStringMapField(objectLiteral, "headers", ctx)
	query := objectStringMapField(objectLiteral, "queryString", ctx)
	if containsTemplateExpression(rawURL) || containsTemplateExpression(method) || containsTemplateExpression(body) || stringMapContainsTemplateExpression(headers) || stringMapContainsTemplateExpression(query) {
		return dsl.Action{}, false
	}
	if len(query) > 0 {
		rawURL = appendQueryString(rawURL, query)
	}
	action := dsl.Action{
		Name:    actionName(blockName, method),
		Type:    "request",
		Method:  method,
		Target:  rawURL,
		Headers: headers,
		Body:    body,
	}
	if len(query) > 0 {
		action.Metadata = map[string]any{"devweb_query_string": query}
	}
	action.Extractors = parseDevWebExtractors(objectLiteral)
	return action, true
}

func containsTemplateExpression(value string) bool {
	return strings.Contains(value, "${")
}

func stringMapContainsTemplateExpression(values map[string]string) bool {
	for _, value := range values {
		if containsTemplateExpression(value) {
			return true
		}
	}
	return false
}

func dominantProtocol(protocols []string, warnings *[]string) string {
	counts := map[string]int{}
	for _, protocol := range protocols {
		counts[protocol]++
	}
	if len(counts) == 1 {
		for protocol := range counts {
			return protocol
		}
	}
	*warnings = append(*warnings, "mixed-protocol DevWeb scripts are imported into one script for review; split HTTP, WebSocket, and gRPC actions before execution")
	if counts["http"] > 0 {
		return "http"
	}
	if counts["websocket"] > 0 {
		return "websocket"
	}
	return "grpc"
}

func detectBaseURL(actions []dsl.Action) string {
	for _, action := range actions {
		parsed, err := url.Parse(action.Target)
		if err == nil && parsed.Scheme != "" && parsed.Host != "" {
			return parsed.Scheme + "://" + parsed.Host
		}
	}
	return ""
}

func objectStringField(objectLiteral string, field string, ctx convertContext) string {
	raw, ok := objectFieldRaw(objectLiteral, field)
	if !ok {
		return ""
	}
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "{") {
		return jsObjectToJSON(raw, ctx.parameterTemplates)
	}
	return jsStringValue(raw, ctx.parameterTemplates)
}

func objectStringMapField(objectLiteral string, field string, ctx convertContext) map[string]string {
	raw, ok := objectFieldRaw(objectLiteral, field)
	if !ok {
		return nil
	}
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "{") || !strings.HasSuffix(raw, "}") {
		return nil
	}
	entries := objectLiteralEntries(strings.TrimSpace(raw[1 : len(raw)-1]))
	values := make(map[string]string, len(entries))
	for key, rawValue := range entries {
		values[key] = jsStringValue(rawValue, ctx.parameterTemplates)
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

func selectDevWebMainSource(files map[string]string) (string, string, string) {
	if len(files) == 0 {
		return "", "", ""
	}
	names := make([]string, 0, len(files))
	for name := range files {
		if strings.EqualFold(filepath.Ext(name), ".js") {
			names = append(names, name)
		}
	}
	sortStrings(names)
	for _, name := range names {
		if strings.EqualFold(filepath.Base(name), "main.js") {
			return devWebScriptNameFromFiles(files, name), name, files[name]
		}
	}
	if len(names) == 0 {
		return "", "", ""
	}
	name := names[0]
	return devWebScriptNameFromFiles(files, name), name, files[name]
}

func devWebScriptNameFromFiles(files map[string]string, sourceFile string) string {
	for name, content := range files {
		if !strings.EqualFold(filepath.Ext(name), ".usr") {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
		if strings.TrimSpace(base) != "" {
			return base
		}
		if parsed := devWebUSRScriptName(content); parsed != "" {
			return parsed
		}
	}
	return strings.TrimSuffix(filepath.Base(sourceFile), filepath.Ext(sourceFile))
}

func devWebUSRScriptName(content string) string {
	for _, line := range strings.Split(content, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(key), "ScriptName") || strings.EqualFold(strings.TrimSpace(key), "Name") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

type devWebParametersFile struct {
	Parameters []devWebParameter `yaml:"parameters"`
}

type devWebParameter struct {
	Name       string `yaml:"name"`
	Type       string `yaml:"type"`
	FileName   string `yaml:"fileName"`
	ColumnName string `yaml:"columnName"`
	NextRow    string `yaml:"nextRow"`
	NextValue  string `yaml:"nextValue"`
	OnEnd      string `yaml:"onEnd"`
}

type devWebHelperRequestTemplate struct {
	Module           string   `json:"module"`
	Function         string   `json:"function,omitempty"`
	Method           string   `json:"method"`
	URL              string   `json:"url,omitempty"`
	QueryKeys        []string `json:"query_keys,omitempty"`
	HeaderKeys       []string `json:"header_keys,omitempty"`
	HasBody          bool     `json:"has_body,omitempty"`
	StaticResolvable bool     `json:"static_resolvable"`
}

type devWebHelperInvocation struct {
	Module              string   `json:"module,omitempty"`
	Object              string   `json:"object"`
	Function            string   `json:"function"`
	AssignedTo          string   `json:"assigned_to,omitempty"`
	ArgumentCount       int      `json:"argument_count"`
	Sent                bool     `json:"sent"`
	ExtractorNames      []string `json:"extractor_names,omitempty"`
	TemplateInventoried bool     `json:"template_inventoried"`
}

func applyDevWebZipMetadata(result *ConvertResult, ctx *convertContext, diagnostics *diag.Collector) {
	if result == nil || result.Script == nil || ctx == nil || len(ctx.files) == 0 {
		return
	}
	preserved := ensureDevWebImportPreserved(result.Script)
	helperModules := make([]string, 0)
	for name := range ctx.files {
		if strings.EqualFold(filepath.Ext(name), ".js") && !strings.EqualFold(filepath.Base(name), "main.js") && name != ctx.sourceFileName {
			helperModules = append(helperModules, name)
		}
	}
	sortStrings(helperModules)
	if len(helperModules) > 0 {
		preserved["helper_modules"] = helperModules
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "DevWeb helper module", strings.Join(helperModules, ", "), "helper JavaScript modules were preserved for review; only statically visible WebRequest templates are converted")
		}
	}
	ctx.parameterTemplates = map[string]string{}
	applyDevWebParameters(result, ctx, diagnostics)
	if len(helperModules) > 0 {
		if templates := devWebHelperRequestTemplates(ctx.files, helperModules, *ctx); len(templates) > 0 {
			preserved["helper_module_request_templates"] = templates
			if diagnostics != nil {
				diagnostics.Add(diag.Warning, "DevWeb helper module request template", strings.Join(helperModules, ", "), fmt.Sprintf("%d helper WebRequest template(s) were inventoried for review; helper calls remain review-gated until arguments are resolved", len(templates)))
			}
		}
	}
	if invocations := devWebHelperInvocations(ctx.files, ctx.sourceFileName, helperModules); len(invocations) > 0 {
		preserved["helper_module_invocations"] = invocations
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "DevWeb helper module invocation", ctx.sourceFileName, fmt.Sprintf("%d helper module invocation(s) were inventoried for review; helper calls remain review-gated until the generated request template is reconciled", len(invocations)))
		}
	}
	applyDevWebRTS(result, ctx, diagnostics)
}

func devWebHelperRequestTemplates(files map[string]string, helperModules []string, ctx convertContext) []devWebHelperRequestTemplate {
	templates := make([]devWebHelperRequestTemplate, 0)
	for _, module := range helperModules {
		content := files[module]
		if strings.TrimSpace(content) == "" {
			continue
		}
		templates = append(templates, devWebHelperRequestTemplatesForModule(module, content, ctx)...)
	}
	return templates
}

func devWebHelperRequestTemplatesForModule(module string, content string, ctx convertContext) []devWebHelperRequestTemplate {
	requestPattern := regexp.MustCompile(`(?s)new\s+load\.WebRequest\s*\(\s*\{`)
	matches := requestPattern.FindAllStringIndex(content, -1)
	if len(matches) == 0 {
		return nil
	}
	functions := devWebFunctionRanges(content)
	templates := make([]devWebHelperRequestTemplate, 0, len(matches))
	for _, match := range matches {
		openBrace := match[1] - 1
		closeBrace := findMatchingJavaScriptBrace(content, openBrace)
		if closeBrace <= openBrace {
			continue
		}
		objectLiteral := content[openBrace+1 : closeBrace]
		functionName := devWebFunctionForOffset(functions, match[0])
		query := objectStringMapField(objectLiteral, "queryString", ctx)
		headers := objectStringMapField(objectLiteral, "headers", ctx)
		body, hasBody := objectFieldRaw(objectLiteral, "body")
		url := firstNonEmpty(objectStringField(objectLiteral, "url", ctx), objectStringField(objectLiteral, "URL", ctx))
		method := strings.ToUpper(firstNonEmpty(objectStringField(objectLiteral, "method", ctx), "GET"))
		template := devWebHelperRequestTemplate{
			Module:           module,
			Function:         functionName,
			Method:           method,
			URL:              url,
			QueryKeys:        sortedMapKeys(query),
			HeaderKeys:       sortedMapKeys(headers),
			HasBody:          strings.TrimSpace(body) != "" || hasBody,
			StaticResolvable: devWebHelperTemplateStatic(url, method, query, headers, body),
		}
		templates = append(templates, template)
	}
	return templates
}

func devWebHelperInvocations(files map[string]string, sourceFileName string, helperModules []string) []devWebHelperInvocation {
	content := files[sourceFileName]
	if strings.TrimSpace(content) == "" {
		return nil
	}
	aliases := devWebHelperAliases(content, sourceFileName, helperModules)
	if len(aliases) == 0 {
		return nil
	}
	invocations := make([]devWebHelperInvocation, 0)
	callPattern := devWebHelperCallPattern()
	for _, match := range callPattern.FindAllStringSubmatch(content, -1) {
		if len(match) < 6 {
			continue
		}
		objectName := strings.TrimSpace(match[2])
		module := aliases[objectName]
		if module == "" {
			continue
		}
		assignedTo := strings.TrimSpace(match[1])
		functionName := strings.TrimSpace(match[3])
		sent := strings.TrimSpace(match[5]) != "" || devWebHelperInvocationSent(content, assignedTo)
		invocation := devWebHelperInvocation{
			Module:              module,
			Object:              objectName,
			Function:            functionName,
			AssignedTo:          assignedTo,
			ArgumentCount:       len(splitTopLevel(strings.TrimSpace(match[4]), ',')),
			Sent:                sent,
			ExtractorNames:      devWebHelperInvocationExtractors(content, assignedTo),
			TemplateInventoried: devWebHelperModuleHasFunction(files[module], functionName),
		}
		invocations = append(invocations, invocation)
	}
	return invocations
}

func convertedDevWebHelperInvocationActions(block namedBlock, stats *ConvertStats, diagnostics *diag.Collector, ctx convertContext) []parsedStep {
	if len(ctx.files) == 0 || strings.TrimSpace(ctx.sourceFileName) == "" {
		return nil
	}
	helperModules := devWebHelperModules(ctx.files, ctx.sourceFileName)
	if len(helperModules) == 0 {
		return nil
	}
	aliases := devWebHelperAliases(ctx.files[ctx.sourceFileName], ctx.sourceFileName, helperModules)
	if len(aliases) == 0 {
		return nil
	}
	definitions := devWebHelperRequestDefinitions(ctx.files, helperModules)
	if len(definitions) == 0 {
		return nil
	}
	localValues := devWebBlockConstantValues(block.Body, ctx)
	callPattern := devWebHelperCallPattern()
	var steps []parsedStep
	for _, match := range callPattern.FindAllStringSubmatchIndex(block.Body, -1) {
		if len(match) < 12 || match[4] < 0 || match[6] < 0 || match[8] < 0 {
			continue
		}
		objectName := strings.TrimSpace(block.Body[match[4]:match[5]])
		module := aliases[objectName]
		if module == "" {
			continue
		}
		assignedTo := ""
		if match[2] >= 0 && match[3] >= 0 {
			assignedTo = strings.TrimSpace(block.Body[match[2]:match[3]])
		}
		directSend := match[10] >= 0 && match[11] >= 0
		if !directSend && !devWebHelperInvocationSent(block.Body, assignedTo) {
			continue
		}
		functionName := strings.TrimSpace(block.Body[match[6]:match[7]])
		definition, ok := definitions[module+"::"+functionName]
		if !ok {
			continue
		}
		localCtx, ok := devWebHelperInvocationContext(ctx, definition.Params, block.Body[match[8]:match[9]], localValues)
		if !ok {
			continue
		}
		action, ok := parseWebRequest(block.Name, definition.ObjectLiteral, localCtx)
		if !ok || containsTemplateExpression(action.Target) || containsTemplateExpression(action.Body) || stringMapContainsTemplateExpression(action.Headers) {
			continue
		}
		if action.Metadata == nil {
			action.Metadata = map[string]any{}
		}
		action.Metadata["devweb_helper_module"] = definition.Module
		action.Metadata["devweb_helper_function"] = definition.Function
		action.Metadata["devweb_helper_object"] = objectName
		if assignedTo != "" {
			action.Metadata["devweb_request_variable"] = assignedTo
		}
		action.Extractors = append(action.Extractors, devWebHelperInvocationExtractorsFull(block.Body, assignedTo)...)
		stats.TotalSteps++
		stats.HTTPRequests++
		stats.Extractors += len(action.Extractors)
		if diagnostics != nil {
			diagnostics.Add(diag.Info, "DevWeb helper module request", devwebLocation(block, match[0]), fmt.Sprintf("%s.%s was mapped to a native HTTP action", objectName, functionName))
		}
		steps = append(steps, parsedStep{Protocol: "http", Action: action, Function: "load.WebRequest(helper)", StartLine: devwebLine(block, match[0]), EndLine: devwebLine(block, match[1])})
	}
	return steps
}

type devWebHelperRequestDefinition struct {
	Module        string
	Function      string
	Params        []string
	ObjectLiteral string
}

func devWebHelperModules(files map[string]string, sourceFileName string) []string {
	helperModules := make([]string, 0)
	for name := range files {
		if strings.EqualFold(filepath.Ext(name), ".js") && !strings.EqualFold(filepath.Base(name), "main.js") && name != sourceFileName {
			helperModules = append(helperModules, name)
		}
	}
	sortStrings(helperModules)
	return helperModules
}

func devWebHelperRequestDefinitions(files map[string]string, helperModules []string) map[string]devWebHelperRequestDefinition {
	definitions := map[string]devWebHelperRequestDefinition{}
	requestPattern := regexp.MustCompile(`(?s)new\s+load\.WebRequest\s*\(\s*\{`)
	for _, module := range helperModules {
		content := files[module]
		for _, function := range devWebFunctionRanges(content) {
			body := content[function.Start:function.End]
			match := requestPattern.FindStringIndex(body)
			if len(match) == 0 {
				continue
			}
			openBrace := function.Start + match[1] - 1
			closeBrace := findMatchingJavaScriptBrace(content, openBrace)
			if closeBrace <= openBrace {
				continue
			}
			definitions[module+"::"+function.Name] = devWebHelperRequestDefinition{
				Module:        module,
				Function:      function.Name,
				Params:        function.Params,
				ObjectLiteral: content[openBrace+1 : closeBrace],
			}
		}
	}
	return definitions
}

func devWebHelperCallPattern() *regexp.Regexp {
	return regexp.MustCompile(`(?s)(?:(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*)?([A-Za-z_$][\w$]*)\.([A-Za-z_$][\w$]*)\s*\((.*?)\)\s*(?:\.\s*(send|sendSync)\s*\(\s*\))?\s*;`)
}

func devWebHelperInvocationContext(ctx convertContext, params []string, rawArgs string, localValues map[string]string) (convertContext, bool) {
	args := splitTopLevel(strings.TrimSpace(rawArgs), ',')
	if len(args) != len(params) {
		return ctx, false
	}
	local := ctx
	local.parameterTemplates = map[string]string{}
	for key, value := range ctx.parameterTemplates {
		local.parameterTemplates[key] = value
	}
	for i, param := range params {
		value, ok := devWebHelperArgumentValue(args[i], ctx, localValues)
		if !ok {
			return ctx, false
		}
		local.parameterTemplates[param] = value
	}
	return local, true
}

func devWebHelperArgumentValue(raw string, ctx convertContext, localValues map[string]string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if regexp.MustCompile(`^[A-Za-z_$][\w$]*$`).MatchString(raw) && localValues != nil {
		if value, ok := localValues[raw]; ok {
			return value, true
		}
	}
	return devWebStaticArgumentValue(raw, ctx)
}

func devWebStaticArgumentValue(raw string, ctx convertContext) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if strings.HasPrefix(raw, "load.params.") {
		name := strings.TrimSpace(strings.TrimPrefix(raw, "load.params."))
		if ctx.parameterTemplates != nil && ctx.parameterTemplates[name] != "" {
			return ctx.parameterTemplates[name], true
		}
		return "{{var." + name + "}}", true
	}
	if regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`).MatchString(raw) {
		return raw, true
	}
	if strings.HasPrefix(raw, `"`) || strings.HasPrefix(raw, `'`) || strings.HasPrefix(raw, "`") {
		value := jsStringValue(raw, ctx.parameterTemplates)
		return value, !containsTemplateExpression(value)
	}
	return "", false
}

func devWebBlockConstantValues(content string, ctx convertContext) map[string]string {
	pattern := regexp.MustCompile(`(?m)\b(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*([^;\n]+)\s*;`)
	values := map[string]string{}
	for _, match := range pattern.FindAllStringSubmatch(content, -1) {
		if len(match) < 3 {
			continue
		}
		value, ok := devWebStaticArgumentValue(match[2], ctx)
		if !ok || containsTemplateExpression(value) {
			continue
		}
		values[strings.TrimSpace(match[1])] = value
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

func devWebHelperAliases(content string, sourceFileName string, helperModules []string) map[string]string {
	aliases := map[string]string{}
	pattern := regexp.MustCompile(`(?m)(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*require\s*\(\s*["']([^"']+)["']\s*\)\s*(?:\(\s*\))?`)
	for _, match := range pattern.FindAllStringSubmatch(content, -1) {
		if len(match) < 3 {
			continue
		}
		module := resolveDevWebHelperModule(sourceFileName, match[2], helperModules)
		if module != "" {
			aliases[match[1]] = module
		}
	}
	return aliases
}

func resolveDevWebHelperModule(sourceFileName string, required string, helperModules []string) string {
	required = strings.TrimSpace(strings.TrimSuffix(required, ".js"))
	if required == "" {
		return ""
	}
	sourceDir := filepath.ToSlash(filepath.Dir(sourceFileName))
	candidates := []string{
		filepath.ToSlash(filepath.Join(sourceDir, required+".js")),
		filepath.ToSlash(filepath.Join(sourceDir, required)),
		strings.TrimPrefix(required+".js", "./"),
		strings.TrimPrefix(required, "./"),
		filepath.Base(required) + ".js",
		filepath.Base(required),
	}
	for _, helper := range helperModules {
		normalized := filepath.ToSlash(helper)
		base := filepath.Base(normalized)
		for _, candidate := range candidates {
			candidate = filepath.ToSlash(candidate)
			if strings.EqualFold(normalized, candidate) || strings.EqualFold(base, filepath.Base(candidate)) {
				return helper
			}
		}
	}
	return ""
}

func devWebHelperInvocationSent(content string, assignedTo string) bool {
	if strings.TrimSpace(assignedTo) == "" {
		return false
	}
	pattern := regexp.MustCompile(regexp.QuoteMeta(assignedTo) + `\s*\.\s*(?:send|sendSync)\s*\(`)
	return pattern.MatchString(content)
}

func devWebHelperInvocationExtractors(content string, assignedTo string) []string {
	if strings.TrimSpace(assignedTo) == "" {
		return nil
	}
	pattern := regexp.MustCompile(regexp.QuoteMeta(assignedTo) + `\s*\.\s*extractors\s*\.\s*push\s*\(\s*new\s+load\.(?:JsonPathExtractor|BoundaryExtractor)\s*\(\s*["']([^"']+)["']`)
	names := make([]string, 0)
	for _, match := range pattern.FindAllStringSubmatch(content, -1) {
		if len(match) >= 2 {
			names = append(names, match[1])
		}
	}
	sortStrings(names)
	if len(names) == 0 {
		return nil
	}
	return names
}

func devWebHelperInvocationExtractorsFull(content string, assignedTo string) []dsl.Extractor {
	if strings.TrimSpace(assignedTo) == "" {
		return nil
	}
	var extractors []dsl.Extractor
	boundaryPattern := regexp.MustCompile(regexp.QuoteMeta(assignedTo) + `\s*\.\s*extractors\s*\.\s*push\s*\(\s*new\s+load\.BoundaryExtractor\s*\(\s*["']([^"']+)["']\s*,\s*["']([^"']*)["']\s*,\s*["']([^"']*)["']`)
	for _, match := range boundaryPattern.FindAllStringSubmatch(content, -1) {
		if len(match) < 4 {
			continue
		}
		extractors = append(extractors, dsl.Extractor{
			Name:       match[1],
			Source:     "body",
			Type:       "boundary",
			Expression: match[2] + "|||" + match[3],
		})
	}
	jsonPathPattern := regexp.MustCompile(regexp.QuoteMeta(assignedTo) + `\s*\.\s*extractors\s*\.\s*push\s*\(\s*new\s+load\.JsonPathExtractor\s*\(\s*["']([^"']+)["']\s*,\s*["']([^"']*)["']`)
	for _, match := range jsonPathPattern.FindAllStringSubmatch(content, -1) {
		if len(match) < 3 {
			continue
		}
		extractors = append(extractors, dsl.Extractor{
			Name:       match[1],
			Source:     "body",
			Type:       "jsonpath",
			Expression: match[2],
		})
	}
	return extractors
}

func devWebHelperModuleHasFunction(content string, functionName string) bool {
	if strings.TrimSpace(content) == "" || strings.TrimSpace(functionName) == "" {
		return false
	}
	for _, function := range devWebFunctionRanges(content) {
		if function.Name == functionName {
			return true
		}
	}
	return false
}

type devWebFunctionRange struct {
	Name   string
	Params []string
	Start  int
	End    int
}

func devWebFunctionRanges(content string) []devWebFunctionRange {
	ranges := make([]devWebFunctionRange, 0)
	seen := map[string]struct{}{}
	appendMatches := func(pattern *regexp.Regexp, nameGroup int, paramsGroup int) {
		for _, match := range pattern.FindAllStringSubmatchIndex(content, -1) {
			if len(match) <= paramsGroup*2+1 || match[nameGroup*2] < 0 || match[paramsGroup*2] < 0 {
				continue
			}
			name := content[match[nameGroup*2]:match[nameGroup*2+1]]
			if !isDevWebFunctionRangeName(name) {
				continue
			}
			openBrace := match[1] - 1
			closeBrace := findMatchingJavaScriptBrace(content, openBrace)
			if closeBrace <= openBrace {
				continue
			}
			key := fmt.Sprintf("%s:%d:%d", name, match[0], closeBrace)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			ranges = append(ranges, devWebFunctionRange{
				Name:   name,
				Params: devWebFunctionParams(content[match[paramsGroup*2]:match[paramsGroup*2+1]]),
				Start:  match[0],
				End:    closeBrace,
			})
		}
	}
	appendMatches(regexp.MustCompile(`(?s)function\s+([A-Za-z_$][\w$]*)\s*\(([^)]*)\)\s*\{`), 1, 2)
	appendMatches(regexp.MustCompile(`(?s)(?:^|[,{]\s*)([A-Za-z_$][\w$]*)\s*\(([^)]*)\)\s*\{`), 1, 2)
	appendMatches(regexp.MustCompile(`(?s)(?:^|[,{]\s*)([A-Za-z_$][\w$]*)\s*:\s*function\s*\(([^)]*)\)\s*\{`), 1, 2)
	return ranges
}

func isDevWebFunctionRangeName(name string) bool {
	switch strings.TrimSpace(name) {
	case "", "if", "for", "while", "switch", "catch", "function":
		return false
	default:
		return true
	}
}

func devWebFunctionParams(raw string) []string {
	parts := splitTopLevel(raw, ',')
	params := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if regexp.MustCompile(`^[A-Za-z_$][\w$]*$`).MatchString(name) {
			params = append(params, name)
		}
	}
	return params
}

func devWebFunctionForOffset(functions []devWebFunctionRange, offset int) string {
	for _, function := range functions {
		if offset >= function.Start && offset <= function.End {
			return function.Name
		}
	}
	return ""
}

func devWebHelperTemplateStatic(url string, method string, query map[string]string, headers map[string]string, body string) bool {
	if containsTemplateExpression(url) || containsTemplateExpression(method) || containsTemplateExpression(body) || stringMapContainsTemplateExpression(query) || stringMapContainsTemplateExpression(headers) {
		return false
	}
	for _, value := range append([]string{url, method, body}, mapValues(query, headers)...) {
		if strings.Contains(value, "this.") || strings.Contains(value, "=>") {
			return false
		}
	}
	return strings.TrimSpace(url) != ""
}

func mapValues(maps ...map[string]string) []string {
	values := make([]string, 0)
	for _, items := range maps {
		for _, value := range items {
			values = append(values, value)
		}
	}
	return values
}

func sortedMapKeys(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sortStrings(keys)
	return keys
}

func ensureDevWebImportPreserved(script *dsl.Script) map[string]any {
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{Source: "devweb"}
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = map[string]any{}
	}
	return script.Metadata.Import.Preserved
}

func applyDevWebParameters(result *ConvertResult, ctx *convertContext, diagnostics *diag.Collector) {
	parametersPath, content := findDevWebFile(ctx.files, "parameters.yml")
	if strings.TrimSpace(content) == "" {
		return
	}
	var parsed devWebParametersFile
	if err := yaml.Unmarshal([]byte(content), &parsed); err != nil {
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "DevWeb parameters.yml", parametersPath, "parameters.yml could not be parsed: "+err.Error())
		}
		return
	}
	preserved := ensureDevWebImportPreserved(result.Script)
	preserved["parameters_file"] = parametersPath
	dsByFile := map[string]*dsl.DataSource{}
	for _, param := range parsed.Parameters {
		if !strings.EqualFold(param.Type, "csv") || strings.TrimSpace(param.FileName) == "" {
			continue
		}
		sourceName := sanitizeName(strings.TrimSuffix(filepath.Base(param.FileName), filepath.Ext(param.FileName)))
		ds := dsByFile[param.FileName]
		if ds == nil {
			inline := devWebInlineFile(ctx.files, param.FileName)
			dataSource := dsl.DataSource{
				Name:           sourceName,
				Type:           "csv",
				Path:           param.FileName,
				InlineData:     inline,
				Strategy:       devWebCSVStrategy(param.NextRow),
				Delimiter:      ",",
				FirstRowHeader: true,
			}
			dsByFile[param.FileName] = &dataSource
			result.Script.DataSources = append(result.Script.DataSources, dataSource)
			result.Stats.DataSources++
		}
		column := firstNonEmpty(param.ColumnName, param.Name)
		if column != "" {
			ctx.parameterTemplates[param.Name] = fmt.Sprintf("{{data.%s.%s}}", sourceName, column)
		}
		result.Script.Parameters = append(result.Script.Parameters, dsl.Parameter{
			Name:         param.Name,
			Type:         "file",
			Update:       devWebParameterUpdate(param.NextValue),
			Selection:    devWebCSVStrategy(param.NextRow),
			OnExhaustion: devWebOnExhaustion(param.OnEnd),
			SameLineAs:   devWebSameLineAs(param.NextRow),
			DataSource:   sourceName,
			Column:       column,
		})
	}
	if len(result.Script.DataSources) > 0 {
		preserved["data_source_count"] = len(result.Script.DataSources)
	}
}

func applyDevWebRTS(result *ConvertResult, ctx *convertContext, diagnostics *diag.Collector) {
	rtsPath, content := findDevWebFile(ctx.files, "rts.yml")
	if strings.TrimSpace(content) == "" {
		return
	}
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "DevWeb rts.yml", rtsPath, "rts.yml could not be parsed: "+err.Error())
		}
		return
	}
	preserved := ensureDevWebImportPreserved(result.Script)
	preserved["runtime_file"] = rtsPath
	parity := map[string]string{}
	if httpConnection := mapValue(raw, "httpConnection"); len(httpConnection) > 0 {
		if value := intValue(httpConnection["maxPersistentConnectionsPerHost"]); value > 0 {
			result.Script.Config.HTTP.MaxConnsPerHost = value
			parity["httpConnection.maxPersistentConnectionsPerHost"] = "executed"
		}
		if value := intValue(httpConnection["maxRedirectDepth"]); value != 0 {
			result.Script.Config.HTTP.MaxRedirects = &value
			parity["httpConnection.maxRedirectDepth"] = "executed"
		}
		if value := intValue(httpConnection["connectTimeout"]); value > 0 {
			result.Script.Config.HTTP.ConnectTimeoutMs = value * 1000
			parity["httpConnection.connectTimeout"] = "executed"
		}
		if value := intValue(httpConnection["requestTimeout"]); value > 0 {
			result.Script.Config.HTTP.RequestTimeoutMs = value * 1000
			parity["httpConnection.requestTimeout"] = "executed"
		}
		if value := intValue(httpConnection["keepAliveTimeout"]); value > 0 {
			result.Script.Config.HTTP.IdleTimeoutMs = value * 1000
			parity["httpConnection.keepAliveTimeout"] = "executed"
		}
	}
	if proxy := mapValue(raw, "proxy"); len(proxy) > 0 {
		if boolValue(proxy["useProxy"]) {
			result.Script.Config.HTTP.ProxyMode = "custom"
			result.Script.Config.HTTP.Proxy.URL = stringValue(proxy["proxyServer"])
			result.Script.Config.HTTP.Proxy.Username = stringValue(proxy["proxyUser"])
			result.Script.Config.HTTP.Proxy.Password = stringValue(proxy["proxyPassword"])
			parity["proxy"] = "partial"
		} else {
			result.Script.Config.HTTP.ProxyMode = "none"
			parity["proxy"] = "executed"
		}
	}
	if ssl := mapValue(raw, "ssl"); len(ssl) > 0 {
		if boolValue(ssl["ignoreBadCertificate"]) {
			result.Script.Config.HTTP.TLS.InsecureSkipVerify = true
		}
		if version := stringValue(ssl["tlsMaxVersion"]); version != "" {
			result.Script.Config.HTTP.TLS.MaxVersion = strings.TrimPrefix(strings.ToLower(version), "tls")
		}
		parity["ssl"] = "partial"
	}
	if replay := mapValue(raw, "replay"); len(replay) > 0 {
		if strings.EqualFold(stringValue(replay["saveSnapshots"]), "error") {
			result.Script.Config.ErrorHandling.SnapshotOnError = true
		}
		if !boolValue(replay["useCache"]) {
			setDevWebRuntimeAttribute(result.Script, "devweb.replay.useCache", "false")
		}
		parity["replay"] = "partial"
	}
	if logger := mapValue(raw, "vuserLogger"); len(logger) > 0 {
		if strings.EqualFold(stringValue(logger["logMode"]), "error") {
			result.Script.Config.Log.OnErrorOnly = true
			result.Script.Config.Log.Level = "extended"
		}
		parity["vuserLogger"] = "partial"
	}
	if thinkTime := mapValue(raw, "thinkTime"); len(thinkTime) > 0 {
		if strings.EqualFold(stringValue(thinkTime["type"]), "ignore") {
			result.Script.Config.ThinkTime.Ignore = true
			parity["thinkTime"] = "executed"
		}
	}
	if len(parity) > 0 {
		preserved["runtime_parity"] = parity
	}
}

func findDevWebFile(files map[string]string, base string) (string, string) {
	for name, content := range files {
		if strings.EqualFold(filepath.Base(name), base) {
			return name, content
		}
	}
	return "", ""
}

func devWebInlineFile(files map[string]string, fileName string) string {
	for name, content := range files {
		if strings.EqualFold(filepath.Base(name), filepath.Base(fileName)) {
			return content
		}
	}
	return ""
}

func setDevWebRuntimeAttribute(script *dsl.Script, key string, value string) {
	if script.Config.WebRuntime.AdditionalAttributes == nil {
		script.Config.WebRuntime.AdditionalAttributes = map[string]string{}
	}
	script.Config.WebRuntime.AdditionalAttributes[key] = value
}

func mapValue(values map[string]any, key string) map[string]any {
	if values == nil {
		return nil
	}
	if typed, ok := values[key].(map[string]any); ok {
		return typed
	}
	if typed, ok := values[key].(map[any]any); ok {
		out := map[string]any{}
		for k, v := range typed {
			if s, ok := k.(string); ok {
				out[s] = v
			}
		}
		return out
	}
	return nil
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		n, _ := fmt.Sscanf(strings.TrimSpace(typed), "%d", new(int))
		if n == 0 {
			return 0
		}
		var out int
		_, _ = fmt.Sscanf(strings.TrimSpace(typed), "%d", &out)
		return out
	default:
		return 0
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func boolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "on":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func devWebCSVStrategy(value string) string {
	switch {
	case strings.Contains(strings.ToLower(value), "random"):
		return "random"
	default:
		return "sequential"
	}
}

func devWebParameterUpdate(value string) string {
	if strings.TrimSpace(value) == "" {
		return "iteration"
	}
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), " ", "_")
}

func devWebOnExhaustion(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "loop", "cycle":
		return "loop"
	case "stop", "abort":
		return "stop"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func devWebSameLineAs(value string) string {
	lower := strings.ToLower(strings.TrimSpace(value))
	if strings.HasPrefix(lower, "same as ") {
		return strings.TrimSpace(value[len("same as "):])
	}
	return ""
}

func objectFieldRaw(objectLiteral string, field string) (string, bool) {
	pattern := regexp.MustCompile(`(?s)(^|[,{\s])` + regexp.QuoteMeta(field) + `\s*:`)
	match := pattern.FindStringSubmatchIndex(objectLiteral)
	if len(match) == 0 {
		return "", false
	}
	start := match[1]
	if start < 0 || start > len(objectLiteral) {
		return "", false
	}
	end := findTopLevelFieldEnd(objectLiteral, start)
	return strings.TrimSpace(objectLiteral[start:end]), true
}

func findTopLevelFieldEnd(value string, start int) int {
	quote := rune(0)
	escaped := false
	depth := 0
	for i, r := range value[start:] {
		pos := start + i
		if escaped {
			escaped = false
			continue
		}
		if quote != 0 {
			if r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"', '`':
			quote = r
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			if depth == 0 {
				return pos
			}
			depth--
		case ',':
			if depth == 0 {
				return pos
			}
		}
	}
	return len(value)
}

func objectLiteralEntries(inner string) map[string]string {
	entries := map[string]string{}
	for _, part := range splitTopLevel(inner, ',') {
		key, value, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		cleanKey := strings.Trim(strings.TrimSpace(key), `"'`+"`")
		if cleanKey == "" {
			continue
		}
		entries[cleanKey] = strings.TrimSpace(value)
	}
	return entries
}

func splitTopLevel(value string, delimiter rune) []string {
	var out []string
	var current strings.Builder
	quote := rune(0)
	escaped := false
	depth := 0
	for _, r := range value {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if quote != 0 {
			current.WriteRune(r)
			if r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"', '`':
			quote = r
			current.WriteRune(r)
		case '{', '[', '(':
			depth++
			current.WriteRune(r)
		case '}', ']', ')':
			if depth > 0 {
				depth--
			}
			current.WriteRune(r)
		default:
			if r == delimiter && depth == 0 {
				if strings.TrimSpace(current.String()) != "" {
					out = append(out, strings.TrimSpace(current.String()))
				}
				current.Reset()
				continue
			}
			current.WriteRune(r)
		}
	}
	if strings.TrimSpace(current.String()) != "" {
		out = append(out, strings.TrimSpace(current.String()))
	}
	return out
}

func jsStringValue(raw string, params map[string]string) string {
	value := jsString(raw)
	return convertDevWebTemplate(value, params)
}

func jsObjectToJSON(raw string, params map[string]string) string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "{") || !strings.HasSuffix(raw, "}") {
		return jsStringValue(raw, params)
	}
	entries := objectLiteralEntries(strings.TrimSpace(raw[1 : len(raw)-1]))
	if len(entries) == 0 {
		return "{}"
	}
	values := map[string]string{}
	for key, rawValue := range entries {
		values[key] = jsStringValue(rawValue, params)
	}
	data, err := json.Marshal(values)
	if err != nil {
		return raw
	}
	return string(data)
}

func appendQueryString(rawURL string, query map[string]string) string {
	if len(query) == 0 {
		return rawURL
	}
	separator := "?"
	if strings.Contains(rawURL, "?") {
		separator = "&"
	}
	parts := make([]string, 0, len(query))
	for key, value := range query {
		parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(value))
	}
	sortStrings(parts)
	return rawURL + separator + strings.Join(parts, "&")
}

func parseDevWebExtractors(objectLiteral string) []dsl.Extractor {
	var extractors []dsl.Extractor
	for _, match := range regexp.MustCompile("new\\s+load\\.BoundaryExtractor\\s*\\(\\s*[\"'`]([^\"'`]+)[\"'`]\\s*,\\s*[\"'`]([^\"'`]+)[\"'`]\\s*,\\s*[\"'`]([^\"'`]+)[\"'`]\\s*\\)").FindAllStringSubmatch(objectLiteral, -1) {
		extractors = append(extractors, dsl.Extractor{
			Name:       match[1],
			Source:     "body",
			Type:       "boundary",
			Expression: match[2] + "|||" + match[3],
		})
	}
	for _, match := range regexp.MustCompile("new\\s+load\\.JsonPathExtractor\\s*\\(\\s*[\"'`]([^\"'`]+)[\"'`]\\s*,\\s*[\"'`]([^\"'`]+)[\"'`]").FindAllStringSubmatch(objectLiteral, -1) {
		extractors = append(extractors, dsl.Extractor{
			Name:       match[1],
			Source:     "body",
			Type:       "jsonpath",
			Expression: match[2],
		})
	}
	if len(extractors) == 0 {
		return nil
	}
	return extractors
}

func isDevWebNativeExtractorAt(content string, offset int) bool {
	line := extractLine(content, offset)
	return strings.Contains(line, "BoundaryExtractor") || strings.Contains(line, "JsonPathExtractor")
}

func activeDevWebTransaction(prefix string) string {
	matches := transactionNewPattern.FindAllStringSubmatch(prefix, -1)
	if len(matches) == 0 {
		return ""
	}
	var activeVar, activeName string
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		activeVar = match[1]
		activeName = match[2]
	}
	if activeVar == "" || activeName == "" {
		return ""
	}
	start := strings.LastIndex(prefix, activeVar+".start(")
	stop := strings.LastIndex(prefix, activeVar+".stop(")
	if start >= 0 && start > stop {
		return activeName
	}
	return ""
}

func convertDevWebTemplate(value string, params map[string]string) string {
	return regexp.MustCompile(`\$\{\s*([^}]+?)\s*\}`).ReplaceAllStringFunc(value, func(match string) string {
		expr := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(match, "${"), "}"))
		switch {
		case strings.HasPrefix(expr, "load.params."):
			name := strings.TrimSpace(strings.TrimPrefix(expr, "load.params."))
			if params != nil && params[name] != "" {
				return params[name]
			}
			return "{{var." + name + "}}"
		case strings.Contains(expr, ".extractors."):
			name := expr[strings.LastIndex(expr, ".extractors.")+len(".extractors."):]
			return "{{var." + strings.TrimSpace(name) + "}}"
		case params != nil && params[expr] != "":
			return params[expr]
		default:
			return match
		}
	})
}

func isSafeDevWebTemplateLiteral(line string, params map[string]string) bool {
	if !strings.Contains(line, "${") {
		return true
	}
	unsafe := false
	regexp.MustCompile(`\$\{\s*([^}]+?)\s*\}`).ReplaceAllStringFunc(line, func(match string) string {
		expr := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(match, "${"), "}"))
		if strings.HasPrefix(expr, "load.params.") || strings.Contains(expr, ".extractors.") {
			return match
		}
		unsafe = true
		return match
	})
	return !unsafe
}

func sortStrings(values []string) {
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}

func splitArgs(raw string) []string {
	var args []string
	var current strings.Builder
	quote := rune(0)
	depth := 0
	for _, r := range raw {
		switch {
		case quote != 0:
			current.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"' || r == '`':
			quote = r
			current.WriteRune(r)
		case r == '{' || r == '[' || r == '(':
			depth++
			current.WriteRune(r)
		case r == '}' || r == ']' || r == ')':
			if depth > 0 {
				depth--
			}
			current.WriteRune(r)
		case r == ',' && depth == 0:
			args = append(args, strings.TrimSpace(current.String()))
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if strings.TrimSpace(current.String()) != "" {
		args = append(args, strings.TrimSpace(current.String()))
	}
	return args
}

func grpcTarget(args []string) string {
	if len(args) >= 2 {
		service := strings.Trim(jsString(args[0]), "/")
		method := strings.Trim(jsString(args[1]), "/")
		if strings.Contains(service, "/") && method == "" {
			return service
		}
		return service + "/" + method
	}
	return strings.Trim(jsString(args[0]), "/")
}

func jsString(raw string) string {
	value := strings.TrimSpace(raw)
	if len(value) >= 2 {
		first := value[0]
		last := value[len(value)-1]
		if (first == '"' || first == '\'' || first == '`') && first == last {
			value = value[1 : len(value)-1]
		}
	}
	return strings.ReplaceAll(value, `\"`, `"`)
}

func jsObjectLiteral(raw string) string {
	value := strings.TrimSpace(raw)
	if strings.HasPrefix(value, "{") {
		value = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*:`).ReplaceAllString(value, `"$1":`)
		value = strings.ReplaceAll(value, `'`, `"`)
		return value
	}
	return jsString(value)
}

func secondsToMilliseconds(raw string) float64 {
	var seconds float64
	fmt.Sscanf(raw, "%f", &seconds)
	return seconds * 1000
}

func actionName(prefix string, suffix string) string {
	return sanitizeName(prefix + "_" + suffix)
}

func sanitizeName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "devweb_action"
	}
	value = regexp.MustCompile(`[^A-Za-z0-9_]+`).ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return "devweb_action"
	}
	return value
}

func readZipFile(file *zip.File) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
