package openapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
)

const openAPIFilePlaceholder = "[APEXLOAD_OPENAPI_FILE_PLACEHOLDER]"

var openAPIDataRefPattern = regexp.MustCompile(`\{\{\s*data\.openapi\.([a-zA-Z0-9_.-]+)\s*\}\}`)
var openAPIFileRefPattern = regexp.MustCompile(`\{\{\s*file\.([a-zA-Z0-9_.-]+)\s*\}\}`)
var rawJSONTemplatePattern = regexp.MustCompile(`"__APEX_RAW_TEMPLATE__([a-zA-Z0-9_.-]+)__"`)

func rawJSONTemplateMarker(column string) string {
	return "__APEX_RAW_TEMPLATE__" + sanitizeColumnName(column) + "__"
}

func expandRawJSONTemplates(value string) string {
	return rawJSONTemplatePattern.ReplaceAllString(value, "{{data.openapi.$1}}")
}

func successResponseSchema(responses map[string]any, responseKey string, res resolver, location string) (*schema, *responseObject) {
	if len(responses) == 0 || responseKey == "" {
		return nil, nil
	}
	raw, ok := responses[responseKey]
	if !ok || raw == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, nil
	}
	var response responseObject
	if err := json.Unmarshal(encoded, &response); err != nil {
		return nil, nil
	}
	responseSchema := response.Schema
	if responseSchema == nil {
		contentTypes := sortedMapKeys(response.Content)
		preferred := preferredContentType(contentTypes)
		if !strings.Contains(strings.ToLower(preferred), "json") {
			return nil, &response
		}
		media := response.Content[preferred]
		responseSchema = &media.Schema
	}
	if responseSchema == nil {
		return nil, &response
	}
	resolved := res.resolveSchema(*responseSchema, location+" response", nil)
	return &resolved, &response
}

func responseDeclaresLocation(status int, response *responseObject) bool {
	if status == httpStatusCreated {
		return true
	}
	if response == nil {
		return false
	}
	for name := range response.Headers {
		if strings.EqualFold(name, "Location") {
			return true
		}
	}
	return false
}

const httpStatusCreated = 201

func applyResponseExtractors(script *dsl.Script, built []builtOperation, res resolver) int {
	if script == nil {
		return 0
	}
	total := 0
	used := extractorNameSet(script)
	for i := range built {
		op := &built[i]
		if op.Index < 0 || op.Index >= len(script.Actions) {
			continue
		}
		action := &script.Actions[op.Index]
		resource := resourceOfPath(op.RawPath)
		if op.SuccessSchema != nil {
			for _, candidate := range extractorCandidatesForSchema(*op.SuccessSchema, resource) {
				if used[candidate.VarName] {
					candidate.VarName = uniqueExtractorName(candidate.VarName, used)
				}
				used[candidate.VarName] = true
				action.Extractors = append(action.Extractors, dsl.Extractor{
					Name:       candidate.VarName,
					Source:     "body",
					Type:       "jsonpath",
					Expression: candidate.Expression,
					OnMiss:     "empty",
				})
				candidate.ActionIndex = op.Index
				candidate.Source = "body"
				candidate.ExtractType = "jsonpath"
				op.Produced = append(op.Produced, candidate)
				total++
				res.add(diag.Info, "OpenAPI response extractor", strings.ToUpper(op.Method)+" "+op.RawPath, "generated extractor "+candidate.VarName+" from "+candidate.Expression)
				if len(action.Extractors) >= 3 {
					break
				}
			}
		}
		if op.LocationHeader {
			name := uniqueExtractorName(sanitizeColumnName(resource+"_location"), used)
			used[name] = true
			action.Extractors = append(action.Extractors, dsl.Extractor{
				Name:       name,
				Source:     "header",
				Type:       "header",
				Expression: "Location",
				OnMiss:     "empty",
			})
			op.Produced = append(op.Produced, producedValue{
				VarName: name, Resource: resource, FieldName: "Location", Role: "url",
				Source: "header", Expression: "Location", ExtractType: "header", ActionIndex: op.Index,
			})
			total++
		}
	}
	return total
}

func extractorCandidatesForSchema(s schema, resource string) []producedValue {
	if len(s.Properties) == 0 || strings.EqualFold(s.Type, "array") {
		return nil
	}
	var out []producedValue
	add := func(field string, prop schema, expression string) {
		role, ok := identityRole(field, prop)
		if !ok {
			return
		}
		out = append(out, producedValue{
			VarName:    varNameForProduced(resource, field),
			Resource:   resource,
			FieldName:  field,
			Role:       role,
			Expression: expression,
		})
	}
	for _, name := range sortedSchemaPropertyKeys(s.Properties) {
		prop := s.Properties[name]
		add(name, prop, "$."+name)
		if len(out) >= 3 {
			return out
		}
	}
	for _, name := range sortedSchemaPropertyKeys(s.Properties) {
		prop := s.Properties[name]
		if len(prop.Properties) == 0 {
			continue
		}
		for _, nested := range sortedSchemaPropertyKeys(prop.Properties) {
			add(name+"_"+nested, prop.Properties[nested], "$."+name+"."+nested)
			if len(out) >= 3 {
				return out
			}
		}
	}
	return out
}

