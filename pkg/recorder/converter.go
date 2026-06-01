package recorder

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type Correlation struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Expression  string `json:"expression"`
	ExtractType string `json:"extract_type"`
	FoundInSeq  int    `json:"found_in_seq"`
	UsedInSeqs  []int  `json:"used_in_seqs"`
	Value       string `json:"-"`
	SampleValue string `json:"sample_value"`
	// RuleID, Confidence, and Reasons carry the rules-engine ranking through to
	// the import-review UI so users can triage the highest-confidence
	// correlations first.
	RuleID     string   `json:"rule_id,omitempty"`
	Confidence float64  `json:"confidence,omitempty"`
	Reasons    []string `json:"reasons,omitempty"`
}

type ConvertResult struct {
	Script       *dsl.Script   `json:"script"`
	Correlations []Correlation `json:"correlations"`
	Warnings     []string      `json:"warnings,omitempty"`
}

// ConvertOptions toggles optional, opt-in conversion behaviors.
type ConvertOptions struct {
	// Parameterize promotes recorded input values (form fields / query params)
	// into a generated data source so the script is data-driven instead of
	// replaying hard-coded values.
	Parameterize bool
}

// ConvertSession converts a recording with default options.
func ConvertSession(session *Session, name string) *ConvertResult {
	return ConvertSessionWithOptions(session, name, ConvertOptions{})
}

// ConvertSessionWithOptions converts a recording, applying the supplied options.
func ConvertSessionWithOptions(session *Session, name string, opts ConvertOptions) *ConvertResult {
	if name == "" {
		name = session.Name
	}

	result := &ConvertResult{}

	webSocketRequests := includedWebSocketRequests(session.Requests)
	if len(webSocketRequests) > 0 {
		result.Script = convertWebSocketSession(session, name, webSocketRequests)
		if hasIncludedHTTPRequests(session.Requests) {
			result.Warnings = append(result.Warnings, "recording contains HTTP and WebSocket traffic; generated WebSocket script includes WebSocket exchanges only")
		}
		return result
	}

	correlations := detectCorrelations(session.Requests)
	result.Correlations = correlations

	corrMap := buildCorrelationMap(correlations)

	script := &dsl.Script{
		Name:        name,
		Version:     "1.0",
		Protocol:    "http",
		GeneratedBy: "recorder",
		Config: dsl.ScriptConfig{
			BaseURL: session.BaseURL,
			ThinkTime: dsl.ThinkTimeConfig{
				Type:   "recorded",
				MeanMs: 0,
			},
		},
		CreatedAt: time.Now(),
	}
	recordedResourceURLs := make([]string, 0)
	recordedResourceSeen := make(map[string]struct{})

	for i, req := range session.Requests {
		if req.IsStatic && !req.Excluded {
			rawURL := strings.TrimSpace(req.URL)
			if rawURL != "" {
				if _, ok := recordedResourceSeen[rawURL]; !ok {
					recordedResourceSeen[rawURL] = struct{}{}
					recordedResourceURLs = append(recordedResourceURLs, rawURL)
				}
			}
		}
		if req.IsStatic || req.Excluded {
			continue
		}

		target := req.Path
		if session.BaseURL != "" && strings.HasPrefix(req.URL, session.BaseURL) {
			target = strings.TrimPrefix(req.URL, session.BaseURL)
		}
		if target == "" {
			target = "/"
		}

		target = applyCorrelationVars(target, corrMap)

		actionName := fmt.Sprintf("%s %s", req.Method, simplifyPath(req.Path))
		if req.StepName != "" {
			actionName = req.StepName
		}
		if i < 10 {
			actionName = fmt.Sprintf("%02d - %s", i+1, actionName)
		} else {
			actionName = fmt.Sprintf("%d - %s", i+1, actionName)
		}

		action := dsl.Action{
			Name:   actionName,
			Type:   "request",
			Method: req.Method,
			Target: target,
		}

		// Carry the curated transaction name through to the engine, which groups
		// metrics by action metadata "source_transaction" (see
		// engine.applyProtocolResultSourceMetadata). Without this the transaction
		// names users assign in the recording studio are silently dropped.
		if txn := strings.TrimSpace(req.TransactionName); txn != "" {
			action.Metadata = map[string]any{"source_transaction": txn}
		}

		headers := filterRecordedHeaders(req.Headers, session.Filters.CaptureCookies)
		for key, value := range headers {
			headers[key] = applyCorrelationVars(value, corrMap)
		}
		if len(headers) > 0 {
			action.Headers = headers
		}

		if req.Body != "" {
			body := req.Body
			if req.BodyEncoding == "" {
				body = applyCorrelationVars(body, corrMap)
			}
			action.Body = body
			action.BodyEncoding = req.BodyEncoding
		}

		for _, corr := range correlations {
			if corr.FoundInSeq == req.Seq {
				action.Extractors = append(action.Extractors, dsl.Extractor{
					Name:       corr.Name,
					Source:     corr.Source,
					Type:       corr.ExtractType,
					Expression: corr.Expression,
				})
			}
		}

		action.Assertions = append(action.Assertions, dsl.Assertion{
			Type:     "status_code",
			Expected: req.StatusCode,
		})

		if req.ThinkTimeMs > 500 {
			action.ThinkTimeAfter = true
			action.ThinkTime = dsl.ThinkTimeConfig{
				Type:   "fixed",
				MeanMs: float64(req.ThinkTimeMs),
			}
		}

		script.Actions = append(script.Actions, action)
	}

	if len(script.Actions) == 0 {
		result.Warnings = append(result.Warnings, "no non-static requests found in recording")
	}
	if opts.Parameterize {
		result.Warnings = append(result.Warnings, parameterizeInputs(script)...)
	}
	if len(recordedResourceURLs) > 0 {
		script.Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source:           "recorder",
			SourceScriptName: session.Name,
			Preserved: map[string]any{
				"recorded_resource_urls_available": true,
				"recorded_resource_urls":           recordedResourceURLs,
			},
		}}
	}

	result.Script = script
	return result
}

