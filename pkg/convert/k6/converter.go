package k6

import (
	"encoding/json"
	"fmt"
	"net/url"
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

type ImportRequest struct {
	Name      string         `json:"name,omitempty"`
	ProjectID string         `json:"project_id,omitempty"`
	Content   string         `json:"content"`
	Options   ConvertOptions `json:"options,omitempty"`
}

type ConvertOptions struct {
	BaseURL    string `json:"base_url,omitempty"`
	ScriptName string `json:"script_name,omitempty"`
}

type ConvertStats struct {
	HTTPRequests     int      `json:"http_requests"`
	Checks           int      `json:"checks"`
	Sleeps           int      `json:"sleeps"`
	Thresholds       int      `json:"thresholds"`
	ConvertedActions int      `json:"converted_actions"`
	UnsupportedCalls []string `json:"unsupported_calls,omitempty"`
}

type ConvertResult struct {
	Script       *dsl.Script        `json:"script"`
	Warnings     []string           `json:"warnings,omitempty"`
	Unsupported  diag.Diagnostics   `json:"unsupported,omitempty"`
	Assumptions  []string           `json:"assumptions,omitempty"`
	Stats        ConvertStats       `json:"stats"`
	SourceReview *sourceview.Review `json:"source_review,omitempty"`
}

type ExportRequest struct {
	Script  dsl.Script    `json:"script"`
	Options ExportOptions `json:"options,omitempty"`
}

type ExportOptions struct {
	IncludeThresholds bool `json:"include_thresholds,omitempty"`
}

type ExportResult struct {
	Script      string       `json:"script"`
	Warnings    []string     `json:"warnings,omitempty"`
	Unsupported []string     `json:"unsupported_elements,omitempty"`
	Assumptions []string     `json:"assumptions,omitempty"`
	Stats       ConvertStats `json:"stats"`
}

type parsedHTTPRequest struct {
	Method    string
	URL       string
	Body      string
	Headers   map[string]string
	StartLine int
	EndLine   int
}

type sortedAction struct {
	Action  dsl.Action
	Mapping sourceview.Mapping
}

var (
	optionsBlockPattern  = regexp.MustCompile(`(?s)export\s+(?:const|let|var)\s+options\s*=\s*\{(.*?)\}\s*;`)
	httpCallStartPattern = regexp.MustCompile(`\bhttp\.(get|post|put|delete)\s*\(`)
	sleepPattern         = regexp.MustCompile(`\bsleep\s*\(\s*([0-9]+(?:\.[0-9]+)?)\s*\)`)
	checkPattern         = regexp.MustCompile(`\bcheck\s*\(`)
	thresholdPattern     = regexp.MustCompile(`(?s)thresholds\s*:\s*\{(.*?)\}`)
	vusPattern           = regexp.MustCompile(`\bvus\s*:\s*([0-9]+)`)
	durationPattern      = regexp.MustCompile(`\bduration\s*:\s*['"]([^'"]+)['"]`)
	stagePattern         = regexp.MustCompile(`\{\s*duration\s*:\s*['"]([^'"]+)['"]\s*,\s*target\s*:\s*([0-9]+)\s*\}`)
	headersPattern       = regexp.MustCompile(`(?s)headers\s*:\s*\{(.*?)\}`)
	objectStringPattern  = regexp.MustCompile(`['"]([^'"]+)['"]\s*:\s*['"]([^'"]*)['"]`)
	importPattern        = regexp.MustCompile(`(?m)^\s*import\s+`)
	requirePattern       = regexp.MustCompile(`\brequire\s*\(`)
	browserPattern       = regexp.MustCompile(`['"]k6/browser['"]|\bbrowser\.`)
	controlFlowPattern   = regexp.MustCompile(`(?m)\b(?:if|switch|for|while)\s*\(`)
)

func Convert(req ImportRequest) (*ConvertResult, error) {
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, fmt.Errorf("content is required")
	}
	name := convertutil.FirstNonEmpty(req.Options.ScriptName, req.Name, "Converted k6 HTTP Script")
	result := &ConvertResult{
		Script: &dsl.Script{
			Name:          sanitizeName(name),
			Version:       "1.0.0",
			Protocol:      "http",
			ProjectID:     req.ProjectID,
			GeneratedBy:   "apexload-k6-converter",
			GeneratedFrom: "k6",
			CreatedAt:     time.Now(),
			Config: dsl.ScriptConfig{
				BaseURL: convertutil.FirstNonEmpty(strings.TrimSpace(req.Options.BaseURL), detectBaseURL(content)),
				ThinkTime: dsl.ThinkTimeConfig{
					Type:   "fixed",
					MeanMs: 1000,
				},
				Pacing:        dsl.PacingConfig{Type: "none"},
				RunLogic:      dsl.RunLogicConfig{Iterations: 0, AutoTransactions: "per_action"},
				ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: false, SnapshotOnError: true},
				Log:           dsl.LogConfig{Level: "standard"},
			},
			Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
				Source:           "k6",
				SourceScriptName: convertutil.FirstNonEmpty(req.Name, "script.js"),
				Preserved:        map[string]any{},
			}},
		},
		Assumptions: []string{
			"APEX Load imports a constrained HTTP subset and does not execute k6 JavaScript.",
			"Dynamic JavaScript expressions, modules, browser flows, and custom control flow require review before promotion.",
		},
	}
	var diagnostics diag.Collector
	optionsSummary := parseOptions(content, result)
	if result.Script.Metadata != nil && result.Script.Metadata.Import != nil && len(optionsSummary) > 0 {
		result.Script.Metadata.Import.Preserved["k6_options"] = optionsSummary
	}

	var unsorted []sortedAction

	requests := parseHTTPRequests(content)
	if len(requests) == 0 {
		return nil, fmt.Errorf("no supported k6 http.get/post/put/delete calls found")
	}
	for idx, req := range requests {
		action := dsl.Action{
			Name:    fmt.Sprintf("%s_%02d", titleMethod(req.Method), idx+1),
			Type:    "request",
			Method:  req.Method,
			Target:  relativeTarget(req.URL, result.Script.Config.BaseURL),
			Headers: req.Headers,
			Body:    req.Body,
			Metadata: map[string]any{
				"k6_source": "http." + strings.ToLower(req.Method),
			},
		}
		unsorted = append(unsorted, sortedAction{
			Action: action,
			Mapping: sourceview.Mapping{
				SourceFile:  convertutil.FirstNonEmpty(reqFileName(result), "script.js"),
				StartLine:   req.StartLine,
				EndLine:     req.EndLine,
				Function:    "http." + strings.ToLower(req.Method),
				TargetName:  action.Name,
				TargetKind:  "action",
				Status:      "converted",
				SupportMode: "executable",
				Notes:       "Converted from the constrained k6 HTTP subset.",
			},
		})
		result.Stats.HTTPRequests++
		result.Stats.ConvertedActions++
	}

	diagnoseUnsupported(content, &diagnostics, &result.Stats, &unsorted, convertutil.FirstNonEmpty(reqFileName(result), "script.js"))

	for i := 0; i < len(unsorted); i++ {
		for j := i + 1; j < len(unsorted); j++ {
			if unsorted[i].Mapping.StartLine > unsorted[j].Mapping.StartLine {
				unsorted[i], unsorted[j] = unsorted[j], unsorted[i]
			}
		}
	}

	sourceMappings := make([]sourceview.Mapping, 0, len(unsorted))
	for idx, s := range unsorted {
		s.Mapping.TargetPath = fmt.Sprintf("actions[%d]", idx)
		result.Script.Actions = append(result.Script.Actions, s.Action)
		sourceMappings = append(sourceMappings, s.Mapping)
	}
	result.Stats.Checks = len(checkPattern.FindAllStringIndex(content, -1))
	result.Stats.Sleeps = len(sleepPattern.FindAllStringIndex(content, -1))
	if result.Stats.Checks > 0 {
		result.Warnings = append(result.Warnings, "k6 check() calls are preserved as import evidence; recreate required checks as APEX Load assertions before promotion")
	}
	if result.Stats.Sleeps > 0 {
		result.Warnings = append(result.Warnings, "k6 sleep() calls are summarized in import stats; review APEX think-time/pacing before promotion")
	}
	result.Unsupported = diagnostics.Items()
	result.SourceReview = sourceview.Build(map[string]string{reqFileName(result): req.Content}, sourceMappings)
	return result, nil
}