func identityRole(field string, prop schema) (string, bool) {
	lower := strings.ToLower(field)
	switch {
	case lower == "id", strings.HasSuffix(lower, "id"), strings.HasSuffix(lower, "_id"):
		return "id", true
	case lower == "uuid", lower == "guid", strings.EqualFold(prop.Format, "uuid"):
		return "id", true
	case lower == "key", strings.HasSuffix(lower, "key"):
		return "key", true
	case lower == "href", lower == "self", lower == "url", strings.EqualFold(prop.Format, "uri"), strings.EqualFold(prop.Format, "url"):
		return "url", true
	default:
		return "", false
	}
}

func applyOperationChaining(script *dsl.Script, built []builtOperation, res resolver, columns columnRegistry) []Correlation {
	if script == nil {
		return nil
	}
	var correlations []Correlation
	for i := range built {
		consumer := &built[i]
		if consumer.Index < 0 || consumer.Index >= len(script.Actions) {
			continue
		}
		for _, param := range consumer.PathParams {
			if !strings.EqualFold(param.In, "path") || strings.TrimSpace(param.Name) == "" {
				continue
			}
			column := columns.destination(strings.TrimSpace(param.Name))
			resource := resourceForParam(consumer.RawPath, param.Name)
			match, confidence := bestProducerMatch(built[:i], resource, param.Name)
			if match == nil || confidence < 0.8 {
				if match != nil {
					res.add(diag.Info, "OpenAPI possible chain", strings.ToUpper(consumer.Method)+" "+consumer.RawPath, "possible "+param.Name+" correlation from "+match.VarName+" was left for review")
				}
				continue
			}
			from := "{{data.openapi." + column + "}}"
			to := "{{var." + match.VarName + "}}"
			action := &script.Actions[consumer.Index]
			if replaceActionTemplate(action, from, to) {
				correlations = appendCorrelationUsage(correlations, *match, match.ActionIndex+1, consumer.Index+1)
				res.add(diag.Info, "OpenAPI chained parameter", strings.ToUpper(consumer.Method)+" "+consumer.RawPath, "wired "+param.Name+" to {{var."+match.VarName+"}}")
			}
		}
	}
	return correlations
}

func bestProducerMatch(producers []builtOperation, resource string, paramName string) (*producedValue, float64) {
	var best *producedValue
	bestScore := 0.0
	paramNorm := normalizedIdentityToken(paramName, resource)
	for i := len(producers) - 1; i >= 0; i-- {
		for j := range producers[i].Produced {
			produced := &producers[i].Produced[j]
			score := 0.0
			if produced.Resource == resource && (produced.Role == "id" || produced.Role == "key") {
				score = 0.9
			}
			fieldNorm := normalizedIdentityToken(produced.FieldName, resource)
			if paramNorm != "" && fieldNorm != "" && paramNorm == fieldNorm {
				score = maxFloat(score, 0.85)
			}
			if score > bestScore {
				bestScore = score
				best = produced
			}
		}
	}
	return best, bestScore
}

func appendCorrelationUsage(correlations []Correlation, produced producedValue, foundStep int, usedStep int) []Correlation {
	for i := range correlations {
		if correlations[i].Name == produced.VarName && correlations[i].FoundInStep == foundStep {
			if !intSliceContains(correlations[i].UsedInSteps, usedStep) {
				correlations[i].UsedInSteps = append(correlations[i].UsedInSteps, usedStep)
				sort.Ints(correlations[i].UsedInSteps)
			}
			return correlations
		}
	}
	return append(correlations, Correlation{
		Name:        produced.VarName,
		Source:      produced.Source,
		Expression:  produced.Expression,
		ExtractType: produced.ExtractType,
		SampleValue: sampleColumnValue(produced.VarName),
		FoundInStep: foundStep,
		UsedInSteps: []int{usedStep},
	})
}

func replaceActionTemplate(action *dsl.Action, from string, to string) bool {
	changed := false
	replace := func(value *string) {
		next := strings.ReplaceAll(*value, from, to)
		if next != *value {
			changed = true
			*value = next
		}
	}
	replace(&action.Target)
	replace(&action.Body)
	replace(&action.Value)
	for key, value := range action.Headers {
		next := strings.ReplaceAll(value, from, to)
		if next != value {
			changed = true
			action.Headers[key] = next
		}
	}
	return changed
}

