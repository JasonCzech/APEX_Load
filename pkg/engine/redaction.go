package engine

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/jasonczech/stormrunner/pkg/protocol"
)

const redactedPayloadValue = "[REDACTED]"

type RedactionRule struct {
	Name        string `json:"name,omitempty"`
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement,omitempty"`
}

type RedactionConfig struct {
	Rules    []RedactionRule
	compiled []payloadReplacement
}

var sensitiveSnapshotHeaderNames = map[string]struct{}{
	"authorization":        {},
	"proxy-authorization":  {},
	"cookie":               {},
	"set-cookie":           {},
	"x-api-key":            {},
	"x-auth-token":         {},
	"x-csrf-token":         {},
	"x-xsrf-token":         {},
	"x-amz-security-token": {},
	"id-token":             {},
}

var payloadRedactionPatterns = []payloadReplacement{
	{
		re:   regexp.MustCompile(`(?i)("(?:access_token|refresh_token|api[_-]?key|auth(?:orization)?|token|secret|password|passwd|session(?:_id)?|ssn|social_security_number|mrn|medical_record_number|patient(?:_id)?|member(?:_id)?)"\s*:\s*")([^"]+)(")`),
		repl: `${1}[REDACTED]${3}`,
	},
	{
		re:   regexp.MustCompile(`(?i)((?:^|[?&;\s])(?:access_token|refresh_token|api[_-]?key|auth(?:orization)?|token|secret|password|passwd|session(?:_id)?|ssn|social_security_number|mrn|medical_record_number|patient(?:_id)?|member(?:_id)?)=)([^&\s;#]+)`),
		repl: `${1}[REDACTED]`,
	},
	{
		re:   regexp.MustCompile(`(?is)(<(?:[\w-]+:)?(?:access_token|refresh_token|api[_-]?key|token|secret|password|passwd|session(?:_id)?|ssn|social_security_number|mrn|medical_record_number|patient(?:_id)?|member(?:_id)?)\b[^>]*>)(.*?)(</(?:[\w-]+:)?(?:access_token|refresh_token|api[_-]?key|token|secret|password|passwd|session(?:_id)?|ssn|social_security_number|mrn|medical_record_number|patient(?:_id)?|member(?:_id)?)>)`),
		repl: `${1}[REDACTED]${3}`,
	},
	{
		re:   regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
		repl: redactedPayloadValue,
	},
	{
		re:   regexp.MustCompile(`\b[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`),
		repl: redactedPayloadValue,
	},
}

var highEntropyTokenPattern = regexp.MustCompile(`\b[A-Za-z0-9+/=_-]{24,}\b`)

type payloadReplacement struct {
	re   *regexp.Regexp
	repl string
}

func NewRedactionConfig(rules []RedactionRule) (*RedactionConfig, error) {
	if len(rules) == 0 {
		return nil, nil
	}

	config := &RedactionConfig{
		Rules: make([]RedactionRule, len(rules)),
	}
	copy(config.Rules, rules)
	config.compiled = make([]payloadReplacement, 0, len(rules))

	for i, rule := range rules {
		pattern := strings.TrimSpace(rule.Pattern)
		if pattern == "" {
			return nil, fmt.Errorf("redaction rule[%d] pattern is required", i)
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			name := strings.TrimSpace(rule.Name)
			if name == "" {
				name = pattern
			}
			return nil, fmt.Errorf("redaction rule[%d] %q: %w", i, name, err)
		}
		replacement := rule.Replacement
		if replacement == "" {
			replacement = redactedPayloadValue
		}
		config.compiled = append(config.compiled, payloadReplacement{re: re, repl: replacement})
	}

	return config, nil
}

func (c *RedactionConfig) ScrubText(value string) string {
	return scrubSensitiveTextWithConfig(value, c)
}

func scrubSensitiveResult(result *protocol.Result, config *RedactionConfig) {
	if result == nil {
		return
	}

	result.ResponseBody = scrubSensitiveTextWithConfig(result.ResponseBody, config)
	if result.Snapshot == nil {
		return
	}

	result.Snapshot.RequestURL = scrubSensitiveTextWithConfig(result.Snapshot.RequestURL, config)
	result.Snapshot.RequestHeaders = scrubSensitiveHeaders(result.Snapshot.RequestHeaders, config)
	result.Snapshot.RequestBody = scrubSensitiveTextWithConfig(result.Snapshot.RequestBody, config)
	result.Snapshot.ResponseHeaders = scrubSensitiveHeaders(result.Snapshot.ResponseHeaders, config)
	result.Snapshot.ResponseBody = scrubSensitiveTextWithConfig(result.Snapshot.ResponseBody, config)
	result.Snapshot.HTML = scrubSensitiveTextWithConfig(result.Snapshot.HTML, config)
}

func scrubSensitiveHeaders(headers map[string]string, config *RedactionConfig) map[string]string {
	if len(headers) == 0 {
		return headers
	}
	for key, value := range headers {
		if isSensitiveHeaderName(key) {
			headers[key] = redactedPayloadValue
			continue
		}
		headers[key] = scrubSensitiveTextWithConfig(value, config)
	}
	return headers
}

func isSensitiveHeaderName(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if _, ok := sensitiveSnapshotHeaderNames[normalized]; ok {
		return true
	}
	for _, marker := range []string{"token", "secret", "password", "session", "cookie", "ssn", "mrn", "patient"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func scrubSensitiveText(value string) string {
	return scrubSensitiveTextWithConfig(value, nil)
}

func scrubSensitiveTextWithConfig(value string, config *RedactionConfig) string {
	if strings.TrimSpace(value) == "" {
		return value
	}

	redacted := value
	for _, pattern := range payloadRedactionPatterns {
		redacted = pattern.re.ReplaceAllString(redacted, pattern.repl)
	}
	if config != nil {
		for _, pattern := range config.compiled {
			redacted = pattern.re.ReplaceAllString(redacted, pattern.repl)
		}
	}
	redacted = highEntropyTokenPattern.ReplaceAllStringFunc(redacted, func(candidate string) string {
		if !looksSensitiveToken(candidate) {
			return candidate
		}
		return redactedPayloadValue
	})
	return redacted
}

func looksSensitiveToken(value string) bool {
	if len(value) < 24 {
		return false
	}

	hasLetter := false
	hasDigit := false
	for _, ch := range value {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z':
			hasLetter = true
		case ch >= '0' && ch <= '9':
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return false
	}

	return shannonEntropy(value) >= 4.0
}

func shannonEntropy(value string) float64 {
	if value == "" {
		return 0
	}

	counts := make(map[rune]float64)
	for _, ch := range value {
		counts[ch]++
	}

	length := float64(len([]rune(value)))
	entropy := 0.0
	for _, count := range counts {
		p := count / length
		entropy -= p * math.Log2(p)
	}
	return entropy
}
