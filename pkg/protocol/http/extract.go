package http

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/PuerkitoBio/goquery"
	"github.com/antchfx/htmlquery"
	"github.com/antchfx/xmlquery"
	"github.com/antchfx/xpath"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jmespath/go-jmespath"
)

// compiledRegexCache memoizes *regexp.Regexp keyed by the exact pattern string.
// Extractor expressions are static for the life of a script, so without caching
// each VUser would recompile the same pattern on every request — a measurable
// CPU/allocation cost on the load-generator hot path. Only patterns derived from
// the static extractor config (delimiters/expression) are cached here; nothing
// keyed on per-request response bytes is ever stored, so the map stays bounded
// by the number of distinct extractor patterns in the script.
var compiledRegexCache sync.Map // map[string]*regexp.Regexp

// compileRegexCached returns a shared compiled regexp for expr, compiling it on
// the first request and reusing the result thereafter. Compile errors are not
// cached: an invalid pattern surfaces the same error on every call, exactly as
// regexp.Compile did before.
func compileRegexCached(expr string) (*regexp.Regexp, error) {
	if cached, ok := compiledRegexCache.Load(expr); ok {
		return cached.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, err
	}
	actual, _ := compiledRegexCache.LoadOrStore(expr, re)
	return actual.(*regexp.Regexp), nil
}

const xpathFragmentExpressionPrefix = "__apexload_xpath_fragment__:"

const xpathExpressionConfigPrefix = "__apexload_xpath_config__:"

const regexOrdinalExpressionConfigPrefix = "__apexload_regex_ordinal__:"

const boundaryExpressionConfigPrefix = "__apexload_boundary_config__:"

type xpathExpressionConfig struct {
	Expression string            `json:"expression"`
	Fragment   bool              `json:"fragment,omitempty"`
	Namespaces map[string]string `json:"namespaces,omitempty"`
}

type regexOrdinalExpressionConfig struct {
	Expression string `json:"expression"`
	Ordinal    int    `json:"ordinal"`
}

type boundaryExpressionConfig struct {
	Left            string `json:"left"`
	Right           string `json:"right"`
	Ordinal         int    `json:"ordinal,omitempty"`
	CaseInsensitive bool   `json:"case_insensitive,omitempty"`
}

func extract(ext protocol.Extractor, headers http.Header, body []byte) (string, error) {
	value, err := extractRaw(ext, headers, body)
	if err != nil {
		return "", err
	}
	return postProcessExtractedValue(ext, value)
}

func extractRaw(ext protocol.Extractor, headers http.Header, body []byte) (string, error) {
	switch ext.Source {
	case "", "body":
		return extractFromBody(ext, body)
	case "header":
		return extractFromHeaders(ext, headers)
	default:
		return "", fmt.Errorf("unsupported extraction source: %s", ext.Source)
	}
}

// extractAll returns every match for a save-all (mode "all") extractor, with
// per-value post-processing applied. Zero matches return an empty slice and no
// error; the caller decides whether that counts as a miss.
func extractAll(ext protocol.Extractor, headers http.Header, body []byte) ([]string, error) {
	content := body
	switch ext.Source {
	case "", "body":
	case "header":
		content = []byte(flattenHeaders(headers))
	default:
		return nil, fmt.Errorf("unsupported extraction source: %s", ext.Source)
	}

	var values []string
	var err error
	switch ext.Type {
	case "regex":
		values, err = extractAllRegex(ext.Expression, content)
	case "boundary":
		values, err = extractAllBoundary(ext.Expression, content)
	case "jsonpath", "json_path":
		values, err = extractAllJSONPath(ext.Expression, content)
	case "xpath":
		values, err = extractAllXPath(ext.Expression, content)
	default:
		return nil, fmt.Errorf("extraction type %q does not support mode \"all\"", ext.Type)
	}
	if err != nil {
		return nil, err
	}
	for i, value := range values {
		processed, err := postProcessExtractedValue(ext, value)
		if err != nil {
			return nil, err
		}
		values[i] = processed
	}
	return values, nil
}

