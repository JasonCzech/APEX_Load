package sample

// har.go — split from converter.go (same package, behavior-preserving).

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/andybalholm/brotli"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
)

const maxHARDecodedContentBytes = 10 << 20

func parseHAR(content string, includeStaticAssets bool, diagnostics *diag.Collector) ([]exchange, []string, int, int, error) {
	var payload any
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return nil, nil, 0, 0, fmt.Errorf("failed to parse HAR JSON: %w", err)
	}
	items, err := extractHARItems(payload)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	exchanges := make([]exchange, 0, len(items))
	filtered := 0
	skipped := 0
	warnings := []string{}
	for index, item := range items {
		if !includeStaticAssets && isStaticHAREntry(item) {
			filtered++
			continue
		}
		if isHARWebSocketUpgrade(item) {
			diagnostics.Add(diag.Warning, "HAR websocket upgrade", fmt.Sprintf("HAR entry %d", index+1), "WebSocket upgrade/101 traffic is not converted by the generic HTTP HAR importer; import with a WebSocket-aware path before replay")
			warnings = append(warnings, "HAR WebSocket upgrade entries were skipped by the generic HTTP importer")
			skipped++
			continue
		}
		ex, err := parseHARItem(item, diagnostics, index)
		if err != nil {
			diagnostics.Add(diag.Warning, "HAR entry skipped", fmt.Sprintf("HAR entry %d", index+1), err.Error())
			warnings = append(warnings, fmt.Sprintf("HAR entry %d was skipped: %s", index+1, err.Error()))
			skipped++
			continue
		}
		exchanges = append(exchanges, ex)
	}
	if len(exchanges) > 1 {
		warnings = append(warnings, "HAR import preserves inter-request think time when entry timestamps are available, but browser-specific asset metadata is not preserved")
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

func parseHARItem(item map[string]any, diagnostics *diag.Collector, index int) (exchange, error) {
	requestData, ok := item["request"].(map[string]any)
	if !ok {
		return exchange{}, fmt.Errorf("HAR entry is missing request")
	}
	location := fmt.Sprintf("HAR entry %d", index+1)
	headers := headerListToMap(requestData["headers"], diagnostics, location)
	if headers == nil {
		headers = map[string]string{}
	}
	headerList := headerListEntries(requestData["headers"])
	body, bodyEncoding, fileAssets := harBody(requestData, headers, diagnostics, location)
	ex := exchange{
		Method:       strings.ToUpper(stringValue(requestData["method"])),
		URL:          stringValue(requestData["url"]),
		Body:         body,
		BodyEncoding: bodyEncoding,
		FileAssets:   fileAssets,
		Headers:      headers,
		HeaderList:   headerList,
		Metadata:     harEntryMetadata(item, requestData, nil),
		DurationMs:   floatValue(item["time"]),
	}
	if ex.Method != "" || ex.URL != "" {
		location = strings.TrimSpace(ex.Method + " " + ex.URL)
	}
	applyHARPostDataHeaders(requestData, ex.Headers)
	// applyHARPostDataHeaders may inject a multipart boundary into the Content-Type
	// in the map; keep the ordered HeaderList (which replay prefers over the map) in
	// sync so the body stays parseable at replay.
	ex.HeaderList = reconcileMultipartBoundaryInList(ex.HeaderList, ex.Headers)
	mergeHARCookies(requestData["cookies"], ex.Headers, diagnostics, location)
	sanitizeReplayHeaders(&ex, diagnostics, location)
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
		responseBody := ""
		if content, ok := responseData["content"].(map[string]any); ok {
			responseBody = decodeHARText(bodyValue(content["text"]), stringValue(content["encoding"]), responseHeaders, diagnostics, location+" response", "response body")
		}
		ex.Response = &response{
			Status:  intValue(responseData["status"]),
			Headers: responseHeaders,
			Body:    responseBody,
		}
	} else {
		diagnostics.Add(diag.Warning, "HAR missing response", location, "entry has no response object; no status assertion or response extractors can be generated for this step")
	}
	return ex, nil
}

