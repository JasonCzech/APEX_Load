package jms

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/internal/boundedcache"
	"github.com/jasonczech/apexload/pkg/protocol"
)

var extractorRegexCache = boundedcache.NewStringCache[*regexp.Regexp](0, 0, 0)

func compileExtractorRegex(expr string) (*regexp.Regexp, error) {
	if cached, ok := extractorRegexCache.Load(expr); ok {
		return cached, nil
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, err
	}
	actual, _ := extractorRegexCache.LoadOrStore(expr, re)
	return actual, nil
}

func runExtractors(extractors []protocol.Extractor, result *protocol.Result, msg message) error {
	for _, extractor := range extractors {
		value, err := extract(extractor, msg)
		if err != nil {
			switch strings.ToLower(strings.TrimSpace(extractor.OnMiss)) {
			case "", "ignore":
				continue
			case "empty":
				value = ""
			default:
				return err
			}
		}
		if err := protocol.StoreExtraction(result, extractor.Name, value); err != nil {
			result.Extractions = nil
			return fmt.Errorf("JMS extractor %q output rejected: %w", extractor.Name, err)
		}
	}
	return nil
}

func extract(ext protocol.Extractor, msg message) (string, error) {
	switch ext.Source {
	case "body":
		switch ext.Type {
		case "regex":
			re, err := compileExtractorRegex(ext.Expression)
			if err != nil {
				return "", fmt.Errorf("invalid regex: %w", err)
			}
			matches := re.FindSubmatch(msg.Body)
			if len(matches) == 0 {
				return "", fmt.Errorf("no match for regex %q", ext.Expression)
			}
			if len(matches) == 1 {
				return string(matches[0]), nil
			}
			return string(matches[1]), nil
		case "jsonpath":
			return extractJSONPath(ext.Expression, msg.Body)
		default:
			return "", fmt.Errorf("unsupported JMS body extraction type: %s", ext.Type)
		}
	case "header":
		return lookupString(ext.Expression, msg.Headers, "header")
	case "property":
		return lookupString(ext.Expression, msg.Properties, "property")
	case "message_id":
		return msg.ID, nil
	case "correlation_id":
		return msg.CorrelationID, nil
	default:
		return "", fmt.Errorf("unsupported JMS extraction source: %s", ext.Source)
	}
}

func evaluateAssertions(assertions []protocol.Assertion, result *protocol.Result, msg message) error {
	for _, assertion := range assertions {
		switch assertion.Type {
		case "message_received":
			if msg.ID == "" {
				return fmt.Errorf("expected a JMS message")
			}
		case "body_contains":
			if !strings.Contains(string(msg.Body), assertion.Value) {
				return fmt.Errorf("JMS message body does not contain %q", assertion.Value)
			}
		case "property_equals":
			if msg.Properties[assertion.Value] != fmt.Sprint(assertion.Expected) {
				return fmt.Errorf("JMS property %q = %q, want %q", assertion.Value, msg.Properties[assertion.Value], assertion.Expected)
			}
		case "header_equals":
			if msg.Headers[assertion.Value] != fmt.Sprint(assertion.Expected) {
				return fmt.Errorf("JMS header %q = %q, want %q", assertion.Value, msg.Headers[assertion.Value], assertion.Expected)
			}
		case "correlation_match":
			if msg.CorrelationID != assertion.Value {
				return fmt.Errorf("JMS correlation_id = %q, want %q", msg.CorrelationID, assertion.Value)
			}
		case "response_time_ms":
			if assertion.Max > 0 && result.Duration.Milliseconds() > int64(assertion.Max) {
				return fmt.Errorf("JMS response time %dms exceeded max %dms", result.Duration.Milliseconds(), assertion.Max)
			}
		default:
			return fmt.Errorf("unsupported JMS assertion type: %s", assertion.Type)
		}
	}
	return nil
}

func extractJSONPath(expr string, body []byte) (string, error) {
	expr = strings.TrimSpace(expr)
	if expr == "$" || expr == "" {
		return string(body), nil
	}
	parts, err := parseJSONPathSegments(expr)
	if err != nil {
		return "", err
	}
	var current any
	if err := json.Unmarshal(body, &current); err != nil {
		return "", fmt.Errorf("invalid JSON body: %w", err)
	}
	for _, part := range parts {
		if idx, ok := parseArrayIndex(part); ok {
			arr, ok := current.([]any)
			if !ok {
				return "", fmt.Errorf("expected JSON array at index %d", idx)
			}
			if idx < 0 || idx >= len(arr) {
				return "", fmt.Errorf("JSON array index %d out of range", idx)
			}
			current = arr[idx]
			continue
		}
		obj, ok := current.(map[string]any)
		if !ok {
			return "", fmt.Errorf("expected JSON object at %q", part)
		}
		current, ok = obj[part]
		if !ok {
			return "", fmt.Errorf("JSON field %q not found", part)
		}
	}
	return fmt.Sprint(current), nil
}

func parseJSONPathSegments(expr string) ([]string, error) {
	expr = strings.TrimSpace(expr)
	expr = strings.TrimPrefix(expr, "$")
	expr = strings.TrimPrefix(expr, ".")
	if expr == "" {
		return nil, nil
	}
	var parts []string
	for len(expr) > 0 {
		if expr[0] == '.' {
			expr = expr[1:]
			continue
		}
		if expr[0] == '[' {
			end := strings.IndexByte(expr, ']')
			if end < 0 {
				return nil, fmt.Errorf("invalid JSONPath %q", expr)
			}
			token := expr[1:end]
			if len(token) >= 2 && ((token[0] == '"' && token[len(token)-1] == '"') || (token[0] == '\'' && token[len(token)-1] == '\'')) {
				var decoded string
				if token[0] == '"' {
					if err := json.Unmarshal([]byte(token), &decoded); err != nil {
						return nil, err
					}
				} else {
					decoded = strings.ReplaceAll(token[1:len(token)-1], `\'`, `'`)
				}
				parts = append(parts, decoded)
			} else {
				parts = append(parts, token)
			}
			expr = expr[end+1:]
			continue
		}
		nextDot := strings.IndexAny(expr, ".[")
		if nextDot < 0 {
			parts = append(parts, expr)
			break
		}
		parts = append(parts, expr[:nextDot])
		expr = expr[nextDot:]
	}
	return parts, nil
}

func parseArrayIndex(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	idx, err := strconv.Atoi(value)
	return idx, err == nil
}
