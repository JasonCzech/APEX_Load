package sample

// rawhttp.go — split from converter.go (same package, behavior-preserving).

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/dsl"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

const (
	maxRawHTTPContentBytes    = 16 << 20
	maxRawHTTPMessages        = 4096
	maxRawHTTPHeaderBytes     = 1 << 20
	maxRawHTTPHeaderLines     = 8192
	maxRawHTTPHeaderLineBytes = 128 << 10
	maxRawHTTPStartLineBytes  = 16 << 10
)

func parseRawHTTP(content, responseContent, baseURL string) ([]exchange, []string, error) {
	if len(content) > maxRawHTTPContentBytes {
		return nil, nil, fmt.Errorf("raw HTTP request content exceeds %d bytes", maxRawHTTPContentBytes)
	}
	if len(responseContent) > maxRawHTTPContentBytes {
		return nil, nil, fmt.Errorf("raw HTTP response content exceeds %d bytes", maxRawHTTPContentBytes)
	}
	requestSamples, err := splitBatchSamples(content, rawHTTPRequestDelimiter)
	if err != nil {
		return nil, nil, fmt.Errorf("split raw HTTP requests: %w", err)
	}
	if len(requestSamples) == 0 {
		return nil, nil, errors.New("raw HTTP request is empty")
	}
	responseSamples, err := splitBatchSamples(responseContent, rawHTTPResponseDelimiter)
	if err != nil {
		return nil, nil, fmt.Errorf("split raw HTTP responses: %w", err)
	}
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

func splitBatchSamples(content, delimiter string) ([]string, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, nil
	}
	if !strings.Contains(content, delimiter) {
		return []string{content}, nil
	}
	if strings.Count(content, delimiter) >= maxRawHTTPMessages {
		return nil, fmt.Errorf("message count exceeds %d", maxRawHTTPMessages)
	}
	parts := strings.Split(content, delimiter)
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			filtered = append(filtered, part)
		}
	}
	if len(filtered) > maxRawHTTPMessages {
		return nil, fmt.Errorf("message count exceeds %d", maxRawHTTPMessages)
	}
	return filtered, nil
}

