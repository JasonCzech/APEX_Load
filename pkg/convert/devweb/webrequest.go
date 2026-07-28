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

type devWebStringEntry struct {
	Key   string
	Value string
}

func parseWebRequest(blockName string, objectLiteral string, ctx convertContext) (dsl.Action, bool) {
	rawURL, urlPresent, urlOK := objectStaticStringField(objectLiteral, "url", ctx)
	if !urlPresent {
		rawURL, urlPresent, urlOK = objectStaticStringField(objectLiteral, "URL", ctx)
	}
	if !urlPresent || !urlOK || rawURL == "" {
		return dsl.Action{}, false
	}
	methodValue, methodPresent, methodOK := objectStaticStringField(objectLiteral, "method", ctx)
	if methodPresent && !methodOK {
		return dsl.Action{}, false
	}
	method := strings.ToUpper(convertutil.FirstNonEmpty(methodValue, "GET"))

	body, bodyPresent, bodyOK := objectStaticBodyField(objectLiteral, "body", ctx)
	if !bodyPresent {
		body, bodyPresent, bodyOK = objectStaticBodyField(objectLiteral, "bodyText", ctx)
	}
	if bodyPresent && !bodyOK {
		return dsl.Action{}, false
	}

	headerEntries, headersPresent, headersOK := objectStringEntriesField(objectLiteral, "headers", ctx)
	if headersPresent && !headersOK {
		return dsl.Action{}, false
	}
	queryEntries, queryPresent, queryOK := objectStringEntriesField(objectLiteral, "queryString", ctx)
	if queryPresent && !queryOK {
		return dsl.Action{}, false
	}
	headers := devWebStringEntriesMap(headerEntries)
	query := devWebStringEntriesMap(queryEntries)
	if containsTemplateExpression(rawURL) || containsTemplateExpression(method) || containsTemplateExpression(body) || stringMapContainsTemplateExpression(headers) || stringMapContainsTemplateExpression(query) {
		return dsl.Action{}, false
	}
	if len(queryEntries) > 0 {
		rawURL = appendOrderedQueryString(rawURL, queryEntries)
	}
	action := dsl.Action{
		Name:    actionName(blockName, method),
		Type:    "request",
		Method:  method,
		Target:  rawURL,
		Headers: headers,
		Body:    body,
	}
	if len(queryEntries) > 0 {
		ordered := make([]map[string]string, 0, len(queryEntries))
		for _, entry := range queryEntries {
			ordered = append(ordered, map[string]string{"name": entry.Key, "value": entry.Value})
		}
		action.Metadata = map[string]any{
			"devweb_query_string":         query,
			"devweb_query_string_ordered": ordered,
		}
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
	value, present, ok := objectStaticStringField(objectLiteral, field, ctx)
	if !present || !ok {
		return ""
	}
	return value
}

func objectStringMapField(objectLiteral string, field string, ctx convertContext) map[string]string {
	entries, present, ok := objectStringEntriesField(objectLiteral, field, ctx)
	if !present || !ok {
		return nil
	}
	return devWebStringEntriesMap(entries)
}

func objectStaticStringField(objectLiteral string, field string, ctx convertContext) (string, bool, bool) {
	raw, ok := objectFieldRaw(objectLiteral, field)
	if !ok {
		return "", false, true
	}
	raw = strings.TrimSpace(raw)
	value, ok := devWebStaticStringValue(raw, ctx.parameterTemplates)
	return value, true, ok
}

func objectStaticBodyField(objectLiteral string, field string, ctx convertContext) (string, bool, bool) {
	raw, ok := objectFieldRaw(objectLiteral, field)
	if !ok {
		return "", false, true
	}
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "{") || strings.HasPrefix(raw, "[") {
		encoded, ok := encodeDevWebStaticJSONValue(raw, ctx.parameterTemplates)
		return encoded, true, ok
	}
	if value, ok := devWebStaticStringValue(raw, ctx.parameterTemplates); ok {
		return value, true, true
	}
	encoded, ok := encodeDevWebStaticJSONValue(raw, ctx.parameterTemplates)
	return encoded, true, ok
}

