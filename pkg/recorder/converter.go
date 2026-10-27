package recorder

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/recordingprofile"
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
	PreApplied bool     `json:"pre_applied,omitempty"`
	// ReviewAction is populated when one or more detected spellings cannot be
	// rewritten without changing wire encoding semantics.
	ReviewAction string `json:"review_action,omitempty"`
}

type ConvertResult struct {
	Script            *dsl.Script               `json:"script"`
	Correlations      []Correlation             `json:"correlations"`
	Warnings          []string                  `json:"warnings,omitempty"`
	EffectiveSettings *sample.EffectiveSettings `json:"effective_settings,omitempty"`
	// Diagnostics carries coded, informational observations from the shared
	// conversion pipeline, such as origins that advertise HTTP/3. Warnings
	// and blockers stay in Warnings.
	Diagnostics diag.Diagnostics `json:"diagnostics,omitempty"`
	// Document and HARData retain the synthesized in-memory HAR representation
	// for optional downstream Stage 2/3 composition passes.
	Document           *sample.HARDocument  `json:"-"`
	HARData            []byte               `json:"-"`
	Stats              sample.ConvertStats  `json:"-"`
	SampleCorrelations []sample.Correlation `json:"-"`
	// ProtectedRequirements transfers source values needed to bind {{secret.*}}
	// references during conversion composition.
	ProtectedRequirements credentialpolicy.ProtectedRequirements `json:"-"`
	// Baseline is the raw recorded observation of each converted request,
	// with ID the index of its action in Script: the recorded side of a
	// record-vs-replay correlation diagnosis. It holds source values; the
	// caller redacts it before persisting.
	Baseline []correlate.Observation `json:"-"`
}

// ConvertOptions toggles optional, opt-in conversion behaviors.
type ConvertOptions struct {
	ConversionPolicy       string
	CorrelationMode        string
	RecordingProfileID     string
	IncludeAssertions      *bool
	InferExtractors        *bool
	IncludeStaticAssets    *bool
	HighConfidenceOnly     *bool
	ReviewOnlyCorrelations bool
	DisableSalesforce      *bool
	SalesforceNative       *bool
	AgentforceTransform    *bool
	// Parameterize promotes recorded input values (form fields / query params)
	// into a generated data source so the script is data-driven instead of
	// replaying hard-coded values.
	Parameterize      bool
	DeferParameterize bool
	// PageTitleChecks adds a body_contains check on each recorded HTML page's
	// <title>, as VuGen does by default. Nil means on.
	PageTitleChecks *bool
	// ResourceMode is "runtime" (default: static resources are dropped and
	// discovered at replay), "in_step" (each static resource is attached to
	// the page that requested it as an explicit resource) or "separate"
	// (each static resource is its own step).
	ResourceMode string
	// AsyncScan collapses requests a page repeated in the background (poll,
	// long-poll, push) into background conversations. Nil means on; false
	// keeps them as recorded steps.
	AsyncScan *bool
	// Profile is the project recording profile: its header lists, host and
	// content-type filters, think-time threshold and correlation settings
	// apply, and it supplies the options above that are left unset.
	Profile *recordingprofile.Profile
	// HTTPVersionMode maps captured HTTP versions to leaf policies (see
	// sample.ConvertOptions.HTTPVersionMode). The proxy's client leg never
	// carries h3, so it matters for advertisements only through the mode's
	// handling of HTTP/2 and HTTP/1.1.
	HTTPVersionMode string
}

// ConvertSession converts a recording with default options.
func ConvertSession(session *Session, name string) *ConvertResult {
	return ConvertSessionWithOptions(session, name, ConvertOptions{})
}

