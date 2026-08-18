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
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
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
	Script             *dsl.Script                    `json:"script"`
	Warnings           []string                       `json:"warnings,omitempty"`
	Unsupported        diag.Diagnostics               `json:"unsupported,omitempty"`
	Assumptions        []string                       `json:"assumptions,omitempty"`
	Findings           []importreview.Finding         `json:"findings,omitempty"`
	SecretRequirements []credentialpolicy.Requirement `json:"secret_requirements,omitempty"`
	Readiness          importreview.Readiness         `json:"readiness"`
	Stats              ConvertStats                   `json:"stats"`
	SourceReview       *sourceview.Review             `json:"source_review,omitempty"`
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
	Method        string
	URL           string
	Body          string
	Headers       map[string]string
	ReviewReasons []string
	// ResponseVar is the `const res = http.get(...)` binding, used to resolve
	// check(res, ...) back to the request that produced the response.
	ResponseVar string
	StartOffset int
	EndOffset   int
	StartLine   int
	EndLine     int
}

type sortedAction struct {
	Action  dsl.Action
	Mapping sourceview.Mapping
	Report  *requestimport.Report
}

var (
	optionsBlockPattern  = regexp.MustCompile(`(?s)export\s+(?:const|let|var)\s+options\s*=\s*\{(.*?)\}\s*;`)
	httpCallStartPattern = regexp.MustCompile(`\bhttp\.(get|post|put|patch|delete|del|head|options)\s*\(`)
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
				BaseURL:  convertutil.FirstNonEmpty(strings.TrimSpace(req.Options.BaseURL), detectBaseURL(content)),
				Pacing:   dsl.PacingConfig{Type: "none"},
				RunLogic: dsl.RunLogicConfig{Iterations: 0, AutoTransactions: "per_action"},
				// continue_on_error is left unset for the scenario policy to decide.
				ErrorHandling: dsl.ErrorHandlingConfig{SnapshotOnError: true},
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
	credentialRegistry := credentialpolicy.NewNameRegistry()
	optionsSummary := parseOptions(content, result)
	if result.Script.Metadata != nil && result.Script.Metadata.Import != nil && len(optionsSummary) > 0 {
		result.Script.Metadata.Import.Preserved["k6_options"] = optionsSummary
	}

	var unsorted []sortedAction

	requests := parseHTTPRequests(content)
	if len(requests) == 0 {
		return nil, fmt.Errorf("no supported k6 HTTP calls found")
	}
	missingBaseURL := strings.TrimSpace(result.Script.Config.BaseURL) == ""
	if missingBaseURL {
		// Keep dynamic/relative-only conversions as valid, disclosure-safe
		// drafts. The per-request gate below prevents this non-routable
		// placeholder from reaching transport.
		result.Script.Config.BaseURL = "https://missing-base-url.apexload.invalid"
	}
	// Translate the statically analysable sleep()/check() idioms before the
	// residual scan so faithfully reproduced statements stop gating the flow.
	translation := translateK6StaticPatterns(content, requests)
	residualLines := detectK6ResidualSource(content, requests, translation.translatedSpans)
	if len(residualLines) > 0 && result.Script.Metadata != nil &&
		result.Script.Metadata.Import != nil {
		// Keep the exact, inert JavaScript in the durable destination whenever
		// the constrained adapter cannot reproduce all behavior. Execution is
		// still gated, but an authorized operator can repair it without finding
		// the original file or re-entering credentials embedded in the source.
		result.Script.Metadata.Import.Preserved["k6_gated_source"] = content
	}
	for idx, req := range requests {
		if missingBaseURL {
			req.ReviewReasons = uniqueSortedK6Strings(append(req.ReviewReasons, "missing_base_url"))
		}
		target := relativeTarget(req.URL, result.Script.Config.BaseURL)
		if containsK6Reason(req.ReviewReasons, "dynamic_url_expression") {
			target = "/__k6_dynamic_target__"
		}
		body := req.Body
		if containsK6Reason(req.ReviewReasons, "dynamic_body_expression") {
			body = ""
		}
		action, report, finalizeErr := finalizeK6HTTPRequest(req, idx, target, body, convertutil.FirstNonEmpty(reqFileName(result), "script.js"), credentialRegistry, translation.forRequest(idx))
		if finalizeErr != nil {
			return nil, finalizeErr
		}
		if sharedReasons := k6ExecutionFindingCodes(report.Findings); len(sharedReasons) > 0 {
			setK6ReviewGate(&action, sharedReasons, []string{fmt.Sprintf("%s lines %d-%d", convertutil.FirstNonEmpty(reqFileName(result), "script.js"), req.StartLine, req.EndLine)})
		}
		supportMode := "executable"
		status := "converted"
		notes := "Converted from the constrained k6 HTTP subset."
		if len(req.ReviewReasons) != 0 {
			setK6ReviewGate(&action, req.ReviewReasons, []string{fmt.Sprintf("%s lines %d-%d", convertutil.FirstNonEmpty(reqFileName(result), "script.js"), req.StartLine, req.EndLine)})
			supportMode = "review_required"
			status = "review_only"
			notes = "Request shape was preserved as a disclosure-safe draft; dynamic JavaScript must be replaced before execution."
		}
		unsorted = append(unsorted, sortedAction{
			Action: action,
			Report: &report,
			Mapping: sourceview.Mapping{
				SourceFile:  convertutil.FirstNonEmpty(reqFileName(result), "script.js"),
				StartLine:   req.StartLine,
				EndLine:     req.EndLine,
				Function:    "http." + strings.ToLower(req.Method),
				TargetName:  action.Name,
				TargetKind:  "action",
				Status:      status,
				SupportMode: supportMode,
				Notes:       notes,
			},
		})
		result.Stats.HTTPRequests++
		result.Stats.ConvertedActions++
	}

	diagnoseUnsupported(content, &diagnostics, &result.Stats, &unsorted, convertutil.FirstNonEmpty(reqFileName(result), "script.js"))
	diagnoseK6ResidualSource(residualLines, &diagnostics, &result.Stats, convertutil.FirstNonEmpty(reqFileName(result), "script.js"))
	applyK6FlowReviewGates(content, &unsorted, convertutil.FirstNonEmpty(reqFileName(result), "script.js"), residualLines, translation)

	for i := 0; i < len(unsorted); i++ {
		for j := i + 1; j < len(unsorted); j++ {
			if unsorted[i].Mapping.StartLine > unsorted[j].Mapping.StartLine {
				unsorted[i], unsorted[j] = unsorted[j], unsorted[i]
			}
		}
	}

	sourceMappings := make([]sourceview.Mapping, 0, len(unsorted))
	reports := make([]requestimport.Report, 0, len(requests))
	for idx, s := range unsorted {
		s.Mapping.TargetPath = fmt.Sprintf("actions[%d]", idx)
		result.Script.Actions = append(result.Script.Actions, s.Action)
		sourceMappings = append(sourceMappings, s.Mapping)
		if s.Report != nil {
			report := *s.Report
			report.ActionPath = fmt.Sprintf("actions[%d]", idx)
			for findingIndex := range report.Findings {
				report.Findings[findingIndex].ActionPath = report.ActionPath
			}
			reports = append(reports, report)
		}
	}
	result.Stats.Checks = translation.checks()
	result.Stats.Sleeps = translation.sleeps()
	if len(translation.untranslatedChecks) > 0 {
		result.Warnings = append(result.Warnings, "k6 check() calls outside the static status-code idiom are preserved as import evidence; recreate required checks as APEX Load assertions before promotion")
	}
	if len(translation.untranslatedSleeps) > 0 {
		result.Warnings = append(result.Warnings, "k6 sleep() calls with non-literal durations (or with no preceding request) are summarized in import stats; review APEX think-time/pacing before promotion")
	}
	if len(residualLines) > 0 {
		// Residual JavaScript already produces an unsupported diagnostic and a
		// durable per-action gate, but the top-level warning list is what the
		// import surfaces first. Say plainly that the draft is blocked.
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"k6 source outside the translated static subset remains at %s line %d and is omitted from the public draft; the affected requests stay blocked until the behavior is replaced",
			convertutil.FirstNonEmpty(reqFileName(result), "script.js"), residualLines[0]))
	}
	if translation.translatedChecks > 0 || translation.translatedSleeps > 0 {
		result.Assumptions = append(result.Assumptions, fmt.Sprintf(
			"%d static check() status assertion(s) and %d literal sleep() call(s) were translated into APEX assertions and per-action think time; every other check/sleep form stays review-gated.",
			translation.translatedChecks, translation.translatedSleeps))
	}
	if translation.translatedChecks > 0 {
		// k6 check() is a metric: a failed check leaves the request successful
		// and the iteration running. The APEX assertion it becomes fails the
		// request, and under this draft's default error handling that ends the
		// iteration. Disclose the fidelity change rather than let a k6-vs-APEX
		// comparison diverge silently.
		result.Assumptions = append(result.Assumptions,
			"A failed k6 check() only records a check metric, but the translated APEX assertion fails the request and, with the draft's default stop-on-error handling, ends the iteration; set Continue On Error in runtime settings to match k6's behavior.")
	}
	unresolvedBindings := unresolvedK6CredentialBindings(reports)
	finalized, err := requestimport.FinalizeScript(result.Script, reports, requestimport.FinalizeOptions{UnresolvedBindings: unresolvedBindings})
	if err != nil {
		return nil, fmt.Errorf("finalize k6 HTTP import: %w", err)
	}
	result.Script = finalized.Script
	result.Findings = finalized.Findings
	result.Readiness = finalized.Readiness
	for _, item := range finalized.Diagnostics {
		diagnostics.Add(item.Severity, item.Element, item.Location, item.Detail)
	}
	reviewContent, credentialReviewRedacted := redactK6CredentialValues(req.Content, finalized.Requirements, requests)
	reviewContent, residualReviewRedacted := disclosureSafeK6SourceReview(reviewContent, len(residualLines) != 0)
	result.SecretRequirements = publicK6CredentialRequirements(finalized.Requirements)
	result.Unsupported = diagnostics.Items()
	result.SourceReview = sourceview.Build(map[string]string{reqFileName(result): reviewContent}, sourceMappings)
	if credentialReviewRedacted || residualReviewRedacted {
		markK6SourceReviewRedacted(result.SourceReview, reqFileName(result))
	}
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
		if method != "get" && method != "post" && method != "put" && method != "patch" &&
			method != "delete" && method != "head" && method != "options" {
			result.Unsupported = append(result.Unsupported, fmt.Sprintf("%s uses unsupported HTTP method %q", action.Name, action.Method))
			continue
		}
		urlExpr := jsURLExpression(action.Target)
		callMethod := method
		if callMethod == "delete" {
			callMethod = "del"
		}
		headers := mergedHeaders(script.Config.HTTP.DefaultHeaders, action.Headers)
		if method == "get" || method == "head" {
			b.WriteString("  ")
			b.WriteString("const res")
			b.WriteString(strconv.Itoa(result.Stats.ConvertedActions + 1))
			b.WriteString(" = http.")
			b.WriteString(callMethod)
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
			b.WriteString(callMethod)
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
	lineIndex := sourceview.NewLineIndex(content)
	// Locate call expressions only in executable source. Running the regular
	// expression over the raw JavaScript would promote examples in comments or
	// string/template literals into live HTTP actions.
	callSearchSource := maskK6NonCodeForCallSearch(content)
	matches := httpCallStartPattern.FindAllStringSubmatchIndex(callSearchSource, -1)
	out := make([]parsedHTTPRequest, 0, len(matches))
	for _, match := range matches {
		method := strings.ToUpper(content[match[2]:match[3]])
		if method == "DEL" {
			method = "DELETE"
		}
		openOffset := match[1] - 1
		closeOffset := matchingParenOffset(content, openOffset)
		if closeOffset < 0 {
			continue
		}
		args := splitTopLevelArgs(content[openOffset+1 : closeOffset])
		if len(args) == 0 {
			continue
		}
		rawURL, staticURL := parseK6StringLiteral(args[0])
		reviewReasons := make([]string, 0, 3)
		if !staticURL {
			reviewReasons = append(reviewReasons, "dynamic_url_expression")
		}
		body := ""
		options := ""
		if k6MethodHasBody(method) {
			if len(args) > 1 {
				var staticBody bool
				body, staticBody = parseK6StringLiteral(args[1])
				if !staticBody {
					reviewReasons = append(reviewReasons, "dynamic_body_expression")
				}
			}
			if len(args) > 2 {
				options = args[2]
			}
		} else if len(args) > 1 {
			options = args[1]
		}
		headers, optionReasons := parseK6RequestOptions(options)
		reviewReasons = append(reviewReasons, optionReasons...)
		out = append(out, parsedHTTPRequest{
			Method:        method,
			URL:           rawURL,
			Body:          body,
			Headers:       headers,
			ReviewReasons: uniqueSortedK6Strings(reviewReasons),
			ResponseVar:   k6AssignedResponseVariable(content, match[0]),
			StartOffset:   match[0],
			EndOffset:     closeOffset + 1,
			StartLine:     lineIndex.LineForOffset(match[0]),
			EndLine:       lineIndex.LineForOffset(closeOffset),
		})
	}
	return out
}

