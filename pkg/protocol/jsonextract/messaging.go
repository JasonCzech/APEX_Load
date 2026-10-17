package jsonextract

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// MessagingJSONPath is the historical JMS/Kafka path dialect: dotted and
// quoted bracket keys, and numeric segments interpreted against their current
// container. Root/empty expressions retain the original body and composite
// values retain fmt.Sprint formatting. Keep this distinct from JSONPath-lite.
func MessagingJSONPath(expr string, body []byte) (string, error) {
	expr = strings.TrimSpace(expr)
	if expr == "$" || expr == "" {
		return string(body), nil
	}
	parts, err := parseJSONPathSegments(expr)
	if err != nil {
		return "", err
	}
	var current any
	if err := DecodeExact(body, &current); err != nil {
		return "", fmt.Errorf("invalid JSON body: %w", err)
	}
	for _, part := range parts {
		// Treat an all-digit segment as an array index ONLY when the current value
		// is actually an array. Against an object, an all-digit segment is a
		// (numeric-string) key — otherwise a key like "123" could never be reached.
		if arr, ok := current.([]any); ok {
			idx, ok := parseArrayIndex(part)
			if !ok {
				return "", fmt.Errorf("expected JSON array index, got %q", part)
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
			end := messagingBracketEnd(expr)
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

// A closing bracket inside a quoted key is data, including after escaped
// quotes. The token decoder below remains responsible for escape validation.
func messagingBracketEnd(expr string) int {
	var quote byte
	escaped := false
	for i := 1; i < len(expr); i++ {
		c := expr[i]
		if quote != 0 {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
		} else if c == ']' {
			return i
		}
	}
	return -1
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
