package sample

// har.go — split from converter.go (same package, behavior-preserving).

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/httpcontract"
)

const maxHARDecodedContentBytes = requestimport.MaxResponseContentStorageBytes

// HARCorrelationBaseline returns the same application exchanges
// that HAR conversion consumes under the selected asset filter, projected as raw record-vs-replay
// observations. The caller owns disclosure and persistence; this helper must
// never be used in a public conversion response because the observations can
// contain source credentials.
func HARCorrelationBaseline(content string, includeStaticAssets bool) ([]correlate.Observation, error) {
	sources, err := harObservations(content, includeStaticAssets, false)
	if err != nil {
		return nil, err
	}
	observations := make([]correlate.Observation, 0, len(sources))
	for _, source := range sources {
		observations = append(observations, source.Observation)
	}
	return observations, nil
}

// HARSourceObservation is a private source observation with the request method.
type HARSourceObservation struct {
	correlate.Observation
	Method string
}

// HARSourceObservations shares HAR decoding with import while retaining original
// one-based entry IDs for source-aware adapters. Like HARCorrelationBaseline,
// these observations contain source values and must remain private.
func HARSourceObservations(content string, includeStaticAssets bool) ([]HARSourceObservation, error) {
	return harObservations(content, includeStaticAssets, true)
}

func harObservations(content string, includeStaticAssets, sourceIDs bool) ([]HARSourceObservation, error) {
	var diagnostics diag.Collector
	exchanges, _, _, _, err := parseHAR(content, includeStaticAssets, &diagnostics)
	if err != nil {
		return nil, err
	}
	observations := make([]HARSourceObservation, 0, len(exchanges))
	for index, exchange := range exchanges {
		id := index
		if sourceIDs {
			id = exchange.SourceEntryID
		}
		observation := correlate.Observation{
			ID:                     id,
			RequestURL:             exchange.URL,
			RequestBody:            exchange.Body,
			RequestBodyTransformed: exchange.BodyEncoding != "",
			RequestHeaders:         correlationObservationHeaders(exchange.Headers, exchange.HeaderList),
		}
		if exchange.Response != nil {
			observation.ResponseBody = exchange.Response.Body
			observation.ResponseHeaders = requestimport.ResponseContentBaselineHeaders(correlationObservationHeaders(exchange.Response.Headers, exchange.Response.HeaderList), exchange.Response.ContentCapture)
			observation.ResponseStatus = exchange.Response.Status
		}
		observations = append(observations, HARSourceObservation{Observation: observation, Method: exchange.Method})
	}
	return observations, nil
}

func parseHAR(content string, includeStaticAssets bool, diagnostics *diag.Collector) ([]exchange, []string, int, int, error) {
	return parseHARWithMode(content, includeStaticAssets, requestimport.HTTPVersionModeUpgrade, diagnostics)
}

