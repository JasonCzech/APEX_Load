package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/jasonczech/stormrunner/pkg/protocol"
)

func extract(ext protocol.Extractor, headers http.Header, body []byte) (string, error) {
	switch ext.Source {
	case "body":
		return extractFromBody(ext, body)
	case "header":
		v := headers.Get(ext.Expression)
		if v == "" {
			return "", fmt.Errorf("header %q not found", ext.Expression)
		}
		return v, nil
	default:
		return "", fmt.Errorf("unsupported extraction source: %s", ext.Source)
	}
}

func extractFromBody(ext protocol.Extractor, body []byte) (string, error) {
	switch ext.Type {
	case "jsonpath":
		return extractJSONPath(ext.Expression, body)
	case "regex":
		return extractRegex(ext.Expression, body)
	case "boundary":
		return extractBoundary(ext.Expression, body)
	default:
		return "", fmt.Errorf("unsupported extraction type: %s", ext.Type)
	}
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
