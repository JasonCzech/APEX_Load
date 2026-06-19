// Package convertutil holds tiny helpers shared by the format importers under
// pkg/convert. They previously existed as ~14 near-identical per-importer copies
// of FirstNonEmpty and 5 of BoolOption; one copy (asyncapi's FirstNonEmpty)
// returned the untrimmed value, which this consolidation reconciles to trimmed.
package convertutil

import "strings"

// FirstNonEmpty returns the first value that is non-empty after trimming
// surrounding whitespace, or "" when every value is blank. The returned value is
// trimmed.
func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// BoolOption returns *value when value is non-nil, otherwise fallback. It is used
// to resolve optional *bool import options against a default.
func BoolOption(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}