// parseHARWithMode parses a HAR, mapping each observed HTTP version to a leaf
// policy by mode (requestimport.HTTPVersionMode*).
func parseHARWithMode(content string, includeStaticAssets bool, mode string, diagnostics *diag.Collector) ([]exchange, []string, int, int, error) {
	var payload any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(content, "\uFEFF")), &payload); err != nil {
		return nil, nil, 0, 0, fmt.Errorf("failed to parse HAR JSON: %w", err)
	}
	items, err := extractHARItems(payload)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	legacyRecorder := false
	if root, ok := payload.(map[string]any); ok {
		if log, ok := root["log"].(map[string]any); ok {
			root = log
		}
		if creator, ok := root["creator"].(map[string]any); ok {
			legacyRecorder = creator["name"] == "APEX Load Recorder"
		}
	}
	exchanges := make([]exchange, 0, len(items))
	filtered := 0
	skipped := 0
	warnings := []string{}
	h3Advertised := map[string]bool{}
	revalidations := 0
	h3Used := map[string]bool{}
	// Per origin: final h3 requests mapped by mode, and draft tokens seen.
	h3Mapped := map[string]int{}
	h3Drafts := map[string]int{}
	var localEntries, braceEntries []int
	localSchemes := map[string]int{}
	requestDecodeBudget := int64(maxDecodedHARRequestBodiesBytes)
	for index, item := range items {
		if origin := harOriginAdvertisingH3(item); origin != "" {
			h3Advertised[origin] = true
		}
		if origin := harOriginUsingH3(item); origin != "" {
			h3Used[origin] = true
		}
		if !includeStaticAssets && isStaticHAREntry(item) {
			filtered++
			continue
		}
		requestData, _ := item["request"].(map[string]any)
		if strings.EqualFold(strings.TrimSpace(stringValue(requestData["method"])), http.MethodConnect) {
			diagnostics.Add(diag.Info, "HAR proxy tunnel", fmt.Sprintf("HAR entry %d", index+1), "proxy CONNECT transport setup was excluded from application actions; configure replay proxy policy separately")
			skipped++
			continue
		}
		if scheme := harLocalRequestScheme(requestData); scheme != "" {
			// The page made this URL itself (an object URL or inline data);
			// nothing went on the network, so replay has nothing to send.
			localEntries = append(localEntries, index+1)
			localSchemes[scheme+":"]++
			skipped++
			continue
		}
		if isHARWebSocketUpgrade(item) {
			// The connection becomes WebSocket steps (see harWebSockets),
			// not an HTTP request.
			diagnostics.Add(diag.Info, "HAR websocket upgrade", fmt.Sprintf("HAR entry %d", index+1), webSocketHARDiagnostic(item))
			skipped++
			continue
		}
		ex, err := parseHARItemWithProvenance(item, diagnostics, index, legacyRecorder)
		if err != nil {
			diagnostics.Add(diag.Warning, "HAR entry skipped", fmt.Sprintf("HAR entry %d", index+1), err.Error())
			warnings = append(warnings, fmt.Sprintf("HAR entry %d was skipped: %s", index+1, err.Error()))
			skipped++
			continue
		}
		normalizeHARRequestContentEncoding(&ex, &requestDecodeBudget, diagnostics, fmt.Sprintf("HAR entry %d", index+1))
		if escaped, changed := escapeLiteralTemplateBraces(ex.URL); changed {
			ex.URL = escaped
			braceEntries = append(braceEntries, index+1)
		}
		if ex.CacheValidatorsDropped {
			revalidations++
		}
		responseData, _ := item["response"].(map[string]any)
		requestVersion, requestPresent := requestData["httpVersion"]
		responseVersion, responsePresent := responseData["httpVersion"]
		provenance, provenancePresent := item[requestimport.HARHTTPVersionProvenanceKey]
		capture := requestimport.ReviewHTTPVersionCapture(requestVersion, requestPresent, responseVersion, responsePresent, provenance, provenancePresent, legacyRecorder)
		// The observation stays in http_version_capture; the leaf policy is
		// the mode's mapping of it.
		ex.HTTPVersion = requestimport.HTTPVersionPolicyForCapture(capture, mode)
		if ex.HTTPVersion == httpprotocol.VersionHTTP3 && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(ex.URL)), "https://") {
			// HTTP/3 serves only https origins, so exact 3 cannot represent
			// this entry; it keeps the inherited policy instead of failing
			// the whole import.
			ex.HTTPVersion = ""
			diagnostics.Add(diag.Warning, "HTTP version", fmt.Sprintf("HAR entry %d", index+1), "The capture records HTTP/3 for a cleartext URL, which HTTP/3 cannot serve; the request keeps the script's inherited HTTP version policy.")
		}
		action := dsl.Action{Metadata: ex.Metadata}
		requestimport.ApplyHTTPVersionCapture(&action, capture, requestimport.SourceRef{Format: "har", Pointer: fmt.Sprintf("log.entries[%d].request.httpVersion", index)})
		ex.Metadata = action.Metadata
		if observation, ok := ex.Metadata["http_version_capture"].(map[string]any); ok && capture.Version != "" {
			observation["mode"] = mode
		}
		if origin := harEntryOrigin(requestData); origin != "" {
			switch {
			case capture.Version == httpprotocol.VersionHTTP3 && (ex.HTTPVersion != "" || mode == requestimport.HTTPVersionModeInherit):
				// A cleartext entry exact mode could not map is reported on
				// its own, not as mapped.
				h3Mapped[origin]++
			case capture.ReasonCode == requestimport.ReasonHTTP3DraftToken:
				h3Drafts[origin]++
			}
		}
		if capture.Provenance == "unavailable" {
			delete(ex.Metadata, "har.request.http_version")
			delete(ex.Metadata, "har.response.http_version")
		}
		if provenancePresent && capture.ReasonCode != "malformed_http_version_provenance" {
			ex.Metadata["har.http_version_provenance"] = requestimport.ProxyHTTPVersionProvenance()
		}
		switch {
		case capture.ReasonCode == requestimport.ReasonHTTP3DraftToken:
			diagnostics.Add(diag.Warning, "HTTP version", fmt.Sprintf("HAR entry %d", index+1), "The capture used a draft HTTP/3 version (http3_draft_token); replay uses the script's inherited HTTP version policy until final HTTP/3 is chosen.")
		case requestimport.HTTPVersionCaptureQualityOnly(capture.ReasonCode):
			diagnostics.Add(diag.Warning, "HTTP version", fmt.Sprintf("HAR entry %d", index+1), "The capture does not record a replayable HTTP version ("+capture.ReasonCode+"); replay uses the script's inherited HTTP version policy.")
		case capture.ReasonCode != "":
			diagnostics.Add(diag.Blocker, "HTTP version", fmt.Sprintf("HAR entry %d", index+1), "Captured HTTP version evidence requires review before replay ("+capture.ReasonCode+").")
		}
		exchanges = append(exchanges, ex)
	}
	if len(localEntries) > 0 {
		var schemes []string
		for _, scheme := range slices.Sorted(maps.Keys(localSchemes)) {
			schemes = append(schemes, fmt.Sprintf("%s %d", scheme, localSchemes[scheme]))
		}
		diagnostics.Add(diag.Info, "HAR local URLs", harEntryList(localEntries), fmt.Sprintf("%d URL(s) the browser made itself (%s) never reached the network, so they are not requests to replay", len(localEntries), strings.Join(schemes, ", ")))
	}
	if len(braceEntries) > 0 {
		diagnostics.Add(diag.Info, "HAR URL braces", harEntryList(braceEntries), `these URLs hold a literal "{{" or "}}", which a script reads as a template, so their braces are sent as %7B and %7D; servers decode them to the same characters`)
	}
	if len(exchanges) > 1 {
		warnings = append(warnings, "HAR import preserves inter-request think time when entry timestamps are available, but browser-specific asset metadata is not preserved")
	}
	for _, origin := range slices.Sorted(maps.Keys(h3Mapped)) {
		count := h3Mapped[origin]
		switch mode {
		case requestimport.HTTPVersionModeInherit:
			diagnostics.AddCoded(diag.Warning, requestimport.CodeH3NotPreserved, "HTTP version", origin,
				fmt.Sprintf("%d captured HTTP/3 request%s keep the script's inherited HTTP version policy (http_version_mode: inherit).", count, pluralSuffix(count, "", "s")))
		case requestimport.HTTPVersionModeExact:
			diagnostics.AddCoded(diag.Info, requestimport.CodeCapturedH3, "HTTP version", origin,
				fmt.Sprintf("%d captured HTTP/3 request%s replay with exact HTTP/3 (3), which fails rather than falls back when HTTP/3 is unavailable.", count, pluralSuffix(count, "", "s")))
		default:
			diagnostics.AddCoded(diag.Info, requestimport.CodeCapturedH3, "HTTP version", origin,
				fmt.Sprintf("%d captured HTTP/3 request%s replay with auto+h3: TCP until the origin advertises HTTP/3, then HTTP/3.", count, pluralSuffix(count, "", "s")))
		}
	}
	for _, origin := range slices.Sorted(maps.Keys(h3Drafts)) {
		count := h3Drafts[origin]
		diagnostics.AddCoded(diag.Warning, requestimport.ReasonHTTP3DraftToken, "HTTP version", origin,
			fmt.Sprintf("%d request%s used a draft HTTP/3 version; they replay on the inherited policy until final HTTP/3 is chosen explicitly.", count, pluralSuffix(count, "", "s")))
	}
	for _, origin := range slices.Sorted(maps.Keys(h3Advertised)) {
		detail := "The origin advertises HTTP/3 (Alt-Svc), but no captured request used it. Its requests keep their captured HTTP version; moving the origin to auto+h3 is an explicit choice."
		if h3Used[origin] {
			detail = "The origin advertises HTTP/3 (Alt-Svc), and captured requests used it."
		}
		diagnostics.AddCoded(diag.Info, OriginAdvertisesH3Code, "HTTP/3 advertisement", origin, detail)
	}
	if revalidations > 0 {
		diagnostics.Add(diag.Info, "HAR cache validators", "HAR import", fmt.Sprintf(
			"%d request%s carried the browser's cache validators (If-None-Match, If-Modified-Since, If-Range), which were removed: replay fetches the full response, as a virtual user without the recording browser's cache does, and a recorded 304 Not Modified is checked as the full response.",
			revalidations, pluralSuffix(revalidations, "", "s")))
	}
	if filtered > 0 {
		diagnostics.Add(diag.Info, "HAR static asset filter", "HAR import", fmt.Sprintf("%d static asset entr%s skipped by default; enable include_static_assets to retain browser assets", filtered, pluralSuffix(filtered, "y was", "ies were")))
	}
	return exchanges, warnings, filtered, skipped, nil
}

