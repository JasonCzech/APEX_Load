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

	"github.com/jasonczech/apexload/pkg/convert/clienttimestamps"
	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/recordedshape"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
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
	// PageTitleChecks adds a body_contains check on each recorded HTML page's
	// <title>, as VuGen does by default. Nil means on.
	PageTitleChecks *bool
	// ResourceMode is "runtime" (default: static resources are dropped and
	// discovered at replay) or "in_step" (each static resource is attached to
	// the page that requested it as an explicit resource).
	ResourceMode string
	// AsyncScan collapses requests a page repeated in the background (poll,
	// long-poll, push) into background conversations. Nil means on; false
	// keeps them as recorded steps.
	AsyncScan *bool
}

// ConvertSession converts a recording with default options.
func ConvertSession(session *Session, name string) *ConvertResult {
	return ConvertSessionWithOptions(session, name, ConvertOptions{})
}

// ConvertSessionWithOptions converts a recording, applying the supplied options.
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

	normalized := normalizeRecordedRequests(session.Requests)
	recordedRequests := normalized.requests
	webSocketRequests := includedWebSocketRequests(recordedRequests)
	hasHTTPRequests := hasIncludedHTTPRequests(recordedRequests)
	if len(webSocketRequests) > 0 && !hasHTTPRequests {
		result.Script = convertWebSocketSession(session, name, webSocketRequests)
		return result
	}

	correlations, correlationErr := detectCorrelationsChecked(convertibleCorrelationRequests(recordedRequests))
	if correlationErr != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Correlation analysis was not completed and no automatic correlation rewrites were applied: %v", correlationErr))
	}
	correlationApplication := prepareRecorderCorrelationApplication(recordedRequests, correlations)
	result.Correlations = disclosureSafeCorrelations(correlations)

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
	if warning := requestimport.ApplyIntegratedAuth(script, normalized.integratedAuthHosts); warning != "" {
		result.Warnings = append(result.Warnings, warning)
	}
	recordedResourceURLs := make([]string, 0)
	recordedResourceSeen := make(map[string]struct{})

	inStep := strings.EqualFold(strings.TrimSpace(opts.ResourceMode), "in_step")
	pageActionByURL := map[string]int{}
	lastPageAction := -1
	// steps holds the recorded timing of each generated action, for the
	// async scan and WebSocket placement.
	var steps []recordedshape.Step
	for i, req := range recordedRequests {
		if req.IsStatic && !req.Excluded {
			rawURL := strings.TrimSpace(req.URL)
			if rawURL != "" {
				if _, ok := recordedResourceSeen[rawURL]; !ok {
					recordedResourceSeen[rawURL] = struct{}{}
					recordedResourceURLs = append(recordedResourceURLs, rawURL)
				}
				if inStep {
					attachRecordedResource(script, pageActionByURL, lastPageAction, rawURL, recordedRequestHeader(req, "Referer"))
				}
			}
		}
		if req.IsStatic || req.Excluded {
			continue
		}
		if len(req.WebSocketMessages) > 0 {
			continue
		}
		corrMap := recorderCorrelationVarsAvailableAtSequence(correlationApplication.vars, correlationApplication.sourceSequences, req.Seq)
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
		if len(headers.Summary) > 0 {
			action.Headers = headers.Summary
		}
		if len(headers.Repeated) > 0 {
			action.HeaderList = headers.Repeated
		}

		if req.Body != "" {
			body := req.Body
			if req.BodyEncoding == "" {
				body = applyCorrelationVars(body, corrMap)
			}
			action.Body = body
			action.BodyEncoding = req.BodyEncoding
		}

		action.Extractors = append(action.Extractors, correlationApplication.extractors[req.Seq]...)
		capture := requestimport.ReviewHTTPVersionCapture(exportedObservedHTTPVersion(req.RequestHTTPVersion), true, exportedObservedHTTPVersion(req.ResponseHTTPVersion), true, requestimport.ProxyHTTPVersionProvenance(), true, false)
		requestimport.ApplyHTTPVersionCapture(&action, capture, requestimport.SourceRef{Format: "recording", ItemID: fmt.Sprintf("request-%d", req.Seq), Pointer: "request_http_version"})
		if capture.Version != "" {
			action.HTTPVersion, _ = dsl.NewHTTPVersionOverride(capture.Version)
		}
		if capture.ReasonCode != "" {
			result.Warnings = append(result.Warnings, "Captured HTTP version evidence requires review before replay ("+capture.ReasonCode+").")
		}

		responseCapture := effectiveRecordedResponseContent(req)
		requestimport.ApplyResponseContentCapture(&action, responseCapture, requestimport.SourceRef{Format: "recording", ItemID: fmt.Sprintf("request-%d", req.Seq), Pointer: "response_content"})
		if responseCapture.ReasonCode != "" {
			result.Warnings = append(result.Warnings, "Captured response content requires review before correlation ("+responseCapture.ReasonCode+").")
		}

		if isRecordedRedirectStep(req) {
			// The browser followed this redirect itself and the target is the
			// next recorded step; following it here too would request the
			// target twice and hide the Location header from extractors.
			zero := 0
			action.MaxRedirects = &zero
			action.Assertions = append(action.Assertions, dsl.Assertion{Type: "status_code", Expected: req.StatusCode})
		} else if shouldAssertRecordedStatus(req.StatusCode) {
			action.Assertions = append(action.Assertions, dsl.Assertion{
				Type:     "status_code",
				Expected: req.StatusCode,
			})
		}
		if opts.PageTitleChecks == nil || *opts.PageTitleChecks {
			if title := requestimport.RecordedPageTitle(recordedResponseHeader(req, "Content-Type"), req.StatusCode, decodedRecordedResponseBody(req), correlationLiteralValues(correlations)); title != "" {
				action.Assertions = append(action.Assertions, dsl.Assertion{Type: "body_contains", Value: title})
			}
		}
		if findings := normalized.findings[req.Seq]; len(findings) > 0 {
			importreview.ApplyToAction(&action, findings)
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
		steps = append(steps, recordedshape.Step{
			StartedAt: recordedAt(req), Duration: time.Duration(req.ResponseTimeMs * float64(time.Millisecond)),
			URL: req.URL, Method: req.Method, ContentType: recordedResponseHeader(req, "Content-Type"),
			Page: recordedshape.IsPage(recordedResponseHeader(req, "Content-Type"), req.Headers),
		})
		if inStep && strings.Contains(strings.ToLower(recordedResponseHeader(req, "Content-Type")), "text/html") {
			lastPageAction = len(script.Actions) - 1
			pageActionByURL[strings.TrimSpace(req.URL)] = lastPageAction
		}
	}

	if len(script.Actions) == 0 {
		result.Warnings = append(result.Warnings, "no non-static requests found in recording")
	}
	if skipped := countUnappliedRecorderCorrelations(correlations); skipped > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Detected %d correlation candidate(s) left for review because confidence or encoding semantics did not permit an automatic rewrite.", skipped))
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
	}
	if len(preserved) > 0 {
		target := ensureRecorderImportPreserved(script, session.Name)
		for key, value := range preserved {
			target[key] = value
		}
	}

	if warning := clienttimestamps.Apply(script, recordedTimestampEvidence(recordedRequests)).Warning(); warning != "" {
		result.Warnings = append(result.Warnings, warning)
	}
	if _, _, err := requestimport.ApplyRuntimeFidelityReview(script, requestimport.RuntimeFidelityOptions{SourceProfile: "recording"}); err != nil {
		result.Warnings = append(result.Warnings, "Recording HTTP fidelity review could not inspect the script; repair its structure before replay.")
	}
	var sockets []recordedshape.Socket
	if hasHTTPRequests {
		sockets = recordedSockets(webSocketRequests, session.Filters.CaptureCookies)
	}
	asyncScan := opts.AsyncScan == nil || *opts.AsyncScan
	if asyncScan || len(sockets) > 0 {
		original := append([]dsl.Action(nil), script.Actions...)
		shaped := recordedshape.Shape(script, steps, sockets, recordedshape.Options{AsyncScan: asyncScan})
		if !shaped.Changed() {
			result.Warnings = append(result.Warnings, shaped.Warnings...)
		} else if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
			script.Actions = original
			result.Warnings = append(result.Warnings, "the recording's background requests and WebSocket traffic were left as recorded because the reshaped script did not validate: "+strings.Join(issues, "; "))
		} else {
			result.Warnings = append(result.Warnings, shaped.Warnings...)
		}
	}
	result.Script = script
	return result
}

