package devweb

// webrequest.go — split from converter.go (same package, behavior-preserving).

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func parseWebRequest(blockName string, objectLiteral string, ctx convertContext) (dsl.Action, bool) {
	rawURL := objectStringField(objectLiteral, "url", ctx)
	if rawURL == "" {
		rawURL = objectStringField(objectLiteral, "URL", ctx)
	}
	if rawURL == "" {
		return dsl.Action{}, false
	}
	method := strings.ToUpper(convertutil.FirstNonEmpty(objectStringField(objectLiteral, "method", ctx), "GET"))
	body := objectStringField(objectLiteral, "body", ctx)
	if body == "" {
		body = objectStringField(objectLiteral, "bodyText", ctx)
	}
	headers := objectStringMapField(objectLiteral, "headers", ctx)
	query := objectStringMapField(objectLiteral, "queryString", ctx)
	if containsTemplateExpression(rawURL) || containsTemplateExpression(method) || containsTemplateExpression(body) || stringMapContainsTemplateExpression(headers) || stringMapContainsTemplateExpression(query) {
		return dsl.Action{}, false
	}
	if len(query) > 0 {
		rawURL = appendQueryString(rawURL, query)
	}
	action := dsl.Action{
		Name:    actionName(blockName, method),
		Type:    "request",
		Method:  method,
		Target:  rawURL,
		Headers: headers,
		Body:    body,
	}
	if len(query) > 0 {
		action.Metadata = map[string]any{"devweb_query_string": query}
	}
	action.Extractors = parseDevWebExtractors(objectLiteral)
	return action, true
}

func containsTemplateExpression(value string) bool {
	return strings.Contains(value, "${")
}

func stringMapContainsTemplateExpression(values map[string]string) bool {
	for _, value := range values {
		if containsTemplateExpression(value) {
			return true
		}
	}
	return false
}

func objectStringField(objectLiteral string, field string, ctx convertContext) string {
	raw, ok := objectFieldRaw(objectLiteral, field)
	if !ok {
		return ""
	}
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "{") {
		return jsObjectToJSON(raw, ctx.parameterTemplates)
	}
	return jsStringValue(raw, ctx.parameterTemplates)
}

func objectStringMapField(objectLiteral string, field string, ctx convertContext) map[string]string {
	raw, ok := objectFieldRaw(objectLiteral, field)
	if !ok {
		return nil
	}
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "{") || !strings.HasSuffix(raw, "}") {
		return nil
	}
	entries := objectLiteralEntries(strings.TrimSpace(raw[1 : len(raw)-1]))
	values := make(map[string]string, len(entries))
	for key, rawValue := range entries {
		values[key] = jsStringValue(rawValue, ctx.parameterTemplates)
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

func objectFieldRaw(objectLiteral string, field string) (string, bool) {
	pattern := regexp.MustCompile(`(?s)(^|[,{\s])` + regexp.QuoteMeta(field) + `\s*:`)
	match := pattern.FindStringSubmatchIndex(objectLiteral)
	if len(match) == 0 {
		return "", false
	}
	start := match[1]
	if start < 0 || start > len(objectLiteral) {
		return "", false
	}
	end := findTopLevelFieldEnd(objectLiteral, start)
	return strings.TrimSpace(objectLiteral[start:end]), true
}

func findTopLevelFieldEnd(value string, start int) int {
	quote := rune(0)
	escaped := false
	depth := 0
	for i, r := range value[start:] {
		pos := start + i
		if escaped {
			escaped = false
			continue
		}
		if quote != 0 {
			if r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"', '`':
			quote = r
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			if depth == 0 {
				return pos
			}
			depth--
		case ',':
			if depth == 0 {
				return pos
			}
		}
	}
	return len(value)
}

func objectLiteralEntries(inner string) map[string]string {
	entries := map[string]string{}
	for _, part := range splitTopLevel(inner, ',') {
		key, value, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		cleanKey := strings.Trim(strings.TrimSpace(key), `"'`+"`")
		if cleanKey == "" {
			continue
		}
		entries[cleanKey] = strings.TrimSpace(value)
	}
	return entries
}

func splitTopLevel(value string, delimiter rune) []string {
	var out []string
	var current strings.Builder
	quote := rune(0)
	escaped := false
	depth := 0
	for _, r := range value {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if quote != 0 {
			current.WriteRune(r)
			if r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"', '`':
			quote = r
			current.WriteRune(r)
		case '{', '[', '(':
			depth++
			current.WriteRune(r)
		case '}', ']', ')':
			if depth > 0 {
				depth--
			}
			current.WriteRune(r)
		default:
			if r == delimiter && depth == 0 {
				if strings.TrimSpace(current.String()) != "" {
					out = append(out, strings.TrimSpace(current.String()))
				}
				current.Reset()
				continue
			}
			current.WriteRune(r)
		}
	}
	if strings.TrimSpace(current.String()) != "" {
		out = append(out, strings.TrimSpace(current.String()))
	}
	return out
}

func jsStringValue(raw string, params map[string]string) string {
	value := jsString(raw)
	return convertDevWebTemplate(value, params)
}

func jsObjectToJSON(raw string, params map[string]string) string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "{") || !strings.HasSuffix(raw, "}") {
		return jsStringValue(raw, params)
	}
	entries := objectLiteralEntries(strings.TrimSpace(raw[1 : len(raw)-1]))
	if len(entries) == 0 {
		return "{}"
	}
	values := map[string]string{}
	for key, rawValue := range entries {
		values[key] = jsStringValue(rawValue, params)
	}
	data, err := json.Marshal(values)
	if err != nil {
		return raw
	}
	return string(data)
}