// ConvertSessionWithOptions converts a recording, applying the supplied
// options. HTTP traffic converts through the shared HAR pipeline (see
// convertThroughSharedPipeline); a recording of WebSocket traffic alone
// becomes a WebSocket script.
func ConvertSessionWithOptions(session *Session, name string, opts ConvertOptions) *ConvertResult {
	result := &ConvertResult{}
	if session == nil {
		result.Warnings = append(result.Warnings, "recording conversion was not completed because the session is unavailable")
		return result
	}
	if name == "" {
		name = session.Name
	}
	if session.DroppedRequests > 0 || session.DroppedWebSocketMessages > 0 || session.Truncated {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"Recording capture was truncated: %d request(s) and %d WebSocket message(s) were dropped after retention limits were reached; review the generated script for missing tail steps.",
			session.DroppedRequests,
			session.DroppedWebSocketMessages,
		))
	}

	profile := opts.Profile
	if profile != nil {
		if opts.PageTitleChecks == nil {
			opts.PageTitleChecks = profile.PageTitleChecks
		}
		if opts.ResourceMode == "" && opts.IncludeStaticAssets == nil {
			opts.ResourceMode = profile.ResourceMode
		}
		if opts.AsyncScan == nil {
			opts.AsyncScan = profile.AsyncScan
		}
	}
	// The session's cookie capture already reflects the profile it was
	// started with (or an explicit choice over it); converting with a
	// different profile applies that profile's choice.
	policy := headerPolicy{cookies: session.Filters.CaptureCookies, profile: profile}
	if profile != nil && profile.CaptureCookies != nil && !strings.EqualFold(profile.ID, session.Filters.RecordingProfileID) {
		policy.cookies = *profile.CaptureCookies
	}

	webSocketRequests := includedWebSocketRequests(session.Requests)
	hasHTTPRequests := hasIncludedHTTPRequests(session.Requests)
	if len(webSocketRequests) > 0 && !hasHTTPRequests {
		result.Script = convertWebSocketSession(session, name, webSocketRequests)
		if len(session.Markers) > 0 {
			result.Warnings = append(result.Warnings, "The transactions and notes marked while recording attach to HTTP steps; this WebSocket-only recording has none, so its script carries neither.")
		}
		return result
	}
	if !hasHTTPRequests {
		result.Warnings = append(result.Warnings, "no non-static requests found in recording")
		result.Script = emptyRecordingScript(session, name)
		return result
	}

	convertThroughSharedPipeline(session, name, opts, policy, result)
	script := result.Script
	if len(script.Actions) == 0 {
		return result
	}
	if opts.Parameterize && !opts.DeferParameterize {
		result.Warnings = append(result.Warnings, parameterizeInputs(script, session.Name)...)
	}
	preserved := make(map[string]any)
	if urls := recordedResourceURLs(session.Requests); len(urls) > 0 {
		preserved["recorded_resource_urls_available"] = true
		preserved["recorded_resource_urls"] = urls
	}
	if len(webSocketRequests) > 0 {
		preserved["recorded_websocket_urls_available"] = true
		preserved["recorded_websocket_urls"] = recordedWebSocketURLs(webSocketRequests)
	}
	if len(preserved) > 0 {
		target := ensureRecorderImportPreserved(script, session.Name)
		for key, value := range preserved {
			target[key] = value
		}
	}
	return result
}

// emptyRecordingScript is the script of a recording that converted no HTTP
// requests.
func emptyRecordingScript(session *Session, name string) *dsl.Script {
	return &dsl.Script{
		Name:          name,
		Version:       "1.0",
		Protocol:      "http",
		GeneratedBy:   "recorder",
		GeneratedFrom: "proxy_recording",
		Config: dsl.ScriptConfig{
			BaseURL:   session.BaseURL,
			ThinkTime: dsl.ThinkTimeConfig{Type: "recorded"},
		},
		CreatedAt: time.Now(),
	}
}

// recordedAt is when a request was recorded; a request without a timestamp
// keeps its recorded order.
func recordedAt(req RecordedRequest) time.Time {
	if !req.Timestamp.IsZero() {
		return req.Timestamp
	}
	return time.Unix(0, 0).Add(time.Duration(req.Seq) * time.Millisecond)
}

func disclosureSafeCorrelations(correlations []Correlation) []Correlation {
	if len(correlations) == 0 {
		return correlations
	}
	safe := make([]Correlation, len(correlations))
	copy(safe, correlations)
	for index := range safe {
		safe[index].Value = ""
		safe[index].SampleValue = ""
	}
	return safe
}

