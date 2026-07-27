package dslwalk

import "strings"

// Action metadata is an untyped map that reaches the DSL from JSON, from YAML,
// and from importer code that sets Go values directly. The same logical field
// therefore arrives as a bool or a string, and a list as []string or []any.
// These helpers own that decoding so each consumer does not re-derive it.
//
// They deliberately stop at extraction. Normalization differs by consumer and
// is not incidental: pkg/api lowercases gate names because it compares them to
// literals, while pkg/convert/importreview preserves case for display. Folding
// those choices in here would silently change one of them, so callers apply
// their own.

// MetadataBool reads a boolean action-metadata field, accepting either a real
// bool or the string "true" (case-insensitive), which is how the value arrives
// after a JSON round trip through some importers.
func MetadataBool(metadata map[string]any, key string) bool {
	if metadata == nil {
		return false
	}
	switch value := metadata[key].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	default:
		return false
	}
}

// MetadataString reads a string action-metadata field and trims it. Callers
// that compare against a fixed vocabulary should lowercase the result.
func MetadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, _ := metadata[key].(string)
	return strings.TrimSpace(value)
}

// MetadataStrings reads a string-list action-metadata field, accepting both
// []string and []any, and drops empty entries. Order is the source order;
// callers that need a stable or deduplicated order apply it themselves.
func MetadataStrings(metadata map[string]any, key string) []string {
	if metadata == nil {
		return nil
	}
	var values []string
	switch raw := metadata[key].(type) {
	case []string:
		values = append(values, raw...)
	case []any:
		for _, item := range raw {
			if value, ok := item.(string); ok {
				values = append(values, value)
			}
		}
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