func appendQueryString(rawURL string, query map[string]string) string {
	if len(query) == 0 {
		return rawURL
	}
	separator := "?"
	if strings.Contains(rawURL, "?") {
		separator = "&"
	}
	parts := make([]string, 0, len(query))
	for key, value := range query {
		parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(value))
	}
	sortStrings(parts)
	return rawURL + separator + strings.Join(parts, "&")
}

func parseDevWebExtractors(objectLiteral string) []dsl.Extractor {
	var extractors []dsl.Extractor
	for _, match := range regexp.MustCompile("new\\s+load\\.BoundaryExtractor\\s*\\(\\s*[\"'`]([^\"'`]+)[\"'`]\\s*,\\s*[\"'`]([^\"'`]+)[\"'`]\\s*,\\s*[\"'`]([^\"'`]+)[\"'`]\\s*\\)").FindAllStringSubmatch(objectLiteral, -1) {
		extractors = append(extractors, dsl.Extractor{
			Name:       match[1],
			Source:     "body",
			Type:       "boundary",
			Expression: match[2] + "|||" + match[3],
		})
	}
	for _, match := range regexp.MustCompile("new\\s+load\\.JsonPathExtractor\\s*\\(\\s*[\"'`]([^\"'`]+)[\"'`]\\s*,\\s*[\"'`]([^\"'`]+)[\"'`]").FindAllStringSubmatch(objectLiteral, -1) {
		extractors = append(extractors, dsl.Extractor{
			Name:       match[1],
			Source:     "body",
			Type:       "jsonpath",
			Expression: match[2],
		})
	}
	if len(extractors) == 0 {
		return nil
	}
	return extractors
}

func convertDevWebTemplate(value string, params map[string]string) string {
	return regexp.MustCompile(`\$\{\s*([^}]+?)\s*\}`).ReplaceAllStringFunc(value, func(match string) string {
		expr := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(match, "${"), "}"))
		switch {
		case strings.HasPrefix(expr, "load.params."):
			name := strings.TrimSpace(strings.TrimPrefix(expr, "load.params."))
			if params != nil && params[name] != "" {
				return params[name]
			}
			return "{{var." + name + "}}"
		case strings.Contains(expr, ".extractors."):
			name := expr[strings.LastIndex(expr, ".extractors.")+len(".extractors."):]
			return "{{var." + strings.TrimSpace(name) + "}}"
		case params != nil && params[expr] != "":
			return params[expr]
		default:
			return match
		}
	})
}

func isSafeDevWebTemplateLiteral(line string, params map[string]string) bool {
	if !strings.Contains(line, "${") {
		return true
	}
	unsafe := false
	regexp.MustCompile(`\$\{\s*([^}]+?)\s*\}`).ReplaceAllStringFunc(line, func(match string) string {
		expr := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(match, "${"), "}"))
		if strings.HasPrefix(expr, "load.params.") || strings.Contains(expr, ".extractors.") {
			return match
		}
		unsafe = true
		return match
	})
	return !unsafe
}
