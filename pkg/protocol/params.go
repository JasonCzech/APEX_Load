package protocol

import "strings"

// This file holds the small action/config coercion helpers shared by the
// protocol implementations (network, database, http, jms, kafka, browser,
// java_vuser, …). They previously existed as near-identical per-package copies
// that had drifted (e.g. database's ConfigString lost its TrimSpace, and the
// JMS/Kafka FirstNonEmpty returned untrimmed values); consolidating them here
// fixes those divergences and gives the behavior a single home.

// ActionName returns the action's name, or "" for a nil action.
func ActionName(action *Action) string {
	if action == nil {
		return ""
	}
	return action.Name
}

// ConfigString returns the trimmed string at key, or "" when the config is nil,
// the key is absent, or the value is not a string.
func ConfigString(config map[string]any, key string) string {
	if config == nil {
		return ""
	}
	if value, ok := config[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

// ConfigStringSlice returns the string slice at key, trimming entries and
// dropping blanks. It accepts []string and []any inputs; anything else yields nil.
func ConfigStringSlice(config map[string]any, key string) []string {
	if config == nil {
		return nil
	}
	switch value := config[key].(type) {
	case []string:
		return append([]string(nil), value...)
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	default:
		return nil
	}
}

// ConfigInt coerces the numeric value at key to int, or 0 when the config is nil,
// the key is absent, or the value is non-numeric.
func ConfigInt(config map[string]any, key string) int {
	if config == nil {
		return 0
	}
	switch value := config[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case float32:
		return int(value)
	default:
		return 0
	}
}

// ConfigFloat coerces the numeric value at key to float64, or 0 when the config
// is nil, the key is absent, or the value is non-numeric.
func ConfigFloat(config map[string]any, key string) float64 {
	if config == nil {
		return 0
	}
	switch value := config[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	default:
		return 0
	}
}

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