func extractHARItems(payload any) ([]map[string]any, error) {
	root, ok := payload.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("HAR content must be a JSON object")
	}
	if logObj, ok := root["log"].(map[string]any); ok {
		root = logObj
	}
	if entries, ok := root["entries"].([]any); ok {
		items := make([]map[string]any, 0, len(entries))
		for _, entry := range entries {
			mapped, ok := entry.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("HAR entries must be objects")
			}
			items = append(items, mapped)
		}
		return items, nil
	}
	if _, hasRequest := root["request"]; hasRequest {
		return []map[string]any{root}, nil
	}
	return nil, fmt.Errorf("HAR content must include log.entries or a single entry with request/response")
}

func isStaticHAREntry(item map[string]any) bool {
	if static, ok := item[harStaticKey].(bool); ok {
		// The recorder classified the request itself.
		return static
	}
	requestData, _ := item["request"].(map[string]any)
	method := strings.ToUpper(strings.TrimSpace(stringValue(requestData["method"])))
	if method != "" && method != http.MethodGet && method != http.MethodHead {
		return false
	}
	responseData, _ := item["response"].(map[string]any)
	if content, ok := responseData["content"].(map[string]any); ok {
		if isStaticMIMEType(stringValue(content["mimeType"])) {
			return true
		}
	}
	return isStaticURL(stringValue(requestData["url"]))
}

func isStaticMIMEType(mimeType string) bool {
	mimeType = strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	if mimeType == "" {
		return false
	}
	if strings.HasPrefix(mimeType, "image/") || strings.HasPrefix(mimeType, "font/") || strings.HasPrefix(mimeType, "audio/") || strings.HasPrefix(mimeType, "video/") {
		return true
	}
	switch mimeType {
	case "text/css", "text/javascript", "application/javascript", "application/x-javascript", "application/font-woff", "application/font-woff2", "application/vnd.ms-fontobject":
		return true
	default:
		return false
	}
}

func isStaticURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	pathValue := rawURL
	if err == nil && parsed.Path != "" {
		pathValue = parsed.Path
	}
	pathValue = strings.ToLower(pathValue)
	for _, ext := range []string{".css", ".js", ".mjs", ".map", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".ico", ".woff", ".woff2", ".ttf", ".eot", ".otf", ".mp4", ".webm", ".mp3", ".wav"} {
		if strings.HasSuffix(pathValue, ext) {
			return true
		}
	}
	return false
}

// harEntryList names HAR entries for one diagnostic: "HAR entries 3, 7, 9",
// the first ten of a longer list followed by how many more.
func harEntryList(entries []int) string {
	const shown = 10
	names := make([]string, 0, min(len(entries), shown))
	for _, entry := range entries[:min(len(entries), shown)] {
		names = append(names, strconv.Itoa(entry))
	}
	list := strings.Join(names, ", ")
	if len(entries) > shown {
		list += fmt.Sprintf(" and %d more", len(entries)-shown)
	}
	if len(entries) == 1 {
		return "HAR entry " + list
	}
	return "HAR entries " + list
}

var templateBraceEscaper = strings.NewReplacer("{", "%7B", "}", "%7D")

// escapeLiteralTemplateBraces percent-encodes the braces of a recorded URL
// that holds a literal "{{" or "}}" (a page's own JSON in its query string,
// such as {d:1,p:{cn:2}}), which the script would read as a template.
func escapeLiteralTemplateBraces(raw string) (string, bool) {
	if !strings.Contains(raw, "{{") && !strings.Contains(raw, "}}") {
		return raw, false
	}
	return templateBraceEscaper.Replace(raw), true
}

// harLocalRequestScheme names the scheme of a HAR request the browser served
// itself (blob:, data:, about:, extension pages), or "" for a network request.
func harLocalRequestScheme(requestData map[string]any) string {
	raw := strings.TrimSpace(stringValue(requestData["url"]))
	scheme, _, found := strings.Cut(raw, ":")
	if !found {
		return ""
	}
	switch scheme = strings.ToLower(scheme); scheme {
	case "blob", "data", "about", "file", "chrome-extension", "moz-extension", "filesystem", "javascript":
		return scheme
	default:
		return ""
	}
}

func parseHARItem(item map[string]any, diagnostics *diag.Collector, index int) (exchange, error) {
	return parseHARItemWithProvenance(item, diagnostics, index, false)
}

