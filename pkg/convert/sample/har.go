package sample

// har.go — split from converter.go (same package, behavior-preserving).

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/convert/diag"
)

func parseHAR(content string, includeStaticAssets bool, diagnostics *diag.Collector) ([]exchange, []string, int, error) {
	var payload any
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return nil, nil, 0, fmt.Errorf("failed to parse HAR JSON: %w", err)
	}
	items, err := extractHARItems(payload)
	if err != nil {
		return nil, nil, 0, err
	}
	exchanges := make([]exchange, 0, len(items))
	filtered := 0
	for index, item := range items {
		if !includeStaticAssets && isStaticHAREntry(item) {
			filtered++
			continue
		}
		ex, err := parseHARItem(item, diagnostics, index)
		if err != nil {
			return nil, nil, 0, err
		}
		exchanges = append(exchanges, ex)
	}
	warnings := []string{}
	if len(exchanges) > 1 {
		warnings = append(warnings, "HAR import preserves inter-request think time when entry timestamps are available, but browser-specific asset metadata is not preserved")
	}
	if filtered > 0 {
		diagnostics.Add(diag.Info, "HAR static asset filter", "HAR import", fmt.Sprintf("%d static asset entr%s skipped by default; enable include_static_assets to retain browser assets", filtered, pluralSuffix(filtered, "y was", "ies were")))
	}
	return exchanges, warnings, filtered, nil
}

func extractHARItems(payload any) ([]map[string]any, error) {
	root, ok := payload.(map[string]any)
	if !ok {
		return nil, errors.New("HAR content must be a JSON object")
	}
	if logObj, ok := root["log"].(map[string]any); ok {
		root = logObj
	}
	if entries, ok := root["entries"].([]any); ok {
		items := make([]map[string]any, 0, len(entries))
		for _, entry := range entries {
			mapped, ok := entry.(map[string]any)
			if !ok {
				return nil, errors.New("HAR entries must be objects")
			}
			items = append(items, mapped)
		}
		return items, nil
	}
	if _, hasRequest := root["request"]; hasRequest {
		return []map[string]any{root}, nil
	}
	return nil, errors.New("HAR content must include log.entries or a single entry with request/response")
}

func isStaticHAREntry(item map[string]any) bool {
	requestData, _ := item["request"].(map[string]any)
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
		return exchange{}, errors.New("HAR entry is missing request")
	}
	location := fmt.Sprintf("HAR entry %d", index+1)
	body, bodyEncoding := harBody(requestData, diagnostics, location)
	ex := exchange{
		Method:       strings.ToUpper(stringValue(requestData["method"])),
		URL:          stringValue(requestData["url"]),
		Body:         body,
		BodyEncoding: bodyEncoding,
		Headers:      headerListToMap(requestData["headers"], diagnostics, location),
		DurationMs:   floatValue(item["time"]),
	}
	if ex.Headers == nil {
		ex.Headers = map[string]string{}
	}
	if ex.Method != "" || ex.URL != "" {
		location = strings.TrimSpace(ex.Method + " " + ex.URL)
	}
	applyHARPostDataHeaders(requestData, ex.Headers)
	mergeHARCookies(requestData["cookies"], ex.Headers, diagnostics, location)
	if startedAt, ok := parseHARStartedAt(item["startedDateTime"]); ok {
		ex.StartedAt = startedAt
		ex.HasStartedAt = true
	}
	if ex.Method == "" || ex.URL == "" {
		return exchange{}, errors.New("HAR request entries must include method and url")
	}
	if responseData, ok := item["response"].(map[string]any); ok {
		responseBody := ""
		if content, ok := responseData["content"].(map[string]any); ok {
			responseBody = decodeHARText(bodyValue(content["text"]), stringValue(content["encoding"]), diagnostics, location+" response", "response body")
		}
		ex.Response = &response{
			Status:  intValue(responseData["status"]),
			Headers: headerListToMap(responseData["headers"], diagnostics, location+" response"),
			Body:    responseBody,
		}
	}
	return ex, nil
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