func includedWebSocketRequests(requests []RecordedRequest) []RecordedRequest {
	result := make([]RecordedRequest, 0)
	for _, req := range requests {
		if req.Excluded || req.IsStatic || len(req.WebSocketMessages) == 0 {
			continue
		}
		result = append(result, req)
	}
	return result
}

func hasIncludedHTTPRequests(requests []RecordedRequest) bool {
	for _, req := range requests {
		if req.Excluded || req.IsStatic || len(req.WebSocketMessages) > 0 {
			continue
		}
		return true
	}
	return false
}

func convertWebSocketSession(session *Session, name string, requests []RecordedRequest) *dsl.Script {
	baseURL := webSocketBaseURL(requests[0].URL, session.BaseURL)
	script := &dsl.Script{
		Name:        name,
		Version:     "1.0",
		Protocol:    "websocket",
		GeneratedBy: "recorder",
		Config: dsl.ScriptConfig{
			BaseURL: baseURL,
			ThinkTime: dsl.ThinkTimeConfig{
				Type:   "recorded",
				MeanMs: 0,
			},
			WebSocket: dsl.WebSocketConfig{
				ReadTimeoutMs:  30000,
				WriteTimeoutMs: 10000,
			},
		},
		CreatedAt: time.Now(),
	}

	actionSeq := 1
	for _, req := range requests {
		target := webSocketActionTarget(req.URL, baseURL)
		connect := dsl.Action{
			Name:   numberedActionName(actionSeq, "WS Connect "+simplifyPath(req.Path)),
			Type:   "ws_connect",
			Target: target,
		}
		headers := filterRecordedHeaders(req.Headers, session.Filters.CaptureCookies)
		if len(headers) > 0 {
			connect.Headers = headers
		}
		if req.ThinkTimeMs > 500 {
			connect.ThinkTimeAfter = true
			connect.ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: float64(req.ThinkTimeMs)}
		}
		script.Actions = append(script.Actions, connect)
		actionSeq++

		for _, msg := range req.WebSocketMessages {
			if msg.Opcode != webSocketOpcodeText && msg.Opcode != webSocketOpcodeBinary {
				continue
			}
			switch msg.Direction {
			case webSocketDirectionClientToServer:
				action := dsl.Action{
					Name:         numberedActionName(actionSeq, "WS Send"),
					Type:         "ws_send",
					Body:         msg.Payload,
					BodyEncoding: msg.PayloadEncoding,
					Params:       map[string]any{"type": webSocketDSLMessageType(msg)},
				}
				script.Actions = append(script.Actions, action)
				actionSeq++
			case webSocketDirectionServerToClient:
				params := map[string]any{"timeout_ms": 1000}
				if msg.PayloadEncoding == "" && !msg.PayloadTruncated && msg.Payload != "" {
					params["match"] = msg.Payload
				}
				action := dsl.Action{
					Name:         numberedActionName(actionSeq, "WS Receive"),
					Type:         "ws_receive",
					Body:         msg.Payload,
					BodyEncoding: msg.PayloadEncoding,
					Params:       params,
				}
				script.Actions = append(script.Actions, action)
				actionSeq++
			}
		}

		script.Actions = append(script.Actions, dsl.Action{
			Name: numberedActionName(actionSeq, "WS Close"),
			Type: "ws_close",
		})
		actionSeq++
	}

	return script
}