func parseHARItemWithProvenance(item map[string]any, diagnostics *diag.Collector, index int, legacyRecorder bool) (exchange, error) {
	requestData, ok := item["request"].(map[string]any)
	if !ok {
		return exchange{}, fmt.Errorf("HAR entry is missing request")
	}
	location := fmt.Sprintf("HAR entry %d", index+1)
	// Keep request occurrences until the shared finalizer knows this leaf's
	// selected version. Preemptive stripping would lose valid H1 settings or
	// erase review evidence for incompatible H2 settings.
	headerList := harRequestHeaderList(requestData["headers"])
	headers := headerList.ToMap()
	if headers == nil {
		headers = map[string]string{}
	}
	body, bodyEncoding, fileAssets := harBody(requestData, headers, diagnostics, location)
	ex := exchange{
		SourceEntryID: index + 1,
		Method:        strings.ToUpper(stringValue(requestData["method"])),
		URL:           stringValue(requestData["url"]),
		Body:          body,
		BodyEncoding:  bodyEncoding,
		FileAssets:    fileAssets,
		Headers:       headers,
		HeaderList:    headerList,
		Metadata:      harEntryMetadata(item, requestData, nil),
		DurationMs:    floatValue(item["time"]),
	}
	if ex.Method != "" || ex.URL != "" {
		location = strings.TrimSpace(ex.Method + " " + ex.URL)
	}
	applyHARPostDataHeaders(requestData, ex.Headers)
	// applyHARPostDataHeaders may inject a multipart boundary into the Content-Type
	// in the map; keep the ordered HeaderList (which replay prefers over the map) in
	// sync so the body stays parseable at replay.
	ex.HeaderList = reconcileMultipartBoundaryInList(ex.HeaderList, ex.Headers)
	mergeHARCookies(requestData["cookies"], &ex, diagnostics, location)
	if startedAt, ok := parseHARStartedAt(item["startedDateTime"]); ok {
		ex.StartedAt = startedAt
		ex.HasStartedAt = true
	}
	if ex.Method == "" || ex.URL == "" {
		return exchange{}, fmt.Errorf("HAR request entries must include method and url")
	}
	if responseData, ok := item["response"].(map[string]any); ok {
		ex.Metadata = harEntryMetadata(item, requestData, responseData)
		responseHeaders := headerListToMap(responseData["headers"], diagnostics, location+" response")
		responseHeaderList := headerListEntries(responseData["headers"])
		responseBody, capture := harResponseContent(item, responseData, ex.Method, responseHeaders, legacyRecorder)
		action := dsl.Action{Metadata: ex.Metadata}
		requestimport.ApplyResponseContentCapture(&action, capture, requestimport.SourceRef{Format: "har", Pointer: fmt.Sprintf("log.entries[%d].response.content", index)})
		ex.Metadata = action.Metadata
		if capture.ReasonCode != "" && capture.ReasonCode != "response_content_unavailable" {
			diagnostics.Add(diag.Blocker, "Response content", fmt.Sprintf("HAR entry %d", index+1), "Captured response content requires review before correlation ("+capture.ReasonCode+").")
		}
		if capture.Complete() && capture.BodyKind == "binary" {
			diagnostics.Add(diag.Info, "HAR base64 content", fmt.Sprintf("HAR entry %d", index+1), "Binary response storage remains in the source artifact and is excluded from textual correlation.")
		}
		ex.Response = &response{
			ContentCapture: capture,
			Status:         intValue(responseData["status"]),
			Headers:        responseHeaders,
			HeaderList:     responseHeaderList,
			Body:           responseBody,
		}
	} else {
		diagnostics.Add(diag.Warning, "HAR missing response", location, "entry has no response object; no status assertion or response extractors can be generated for this step")
		if rawProvenance, present := item[requestimport.HARResponseContentProvenanceKey]; present {
			capture := requestimport.ReviewResponseContentProvenance(rawProvenance)
			if capture.Complete() {
				capture = requestimport.UnavailableResponseContent("unknown", "malformed_response_content_provenance")
			}
			action := dsl.Action{Metadata: ex.Metadata}
			requestimport.ApplyResponseContentCapture(&action, capture, requestimport.SourceRef{Format: "har", Pointer: fmt.Sprintf("log.entries[%d].response.content", index)})
			ex.Metadata = action.Metadata
			diagnostics.Add(diag.Blocker, "Response content", fmt.Sprintf("HAR entry %d", index+1), "Captured response content requires review before correlation ("+capture.ReasonCode+").")
		}
	}
	normalizeHARContentLength(&ex, requestData, diagnostics, location)
	normalizeHARPseudoHeaders(&ex, diagnostics, location)
	dropHARCacheValidators(&ex)
	applyRecorderEntryFields(&ex, item, index)
	return ex, nil
}

// harCacheValidatorHeaders are the conditional headers a browser adds from
// its own cache. Replayed, the recording's validators make the server answer
// 304 Not Modified, or 200 where the recording got 304, depending on what a
// CDN edge holds; a virtual user has no such cache. LoadRunner leaves them
// out too. If-Match and If-Unmodified-Since are the application's own
// concurrency checks, and stay.
var harCacheValidatorHeaders = []string{"If-None-Match", "If-Modified-Since", "If-Range"}

// dropHARCacheValidators removes a captured request's browser cache
// validators, so replay fetches the full response.
func dropHARCacheValidators(ex *exchange) {
	before := len(ex.HeaderList) + len(ex.Headers)
	dropHARRequestHeaders(ex, harCacheValidatorHeaders...)
	if len(ex.HeaderList)+len(ex.Headers) < before {
		ex.CacheValidatorsDropped = true
	}
}

// HAR entry fields the APEX Load proxy recorder writes, for what a HAR
// cannot say: its own static classification, the step and transaction
// names the tester gave, the measured (or edited) pause before the request,
// and a request body cut off at the capture limit.
const (
	harStaticKey             = "_apexload_static"
	harStepNameKey           = "_apexload_step_name"
	harTransactionKey        = "_apexload_transaction"
	harThinkTimeKey          = "_apexload_think_time_ms"
	harRequestTruncatedKey   = "_apexload_request_body_truncated"
	harRecordedBodyBytesKey  = "_apexload_request_body_bytes"
	harSourceTransactionMeta = "source_transaction"
)

// maxHAREntryNoteBytes bounds a HAR entry comment kept as the action's note,
// as the recorder bounds the notes it writes there.
const maxHAREntryNoteBytes = 64 << 10

