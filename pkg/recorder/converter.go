package recorder

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type Correlation struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Expression  string `json:"expression"`
	ExtractType string `json:"extract_type"`
	Convert     string `json:"convert,omitempty"`
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
	hasHTTPRequests := hasIncludedHTTPRequests(session.Requests)
	if len(webSocketRequests) > 0 && !hasHTTPRequests {
		result.Script = convertWebSocketSession(session, name, webSocketRequests)
		return result
	}

	correlations := detectCorrelations(convertibleCorrelationRequests(session.Requests))
	result.Correlations = correlations

	appliedCorrelations := highConfidenceCorrelations(correlations)
	corrMap := buildCorrelationMap(appliedCorrelations)

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
		if len(req.WebSocketMessages) > 0 {
			continue
		}
		target := recordedRequestTarget(req, session.BaseURL)
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

		headers := correlatedRecordedHeaders(req, session.Filters.CaptureCookies, corrMap)
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

		for _, corr := range appliedCorrelations {
			if corr.FoundInSeq == req.Seq {
				action.Extractors = append(action.Extractors, dsl.Extractor{
					Name:       corr.Name,
					Source:     corr.Source,
					Type:       corr.ExtractType,
					Expression: corr.Expression,
					Convert:    corr.Convert,
				})
			}
		}

		if shouldAssertRecordedStatus(req.StatusCode) {
			action.Assertions = append(action.Assertions, dsl.Assertion{
				Type:     "status_code",
				Expected: req.StatusCode,
			})
		}

		if req.ThinkTimeMs > 500 && len(script.Actions) > 0 {
			prev := &script.Actions[len(script.Actions)-1]
			prev.ThinkTimeAfter = true
			prev.ThinkTime = dsl.ThinkTimeConfig{
				Type:   "fixed",
				MeanMs: float64(req.ThinkTimeMs),
			}
		}

		script.Actions = append(script.Actions, action)
	}

	if len(script.Actions) == 0 {
		result.Warnings = append(result.Warnings, "no non-static requests found in recording")
	}
	if skipped := len(correlations) - len(appliedCorrelations); skipped > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Detected %d lower-confidence correlation candidate(s) left for review; only high-confidence correlations were applied to the generated script.", skipped))
	}
	if opts.Parameterize {
		result.Warnings = append(result.Warnings, parameterizeInputs(script, session.Name)...)
	}
	preserved := make(map[string]any)
	if len(recordedResourceURLs) > 0 {
		preserved["recorded_resource_urls_available"] = true
		preserved["recorded_resource_urls"] = recordedResourceURLs
	}
	if len(webSocketRequests) > 0 && hasHTTPRequests {
		preserved["recorded_websocket_urls_available"] = true
		preserved["recorded_websocket_urls"] = recordedWebSocketURLs(webSocketRequests)
		result.Warnings = append(result.Warnings, "recording contains HTTP and WebSocket traffic; generated HTTP script includes HTTP requests and preserves WebSocket exchanges as review metadata")
	}
	if len(preserved) > 0 {
		target := ensureRecorderImportPreserved(script, session.Name)
		for key, value := range preserved {
			target[key] = value
		}
	}

	result.Script = script
	return result
}

func convertibleCorrelationRequests(requests []RecordedRequest) []RecordedRequest {
	filtered := make([]RecordedRequest, 0, len(requests))
	for _, req := range requests {
		if req.IsStatic || req.Excluded || len(req.WebSocketMessages) > 0 {
			continue
		}
		if len(req.RawHeaders) > 0 {
			req.Headers = withoutRecorderControlHeaders(req.RawHeaders)
		} else {
			req.Headers = withoutRecorderControlHeaders(req.Headers)
		}
		req.ResponseHeaders = withoutRecorderControlHeaders(req.ResponseHeaders)
		filtered = append(filtered, req)
	}
	return filtered
}