// postProcessExtractedValue applies the optional save_offset / save_length /
// convert transforms to one matched value.
func postProcessExtractedValue(ext protocol.Extractor, value string) (string, error) {
	if ext.SaveOffset > 0 {
		if ext.SaveOffset >= len(value) {
			value = ""
		} else {
			value = value[ext.SaveOffset:]
		}
	}
	if ext.SaveLength > 0 && ext.SaveLength < len(value) {
		value = value[:ext.SaveLength]
	}
	switch ext.Convert {
	case "":
		return value, nil
	case "url_encode":
		return url.QueryEscape(value), nil
	case "html_decode":
		return html.UnescapeString(value), nil
	case "html_url_encode":
		// HTML-format data to URL format: decode entities first, then
		// percent-encode with %20 (not form-style '+') for spaces.
		encoded := url.QueryEscape(html.UnescapeString(value))
		return strings.ReplaceAll(encoded, "+", "%20"), nil
	default:
		return "", fmt.Errorf("unsupported extractor convert mode: %s", ext.Convert)
	}
}

func extractFromHeaders(ext protocol.Extractor, headers http.Header) (string, error) {
	switch ext.Type {
	case "", "header":
		v := headers.Get(ext.Expression)
		if v == "" {
			return "", fmt.Errorf("header %q not found", ext.Expression)
		}
		return v, nil
	case "regex":
		return extractRegex(ext.Expression, []byte(flattenHeaders(headers)))
	case "boundary":
		return extractBoundary(ext.Expression, []byte(flattenHeaders(headers)))
	default:
		return "", fmt.Errorf("unsupported header extraction type: %s", ext.Type)
	}
}

func flattenHeaders(headers http.Header) string {
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var builder strings.Builder
	for _, key := range keys {
		for _, value := range headers.Values(key) {
			builder.WriteString(key)
			builder.WriteString(": ")
			builder.WriteString(value)
			builder.WriteString("\n")
		}
	}
	return builder.String()
}

func extractFromBody(ext protocol.Extractor, body []byte) (string, error) {
	switch ext.Type {
	case "jsonpath", "json_path":
		return extractJSONPath(ext.Expression, body)
	case "jmespath":
		return extractJMESPath(ext.Expression, body)
	case "css":
		return extractCSS(ext.Expression, body)
	case "xpath":
		return extractXPath(ext.Expression, body)
	case "regex":
		return extractRegex(ext.Expression, body)
	case "regex_count":
		return extractRegexCount(ext.Expression, body)
	case "regex_ordinal":
		return extractRegexOrdinal(ext.Expression, body)
	case "boundary":
		return extractBoundary(ext.Expression, body)
	case "sse_concat":
		return extractSSEConcat(ext.Expression, body)
	case "sse_event":
		return extractSSEEvent(ext.Expression, body)
	default:
		return "", fmt.Errorf("unsupported extraction type: %s", ext.Type)
	}
}

func extractCSS(expr string, body []byte) (string, error) {
	selector, attribute := splitCSSExpression(expr)
	if selector == "" {
		return "", fmt.Errorf("css selector is required")
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("invalid HTML body: %w", err)
	}

	match := doc.Find(selector).First()
	if match.Length() == 0 {
		return "", fmt.Errorf("no match for CSS selector %q", selector)
	}

	if attribute != "" {
		value, ok := match.Attr(attribute)
		if !ok {
			return "", fmt.Errorf("attribute %q not found for CSS selector %q", attribute, selector)
		}
		return value, nil
	}

	return strings.TrimSpace(match.Text()), nil
}

func extractXPath(expr string, body []byte) (string, error) {
	expr, fragment, namespaces, err := decodeXPathExpression(expr)
	if err != nil {
		return "", fmt.Errorf("invalid encoded XPath expression: %w", err)
	}
	if expr == "" {
		return "", fmt.Errorf("xpath expression is required")
	}

	if doc, err := xmlquery.Parse(strings.NewReader(string(body))); err == nil {
		selector, err := compileXPathSelector(expr, namespaces)
		if err != nil {
			return "", fmt.Errorf("invalid XPath expression: %w", err)
		}
		node := xmlquery.QuerySelector(doc, selector)
		if node == nil {
			return "", fmt.Errorf("no match for XPath %q", expr)
		}
		if fragment {
			return node.OutputXMLWithOptions(xmlquery.WithOutputSelf(), xmlquery.WithoutPreserveSpace()), nil
		}
		return node.InnerText(), nil
	}

	selector, err := compileXPathSelector(expr, namespaces)
	if err != nil {
		return "", fmt.Errorf("invalid XPath expression: %w", err)
	}

	doc, err := htmlquery.Parse(strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("invalid HTML/XML body: %w", err)
	}

	node := htmlquery.QuerySelector(doc, selector)
	if node == nil {
		return "", fmt.Errorf("no match for XPath %q", expr)
	}
	if fragment {
		return htmlquery.OutputHTML(node, true), nil
	}

	return htmlquery.InnerText(node), nil
}