func applyRecorderEntryFields(ex *exchange, item map[string]any, index int) {
	ex.StepName = strings.TrimSpace(stringValue(item[harStepNameKey]))
	ex.Note = boundedtext.TruncateUTF8(strings.TrimSpace(stringValue(item["comment"])), maxHAREntryNoteBytes)
	if transaction := strings.TrimSpace(stringValue(item[harTransactionKey])); transaction != "" {
		// The engine groups metrics by this metadata key.
		if ex.Metadata == nil {
			ex.Metadata = map[string]any{}
		}
		ex.Metadata[harSourceTransactionMeta] = transaction
	}
	if raw, ok := item[harThinkTimeKey]; ok {
		if thinkMs := int64(floatValue(raw)); thinkMs >= 0 {
			ex.RecordedThinkTimeMs, ex.HasRecordedThinkTime = thinkMs, true
		}
	}
	if truncated, _ := item[harRequestTruncatedKey].(bool); truncated {
		ex.ReviewFindings = append(ex.ReviewFindings, requestimport.RecordedBodyTruncatedFinding("har", fmt.Sprintf("entry-%d", index+1), intValue(item[harRecordedBodyBytesKey])))
	}
}

func harRequestHeaderList(raw any) dsl.HeaderList {
	items, _ := raw.([]any)
	var headers dsl.HeaderList
	for _, item := range items {
		value, ok := item.(map[string]any)
		if !ok {
			continue
		}
		headers = append(headers, dsl.HeaderEntry{Name: stringValue(value["name"]), Value: stringValue(value["value"])})
	}
	return headers
}

// HTTP/2 pseudo-headers (RFC 9113 section 8.3.1) restate the request line.
// Chrome and LoadRunner export them as ordinary HAR headers. When every
// occurrence agrees with the entry's method and URL they add nothing and the
// native transport regenerates them, so they are dropped. Unknown or
// disagreeing pseudo-headers stay in the shared fidelity pipeline, which still
// requires explicit review for them.
func normalizeHARPseudoHeaders(ex *exchange, diagnostics *diag.Collector, location string) {
	target, ok := harRequestTarget(ex.URL)
	if !ok {
		return
	}
	found := false
	for _, header := range ex.HeaderList {
		if !strings.HasPrefix(header.Name, ":") {
			continue
		}
		if !harPseudoHeaderAgrees(header, ex.Method, target) {
			return
		}
		found = true
	}
	if !found {
		return
	}
	kept := make(dsl.HeaderList, 0, len(ex.HeaderList))
	for _, header := range ex.HeaderList {
		if !strings.HasPrefix(header.Name, ":") {
			kept = append(kept, header)
		}
	}
	ex.HeaderList = kept
	for name := range ex.Headers {
		if strings.HasPrefix(name, ":") {
			delete(ex.Headers, name)
		}
	}
	diagnostics.Add(diag.Info, "HAR HTTP/2 pseudo-headers", location, "recorded :method/:scheme/:authority/:path matched the request line; the native transport regenerates them")
}

type harTarget struct {
	scheme, authority, requestTarget string
}

// harRequestTarget keeps the raw path and query spelling from the HAR URL so
// :path is compared byte-for-byte rather than after re-encoding.
func harRequestTarget(rawURL string) (harTarget, bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return harTarget{}, false
	}
	rest := rawURL[strings.Index(rawURL, "://")+3:]
	if index := strings.IndexByte(rest, '#'); index >= 0 {
		rest = rest[:index]
	}
	requestTarget := "/"
	if index := strings.IndexAny(rest, "/?"); index >= 0 {
		requestTarget = rest[index:]
		if strings.HasPrefix(requestTarget, "?") {
			requestTarget = "/" + requestTarget
		}
	}
	return harTarget{scheme: strings.ToLower(parsed.Scheme), authority: harCanonicalAuthority(parsed.Scheme, parsed.Host), requestTarget: requestTarget}, true
}

func harCanonicalAuthority(scheme, authority string) string {
	authority = strings.ToLower(strings.TrimSpace(authority))
	switch {
	case scheme == "https" && strings.HasSuffix(authority, ":443"):
		return strings.TrimSuffix(authority, ":443")
	case scheme == "http" && strings.HasSuffix(authority, ":80"):
		return strings.TrimSuffix(authority, ":80")
	}
	return authority
}

func harPseudoHeaderAgrees(header dsl.HeaderEntry, method string, target harTarget) bool {
	value := strings.TrimSpace(header.Value)
	switch strings.ToLower(header.Name) {
	case ":method":
		return value != "" && strings.EqualFold(value, method)
	case ":scheme":
		return strings.EqualFold(value, target.scheme)
	case ":authority":
		return value != "" && harCanonicalAuthority(target.scheme, value) == target.authority
	case ":path":
		return value == target.requestTarget
	default:
		return false
	}
}

func harEntryMetadata(item map[string]any, requestData map[string]any, responseData map[string]any) map[string]any {
	metadata := make(map[string]any)
	addStringMetadata(metadata, "har.pageref", stringValue(item["pageref"]))
	addStringMetadata(metadata, "har.server_ip_address", stringValue(item["serverIPAddress"]))
	addCapturedVersionMetadata(metadata, "har.request.http_version", requestData["httpVersion"])
	if len(metadataMap(item["timings"])) > 0 {
		metadata["har.timings"] = metadataMap(item["timings"])
	}
	if len(metadataMap(item["cache"])) > 0 {
		metadata["har.cache"] = metadataMap(item["cache"])
	}
	if queryString, ok := itemListMetadata(requestData["queryString"]); ok {
		metadata["har.request.query_string"] = disclosureSafeHARQueryMetadata(queryString)
	}
	if responseData != nil {
		addCapturedVersionMetadata(metadata, "har.response.http_version", responseData["httpVersion"])
		if cookies, ok := itemListMetadata(responseData["cookies"]); ok {
			metadata["har.response.cookies"] = disclosureSafeHARCookieMetadata(cookies)
		}
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func addCapturedVersionMetadata(metadata map[string]any, key string, raw any) {
	if value, ok := raw.(string); ok && len(value) <= 256 {
		capture := requestimport.ReviewHTTPVersionCapture(value, true, nil, false, nil, false, false)
		// A draft token is kept as the raw observation it is.
		if capture.RequestState == "available" || capture.RequestState == "unavailable" || capture.ReasonCode == requestimport.ReasonHTTP3DraftToken {
			metadata[key] = value
		}
	}
}

func disclosureSafeHARQueryMetadata(items []map[string]any) []map[string]any {
	out := make([]map[string]any, len(items))
	for index, item := range items {
		out[index] = metadataMap(item)
	}
	return out
}

func disclosureSafeHARCookieMetadata(items []map[string]any) []map[string]any {
	out := make([]map[string]any, len(items))
	for index, item := range items {
		out[index] = metadataMap(item)
	}
	return out
}

func addStringMetadata(metadata map[string]any, key string, value string) {
	if strings.TrimSpace(value) != "" {
		metadata[key] = value
	}
}

func metadataMap(value any) map[string]any {
	typed, ok := value.(map[string]any)
	if !ok || len(typed) == 0 {
		return nil
	}
	out := make(map[string]any, len(typed))
	for key, item := range typed {
		out[key] = item
	}
	return out
}

func itemListMetadata(value any) ([]map[string]any, bool) {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return nil, false
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		mapped, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, metadataMap(mapped))
	}
	return out, len(out) > 0
}