func openAPIDataColumnsFromScript(script *dsl.Script) []string {
	if script == nil {
		return nil
	}
	seen := map[string]struct{}{}
	scan := func(value string) {
		for _, match := range openAPIDataRefPattern.FindAllStringSubmatch(value, -1) {
			if len(match) > 1 {
				seen[sanitizeColumnName(match[1])] = struct{}{}
			}
		}
	}
	for _, action := range append(append([]dsl.Action{}, script.Init...), append(script.Actions, script.Teardown...)...) {
		scan(action.Target)
		scan(action.Body)
		scan(action.Value)
		for key, value := range action.Headers {
			scan(key)
			scan(value)
		}
	}
	return sortedMapKeys(seen)
}

func openAPIFileDataSources(script *dsl.Script) []dsl.DataSource {
	if script == nil {
		return nil
	}
	seen := map[string]struct{}{}
	scan := func(value string) {
		for _, match := range openAPIFileRefPattern.FindAllStringSubmatch(value, -1) {
			if len(match) > 1 {
				seen[sanitizeColumnName(match[1])] = struct{}{}
			}
		}
	}
	for _, action := range append(append([]dsl.Action{}, script.Init...), append(script.Actions, script.Teardown...)...) {
		scan(action.Target)
		scan(action.Body)
		scan(action.Value)
		for key, value := range action.Headers {
			scan(key)
			scan(value)
		}
	}
	names := sortedMapKeys(seen)
	out := make([]dsl.DataSource, 0, len(names))
	for _, name := range names {
		out = append(out, dsl.DataSource{
			Name:        name,
			Type:        "file",
			FileName:    name + ".bin",
			ContentType: "application/octet-stream",
			InlineData:  openAPIFilePlaceholder,
		})
	}
	return out
}

func openAPIDataSource(columns []string, registry columnRegistry) dsl.DataSource {
	values := make([]string, len(columns))
	for i, column := range columns {
		values[i] = registry.sample(column)
	}
	return dsl.DataSource{
		Name:           "openapi",
		Type:           "csv",
		InlineData:     strings.Join(columns, ",") + "\n" + strings.Join(values, ",") + "\n",
		Strategy:       "sequential",
		Delimiter:      ",",
		FirstRowHeader: true,
		Columns:        columns,
	}
}

func xmlForSchema(s schema, res resolver, location string, columns columnRegistry) string {
	resolved := res.resolveSchema(s, location, map[string]bool{})
	root := xmlElementName("request", resolved)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteByte('\n')
	writeXMLValue(&b, root, resolved, columns)
	return b.String()
}

func writeXMLValue(b *strings.Builder, name string, s schema, columns columnRegistry) {
	sourceName := name
	sensitive := schemaCredentialField(sourceName, s)
	name = xmlElementName(name, s)
	if len(s.Properties) == 0 && s.Type != "object" {
		b.WriteString("<" + name + ">")
		if sensitive {
			value := openAPICredentialReference(name)
			if authored, ok := authoredSchemaWireValue(s); ok {
				value = authored
			}
			b.WriteString(xmlEscape(value))
		} else {
			column := columns.add(sourceName, s)
			b.WriteString(xmlEscape("{{data.openapi." + column + "}}"))
		}
		b.WriteString("</" + name + ">")
		return
	}
	attrs := make([]string, 0)
	children := make([]string, 0)
	for _, propName := range sortedSchemaPropertyKeys(s.Properties) {
		prop := s.Properties[propName]
		if prop.XML != nil && prop.XML.Attribute {
			value := ""
			if schemaCredentialField(propName, prop) {
				value = openAPICredentialReference(propName)
				if authored, ok := authoredSchemaWireValue(prop); ok {
					value = authored
				}
			} else {
				column := columns.add(propName, prop)
				value = "{{data.openapi." + column + "}}"
			}
			attrs = append(attrs, xmlElementName(propName, prop)+`="`+xmlEscape(value)+`"`)
			continue
		}
		var child strings.Builder
		writeXMLValue(&child, propName, prop, columns)
		children = append(children, child.String())
	}
	b.WriteString("<" + name)
	if len(attrs) > 0 {
		b.WriteByte(' ')
		b.WriteString(strings.Join(attrs, " "))
	}
	b.WriteString(">")
	b.WriteString(strings.Join(children, ""))
	b.WriteString("</" + name + ">")
}

func xmlElementName(fallback string, s schema) string {
	if s.XML != nil && strings.TrimSpace(s.XML.Name) != "" {
		return strings.TrimSpace(s.XML.Name)
	}
	if fallback == "" {
		return "value"
	}
	return fallback
}