func correlatedRecordedHeaders(req RecordedRequest, captureCookies bool, corrMap map[string]string) map[string]string {
	headers := filterRecordedHeaders(req.Headers, captureCookies)
	rawHeaders := filterRecordedHeaders(req.RawHeaders, captureCookies)
	for key, rawValue := range rawHeaders {
		correlated := applyCorrelationVars(rawValue, corrMap)
		if correlated == rawValue {
			continue // nothing in the raw value correlated
		}
		redactedValue, ok := headers[key]
		// When the raw header value was redacted (its redacted view differs from
		// raw, i.e. it carried secret material), only adopt the raw-derived
		// correlated value if the correlation consumed the secret and left behind
		// nothing but recognized non-secret scaffolding (e.g. a "Bearer " auth
		// scheme). A substring match on the secret itself (e.g. a token inside
		// "Bearer abc<matched>xyz") would splice the un-correlated remainder of
		// the live credential into the generated script.
		if ok && redactedValue != rawValue && !headerResidualIsSafe(correlated) {
			continue
		}
		headers[key] = correlated
	}
	for key, value := range headers {
		headers[key] = applyCorrelationVars(value, corrMap)
	}
	return headers
}

// templatePlaceholderPattern matches a {{...}} correlation/parameter placeholder.
var templatePlaceholderPattern = regexp.MustCompile(`\{\{[^}]*\}\}`)

// knownHeaderScaffolding is the set of non-secret words allowed to remain around
// a correlated token in a header that was otherwise redacted (auth schemes and
// similar structural keywords).
var knownHeaderScaffolding = map[string]bool{
	"bearer": true, "basic": true, "digest": true, "negotiate": true,
	"token": true, "apikey": true, "hmac": true, "sso": true, "jwt": true,
}

// headerResidualIsSafe reports whether the non-placeholder remainder of a
// correlated header value is free of leftover secret material: once the {{...}}
// placeholders are removed, every remaining alphanumeric word must be recognized
// non-secret scaffolding (e.g. "Bearer"). Any other residual word is treated as
// an un-correlated fragment of the original secret, so the header stays redacted.
func headerResidualIsSafe(correlated string) bool {
	stripped := templatePlaceholderPattern.ReplaceAllString(correlated, " ")
	for _, word := range strings.FieldsFunc(stripped, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if !knownHeaderScaffolding[strings.ToLower(word)] {
			return false
		}
	}
	return true
}

func shouldAssertRecordedStatus(statusCode int) bool {
	return statusCode >= 200 && statusCode < 300
}

// SessionBaseline returns the recorded request/response exchanges that the
// generated HTTP script's actions map to, in action order (ID = action index).
// It is the recorded side of a record-vs-replay correlation diagnosis and uses
// the same meaningful-request filter as ConvertSessionWithOptions's main loop,
// so observation IDs line up with the saved script's action indices. Raw values
// are returned; the caller applies redaction before persisting.
func SessionBaseline(session *Session) []correlate.Observation {
	if session == nil {
		return nil
	}
	observations := make([]correlate.Observation, 0, len(session.Requests))
	actionIdx := 0
	for _, req := range session.Requests {
		if req.IsStatic || req.Excluded || len(req.WebSocketMessages) > 0 {
			continue
		}
		observations = append(observations, correlate.Observation{
			ID:              actionIdx,
			RequestURL:      req.URL,
			RequestBody:     req.Body,
			RequestHeaders:  requestHeadersForBaseline(req),
			ResponseBody:    req.ResponseBody,
			ResponseHeaders: withoutRecorderControlHeaders(req.ResponseHeaders),
		})
		actionIdx++
	}
	return observations
}