func decodeXPathExpression(expr string) (string, bool, map[string]string, error) {
	expr = strings.TrimSpace(expr)
	if strings.HasPrefix(expr, xpathFragmentExpressionPrefix) {
		return strings.TrimSpace(strings.TrimPrefix(expr, xpathFragmentExpressionPrefix)), true, nil, nil
	}
	if strings.HasPrefix(expr, xpathExpressionConfigPrefix) {
		encoded := strings.TrimSpace(strings.TrimPrefix(expr, xpathExpressionConfigPrefix))
		payload, err := base64.RawStdEncoding.DecodeString(encoded)
		if err != nil {
			return "", false, nil, err
		}
		var config xpathExpressionConfig
		if err := json.Unmarshal(payload, &config); err != nil {
			return "", false, nil, err
		}
		return strings.TrimSpace(config.Expression), config.Fragment, config.Namespaces, nil
	}
	return expr, false, nil, nil
}

func compileXPathSelector(expr string, namespaces map[string]string) (*xpath.Expr, error) {
	if len(namespaces) == 0 {
		return xpath.Compile(expr)
	}
	return xpath.CompileWithNS(expr, namespaces)
}

func splitCSSExpression(expr string) (selector string, attribute string) {
	parts := strings.SplitN(expr, "|||", 2)
	selector = strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		attribute = strings.TrimSpace(parts[1])
	}
	return selector, attribute
}

func extractJSONPath(expr string, body []byte) (string, error) {
	current, err := extractJSONPathValue(expr, body)
	if err != nil {
		return "", err
	}
	return jsonPathValueString(current), nil
}

func extractJSONPathValue(expr string, body []byte) (any, error) {
	var current any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&current); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}

	if expr == "$" {
		return current, nil
	}
	if !strings.HasPrefix(expr, "$") {
		return nil, fmt.Errorf("jsonpath must start with $")
	}
	path := strings.TrimPrefix(expr, "$")
	for len(path) > 0 {
		switch path[0] {
		case '.':
			path = path[1:]
			nextSep := len(path)
			if dot := strings.Index(path, "."); dot >= 0 && dot < nextSep {
				nextSep = dot
			}
			if bracket := strings.Index(path, "["); bracket >= 0 && bracket < nextSep {
				nextSep = bracket
			}
			if nextSep == 0 {
				return nil, fmt.Errorf("empty jsonpath field in %q", expr)
			}
			field := path[:nextSep]
			obj, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("expected object at %q", field)
			}
			var exists bool
			current, exists = obj[field]
			if !exists {
				return nil, fmt.Errorf("field %q not found", field)
			}
			path = path[nextSep:]
		case '[':
			close := strings.Index(path, "]")
			if close < 0 {
				return nil, fmt.Errorf("unterminated jsonpath index in %q", expr)
			}
			indexText := path[1:close]
			idx, err := strconv.Atoi(indexText)
			if err != nil {
				return nil, fmt.Errorf("unsupported jsonpath index %q", indexText)
			}
			arr, ok := current.([]any)
			if !ok {
				return nil, fmt.Errorf("expected array for index %d", idx)
			}
			if idx < 0 || idx >= len(arr) {
				return nil, fmt.Errorf("index %d out of range", idx)
			}
			current = arr[idx]
			path = path[close+1:]
		default:
			return nil, fmt.Errorf("unsupported jsonpath segment in %q", expr)
		}
	}

	return current, nil
}

func jsonPathValueString(value any) string {
	switch typed := value.(type) {
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 32)
	default:
		return fmt.Sprintf("%v", value)
	}
}