func isHARWebSocketUpgrade(item map[string]any) bool {
	requestData, _ := item["request"].(map[string]any)
	responseData, _ := item["response"].(map[string]any)
	if messages, ok := item["_webSocketMessages"].([]any); ok && len(messages) > 0 {
		return true
	}
	return strings.EqualFold(rawHARHeaderLookup(requestData["headers"], "Upgrade"), "websocket") ||
		strings.EqualFold(rawHARHeaderLookup(responseData["headers"], "Upgrade"), "websocket") ||
		intValue(responseData["status"]) == 101
}

// OriginAdvertisesH3Code marks an origin whose captured responses advertised
// final HTTP/3 in Alt-Svc. It is an observation; it never changes a policy.
const OriginAdvertisesH3Code = "origin_advertises_h3"

// harOriginAdvertisingH3 returns the request origin when the entry's
// response advertised final HTTP/3, else "".
func harOriginAdvertisingH3(item map[string]any) string {
	responseData, _ := item["response"].(map[string]any)
	values := rawHARHeaderValues(responseData["headers"], "Alt-Svc")
	if len(values) == 0 || !httpprotocol.AltSvcAdvertisesH3(values) {
		return ""
	}
	requestData, _ := item["request"].(map[string]any)
	parsed, err := url.Parse(stringValue(requestData["url"]))
	if err != nil || parsed.Host == "" || !strings.EqualFold(parsed.Scheme, "https") {
		return ""
	}
	return strings.ToLower(parsed.Scheme + "://" + parsed.Host)
}

// harOriginUsingH3 returns the request origin when the entry itself was
// captured over final HTTP/3, else "".
// harEntryOrigin is the scheme://host of a HAR request, lowercased.
func harEntryOrigin(requestData map[string]any) string {
	parsed, err := url.Parse(stringValue(requestData["url"]))
	if err != nil || parsed.Host == "" {
		return ""
	}
	return strings.ToLower(parsed.Scheme + "://" + parsed.Host)
}

func harOriginUsingH3(item map[string]any) string {
	requestData, _ := item["request"].(map[string]any)
	switch strings.ToLower(strings.TrimSpace(stringValue(requestData["httpVersion"]))) {
	case "h3", "http/3", "http/3.0":
	default:
		return ""
	}
	parsed, err := url.Parse(stringValue(requestData["url"]))
	if err != nil || parsed.Host == "" || !strings.EqualFold(parsed.Scheme, "https") {
		return ""
	}
	return strings.ToLower(parsed.Scheme + "://" + parsed.Host)
}

func rawHARHeaderValues(value any, name string) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	var values []string
	for _, item := range items {
		mapped, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if strings.EqualFold(stringValue(mapped["name"]), name) {
			values = append(values, stringValue(mapped["value"]))
		}
	}
	return values
}

func rawHARHeaderLookup(value any, name string) string {
	items, ok := value.([]any)
	if !ok {
		return ""
	}
	for _, item := range items {
		mapped, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if strings.EqualFold(stringValue(mapped["name"]), name) {
			return stringValue(mapped["value"])
		}
	}
	return ""
}

func parseHARStartedAt(value any) (time.Time, bool) {
	raw := stringValue(value)
	if raw == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func harBody(requestData map[string]any, headers map[string]string, diagnostics *diag.Collector, location string) (string, string, []dsl.DataSource) {
	if postData, ok := requestData["postData"].(map[string]any); ok {
		if text := bodyValue(postData["text"]); text != "" {
			body, encoding := decodeHARRequestBody(text, stringValue(postData["encoding"]), diagnostics, location)
			return body, encoding, nil
		}
		body, assets := harParamsBody(postData, headers, diagnostics, location)
		return body, "", assets
	}
	return "", "", nil
}

// decodeHARText decodes only the HAR storage representation. HTTP response
// decoding precedes content.text in HAR 1.2, even for binary/base64 resources.
func decodeHARText(text string, encoding string, _ map[string]string, diagnostics *diag.Collector, location string, label string) string {
	decoded, reason := decodeHARContentStorage(text, encoding)
	if reason != "" {
		diagnostics.Add(diag.Blocker, "Response content", location, "HAR response storage requires review before correlation ("+reason+").")
		return ""
	}
	return string(decoded)
}

func decodeHARContentStorage(text string, encoding string) ([]byte, string) {
	if len(text) > ((maxHARDecodedContentBytes+2)/3)*4+1024 {
		return nil, "har_content_storage_limit"
	}
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "raw": // Raw is an explicit legacy storage alias, never wire coding.
		if len(text) > maxHARDecodedContentBytes {
			return nil, "har_content_storage_limit"
		}
		return []byte(text), ""
	case "base64":
		decoded, err := decodeHARResponseBase64Text(text)
		if err != nil {
			return nil, "invalid_har_content_storage"
		}
		if len(decoded) > maxHARDecodedContentBytes {
			return nil, "har_content_storage_limit"
		}
		return decoded, ""
	default:
		return nil, "unsupported_har_content_storage"
	}
}

func decodeHARResponseBase64Text(text string) ([]byte, error) {
	compact := strings.Join(strings.Fields(text), "")
	var decodeErr error
	for _, encoding := range []*base64.Encoding{base64.StdEncoding.Strict(), base64.RawStdEncoding.Strict(), base64.URLEncoding.Strict(), base64.RawURLEncoding.Strict()} {
		decoded, err := encoding.DecodeString(compact)
		if err == nil {
			return decoded, nil
		}
		decodeErr = err
	}
	return nil, decodeErr
}