func harBody(requestData map[string]any, diagnostics *diag.Collector, location string) (string, string) {
	if postData, ok := requestData["postData"].(map[string]any); ok {
		if text := bodyValue(postData["text"]); text != "" {
			return decodeHARRequestBody(text, stringValue(postData["encoding"]), diagnostics, location)
		}
		return harParamsBody(postData, diagnostics, location), ""
	}
	return "", ""
}

func decodeHARText(text string, encoding string, diagnostics *diag.Collector, location string, label string) string {
	if !strings.EqualFold(strings.TrimSpace(encoding), "base64") {
		return text
	}
	decoded, err := decodeBase64Text(text)
	if err != nil {
		diagnostics.Add(diag.Warning, "HAR base64 content", location, label+" is marked base64 but could not be decoded; original text was preserved")
		return text
	}
	if !utf8.Valid(decoded) {
		placeholder := "{{binary.har_" + strings.ReplaceAll(strings.ToLower(label), " ", "_") + "}}"
		diagnostics.Add(diag.Info, "HAR base64 content", location, label+" decoded to non-UTF-8 bytes and was represented with "+placeholder)
		return placeholder
	}
	return string(decoded)
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

func harParamsBody(postData map[string]any, diagnostics *diag.Collector, location string) string {
	params, ok := postData["params"].([]any)
	if !ok || len(params) == 0 {
		return ""
	}
	mimeType := strings.ToLower(stringValue(postData["mimeType"]))
	if strings.Contains(mimeType, "multipart/form-data") {
		return harMultipartBody(params, diagnostics, location)
	}
	values := url.Values{}
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
			placeholder := "{{file." + sanitizeIdentifier(name) + "}}"
			if filename != "" {
				placeholder = filename
			}
			value = placeholder
			diagnostics.Add(diag.Warning, "HAR postData.params file", location, "form parameter "+name+" references file content that is not embedded in the HAR; replace the placeholder before replay")
		}
		values.Add(name, value)
	}
	if !strings.Contains(mimeType, "application/x-www-form-urlencoded") && mimeType != "" {
		diagnostics.Add(diag.Info, "HAR postData.params", location, "postData.params were serialized as URL-encoded form data because text was absent")
	}
	return values.Encode()
}

func harMultipartBody(params []any, diagnostics *diag.Collector, location string) string {
	const boundary = "----apexload-har-boundary"
	var b strings.Builder
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
			if contentType := stringValue(param["contentType"]); contentType != "" {
				b.WriteString("Content-Type: " + contentType + "\r\n")
			}
			b.WriteString("\r\n{{file." + sanitizeIdentifier(name) + "}}\r\n")
			diagnostics.Add(diag.Warning, "HAR multipart file", location, "multipart parameter "+name+" references file bytes that are not embedded in the HAR; replace {{file."+sanitizeIdentifier(name)+"}} before replay")
			continue
		}
		b.WriteString(`Content-Disposition: form-data; name="` + name + `"` + "\r\n\r\n")
		b.WriteString(value + "\r\n")
	}
	b.WriteString("--" + boundary + "--\r\n")
	return b.String()
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
	if mimeType == "" || headerLookup(headers, "Content-Type") != "" {
		return
	}
	if strings.Contains(strings.ToLower(mimeType), "multipart/form-data") && !strings.Contains(strings.ToLower(mimeType), "boundary=") {
		mimeType += "; boundary=----apexload-har-boundary"
	}
	headers["Content-Type"] = mimeType
}

func mergeHARCookies(value any, headers map[string]string, diagnostics *diag.Collector, location string) {
	if headerLookup(headers, "Cookie") != "" {
		return
	}
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return
	}
	pairs := make([]string, 0, len(items))
	for _, item := range items {
		mapped, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := stringValue(mapped["name"])
		if name == "" {
			continue
		}
		pairs = append(pairs, name+"="+stringValue(mapped["value"]))
	}
	if len(pairs) == 0 {
		return
	}
	if headers == nil {
		return
	}
	headers["Cookie"] = strings.Join(pairs, "; ")
	diagnostics.Add(diag.Info, "HAR cookies[]", location, "request cookies[] were merged into a Cookie header; browser cookie-jar state is not simulated")
}