// recordedAt is when a request was recorded; a request without a timestamp
// keeps its recorded order.
func recordedAt(req RecordedRequest) time.Time {
	if !req.Timestamp.IsZero() {
		return req.Timestamp
	}
	return time.Unix(0, 0).Add(time.Duration(req.Seq) * time.Millisecond)
}

// recordedSockets turns the WebSocket connections recorded alongside HTTP
// traffic into sockets for recordedshape.
func recordedSockets(requests []RecordedRequest, captureCookies bool) []recordedshape.Socket {
	sockets := make([]recordedshape.Socket, 0, len(requests))
	for _, req := range requests {
		started := recordedAt(req)
		socket := recordedshape.Socket{URL: recordedshape.WebSocketURL(req.URL), StartedAt: started, EndedAt: started, Headers: filterRecordedHeaders(req.Headers, captureCookies)}
		for index, msg := range coalesceWebSocketMessages(req.WebSocketMessages) {
			if msg.Timestamp.IsZero() {
				msg.Timestamp = started.Add(time.Duration(index+1) * time.Microsecond)
			}
			if msg.Opcode != webSocketOpcodeText && msg.Opcode != webSocketOpcodeBinary {
				continue
			}
			socket.Frames = append(socket.Frames, recordedshape.Frame{
				Send:    msg.Direction == webSocketDirectionClientToServer,
				Payload: msg.Payload,
				Base64:  webSocketDSLMessageType(msg) == "binary",
				At:      msg.Timestamp,
			})
			if msg.Timestamp.After(socket.EndedAt) {
				socket.EndedAt = msg.Timestamp
			}
		}
		sockets = append(sockets, socket)
	}
	return sockets
}

// recordedTimestampEvidence collects what the recorded responses say about
// client timestamps: the capture window and every number a response carried.
func recordedTimestampEvidence(requests []RecordedRequest) clienttimestamps.Evidence {
	evidence := clienttimestamps.Evidence{}
	for _, request := range requests {
		evidence.Observe(request.Timestamp)
		body := request.ResponseBody
		if strings.EqualFold(request.ResponseBodyEncoding, "base64") {
			if decoded, err := base64.StdEncoding.DecodeString(body); err == nil {
				body = string(decoded)
			}
		}
		evidence.AddServerText(body)
		for _, value := range request.ResponseHeaders {
			evidence.AddServerText(value)
		}
		for _, values := range request.ResponseHeadersMulti {
			for _, value := range values {
				evidence.AddServerText(value)
			}
		}
	}
	return evidence
}