func Export(req ExportRequest) (*ExportResult, error) {
	script := req.Script
	if strings.TrimSpace(script.Protocol) != "http" {
		return nil, fmt.Errorf("k6 export supports HTTP scripts only")
	}
	if len(script.Actions) == 0 {
		return nil, fmt.Errorf("script must have at least one action")
	}
	result := &ExportResult{
		Assumptions: []string{
			"Generated k6 is a reviewable HTTP subset script, not a drop-in translation of every APEX runtime setting.",
			"APEX extractors, data sources, and non-HTTP protocol actions are omitted or flagged for review.",
		},
	}
	var b strings.Builder
	b.WriteString("import http from 'k6/http';\n")
	b.WriteString("import { check, sleep } from 'k6';\n\n")
	b.WriteString("export const options = {\n")
	b.WriteString("  vus: 1,\n")
	b.WriteString("  duration: '1m'")
	if req.Options.IncludeThresholds && script.GoalConfig != nil {
		thresholds := exportThresholds(script.GoalConfig)
		if len(thresholds) > 0 {
			b.WriteString(",\n  thresholds: ")
			b.WriteString(thresholds)
		}
	}
	b.WriteString("\n};\n\n")
	baseURL := strings.TrimRight(script.Config.BaseURL, "/")
	b.WriteString("const BASE_URL = __ENV.BASE_URL || ")
	b.WriteString(jsString(baseURL))
	b.WriteString(";\n\nexport default function () {\n")
	for _, action := range script.Actions {
		if strings.TrimSpace(action.Type) != "request" {
			result.Unsupported = append(result.Unsupported, fmt.Sprintf("%s uses unsupported action type %q", action.Name, action.Type))
			continue
		}
		method := strings.ToLower(convertutil.FirstNonEmpty(action.Method, "GET"))
		if method != "get" && method != "post" && method != "put" && method != "delete" {
			result.Unsupported = append(result.Unsupported, fmt.Sprintf("%s uses unsupported HTTP method %q", action.Name, action.Method))
			continue
		}
		urlExpr := jsURLExpression(action.Target)
		headers := mergedHeaders(script.Config.HTTP.DefaultHeaders, action.Headers)
		if method == "get" || method == "delete" {
			b.WriteString("  ")
			b.WriteString("const res")
			b.WriteString(strconv.Itoa(result.Stats.ConvertedActions + 1))
			b.WriteString(" = http.")
			b.WriteString(method)
			b.WriteString("(")
			b.WriteString(urlExpr)
			if len(headers) > 0 {
				b.WriteString(", { headers: ")
				b.WriteString(jsStringMap(headers))
				b.WriteString(" }")
			}
			b.WriteString(");\n")
		} else {
			b.WriteString("  const res")
			b.WriteString(strconv.Itoa(result.Stats.ConvertedActions + 1))
			b.WriteString(" = http.")
			b.WriteString(method)
			b.WriteString("(")
			b.WriteString(urlExpr)
			b.WriteString(", ")
			b.WriteString(jsString(action.Body))
			if len(headers) > 0 {
				b.WriteString(", { headers: ")
				b.WriteString(jsStringMap(headers))
				b.WriteString(" }")
			}
			b.WriteString(");\n")
		}
		if len(action.Assertions) > 0 {
			b.WriteString("  check(res")
			b.WriteString(strconv.Itoa(result.Stats.ConvertedActions + 1))
			b.WriteString(", {\n")
			for _, assertion := range action.Assertions {
				if assertion.Type != "status_code" {
					result.Unsupported = append(result.Unsupported, fmt.Sprintf("%s assertion %q requires manual k6 check recreation", action.Name, assertion.Type))
					continue
				}
				b.WriteString("    ")
				b.WriteString(jsString("status is " + fmt.Sprint(assertion.Expected)))
				b.WriteString(": r => r.status === ")
				b.WriteString(fmt.Sprint(assertion.Expected))
				b.WriteString(",\n")
			}
			b.WriteString("  });\n")
			result.Stats.Checks++
		}
		if action.ThinkTime.MeanMs > 0 {
			b.WriteString("  sleep(")
			b.WriteString(strconv.FormatFloat(action.ThinkTime.MeanMs/1000, 'f', -1, 64))
			b.WriteString(");\n")
			result.Stats.Sleeps++
		}
		if len(action.Extractors) > 0 {
			result.Unsupported = append(result.Unsupported, fmt.Sprintf("%s has %d extractor(s); recreate correlation logic in k6 manually", action.Name, len(action.Extractors)))
		}
		result.Stats.HTTPRequests++
		result.Stats.ConvertedActions++
	}
	b.WriteString("}\n")
	result.Script = b.String()
	sort.Strings(result.Unsupported)
	return result, nil
}