func maskK6NonCodeForCallSearch(content string) string {
	masked := []byte(content)
	for offset := 0; offset < len(content); {
		switch content[offset] {
		case '\'', '"', '`':
			quote := content[offset]
			start := offset
			offset++
			for offset < len(content) {
				current := content[offset]
				if current == '\\' {
					offset += 2
					if offset > len(content) {
						offset = len(content)
					}
					continue
				}
				offset++
				if current == quote {
					break
				}
				if quote != '`' && (current == '\n' || current == '\r') {
					break
				}
			}
			maskK6Span(masked, start, offset)
		case '/':
			if offset+1 >= len(content) {
				offset++
				continue
			}
			switch content[offset+1] {
			case '/':
				start := offset
				offset += 2
				for offset < len(content) && content[offset] != '\n' && content[offset] != '\r' {
					offset++
				}
				maskK6Span(masked, start, offset)
			case '*':
				start := offset
				offset += 2
				for offset+1 < len(content) && !(content[offset] == '*' && content[offset+1] == '/') {
					offset++
				}
				if offset+1 < len(content) {
					offset += 2
				} else {
					offset = len(content)
				}
				maskK6Span(masked, start, offset)
			default:
				offset++
			}
		default:
			offset++
		}
	}
	return string(masked)
}

func k6MethodHasBody(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
		return true
	default:
		return false
	}
}