func requestHeadersForBaseline(req RecordedRequest) map[string]string {
	if len(req.RawHeaders) > 0 {
		return withoutRecorderControlHeaders(req.RawHeaders)
	}
	return withoutRecorderControlHeaders(req.Headers)
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

func recordedWebSocketURLs(requests []RecordedRequest) []string {
	urls := make([]string, 0, len(requests))
	seen := make(map[string]struct{}, len(requests))
	for _, req := range requests {
		raw := strings.TrimSpace(req.URL)
		if raw == "" {
			continue
		}
		if _, ok := seen[raw]; ok {
			continue
		}
		seen[raw] = struct{}{}
		urls = append(urls, raw)
	}
	return urls
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

func recordedRequestTarget(req RecordedRequest, baseURL string) string {
	rawURL := strings.TrimSpace(req.URL)
	base := strings.TrimSpace(baseURL)
	if rawURL != "" {
		if base != "" {
			if target, ok := stripRecordedBaseURL(rawURL, base); ok {
				return target
			}
		}
		parsed, err := url.Parse(rawURL)
		if err == nil && parsed.Host != "" {
			if base != "" && sameRecordedOrigin(parsed, base) {
				if uri := parsed.RequestURI(); uri != "" {
					return uri
				}
			}
			return rawURL
		}
	}
	target := req.Path
	if target == "" {
		target = "/"
	}
	if req.QueryString != "" && !strings.Contains(target, "?") {
		target += "?" + strings.TrimPrefix(req.QueryString, "?")
	}
	return target
}

func stripRecordedBaseURL(rawURL, baseURL string) (string, bool) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" || !strings.HasPrefix(rawURL, base) {
		return "", false
	}
	rest := strings.TrimPrefix(rawURL, base)
	if rest == "" {
		return "/", true
	}
	if rest[0] != '/' && rest[0] != '?' && rest[0] != '#' {
		return "", false
	}
	if rest[0] == '?' || rest[0] == '#' {
		return "/" + rest, true
	}
	return rest, true
}

func sameRecordedOrigin(parsed *url.URL, baseURL string) bool {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || base.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Scheme, base.Scheme) && strings.EqualFold(parsed.Host, base.Host)
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

		for _, msg := range coalesceWebSocketMessages(req.WebSocketMessages) {
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

func coalesceWebSocketMessages(messages []RecordedWebSocketMessage) []RecordedWebSocketMessage {
	if len(messages) == 0 {
		return nil
	}
	coalesced := make([]RecordedWebSocketMessage, 0, len(messages))
	lastByDirection := make(map[string]int)
	for _, msg := range messages {
		switch msg.Opcode {
		case webSocketOpcodeText, webSocketOpcodeBinary:
			coalesced = append(coalesced, msg)
			lastByDirection[msg.Direction] = len(coalesced) - 1
		case webSocketOpcodeContinuation:
			idx, ok := lastByDirection[msg.Direction]
			if !ok || idx < 0 || idx >= len(coalesced) {
				continue
			}
			coalesced[idx] = appendWebSocketContinuation(coalesced[idx], msg)
		}
	}
	return coalesced
}

func appendWebSocketContinuation(base RecordedWebSocketMessage, cont RecordedWebSocketMessage) RecordedWebSocketMessage {
	base.PayloadSizeBytes += cont.PayloadSizeBytes
	base.PayloadTruncated = base.PayloadTruncated || cont.PayloadTruncated
	if base.Opcode == webSocketOpcodeBinary || base.PayloadEncoding == "base64" || cont.PayloadEncoding == "base64" {
		left := webSocketPayloadBytes(base)
		right := webSocketPayloadBytes(cont)
		base.Payload = base64.StdEncoding.EncodeToString(append(left, right...))
		base.PayloadEncoding = "base64"
		return base
	}
	base.Payload += cont.Payload
	return base
}

func webSocketPayloadBytes(msg RecordedWebSocketMessage) []byte {
	if msg.PayloadEncoding == "base64" {
		if decoded, err := base64.StdEncoding.DecodeString(msg.Payload); err == nil {
			return decoded
		}
	}
	return []byte(msg.Payload)
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
			RequestHeaders:  withoutRecorderControlHeaders(req.Headers),
			ResponseBody:    req.ResponseBody,
			ResponseHeaders: withoutRecorderControlHeaders(req.ResponseHeaders),
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
			Convert:     corr.Convert,
			FoundInSeq:  corr.FoundInID,
			UsedInSeqs:  corr.UsedInIDs,
			Value:       corr.Value,
			SampleValue: corr.SampleValue,
			RuleID:      corr.RuleID,
			Confidence:  corr.Confidence,
			Reasons:     corr.Reasons,
		})
	}
	return uniquifyCorrelationNames(correlations)
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

func highConfidenceCorrelations(correlations []Correlation) []Correlation {
	result := make([]Correlation, 0, len(correlations))
	for _, c := range correlations {
		if correlate.HighConfidence(toSharedCorrelation(c)) {
			result = append(result, c)
		}
	}
	return result
}