func objectStringEntriesField(objectLiteral string, field string, ctx convertContext) ([]devWebStringEntry, bool, bool) {
	raw, ok := objectFieldRaw(objectLiteral, field)
	if !ok {
		return nil, false, true
	}
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "{") || !strings.HasSuffix(raw, "}") {
		return nil, true, false
	}
	rawEntries, ok := devWebObjectEntries(strings.TrimSpace(raw[1 : len(raw)-1]))
	if !ok {
		return nil, true, false
	}
	entries := make([]devWebStringEntry, 0, len(rawEntries))
	indexByKey := make(map[string]int, len(rawEntries))
	for _, entry := range rawEntries {
		value, ok := devWebStaticStringValue(entry.Value, ctx.parameterTemplates)
		if !ok {
			return nil, true, false
		}
		if index, duplicate := indexByKey[entry.Key]; duplicate {
			// JavaScript uses the last value while retaining the property's
			// original enumeration position.
			entries[index].Value = value
			continue
		}
		indexByKey[entry.Key] = len(entries)
		entries = append(entries, devWebStringEntry{Key: entry.Key, Value: value})
	}
	return entries, true, true
}

func devWebStringEntriesMap(entries []devWebStringEntry) map[string]string {
	if len(entries) == 0 {
		return nil
	}
	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		values[entry.Key] = entry.Value
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
	ordered, _ := devWebObjectEntries(inner)
	for _, entry := range ordered {
		entries[entry.Key] = entry.Value
	}
	return entries
}

func devWebObjectEntries(inner string) ([]devWebStringEntry, bool) {
	if strings.TrimSpace(inner) == "" {
		return nil, true
	}
	entries := make([]devWebStringEntry, 0)
	for _, part := range splitTopLevel(inner, ',') {
		keyExpression, value, ok := splitDevWebObjectProperty(part)
		if !ok {
			return nil, false
		}
		keyExpression = strings.TrimSpace(keyExpression)
		key := ""
		if decoded, literal := decodeDevWebJSStringLiteral(keyExpression); literal {
			key = decoded
		} else if regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`).MatchString(keyExpression) ||
			regexp.MustCompile(`^(?:0|[1-9][0-9]*)$`).MatchString(keyExpression) {
			key = keyExpression
		} else {
			return nil, false
		}
		if key == "" {
			return nil, false
		}
		entries = append(entries, devWebStringEntry{Key: key, Value: strings.TrimSpace(value)})
	}
	return entries, true
}

func splitDevWebObjectProperty(property string) (string, string, bool) {
	quote := rune(0)
	escaped := false
	depth := 0
	for index, r := range property {
		if escaped {
			escaped = false
			continue
		}
		if quote != 0 {
			if r == '\\' {
				escaped = true
			} else if r == quote {
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
				return "", "", false
			}
			depth--
		case ':':
			if depth == 0 {
				key := strings.TrimSpace(property[:index])
				value := strings.TrimSpace(property[index+1:])
				return key, value, key != "" && value != ""
			}
		}
	}
	return "", "", false
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
	value, ok := devWebStaticStringValue(raw, params)
	if !ok {
		return strings.TrimSpace(raw)
	}
	return value
}

func jsObjectToJSON(raw string, params map[string]string) string {
	encoded, ok := encodeDevWebStaticJSONValue(raw, params)
	if !ok {
		return strings.TrimSpace(raw)
	}
	return encoded
}

func appendQueryString(rawURL string, query map[string]string) string {
	if len(query) == 0 {
		return rawURL
	}
	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	sortStrings(keys)
	entries := make([]devWebStringEntry, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, devWebStringEntry{Key: key, Value: query[key]})
	}
	return appendOrderedQueryString(rawURL, entries)
}

func appendOrderedQueryString(rawURL string, query []devWebStringEntry) string {
	if len(query) == 0 {
		return rawURL
	}
	base, fragment, hasFragment := strings.Cut(rawURL, "#")
	separator := "?"
	if strings.Contains(base, "?") {
		separator = "&"
	}
	parts := make([]string, 0, len(query))
	for _, entry := range query {
		parts = append(parts,
			encodeDevWebQueryComponent(entry.Key)+"="+
				encodeDevWebQueryComponent(entry.Value),
		)
	}
	result := base + separator + strings.Join(parts, "&")
	if hasFragment {
		result += "#" + fragment
	}
	return result
}

func encodeDevWebQueryComponent(value string) string {
	var builder strings.Builder
	for len(value) > 0 {
		open := strings.Index(value, "{{")
		if open < 0 {
			builder.WriteString(url.QueryEscape(value))
			break
		}
		builder.WriteString(url.QueryEscape(value[:open]))
		closeOffset := strings.Index(value[open+2:], "}}")
		if closeOffset < 0 {
			builder.WriteString(url.QueryEscape(value[open:]))
			break
		}
		end := open + 2 + closeOffset + 2
		builder.WriteString(value[open:end])
		value = value[end:]
	}
	return builder.String()
}

func devWebStaticStringValue(raw string, params map[string]string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if decoded, ok := decodeDevWebJSStringLiteral(raw); ok {
		value := convertDevWebTemplate(decoded, params)
		return value, !containsTemplateExpression(value)
	}
	if strings.HasPrefix(raw, "load.params.") {
		name := strings.TrimSpace(strings.TrimPrefix(raw, "load.params."))
		if name == "" {
			return "", false
		}
		if params != nil && params[name] != "" {
			return params[name], true
		}
		return "{{var." + name + "}}", true
	}
	if strings.Contains(raw, ".extractors.") &&
		regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*\.extractors\.[A-Za-z_$][A-Za-z0-9_$]*$`).MatchString(raw) {
		name := raw[strings.LastIndex(raw, ".extractors.")+len(".extractors."):]
		return "{{var." + name + "}}", true
	}
	if params != nil {
		if value, ok := params[raw]; ok && value != "" {
			return value, true
		}
	}
	return "", false
}