func parseK6StringLiteral(expression string) (string, bool) {
	expression = strings.TrimSpace(expression)
	if len(expression) < 2 {
		return "", false
	}
	quote := expression[0]
	if (quote != '\'' && quote != '"') || expression[len(expression)-1] != quote {
		return "", false
	}
	if quote == '"' {
		var decoded string
		if err := json.Unmarshal([]byte(expression), &decoded); err != nil {
			return "", false
		}
		return decoded, true
	}

	// JSON and JavaScript share the escape sequences accepted below. Convert a
	// single-quoted JavaScript literal into a JSON string without evaluating
	// expressions, line continuations, hexadecimal escapes, or arbitrary code.
	var encoded strings.Builder
	encoded.WriteByte('"')
	inner := expression[1 : len(expression)-1]
	for i := 0; i < len(inner); i++ {
		ch := inner[i]
		if ch < 0x20 {
			return "", false
		}
		if ch == '"' {
			encoded.WriteString(`\"`)
			continue
		}
		if ch != '\\' {
			encoded.WriteByte(ch)
			continue
		}
		if i+1 >= len(inner) {
			return "", false
		}
		i++
		next := inner[i]
		switch next {
		case '\'':
			encoded.WriteByte('\'')
		case '"':
			encoded.WriteString(`\"`)
		case '\\', '/', 'b', 'f', 'n', 'r', 't':
			encoded.WriteByte('\\')
			encoded.WriteByte(next)
		case 'u':
			if i+4 >= len(inner) {
				return "", false
			}
			encoded.WriteString(`\u`)
			encoded.WriteString(inner[i+1 : i+5])
			i += 4
		default:
			return "", false
		}
	}
	encoded.WriteByte('"')
	var decoded string
	if err := json.Unmarshal([]byte(encoded.String()), &decoded); err != nil {
		return "", false
	}
	return decoded, true
}