func parseRawRequest(content, baseURL string) (exchange, error) {
	head, body := splitHTTPMessage(content)
	if err := validateRawHTTPHead(head); err != nil {
		return exchange{}, fmt.Errorf("invalid raw HTTP request headers: %w", err)
	}
	lines := splitLines(head)
	if len(lines) == 0 {
		return exchange{}, errors.New("raw HTTP request is empty")
	}
	if len(lines[0]) > maxRawHTTPStartLineBytes {
		return exchange{}, fmt.Errorf("raw HTTP request line exceeds %d bytes", maxRawHTTPStartLineBytes)
	}
	parts := strings.Fields(lines[0])
	if len(parts) < 2 || len(parts) > 3 {
		return exchange{}, errors.New("raw HTTP request line must contain method, target, and optional HTTP version")
	}
	if !validHTTPToken(parts[0]) {
		return exchange{}, fmt.Errorf("raw HTTP request method %q is invalid", parts[0])
	}
	if len(parts) == 3 {
		if _, _, ok := http.ParseHTTPVersion(parts[2]); !ok {
			return exchange{}, fmt.Errorf("raw HTTP request version %q is invalid", parts[2])
		}
	}
	unfolded, err := unfoldHeaderLines(lines[1:])
	if err != nil {
		return exchange{}, fmt.Errorf("invalid raw HTTP request headers: %w", err)
	}
	headers, headerList, err := parseHeaderLines(unfolded)
	if err != nil {
		return exchange{}, fmt.Errorf("invalid raw HTTP request headers: %w", err)
	}
	chunked, err := rawHTTPUsesChunkedTransferEncoding(headerList)
	if err != nil {
		return exchange{}, fmt.Errorf("invalid raw HTTP request Transfer-Encoding: %w", err)
	}
	if chunked {
		decoded, err := decodeRawChunkedBody(body)
		if err != nil {
			return exchange{}, fmt.Errorf("invalid chunked raw HTTP request body: %w", err)
		}
		body = decoded
		deleteHeader(headers, "Transfer-Encoding")
		headerList = filterHeaderList(headerList, "Transfer-Encoding")
		// Content-Length on a chunked message is ignored on the wire and usually
		// describes the encoded framing rather than the decoded action body.
		deleteHeader(headers, "Content-Length")
		headerList = filterHeaderList(headerList, "Content-Length")
	}
	metadata := map[string]any{}
	if len(parts) >= 3 {
		if httpprotocol.NormalizeVersion(parts[2]) == httpprotocol.VersionHTTP3 {
			// HTTP/3 has no text request line: the line describes a logical
			// request, so it stays an observation for review, never a policy.
			metadata[rawHTTP3RequestLineKey] = parts[2]
		} else {
			metadata["http_version"] = parts[2]
		}
	}
	if parts[1] == "*" || (!strings.HasPrefix(parts[1], "/") && !isAbsoluteRawHTTPURL(parts[1])) {
		metadata["raw_request_target"] = parts[1]
	}
	bodyEncoding := ""
	if !utf8.ValidString(body) {
		body = base64.StdEncoding.EncodeToString([]byte(body))
		bodyEncoding = "base64"
	}
	ex := exchange{
		Method:       strings.ToUpper(parts[0]),
		URL:          parts[1],
		HTTPVersion:  stringMapValue(metadata, "http_version"),
		Headers:      headers,
		HeaderList:   headerList,
		Body:         body,
		BodyEncoding: bodyEncoding,
		Metadata:     metadata,
	}
	if !isAbsoluteRawHTTPURL(ex.URL) {
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

func isAbsoluteRawHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Host != "" &&
		(strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https"))
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
	if err := validateRawHTTPHead(head); err != nil {
		return nil, nil, fmt.Errorf("invalid raw HTTP response headers: %w", err)
	}
	lines := splitLines(head)
	if len(lines) == 0 {
		return nil, nil, errors.New("raw HTTP response is empty")
	}
	if len(lines[0]) > maxRawHTTPStartLineBytes {
		return nil, nil, fmt.Errorf("raw HTTP response status line exceeds %d bytes", maxRawHTTPStartLineBytes)
	}
	parts := strings.Fields(lines[0])
	if len(parts) < 2 {
		return nil, nil, errors.New("raw HTTP response status line is invalid")
	}
	if _, _, ok := http.ParseHTTPVersion(parts[0]); !ok {
		return nil, nil, fmt.Errorf("invalid HTTP response version %q", parts[0])
	}
	status, err := strconv.Atoi(parts[1])
	if err != nil || status < 100 || status > 999 {
		return nil, nil, fmt.Errorf("invalid HTTP response status %q", parts[1])
	}
	unfolded, err := unfoldHeaderLines(lines[1:])
	if err != nil {
		return nil, nil, fmt.Errorf("invalid raw HTTP response headers: %w", err)
	}
	headers, headerList, err := parseHeaderLines(unfolded)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid raw HTTP response headers: %w", err)
	}
	chunked, err := rawHTTPUsesChunkedTransferEncoding(headerList)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid raw HTTP response Transfer-Encoding: %w", err)
	}
	if chunked {
		decoded, err := decodeRawChunkedBody(body)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid chunked raw HTTP response body: %w", err)
		}
		body = decoded
		deleteHeader(headers, "Transfer-Encoding")
		headerList = filterHeaderList(headerList, "Transfer-Encoding")
	}
	return &response{Status: status, Headers: headers, HeaderList: headerList, Body: body}, nil, nil
}

func validateRawHTTPHead(head string) error {
	if len(head) > maxRawHTTPHeaderBytes {
		return fmt.Errorf("header block exceeds %d bytes", maxRawHTTPHeaderBytes)
	}
	if strings.Count(head, "\n") > maxRawHTTPHeaderLines {
		return fmt.Errorf("header block exceeds %d lines", maxRawHTTPHeaderLines)
	}
	if !utf8.ValidString(head) {
		return errors.New("header block contains invalid UTF-8")
	}
	return nil
}