func harEntryMetadata(item map[string]any, requestData map[string]any, responseData map[string]any) map[string]any {
	metadata := make(map[string]any)
	addStringMetadata(metadata, "har.pageref", stringValue(item["pageref"]))
	addStringMetadata(metadata, "har.server_ip_address", stringValue(item["serverIPAddress"]))
	addStringMetadata(metadata, "har.request.http_version", stringValue(requestData["httpVersion"]))
	if len(metadataMap(item["timings"])) > 0 {
		metadata["har.timings"] = metadataMap(item["timings"])
	}
	if len(metadataMap(item["cache"])) > 0 {
		metadata["har.cache"] = metadataMap(item["cache"])
	}
	if queryString, ok := itemListMetadata(requestData["queryString"]); ok {
		metadata["har.request.query_string"] = queryString
	}
	if responseData != nil {
		addStringMetadata(metadata, "har.response.http_version", stringValue(responseData["httpVersion"]))
		if cookies, ok := itemListMetadata(responseData["cookies"]); ok {
			metadata["har.response.cookies"] = cookies
		}
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
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
	requestHeaders := headerListEntries(requestData["headers"]).ToMap()
	responseHeaders := headerListEntries(responseData["headers"]).ToMap()
	return strings.EqualFold(headerLookup(requestHeaders, "Upgrade"), "websocket") ||
		strings.EqualFold(headerLookup(responseHeaders, "Upgrade"), "websocket") ||
		intValue(responseData["status"]) == 101
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

func decodeHARText(text string, encoding string, headers map[string]string, diagnostics *diag.Collector, location string, label string) string {
	decoded := []byte(text)
	if strings.EqualFold(strings.TrimSpace(encoding), "base64") {
		base64Decoded, err := decodeBase64Text(text)
		if err != nil {
			diagnostics.Add(diag.Warning, "HAR base64 content", location, label+" is marked base64 but could not be decoded; original text was preserved")
			return text
		}
		decoded = base64Decoded
	}
	if encodings := contentEncodings(headers); len(encodings) > 0 {
		var err error
		decoded, err = decodeHARContentEncodings(decoded, encodings)
		if err != nil {
			diagnostics.Add(diag.Warning, "HAR content-encoding", location, label+" could not be decompressed from Content-Encoding "+strings.Join(encodings, ", ")+"; encoded text was preserved")
			return text
		}
		diagnostics.Add(diag.Info, "HAR content-encoding", location, label+" was decompressed from Content-Encoding "+strings.Join(encodings, ", "))
	}
	if !utf8.Valid(decoded) {
		placeholder := "{{binary.har_" + strings.ReplaceAll(strings.ToLower(label), " ", "_") + "}}"
		diagnostics.Add(diag.Info, "HAR base64 content", location, label+" decoded to non-UTF-8 bytes and was represented with "+placeholder)
		return placeholder
	}
	return string(decoded)
}

func contentEncodings(headers map[string]string) []string {
	value := headerLookup(headers, "Content-Encoding")
	if value == "" {
		return nil
	}
	encodings := make([]string, 0)
	for _, part := range strings.Split(value, ",") {
		encoding := strings.ToLower(strings.TrimSpace(part))
		if encoding != "" {
			encodings = append(encodings, encoding)
		}
	}
	return encodings
}

func decodeHARContentEncodings(body []byte, encodings []string) ([]byte, error) {
	decoded := append([]byte(nil), body...)
	for i := len(encodings) - 1; i >= 0; i-- {
		switch encodings[i] {
		case "identity":
			continue
		case "gzip", "x-gzip":
			next, err := decodeHARGzip(decoded)
			if err != nil {
				return body, err
			}
			decoded = next
		case "deflate":
			next, err := decodeHARDeflate(decoded)
			if err != nil {
				return body, err
			}
			decoded = next
		case "br":
			next, err := readHARDecodedLimited(brotli.NewReader(bytes.NewReader(decoded)))
			if err != nil {
				return body, err
			}
			decoded = next
		default:
			return body, fmt.Errorf("unsupported content encoding %q", encodings[i])
		}
	}
	return decoded, nil
}

func decodeHARGzip(body []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return readHARDecodedLimited(reader)
}

func decodeHARDeflate(body []byte) ([]byte, error) {
	zlibReader, err := zlib.NewReader(bytes.NewReader(body))
	if err == nil {
		defer zlibReader.Close()
		return readHARDecodedLimited(zlibReader)
	}
	flateReader := flate.NewReader(bytes.NewReader(body))
	defer flateReader.Close()
	return readHARDecodedLimited(flateReader)
}

func readHARDecodedLimited(reader io.Reader) ([]byte, error) {
	decoded, err := io.ReadAll(io.LimitReader(reader, maxHARDecodedContentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(decoded) > maxHARDecodedContentBytes {
		return nil, fmt.Errorf("decoded HAR content exceeded %d-byte limit", maxHARDecodedContentBytes)
	}
	return decoded, nil
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
	values := url.Values{}
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
		if filename := stringValue(param["fileName"]); filename != "" || value == "" {
			assetName := sanitizeIdentifier(name)
			if filename == "" {
				filename = assetName
			}
			value = "{{file." + assetName + "}}"
			assets = append(assets, fileAssetDataSource(assetName, filename, stringValue(param["contentType"])))
			diagnostics.Add(diag.Warning, "HAR postData.params file", location, "form parameter "+name+" references file content that is not embedded in the HAR; replace the placeholder before replay")
		}
		values.Add(name, value)
	}
	if !strings.Contains(mimeType, "application/x-www-form-urlencoded") && mimeType != "" {
		diagnostics.Add(diag.Info, "HAR postData.params", location, "postData.params were serialized as URL-encoded form data because text was absent")
	}
	return values.Encode(), assets
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
		if filename != "" || value == "" {
			if filename == "" {
				filename = "upload.bin"
			}
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

func mergeHARCookies(value any, headers map[string]string, diagnostics *diag.Collector, location string) {
	items, ok := value.([]any)
	if !ok || len(items) == 0 || headers == nil {
		return
	}
	deleteHeader(headers, "Cookie")
	diagnostics.Add(diag.Info, "HAR cookies[]", location, "recorded request cookies[] were not replayed verbatim; the runtime cookie jar owns Cookie headers")
}

func sanitizeReplayHeaders(ex *exchange, diagnostics *diag.Collector, location string) {
	if ex == nil {
		return
	}
	if headerLookup(ex.Headers, "Cookie") != "" {
		deleteHeader(ex.Headers, "Cookie")
		diagnostics.Add(diag.Info, "HAR Cookie header stripped", location, "recorded Cookie header was omitted so replay uses the per-VUser cookie jar")
	}
	ex.HeaderList = filterHeaderList(ex.HeaderList, "Cookie")
}
