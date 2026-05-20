package devweb

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
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
	Skipped          int      `json:"skipped"`
	UnsupportedCalls []string `json:"unsupported_calls,omitempty"`
}

type ConvertResult struct {
	Script   *dsl.Script  `json:"script"`
	Warnings []string     `json:"warnings,omitempty"`
	Stats    ConvertStats `json:"stats"`
}

type parsedStep struct {
	Protocol string
	Action   dsl.Action
}

var (
	actionBlockPattern    = regexp.MustCompile("(?s)load\\.action\\s*\\(\\s*[\"'`]([^\"'`]+)[\"'`]\\s*,\\s*(?:async\\s*)?function\\s*\\([^)]*\\)\\s*\\{(.*?)\\}\\s*\\)")
	webRequestPattern     = regexp.MustCompile(`(?s)(?:new\s+)?load\.WebRequest\s*\(\s*\{(.*?)\}\s*\)\s*\.send\s*\(`)
	webSocketNewPattern   = regexp.MustCompile(`(?s)(?:const|let|var)?\s*([A-Za-z_$][\w$]*)?\s*=?\s*new\s+load\.WebSocket\s*\(\s*([^)]+)\)`)
	webSocketOpenPattern  = regexp.MustCompile(`(?s)([A-Za-z_$][\w$]*)\.open\s*\(`)
	webSocketSendPattern  = regexp.MustCompile(`(?s)([A-Za-z_$][\w$]*)\.send\s*\(\s*([^)]*)\)`)
	webSocketRecvPattern  = regexp.MustCompile(`(?s)([A-Za-z_$][\w$]*)\.receive\s*\(`)
	webSocketClosePattern = regexp.MustCompile(`(?s)([A-Za-z_$][\w$]*)\.close\s*\(`)
	grpcInvokePattern     = regexp.MustCompile(`(?s)([A-Za-z_$][\w$]*)\.invoke\s*\(\s*([^)]*)\)`)
	thinkTimePattern      = regexp.MustCompile(`load\.thinkTime\s*\(\s*([0-9]+(?:\.[0-9]+)?)\s*\)`)
)

func Convert(req ImportRequest) (*ConvertResult, error) {
	if strings.TrimSpace(req.Content) == "" {
		return nil, fmt.Errorf("content is required")
	}
	return convertSource(firstNonEmpty(req.Name, "Converted_DevWeb_Script"), req.Content, req.Options)
}

func ConvertZip(zipData []byte, opts ConvertOptions) (*ConvertResult, error) {
	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, err
	}
	var name, content string
	for _, file := range reader.File {
		if file.FileInfo().IsDir() || !strings.EqualFold(filepath.Ext(file.Name), ".js") {
			continue
		}
		body, err := readZipFile(file)
		if err != nil {
			return nil, err
		}
		name = strings.TrimSuffix(filepath.Base(file.Name), filepath.Ext(file.Name))
		content = string(body)
		break
	}
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("no DevWeb .js file found in archive")
	}
	return convertSource(firstNonEmpty(name, "Converted_DevWeb_Script"), content, opts)
}