// stringifyJSONLeaf renders one JSON value as a variable string: scalars use
// their plain form, composites round-trip through JSON encoding.
func stringifyJSONLeaf(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []any, map[string]any:
		encoded, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(encoded)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func extractJMESPath(expr string, body []byte) (string, error) {
	var current any
	if err := json.Unmarshal(body, &current); err != nil {
		return "", fmt.Errorf("invalid JSON body: %w", err)
	}

	result, err := jmespath.Search(expr, current)
	if err != nil {
		return "", fmt.Errorf("invalid JMESPath expression: %w", err)
	}
	if result == nil {
		return "", fmt.Errorf("no match for JMESPath %q", expr)
	}

	switch value := result.(type) {
	case string:
		return value, nil
	case []any, map[string]any:
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", fmt.Errorf("marshal JMESPath result: %w", err)
		}
		return string(encoded), nil
	default:
		return fmt.Sprintf("%v", value), nil
	}
}

func extractRegex(expr string, body []byte) (string, error) {
	re, err := compileRegexCached(expr)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}
	matches := re.FindSubmatch(body)
	if len(matches) < 2 {
		return "", fmt.Errorf("no match for regex %q", expr)
	}
	return string(matches[1]), nil
}

func extractRegexCount(expr string, body []byte) (string, error) {
	re, err := compileRegexCached(expr)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}
	return fmt.Sprintf("%d", len(re.FindAll(body, -1))), nil
}

func extractRegexOrdinal(expr string, body []byte) (string, error) {
	config, err := decodeRegexOrdinalExpression(expr)
	if err != nil {
		return "", err
	}
	re, err := compileRegexCached(config.Expression)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}
	matches := re.FindAllSubmatch(body, -1)
	if len(matches) < config.Ordinal {
		return "", fmt.Errorf("regex %q match ordinal %d not found", config.Expression, config.Ordinal)
	}
	match := matches[config.Ordinal-1]
	if len(match) < 2 {
		return "", fmt.Errorf("regex %q ordinal %d did not capture a value", config.Expression, config.Ordinal)
	}
	return string(match[1]), nil
}

func decodeRegexOrdinalExpression(expr string) (regexOrdinalExpressionConfig, error) {
	if !strings.HasPrefix(expr, regexOrdinalExpressionConfigPrefix) {
		return regexOrdinalExpressionConfig{Expression: expr, Ordinal: 1}, nil
	}
	var config regexOrdinalExpressionConfig
	payload := strings.TrimPrefix(expr, regexOrdinalExpressionConfigPrefix)
	if err := json.Unmarshal([]byte(payload), &config); err != nil {
		return config, fmt.Errorf("invalid regex ordinal config: %w", err)
	}
	if strings.TrimSpace(config.Expression) == "" {
		return config, fmt.Errorf("regex ordinal expression is required")
	}
	if config.Ordinal <= 0 {
		return config, fmt.Errorf("regex ordinal must be greater than zero")
	}
	return config, nil
}

// asciiLowerFold lowercases ASCII letters only, preserving byte length so
// indices computed on the folded string remain valid on the original.
// Unicode-aware folding (strings.ToLower) can change byte length — U+212A
// "K" shrinks, U+023A "Ⱥ" grows — which would corrupt extracted slices.
func asciiLowerFold(s string) string {
	hasUpper := false
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			hasUpper = true
			break
		}
	}
	if !hasUpper {
		return s
	}
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

func extractBoundary(expr string, body []byte) (string, error) {
	config, err := decodeBoundaryExpression(expr)
	if err != nil {
		return "", err
	}

	s := string(body)
	haystack := s
	left, right := config.Left, config.Right
	if config.CaseInsensitive {
		haystack = asciiLowerFold(s)
		left = asciiLowerFold(left)
		right = asciiLowerFold(right)
	}

	ordinal := config.Ordinal
	if ordinal <= 0 {
		ordinal = 1
	}

	pos := 0
	for occurrence := 1; ; occurrence++ {
		li := strings.Index(haystack[pos:], left)
		if li < 0 {
			if occurrence == 1 {
				return "", fmt.Errorf("left boundary %q not found", config.Left)
			}
			return "", fmt.Errorf("boundary match ordinal %d not found (only %d occurrences)", ordinal, occurrence-1)
		}
		start := pos + li + len(left)
		// An empty right boundary extends the match to the end of the content.
		ri := len(haystack) - start
		if right != "" {
			ri = strings.Index(haystack[start:], right)
		}
		if ri < 0 {
			if occurrence == 1 {
				return "", fmt.Errorf("right boundary %q not found", config.Right)
			}
			return "", fmt.Errorf("boundary match ordinal %d not found (only %d occurrences)", ordinal, occurrence-1)
		}
		if occurrence == ordinal {
			return s[start : start+ri], nil
		}
		pos = start + ri + len(right)
	}
}