func splitHTTPMessage(content string) (string, string) {
	// Pasted captures commonly mix LF and CRLF line endings at the blank line.
	// Treat any two consecutive supported line terminators as the boundary and
	// choose the earliest logical boundary, not merely the first pattern checked.
	boundaryIndex := -1
	boundaryBytes := 0
	for _, boundary := range []string{"\r\n\r\n", "\r\n\n", "\n\r\n", "\n\n"} {
		index := strings.Index(content, boundary)
		if index >= 0 && (boundaryIndex < 0 || index < boundaryIndex) {
			boundaryIndex = index
			boundaryBytes = len(boundary)
		}
	}
	if boundaryIndex >= 0 {
		return content[:boundaryIndex], content[boundaryIndex+boundaryBytes:]
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

func parseHeaderLines(lines []string) (map[string]string, dsl.HeaderList, error) {
	if len(lines) > maxRawHTTPHeaderLines {
		return nil, nil, fmt.Errorf("header count exceeds %d", maxRawHTTPHeaderLines)
	}
	headers := map[string]string{}
	headerList := dsl.HeaderList{}
	summaryNames := make(map[string]string, len(lines))
	for index, line := range lines {
		key, value, ok := splitHeader(line)
		if !ok {
			return nil, nil, fmt.Errorf("header line %d is malformed", index+1)
		}
		lower := strings.ToLower(key)
		if previous := summaryNames[lower]; previous != "" && previous != key {
			delete(headers, previous)
		}
		summaryNames[lower] = key
		headers[key] = value
		headerList = append(headerList, dsl.HeaderEntry{Name: key, Value: value})
	}
	return headers, headerList, nil
}

func unfoldHeaderLines(lines []string) ([]string, error) {
	if len(lines) > maxRawHTTPHeaderLines {
		return nil, fmt.Errorf("header count exceeds %d", maxRawHTTPHeaderLines)
	}
	unfolded := make([]string, 0, len(lines))
	var current strings.Builder
	haveCurrent := false
	flush := func() {
		if !haveCurrent {
			return
		}
		unfolded = append(unfolded, current.String())
		current.Reset()
		haveCurrent = false
	}
	for _, line := range lines {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			if !haveCurrent {
				return nil, errors.New("header continuation has no preceding header")
			}
			continuation := strings.TrimSpace(line)
			if current.Len()+1+len(continuation) > maxRawHTTPHeaderLineBytes {
				return nil, fmt.Errorf("unfolded header line exceeds %d bytes", maxRawHTTPHeaderLineBytes)
			}
			current.WriteByte(' ')
			current.WriteString(continuation)
			continue
		}
		flush()
		if len(line) > maxRawHTTPHeaderLineBytes {
			return nil, fmt.Errorf("header line exceeds %d bytes", maxRawHTTPHeaderLineBytes)
		}
		current.WriteString(line)
		haveCurrent = true
	}
	flush()
	return unfolded, nil
}

func splitHeader(value string) (string, string, bool) {
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	key := parts[0]
	if key != strings.TrimSpace(key) || !validHTTPToken(key) {
		return "", "", false
	}
	headerValue := strings.Trim(parts[1], " \t")
	for index := 0; index < len(headerValue); index++ {
		if headerValue[index] == '\t' || headerValue[index] >= 0x20 && headerValue[index] != 0x7f {
			continue
		}
		return "", "", false
	}
	return key, headerValue, true
}

func validHTTPToken(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' {
			continue
		}
		switch char {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			continue
		default:
			return false
		}
	}
	return true
}

func rawHTTPUsesChunkedTransferEncoding(headers dsl.HeaderList) (bool, error) {
	codings := make([]string, 0, 1)
	for _, header := range headers {
		if !strings.EqualFold(header.Name, "Transfer-Encoding") {
			continue
		}
		for _, rawCoding := range strings.Split(header.Value, ",") {
			coding, _, _ := strings.Cut(strings.TrimSpace(rawCoding), ";")
			if !validHTTPToken(coding) {
				return false, fmt.Errorf("invalid transfer coding %q", rawCoding)
			}
			codings = append(codings, coding)
		}
	}
	chunkedIndex := -1
	for index, coding := range codings {
		if strings.EqualFold(coding, "chunked") {
			if chunkedIndex >= 0 {
				return false, errors.New("chunked transfer coding is repeated")
			}
			chunkedIndex = index
		}
	}
	if chunkedIndex < 0 {
		return false, nil
	}
	if chunkedIndex != len(codings)-1 {
		return false, errors.New("chunked transfer coding is not final")
	}
	if len(codings) != 1 {
		return false, fmt.Errorf("unsupported transfer coding before chunked: %q", strings.Join(codings[:chunkedIndex], ", "))
	}
	return true, nil
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

// rawHTTP3RequestLineKey keeps a raw request line's HTTP/3 version as an
// observation: the author converts the logical request explicitly.
const rawHTTP3RequestLineKey = "raw_http3_request_line"