func harResponseContent(item, responseData map[string]any, method string, headers map[string]string, legacyRecorder bool) (string, requestimport.ResponseContentCapture) {
	content, contentOK := responseData["content"].(map[string]any)
	bodyless := strings.EqualFold(method, http.MethodHead) || intValue(responseData["status"]) == http.StatusNoContent || intValue(responseData["status"]) == http.StatusNotModified
	rawProvenance, provenancePresent := item[requestimport.HARResponseContentProvenanceKey]
	if provenancePresent {
		capture := requestimport.ReviewResponseContentProvenance(rawProvenance)
		if !capture.Complete() {
			return "", capture
		}
		if !contentOK {
			return "", requestimport.UnavailableResponseContent("unknown", "malformed_response_content_provenance")
		}
		body, parsed := harResponseContentStorage(content, bodyless)
		if !parsed.Complete() {
			return "", requestimport.UnavailableResponseContent("unknown", "malformed_response_content_provenance")
		}
		if capture.DecodedSizeBytes != parsed.DecodedSizeBytes || (capture.BodyKind == "text" && parsed.BodyKind == "binary") {
			return "", requestimport.UnavailableResponseContent("unknown", "malformed_response_content_provenance")
		}
		if capture.Captured == "wire" && bodyless && parsed.DecodedSizeBytes != 0 {
			return "", requestimport.UnavailableResponseContent("unknown", "malformed_response_content_provenance")
		}
		if capture.BodyKind == "binary" {
			body = ""
		} else if capture.BodyKind == "" {
			capture.BodyKind = parsed.BodyKind
		}
		return body, capture
	}
	if !contentOK {
		// An explicit null is the exporter's way of saying it kept no content,
		// the same as an omitted field. Other shapes are malformed storage.
		if raw, present := responseData["content"]; present && raw != nil {
			return "", requestimport.UnavailableResponseContent("har_decoded", "invalid_har_content_storage")
		}
		if bodyless {
			return "", requestimport.CompleteResponseContent("har_decoded", 0)
		}
		return "", requestimport.UnavailableResponseContent("har_decoded", "response_content_unavailable")
	}
	body, capture := harResponseContentStorage(content, bodyless)
	if legacyRecorder && !bodyless && capture.Complete() && harHeaderPresent(responseData["headers"], "Content-Encoding") {
		return "", requestimport.UnavailableResponseContent("unknown", "response_content_provenance_unavailable")
	}
	return body, capture
}

func harHeaderPresent(raw any, name string) bool {
	items, _ := raw.([]any)
	for _, item := range items {
		if header, ok := item.(map[string]any); ok {
			if value, ok := header["name"].(string); ok && strings.EqualFold(value, name) {
				return true
			}
		}
	}
	return false
}

func harResponseContentStorage(content map[string]any, bodyless bool) (string, requestimport.ResponseContentCapture) {
	rawText, present := content["text"]
	if !present || rawText == nil {
		if bodyless {
			return "", requestimport.CompleteResponseContent("har_decoded", 0)
		}
		return "", requestimport.UnavailableResponseContent("har_decoded", "response_content_unavailable")
	}
	text, ok := rawText.(string)
	if !ok {
		return "", requestimport.UnavailableResponseContent("har_decoded", "invalid_har_content_storage")
	}
	encoding := ""
	if rawEncoding, exists := content["encoding"]; exists {
		var encodingOK bool
		encoding, encodingOK = rawEncoding.(string)
		if !encodingOK || len(encoding) > 256 {
			return "", requestimport.UnavailableResponseContent("har_decoded", "invalid_har_content_storage")
		}
	}
	decoded, reason := decodeHARContentStorage(text, encoding)
	if reason != "" {
		return "", requestimport.UnavailableResponseContent("har_decoded", reason)
	}
	capture := requestimport.CompleteResponseContent("har_decoded", len(decoded))
	if !isHARResponseText(decoded) {
		capture.BodyKind = "binary"
		return "", capture
	}
	return string(decoded), capture
}

// Binary HAR resources remain in the immutable source and retain exact decoded
// size/provenance, but are not application text for correlation or Aura parsing.
func isHARResponseText(body []byte) bool {
	return requestimport.IsResponseContentText(body)
}

func decodeHARRequestBody(text string, encoding string, diagnostics *diag.Collector, location string) (string, string) {
	if !strings.EqualFold(strings.TrimSpace(encoding), "base64") {
		return text, ""
	}
	decoded, err := decodeBase64Text(text)
	if err != nil {
		diagnostics.Add(diag.Warning, "HAR base64 content", location, "request body is marked base64 but could not be decoded; original text was preserved")
		return text, ""
	}
	if !utf8.Valid(decoded) {
		diagnostics.Add(diag.Info, "HAR base64 content", location, "request body decoded to non-UTF-8 bytes and was preserved as base64 for replay")
		return base64.StdEncoding.EncodeToString(decoded), "base64"
	}
	return string(decoded), ""
}