func parseHTTPRequests(content string) []parsedHTTPRequest {
	matches := httpCallStartPattern.FindAllStringSubmatchIndex(content, -1)
	out := make([]parsedHTTPRequest, 0, len(matches))
	for _, match := range matches {
		method := strings.ToUpper(content[match[2]:match[3]])
		openOffset := match[1] - 1
		closeOffset := matchingParenOffset(content, openOffset)
		if closeOffset < 0 {
			continue
		}
		args := splitTopLevelArgs(content[openOffset+1 : closeOffset])
		if len(args) == 0 {
			continue
		}
		rawURL := literalArg(args[0])
		body := ""
		options := ""
		if method == "POST" || method == "PUT" {
			if len(args) > 1 {
				body = literalArg(args[1])
			}
			if len(args) > 2 {
				options = args[2]
			}
		} else if len(args) > 1 {
			options = args[1]
		}
		out = append(out, parsedHTTPRequest{
			Method:    method,
			URL:       rawURL,
			Body:      body,
			Headers:   parseHeaders(options),
			StartLine: sourceview.LineForOffset(content, match[0]),
			EndLine:   sourceview.LineForOffset(content, closeOffset),
		})
	}
	return out
}

func matchingParenOffset(content string, openOffset int) int {
	if openOffset < 0 || openOffset >= len(content) || content[openOffset] != '(' {
		return -1
	}
	depth := 0
	var quote byte
	escaped := false
	for i := openOffset; i < len(content); i++ {
		ch := content[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"', '`':
			quote = ch
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func splitTopLevelArgs(input string) []string {
	args := []string{}
	start := 0
	depth := 0
	var quote byte
	escaped := false
	for i := 0; i < len(input); i++ {
		ch := input[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"', '`':
			quote = ch
		case '(', '{', '[':
			depth++
		case ')', '}', ']':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				arg := strings.TrimSpace(input[start:i])
				if arg != "" {
					args = append(args, arg)
				}
				start = i + 1
			}
		}
	}
	if tail := strings.TrimSpace(input[start:]); tail != "" {
		args = append(args, tail)
	}
	return args
}

func parseOptions(content string, result *ConvertResult) map[string]any {
	match := optionsBlockPattern.FindStringSubmatch(content)
	if len(match) < 2 {
		return nil
	}
	block := match[1]
	out := map[string]any{}
	if vus := firstRegexInt(vusPattern, block); vus > 0 {
		out["vus"] = vus
	}
	if duration := firstRegexString(durationPattern, block); duration != "" {
		out["duration"] = duration
	}
	stages := stagePattern.FindAllStringSubmatch(block, -1)
	if len(stages) > 0 {
		stageSummaries := make([]map[string]any, 0, len(stages))
		for _, stage := range stages {
			target, _ := strconv.Atoi(stage[2])
			stageSummaries = append(stageSummaries, map[string]any{"duration": stage[1], "target": target})
		}
		out["stages"] = stageSummaries
	}
	if thresholds := thresholdPattern.FindStringSubmatch(block); len(thresholds) > 1 {
		result.Stats.Thresholds++
		result.Warnings = append(result.Warnings, "k6 thresholds were detected; map release gates to APEX goal_config before execution")
		out["thresholds_detected"] = true
	}
	return out
}

func diagnoseUnsupported(content string, diagnostics *diag.Collector, stats *ConvertStats, unsorted *[]sortedAction, sourceFile string) {
	addUnsupported := func(matches [][]int, construct, element, detail string, diagType string) {
		for _, match := range matches {
			stats.UnsupportedCalls = append(stats.UnsupportedCalls, construct)
			startLine := sourceview.LineForOffset(content, match[0])
			if diagType == "blocker" {
				diagnostics.Add(diag.Blocker, element, fmt.Sprintf("%s line %d", sourceFile, startLine), detail)
			} else {
				diagnostics.Add(diag.Warning, element, fmt.Sprintf("%s line %d", sourceFile, startLine), detail)
			}
			lineContent := extractLine(content, match[0])
			*unsorted = append(*unsorted, sortedAction{
				Action: dsl.Action{
					Name:  "Review_Required",
					Type:  "review_required",
					Value: strings.TrimSpace(lineContent),
					Metadata: map[string]any{
						"source": construct,
					},
				},
				Mapping: sourceview.Mapping{
					SourceFile:  sourceFile,
					StartLine:   startLine,
					EndLine:     startLine,
					Function:    construct,
					TargetName:  "Review_Required",
					TargetKind:  "action",
					Status:      "converted",
					SupportMode: "review_required",
					Notes:       detail,
				},
			})
		}
	}

	if browserPattern.MatchString(content) {
		addUnsupported(browserPattern.FindAllStringIndex(content, -1), "k6/browser", "k6 browser", "k6 browser scripts are not imported as executable protocol-load scripts; use APEX browser canary authoring instead", "blocker")
	}
	if requirePattern.MatchString(content) {
		addUnsupported(requirePattern.FindAllStringIndex(content, -1), "require", "k6 module", "CommonJS require() is not evaluated by the static importer", "warning")
	}
	imports := importPattern.FindAllStringIndex(content, -1)
	if len(imports) > 2 {
		addUnsupported(imports, "custom import", "k6 module", "non-core imports require review; APEX Load does not execute JavaScript modules", "warning")
	}
	if controlFlowPattern.MatchString(content) {
		addUnsupported(controlFlowPattern.FindAllStringIndex(content, -1), "dynamic control flow", "k6 JavaScript control flow", "loops, branches, and dynamic control flow are not preserved by the constrained HTTP importer", "warning")
	}
}

func extractLine(content string, offset int) string {
	if offset < 0 || offset >= len(content) {
		return ""
	}
	start := strings.LastIndex(content[:offset], "\n")
	if start == -1 {
		start = 0
	} else {
		start++
	}
	end := strings.Index(content[offset:], "\n")
	if end == -1 {
		end = len(content)
	} else {
		end += offset
	}
	return content[start:end]
}

func parseHeaders(options string) map[string]string {
	match := headersPattern.FindStringSubmatch(options)
	if len(match) < 2 {
		return nil
	}
	headers := map[string]string{}
	for _, pair := range objectStringPattern.FindAllStringSubmatch(match[1], -1) {
		headers[pair[1]] = pair[2]
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

func detectBaseURL(content string) string {
	for _, req := range parseHTTPRequests(content) {
		parsed, err := url.Parse(req.URL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			continue
		}
		return parsed.Scheme + "://" + parsed.Host
	}
	return ""
}

func relativeTarget(raw, base string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return raw
	}
	if base != "" {
		baseURL, err := url.Parse(base)
		if err == nil && strings.EqualFold(baseURL.Scheme, parsed.Scheme) && strings.EqualFold(baseURL.Host, parsed.Host) {
			target := parsed.EscapedPath()
			if target == "" {
				target = "/"
			}
			if parsed.RawQuery != "" {
				target += "?" + parsed.RawQuery
			}
			return target
		}
	}
	return raw
}

func literalArg(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		quote := value[0]
		if (quote == '\'' || quote == '"' || quote == '`') && value[len(value)-1] == quote {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func exportThresholds(goal *dsl.GoalConfig) string {
	values := map[string][]string{}
	if goal.P95LatencyMs != nil {
		values["http_req_duration"] = append(values["http_req_duration"], fmt.Sprintf("p(95)<%s", strconv.FormatFloat(*goal.P95LatencyMs, 'f', -1, 64)))
	}
	if goal.MaxErrorPct != nil {
		rate := 1 - (*goal.MaxErrorPct / 100)
		values["http_req_failed"] = append(values["http_req_failed"], fmt.Sprintf("rate<%s", strconv.FormatFloat(1-rate, 'f', -1, 64)))
	}
	if len(values) == 0 {
		return ""
	}
	payload, _ := json.MarshalIndent(values, "  ", "  ")
	return string(payload)
}

func jsURLExpression(target string) string {
	target = strings.TrimSpace(target)
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return jsString(target)
	}
	if target == "" {
		target = "/"
	}
	return "`${BASE_URL}" + strings.ReplaceAll(target, "`", "\\`") + "`"
}

func jsString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func jsStringMap(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, jsString(key)+": "+jsString(values[key]))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func mergedHeaders(global, local map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range global {
		out[k] = v
	}
	for k, v := range local {
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func firstRegexInt(re *regexp.Regexp, content string) int {
	value := firstRegexString(re, content)
	if value == "" {
		return 0
	}
	parsed, _ := strconv.Atoi(value)
	return parsed
}

func firstRegexString(re *regexp.Regexp, content string) string {
	match := re.FindStringSubmatch(content)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func titleMethod(method string) string {
	method = strings.ToLower(strings.TrimSpace(method))
	if method == "" {
		return "Request"
	}
	return strings.ToUpper(method[:1]) + method[1:]
}

func reqFileName(result *ConvertResult) string {
	if result == nil || result.Script == nil || result.Script.Metadata == nil || result.Script.Metadata.Import == nil {
		return "script.js"
	}
	return convertutil.FirstNonEmpty(result.Script.Metadata.Import.SourceScriptName, "script.js")
}

func sanitizeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Converted_k6_HTTP_Script"
	}
	re := regexp.MustCompile(`[^A-Za-z0-9_.-]+`)
	name = re.ReplaceAllString(name, "_")
	return strings.Trim(name, "_")
}