func xmlEscape(value string) string {
	var b bytes.Buffer
	if err := xmlEscapeText(&b, value); err != nil {
		return value
	}
	return b.String()
}

func xmlEscapeText(b *bytes.Buffer, value string) error {
	for _, r := range value {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&#34;")
		case '\'':
			b.WriteString("&#39;")
		default:
			b.WriteRune(r)
		}
	}
	return nil
}

func schemaNeedsRawJSONPlaceholder(s schema) bool {
	return strings.EqualFold(s.Type, "integer") || strings.EqualFold(s.Type, "number") || strings.EqualFold(s.Type, "boolean")
}

func sampleValueForSchema(s schema, column string) string {
	if value, ok := authoredScalarSeed(s.Example); ok {
		return value
	}
	if value, ok := authoredScalarSeed(s.Default); ok {
		return value
	}
	if len(s.Enum) > 0 {
		if value, ok := authoredScalarSeed(s.Enum[0]); ok {
			return value
		}
	}
	switch strings.ToLower(s.Type) {
	case "integer":
		return "42"
	case "number":
		return "42.5"
	case "boolean":
		return "true"
	default:
		return sampleColumnValue(column)
	}
}

func authoredScalarSeed(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case bool:
		return strconv.FormatBool(typed), true
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64), true
	case float32:
		return strconv.FormatFloat(float64(typed), 'g', -1, 32), true
	case int:
		return strconv.Itoa(typed), true
	case int64:
		return strconv.FormatInt(typed, 10), true
	case json.Number:
		return typed.String(), true
	default:
		return "", false
	}
}

func extractorNameSet(script *dsl.Script) map[string]bool {
	used := map[string]bool{}
	if script == nil {
		return used
	}
	for _, action := range append(append([]dsl.Action{}, script.Init...), append(script.Actions, script.Teardown...)...) {
		for _, extractor := range action.Extractors {
			if name := strings.TrimSpace(extractor.Name); name != "" {
				used[name] = true
			}
		}
	}
	return used
}

func uniqueExtractorName(base string, used map[string]bool) string {
	base = sanitizeColumnName(base)
	if base == "" {
		base = "value"
	}
	if !used[base] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s_%d", base, i)
		if !used[candidate] {
			return candidate
		}
	}
}

func varNameForProduced(resource string, field string) string {
	resource = sanitizeColumnName(singularize(resource))
	token := sanitizeColumnName(normalizedIdentityToken(field, resource))
	if token == "" || token == "id" || token == "uuid" || token == "guid" {
		token = "id"
	}
	if resource == "" {
		return token
	}
	if strings.HasPrefix(token, resource+"_") {
		return token
	}
	return sanitizeColumnName(resource + "_" + token)
}

func resourceOfPath(path string) string {
	segments := pathSegments(path)
	for i := len(segments) - 1; i >= 0; i-- {
		if !isPathParamSegment(segments[i]) {
			return singularize(segments[i])
		}
	}
	return "resource"
}

func resourceForParam(path string, param string) string {
	segments := pathSegments(path)
	param = strings.TrimSpace(param)
	for i, segment := range segments {
		if strings.Trim(segment, "{}") != param {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			if !isPathParamSegment(segments[j]) {
				return singularize(segments[j])
			}
		}
	}
	return resourceOfPath(path)
}

func pathSegments(path string) []string {
	raw := strings.Split(strings.Trim(path, "/"), "/")
	out := make([]string, 0, len(raw))
	for _, segment := range raw {
		if strings.TrimSpace(segment) != "" {
			out = append(out, strings.TrimSpace(segment))
		}
	}
	return out
}

func isPathParamSegment(segment string) bool {
	return strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}")
}

func singularize(value string) string {
	value = strings.ToLower(strings.Trim(value, "{}"))
	irregular := map[string]string{"people": "person", "children": "child", "statuses": "status"}
	if mapped := irregular[value]; mapped != "" {
		return mapped
	}
	switch {
	case strings.HasSuffix(value, "ies") && len(value) > 3:
		return strings.TrimSuffix(value, "ies") + "y"
	case strings.HasSuffix(value, "ses") && len(value) > 3:
		return strings.TrimSuffix(value, "es")
	case strings.HasSuffix(value, "s") && len(value) > 1:
		return strings.TrimSuffix(value, "s")
	default:
		return value
	}
}

func normalizedIdentityToken(value string, resource string) string {
	value = sanitizeColumnName(value)
	resource = sanitizeColumnName(resource)
	for _, token := range []string{resource + "_", resource} {
		value = strings.TrimPrefix(value, token)
	}
	value = strings.Trim(value, "_")
	switch value {
	case "", "id", "uuid", "guid":
		return "id"
	default:
		return value
	}
}

func intSliceContains(values []int, value int) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
