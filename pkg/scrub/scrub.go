// Package scrub is the shared scrub-on-write component for captured traffic
// artifacts. Recorder sessions, runtime canary HAR exports, and any other
// HAR-with-bodies artifact pass through it before persistence so credentials,
// session tokens, and configured PII patterns never reach disk. Unlike
// design-time parameterization (pkg/convert/paramize), which replaces secrets
// with runnable templates, scrubbing is irreversible redaction.
package scrub

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
)

// Redacted replaces every scrubbed value.
const Redacted = "[REDACTED]"

// Rule is one custom pattern → replacement rewrite applied to values, bodies,
// and HAR content.
type Rule struct {
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement,omitempty"`
}

// Policy configures scrubbing. The zero value applies the built-in protections
// (auth headers, cookies, token-bearing query/body fields).
type Policy struct {
	// ExtraHeaders extends the built-in sensitive header set.
	ExtraHeaders []string `json:"extra_headers,omitempty"`
	// ExtraPatterns are customer-defined PII/sensitive-value rules.
	ExtraPatterns []Rule `json:"extra_patterns,omitempty"`
	// KeepCookies disables cookie redaction (cookies are scrubbed by default).
	KeepCookies bool `json:"keep_cookies,omitempty"`
}

// sensitiveHeaders are always redacted (case-insensitive match).
var sensitiveHeaders = map[string]bool{
	"authorization":        true,
	"proxy-authorization":  true,
	"x-api-key":            true,
	"api-key":              true,
	"x-auth-token":         true,
	"x-access-token":       true,
	"x-csrf-token":         true,
	"x-xsrf-token":         true,
	"x-session-id":         true,
	"x-amz-security-token": true,
}

var cookieHeaders = map[string]bool{
	"cookie":     true,
	"set-cookie": true,
}

// sensitiveParamPattern redacts token-bearing query string and form fields.
var sensitiveParamPattern = regexp.MustCompile(`(?i)((?:^|[&?;\s])(?:password|passwd|secret|token|access_token|refresh_token|id_token|api_key|apikey|session|session_id|sessionid|auth|csrf|xsrf|client_secret|private_key)=)([^&\s;#"']+)`)