// SessionBaseline returns the recorded request/response exchanges that a
// default conversion's actions map to (ID = action index): the recorded side
// of a record-vs-replay correlation diagnosis. A conversion with options
// carries its own in ConvertResult.Baseline. Raw values are returned; the
// caller applies redaction before persisting.
func SessionBaseline(session *Session) []correlate.Observation {
	if session == nil {
		return nil
	}
	return ConvertSession(session, "").Baseline
}

func recordedResponseHeaders(req RecordedRequest) map[string]string {
	if len(req.RawResponseHeadersMulti) > 0 {
		return multiHeadersForBaseline(req.RawResponseHeadersMulti)
	}
	if len(req.RawResponseHeaders) > 0 {
		return withoutRecorderControlHeaders(req.RawResponseHeaders)
	}
	if len(req.ResponseHeadersMulti) > 0 {
		return multiHeadersForBaseline(req.ResponseHeadersMulti)
	}
	return withoutRecorderControlHeaders(req.ResponseHeaders)
}

// multiHeadersForBaseline preserves repeated header lines for the correlation
// detector. Newlines mirror the HTTP extractor's flattened-header surface and,
// unlike comma folding, do not corrupt Set-Cookie values containing Expires.
func multiHeadersForBaseline(headers map[string][]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	result := make(map[string]string, len(headers))
	for name, values := range headers {
		if isRecorderControlHeader(name) {
			continue
		}
		result[name] = strings.Join(values, "\n")
	}
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
		headers := filterRecordedHeaders(req.Headers, headerPolicy{cookies: session.Filters.CaptureCookies})
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

func slugifyCorrelationName(field string) string {
	return correlate.Slug(field)
}

func claimRecorderCorrelationName(base string, used map[string]bool) string {
	if !used[base] {
		used[base] = true
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s_%d", base, suffix)
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
}

func countUnappliedRecorderCorrelations(correlations []Correlation) int {
	count := 0
	for _, correlation := range correlations {
		if !correlation.PreApplied {
			count++
		}
	}
	return count
}

const parameterizedDataSourceName = "recorded_inputs"

const (
	maxRecorderParameterizationScanBytes int64 = 64 << 20
	maxRecorderParameterizationMatches         = 4096
)

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
// ParameterizeRecordingInputs promotes recorded input values into a generated data
// source and rewrites the occurrences to {{data.recorded_inputs.<col>}}.
func ParameterizeRecordingInputs(script *dsl.Script, sourceName string) []string {
	return parameterizeInputs(script, sourceName)
}

func parameterizeInputs(script *dsl.Script, sourceName string) []string {
	return parameterizeInputsWithLimits(
		script,
		sourceName,
		maxRecorderParameterizationScanBytes,
		maxRecorderParameterizationMatches,
	)
}

func parameterizeInputsWithLimits(script *dsl.Script, sourceName string, scanByteLimit int64, matchLimit int) []string {
	if script == nil {
		return nil
	}
	columns := make([]string, 0)
	sampleValues := make(map[string]string)
	secretColumns := make(map[string]bool)
	columnByField := make(map[string]string)
	usedColumns := make(map[string]bool)
	var scannedBytes int64
	matchesSeen := 0

	rewrite := func(s string) (string, bool) {
		valueBytes := int64(len(s))
		if scanByteLimit < 0 || valueBytes > scanByteLimit-scannedBytes {
			return "", false
		}
		scannedBytes += valueBytes
		remainingMatches := matchLimit - matchesSeen
		if remainingMatches < 0 {
			return "", false
		}
		matches := keyValuePair.FindAllStringSubmatchIndex(s, remainingMatches+1)
		if len(matches) > remainingMatches {
			return "", false
		}
		matchesSeen += len(matches)
		if len(matches) == 0 {
			return s, true
		}

		var rewritten strings.Builder
		rewritten.Grow(len(s))
		lastEnd := 0
		for _, match := range matches {
			if len(match) < 8 {
				return "", false
			}
			start, end := match[0], match[1]
			prefix := s[match[2]:match[3]]
			key := s[match[4]:match[5]]
			value := s[match[6]:match[7]]
			replacement := s[start:end]
			if strings.Contains(value, "{{") || !parameterizableField.MatchString(key) {
				rewritten.WriteString(s[lastEnd:end])
				lastEnd = end
				continue
			}
			col, exists := columnByField[key]
			if !exists {
				base := slugifyCorrelationName(key)
				if base != "" {
					col = claimRecorderCorrelationName(base, usedColumns)
					columnByField[key] = col
					columns = append(columns, col)
					decoded, err := url.QueryUnescape(value)
					if err != nil {
						decoded = value
					}
					if parameterizedSecretField.MatchString(key) {
						// Parameterization changes where the captured value is
						// read from; it must not silently discard a password the
						// operator already supplied. The durable imported data
						// row retains the source-encoded bytes because template
						// substitution writes directly back into the query/form
						// wire shape. Response projection uses
						// recorded_input_secret_columns to mask these cells.
						sampleValues[col] = value
						secretColumns[col] = true
					} else {
						sampleValues[col] = decoded
					}
				}
			}
			if col != "" {
				replacement = prefix + key + "={{data." + parameterizedDataSourceName + "." + col + "}}"
			}
			rewritten.WriteString(s[lastEnd:start])
			rewritten.WriteString(replacement)
			lastEnd = end
		}
		rewritten.WriteString(s[lastEnd:])
		return rewritten.String(), true
	}

	// Every phase is scanned: a recorded sign-in may run in init.
	phases := []*[]dsl.Action{&script.Init, &script.Actions, &script.Teardown}
	targets := make([][]string, len(phases))
	bodies := make([][]string, len(phases))
	for phase, actions := range phases {
		targets[phase] = make([]string, len(*actions))
		bodies[phase] = make([]string, len(*actions))
		for i, action := range *actions {
			var complete bool
			targets[phase][i], complete = rewrite(action.Target)
			if complete && action.Body != "" {
				bodies[phase][i], complete = rewrite(action.Body)
			}
			if !complete {
				return []string{fmt.Sprintf(
					"Recorded-input parameterization was not completed and no input rewrites were applied because analysis exceeded %d bytes or %d key/value matches.",
					scanByteLimit, matchLimit,
				)}
			}
		}
	}

	if len(columns) == 0 {
		return nil
	}

	for phase, actions := range phases {
		for i := range *actions {
			(*actions)[i].Target = targets[phase][i]
			if (*actions)[i].Body != "" {
				(*actions)[i].Body = bodies[phase][i]
			}
		}
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
			"Secret-like recorded input field(s) retain their captured values for replay and should be reviewed (%s).",
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

func filterRecordedHeaders(headers map[string]string, policy headerPolicy) map[string]string {
	filtered := make(map[string]string)
	for k, v := range headers {
		if includeRecordedHeader(k, policy) {
			filtered[k] = v
		}
	}
	return filtered
}

// headerPolicy decides which recorded request headers a script keeps: the
// recorder's defaults, the session's cookie capture, and a recording
// profile's record/drop lists.
type headerPolicy struct {
	cookies bool
	profile *recordingprofile.Profile
}

func includeRecordedHeader(name string, policy headerPolicy) bool {
	if isRecorderControlHeader(name) {
		return false
	}
	if policy.profile != nil {
		if keep, decided := policy.profile.HeaderDecision(name); decided {
			// Connection-specific headers stay with the transport.
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "host", "content-length", "connection", "proxy-connection":
				return false
			}
			return keep
		}
	}
	captureCookies := policy.cookies
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "host", "content-length", "accept-encoding",
		"connection", "proxy-connection", "user-agent",
		"accept", "accept-language", "cache-control",
		"pragma", "sec-ch-ua", "sec-ch-ua-mobile",
		"sec-ch-ua-platform", "sec-fetch-dest",
		"sec-fetch-mode", "sec-fetch-site", "sec-fetch-user",
		"upgrade-insecure-requests":
		return false
	case "cookie":
		return captureCookies
	default:
		return true
	}
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
		result = boundedtext.TruncateUTF8(result, 40)
	}
	return result
}
