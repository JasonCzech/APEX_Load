package sample

// rawhttp.go — split from converter.go (same package, behavior-preserving).

import (
	"errors"
	"fmt"
	"io"
	"net/http/httputil"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func parseRawHTTP(content, responseContent, baseURL string) ([]exchange, []string, error) {
	requestSamples := splitBatchSamples(content, rawHTTPRequestDelimiter)
	if len(requestSamples) == 0 {
		return nil, nil, errors.New("raw HTTP request is empty")
	}
	responseSamples := splitBatchSamples(responseContent, rawHTTPResponseDelimiter)
	if len(responseSamples) > 0 && len(responseSamples) != len(requestSamples) {
		return nil, nil, fmt.Errorf("raw HTTP request count (%d) does not match response count (%d)", len(requestSamples), len(responseSamples))
	}

	warnings := []string{}
	exchanges := make([]exchange, 0, len(requestSamples))
	for index, sample := range requestSamples {
		req, err := parseRawRequest(sample, baseURL)
		if err != nil {
			return nil, nil, err
		}
		if len(responseSamples) > 0 {
			resp, respWarnings, err := parseRawResponse(responseSamples[index])
			if err != nil {
				return nil, nil, err
			}
			req.Response = resp
			warnings = append(warnings, respWarnings...)
		}
		exchanges = append(exchanges, req)
	}
	return exchanges, warnings, nil
}

func splitBatchSamples(content, delimiter string) []string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil
	}
	if !strings.Contains(content, delimiter) {
		return []string{content}
	}
	parts := strings.Split(content, delimiter)
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			filtered = append(filtered, part)
		}
	}
	return filtered
}

func parseRawRequest(content, baseURL string) (exchange, error) {
	head, body := splitHTTPMessage(content)
	lines := splitLines(head)
	if len(lines) == 0 {
		return exchange{}, errors.New("raw HTTP request is empty")
	}
	parts := strings.Fields(lines[0])
	if len(parts) < 2 {
		return exchange{}, errors.New("raw HTTP request line must contain method and target")
	}
	headers, headerList := parseHeaderLines(unfoldHeaderLines(lines[1:]))
	if strings.Contains(strings.ToLower(headerLookup(headers, "Transfer-Encoding")), "chunked") {
		decoded, err := decodeRawChunkedBody(body)
		if err != nil {
			return exchange{}, fmt.Errorf("invalid chunked raw HTTP request body: %w", err)
		}
		body = decoded
		deleteHeader(headers, "Transfer-Encoding")
		headerList = filterHeaderList(headerList, "Transfer-Encoding")
	}
	metadata := map[string]any{}
	if len(parts) >= 3 {
		metadata["http_version"] = parts[2]
	}
	if parts[1] == "*" || (!strings.HasPrefix(parts[1], "/") && !strings.HasPrefix(parts[1], "http://") && !strings.HasPrefix(parts[1], "https://")) {
		metadata["raw_request_target"] = parts[1]
	}
	ex := exchange{
		Method:      strings.ToUpper(parts[0]),
		URL:         parts[1],
		HTTPVersion: stringMapValue(metadata, "http_version"),
		Headers:     headers,
		HeaderList:  headerList,
		Body:        body,
		Metadata:    metadata,
	}
	if !strings.HasPrefix(ex.URL, "http://") && !strings.HasPrefix(ex.URL, "https://") {
		host := headerLookup(ex.Headers, "host")
		if baseURL != "" {
			ex.URL = resolveRawHTTPRelativeTarget(baseURL, ex.Method, ex.URL)
		} else if host != "" {
			scheme := "https"
			if strings.Contains(host, "localhost") || strings.Contains(host, "127.0.0.1") {
				scheme = "http"
			}
			ex.URL = resolveRawHTTPRelativeTarget(scheme+"://"+host, ex.Method, ex.URL)
		} else {
			return exchange{}, errors.New("raw HTTP request must include an absolute URL, Host header, or base_url option")
		}
	}
	if len(metadata) == 0 {
		ex.Metadata = nil
	}
	return ex, nil
}