func numberedActionName(seq int, label string) string {
	if seq < 10 {
		return fmt.Sprintf("%02d - %s", seq, label)
	}
	return fmt.Sprintf("%d - %s", seq, label)
}

func webSocketDSLMessageType(msg RecordedWebSocketMessage) string {
	if msg.Opcode == webSocketOpcodeBinary || msg.PayloadEncoding == "base64" {
		return "binary"
	}
	return "text"
}

func webSocketBaseURL(rawURL, fallback string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		parsed, err = url.Parse(strings.TrimSpace(fallback))
	}
	if err != nil || parsed.Host == "" {
		return fallback
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	case "":
		parsed.Scheme = "ws"
	}
	parsed.Path = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func webSocketActionTarget(rawURL, baseURL string) string {
	raw := strings.TrimSpace(rawURL)
	if raw == "" {
		return "/"
	}
	if baseURL != "" && strings.HasPrefix(raw, baseURL) {
		target := strings.TrimPrefix(raw, baseURL)
		if target == "" {
			return "/"
		}
		return target
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if baseURL == "" || parsed.Host == "" {
		if target := parsed.RequestURI(); target != "" {
			return target
		}
		return "/"
	}
	baseParsed, err := url.Parse(baseURL)
	if err == nil && strings.EqualFold(parsed.Host, baseParsed.Host) {
		if target := parsed.RequestURI(); target != "" {
			return target
		}
		return "/"
	}
	return raw
}

func detectCorrelations(requests []RecordedRequest) []Correlation {
	observations := make([]correlate.Observation, 0, len(requests))
	for _, req := range requests {
		observations = append(observations, correlate.Observation{
			ID:              req.Seq,
			RequestURL:      req.URL,
			RequestBody:     req.Body,
			RequestHeaders:  req.Headers,
			ResponseBody:    req.ResponseBody,
			ResponseHeaders: req.ResponseHeaders,
		})
	}
	shared := correlate.Detect(observations)
	correlations := make([]Correlation, 0, len(shared))
	for _, corr := range shared {
		correlations = append(correlations, Correlation{
			Name:        corr.Name,
			Source:      corr.Source,
			Expression:  corr.Expression,
			ExtractType: corr.ExtractType,
			FoundInSeq:  corr.FoundInID,
			UsedInSeqs:  corr.UsedInIDs,
			Value:       corr.Value,
			SampleValue: corr.SampleValue,
			RuleID:      corr.RuleID,
			Confidence:  corr.Confidence,
			Reasons:     corr.Reasons,
		})
	}
	return correlations
}

func slugifyCorrelationName(field string) string {
	return correlate.Slug(field)
}

func buildCorrelationMap(correlations []Correlation) map[string]string {
	shared := make([]correlate.Correlation, 0, len(correlations))
	for _, c := range correlations {
		shared = append(shared, correlate.Correlation{Name: c.Name, Value: c.Value, SampleValue: c.SampleValue})
	}
	return correlate.BuildVarMap(shared)
}

func applyCorrelationVars(s string, corrMap map[string]string) string {
	return correlate.ApplyVars(s, corrMap)
}

const parameterizedDataSourceName = "recorded_inputs"

// parameterizableField matches form/query field names that typically hold
// user-supplied input worth turning into data-driven parameters.
var parameterizableField = regexp.MustCompile(`(?i)\b(user(name)?|email|login|pass(word|wd)?|search|query|q|first[_-]?name|last[_-]?name|full[_-]?name|phone|mobile|zip|postal[_-]?code|city|address|account|customer)\b`)

// keyValuePair captures `key=value` segments in query strings and
// form-urlencoded bodies, preserving the leading delimiter so it can be
// re-emitted unchanged.
var keyValuePair = regexp.MustCompile(`([?&]|^)([A-Za-z0-9_.\-\[\]]+)=([^&#\s"']+)`)

// parameterizeInputs promotes recorded input values into a generated data
// source and rewrites the occurrences to {{data.recorded_inputs.<col>}}. It runs
// after correlation replacement so values already turned into {{var.*}} are left
// alone. Returns warnings describing what was parameterized.
func parameterizeInputs(script *dsl.Script) []string {
	columns := make([]string, 0)
	sampleValues := make(map[string]string)
	seen := make(map[string]bool)

	rewrite := func(s string) string {
		return keyValuePair.ReplaceAllStringFunc(s, func(segment string) string {
			m := keyValuePair.FindStringSubmatch(segment)
			if m == nil {
				return segment
			}
			prefix, key, value := m[1], m[2], m[3]
			if strings.Contains(value, "{{") || !parameterizableField.MatchString(key) {
				return segment
			}
			col := slugifyCorrelationName(key)
			if col == "" {
				return segment
			}
			if !seen[col] {
				seen[col] = true
				columns = append(columns, col)
				decoded, err := url.QueryUnescape(value)
				if err != nil {
					decoded = value
				}
				sampleValues[col] = decoded
			}
			return prefix + key + "={{data." + parameterizedDataSourceName + "." + col + "}}"
		})
	}

	for i := range script.Actions {
		script.Actions[i].Target = rewrite(script.Actions[i].Target)
		if script.Actions[i].Body != "" {
			script.Actions[i].Body = rewrite(script.Actions[i].Body)
		}
	}

	if len(columns) == 0 {
		return nil
	}

	header := strings.Join(columns, ",")
	row := make([]string, len(columns))
	for i, col := range columns {
		row[i] = csvEscapeField(sampleValues[col])
	}
	script.DataSources = append(script.DataSources, dsl.DataSource{
		Name:           parameterizedDataSourceName,
		Type:           "csv",
		Strategy:       "sequential",
		FirstRowHeader: true,
		InlineData:     header + "\n" + strings.Join(row, ","),
	})

	return []string{fmt.Sprintf(
		"Parameterized %d recorded input field(s) into data source %q (%s). Review the sample values and add rows before running at load.",
		len(columns), parameterizedDataSourceName, strings.Join(columns, ", "),
	)}
}

func csvEscapeField(value string) string {
	if strings.ContainsAny(value, ",\"\n\r") {
		return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
	}
	return value
}

func filterRecordedHeaders(headers map[string]string, captureCookies bool) map[string]string {
	skip := map[string]bool{
		"host": true, "content-length": true, "accept-encoding": true,
		"connection": true, "proxy-connection": true, "user-agent": true,
		"accept": true, "accept-language": true, "cache-control": true,
		"pragma": true, "sec-ch-ua": true, "sec-ch-ua-mobile": true,
		"sec-ch-ua-platform": true, "sec-fetch-dest": true,
		"sec-fetch-mode": true, "sec-fetch-site": true,
		"sec-fetch-user": true, "upgrade-insecure-requests": true,
	}
	if !captureCookies {
		skip["cookie"] = true
	}

	filtered := make(map[string]string)
	for k, v := range headers {
		if !skip[strings.ToLower(k)] {
			filtered[k] = v
		}
	}
	return filtered
}

func simplifyPath(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) > 3 {
		parts = parts[len(parts)-3:]
	}
	result := "/" + strings.Join(parts, "/")
	if len(result) > 40 {
		result = result[:40]
	}
	return result
}

func truncate(s string, maxLen int) string {
	return correlate.Truncate(s, maxLen)
}
