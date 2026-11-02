package requestimport

import (
	"net/url"
	"strings"
)

// HARQueryStringMetadataKey holds the recorded HAR query parameters
// ([]{name, value}) on an imported action. It is provenance: the request
// target, not this list, is sent.
const HARQueryStringMetadataKey = "har.request.query_string"

// ContainsTemplatePlaceholder reports whether raw holds a {{...}} template,
// literally or percent-encoded.
func ContainsTemplatePlaceholder(raw string) bool {
	return strings.Contains(raw, "{{") || strings.Contains(raw, "%7B%7B") || strings.Contains(raw, "%7b%7b")
}

// TemplatedPathMatches reports whether a request path whose segments may hold
// templates matches a recorded path: equal segment counts, and every segment
// either equal or a template standing for a non-empty recorded segment.
func TemplatedPathMatches(templatePath, recordedPath string) bool {
	if templatePath == recordedPath {
		return true
	}
	templateSegs := strings.Split(templatePath, "/")
	recordedSegs := strings.Split(recordedPath, "/")
	if len(templateSegs) != len(recordedSegs) {
		return false
	}
	for i := range templateSegs {
		if templateSegs[i] == recordedSegs[i] {
			continue
		}
		if !ContainsTemplatePlaceholder(templateSegs[i]) || recordedSegs[i] == "" {
			return false
		}
	}
	return true
}

// TemplatedQueryMatches reports whether a raw query whose values may hold
// templates matches a recorded raw query: the same keys and value counts, and
// every value either equal or a template.
func TemplatedQueryMatches(templateQuery, recordedQuery string) bool {
	if templateQuery == recordedQuery {
		return true
	}
	templateValues, err1 := url.ParseQuery(templateQuery)
	recordedValues, err2 := url.ParseQuery(recordedQuery)
	if err1 != nil || err2 != nil || len(templateValues) != len(recordedValues) {
		return false
	}
	for key, values := range templateValues {
		recorded, exists := recordedValues[key]
		if !exists || len(values) != len(recorded) {
			return false
		}
		for i := range values {
			if values[i] != recorded[i] && !ContainsTemplatePlaceholder(values[i]) {
				return false
			}
		}
	}
	return true
}

// CorrelatedQueryValue substitutes correlation templates into one decoded
// query value. apply replaces recorded literals with their templates; a value
// recorded decoded is also tried in its query-encoded spelling, which is how
// the correlation was keyed when the request target carried it encoded.
func CorrelatedQueryValue(value string, vars map[string]string, apply func(string, map[string]string) string) string {
	if value == "" || len(vars) == 0 {
		return value
	}
	if updated := apply(value, vars); updated != value {
		return updated
	}
	escaped := url.QueryEscape(value)
	if escaped == value {
		return value
	}
	if template, ok := vars[escaped]; ok {
		return template
	}
	if updated := apply(escaped, vars); updated != escaped && strings.Contains(updated, "{{") {
		return updated
	}
	return value
}

// UpdateHARQueryStringValues replaces each recorded query value in metadata
// (see HARQueryStringMetadataKey) with mapValue's result. Entries are copied,
// never mutated in place, so metadata shared with another action is safe; when
// mapValue changes no value, the metadata is left as it was and nothing is
// copied.
func UpdateHARQueryStringValues(metadata map[string]any, mapValue func(string) string) {
	if len(metadata) == 0 || mapValue == nil {
		return
	}
	// mapped holds the new value of each entry whose value changed, by index.
	var mapped map[int]string
	mapEntry := func(i int, item map[string]any) {
		value, ok := item["value"].(string)
		if !ok || value == "" {
			return
		}
		if next := mapValue(value); next != value {
			if mapped == nil {
				mapped = map[int]string{}
			}
			mapped[i] = next
		}
	}
	copyEntry := func(item map[string]any, value string) map[string]any {
		entry := make(map[string]any, len(item))
		for key, existing := range item {
			entry[key] = existing
		}
		entry["value"] = value
		return entry
	}
	switch items := metadata[HARQueryStringMetadataKey].(type) {
	case []map[string]any:
		for i, item := range items {
			mapEntry(i, item)
		}
		if mapped == nil {
			return
		}
		updated := make([]map[string]any, len(items))
		for i, item := range items {
			if value, ok := mapped[i]; ok {
				updated[i] = copyEntry(item, value)
			} else {
				updated[i] = item
			}
		}
		metadata[HARQueryStringMetadataKey] = updated
	case []any:
		for i, raw := range items {
			if item, ok := raw.(map[string]any); ok {
				mapEntry(i, item)
			}
		}
		if mapped == nil {
			return
		}
		updated := make([]any, len(items))
		for i, raw := range items {
			if value, ok := mapped[i]; ok {
				updated[i] = copyEntry(raw.(map[string]any), value)
			} else {
				updated[i] = raw
			}
		}
		metadata[HARQueryStringMetadataKey] = updated
	}
}

// CorrelateHARQueryStringValues substitutes correlation templates (vars,
// through apply) into the recorded HAR query values in metadata (see
// HARQueryStringMetadataKey). It does nothing without vars, and copies the
// metadata only when a value changes.
func CorrelateHARQueryStringValues(metadata map[string]any, vars map[string]string, apply func(string, map[string]string) string) {
	if len(vars) == 0 || apply == nil {
		return
	}
	UpdateHARQueryStringValues(metadata, func(value string) string {
		return CorrelatedQueryValue(value, vars, apply)
	})
}