func parseRawResponse(content string) (*response, []string, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, nil, nil
	}
	if !strings.HasPrefix(strings.ToUpper(trimmed), "HTTP/") {
		return &response{Status: 200, Body: content}, []string{"response content did not include an HTTP status line; defaulted assertion status to 200"}, nil
	}
	head, body := splitHTTPMessage(content)
	lines := splitLines(head)
	if len(lines) == 0 {
		return nil, nil, errors.New("raw HTTP response is empty")
	}
	parts := strings.Fields(lines[0])
	if len(parts) < 2 {
		return nil, nil, errors.New("raw HTTP response status line is invalid")
	}
	status, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, nil, fmt.Errorf("invalid HTTP response status %q", parts[1])
	}
	headers, _ := parseHeaderLines(unfoldHeaderLines(lines[1:]))
	if strings.Contains(strings.ToLower(headerLookup(headers, "Transfer-Encoding")), "chunked") {
		decoded, err := decodeRawChunkedBody(body)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid chunked raw HTTP response body: %w", err)
		}
		body = decoded
		deleteHeader(headers, "Transfer-Encoding")
	}
	return &response{Status: status, Headers: headers, Body: body}, nil, nil
}

func splitHTTPMessage(content string) (string, string) {
	if strings.Contains(content, "\r\n\r\n") {
		parts := strings.SplitN(content, "\r\n\r\n", 2)
		return parts[0], parts[1]
	}
	parts := strings.SplitN(content, "\n\n", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return content, ""
}

func splitLines(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimRight(line, "\r")
		if trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	return filtered
}

func parseHeaderLines(lines []string) (map[string]string, dsl.HeaderList) {
	headers := map[string]string{}
	headerList := dsl.HeaderList{}
	for _, line := range lines {
		key, value, ok := splitHeader(line)
		if ok {
			headers[key] = value
			headerList = append(headerList, dsl.HeaderEntry{Name: key, Value: value})
		}
	}
	return headers, headerList
}

func unfoldHeaderLines(lines []string) []string {
	unfolded := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			if len(unfolded) == 0 {
				continue
			}
			unfolded[len(unfolded)-1] += " " + strings.TrimSpace(line)
			continue
		}
		unfolded = append(unfolded, line)
	}
	return unfolded
}

func splitHeader(value string) (string, string, bool) {
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}

func headerLookup(headers map[string]string, key string) string {
	for headerKey, value := range headers {
		if strings.EqualFold(headerKey, key) {
			return value
		}
	}
	return ""
}

func decodeRawChunkedBody(body string) (string, error) {
	decoded, err := io.ReadAll(httputil.NewChunkedReader(strings.NewReader(body)))
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func deleteHeader(headers map[string]string, key string) {
	for headerKey := range headers {
		if strings.EqualFold(headerKey, key) {
			delete(headers, headerKey)
		}
	}
}

func filterHeaderList(headers dsl.HeaderList, key string) dsl.HeaderList {
	if len(headers) == 0 {
		return headers
	}
	filtered := headers[:0]
	for _, header := range headers {
		if strings.EqualFold(header.Name, key) {
			continue
		}
		filtered = append(filtered, header)
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func resolveRawHTTPRelativeTarget(baseURL string, method string, target string) string {
	if target == "*" {
		return strings.TrimRight(baseURL, "/") + "/"
	}
	if !strings.HasPrefix(target, "/") && (strings.EqualFold(method, "CONNECT") || strings.Contains(target, ":")) {
		scheme := "https"
		if before, _, ok := strings.Cut(baseURL, "://"); ok && before != "" {
			scheme = before
		}
		return scheme + "://" + target
	}
	return strings.TrimRight(baseURL, "/") + ensureLeadingSlash(target)
}

func stringMapValue(values map[string]any, key string) string {
	if value, ok := values[key].(string); ok {
		return value
	}
	return ""
}