// sensitiveJSONPattern redacts token-bearing JSON body fields. Note: this list is
// deliberately narrower than sensitiveParamNames. Query-string/header values are
// in transit, but JSON BODY fields are the primary source the correlation engine
// captures-and-replays (the HAR/recorder pipelines scrub BEFORE correlation), so
// classic correlation targets — session/sessionid/csrf/xsrf — must stay
// un-redacted here or replay parameterization breaks (see pkg/convert/har
// TestConvertHAR). Only unambiguous secrets that are never correlation keys are
// added; "authorization" carries a Bearer token in a body and is one such case.
var (
	sensitiveJSONPattern = regexp.MustCompile(`(?i)("(?:authorization|password|passwd|secret|token|access[_-]?token|refresh[_-]?token|id[_-]?token|api[_-]?key|apikey|client[_-]?secret|private[_-]?key)"\s*:\s*)("[^"]*"|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?|true|false|null)`)
	sensitiveXMLPattern  = regexp.MustCompile(`(?is)(<(?:[\w.-]+:)?(?:authorization|password|passwd|secret|token|access[_-]?token|refresh[_-]?token|id[_-]?token|api[_-]?key|apikey|client[_-]?secret|private[_-]?key)\b[^>]*>)(.*?)(</(?:[\w.-]+:)?(?:authorization|password|passwd|secret|token|access[_-]?token|refresh[_-]?token|id[_-]?token|api[_-]?key|apikey|client[_-]?secret|private[_-]?key)>)`)
	sensitiveXMLAttr     = regexp.MustCompile(`(?is)(\b(?:authorization|password|passwd|secret|token|access[_-]?token|refresh[_-]?token|id[_-]?token|api[_-]?key|apikey|client[_-]?secret|private[_-]?key)\s*=\s*")([^"]*)(")`)
	multipartSecretPart  = regexp.MustCompile(`(?is)(Content-Disposition:\s*form-data;[^\r\n]*\bname="(?:authorization|password|passwd|secret|token|access[_-]?token|refresh[_-]?token|id[_-]?token|api[_-]?key|apikey|client[_-]?secret|private[_-]?key)"[^\r\n]*(?:\r?\nContent-Type:[^\r\n]*)?\r?\n\r?\n)(.*?)(\r?\n--)`)
	jwtPattern           = regexp.MustCompile(`\b[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	highEntropyToken     = regexp.MustCompile(`\b[A-Za-z0-9+/=_-]{24,}\b`)
)

// compiledRule is one ExtraPatterns rule with its regexp compiled exactly once.
type compiledRule struct {
	pattern     *regexp.Regexp
	replacement string
}

// compiledPolicy holds a policy together with its ExtraPatterns rules compiled
// up front, so per-value scrubbing reuses the same *regexp.Regexp instead of
// recompiling on every call. It is built once at the top of an entry point
// (Headers/HAR) and threaded through every per-value scrub for that artifact.
type compiledPolicy struct {
	policy Policy
	rules  []compiledRule
}

// Compile compiles the policy's ExtraPatterns once, returning the invalid
// patterns so callers can surface them instead of silently redacting nothing.
// A nil/empty error slice means every custom pattern compiled cleanly. This is
// additive: existing callers that ignore custom-pattern validity keep working,
// while operators who want feedback on a typo'd PII rule can call it directly.
func (p Policy) Compile() (valid []*regexp.Regexp, errs []error) {
	cp, errs := p.compile()
	for _, rule := range cp.rules {
		valid = append(valid, rule.pattern)
	}
	return valid, errs
}

// compile builds a compiledPolicy, compiling each ExtraPatterns rule exactly
// once and collecting any invalid-pattern errors so they can be surfaced once
// at compile time rather than swallowed on every per-value call.
func (p Policy) compile() (compiledPolicy, []error) {
	cp := compiledPolicy{policy: p}
	var errs []error
	for _, rule := range p.ExtraPatterns {
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			errs = append(errs, fmt.Errorf("scrub: invalid extra pattern %q: %w", rule.Pattern, err))
			continue
		}
		replacement := rule.Replacement
		if replacement == "" {
			replacement = Redacted
		}
		cp.rules = append(cp.rules, compiledRule{pattern: re, replacement: replacement})
	}
	return cp, errs
}

// SensitiveHeader reports whether a header is always scrubbed under the policy.
func (p Policy) SensitiveHeader(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if sensitiveHeaders[lower] {
		return true
	}
	if !p.KeepCookies && cookieHeaders[lower] {
		return true
	}
	for _, extra := range p.ExtraHeaders {
		if strings.EqualFold(strings.TrimSpace(extra), lower) {
			return true
		}
	}
	return false
}

// Headers returns a copy of the map with sensitive headers redacted.
func Headers(headers map[string]string, policy Policy) map[string]string {
	if len(headers) == 0 {
		return headers
	}
	cp, _ := policy.compile()
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		if policy.SensitiveHeader(key) {
			out[key] = Redacted
			continue
		}
		out[key] = cp.value(value)
	}
	return out
}

// Value applies the built-in body/query token patterns and the policy's custom
// rules to a string. ExtraPatterns are compiled per call; for hot paths that
// scrub many values under one policy (Headers, HAR), the policy is compiled
// once and reused. Invalid custom patterns are surfaced via Policy.Compile.
func Value(value string, policy Policy) string {
	if value == "" {
		return value
	}
	cp, _ := policy.compile()
	return cp.value(value)
}

// value applies the built-in body/query token patterns and the policy's
// pre-compiled custom rules to a string, reusing the compiled regexps.
func (cp compiledPolicy) value(value string) string {
	if value == "" {
		return value
	}
	if redacted, ok := redactSensitiveJSON(value); ok {
		value = redacted
	}
	value = sensitiveJSONPattern.ReplaceAllString(value, "${1}\""+Redacted+"\"")
	value = sensitiveParamPattern.ReplaceAllString(value, "${1}"+Redacted)
	value = sensitiveXMLPattern.ReplaceAllString(value, "${1}"+Redacted+"${3}")
	value = sensitiveXMLAttr.ReplaceAllString(value, "${1}"+Redacted+"${3}")
	value = multipartSecretPart.ReplaceAllString(value, "${1}"+Redacted+"${3}")
	value = jwtPattern.ReplaceAllString(value, Redacted)
	value = highEntropyToken.ReplaceAllStringFunc(value, func(candidate string) string {
		if looksSensitiveToken(candidate) {
			return Redacted
		}
		return candidate
	})
	for _, rule := range cp.rules {
		value = rule.pattern.ReplaceAllString(value, rule.replacement)
	}
	return value
}

func redactSensitiveJSON(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
		return value, false
	}
	var parsed any
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&parsed); err != nil {
		return value, false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return value, false
	}
	if !redactSensitiveJSONNode(parsed) {
		return value, false
	}
	out, err := json.Marshal(parsed)
	if err != nil {
		return value, false
	}
	return string(out), true
}

func redactSensitiveJSONNode(node any) bool {
	changed := false
	switch typed := node.(type) {
	case map[string]any:
		for key, value := range typed {
			if sensitiveJSONFieldName(key) {
				typed[key] = Redacted
				changed = true
				continue
			}
			if redactSensitiveJSONNode(value) {
				changed = true
			}
		}
	case []any:
		for _, item := range typed {
			if redactSensitiveJSONNode(item) {
				changed = true
			}
		}
	}
	return changed
}

var sensitiveJSONFieldNames = map[string]bool{
	"authorization": true,
	"password":      true,
	"passwd":        true,
	"secret":        true,
	"token":         true,
	"accesstoken":   true,
	"refreshtoken":  true,
	"idtoken":       true,
	"apikey":        true,
	"clientsecret":  true,
	"privatekey":    true,
}

func sensitiveJSONFieldName(name string) bool {
	normalized := strings.ToLower(strings.TrimSpace(name))
	normalized = strings.ReplaceAll(normalized, "_", "")
	normalized = strings.ReplaceAll(normalized, "-", "")
	return sensitiveJSONFieldNames[normalized]
}

// HAR scrubs a HAR document: request/response headers and cookies, post data,
// query strings, and URLs across every entry, plus response bodies via the
// built-in and custom patterns. Unknown HAR fields are preserved verbatim.
// The returned document is always re-marshaled; the input is not modified.
//
// Invalid ExtraPatterns are skipped (the built-in protections still apply);
// callers that need to surface a typo'd custom pattern should use HARWithErrors.
func HAR(data []byte, policy Policy) ([]byte, error) {
	out, _, err := HARWithErrors(data, policy)
	return out, err
}

// HARWithErrors is HAR plus the invalid-ExtraPatterns errors collected once at
// policy-compile time, so an operator with a typo'd PII rule gets feedback
// instead of silently believing data is redacted. patternErrs is non-nil only
// when a custom pattern failed to compile; the built-in redaction always runs.
func HARWithErrors(data []byte, policy Policy) (scrubbed []byte, patternErrs []error, err error) {
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("parse HAR for scrubbing: %w", err)
	}
	log, _ := doc["log"].(map[string]any)
	singleEntry := false
	if log == nil {
		if _, hasRequest := doc["request"]; hasRequest {
			log = map[string]any{"entries": []any{doc}}
			singleEntry = true
		}
	}
	if log == nil {
		return nil, nil, fmt.Errorf("HAR document has no log section")
	}
	cp, patternErrs := policy.compile()
	entries, _ := log["entries"].([]any)
	for _, rawEntry := range entries {
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			continue
		}
		cp.scrubHAREntry(entry)
	}
	if singleEntry {
		doc["_apexloadSensitivity"] = SensitivityScrubbed
	} else {
		annotateScrubbed(log)
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, patternErrs, err
	}
	return out, patternErrs, nil
}

func (cp compiledPolicy) scrubHAREntry(entry map[string]any) {
	if request, ok := entry["request"].(map[string]any); ok {
		cp.scrubNameValueList(request, "headers")
		cp.scrubNameValueList(request, "cookies")
		cp.scrubNameValueList(request, "queryString")
		if rawURL, ok := request["url"].(string); ok {
			request["url"] = cp.value(rawURL)
		}
		if postData, ok := request["postData"].(map[string]any); ok {
			if text, ok := postData["text"].(string); ok {
				postData["text"] = cp.value(text)
			}
			cp.scrubNameValueList(postData, "params")
		}
	}
	if response, ok := entry["response"].(map[string]any); ok {
		cp.scrubNameValueList(response, "headers")
		cp.scrubNameValueList(response, "cookies")
		if content, ok := response["content"].(map[string]any); ok {
			if text, ok := content["text"].(string); ok {
				content["text"] = cp.value(text)
			}
		}
	}
	cp.scrubWebSocketMessages(entry, "_webSocketMessages")
	cp.scrubWebSocketMessages(entry, "websocket_messages")
}

// scrubNameValueList handles HAR's [{name, value}] collections. Cookie
// collections are fully redacted; for headers only sensitive names are
// redacted; query/post params are redacted when the name matches the
// sensitive-parameter set. It reuses the policy's pre-compiled custom rules.
func (cp compiledPolicy) scrubNameValueList(parent map[string]any, key string) {
	items, _ := parent[key].([]any)
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		name, _ := item["name"].(string)
		value, _ := item["value"].(string)
		switch key {
		case "cookies":
			if !cp.policy.KeepCookies {
				item["value"] = Redacted
			}
		case "headers":
			if cp.policy.SensitiveHeader(name) {
				item["value"] = Redacted
			} else {
				item["value"] = cp.value(value)
			}
		default: // queryString, params
			if sensitiveParamName(name) {
				item["value"] = Redacted
			} else {
				item["value"] = cp.value(value)
			}
		}
	}
}

var sensitiveParamNames = map[string]bool{
	"password": true, "passwd": true, "secret": true, "token": true,
	"access_token": true, "refresh_token": true, "id_token": true,
	"api_key": true, "apikey": true, "session": true, "session_id": true,
	"sessionid": true, "auth": true, "csrf": true, "xsrf": true,
	"client_secret": true, "csrf_token": true,
}

func sensitiveParamName(name string) bool {
	return sensitiveParamNames[strings.ToLower(strings.TrimSpace(name))]
}

func (cp compiledPolicy) scrubWebSocketMessages(entry map[string]any, key string) {
	messages, _ := entry[key].([]any)
	for _, rawMessage := range messages {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			continue
		}
		if data, ok := message["data"].(string); ok {
			message["data"] = cp.value(data)
		}
		if payload, ok := message["payload"].(string); ok {
			message["payload"] = cp.value(payload)
		}
	}
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

// Sensitivity labels for artifact metadata, surfaced in API/UI responses so
// reviewers know whether an artifact passed through scrub-on-write.
const (
	SensitivityScrubbed = "scrubbed"
	SensitivityRaw      = "raw_contains_secrets"
)

// annotateScrubbed marks the HAR log so consumers can verify the artifact
// passed through scrub-on-write.
func annotateScrubbed(log map[string]any) {
	log["_apexloadSensitivity"] = SensitivityScrubbed
}

// HARSensitivity reads the sensitivity label from a HAR document, returning
// SensitivityRaw when the artifact never passed through scrub-on-write.
func HARSensitivity(data []byte) string {
	var doc struct {
		Sensitivity string `json:"_apexloadSensitivity"`
		Log         struct {
			Sensitivity string `json:"_apexloadSensitivity"`
		} `json:"log"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return SensitivityRaw
	}
	if doc.Log.Sensitivity == SensitivityScrubbed || doc.Sensitivity == SensitivityScrubbed {
		return SensitivityScrubbed
	}
	return SensitivityRaw
}
