package engine

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
)

const redactedPayloadValue = "[REDACTED]"

const (
	// Custom redaction runs on every retained result field and therefore needs a
	// hard runtime ceiling independent of API request limits. These limits are
	// generous for enterprise PII policies while preventing a persisted or
	// remotely assigned policy from multiplying per-transaction work without
	// bound.
	MaxRedactionRules            = 128
	MaxRedactionRuleNameBytes    = 1 << 10
	MaxRedactionPatternBytes     = 4 << 10
	MaxRedactionReplacementBytes = 4 << 10
	MaxRedactionPolicyBytes      = 256 << 10
)

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
	if len(rules) > MaxRedactionRules {
		return nil, fmt.Errorf("redaction policy exceeds %d rule limit", MaxRedactionRules)
	}
	totalBytes := 0
	for i, rule := range rules {
		if len(rule.Name) > MaxRedactionRuleNameBytes {
			return nil, fmt.Errorf("redaction rule[%d] name exceeds %d byte limit", i, MaxRedactionRuleNameBytes)
		}
		if len(rule.Pattern) > MaxRedactionPatternBytes {
			return nil, fmt.Errorf("redaction rule[%d] pattern exceeds %d byte limit", i, MaxRedactionPatternBytes)
		}
		if len(rule.Replacement) > MaxRedactionReplacementBytes {
			return nil, fmt.Errorf("redaction rule[%d] replacement exceeds %d byte limit", i, MaxRedactionReplacementBytes)
		}
		ruleBytes := len(rule.Name) + len(rule.Pattern) + len(rule.Replacement)
		if ruleBytes > MaxRedactionPolicyBytes-totalBytes {
			return nil, fmt.Errorf("redaction policy exceeds %d byte limit", MaxRedactionPolicyBytes)
		}
		totalBytes += ruleBytes
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
	scrubSensitiveResultWithOptions(result, config, true)
}

func scrubSensitiveResultWithOptions(result *protocol.Result, config *RedactionConfig, retainResponseBody bool) {
	if result == nil {
		return
	}

	scrubSensitiveResultMetadata(result, config)
	result.ErrorMessage = scrubSensitiveTextWithConfig(result.ErrorMessage, config)
	if retainResponseBody {
		result.ResponseBody = scrubSensitiveTextWithConfig(result.ResponseBody, config)
	} else {
		result.ResponseBody = ""
	}
	result.PageURL = scrubSensitiveTextWithConfig(result.PageURL, config)
	result.PageTitle = scrubSensitiveTextWithConfig(result.PageTitle, config)
	result.HTML = scrubSensitiveTextWithConfig(result.HTML, config)
	result.ConsoleEntries = scrubSensitiveConsoleEntries(result.ConsoleEntries, config)
	result.NetworkEntries = scrubSensitiveNetworkEntries(result.NetworkEntries, config)
	result.DOMSummary = scrubSensitiveDOMSummary(result.DOMSummary, config)
	result.Extractions = scrubSensitiveExtractions(result.Extractions, config)
	result.ProtocolEvidence = scrubSensitiveEvidenceMap(result.ProtocolEvidence, config)
	if result.Snapshot == nil {
		return
	}

	result.Snapshot.RequestURL = scrubSensitiveTextWithConfig(result.Snapshot.RequestURL, config)
	result.Snapshot.RequestHeaders = scrubSensitiveHeaders(result.Snapshot.RequestHeaders, config)
	result.Snapshot.RequestBody = scrubSensitiveTextWithConfig(result.Snapshot.RequestBody, config)
	result.Snapshot.ResponseHeaders = scrubSensitiveHeaders(result.Snapshot.ResponseHeaders, config)
	result.Snapshot.ResponseBody = scrubSensitiveTextWithConfig(result.Snapshot.ResponseBody, config)
	result.Snapshot.PageURL = scrubSensitiveTextWithConfig(result.Snapshot.PageURL, config)
	result.Snapshot.PageTitle = scrubSensitiveTextWithConfig(result.Snapshot.PageTitle, config)
	result.Snapshot.HTML = scrubSensitiveTextWithConfig(result.Snapshot.HTML, config)
	result.Snapshot.ConsoleEntries = scrubSensitiveConsoleEntries(result.Snapshot.ConsoleEntries, config)
	result.Snapshot.NetworkEntries = scrubSensitiveNetworkEntries(result.Snapshot.NetworkEntries, config)
	result.Snapshot.DOMSummary = scrubSensitiveDOMSummary(result.Snapshot.DOMSummary, config)
}