var k6IdentifierPattern = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

func parseK6RequestOptions(options string) (map[string]string, []string) {
	options = strings.TrimSpace(options)
	if options == "" {
		return nil, nil
	}
	if len(options) < 2 || options[0] != '{' || options[len(options)-1] != '}' {
		return nil, []string{"dynamic_request_options"}
	}
	properties := splitTopLevelArgs(options[1 : len(options)-1])
	headers := map[string]string{}
	var reasons []string
	for _, property := range properties {
		keyExpression, valueExpression, ok := splitK6Property(property)
		if !ok {
			reasons = append(reasons, "dynamic_request_options")
			continue
		}
		key, ok := parseK6PropertyKey(keyExpression)
		if !ok {
			reasons = append(reasons, "dynamic_request_options")
			continue
		}
		if key != "headers" {
			reasons = append(reasons, "unsupported_request_options")
			continue
		}
		// JavaScript object literals use the last property occurrence. Replace
		// the complete header object instead of merging duplicate `headers`
		// properties, otherwise values from an overridden object can reach the
		// imported wire request.
		headers = nil
		parsedHeaders, ok := parseK6StringMap(valueExpression)
		if !ok {
			reasons = append(reasons, "dynamic_headers_expression")
			continue
		}
		headers = parsedHeaders
	}
	if len(headers) == 0 {
		headers = nil
	}
	return headers, uniqueSortedK6Strings(reasons)
}