func decodeBase64Text(value string) ([]byte, error) {
	compact := strings.Join(strings.Fields(value), "")
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	var lastErr error
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(compact)
		if err == nil {
			return decoded, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func harParamsBody(postData map[string]any, headers map[string]string, diagnostics *diag.Collector, location string) (string, []dsl.DataSource) {
	params, ok := postData["params"].([]any)
	if !ok || len(params) == 0 {
		return "", nil
	}
	mimeType := strings.ToLower(stringValue(postData["mimeType"]))
	if strings.Contains(mimeType, "multipart/form-data") {
		contentType := firstNonEmpty(headerLookup(headers, "Content-Type"), stringValue(postData["mimeType"]))
		return harMultipartBody(params, multipartBoundaryOrDefault(contentType, defaultHARMultipartBoundary), diagnostics, location)
	}
	encoded := make([]string, 0, len(params))
	assets := make([]dsl.DataSource, 0)
	for _, item := range params {
		param, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := stringValue(param["name"])
		if name == "" {
			continue
		}
		value := stringValue(param["value"])
		if filename := stringValue(param["fileName"]); filename != "" {
			assetName := sanitizeIdentifier(name)
			value = "{{file." + assetName + "}}"
			assets = append(assets, fileAssetDataSource(assetName, filename, stringValue(param["contentType"])))
			diagnostics.Add(diag.Warning, "HAR postData.params file", location, "form parameter "+name+" references file content that is not embedded in the HAR; replace the placeholder before replay")
		}
		encoded = append(encoded, url.QueryEscape(name)+"="+url.QueryEscape(value))
	}
	if !strings.Contains(mimeType, "application/x-www-form-urlencoded") && mimeType != "" {
		diagnostics.Add(diag.Info, "HAR postData.params", location, "postData.params were serialized as URL-encoded form data because text was absent")
	}
	return strings.Join(encoded, "&"), assets
}

func harMultipartBody(params []any, boundary string, diagnostics *diag.Collector, location string) (string, []dsl.DataSource) {
	var b strings.Builder
	assets := make([]dsl.DataSource, 0)
	for _, item := range params {
		param, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := stringValue(param["name"])
		if name == "" {
			continue
		}
		value := stringValue(param["value"])
		filename := stringValue(param["fileName"])
		b.WriteString("--" + boundary + "\r\n")
		if filename != "" {
			b.WriteString(`Content-Disposition: form-data; name="` + name + `"; filename="` + filename + `"` + "\r\n")
			contentType := stringValue(param["contentType"])
			if contentType != "" {
				b.WriteString("Content-Type: " + contentType + "\r\n")
			}
			assetName := sanitizeIdentifier(name)
			assets = append(assets, fileAssetDataSource(assetName, filename, contentType))
			b.WriteString("\r\n{{file." + assetName + "}}\r\n")
			diagnostics.Add(diag.Warning, "HAR multipart file", location, "multipart parameter "+name+" references file bytes that are not embedded in the HAR; replace {{file."+assetName+"}} before replay")
			continue
		}
		b.WriteString(`Content-Disposition: form-data; name="` + name + `"` + "\r\n\r\n")
		b.WriteString(value + "\r\n")
	}
	b.WriteString("--" + boundary + "--\r\n")
	return b.String(), assets
}

func fileAssetDataSource(name string, fileName string, contentType string) dsl.DataSource {
	name = sanitizeIdentifier(name)
	if name == "" {
		name = "file"
	}
	return dsl.DataSource{
		Name:        name,
		Type:        "file",
		FileName:    fileName,
		ContentType: contentType,
	}
}

func applyHARPostDataHeaders(requestData map[string]any, headers map[string]string) {
	if headers == nil {
		return
	}
	postData, ok := requestData["postData"].(map[string]any)
	if !ok {
		return
	}
	mimeType := stringValue(postData["mimeType"])
	existing := headerLookup(headers, "Content-Type")
	if mimeType == "" && existing == "" {
		return
	}
	contentType := firstNonEmpty(existing, mimeType)
	if strings.Contains(strings.ToLower(contentType), "multipart/form-data") {
		boundary := multipartBoundaryOrDefault(contentType, defaultHARMultipartBoundary)
		ensureMultipartContentTypeBoundary(headers, boundary)
		return
	}
	if existing == "" {
		headers["Content-Type"] = mimeType
	}
}

func mergeHARCookies(value any, ex *exchange, diagnostics *diag.Collector, location string) {
	items, ok := value.([]any)
	if !ok || len(items) == 0 || ex == nil || ex.Headers == nil {
		return
	}

	for _, header := range ex.HeaderList {
		if strings.EqualFold(header.Name, "Cookie") {
			diagnostics.Add(diag.Info, "HAR cookies[]", location, "recorded request cookies[] were not synthesized because the explicit Cookie header is authoritative for replay")
			return
		}
	}
	if cookie := headerLookup(ex.Headers, "Cookie"); cookie != "" {
		ex.HeaderList = append(ex.HeaderList, dsl.HeaderEntry{Name: "Cookie", Value: cookie})
		diagnostics.Add(diag.Info, "HAR cookies[]", location, "recorded request cookies[] were not synthesized because the explicit Cookie header is authoritative for replay")
		return
	}

	cookies := make([]string, 0, len(items))
	for _, item := range items {
		cookie, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := stringValue(cookie["name"])
		if name == "" {
			continue
		}
		cookies = append(cookies, name+"="+stringValue(cookie["value"]))
	}
	if len(cookies) == 0 {
		return
	}
	cookieValue := strings.Join(cookies, "; ")
	ex.Headers["Cookie"] = cookieValue
	ex.HeaderList = append(ex.HeaderList, dsl.HeaderEntry{Name: "Cookie", Value: cookieValue})
	diagnostics.Add(diag.Info, "HAR cookies[]", location, "recorded request cookies[] were serialized in source order into a Cookie header for exact replay")
}

// harInStepResources attaches each static HAR entry to the page that
// requested it, as VuGen's "record resources in the step" option does: the
// earlier page whose URL matches the entry's Referer, otherwise the most
// recent page before it. It returns the explicit resources per page entry
// (keyed by SourceEntryID), bounded per page.
func harInStepResources(content string) map[int][]any {
	var payload any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(content, "\uFEFF")), &payload); err != nil {
		return nil
	}
	items, err := extractHARItems(payload)
	if err != nil {
		return nil
	}
	resources := map[int][]any{}
	pageByURL := map[string]int{}
	lastPage := 0
	for index, item := range items {
		requestData, _ := item["request"].(map[string]any)
		rawURL := strings.TrimSpace(stringValue(requestData["url"]))
		if !isStaticHAREntry(item) {
			if !isHARWebSocketUpgrade(item) && !strings.EqualFold(strings.TrimSpace(stringValue(requestData["method"])), http.MethodConnect) {
				lastPage = index + 1
				pageByURL[rawURL] = index + 1
			}
			continue
		}
		referer := harRequestHeader(requestData, "Referer")
		page := lastPage
		if byReferer, ok := pageByURL[referer]; ok && referer != "" {
			page = byReferer
		}
		if page == 0 || rawURL == "" || len(resources[page]) >= httpprotocol.MaxPageResources {
			continue
		}
		resource := map[string]any{"url": rawURL}
		if referer != "" {
			resource["referer"] = referer
		}
		resources[page] = append(resources[page], resource)
	}
	return resources
}

func harRequestHeader(requestData map[string]any, name string) string {
	headers, _ := requestData["headers"].([]any)
	for _, raw := range headers {
		header, _ := raw.(map[string]any)
		if strings.EqualFold(stringValue(header["name"]), name) {
			return strings.TrimSpace(stringValue(header["value"]))
		}
	}
	return ""
}
