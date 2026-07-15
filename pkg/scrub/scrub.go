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
	"net/url"
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

// sensitiveJSONPattern redacts token-bearing JSON body fields. Correlation-time
// replay reconstruction runs against the raw server-side recording sidecar and
// the raw import content (not this scrubbed view), so session/CSRF tokens — live
// credentials that were previously left un-redacted here for correlation — are
// now redacted like any other secret. The structural redactor
// (redactSensitiveJSON / sensitiveJSONFieldNames) handles valid JSON; this regex
// is the fallback for JSON-ish bodies that do not parse strictly.
var (
	sensitiveJSONPattern = regexp.MustCompile(`(?i)("(?:authorization|password|passwd|secret|token|access[_-]?token|refresh[_-]?token|id[_-]?token|api[_-]?key|apikey|client[_-]?secret|private[_-]?key|session[_-]?id|session|csrf[_-]?token|csrf|xsrf[_-]?token|xsrf)"\s*:\s*)("[^"]*"|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?|true|false|null)`)
	encodedParamPattern  = regexp.MustCompile(`(?i)(^|[&?;\s])([A-Za-z0-9_.%+\[\]-]+)=([^&\s;#"']+)`)
	sensitiveXMLPattern  = regexp.MustCompile(`(?is)(<(?:[\w.-]+:)?(?:authorization|password|passwd|secret|token|access[_-]?token|refresh[_-]?token|id[_-]?token|api[_-]?key|apikey|client[_-]?secret|private[_-]?key|connection[_-]?string|account[_-]?key|session(?:[_-]?id|[_-]?token)?|csrf(?:[_-]?token)?|xsrf(?:[_-]?token)?|settlement[_-]?token|lock[_-]?token|receipt[_-]?handle|delivery[_-]?tag)\b[^>]*>)(.*?)(</(?:[\w.-]+:)?(?:authorization|password|passwd|secret|token|access[_-]?token|refresh[_-]?token|id[_-]?token|api[_-]?key|apikey|client[_-]?secret|private[_-]?key|connection[_-]?string|account[_-]?key|session(?:[_-]?id|[_-]?token)?|csrf(?:[_-]?token)?|xsrf(?:[_-]?token)?|settlement[_-]?token|lock[_-]?token|receipt[_-]?handle|delivery[_-]?tag)>)`)
	sensitiveXMLAttr     = regexp.MustCompile(`(?is)(^|[^\w-])((?:authorization|password|passwd|secret|token|access[_-]?token|refresh[_-]?token|id[_-]?token|api[_-]?key|apikey|client[_-]?secret|private[_-]?key|connection[_-]?string|account[_-]?key|session(?:[_-]?id|[_-]?token)?|csrf(?:[_-]?token)?|xsrf(?:[_-]?token)?|settlement[_-]?token|lock[_-]?token|receipt[_-]?handle|delivery[_-]?tag)\s*=\s*")([^"]*)(")`)
	multipartPartPattern = regexp.MustCompile(`(?is)(Content-Disposition:\s*form-data;[^\r\n]*\bname="([^"]+)"[^\r\n]*(?:\r?\nContent-Type:[^\r\n]*)?\r?\n\r?\n)(.*?)(\r?\n--)`)
	namedSecretText      = regexp.MustCompile(`(?i)\b((?:jms[_-]?)?settlement[_-]?token|lock[_-]?token|receipt[_-]?handle|delivery[_-]?tag|session[_-]?token|csrf[_-]?token|xsrf[_-]?token)(\s*(?:=|:)\s*|\s+)(["']?)([^\s,;}\]"']+)(["']?)`)
	jwtPattern           = regexp.MustCompile(`\b[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	highEntropyToken     = regexp.MustCompile(`\b[A-Za-z0-9+/=_-]{24,}\b`)
	// embeddedURLPattern lets Value redact URL/DSN userinfo inside error and log
	// prose, not only when the whole input is a URL. Quotes and whitespace are
	// excluded so common net/http errors retain their surrounding syntax.
	embeddedURLPattern = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]{1,15}://[^\s<>"']+`)
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
	if !cookieHeaders[lower] && SensitiveName(name) {
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

// ValueURL scrubs a URL string: sensitive query parameters and custom rules are
// applied, but the blanket JWT/high-entropy heuristics are skipped because they
// corrupt hostnames and content-hashed paths (see compiledPolicy.valueURL).
func ValueURL(value string, policy Policy) string {
	if value == "" {
		return value
	}
	cp, _ := policy.compile()
	return cp.valueURL(value)
}

// value applies the built-in body/query token patterns and the policy's
// pre-compiled custom rules to a string, reusing the compiled regexps.
func (cp compiledPolicy) value(value string) string {
	if value == "" {
		return value
	}
	value = embeddedURLPattern.ReplaceAllStringFunc(value, func(candidate string) string {
		return cp.valueURL(candidate)
	})
	if redacted, ok := redactSensitiveJSON(value); ok {
		value = redacted
	}
	value = sensitiveJSONPattern.ReplaceAllString(value, "${1}\""+Redacted+"\"")
	value = sensitiveParamPattern.ReplaceAllString(value, "${1}"+Redacted)
	value = encodedParamPattern.ReplaceAllStringFunc(value, func(parameter string) string {
		matches := encodedParamPattern.FindStringSubmatch(parameter)
		if len(matches) != 4 || !SensitiveName(matches[2]) {
			return parameter
		}
		return matches[1] + matches[2] + "=" + Redacted
	})
	value = sensitiveXMLPattern.ReplaceAllString(value, "${1}"+Redacted+"${3}")
	value = sensitiveXMLAttr.ReplaceAllString(value, "${1}${2}"+Redacted+"${4}")
	value = multipartPartPattern.ReplaceAllStringFunc(value, func(part string) string {
		matches := multipartPartPattern.FindStringSubmatch(part)
		if len(matches) != 5 || !SensitiveName(matches[2]) {
			return part
		}
		return matches[1] + Redacted + matches[4]
	})
	value = namedSecretText.ReplaceAllString(value, "${1}${2}${3}"+Redacted+"${5}")
	value = redactBareTokens(value)
	for _, rule := range cp.rules {
		value = rule.pattern.ReplaceAllString(value, rule.replacement)
	}
	return value
}

// valueURL scrubs a URL: it redacts sensitive query parameters and applies
// custom rules, but deliberately skips the blanket JWT/high-entropy heuristics.
// Those match structurally on any three 8+-char dot-separated labels (every
// multi-label hostname) and on any 24+-char base64-ish run (content-hashed
// asset paths), so running them over a URL corrupts the host/path — replacing
// the request URL itself with [REDACTED] and breaking HAR re-import — without
// catching anything the query-param redaction does not already handle.
func (cp compiledPolicy) valueURL(value string) string {
	if value == "" {
		return value
	}
	// Split scheme/host/path from the query and fragment by hand (NOT url.Parse +
	// String(), which would re-encode/normalize parts a token-redaction touched).
	// Query/fragment param VALUES are redacted individually so the param NAME is
	// never swallowed and percent-escapes are decoded before the credential-shape
	// check, while scheme/host/path — where multi-label hosts and content-hashed
	// asset filenames live — are left byte-for-byte intact.
	base, fragment, hasFragment := strings.Cut(value, "#")
	path, query, hasQuery := strings.Cut(base, "?")
	out := redactURLUserinfo(path)
	if hasQuery {
		out += "?" + redactURLParams(query)
	}
	if hasFragment {
		out += "#" + redactURLParams(fragment)
	}
	out = sensitiveParamPattern.ReplaceAllString(out, "${1}"+Redacted)
	for _, rule := range cp.rules {
		out = rule.pattern.ReplaceAllString(out, rule.replacement)
	}
	return out
}

// redactURLUserinfo removes credentials from the authority without parsing and
// re-serializing the rest of the URL (which would normalize evidence paths).
func redactURLUserinfo(path string) string {
	schemeEnd := strings.Index(path, "://")
	if schemeEnd < 0 {
		return path
	}
	authorityStart := schemeEnd + 3
	authorityEnd := len(path)
	if offset := strings.IndexAny(path[authorityStart:], "/\\"); offset >= 0 {
		authorityEnd = authorityStart + offset
	}
	authority := path[authorityStart:authorityEnd]
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return path
	}
	userinfo := authority[:at]
	replacement := Redacted
	if strings.Contains(userinfo, ":") {
		replacement += ":" + Redacted
	}
	return path[:authorityStart] + replacement + "@" + authority[at+1:] + path[authorityEnd:]
}

// redactURLParams redacts secret-shaped VALUES in a URL query or fragment segment
// (key=value pairs joined by '&'). The param name is preserved; only the value is
// replaced. Values under a recognized sensitive param name are always redacted;
// otherwise the DECODED value (percent-escapes resolved, so an encoded signature
// is still caught) is checked with the credential-shape heuristics. Bare segments
// with no '=' are treated as a single opaque token.
func redactURLParams(segment string) string {
	if segment == "" {
		return segment
	}
	parts := strings.Split(segment, "&")
	for i, part := range parts {
		key, val, hasEq := strings.Cut(part, "=")
		if !hasEq {
			if urlTokenLooksSensitive(part) {
				parts[i] = Redacted
			}
			continue
		}
		if SensitiveName(key) || urlTokenLooksSensitive(val) {
			parts[i] = key + "=" + Redacted
		}
	}
	return strings.Join(parts, "&")
}

// urlTokenLooksSensitive reports whether a URL query value or bare segment is
// credential-shaped, checking the percent-decoded form so encoded secrets are not
// missed. It reuses the same gated heuristics as bare-token body redaction, so a
// plain low-entropy value (a page number, a country code) is preserved.
func urlTokenLooksSensitive(token string) bool {
	if token == "" {
		return false
	}
	decoded := token
	if unescaped, err := url.QueryUnescape(token); err == nil {
		decoded = unescaped
	}
	return looksLikeJWT(decoded) || looksSensitiveToken(decoded)
}

// redactBareTokens redacts JWTs and high-entropy secret-shaped runs that appear
// as bare values (not inside a recognized named field). Both are gated on a
// credential-shape heuristic so structural look-alikes — plain lowercase
// hostnames like dashboard.internal.example (JWT shape) and low-entropy IDs —
// are preserved, keeping scrubbed artifacts usable as evidence and re-import
// inputs while still removing real tokens.
func redactBareTokens(value string) string {
	value = jwtPattern.ReplaceAllStringFunc(value, func(candidate string) string {
		if looksLikeJWT(candidate) {
			return Redacted
		}
		return candidate
	})
	return highEntropyToken.ReplaceAllStringFunc(value, func(candidate string) string {
		if looksSensitiveToken(candidate) {
			return Redacted
		}
		return candidate
	})
}

// looksLikeJWT reports whether a three-segment dotted match is a real JWT rather
// than a coincidental hostname. Real JWTs are base64url encodings of JSON, so
// the concatenated segments carry mixed case and digits at high entropy; a plain
// hostname (all lowercase, no digits) does not, so it is preserved.
func looksLikeJWT(candidate string) bool {
	return looksSensitiveToken(strings.ReplaceAll(candidate, ".", ""))
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
			if SensitiveName(key) {
				redacted, valueChanged := redactSensitiveJSONSecretValue(value)
				if valueChanged {
					typed[key] = redacted
					changed = true
				}
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

func redactSensitiveJSONSecretValue(node any) (any, bool) {
	switch typed := node.(type) {
	case string:
		if typed == Redacted {
			return typed, false
		}
		return Redacted, true
	case map[string]any:
		changed := false
		for key, value := range typed {
			redacted, valueChanged := redactSensitiveJSONSecretValue(value)
			if valueChanged {
				typed[key] = redacted
				changed = true
			}
		}
		return typed, changed
	case []any:
		changed := false
		for i, item := range typed {
			redacted, valueChanged := redactSensitiveJSONSecretValue(item)
			if valueChanged {
				typed[i] = redacted
				changed = true
			}
		}
		return typed, changed
	default:
		return Redacted, true
	}
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
			request["url"] = cp.valueURL(rawURL)
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
			if SensitiveName(name) {
				item["value"] = Redacted
			} else {
				item["value"] = cp.value(value)
			}
		}
	}
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