func encodeDevWebStaticJSONValue(raw string, params map[string]string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if value, ok := devWebStaticStringValue(raw, params); ok {
		encoded, err := json.Marshal(value)
		return string(encoded), err == nil
	}
	if strings.HasPrefix(raw, "{") {
		if !strings.HasSuffix(raw, "}") {
			return "", false
		}
		entries, ok := devWebObjectEntries(strings.TrimSpace(raw[1 : len(raw)-1]))
		if !ok {
			return "", false
		}
		effective := make([]devWebStringEntry, 0, len(entries))
		indexByKey := make(map[string]int, len(entries))
		for _, entry := range entries {
			if index, duplicate := indexByKey[entry.Key]; duplicate {
				effective[index].Value = entry.Value
				continue
			}
			indexByKey[entry.Key] = len(effective)
			effective = append(effective, entry)
		}
		var builder strings.Builder
		builder.WriteByte('{')
		for index, entry := range effective {
			value, ok := encodeDevWebStaticJSONValue(entry.Value, params)
			if !ok {
				return "", false
			}
			key, err := json.Marshal(entry.Key)
			if err != nil {
				return "", false
			}
			if index > 0 {
				builder.WriteByte(',')
			}
			builder.Write(key)
			builder.WriteByte(':')
			builder.WriteString(value)
		}
		builder.WriteByte('}')
		return builder.String(), true
	}
	if strings.HasPrefix(raw, "[") {
		if !strings.HasSuffix(raw, "]") {
			return "", false
		}
		items := splitTopLevel(strings.TrimSpace(raw[1:len(raw)-1]), ',')
		var builder strings.Builder
		builder.WriteByte('[')
		for index, item := range items {
			value, ok := encodeDevWebStaticJSONValue(item, params)
			if !ok {
				return "", false
			}
			if index > 0 {
				builder.WriteByte(',')
			}
			builder.WriteString(value)
		}
		builder.WriteByte(']')
		return builder.String(), true
	}
	switch raw {
	case "true", "false", "null":
		return raw, true
	}
	if regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`).MatchString(raw) &&
		json.Valid([]byte(raw)) {
		return raw, true
	}
	return "", false
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