func parseK6StringMap(expression string) (map[string]string, bool) {
	expression = strings.TrimSpace(expression)
	if len(expression) < 2 || expression[0] != '{' || expression[len(expression)-1] != '}' {
		return nil, false
	}
	out := map[string]string{}
	for _, property := range splitTopLevelArgs(expression[1 : len(expression)-1]) {
		keyExpression, valueExpression, ok := splitK6Property(property)
		if !ok {
			return nil, false
		}
		key, ok := parseK6PropertyKey(keyExpression)
		if !ok {
			return nil, false
		}
		value, ok := parseK6StringLiteral(valueExpression)
		if !ok {
			return nil, false
		}
		out[key] = value
	}
	return out, true
}

func splitK6Property(property string) (string, string, bool) {
	depth := 0
	var quote byte
	escaped := false
	for i := 0; i < len(property); i++ {
		ch := property[i]
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
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			if depth > 0 {
				depth--
			}
		case ':':
			if depth == 0 {
				key := strings.TrimSpace(property[:i])
				value := strings.TrimSpace(property[i+1:])
				return key, value, key != "" && value != ""
			}
		}
	}
	return "", "", false
}

func parseK6PropertyKey(expression string) (string, bool) {
	if value, ok := parseK6StringLiteral(expression); ok {
		return value, true
	}
	expression = strings.TrimSpace(expression)
	if k6IdentifierPattern.MatchString(expression) {
		return expression, true
	}
	return "", false
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
	if len(out) > 0 {
		// options.vus/duration/stages are recorded as scenario metadata only -
		// APEX scenario sizing, not the imported script, drives execution. The
		// support matrix declares this construct only partly reproduced, so it
		// owes the import review a signal of its own. Without this warning the
		// load profile is dropped from every review surface and the only thing
		// left is the generic "residual JavaScript" disclosure, which any
		// non-static source produces whether or not options were parsed.
		result.Warnings = append(result.Warnings, "k6 options (vus/duration/stages) were preserved as import evidence only; APEX scenario sizing remains the execution source of truth, so re-enter the load profile before promotion")
	}
	if thresholds := thresholdPattern.FindStringSubmatch(block); len(thresholds) > 1 {
		result.Stats.Thresholds++
		result.Warnings = append(result.Warnings, "k6 thresholds were detected; map release gates to APEX goal_config before execution")
		out["thresholds_detected"] = true
	}
	return out
}