// scrubSensitiveResultMetadata covers the scalar dimensions copied into live
// metrics and generator archives. These fields bypass response/snapshot
// evidence handling, so leaving them untouched would let a resolved secret in
// a correlation header, dependency label, adapter label, or imported source
// identifier survive an otherwise valid custom redaction policy.
func scrubSensitiveResultMetadata(result *protocol.Result, config *RedactionConfig) {
	result.ActionName = scrubSensitiveTextWithConfig(result.ActionName, config)
	result.Group = scrubSensitiveTextWithConfig(result.Group, config)
	result.Region = scrubSensitiveTextWithConfig(result.Region, config)
	result.CorrelationID = scrubSensitiveTextWithConfig(result.CorrelationID, config)
	result.NetworkProfile = scrubSensitiveTextWithConfig(result.NetworkProfile, config)
	result.DependencyType = scrubSensitiveTextWithConfig(result.DependencyType, config)
	result.DependencyTarget = scrubSensitiveTextWithConfig(result.DependencyTarget, config)
	result.SourceStepID = scrubSensitiveTextWithConfig(result.SourceStepID, config)
	result.SourceAction = scrubSensitiveTextWithConfig(result.SourceAction, config)
	result.SourceActionName = scrubSensitiveTextWithConfig(result.SourceActionName, config)
	result.SourceTransaction = scrubSensitiveTextWithConfig(result.SourceTransaction, config)
	result.SourceProfile = scrubSensitiveTextWithConfig(result.SourceProfile, config)

	if result.ClientMetrics != nil {
		result.ClientMetrics.DeviceProfile = scrubSensitiveTextWithConfig(result.ClientMetrics.DeviceProfile, config)
		result.ClientMetrics.DevicePlatform = scrubSensitiveTextWithConfig(result.ClientMetrics.DevicePlatform, config)
		result.ClientMetrics.DeviceTier = scrubSensitiveTextWithConfig(result.ClientMetrics.DeviceTier, config)
		result.ClientMetrics.BrowserEngine = scrubSensitiveTextWithConfig(result.ClientMetrics.BrowserEngine, config)
	}
	if result.ServerTelemetry != nil {
		result.ServerTelemetry.RequestSizeClass = scrubSensitiveTextWithConfig(result.ServerTelemetry.RequestSizeClass, config)
		result.ServerTelemetry.ResponseSizeClass = scrubSensitiveTextWithConfig(result.ServerTelemetry.ResponseSizeClass, config)
		result.ServerTelemetry.SOAPFaultCode = scrubSensitiveTextWithConfig(result.ServerTelemetry.SOAPFaultCode, config)
		result.ServerTelemetry.SOAPFaultCategory = scrubSensitiveTextWithConfig(result.ServerTelemetry.SOAPFaultCategory, config)
		result.ServerTelemetry.SOAPNamespaceConflict = scrubSensitiveTextWithConfig(result.ServerTelemetry.SOAPNamespaceConflict, config)
		result.ServerTelemetry.PartitionID = scrubSensitiveTextWithConfig(result.ServerTelemetry.PartitionID, config)
	}
}

// scrubSensitiveStructuredResult runs before the first bounded projection. It
// handles key-aware extraction values and typed/heterogeneous adapter evidence
// that the generic evidence bounder cannot otherwise preserve safely. Runtime
// variables have already received the raw extraction values at this point.
func scrubSensitiveStructuredResult(result *protocol.Result, config *RedactionConfig) {
	if result == nil {
		return
	}
	result.Extractions = boundedResultStringMap(result.Extractions, maxResultExtractions, maxResultExtractionBytes)
	result.Extractions = scrubSensitiveExtractions(result.Extractions, config)
	result.ProtocolEvidence = scrubSensitiveEvidenceMap(result.ProtocolEvidence, config)
}

func scrubSensitiveExtractions(values map[string]string, config *RedactionConfig) map[string]string {
	if len(values) == 0 {
		return values
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		if scrub.SensitiveName(key) {
			out[key] = redactedPayloadValue
			continue
		}
		out[key] = scrubSensitiveTextWithConfig(value, config)
	}
	return out
}

func scrubSensitiveNetworkEntries(entries []protocol.NetworkEntry, config *RedactionConfig) []protocol.NetworkEntry {
	for i := range entries {
		entries[i].URL = scrubSensitiveTextWithConfig(entries[i].URL, config)
		entries[i].RequestHeaders = scrubSensitiveHeaders(entries[i].RequestHeaders, config)
		entries[i].RequestBody = scrubSensitiveTextWithConfig(entries[i].RequestBody, config)
		entries[i].ResponseHeaders = scrubSensitiveHeaders(entries[i].ResponseHeaders, config)
	}
	return entries
}

func scrubSensitiveConsoleEntries(entries []protocol.ConsoleEntry, config *RedactionConfig) []protocol.ConsoleEntry {
	for i := range entries {
		entries[i].Text = scrubSensitiveTextWithConfig(entries[i].Text, config)
		entries[i].Source = scrubSensitiveTextWithConfig(entries[i].Source, config)
	}
	return entries
}

func scrubSensitiveDOMSummary(summary *protocol.DOMSummary, config *RedactionConfig) *protocol.DOMSummary {
	if summary == nil {
		return nil
	}
	summary.Title = scrubSensitiveTextWithConfig(summary.Title, config)
	for i := range summary.VisibleText {
		summary.VisibleText[i] = scrubSensitiveTextWithConfig(summary.VisibleText[i], config)
	}
	return summary
}

func scrubSensitiveEvidenceMap(input map[string]any, config *RedactionConfig) map[string]any {
	if len(input) == 0 {
		return input
	}
	builtIn, _ := scrub.Structured(input, scrub.Policy{}).(map[string]any)
	if len(builtIn) == 0 {
		return nil
	}
	for key, value := range builtIn {
		builtIn[key] = scrubSensitiveEvidenceValue(value, config)
	}
	return builtIn
}

func scrubSensitiveEvidenceValue(value any, config *RedactionConfig) any {
	switch typed := value.(type) {
	case string:
		return scrubSensitiveTextWithConfig(typed, config)
	case []any:
		for i := range typed {
			typed[i] = scrubSensitiveEvidenceValue(typed[i], config)
		}
		return typed
	case map[string]any:
		return scrubSensitiveEvidenceMap(typed, config)
	case map[string]string:
		out := make(map[string]string, len(typed))
		for key, value := range typed {
			if scrub.SensitiveName(key) {
				out[key] = redactedPayloadValue
			} else {
				out[key] = scrubSensitiveTextWithConfig(value, config)
			}
		}
		return out
	default:
		return value
	}
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

func scrubSensitiveError(err error, config *RedactionConfig) error {
	if err == nil {
		return nil
	}
	return errors.New(scrubSensitiveTextWithConfig(err.Error(), config))
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
	return scrub.Value(redacted, scrub.Policy{})
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