func toSharedCorrelation(c Correlation) correlate.Correlation {
	return correlate.Correlation{
		Name:        c.Name,
		Source:      c.Source,
		Expression:  c.Expression,
		ExtractType: c.ExtractType,
		Convert:     c.Convert,
		FoundInID:   c.FoundInSeq,
		UsedInIDs:   append([]int(nil), c.UsedInSeqs...),
		Value:       c.Value,
		SampleValue: c.SampleValue,
		RuleID:      c.RuleID,
		Confidence:  c.Confidence,
		Reasons:     append([]string(nil), c.Reasons...),
	}
}

func uniquifyCorrelationNames(correlations []Correlation) []Correlation {
	seen := make(map[string]int, len(correlations))
	for i := range correlations {
		base := strings.TrimSpace(correlations[i].Name)
		if base == "" {
			base = "corr_value"
		}
		seen[base]++
		if seen[base] == 1 {
			correlations[i].Name = base
			continue
		}
		correlations[i].Name = fmt.Sprintf("%s_%d", base, seen[base])
	}
	return correlations
}

func applyCorrelationVars(s string, corrMap map[string]string) string {
	return correlate.ApplyVars(s, corrMap)
}

const parameterizedDataSourceName = "recorded_inputs"

// parameterizableField matches form/query field names that typically hold
// user-supplied input worth turning into data-driven parameters.
var parameterizableField = regexp.MustCompile(`(?i)\b(user(name)?|email|login|pass(word|wd)?|search|query|q|first[_-]?name|last[_-]?name|full[_-]?name|phone|mobile|zip|postal[_-]?code|city|address|account|customer)\b`)

var parameterizedSecretField = regexp.MustCompile(`(?i)\b(pass(word|wd)?|secret|api[_-]?key|private[_-]?key)\b`)

// keyValuePair captures `key=value` segments in query strings and
// form-urlencoded bodies, preserving the leading delimiter so it can be
// re-emitted unchanged.
var keyValuePair = regexp.MustCompile(`([?&]|^)([A-Za-z0-9_.\-\[\]]+)=([^&#\s"']+)`)

// parameterizeInputs promotes recorded input values into a generated data
// source and rewrites the occurrences to {{data.recorded_inputs.<col>}}. It runs
// after correlation replacement so values already turned into {{var.*}} are left
// alone. Returns warnings describing what was parameterized.
func parameterizeInputs(script *dsl.Script, sourceName string) []string {
	columns := make([]string, 0)
	sampleValues := make(map[string]string)
	secretColumns := make(map[string]bool)
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
				if parameterizedSecretField.MatchString(key) {
					sampleValues[col] = "__REPLACE_WITH_SECRET__"
					secretColumns[col] = true
				} else {
					sampleValues[col] = decoded
				}
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

	warnings := []string{fmt.Sprintf(
		"Parameterized %d recorded input field(s) into data source %q (%s). Review the sample values and add rows before running at load.",
		len(columns), parameterizedDataSourceName, strings.Join(columns, ", "),
	)}
	if len(secretColumns) > 0 {
		names := make([]string, 0, len(secretColumns))
		for name := range secretColumns {
			names = append(names, name)
		}
		sort.Strings(names)
		ensureRecorderImportPreserved(script, sourceName)["recorded_input_secret_columns"] = names
		warnings = append(warnings, fmt.Sprintf(
			"Secret-like recorded input field(s) were seeded with placeholders instead of captured values (%s).",
			strings.Join(names, ", "),
		))
	}
	return warnings
}

func ensureRecorderImportPreserved(script *dsl.Script, sourceName string) map[string]any {
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{}
	}
	if script.Metadata.Import.Source == "" {
		script.Metadata.Import.Source = "recorder"
	}
	if script.Metadata.Import.SourceScriptName == "" {
		script.Metadata.Import.SourceScriptName = sourceName
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = make(map[string]any)
	}
	return script.Metadata.Import.Preserved
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
		if !isRecorderControlHeader(k) && !skip[strings.ToLower(k)] {
			filtered[k] = v
		}
	}
	return filtered
}

func withoutRecorderControlHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	filtered := make(map[string]string, len(headers))
	for name, value := range headers {
		if !isRecorderControlHeader(name) {
			filtered[name] = value
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
