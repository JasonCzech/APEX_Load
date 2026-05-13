package http

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/antchfx/htmlquery"
	"github.com/antchfx/xmlquery"
	"github.com/antchfx/xpath"
	"github.com/jasonczech/stormrunner/pkg/protocol"
	"github.com/jmespath/go-jmespath"
)

const xpathFragmentExpressionPrefix = "__stormrunner_xpath_fragment__:"

const xpathExpressionConfigPrefix = "__stormrunner_xpath_config__:"

type xpathExpressionConfig struct {
	Expression string            `json:"expression"`
	Fragment   bool              `json:"fragment,omitempty"`
	Namespaces map[string]string `json:"namespaces,omitempty"`
}

func extract(ext protocol.Extractor, headers http.Header, body []byte) (string, error) {
	switch ext.Source {
	case "body":
		return extractFromBody(ext, body)
	case "header":
		return extractFromHeaders(ext, headers)
	default:
		return "", fmt.Errorf("unsupported extraction source: %s", ext.Source)
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
	case "jsonpath":
		return extractJSONPath(ext.Expression, body)
	case "jmespath":
		return extractJMESPath(ext.Expression, body)
	case "css":
		return extractCSS(ext.Expression, body)
	case "xpath":
		return extractXPath(ext.Expression, body)
	case "regex":
		return extractRegex(ext.Expression, body)
	case "boundary":
		return extractBoundary(ext.Expression, body)
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
	// Simple JSONPath: supports $.field and $.field.nested and $.array[N].field
	expr = strings.TrimPrefix(expr, "$.")
	parts := strings.Split(expr, ".")

	var current any
	if err := json.Unmarshal(body, &current); err != nil {
		return "", fmt.Errorf("invalid JSON body: %w", err)
	}

	for _, part := range parts {
		idx := -1
		if bracket := strings.Index(part, "["); bracket != -1 {
			idxStr := strings.TrimSuffix(part[bracket+1:], "]")
			fmt.Sscanf(idxStr, "%d", &idx)
			part = part[:bracket]
		}

		obj, ok := current.(map[string]any)
		if !ok {
			return "", fmt.Errorf("expected object at %q", part)
		}
		current, ok = obj[part]
		if !ok {
			return "", fmt.Errorf("field %q not found", part)
		}

		if idx >= 0 {
			arr, ok := current.([]any)
			if !ok {
				return "", fmt.Errorf("expected array at %q", part)
			}
			if idx >= len(arr) {
				return "", fmt.Errorf("index %d out of range for %q", idx, part)
			}
			current = arr[idx]
		}
	}

	return fmt.Sprintf("%v", current), nil
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
	re, err := regexp.Compile(expr)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}
	matches := re.FindSubmatch(body)
	if len(matches) < 2 {
		return "", fmt.Errorf("no match for regex %q", expr)
	}
	return string(matches[1]), nil
}

func extractBoundary(expr string, body []byte) (string, error) {
	parts := strings.SplitN(expr, "|||", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("boundary expression must use ||| separator: left|||right")
	}
	left, right := parts[0], parts[1]
	s := string(body)
	li := strings.Index(s, left)
	if li < 0 {
		return "", fmt.Errorf("left boundary %q not found", left)
	}
	start := li + len(left)
	ri := strings.Index(s[start:], right)
	if ri < 0 {
		return "", fmt.Errorf("right boundary %q not found", right)
	}
	return s[start : start+ri], nil
}
