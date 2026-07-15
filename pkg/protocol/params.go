package protocol

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

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
		out := make([]string, 0, len(value))
		for _, item := range value {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
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
		if value > int64(maxIntValue()) || value < int64(minIntValue()) {
			return 0
		}
		return int(value)
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) || value >= float64(maxIntValue()) || value < float64(minIntValue()) {
			return 0
		}
		return int(value)
	case float32:
		converted := float64(value)
		if math.IsNaN(converted) || math.IsInf(converted, 0) || converted >= float64(maxIntValue()) || converted < float64(minIntValue()) {
			return 0
		}
		return int(converted)
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
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return 0
		}
		return value
	case float32:
		converted := float64(value)
		if math.IsNaN(converted) || math.IsInf(converted, 0) {
			return 0
		}
		return converted
	case int:
		return float64(value)
	case int64:
		return float64(value)
	default:
		return 0
	}
}

func maxIntValue() int { return int(^uint(0) >> 1) }

func minIntValue() int { return -maxIntValue() - 1 }

// BoundedMilliseconds parses an integer millisecond value without permitting
// lossy numeric coercion or time.Duration overflow. A missing/nil value and an
// explicit zero both select fallback; all positive values must fit maximum.
func BoundedMilliseconds(value any, fallback, maximum time.Duration) (time.Duration, error) {
	if maximum <= 0 {
		return 0, fmt.Errorf("maximum duration must be positive")
	}
	if value == nil {
		return fallback, nil
	}
	maximumMilliseconds := int64(maximum / time.Millisecond)
	var milliseconds int64
	switch typed := value.(type) {
	case int:
		milliseconds = int64(typed)
	case int64:
		milliseconds = typed
	case int32:
		milliseconds = int64(typed)
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed || typed < 0 || typed > float64(maximumMilliseconds) {
			return 0, fmt.Errorf("must be an integer between 0 and %d", maximumMilliseconds)
		}
		milliseconds = int64(typed)
	case float32:
		converted := float64(typed)
		if math.IsNaN(converted) || math.IsInf(converted, 0) || math.Trunc(converted) != converted || converted < 0 || converted > float64(maximumMilliseconds) {
			return 0, fmt.Errorf("must be an integer between 0 and %d", maximumMilliseconds)
		}
		milliseconds = int64(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, fmt.Errorf("must be an integer between 0 and %d", maximumMilliseconds)
		}
		milliseconds = parsed
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("must be an integer between 0 and %d", maximumMilliseconds)
		}
		milliseconds = parsed
	default:
		return 0, fmt.Errorf("must be an integer between 0 and %d", maximumMilliseconds)
	}
	if milliseconds < 0 || milliseconds > maximumMilliseconds {
		return 0, fmt.Errorf("must be an integer between 0 and %d", maximumMilliseconds)
	}
	if milliseconds == 0 {
		return fallback, nil
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
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
