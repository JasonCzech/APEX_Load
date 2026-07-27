package paramize

import (
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/credential"
)

// LooksDynamicValue reports whether a key/value pair looks like a dynamic
// runtime value (session token, CSRF nonce, long opaque identifier) rather
// than stable test data. Lifted from the browser-to-HTTP conversion heuristics
// so every importer shares one rule set.
func LooksDynamicValue(key string, value string) bool {
	lowerKey := strings.ToLower(key)
	if strings.Contains(lowerKey, "token") || strings.Contains(lowerKey, "session") || strings.Contains(lowerKey, "csrf") || strings.Contains(lowerKey, "nonce") {
		return true
	}
	value = strings.TrimSpace(value)
	if len(value) >= 24 {
		return true
	}
	if strings.Count(value, "-") >= 4 {
		return true
	}
	return false
}

// LooksParameterName reports whether a field name looks like business test
// data that should be bound to a data source so each VUser gets distinct
// values (user ids, emails, SKUs, account numbers, ...).
func LooksParameterName(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	for _, marker := range []string{"id", "user", "customer", "account", "order", "sku", "email", "zip"} {
		if lower == marker || strings.HasSuffix(lower, "_"+marker) || strings.Contains(lower, marker+"_") {
			return true
		}
	}
	return false
}

// SensitiveHeaderKey reports whether a header carries credentials.
func SensitiveHeaderKey(key string) bool {
	return credential.SensitiveHeader(key)
}

// EnvKey converts a field or header name into the UPPER_SNAKE environment
// variable key used in {{env.KEY}} replacements.
func EnvKey(name string) string {
	return credential.EnvKey(name)
}

var selectorTokenPattern = regexp.MustCompile(`[A-Za-z][A-Za-z0-9_\-]*`)

// SelectorFieldHint derives a field-name hint from a browser selector so fill
// values can run through the same name heuristics as HTTP parameters, e.g.
// `#user_email` -> "user_email", `input[name="user_id"]` -> "user_id".
func SelectorFieldHint(selector string) string {
	// Selectors normally contain only a handful of tokens. Bound the result set
	// anyway: imported selector text is untrusted, and collecting every match in
	// a pathological value would bypass the shared parameterization work limits.
	if int64(len(selector)) > maxSuggestionRegexSearchBytes {
		return ""
	}
	tokens := selectorTokenPattern.FindAllString(selector, maxSuggestionRegexMatches+1)
	if len(tokens) > maxSuggestionRegexMatches {
		return ""
	}
	skip := map[string]bool{
		"input": true, "textarea": true, "select": true, "button": true,
		"form": true, "div": true, "span": true, "label": true,
		"name": true, "type": true, "data": true, "aria": true, "css": true, "xpath": true, "text": true,
	}
	for i := len(tokens) - 1; i >= 0; i-- {
		token := strings.ToLower(tokens[i])
		if !skip[token] {
			return token
		}
	}
	return ""
}
