package devweb

// util.go — split from converter.go (same package, behavior-preserving).

import (
	"fmt"
	"regexp"
	"strings"
)

func mapValues(maps ...map[string]string) []string {
	values := make([]string, 0)
	for _, items := range maps {
		for _, value := range items {
			values = append(values, value)
		}
	}
	return values
}

func sortedMapKeys(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sortStrings(keys)
	return keys
}

func mapValue(values map[string]any, key string) map[string]any {
	if values == nil {
		return nil
	}
	if typed, ok := values[key].(map[string]any); ok {
		return typed
	}
	if typed, ok := values[key].(map[any]any); ok {
		out := map[string]any{}
		for k, v := range typed {
			if s, ok := k.(string); ok {
				out[s] = v
			}
		}
		return out
	}
	return nil
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		n, _ := fmt.Sscanf(strings.TrimSpace(typed), "%d", new(int))
		if n == 0 {
			return 0
		}
		var out int
		_, _ = fmt.Sscanf(strings.TrimSpace(typed), "%d", &out)
		return out
	default:
		return 0
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func boolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "on":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func sortStrings(values []string) {
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}

func splitArgs(raw string) []string {
	var args []string
	var current strings.Builder
	quote := rune(0)
	depth := 0
	for _, r := range raw {
		switch {
		case quote != 0:
			current.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"' || r == '`':
			quote = r
			current.WriteRune(r)
		case r == '{' || r == '[' || r == '(':
			depth++
			current.WriteRune(r)
		case r == '}' || r == ']' || r == ')':
			if depth > 0 {
				depth--
			}
			current.WriteRune(r)
		case r == ',' && depth == 0:
			args = append(args, strings.TrimSpace(current.String()))
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if strings.TrimSpace(current.String()) != "" {
		args = append(args, strings.TrimSpace(current.String()))
	}
	return args
}

func grpcTarget(args []string) string {
	if len(args) >= 2 {
		service := strings.Trim(jsString(args[0]), "/")
		method := strings.Trim(jsString(args[1]), "/")
		if strings.Contains(service, "/") && method == "" {
			return service
		}
		return service + "/" + method
	}
	return strings.Trim(jsString(args[0]), "/")
}

func jsString(raw string) string {
	value := strings.TrimSpace(raw)
	if len(value) >= 2 {
		first := value[0]
		last := value[len(value)-1]
		if (first == '"' || first == '\'' || first == '`') && first == last {
			value = value[1 : len(value)-1]
		}
	}
	return strings.ReplaceAll(value, `\"`, `"`)
}

func jsObjectLiteral(raw string) string {
	value := strings.TrimSpace(raw)
	if strings.HasPrefix(value, "{") {
		value = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*:`).ReplaceAllString(value, `"$1":`)
		value = strings.ReplaceAll(value, `'`, `"`)
		return value
	}
	return jsString(value)
}

func secondsToMilliseconds(raw string) float64 {
	var seconds float64
	fmt.Sscanf(raw, "%f", &seconds)
	return seconds * 1000
}

func actionName(prefix string, suffix string) string {
	return sanitizeName(prefix + "_" + suffix)
}

func sanitizeName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "devweb_action"
	}
	value = regexp.MustCompile(`[^A-Za-z0-9_]+`).ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return "devweb_action"
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
