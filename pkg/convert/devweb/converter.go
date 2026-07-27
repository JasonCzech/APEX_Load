package devweb

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/archiveingest"
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
	Assumptions  []string           `json:"assumptions,omitempty"`
	Unsupported  diag.Diagnostics   `json:"unsupported,omitempty"`
	Stats        ConvertStats       `json:"stats"`
	SourceReview *sourceview.Review `json:"source_review,omitempty"`

	// SecretRequirements describes credentials the shared projector governed out
	// of the source. Values are stripped; they travel in ProtectedRequirements.
	SecretRequirements []credentialpolicy.Requirement `json:"secret_requirements,omitempty"`
	// ProtectedRequirements transfers source credential values to API workflows
	// without placing them in the public conversion payload.
	ProtectedRequirements credentialpolicy.ProtectedRequirements `json:"-"`
	Findings              []importreview.Finding                 `json:"findings,omitempty"`
}

// standingAssumptions are the import-mode caveats every DevWeb conversion
// carries: the importer extracts SDK calls statically and never executes the
// script's JavaScript.
func standingAssumptions() []string {
	return []string{
		"APEX Load converts DevWeb SDK calls statically and does not execute the script's JavaScript; dynamic URLs, control flow, and custom helper logic are not replayed.",
		"Review extracted requests, WebSocket flows, and parameter templates against the original script before promoting this draft to load execution.",
	}
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
	return convertSource(convertutil.FirstNonEmpty(req.Name, "Converted_DevWeb_Script"), convertutil.FirstNonEmpty(req.Name, "devweb")+".js", req.Content, req.Options)
}

// Decompression-bomb guards for untrusted DevWeb archive uploads. These also
// protect the enterprise->devweb bridge, which re-extracts the raw zip here.
const (
	maxDevWebZipEntries = 4000
	maxDevWebEntryBytes = 64 << 20  // per uncompressed entry
	maxDevWebTotalBytes = 256 << 20 // cumulative uncompressed
)

func ConvertZip(zipData []byte, opts ConvertOptions) (*ConvertResult, error) {
	extracted, err := archiveingest.ReadZIP(zipData, archiveingest.Options{Limits: archiveingest.Limits{
		MaxEntries:    maxDevWebZipEntries,
		MaxEntryBytes: maxDevWebEntryBytes,
		MaxTotalBytes: maxDevWebTotalBytes,
		MaxDepth:      32,
	}})
	if err != nil {
		return nil, err
	}
	files := make(map[string]string, len(extracted))
	for _, file := range extracted {
		files[file.Name] = string(file.Data)
	}
	name, sourceFile, content := selectDevWebMainSource(files)
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("no DevWeb .js file found in archive")
	}
	ctx := convertContext{files: files, sourceFileName: sourceFile}
	return convertSourceWithContext(convertutil.FirstNonEmpty(name, "Converted_DevWeb_Script"), sourceFile, content, opts, ctx)
}

func convertSource(name string, sourceFileName string, content string, opts ConvertOptions) (*ConvertResult, error) {
	return convertSourceWithContext(name, sourceFileName, content, opts, convertContext{sourceFileName: sourceFileName})
}

func convertSourceWithContext(name string, sourceFileName string, content string, opts ConvertOptions, ctx convertContext) (*ConvertResult, error) {
	result := &ConvertResult{
		Assumptions: standingAssumptions(),
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
	// Route every HTTP request through the shared credential/request pipeline so
	// authored credentials are governed at conversion time rather than redacted
	// at the response boundary. Non-HTTP steps (WebSocket, gRPC, review-only
	// markers) keep their converter shape: the projector only understands the
	// HTTP request surface.
	//
	// This deliberately stops at FinalizeAction and does not call FinalizeScript.
	// DevWeb legitimately emits WebSocket- and gRPC-only scripts with no base
	// URL, and its zip-metadata path has pre-existing parameter/data-source
	// defects that the whole-script invariant would turn into hard conversion
	// failures. Credential governance is per request and does not need it.
	credentialRegistry := credentialpolicy.NewNameRegistry()
	provenVars := devWebExtractorVariables(result.Script.Actions)
	trustedValues := devWebRuntimeDataReferences(result.Script.Actions)
	requirements := make([]credentialpolicy.Requirement, 0)
	for index := range result.Script.Actions {
		if !devWebHTTPRequestAction(result.Script.Actions[index]) {
			continue
		}
		finalizedAction, report, finalizeErr := finalizeDevWebHTTPRequest(
			result.Script.Actions[index], index, sourceFileName, credentialRegistry, provenVars, trustedValues,
		)
		if finalizeErr != nil {
			return nil, finalizeErr
		}
		result.Script.Actions[index] = finalizedAction
		requirements = append(requirements, report.Requirements...)
		result.Findings = append(result.Findings, report.Findings...)
	}
	result.SecretRequirements = publicDevWebCredentialRequirements(requirements)
	result.ProtectedRequirements.Store(requirements)
	for _, finding := range result.Findings {
		diagnostics.Add(finding.Severity, finding.Element, finding.Location, finding.Detail)
	}

	result.Unsupported = diagnostics.Items()
	// Redact the discovered credential values from the source text before it
	// becomes public review evidence. The projector governed them in the
	// executable script; the source review is a separate disclosure surface.
	reviewFiles := make(map[string]string, len(ctx.files))
	for name, fileContent := range ctx.files {
		reviewFiles[name] = redactDevWebCredentialValues(fileContent, requirements)
	}
	result.SourceReview = sourceview.Build(reviewFiles, sourceMappings)
	return result, nil
}

type namedBlock struct {
	Name      string
	Body      string
	StartLine int
}

func actionBlocks(content string) []namedBlock {
	lineIndex := sourceview.NewLineIndex(content)
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
		blocks = append(blocks, namedBlock{Name: name, Body: body, StartLine: lineIndex.LineForOffset(bodyStart)})
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
