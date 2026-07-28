package devweb

// util.go — split from converter.go (same package, behavior-preserving).

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
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
	if value, ok := decodeDevWebJSStringLiteral(raw); ok {
		return value
	}
	return strings.TrimSpace(raw)
}

// decodeDevWebJSStringLiteral decodes only a complete JavaScript string or
// template literal. It never evaluates expressions. Rejecting an unsupported
// escape lets the HTTP adapter produce a blocked review draft instead of
// silently changing the bytes that the source runtime would send.
func decodeDevWebJSStringLiteral(raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	if len(value) < 2 {
		return "", false
	}
	quote := value[0]
	if (quote != '\'' && quote != '"' && quote != '`') || value[len(value)-1] != quote {
		return "", false
	}
	inner := value[1 : len(value)-1]
	if !utf8.ValidString(inner) {
		return "", false
	}
	var builder strings.Builder
	for index := 0; index < len(inner); {
		ch := inner[index]
		if ch == quote {
			// An unescaped matching quote terminates the literal; because this
			// is not the final delimiter, the expression is not one literal.
			return "", false
		}
		if ch != '\\' {
			if quote != '`' && (ch == '\n' || ch == '\r' || ch < 0x20) {
				return "", false
			}
			r, size := utf8.DecodeRuneInString(inner[index:])
			if r == utf8.RuneError && size == 1 {
				return "", false
			}
			builder.WriteRune(r)
			index += size
			continue
		}
		index++
		if index >= len(inner) {
			return "", false
		}
		escaped := inner[index]
		index++
		switch escaped {
		case '\n':
			continue
		case '\r':
			if index < len(inner) && inner[index] == '\n' {
				index++
			}
			continue
		case '\'', '"', '\\', '/', '`':
			builder.WriteByte(escaped)
		case 'b':
			builder.WriteByte('\b')
		case 'f':
			builder.WriteByte('\f')
		case 'n':
			builder.WriteByte('\n')
		case 'r':
			builder.WriteByte('\r')
		case 't':
			builder.WriteByte('\t')
		case 'v':
			builder.WriteByte('\v')
		case '0':
			if index < len(inner) && inner[index] >= '0' && inner[index] <= '9' {
				return "", false
			}
			builder.WriteByte(0)
		case 'x':
			r, next, ok := decodeDevWebHexEscape(inner, index, 2)
			if !ok {
				return "", false
			}
			builder.WriteRune(r)
			index = next
		case 'u':
			r, next, ok := decodeDevWebUnicodeEscape(inner, index)
			if !ok {
				return "", false
			}
			index = next
			if utf16.IsSurrogate(r) {
				if r < 0xD800 || r > 0xDBFF ||
					index+2 > len(inner) ||
					inner[index] != '\\' ||
					inner[index+1] != 'u' {
					return "", false
				}
				low, lowNext, lowOK := decodeDevWebUnicodeEscape(inner, index+2)
				if !lowOK || low < 0xDC00 || low > 0xDFFF {
					return "", false
				}
				r = utf16.DecodeRune(r, low)
				index = lowNext
			}
			if !utf8.ValidRune(r) {
				return "", false
			}
			builder.WriteRune(r)
		default:
			return "", false
		}
	}
	return builder.String(), true
}

func decodeDevWebHexEscape(value string, start int, digits int) (rune, int, bool) {
	end := start + digits
	if start < 0 || digits <= 0 || end > len(value) {
		return 0, start, false
	}
	parsed, err := strconv.ParseUint(value[start:end], 16, 32)
	if err != nil {
		return 0, start, false
	}
	return rune(parsed), end, true
}

func decodeDevWebUnicodeEscape(value string, start int) (rune, int, bool) {
	if start < len(value) && value[start] == '{' {
		endOffset := strings.IndexByte(value[start+1:], '}')
		if endOffset < 1 {
			return 0, start, false
		}
		end := start + 1 + endOffset
		digits := value[start+1 : end]
		if len(digits) > 6 {
			return 0, start, false
		}
		parsed, err := strconv.ParseUint(digits, 16, 32)
		r := rune(parsed)
		if err != nil || !utf8.ValidRune(r) || utf16.IsSurrogate(r) {
			return 0, start, false
		}
		return r, end + 1, true
	}
	return decodeDevWebHexEscape(value, start, 4)
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

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