func extractAllRegex(expr string, body []byte) ([]string, error) {
	re, err := compileRegexCached(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}
	matches := re.FindAllSubmatch(body, -1)
	values := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		values = append(values, string(match[1]))
	}
	return values, nil
}

func extractAllBoundary(expr string, body []byte) ([]string, error) {
	config, err := decodeBoundaryExpression(expr)
	if err != nil {
		return nil, err
	}
	s := string(body)
	haystack := s
	left, right := config.Left, config.Right
	if config.CaseInsensitive {
		haystack = asciiLowerFold(s)
		left = asciiLowerFold(left)
		right = asciiLowerFold(right)
	}

	var values []string
	pos := 0
	for pos <= len(haystack) {
		li := strings.Index(haystack[pos:], left)
		if li < 0 {
			break
		}
		start := pos + li + len(left)
		ri := len(haystack) - start
		if right != "" {
			ri = strings.Index(haystack[start:], right)
			if ri < 0 {
				break
			}
		}
		values = append(values, s[start:start+ri])
		if right == "" {
			// An empty right boundary consumes the remainder; only one match
			// is possible.
			break
		}
		next := start + ri + len(right)
		if next <= pos {
			break
		}
		pos = next
	}
	return values, nil
}

func extractAllJSONPath(expr string, body []byte) ([]string, error) {
	value, err := extractJSONPathValue(expr, body)
	if err != nil {
		return nil, err
	}
	if arr, ok := value.([]any); ok {
		values := make([]string, 0, len(arr))
		for _, item := range arr {
			values = append(values, stringifyJSONLeaf(item))
		}
		return values, nil
	}
	return []string{stringifyJSONLeaf(value)}, nil
}

func extractAllXPath(expr string, body []byte) ([]string, error) {
	expr, fragment, namespaces, err := decodeXPathExpression(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid encoded XPath expression: %w", err)
	}
	if expr == "" {
		return nil, fmt.Errorf("xpath expression is required")
	}
	selector, err := compileXPathSelector(expr, namespaces)
	if err != nil {
		return nil, fmt.Errorf("invalid XPath expression: %w", err)
	}

	if doc, err := xmlquery.Parse(strings.NewReader(string(body))); err == nil {
		nodes := xmlquery.QuerySelectorAll(doc, selector)
		values := make([]string, 0, len(nodes))
		for _, node := range nodes {
			if fragment {
				values = append(values, node.OutputXMLWithOptions(xmlquery.WithOutputSelf(), xmlquery.WithoutPreserveSpace()))
			} else {
				values = append(values, node.InnerText())
			}
		}
		return values, nil
	}

	doc, err := htmlquery.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("invalid HTML/XML body: %w", err)
	}
	nodes := htmlquery.QuerySelectorAll(doc, selector)
	values := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if fragment {
			values = append(values, htmlquery.OutputHTML(node, true))
		} else {
			values = append(values, htmlquery.InnerText(node))
		}
	}
	return values, nil
}

func decodeBoundaryExpression(expr string) (boundaryExpressionConfig, error) {
	if strings.HasPrefix(expr, boundaryExpressionConfigPrefix) {
		var config boundaryExpressionConfig
		payload := strings.TrimPrefix(expr, boundaryExpressionConfigPrefix)
		if err := json.Unmarshal([]byte(payload), &config); err != nil {
			return config, fmt.Errorf("invalid boundary config: %w", err)
		}
		if config.Left == "" && config.Right == "" {
			return config, fmt.Errorf("boundary config requires a left or right boundary")
		}
		return config, nil
	}
	parts := strings.SplitN(expr, "|||", 2)
	if len(parts) != 2 {
		return boundaryExpressionConfig{}, fmt.Errorf("boundary expression must use ||| separator: left|||right")
	}
	return boundaryExpressionConfig{Left: parts[0], Right: parts[1], Ordinal: 1}, nil
}
