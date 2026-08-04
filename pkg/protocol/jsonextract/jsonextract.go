// Package jsonextract provides the value-extraction engine shared by the network
// and database protocols (and available to any protocol that extracts values from
// a response body). It supports JSONPath-lite, JMESPath, regex, and boundary
// extraction. These functions previously existed as byte-identical per-protocol
// copies (extractNetwork*/extractDatabase*).
package jsonextract

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jmespath/go-jmespath"
)

// JSONPath evaluates a JSONPath-lite expression (dotted keys with optional
// [n] indexing, leading "$"/"$." stripped) against a JSON body.
func JSONPath(expr string, body []byte) (string, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return "", fmt.Errorf("jsonpath expression is required")
	}
	expr = strings.TrimPrefix(expr, "$.")
	expr = strings.TrimPrefix(expr, "$")
	var current any
	if err := json.Unmarshal(body, &current); err != nil {
		return "", err
	}
	if expr == "" {
		return Stringify(current), nil
	}
	for _, part := range strings.Split(expr, ".") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, indexes, err := splitPathToken(part)
		if err != nil {
			return "", fmt.Errorf("invalid jsonpath %q: %w", expr, err)
		}
		if key != "" {
			object, ok := current.(map[string]any)
			if !ok {
				return "", fmt.Errorf("jsonpath %q expected object", expr)
			}
			next, ok := object[key]
			if !ok {
				return "", fmt.Errorf("jsonpath %q not found", expr)
			}
			current = next
		}
		for _, index := range indexes {
			items, ok := current.([]any)
			if !ok || index < 0 || index >= len(items) {
				return "", fmt.Errorf("jsonpath %q index out of range", expr)
			}
			current = items[index]
		}
	}
	return Stringify(current), nil
}

func splitPathToken(token string) (string, []int, error) {
	key := token
	indexes := []int{}
	if bracket := strings.Index(token, "["); bracket >= 0 {
		key = token[:bracket]
		rest := token[bracket:]
		for rest != "" {
			if !strings.HasPrefix(rest, "[") {
				return "", nil, fmt.Errorf("unexpected suffix %q", rest)
			}
			end := strings.Index(rest, "]")
			if end <= 1 {
				return "", nil, fmt.Errorf("malformed array index")
			}
			indexText := rest[1:end]
			index, err := strconv.Atoi(indexText)
			if err != nil || index < 0 {
				return "", nil, fmt.Errorf("malformed array index %q", indexText)
			}
			indexes = append(indexes, index)
			rest = rest[end+1:]
		}
	}
	return key, indexes, nil
}

// JMESPath evaluates a JMESPath expression against a JSON body.
func JMESPath(expr string, body []byte) (string, error) {
	var current any
	if err := json.Unmarshal(body, &current); err != nil {
		return "", err
	}
	value, err := jmespath.Search(expr, current)
	if err != nil {
		return "", err
	}
	return Stringify(value), nil
}

// Regex returns the first submatch (group 1 if present, else the whole match) of
// expr against body.
func Regex(expr string, body []byte) (string, error) {
	re, err := regexp.Compile(expr)
	if err != nil {
		return "", err
	}
	match := re.FindSubmatch(body)
	if len(match) == 0 {
		return "", fmt.Errorf("regex %q not found", expr)
	}
	if len(match) > 1 {
		return string(match[1]), nil
	}
	return string(match[0]), nil
}

// Boundary extracts the text between a left and right boundary, expressed as
// "left|||right".
func Boundary(expr string, body []byte) (string, error) {
	parts := strings.Split(expr, "|||")
	if len(parts) != 2 {
		return "", fmt.Errorf("boundary expression must be left|||right")
	}
	text := string(body)
	start := strings.Index(text, parts[0])
	if start < 0 {
		return "", fmt.Errorf("left boundary not found")
	}
	start += len(parts[0])
	end := strings.Index(text[start:], parts[1])
	if end < 0 {
		return "", fmt.Errorf("right boundary not found")
	}
	return text[start : start+end], nil
}

// Stringify renders an extracted JSON value as a string: "" for nil, the string
// itself for strings, fmt.Sprint for scalars, and compact JSON for anything else.
func Stringify(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case float64, bool, int, int64:
		return fmt.Sprint(typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(data)
	}
}