func diagnoseUnsupported(content string, diagnostics *diag.Collector, stats *ConvertStats, unsorted *[]sortedAction, sourceFile string) {
	lineIndex := sourceview.NewLineIndex(content)
	addUnsupported := func(matches [][]int, construct, element, detail string, diagType string) {
		for _, match := range matches {
			stats.UnsupportedCalls = append(stats.UnsupportedCalls, construct)
			startLine := lineIndex.LineForOffset(match[0])
			if diagType == "blocker" {
				diagnostics.Add(diag.Blocker, element, fmt.Sprintf("%s line %d", sourceFile, startLine), detail)
			} else {
				diagnostics.Add(diag.Warning, element, fmt.Sprintf("%s line %d", sourceFile, startLine), detail)
			}
			*unsorted = append(*unsorted, sortedAction{
				Action: dsl.Action{
					Name:  "Review_Required",
					Type:  "review_required",
					Value: fmt.Sprintf("Review %s at %s line %d", construct, sourceFile, startLine),
					Metadata: map[string]any{
						"source":            construct,
						"execution_blocker": "import_review_required",
						"import_review": map[string]any{
							"version":      1,
							"source":       "k6",
							"reason_codes": []string{"unsupported_source_logic"},
							"source_refs":  []string{fmt.Sprintf("%s line %d", sourceFile, startLine)},
							"resolution":   "replace_behavior_and_remove_gate",
						},
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
	for _, match := range imports {
		line := extractLine(content, match[0])
		if k6CoreImport(line) || browserPattern.MatchString(line) {
			continue
		}
		addUnsupported([][]int{match}, "custom import", "k6 module", "non-core imports require review; APEX Load does not execute JavaScript modules", "warning")
	}
	if controlFlowPattern.MatchString(content) {
		addUnsupported(controlFlowPattern.FindAllStringIndex(content, -1), "dynamic control flow", "k6 JavaScript control flow", "loops, branches, and dynamic control flow are not preserved by the constrained HTTP importer", "warning")
	}
}

func applyK6FlowReviewGates(content string, actions *[]sortedAction, sourceFile string, residualLines []int, translation k6StaticTranslation) {
	if actions == nil {
		return
	}
	lineIndex := sourceview.NewLineIndex(content)
	reasons := map[string]struct{}{}
	sourceRefs := map[string]struct{}{}
	addMatches := func(code string, matches [][]int) {
		if len(matches) == 0 {
			return
		}
		reasons[code] = struct{}{}
		for _, match := range matches {
			sourceRefs[fmt.Sprintf("%s line %d", sourceFile, lineIndex.LineForOffset(match[0]))] = struct{}{}
		}
	}
	addLines := func(code string, lines []int) {
		if len(lines) == 0 {
			return
		}
		reasons[code] = struct{}{}
		for _, line := range lines {
			sourceRefs[fmt.Sprintf("%s line %d", sourceFile, line)] = struct{}{}
		}
	}
	// Only the occurrences the static pass could not reproduce still gate the
	// flow; translated sleep()/check() statements are now real DSL behavior.
	addLines("untranslated_check", translation.untranslatedChecks)
	addLines("untranslated_sleep", translation.untranslatedSleeps)
	addMatches("untranslated_threshold", thresholdPattern.FindAllStringIndex(content, -1))
	addMatches("unsupported_browser_behavior", browserPattern.FindAllStringIndex(content, -1))
	addMatches("unsupported_module", requirePattern.FindAllStringIndex(content, -1))
	addMatches("residual_control_flow", controlFlowPattern.FindAllStringIndex(content, -1))
	for _, match := range importPattern.FindAllStringIndex(content, -1) {
		line := extractLine(content, match[0])
		if k6CoreImport(line) {
			continue
		}
		addMatches("unsupported_module", [][]int{match})
	}
	if len(residualLines) != 0 {
		reasons["unsupported_source_logic"] = struct{}{}
		for _, line := range residualLines {
			sourceRefs[fmt.Sprintf("%s line %d", sourceFile, line)] = struct{}{}
		}
	}
	if len(reasons) == 0 {
		return
	}
	reasonList := sortedK6Set(reasons)
	refList := sortedK6Set(sourceRefs)
	for i := range *actions {
		if strings.EqualFold(strings.TrimSpace((*actions)[i].Action.Type), "request") {
			setK6ReviewGate(&(*actions)[i].Action, reasonList, refList)
			(*actions)[i].Mapping.Status = "review_only"
			(*actions)[i].Mapping.SupportMode = "review_required"
			(*actions)[i].Mapping.Notes = "Residual k6 behavior affects this flow and must be replaced before execution."
		}
	}
}

func setK6ReviewGate(action *dsl.Action, reasons []string, sourceRefs []string) {
	if action == nil {
		return
	}
	if action.Metadata == nil {
		action.Metadata = map[string]any{}
	}
	reasonSet := map[string]struct{}{}
	refSet := map[string]struct{}{}
	if existing, ok := action.Metadata["import_review"].(map[string]any); ok {
		for _, reason := range k6MetadataStrings(existing["reason_codes"]) {
			reasonSet[reason] = struct{}{}
		}
		for _, ref := range k6MetadataStrings(existing["source_refs"]) {
			refSet[ref] = struct{}{}
		}
	}
	for _, reason := range reasons {
		if reason = strings.TrimSpace(reason); reason != "" {
			reasonSet[reason] = struct{}{}
		}
	}
	for _, ref := range sourceRefs {
		if ref = strings.TrimSpace(ref); ref != "" {
			refSet[ref] = struct{}{}
		}
	}
	action.Metadata["execution_blocker"] = "import_review_required"
	action.Metadata["import_review"] = map[string]any{
		"version":      1,
		"source":       "k6",
		"reason_codes": sortedK6Set(reasonSet),
		"source_refs":  sortedK6Set(refSet),
		"resolution":   "replace_behavior_and_remove_gate",
	}
}

func k6MetadataStrings(value any) []string {
	switch values := value.(type) {
	case []string:
		return values
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func containsK6Reason(reasons []string, target string) bool {
	for _, reason := range reasons {
		if reason == target {
			return true
		}
	}
	return false
}

func uniqueSortedK6Strings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			set[value] = struct{}{}
		}
	}
	return sortedK6Set(set)
}

func sortedK6Set(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func k6CoreImport(line string) bool {
	line = strings.TrimSpace(line)
	return strings.Contains(line, "'k6/http'") ||
		strings.Contains(line, `"k6/http"`) ||
		strings.Contains(line, " from 'k6'") ||
		strings.Contains(line, ` from "k6"`)
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
	// Userinfo is part of the effective request credential surface. Keeping
	// the absolute URL lets the shared projector externalize and review-gate
	// it; relativizing here would silently discard both semantics and source
	// migration evidence.
	if parsed.User != nil {
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
