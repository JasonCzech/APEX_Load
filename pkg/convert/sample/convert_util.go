package sample

// convert_util.go — split from converter.go (same package, behavior-preserving).

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
)

func normalizeFormat(format string) string {
	value := strings.ToLower(strings.TrimSpace(format))
	switch value {
	case "http", "raw", "raw_http", "raw-http":
		return "raw_http"
	case "curl", "c_url":
		return "curl"
	case "json", "rest_json", "rest-message", "rest_message":
		return "rest_json"
	case "har", "har_entry", "har-entry":
		return "har"
	default:
		return value
	}
}

func baseName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimRight(value, `/\`)
	if value == "" {
		return ""
	}
	if idx := strings.LastIndexAny(value, `/\`); idx >= 0 {
		return value[idx+1:]
	}
	return value
}

func pluralSuffix(count int, singular string, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

func ensureLeadingSlash(value string) string {
	if strings.HasPrefix(value, "/") {
		return value
	}
	return "/" + value
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	default:
		return ""
	}
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	case string:
		parsed, _ := strconv.Atoi(typed)
		return parsed
	default:
		return 0
	}
}

func floatValue(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		parsed, _ := typed.Float64()
		return parsed
	case string:
		parsed, _ := strconv.ParseFloat(typed, 64)
		return parsed
	default:
		return 0
	}
}

func bodyValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case nil:
		return ""
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(data)
	}
}

func mapValueToStringMap(value any) map[string]string {
	if value == nil {
		return nil
	}
	mapped, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]string, len(mapped))
	for key, item := range mapped {
		result[key] = bodyValue(item)
	}
	return result
}

func headerListToMap(value any, diagnostics *diag.Collector, location string) map[string]string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make(map[string]string, len(items))
	canonical := map[string]string{}
	duplicateSetCookie := false
	for _, item := range items {
		mapped, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := stringValue(mapped["name"])
		if name == "" {
			continue
		}
		value := stringValue(mapped["value"])
		normalized := strings.ToLower(name)
		existingKey := canonical[normalized]
		if existingKey == "" {
			canonical[normalized] = name
			result[name] = value
			continue
		}
		if strings.EqualFold(existingKey, "Set-Cookie") {
			duplicateSetCookie = true
		}
		if result[existingKey] == "" {
			result[existingKey] = value
		} else if value != "" {
			result[existingKey] += ", " + value
		}
	}
	if duplicateSetCookie {
		diagnostics.Add(diag.Info, "duplicate Set-Cookie headers", location, "multiple Set-Cookie headers were folded into one comma-joined header value; validate cookie replay behavior")
	}
	return result
}