func convertibleCorrelationRequests(requests []RecordedRequest) []RecordedRequest {
	filtered := make([]RecordedRequest, 0, len(requests))
	for _, req := range requests {
		if req.IsStatic || req.Excluded || len(req.WebSocketMessages) > 0 {
			continue
		}
		req.Headers = requestHeadersForBaseline(req)
		req.ResponseHeaders = responseHeadersForBaseline(req)
		req.ResponseBody = recordedResponseBodyForBaseline(req)
		filtered = append(filtered, req)
	}
	return filtered
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

type correlatedRecordedHeaderProjection struct {
	Summary  map[string]string
	Repeated dsl.HeaderList
}

func correlatedRecordedHeaders(req RecordedRequest, captureCookies bool, corrMap map[string]string) correlatedRecordedHeaderProjection {
	headers := filterRecordedHeaders(req.Headers, captureCookies)
	rawHeaders := filterRecordedHeaders(req.RawHeaders, captureCookies)
	rawNames := make([]string, 0, len(rawHeaders))
	rawNameCounts := make(map[string]int, len(rawHeaders))
	for key := range rawHeaders {
		rawNames = append(rawNames, key)
		rawNameCounts[strings.ToLower(strings.TrimSpace(key))]++
	}
	sort.Strings(rawNames)
	for _, key := range rawNames {
		if rawNameCounts[strings.ToLower(strings.TrimSpace(key))] != 1 {
			// Case-only duplicates cannot be mapped back to one disclosure-safe
			// summary deterministically. Keep the sanitized projection.
			continue
		}
		rawValue := rawHeaders[key]
		correlated := applyCorrelationVars(rawValue, corrMap)
		if correlated == rawValue {
			continue // nothing in the raw value correlated
		}
		redactedKey, redactedValue, ok, ambiguous := uniqueRecordedHeader(headers, key)
		if ambiguous {
			continue
		}
		// When the raw header value was redacted (its redacted view differs from
		// raw, i.e. it carried secret material), only adopt the raw-derived
		// correlated value if the correlation consumed the secret and left behind
		// nothing but recognized non-secret scaffolding (e.g. a "Bearer " auth
		// scheme). A substring match on the secret itself (e.g. a token inside
		// "Bearer abc<matched>xyz") would splice the un-correlated remainder of
		// the live credential into the generated script.
		if (ok && redactedValue != rawValue || !ok) && !headerResidualIsSafe(correlated) {
			continue
		}
		if ok {
			headers[redactedKey] = correlated
		} else {
			headers[key] = correlated
		}
	}
	for key, value := range headers {
		headers[key] = applyCorrelationVars(value, corrMap)
	}
	return correlatedRecordedHeaderProjection{
		Summary:  headers,
		Repeated: correlatedRepeatedRecordedHeaders(req, captureCookies, corrMap),
	}
}

// uniqueRecordedHeader resolves one logical header name without allowing
// case-only map collisions to choose a disclosure-safe value nondeterministically.
func uniqueRecordedHeader(headers map[string]string, name string) (key, value string, found, ambiguous bool) {
	for _, candidate := range sortedStringMapKeys(headers) {
		if !strings.EqualFold(candidate, name) {
			continue
		}
		if found {
			return "", "", false, true
		}
		key, value, found = candidate, headers[candidate], true
	}
	return key, value, found, false
}

func sortedStringMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// correlatedRepeatedRecordedHeaders preserves duplicate request-header
// occurrences in HeaderList. The legacy Headers map intentionally remains a
// first-value summary; HTTP replay gives HeaderList precedence for names found
// there, so the list is the lossless wire representation for repeated values.
func correlatedRepeatedRecordedHeaders(req RecordedRequest, captureCookies bool, corrMap map[string]string) dsl.HeaderList {
	names := repeatedRecordedHeaderNames(req.HeadersMulti, req.RawHeadersMulti)
	result := make(dsl.HeaderList, 0)
	for _, name := range names {
		if !includeRecordedHeader(name, captureCookies) {
			continue
		}
		redactedValues, _ := recordedHeaderValues(req.HeadersMulti, name)
		rawValues, _ := recordedHeaderValues(req.RawHeadersMulti, name)
		count := len(redactedValues)
		if len(rawValues) > count {
			count = len(rawValues)
		}
		if count <= 1 {
			continue
		}
		entries := make(dsl.HeaderList, 0, count)
		complete := true
		for index := 0; index < count; index++ {
			redactedValue, hasRedacted := headerValueAt(redactedValues, index)
			rawValue, hasRaw := headerValueAt(rawValues, index)
			value := redactedValue
			hasValue := hasRedacted
			if hasRaw {
				correlated := applyCorrelationVars(rawValue, corrMap)
				if correlated != rawValue {
					// Match the single-value safety rule: when sanitization changed a
					// secret-bearing value, use the raw-derived rewrite only if the
					// correlation consumed all secret material except known scaffolding.
					if !hasRedacted || redactedValue == rawValue || headerResidualIsSafe(correlated) {
						value = correlated
						hasValue = true
					}
				} else if !hasValue {
					// Raw multi-values are correlation-only. Do not introduce an
					// unmatched raw value when no disclosure-safe projection exists.
					// Also reject the whole list: an incomplete HeaderList would shadow
					// the summary map and silently drop the omitted occurrence on replay.
					complete = false
					break
				}
			} else if hasRedacted {
				value = applyCorrelationVars(redactedValue, corrMap)
			}
			if hasValue {
				entries = append(entries, dsl.HeaderEntry{Name: name, Value: value})
			}
		}
		if complete && len(entries) == count {
			result = append(result, entries...)
		}
	}
	return result
}

func repeatedRecordedHeaderNames(headerSets ...map[string][]string) []string {
	canonical := make(map[string]string)
	for _, headers := range headerSets {
		for name := range headers {
			normalized := strings.ToLower(strings.TrimSpace(name))
			if normalized == "" {
				continue
			}
			if _, exists := canonical[normalized]; !exists {
				canonical[normalized] = name
			}
		}
	}
	normalizedNames := make([]string, 0, len(canonical))
	for normalized := range canonical {
		normalizedNames = append(normalizedNames, normalized)
	}
	sort.Strings(normalizedNames)
	names := make([]string, 0, len(normalizedNames))
	for _, normalized := range normalizedNames {
		names = append(names, canonical[normalized])
	}
	return names
}

func recordedHeaderValues(headers map[string][]string, name string) ([]string, bool) {
	for candidate, values := range headers {
		if strings.EqualFold(candidate, name) {
			return values, true
		}
	}
	return nil, false
}

func headerValueAt(values []string, index int) (string, bool) {
	if index < 0 || index >= len(values) {
		return "", false
	}
	return values[index], true
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
	for _, char := range stripped {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || char == ' ' || char == '\t' || char == '=' {
			continue
		}
		return false
	}
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
	requests := normalizeRecordedRequests(session.Requests).requests
	observations := make([]correlate.Observation, 0, len(requests))
	actionIdx := 0
	for _, req := range requests {
		if req.IsStatic || req.Excluded || len(req.WebSocketMessages) > 0 {
			continue
		}
		observations = append(observations, correlate.Observation{
			ID:                     actionIdx,
			RequestURL:             req.URL,
			RequestBody:            req.Body,
			RequestBodyTransformed: req.BodyEncoding != "",
			RequestHeaders:         requestHeadersForBaseline(req),
			ResponseBody:           recordedResponseBodyForBaseline(req),
			ResponseHeaders:        responseHeadersForBaseline(req),
		})
		actionIdx++
	}
	return observations
}

func requestHeadersForBaseline(req RecordedRequest) map[string]string {
	if len(req.RawHeadersMulti) > 0 {
		return multiHeadersForBaseline(req.RawHeadersMulti)
	}
	if len(req.RawHeaders) > 0 {
		return withoutRecorderControlHeaders(req.RawHeaders)
	}
	if len(req.HeadersMulti) > 0 {
		return multiHeadersForBaseline(req.HeadersMulti)
	}
	return withoutRecorderControlHeaders(req.Headers)
}

func responseHeadersForBaseline(req RecordedRequest) map[string]string {
	return requestimport.ResponseContentBaselineHeaders(recordedResponseHeaders(req), effectiveRecordedResponseContent(req))
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
	correlations, _ := detectCorrelationsChecked(requests)
	return correlations
}

func detectCorrelationsChecked(requests []RecordedRequest) ([]Correlation, error) {
	observations := make([]correlate.Observation, 0, len(requests))
	for _, req := range requests {
		observations = append(observations, correlate.Observation{
			ID:                     req.Seq,
			RequestURL:             req.URL,
			RequestBody:            req.Body,
			RequestBodyTransformed: req.BodyEncoding != "",
			RequestHeaders:         requestHeadersForBaseline(req),
			ResponseBody:           recordedResponseBodyForBaseline(req),
			ResponseHeaders:        responseHeadersForBaseline(req),
		})
	}
	shared, err := correlate.DetectChecked(observations)
	if err != nil {
		return nil, err
	}
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
	return uniquifyCorrelationNames(correlations), nil
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

func buildCorrelationMapForSequence(correlations []Correlation, sequence int) map[string]string {
	available := make([]Correlation, 0, len(correlations))
	for _, correlation := range correlations {
		if correlation.FoundInSeq < sequence {
			available = append(available, correlation)
		}
	}
	return buildCorrelationMap(available)
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

const recorderManualCorrelationReplacement = "manual_value_replacement_required"

type recorderCorrelationUse struct {
	raw                bool
	encoded            bool
	unsupportedEncoded bool
}

type recorderCorrelationPlan struct {
	index            int
	automatic        bool
	value            string
	queryEncoded     string
	usage            recorderCorrelationUse
	encodedConvert   string
	encodedSupported bool
	rawMapped        bool
	encodedMapped    bool
	encodedName      string
}

type recorderCorrelationApplication struct {
	vars            map[string]string
	sourceSequences map[string]int
	extractors      map[int][]dsl.Extractor
}

// prepareRecorderCorrelationApplication mirrors the sample/Salesforce
// correlation rewrite contract: raw and canonical QueryEscape consumers use
// separate variables when needed, while path/non-canonical spellings remain
// untouched and explicitly require review.
func prepareRecorderCorrelationApplication(requests []RecordedRequest, correlations []Correlation) recorderCorrelationApplication {
	return prepareRecorderCorrelationApplicationWithExtractionLimit(requests, correlations, protocol.MaxExtractionBytes)
}

func prepareRecorderCorrelationApplicationWithExtractionLimit(requests []RecordedRequest, correlations []Correlation, extractionLimit int) recorderCorrelationApplication {
	application := recorderCorrelationApplication{
		vars:            make(map[string]string, len(correlations)*2),
		sourceSequences: make(map[string]int, len(correlations)*2),
		extractors:      make(map[int][]dsl.Extractor),
	}
	usedNames := make(map[string]bool, len(correlations)*2)
	sourceValidator := correlate.NewExactQueryEncodingValidator()
	requestsBySequence := indexRecordedRequestsBySequence(requests)
	for _, correlation := range correlations {
		if name := strings.TrimSpace(correlation.Name); name != "" {
			usedNames[name] = true
		}
	}

	plans := make([]recorderCorrelationPlan, 0, len(correlations))
	for index := range correlations {
		correlation := &correlations[index]
		automatic := correlate.HighConfidence(toSharedCorrelation(*correlation))
		if !automatic {
			correlation.ReviewAction = "manual_review_required"
		}
		value := correlation.Value
		if value == "" {
			value = correlation.SampleValue
		}
		if value == "" {
			correlation.ReviewAction = "manual_review_required"
			continue
		}
		usage := recorderCorrelationValueUsage(requestsBySequence, *correlation, value)
		queryEncoded := correlate.QueryEncodedFormOf(value)
		encodedConvert, encodedSupported := correlate.EncodedReuseExtractorConvert(correlation.Convert)
		encodedSupported = encodedSupported &&
			correlate.CorrelationExtractionValueFits(correlation.Name+"_encoded", queryEncoded)
		if encodedSupported && strings.EqualFold(strings.TrimSpace(correlation.Convert), "url_decode") {
			encodedSupported = recorderCorrelationSourceUsesExactQueryEncoding(sourceValidator, requestsBySequence, *correlation, queryEncoded)
		}
		plans = append(plans, recorderCorrelationPlan{
			index:            index,
			automatic:        automatic,
			value:            value,
			queryEncoded:     queryEncoded,
			usage:            usage,
			encodedConvert:   encodedConvert,
			encodedSupported: encodedSupported,
		})
	}

	ambiguousLiterals, ambiguousPlans := recorderCorrelationReplacementCollisions(requests, plans)
	for planIndex := range ambiguousPlans {
		correlations[plans[planIndex].index].ReviewAction = recorderManualCorrelationReplacement
	}

	sourceExtractionBytes := make(map[int]int)
	for index := range plans {
		plan := &plans[index]
		correlation := &correlations[plan.index]
		if plan.usage.unsupportedEncoded || (plan.usage.encoded && !plan.encodedSupported) {
			correlation.ReviewAction = recorderManualCorrelationReplacement
		}

		// Omit ambiguous literals entirely. Overlapping claims can otherwise rewrite
		// a larger correlation with a smaller variable before its own source extractor
		// is available. Reserve both the base extractor and any encoded sibling before
		// exposing either replacement so an action never exceeds the DSL ceiling.
		plan.rawMapped = plan.automatic && plan.usage.raw && !ambiguousLiterals[plan.value]
		plan.encodedMapped = plan.automatic && plan.usage.encoded && plan.encodedSupported && plan.queryEncoded != "" && !ambiguousLiterals[plan.queryEncoded]
		requiredExtractors := 0
		if plan.rawMapped || plan.encodedMapped {
			requiredExtractors = 1
		}
		if plan.rawMapped && plan.encodedMapped {
			requiredExtractors++
		}
		if plan.encodedMapped {
			plan.encodedName = correlation.Name
			if plan.rawMapped {
				plan.encodedName = claimRecorderCorrelationName(correlation.Name+"_encoded", usedNames)
			}
		}
		if len(application.extractors[correlation.FoundInSeq])+requiredExtractors > dsl.MaxActionExtractors {
			plan.rawMapped = false
			plan.encodedMapped = false
			correlation.PreApplied = false
			correlation.ReviewAction = recorderManualCorrelationReplacement
			continue
		}
		if requiredExtractors == 0 {
			correlation.PreApplied = false
			if correlation.ReviewAction == "" {
				correlation.ReviewAction = recorderManualCorrelationReplacement
			}
			continue
		}
		requiredBytes := 0
		baseValue := plan.value
		if !plan.rawMapped {
			baseValue = plan.queryEncoded
		}
		baseBytes, baseFits := correlate.CorrelationExtractionEntryBytes(correlation.Name, baseValue)
		if !baseFits {
			plan.rawMapped = false
			plan.encodedMapped = false
			correlation.PreApplied = false
			correlation.ReviewAction = recorderManualCorrelationReplacement
			continue
		}
		requiredBytes += baseBytes
		if plan.rawMapped && plan.encodedMapped {
			encodedBytes, encodedFits := correlate.CorrelationExtractionEntryBytes(plan.encodedName, plan.queryEncoded)
			if !encodedFits {
				plan.rawMapped = false
				plan.encodedMapped = false
				correlation.PreApplied = false
				correlation.ReviewAction = recorderManualCorrelationReplacement
				continue
			}
			requiredBytes += encodedBytes
		}
		if requiredBytes > extractionLimit || sourceExtractionBytes[correlation.FoundInSeq] > extractionLimit-requiredBytes {
			plan.rawMapped = false
			plan.encodedMapped = false
			correlation.PreApplied = false
			correlation.ReviewAction = recorderManualCorrelationReplacement
			continue
		}
		sourceExtractionBytes[correlation.FoundInSeq] += requiredBytes

		if plan.rawMapped {
			application.vars[plan.value] = "{{var." + correlation.Name + "}}"
			application.sourceSequences[plan.value] = correlation.FoundInSeq
		}
		if plan.encodedMapped {
			application.vars[plan.queryEncoded] = "{{var." + plan.encodedName + "}}"
			application.sourceSequences[plan.queryEncoded] = correlation.FoundInSeq
			plan.encodedMapped = true
		}

		base := dsl.Extractor{
			Name:       correlation.Name,
			Source:     correlation.Source,
			Type:       correlation.ExtractType,
			Expression: correlation.Expression,
			Convert:    correlation.Convert,
			OnMiss:     "fail",
		}
		switch {
		case plan.rawMapped && plan.encodedMapped:
			application.extractors[correlation.FoundInSeq] = append(application.extractors[correlation.FoundInSeq], base, dsl.Extractor{
				Name:       plan.encodedName,
				Source:     correlation.Source,
				Type:       correlation.ExtractType,
				Expression: correlation.Expression,
				Convert:    plan.encodedConvert,
				OnMiss:     "fail",
			})
		case plan.rawMapped:
			application.extractors[correlation.FoundInSeq] = append(application.extractors[correlation.FoundInSeq], base)
		case plan.encodedMapped:
			base.Convert = plan.encodedConvert
			correlation.Convert = plan.encodedConvert
			application.extractors[correlation.FoundInSeq] = append(application.extractors[correlation.FoundInSeq], base)
		}
		correlation.PreApplied = true
	}

	return application
}

const maxRecorderCorrelationReplacementSurfaces = 100_000

func recorderCorrelationReplacementCollisions(requests []RecordedRequest, plans []recorderCorrelationPlan) (map[string]bool, map[int]bool) {
	return recorderCorrelationReplacementCollisionsWithSurfaceLimit(requests, plans, maxRecorderCorrelationReplacementSurfaces)
}

func recorderCorrelationReplacementCollisionsWithSurfaceLimit(requests []RecordedRequest, plans []recorderCorrelationPlan, surfaceLimit int) (map[string]bool, map[int]bool) {
	const comparisonLimit = 4_000_000

	type literalClaim struct {
		planIndex int
		literal   string
	}
	claims := make([]literalClaim, 0, len(plans)*2)
	for index, plan := range plans {
		seen := make(map[string]bool)
		add := func(literal string) {
			if literal == "" || seen[literal] {
				return
			}
			seen[literal] = true
			claims = append(claims, literalClaim{planIndex: index, literal: literal})
		}
		if plan.usage.raw {
			add(plan.value)
		}
		if plan.usage.encoded {
			add(plan.queryEncoded)
		}
		if plan.usage.unsupportedEncoded {
			for _, spelling := range recorderUnsupportedCorrelationEncodings(plan.value, plan.queryEncoded) {
				add(spelling)
			}
		}
	}

	overlapBudget := paramize.NewLiteralOverlapSearchBudget()
	ambiguousLiterals := make(map[string]bool)
	ambiguousPlans := make(map[int]bool)
	markAutomaticClaimsAmbiguous := func() {
		for _, claim := range claims {
			if !plans[claim.planIndex].automatic {
				continue
			}
			ambiguousLiterals[claim.literal] = true
			ambiguousPlans[claim.planIndex] = true
		}
	}
	// When a bound is exhausted at claims[firstUnchecked], every pair involving
	// an earlier claim has been compared, so those claims keep their outcome.
	// Plans owning any unchecked claim are suppressed whole.
	markUncheckedClaimsAmbiguous := func(firstUnchecked int) {
		unchecked := make(map[int]bool)
		for _, claim := range claims[firstUnchecked:] {
			unchecked[claim.planIndex] = true
		}
		for _, claim := range claims {
			if unchecked[claim.planIndex] && plans[claim.planIndex].automatic {
				ambiguousLiterals[claim.literal] = true
				ambiguousPlans[claim.planIndex] = true
			}
		}
	}
	requestSurfaces, surfacesComplete := recorderRequestReplacementSurfaces(requests, surfaceLimit)
	if !surfacesComplete {
		// Surface collection is safety-critical too. Do not build one aggregate
		// string (recordings can retain hundreds of MiB), and do not analyze only
		// a bounded prefix: either all editable surfaces are represented by string
		// references or every automatic claim is suppressed.
		markAutomaticClaimsAmbiguous()
		return ambiguousLiterals, ambiguousPlans
	}
	occurrences := overlapBudget.NewLiteralOccurrenceIndexForSurfaces(requestSurfaces)
	comparisons := 0
	for leftIndex, left := range claims {
		for _, right := range claims[leftIndex+1:] {
			if left.planIndex == right.planIndex {
				continue
			}
			if !plans[left.planIndex].automatic && !plans[right.planIndex].automatic {
				continue
			}
			comparisons++
			if comparisons > comparisonLimit {
				markUncheckedClaimsAmbiguous(leftIndex)
				return ambiguousLiterals, ambiguousPlans
			}
			overlaps := false
			switch {
			case plans[left.planIndex].automatic && plans[right.planIndex].automatic:
				overlaps = overlapBudget.Contains(left.literal, right.literal) || overlapBudget.Contains(right.literal, left.literal)
			case plans[left.planIndex].automatic:
				// Only the automatic literal can mutate the script. A longer
				// review-only value is at risk when it contains that literal; the
				// inverse is safe because longest-first replaces the automatic value
				// as a whole (for example a contentUrl containing a review-only id).
				overlaps = overlapBudget.Contains(right.literal, left.literal)
			case plans[right.planIndex].automatic:
				overlaps = overlapBudget.Contains(left.literal, right.literal)
			}
			if overlapBudget.Exhausted() {
				markUncheckedClaimsAmbiguous(leftIndex)
				return ambiguousLiterals, ambiguousPlans
			}
			if !overlaps {
				overlaps = occurrences.PartiallyOverlap(left.literal, right.literal)
				if overlapBudget.Exhausted() {
					markUncheckedClaimsAmbiguous(leftIndex)
					return ambiguousLiterals, ambiguousPlans
				}
			}
			if !overlaps {
				continue
			}
			ambiguousLiterals[left.literal] = true
			ambiguousLiterals[right.literal] = true
			ambiguousPlans[left.planIndex] = true
			ambiguousPlans[right.planIndex] = true
		}
	}
	return ambiguousLiterals, ambiguousPlans
}

// recorderRequestReplacementSurfaces returns references to the fields that
// recorder conversion can rewrite. Keeping surfaces separate prevents overlap
// detection across field/action boundaries and avoids copying a retained
// recording (up to hundreds of MiB) into one aggregate string.
func recorderRequestReplacementSurfaces(requests []RecordedRequest, limit int) ([]string, bool) {
	if limit <= 0 || len(requests) > maxRecordedRequests {
		return nil, false
	}
	capacity := min(len(requests)*3, limit)
	surfaces := make([]string, 0, capacity)
	seen := make(map[string]struct{}, capacity)
	add := func(surface string) bool {
		if surface == "" {
			return true
		}
		if _, exists := seen[surface]; exists {
			return true
		}
		if len(surfaces) >= limit {
			return false
		}
		seen[surface] = struct{}{}
		surfaces = append(surfaces, surface)
		return true
	}
	for _, request := range requests {
		if request.IsStatic || request.Excluded || len(request.WebSocketMessages) > 0 {
			continue
		}
		if !add(recordedRequestTarget(request, "")) {
			return nil, false
		}
		if request.BodyEncoding == "" && !add(request.Body) {
			return nil, false
		}
		for _, headers := range []map[string]string{request.Headers, request.RawHeaders} {
			names := make([]string, 0, len(headers))
			for name := range headers {
				if includeRecordedHeader(name, true) {
					names = append(names, name)
				}
			}
			sort.Strings(names)
			for _, name := range names {
				if !add(headers[name]) {
					return nil, false
				}
			}
		}
		for _, headers := range []map[string][]string{request.HeadersMulti, request.RawHeadersMulti} {
			names := make([]string, 0, len(headers))
			for name := range headers {
				if includeRecordedHeader(name, true) {
					names = append(names, name)
				}
			}
			sort.Strings(names)
			for _, name := range names {
				for _, value := range headers[name] {
					if !add(value) {
						return nil, false
					}
				}
			}
		}
	}
	return surfaces, true
}

func recorderCorrelationValueUsage(requestsBySequence map[int]*RecordedRequest, correlation Correlation, value string) recorderCorrelationUse {
	usage := recorderCorrelationUse{}
	queryEncoded := correlate.QueryEncodedFormOf(value)
	unsupported := recorderUnsupportedCorrelationEncodings(value, queryEncoded)
	for _, sequence := range correlation.UsedInSeqs {
		request := requestsBySequence[sequence]
		if request == nil {
			continue
		}
		surfaces := []string{request.URL}
		if request.BodyEncoding == "" {
			surfaces = append(surfaces, request.Body)
		} else if strings.Contains(request.Body, value) || containsAnyLiteral(request.Body, append([]string{queryEncoded}, unsupported...)) {
			usage.unsupportedEncoded = true
		}
		for _, headerValue := range requestHeadersForBaseline(*request) {
			surfaces = append(surfaces, headerValue)
		}
		for _, surface := range surfaces {
			if strings.Contains(surface, value) {
				usage.raw = true
			}
			if queryEncoded != "" && strings.Contains(surface, queryEncoded) {
				usage.encoded = true
			}
			if containsAnyLiteral(surface, unsupported) {
				usage.unsupportedEncoded = true
			}
		}
	}
	return usage
}

func recorderUnsupportedCorrelationEncodings(value, queryEncoded string) []string {
	seen := map[string]bool{value: true, queryEncoded: true}
	forms := make([]string, 0, 8)
	appendForm := func(form string) {
		if form == "" || seen[form] {
			return
		}
		seen[form] = true
		forms = append(forms, form)
	}
	for _, form := range correlate.EncodedFormsOf(value) {
		appendForm(form)
		appendForm(recorderLowerPercentEscapes(form))
	}
	appendForm(recorderLowerPercentEscapes(queryEncoded))
	return forms
}

func recorderLowerPercentEscapes(value string) string {
	encoded := []byte(value)
	for index := 0; index+2 < len(encoded); index++ {
		if encoded[index] != '%' {
			continue
		}
		for offset := 1; offset <= 2; offset++ {
			if encoded[index+offset] >= 'A' && encoded[index+offset] <= 'F' {
				encoded[index+offset] += 'a' - 'A'
			}
		}
		index += 2
	}
	return string(encoded)
}

func recorderCorrelationSourceUsesExactQueryEncoding(validator *correlate.ExactQueryEncodingValidator, requestsBySequence map[int]*RecordedRequest, correlation Correlation, queryEncoded string) bool {
	if queryEncoded == "" {
		return false
	}
	request := requestsBySequence[correlation.FoundInSeq]
	if request == nil {
		return false
	}
	return validator.Matches(correlation.FoundInSeq, request.ResponseBody, responseHeadersForBaseline(*request), toSharedCorrelation(correlation), queryEncoded)
}

func indexRecordedRequestsBySequence(requests []RecordedRequest) map[int]*RecordedRequest {
	indexed := make(map[int]*RecordedRequest, len(requests))
	for index := range requests {
		sequence := requests[index].Seq
		if _, exists := indexed[sequence]; !exists {
			// Preserve the legacy first-match behavior if malformed input repeats a
			// sequence. Checked correlation detection rejects duplicate included IDs;
			// this also keeps excluded/static entries deterministic.
			indexed[sequence] = &requests[index]
		}
	}
	return indexed
}

func containsAnyLiteral(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if candidate != "" && strings.Contains(value, candidate) {
			return true
		}
	}
	return false
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

func recorderCorrelationVarsAvailableAtSequence(vars map[string]string, sourceSequences map[string]int, sequence int) map[string]string {
	available := make(map[string]string, len(vars))
	for literal, replacement := range vars {
		if sourceSequence, exists := sourceSequences[literal]; exists && sourceSequence < sequence {
			available[literal] = replacement
		}
	}
	return available
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

	targets := make([]string, len(script.Actions))
	bodies := make([]string, len(script.Actions))
	for i := range script.Actions {
		var complete bool
		targets[i], complete = rewrite(script.Actions[i].Target)
		if !complete {
			return []string{fmt.Sprintf(
				"Recorded-input parameterization was not completed and no input rewrites were applied because analysis exceeded %d bytes or %d key/value matches.",
				scanByteLimit, matchLimit,
			)}
		}
		if script.Actions[i].Body != "" {
			bodies[i], complete = rewrite(script.Actions[i].Body)
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

	for i := range script.Actions {
		script.Actions[i].Target = targets[i]
		if script.Actions[i].Body != "" {
			script.Actions[i].Body = bodies[i]
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

func filterRecordedHeaders(headers map[string]string, captureCookies bool) map[string]string {
	filtered := make(map[string]string)
	for k, v := range headers {
		if includeRecordedHeader(k, captureCookies) {
			filtered[k] = v
		}
	}
	return filtered
}

func includeRecordedHeader(name string, captureCookies bool) bool {
	if isRecorderControlHeader(name) {
		return false
	}
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

func truncate(s string, maxLen int) string {
	return correlate.Truncate(s, maxLen)
}

// recordedResponseHeader returns a recorded response header, ignoring case.
func recordedResponseHeader(req RecordedRequest, name string) string {
	for key, value := range req.ResponseHeaders {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	for key, values := range req.ResponseHeadersMulti {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func decodedRecordedResponseBody(req RecordedRequest) string {
	if strings.EqualFold(req.ResponseBodyEncoding, "base64") {
		if decoded, err := base64.StdEncoding.DecodeString(req.ResponseBody); err == nil {
			return string(decoded)
		}
	}
	return req.ResponseBody
}

// correlationLiteralValues lists the recorded values the correlation pass found.
func correlationLiteralValues(correlations []Correlation) []string {
	values := make([]string, 0, len(correlations))
	for _, corr := range correlations {
		values = append(values, corr.Value)
	}
	return values
}

// attachRecordedResource adds a static request to the page that requested
// it: the page whose URL matches the Referer, otherwise the most recent page.
func attachRecordedResource(script *dsl.Script, pageActionByURL map[string]int, lastPage int, rawURL, referer string) {
	page := lastPage
	if index, ok := pageActionByURL[referer]; ok && referer != "" {
		page = index
	}
	if page < 0 || page >= len(script.Actions) {
		return
	}
	action := &script.Actions[page]
	if action.Params == nil {
		action.Params = map[string]any{}
	}
	resources, _ := action.Params["resources"].([]any)
	if len(resources) >= httpprotocol.MaxPageResources {
		return
	}
	resource := map[string]any{"url": rawURL}
	if referer != "" {
		resource["referer"] = referer
	}
	action.Params["resources"] = append(resources, resource)
}

// recordedRequestHeader returns a recorded request header, ignoring case.
func recordedRequestHeader(req RecordedRequest, name string) string {
	for key, value := range req.Headers {
		if strings.EqualFold(key, name) {
			return strings.TrimSpace(value)
		}
	}
	for key, values := range req.HeadersMulti {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return strings.TrimSpace(values[0])
		}
	}
	return ""
}
