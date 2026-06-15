package trueclient

// util.go — split from converter.go (same package, behavior-preserving).

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func parseTimeoutMs(raw string) int {
	value, err := parseFirstArgumentValue(raw, "Timeout", "timeout", "TimeoutMs", "timeout_ms")
	if err != nil {
		return 0
	}
	value = trimQuotedValue(value)
	if value == "" {
		return 0
	}
	value = strings.TrimSuffix(strings.ToLower(value), "ms")
	var timeout int
	if _, err := fmt.Sscanf(value, "%d", &timeout); err != nil || timeout <= 0 {
		return 0
	}
	return timeout
}

func parsePositiveInt(value string) int {
	parsed := parseSignedInt(value)
	if parsed < 0 {
		return -parsed
	}
	return parsed
}

func parseSignedInt(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	value = strings.TrimSuffix(strings.ToLower(value), "px")
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func stableActionToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "action"
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	token := strings.Trim(b.String(), "_")
	if token == "" {
		return "action"
	}
	return token
}

func detectBaseURL(target string) string {
	parsed, err := url.Parse(strings.TrimSpace(target))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func appendIfMissing(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
