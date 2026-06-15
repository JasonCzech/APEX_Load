package sample

// rawhttp.go — split from converter.go (same package, behavior-preserving).

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
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
	ex := exchange{
		Method:  strings.ToUpper(parts[0]),
		URL:     parts[1],
		Headers: parseHeaderLines(lines[1:]),
		Body:    body,
	}
	if !strings.HasPrefix(ex.URL, "http://") && !strings.HasPrefix(ex.URL, "https://") {
		host := headerLookup(ex.Headers, "host")
		if baseURL != "" {
			ex.URL = strings.TrimRight(baseURL, "/") + ensureLeadingSlash(ex.URL)
		} else if host != "" {
			scheme := "https"
			if strings.Contains(host, "localhost") || strings.Contains(host, "127.0.0.1") {
				scheme = "http"
			}
			ex.URL = scheme + "://" + host + ensureLeadingSlash(ex.URL)
		} else {
			return exchange{}, errors.New("raw HTTP request must include an absolute URL, Host header, or base_url option")
		}
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
	return &response{Status: status, Headers: parseHeaderLines(lines[1:]), Body: body}, nil, nil
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

func parseHeaderLines(lines []string) map[string]string {
	headers := map[string]string{}
	for _, line := range lines {
		key, value, ok := splitHeader(line)
		if ok {
			headers[key] = value
		}
	}
	return headers
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