func convertSource(name string, content string, opts ConvertOptions) (*ConvertResult, error) {
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

	blocks := actionBlocks(content)
	if len(blocks) == 0 {
		blocks = []namedBlock{{Name: "DevWebAction", Body: content}}
		result.Warnings = append(result.Warnings, "no load.action blocks found; imported top-level DevWeb calls in source order")
	}

	var protocols []string
	for _, block := range blocks {
		steps := parseBlock(block, &result.Stats, &result.Warnings)
		for _, step := range steps {
			protocols = append(protocols, step.Protocol)
			result.Script.Actions = append(result.Script.Actions, step.Action)
			result.Stats.ConvertedActions++
		}
	}
	if len(result.Script.Actions) == 0 && len(blocks) > 0 {
		result.Warnings = append(result.Warnings, "load.action block parsing could not preserve nested JavaScript objects; imported supported calls from full source")
		steps := parseBlock(namedBlock{Name: result.Script.Name, Body: content}, &result.Stats, &result.Warnings)
		for _, step := range steps {
			protocols = append(protocols, step.Protocol)
			result.Script.Actions = append(result.Script.Actions, step.Action)
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
	return result, nil
}

type namedBlock struct {
	Name string
	Body string
}

func actionBlocks(content string) []namedBlock {
	matches := actionBlockPattern.FindAllStringSubmatch(content, -1)
	blocks := make([]namedBlock, 0, len(matches))
	for _, match := range matches {
		blocks = append(blocks, namedBlock{Name: match[1], Body: match[2]})
	}
	return blocks
}

func parseBlock(block namedBlock, stats *ConvertStats, warnings *[]string) []parsedStep {
	var steps []parsedStep
	for _, match := range webRequestPattern.FindAllStringSubmatch(block.Body, -1) {
		stats.TotalSteps++
		action, ok := parseWebRequest(block.Name, match[1])
		if !ok {
			stats.Skipped++
			stats.UnsupportedCalls = append(stats.UnsupportedCalls, "load.WebRequest")
			*warnings = append(*warnings, "skipped DevWeb WebRequest with unsupported object shape")
			continue
		}
		stats.HTTPRequests++
		steps = append(steps, parsedStep{Protocol: "http", Action: action})
	}

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
		}})
	}
	for _, match := range webSocketOpenPattern.FindAllStringSubmatch(block.Body, -1) {
		if _, exists := socketVars[match[1]]; exists {
			continue
		}
		stats.TotalSteps++
		stats.Skipped++
		stats.UnsupportedCalls = append(stats.UnsupportedCalls, match[1]+".open")
		*warnings = append(*warnings, "skipped WebSocket open without constructor URL for "+match[1])
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
		}})
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
		}})
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
		}})
	}
	for _, match := range grpcInvokePattern.FindAllStringSubmatch(block.Body, -1) {
		stats.TotalSteps++
		args := splitArgs(match[2])
		if len(args) < 2 {
			stats.Skipped++
			stats.UnsupportedCalls = append(stats.UnsupportedCalls, match[1]+".invoke")
			*warnings = append(*warnings, "skipped gRPC invoke without service/method arguments")
			continue
		}
		target := grpcTarget(args)
		body := "{}"
		if len(args) >= 3 {
			body = jsObjectLiteral(args[2])
		}
		stats.GRPCCalls++
		steps = append(steps, parsedStep{Protocol: "grpc", Action: dsl.Action{
			Name:   actionName(block.Name, "grpc"),
			Type:   "grpc_unary",
			Target: target,
			Body:   body,
		}})
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
	return steps
}

func parseWebRequest(blockName string, objectLiteral string) (dsl.Action, bool) {
	rawURL := objectStringField(objectLiteral, "url")
	if rawURL == "" {
		rawURL = objectStringField(objectLiteral, "URL")
	}
	if rawURL == "" {
		return dsl.Action{}, false
	}
	method := strings.ToUpper(firstNonEmpty(objectStringField(objectLiteral, "method"), "GET"))
	body := objectStringField(objectLiteral, "body")
	if body == "" {
		body = objectStringField(objectLiteral, "bodyText")
	}
	return dsl.Action{
		Name:    actionName(blockName, method),
		Type:    "request",
		Method:  method,
		Target:  rawURL,
		Headers: objectStringMapField(objectLiteral, "headers"),
		Body:    body,
	}, true
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

func objectStringField(objectLiteral string, field string) string {
	pattern := regexp.MustCompile(`(?s)(?:^|[,{\s])` + regexp.QuoteMeta(field) + `\s*:\s*([^,\n}]+)`)
	match := pattern.FindStringSubmatch(objectLiteral)
	if len(match) < 2 {
		return ""
	}
	return jsString(match[1])
}

func objectStringMapField(objectLiteral string, field string) map[string]string {
	pattern := regexp.MustCompile(`(?s)(?:^|[,{\s])` + regexp.QuoteMeta(field) + `\s*:\s*\{(.*?)\}`)
	match := pattern.FindStringSubmatch(objectLiteral)
	if len(match) < 2 {
		return nil
	}
	entries := regexp.MustCompile(`["'`+"`"+`]?([A-Za-z0-9_\-]+)["'`+"`"+`]?\s*:\s*([^,\n}]+)`).FindAllStringSubmatch(match[1], -1)
	headers := make(map[string]string, len(entries))
	for _, entry := range entries {
		headers[entry[1]] = jsString(entry[2])
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
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
