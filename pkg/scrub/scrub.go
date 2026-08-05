// Package scrub is the shared scrub-on-write component for captured traffic
// artifacts. Recorder sessions, runtime canary HAR exports, and any other
// HAR-with-bodies artifact pass through it before persistence so credentials,
// session tokens, and configured PII patterns never reach disk. Unlike
// design-time parameterization (pkg/convert/paramize), which replaces secrets
// with runnable templates, scrubbing is irreversible redaction.
package scrub

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/credential"
)

// Redacted replaces every scrubbed value.
const Redacted = credential.Redacted

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

// sensitiveParamPattern redacts token-bearing query string and form fields.
var sensitiveParamPattern = regexp.MustCompile(`(?i)((?:^|[&?;\s])(?:password|passwd|secret|token|access_token|refresh_token|id_token|api_key|apikey|session|session_id|sessionid|auth|csrf|xsrf|client_secret|private_key)=)([^&\s;#"']+)`)

// jsonFallbackFieldPattern redacts token-bearing JSON body fields. Correlation-time
// replay reconstruction runs against the raw server-side recording sidecar and
// the raw import content (not this scrubbed view), so session/CSRF tokens — live
// credentials that were previously left un-redacted here for correlation — are
// now redacted like any other secret. The structural redactor
// (redactSensitiveJSON) handles valid JSON; this generic regex is the fallback
// for JSON-ish bodies that do not parse strictly. Its field names are classified
// with credential.SensitiveName instead of maintaining a second vocabulary.
var (
	jsonFallbackFieldPattern = regexp.MustCompile(`(?is)("((?:\\.|[^"\\])*)"\s*:\s*)("(?:\\.|[^"\\])*"|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?|true|false|null)(\s*(?:[,}\]]|$))`)
	jsonStringFieldStart     = regexp.MustCompile(`(?is)("((?:\\.|[^"\\])*)"\s*:\s*")`)
	jsonAnyFieldStart        = regexp.MustCompile(`(?is)("((?:\\.|[^"\\])*)"\s*:\s*)`)
	jsonFieldAtStart         = regexp.MustCompile(`(?is)^\s*"(?:\\.|[^"\\])*"\s*:`)
	jsonFallbackScalar       = regexp.MustCompile(`^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$`)
	encodedParamPattern      = regexp.MustCompile(`(?i)(^|[&?;\s])([A-Za-z0-9_.%+\[\]-]+)=([^&\s;#"']+)`)
	xmlNamespaceDoubleAttr   = regexp.MustCompile(`(?is)(^|[[:space:]])(xmlns(?::[^\s=]+)?\s*=\s*")([^"]*)(")`)
	xmlNamespaceSingleAttr   = regexp.MustCompile(`(?is)(^|[[:space:]])(xmlns(?::[^\s=]+)?\s*=\s*')([^']*)(')`)
	multipartNameFallback    = regexp.MustCompile(`(?is)(?:^|;)\s*name\s*=\s*(?:"([^"\r\n;]*)|'([^'\r\n;]*)|([^;\s\r\n]+))`)
	namedSecretText          = regexp.MustCompile(`(?i)\b((?:jms[_-]?)?settlement[_-]?token|lock[_-]?token|receipt[_-]?handle|delivery[_-]?tag|session[_-]?token|csrf[_-]?token|xsrf[_-]?token)(\s*(?:=|:)\s*|\s+)(["']?)([^\s,;}\]"']+)(["']?)`)
	// opaqueDSNCredential covers driver DSNs whose credentials are not in URL
	// userinfo, so neither embeddedURLPattern nor the name=value matchers see
	// them. The Oracle thin form "jdbc:oracle:thin:user/password@//host:port/sid"
	// is the common one; JMeter JDBC samplers and connection-failure messages
	// carry it verbatim. Anchoring on the jdbc: scheme keeps ordinary text such
	// as "a/b@c.example" untouched.
	opaqueDSNCredential = regexp.MustCompile(`(?i)(\bjdbc:[a-z0-9+.-]+:(?:[a-z0-9+.-]+:)*[^\s/"'@]+/)([^\s/"'@]+)(@)`)
	jwtPattern          = regexp.MustCompile(`\b[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	highEntropyToken    = regexp.MustCompile(`\b[A-Za-z0-9+/=_-]{24,}\b`)
	// embeddedURLPattern lets Value redact URL/DSN userinfo inside error and log
	// prose, not only when the whole input is a URL. Quotes and whitespace are
	// excluded so common net/http errors retain their surrounding syntax.
	embeddedURLPattern     = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]{1,15}://[^\s<>"']+`)
	embeddedEscapedHTTPURL = regexp.MustCompile(`(?i)\bhttps?:(?:\\/){2}[^\s<>"']+`)
)

const maxNestedURLSanitizeDepth = 4

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
	if credential.AlwaysSensitiveHeader(lower) {
		return true
	}
	if !p.KeepCookies && credential.CookieHeader(lower) {
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
	value, restoreURLs := cp.protectEmbeddedURLs(value)
	redactedJSON, jsonChanged, validJSON := redactSensitiveJSON(value)
	if jsonChanged {
		value = redactedJSON
	}
	if !validJSON {
		value = redactSensitiveJSONFallback(value)
	}
	// Scrub XML before generic name=value patterns. Invalid unquoted XML
	// attributes otherwise let the generic matcher consume structural `/>`
	// bytes along with the value. The marker guards below keep the already
	// classified XML span and its surrounding syntax intact.
	value = redactXMLFields(value, credential.SensitiveXMLName)
	value = sensitiveParamPattern.ReplaceAllStringFunc(value, func(parameter string) string {
		matches := sensitiveParamPattern.FindStringSubmatch(parameter)
		if len(matches) != 3 || strings.HasPrefix(matches[2], Redacted) {
			return parameter
		}
		return matches[1] + Redacted
	})
	value = encodedParamPattern.ReplaceAllStringFunc(value, func(parameter string) string {
		matches := encodedParamPattern.FindStringSubmatch(parameter)
		if len(matches) != 4 || !SensitiveName(matches[2]) ||
			strings.HasPrefix(matches[3], Redacted) {
			return parameter
		}
		return matches[1] + matches[2] + "=" + Redacted
	})
	value = redactMultipartFields(value, SensitiveName)
	value = opaqueDSNCredential.ReplaceAllString(value, "${1}"+Redacted+"${3}")
	value = namedSecretText.ReplaceAllString(value, "${1}${2}${3}"+Redacted+"${5}")
	value = redactBareTokens(value)
	value = restoreURLs(value)
	// Run custom rules against the restored whole value. A rule may deliberately
	// include context immediately before an URL (for example
	// `callback=https://...`), which cannot match while the URL is represented by
	// an internal placeholder. valueURL already applies the same rules inside the
	// URL span, matching the historical two-pass behavior.
	for _, rule := range cp.rules {
		value = rule.pattern.ReplaceAllString(value, rule.replacement)
	}
	return value
}

// protectEmbeddedURLs keeps safe URL structure out of the generic bare-token
// pass. URL credentials and sensitive query/fragment values are scrubbed before
// protection; the placeholders are JSON/XML/text-safe and selected so they
// cannot collide with the input.
func (cp compiledPolicy) protectEmbeddedURLs(value string) (string, func(string) string) {
	if !embeddedURLPattern.MatchString(value) &&
		!embeddedEscapedHTTPURL.MatchString(value) &&
		!xmlNamespaceDoubleAttr.MatchString(value) &&
		!xmlNamespaceSingleAttr.MatchString(value) {
		return value, func(result string) string { return result }
	}

	// A digest-derived, separator-grouped prefix is effectively collision-free
	// for this input without the repeated full-string scans of the old
	// grow-until-unique loop. The short hyphen-separated groups also keep the
	// internal marker out of the generic high-entropy-token detector.
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
	var prefixBuilder strings.Builder
	prefixBuilder.WriteString("APEXURL~")
	for offset := 0; offset < len(digest); offset += 4 {
		prefixBuilder.WriteString(digest[offset : offset+4])
		prefixBuilder.WriteByte('~')
	}
	prefix := prefixBuilder.String()
	for strings.Contains(value, prefix) {
		// The digest makes a collision vanishingly unlikely, while this bounded
		// grow-until-absent check makes the placeholder contract deterministic:
		// no literal input span can ever be mistaken for an internal marker.
		prefix += "~"
	}
	var protected []string
	protectValue := func(candidate string) string {
		placeholder := prefix + strconv.Itoa(len(protected)) + "Z"
		protected = append(protected, candidate)
		return placeholder
	}
	protectNamespaceValues := func(input string, pattern *regexp.Regexp) string {
		return pattern.ReplaceAllStringFunc(input, func(attribute string) string {
			match := pattern.FindStringSubmatchIndex(attribute)
			if len(match) < 10 {
				return attribute
			}
			uriStart, uriEnd := match[6], match[7]
			sanitized := cp.valueURL(attribute[uriStart:uriEnd])
			return attribute[:uriStart] + protectValue(sanitized) + attribute[uriEnd:]
		})
	}
	// Namespace identifiers are URIs, but many valid forms (notably urn:...)
	// do not contain :// and therefore are not found by embeddedURLPattern.
	// Protect their values before generic URL replacement so the placeholders
	// never nest and every safe namespace value survives the bare-token pass.
	value = protectNamespaceValues(value, xmlNamespaceDoubleAttr)
	value = protectNamespaceValues(value, xmlNamespaceSingleAttr)
	value = embeddedEscapedHTTPURL.ReplaceAllStringFunc(value, func(candidate string) string {
		var decoded string
		err := json.Unmarshal([]byte(`"`+candidate+`"`), &decoded)
		if err != nil ||
			(!strings.HasPrefix(strings.ToLower(decoded), "http://") &&
				!strings.HasPrefix(strings.ToLower(decoded), "https://")) {
			return candidate
		}
		sanitized := cp.valueURL(decoded)
		if sanitized == decoded {
			return protectValue(candidate)
		}
		quoted := strconv.Quote(sanitized)
		escaped := quoted[1 : len(quoted)-1]
		if strings.Contains(candidate, `\/`) {
			escaped = strings.ReplaceAll(escaped, `/`, `\/`)
		}
		return protectValue(escaped)
	})
	value = embeddedURLPattern.ReplaceAllStringFunc(value, func(candidate string) string {
		sanitized := cp.valueURL(candidate)
		lower := strings.ToLower(candidate)
		if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
			// Non-HTTP schemes may carry opaque credentials in their path. Sanitize
			// their URL components, but leave the span visible to the generic
			// high-entropy/JWT pass instead of granting the HTTP(S) safe-path
			// exemption.
			return sanitized
		}
		return protectValue(sanitized)
	})
	restorePattern := regexp.MustCompile(regexp.QuoteMeta(prefix) + `[0-9]+Z`)
	return value, func(result string) string {
		return restorePattern.ReplaceAllStringFunc(result, func(placeholder string) string {
			indexText := strings.TrimSuffix(strings.TrimPrefix(placeholder, prefix), "Z")
			index, err := strconv.Atoi(indexText)
			if err != nil || index < 0 || index >= len(protected) {
				return placeholder
			}
			return protected[index]
		})
	}
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
	out := redactURLBuiltIns(value, 0)
	for _, rule := range cp.rules {
		out = rule.pattern.ReplaceAllString(out, rule.replacement)
	}
	return out
}

func redactURLBuiltIns(value string, depth int) string {
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
		out += "?" + redactURLParamsDepth(query, depth)
	}
	if hasFragment {
		out += "#" + redactURLParamsDepth(fragment, depth)
	}
	// Rescan every query-like assignment rather than relying only on the outer
	// key/value split. A malformed percent escape can corrupt a nested URL's
	// scheme prefix (h%ZZttps://...) while its own ?password= query remains
	// recognizable and must still fail closed.
	return redactSensitiveQueryAssignments(out, SensitiveName)
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
	return redactURLParamsDepth(segment, 0)
}

func redactURLParamsDepth(segment string, depth int) string {
	if segment == "" {
		return segment
	}
	var out strings.Builder
	out.Grow(len(segment))
	for len(segment) > 0 {
		separator := strings.IndexAny(segment, "&;")
		part := segment
		delimiter := ""
		if separator >= 0 {
			part = segment[:separator]
			delimiter = segment[separator : separator+1]
			segment = segment[separator+1:]
		} else {
			segment = ""
		}
		key, val, hasEq := strings.Cut(part, "=")
		if !hasEq {
			if urlTokenLooksSensitive(part) {
				part = Redacted
			}
		} else if SensitiveName(key) {
			part = key + "=" + Redacted
		} else if nested, changed := sanitizeNestedHTTPURL(val, depth); changed {
			part = key + "=" + nested
		} else if urlTokenLooksSensitive(val) {
			part = key + "=" + Redacted
		}
		out.WriteString(part)
		out.WriteString(delimiter)
	}
	return out.String()
}

type rawDecodedByte struct {
	start int
	end   int
}

func sanitizeNestedHTTPURL(raw string, depth int) (string, bool) {
	decoded, spans, ok := queryUnescapeWithSpans(raw)
	if !ok {
		// Once a query value has established an HTTP(S) prefix, a malformed
		// percent escape later in that value must not disable recursive
		// sanitization. We cannot safely determine which nested component the
		// malformed bytes belong to, so fail closed for the whole nested URL.
		if rawBeginsHTTPURL(raw) {
			return url.QueryEscape(Redacted), true
		}
		return raw, false
	}
	lower := strings.ToLower(decoded)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return raw, false
	}

	sanitized := Redacted
	if depth < maxNestedURLSanitizeDepth {
		sanitized = redactURLBuiltIns(decoded, depth+1)
	}
	if sanitized == decoded {
		return raw, false
	}
	rewritten, ok := rewriteDecodedURLPreservingEscapes(raw, decoded, sanitized, spans)
	if !ok {
		// The decoded value is already known to contain an HTTP(S) URL and the
		// sanitizer changed it. If alignment ever fails, fail closed instead of
		// returning the original nested credential.
		return url.QueryEscape(Redacted), true
	}
	return rewritten, true
}

// rawBeginsHTTPURL recognizes a literal, encoded, or mixed-encoded HTTP(S)
// scheme without decoding beyond the prefix. This lets callers establish that
// an otherwise malformed query value is a nested URL while avoiding any claim
// that the remainder was decoded successfully.
func rawBeginsHTTPURL(raw string) bool {
	const longestScheme = len("https://")
	decoded := make([]byte, 0, longestScheme)
	for index := 0; index < len(raw) && len(decoded) < longestScheme; {
		switch raw[index] {
		case '%':
			if index+2 >= len(raw) {
				return false
			}
			high, highOK := fromHex(raw[index+1])
			low, lowOK := fromHex(raw[index+2])
			if !highOK || !lowOK {
				return false
			}
			decoded = append(decoded, high<<4|low)
			index += 3
		case '+':
			decoded = append(decoded, ' ')
			index++
		default:
			decoded = append(decoded, raw[index])
			index++
		}

		prefix := strings.ToLower(string(decoded))
		if prefix == "http://" || prefix == "https://" {
			return true
		}
		if !strings.HasPrefix("http://", prefix) && !strings.HasPrefix("https://", prefix) {
			return false
		}
	}
	return false
}

// redactSensitiveQueryAssignments finds sensitive name=value pairs at every
// query-like delimiter, including pairs nested inside a nonsensitive outer
// value. It intentionally does not skip an outer value as one opaque match, so
// a malformed nested URL cannot hide a later ?password= assignment.
func redactSensitiveQueryAssignments(value string, sensitiveName func(string) bool) string {
	if value == "" || sensitiveName == nil || !strings.Contains(value, "=") {
		return value
	}
	spans := make([]redactionSpan, 0, 1)
	for index := 0; index < len(value); index++ {
		if index > 0 && !isQueryAssignmentDelimiter(value[index-1]) {
			continue
		}
		keyStart := index
		for index < len(value) && isQueryAssignmentKeyByte(value[index]) {
			index++
		}
		if index == keyStart || index >= len(value) || value[index] != '=' {
			continue
		}
		rawKey := value[keyStart:index]
		key := rawKey
		if decoded, err := url.QueryUnescape(rawKey); err == nil {
			key = decoded
		}
		if !sensitiveName(key) {
			continue
		}
		valueStart := index + 1
		if valueStart >= len(value) || isQueryValueDelimiter(value[valueStart]) {
			continue
		}
		valueEnd := valueStart
		for valueEnd < len(value) && !isQueryValueDelimiter(value[valueEnd]) {
			valueEnd++
		}
		// When this generic nested-query pass follows XML redaction, an invalid
		// unquoted attribute can look like password=[REDACTED]/>. The trailing
		// slash is known to be the XML self-close delimiter only when the complete
		// preceding value was already scrubbed. Never infer that role from an
		// arbitrary credential suffix: for password=/>, the slash is the complete
		// credential value and must be redacted.
		if valueEnd > valueStart && valueEnd < len(value) &&
			value[valueEnd] == '>' && value[valueEnd-1] == '/' &&
			value[valueStart:valueEnd-1] == Redacted {
			continue
		}
		if value[valueStart:valueEnd] == Redacted {
			continue
		}
		spans = append(spans, redactionSpan{start: valueStart, end: valueEnd})
	}
	return applyRedactionSpans(value, spans)
}

func isQueryAssignmentDelimiter(ch byte) bool {
	switch ch {
	case '?', '#', '&', ';', ' ', '\t', '\r', '\n':
		return true
	default:
		return false
	}
}

func isQueryAssignmentKeyByte(ch byte) bool {
	return (ch >= 'A' && ch <= 'Z') ||
		(ch >= 'a' && ch <= 'z') ||
		(ch >= '0' && ch <= '9') ||
		ch == '_' || ch == '-' || ch == '.' || ch == '%' ||
		ch == '+' || ch == '[' || ch == ']'
}

func isQueryValueDelimiter(ch byte) bool {
	switch ch {
	case '?', '#', '&', ';', ' ', '\t', '\r', '\n', '"', '\'', '<', '>':
		return true
	default:
		return false
	}
}

func queryUnescapeWithSpans(raw string) (string, []rawDecodedByte, bool) {
	var decoded strings.Builder
	decoded.Grow(len(raw))
	spans := make([]rawDecodedByte, 0, len(raw))
	for index := 0; index < len(raw); {
		start := index
		switch raw[index] {
		case '%':
			if index+2 >= len(raw) {
				return "", nil, false
			}
			high, highOK := fromHex(raw[index+1])
			low, lowOK := fromHex(raw[index+2])
			if !highOK || !lowOK {
				return "", nil, false
			}
			decoded.WriteByte(high<<4 | low)
			index += 3
		case '+':
			decoded.WriteByte(' ')
			index++
		default:
			decoded.WriteByte(raw[index])
			index++
		}
		spans = append(spans, rawDecodedByte{start: start, end: index})
	}
	return decoded.String(), spans, true
}

func fromHex(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}

func rewriteDecodedURLPreservingEscapes(
	raw, decoded, sanitized string,
	spans []rawDecodedByte,
) (string, bool) {
	if len(spans) != len(decoded) {
		return "", false
	}
	segments, redactions := splitSanitizedURLRedactions(sanitized)
	if len(redactions) == 0 || !strings.HasPrefix(decoded, segments[0]) {
		return "", false
	}

	rawRange := func(start, end int) string {
		if start >= end {
			return ""
		}
		return raw[spans[start].start:spans[end-1].end]
	}
	var out strings.Builder
	out.Grow(len(raw) + len(redactions)*len(Redacted))
	decodedAt := len(segments[0])
	out.WriteString(rawRange(0, decodedAt))
	for index, redaction := range redactions {
		next := segments[index+1]
		if strings.HasPrefix(decoded[decodedAt:], redaction) {
			// An already-redacted source token is unchanged context, not a new
			// edit. Preserve its original percent-escape spelling byte-for-byte.
			out.WriteString(rawRange(decodedAt, decodedAt+len(redaction)))
			decodedAt += len(redaction)
			if !strings.HasPrefix(decoded[decodedAt:], next) {
				return "", false
			}
			out.WriteString(rawRange(decodedAt, decodedAt+len(next)))
			decodedAt += len(next)
			continue
		}
		nextAt := decodedAt
		if next == "" {
			if index == len(redactions)-1 {
				nextAt = len(decoded)
			}
		} else {
			// Prefer the last viable anchor. Choosing an earlier copy that
			// happens to occur inside the removed credential could retain the
			// tail of that credential; consuming more is the fail-closed choice.
			offset := strings.LastIndex(decoded[decodedAt:], next)
			if offset < 0 {
				return "", false
			}
			nextAt = decodedAt + offset
		}
		out.WriteString(url.QueryEscape(redaction))
		out.WriteString(rawRange(nextAt, nextAt+len(next)))
		decodedAt = nextAt + len(next)
	}
	if decodedAt != len(decoded) {
		return "", false
	}
	return out.String(), true
}

func splitSanitizedURLRedactions(value string) ([]string, []string) {
	variants := make([]string, 0, maxNestedURLSanitizeDepth+2)
	variant := Redacted
	for depth := 0; depth <= maxNestedURLSanitizeDepth+1; depth++ {
		variants = append(variants, variant)
		variant = url.QueryEscape(variant)
	}

	segments := make([]string, 0, 2)
	redactions := make([]string, 0, 1)
	cursor := 0
	for cursor < len(value) {
		nextAt := -1
		nextToken := ""
		for _, candidate := range variants {
			if offset := strings.Index(value[cursor:], candidate); offset >= 0 &&
				(nextAt < 0 || offset < nextAt ||
					(offset == nextAt && len(candidate) > len(nextToken))) {
				nextAt = offset
				nextToken = candidate
			}
		}
		if nextAt < 0 {
			break
		}
		absolute := cursor + nextAt
		segments = append(segments, value[cursor:absolute])
		redactions = append(redactions, nextToken)
		cursor = absolute + len(nextToken)
	}
	if len(redactions) == 0 {
		return nil, nil
	}
	segments = append(segments, value[cursor:])
	return segments, redactions
}

// redactMultipartFields redacts complete part bodies using the exact boundary
// declared by the first multipart delimiter line. A credential part may
// contain arbitrary CRLF-- text; only CRLF--<that-boundary> terminates it.
func redactMultipartFields(body string, sensitiveName func(string) bool) string {
	if body == "" || sensitiveName == nil {
		return body
	}
	boundary, boundaryAt, ok := inferMultipartBoundary(body)
	if !ok {
		return body
	}

	spans := make([]redactionSpan, 0, 1)
	for boundaryAt >= 0 {
		partStart, closing, valid := multipartBoundaryLineEnd(body, boundaryAt, boundary)
		if !valid || closing || partStart >= len(body) {
			break
		}
		nextBoundary := findMultipartBoundary(body, boundary, partStart)
		partEnd := len(body)
		if nextBoundary >= 0 {
			partEnd = nextBoundary
		}

		headerEnd, valueStart, hasHeaderBoundary := multipartHeaderBoundary(body, partStart, partEnd)
		headerBlock := body[partStart:partEnd]
		if hasHeaderBoundary {
			headerBlock = body[partStart:headerEnd]
		}
		sensitive := multipartFieldSensitive(headerBlock, sensitiveName)
		if sensitive {
			if !hasHeaderBoundary {
				valueStart = multipartHeaderLineEnd(body, partStart, partEnd)
			}
			valueEnd := partEnd
			if nextBoundary >= 0 {
				valueEnd = trimMultipartBoundaryNewline(body, valueStart, valueEnd)
			}
			if valueStart <= valueEnd {
				spans = append(spans, redactionSpan{start: valueStart, end: valueEnd})
			}
		}

		if nextBoundary < 0 {
			break
		}
		boundaryAt = nextBoundary
	}
	return applyRedactionSpans(body, spans)
}

func inferMultipartBoundary(body string) (string, int, bool) {
	lineStart := 0
	for lineStart < len(body) {
		lineEnd := strings.IndexByte(body[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(body)
		} else {
			lineEnd += lineStart
		}
		line := strings.TrimSuffix(body[lineStart:lineEnd], "\r")
		if strings.HasPrefix(line, "--") {
			boundary := strings.TrimSuffix(line[2:], "--")
			if boundary != "" && len(boundary) <= 200 &&
				!strings.ContainsAny(boundary, "\r\n") {
				if _, _, ok := multipartBoundaryLineEnd(body, lineStart, boundary); ok {
					return boundary, lineStart, true
				}
			}
		}
		if lineEnd >= len(body) {
			break
		}
		lineStart = lineEnd + 1
	}
	return "", 0, false
}

func findMultipartBoundary(body, boundary string, start int) int {
	marker := "--" + boundary
	searchAt := start
	for searchAt < len(body) {
		relative := strings.Index(body[searchAt:], marker)
		if relative < 0 {
			return -1
		}
		candidate := searchAt + relative
		atLineStart := candidate == 0 || body[candidate-1] == '\n'
		if atLineStart {
			if _, _, ok := multipartBoundaryLineEnd(body, candidate, boundary); ok {
				return candidate
			}
		}
		searchAt = candidate + len(marker)
	}
	return -1
}

func multipartBoundaryLineEnd(body string, start int, boundary string) (int, bool, bool) {
	marker := "--" + boundary
	if start < 0 || start+len(marker) > len(body) ||
		body[start:start+len(marker)] != marker ||
		(start > 0 && body[start-1] != '\n') {
		return 0, false, false
	}
	cursor := start + len(marker)
	closing := false
	if strings.HasPrefix(body[cursor:], "--") {
		closing = true
		cursor += 2
	}
	for cursor < len(body) && (body[cursor] == ' ' || body[cursor] == '\t') {
		cursor++
	}
	switch {
	case cursor == len(body):
		return cursor, closing, true
	case body[cursor] == '\n':
		return cursor + 1, closing, true
	case body[cursor] == '\r' && cursor+1 < len(body) && body[cursor+1] == '\n':
		return cursor + 2, closing, true
	default:
		return 0, false, false
	}
}

func multipartHeaderBoundary(body string, start, end int) (int, int, bool) {
	if start < 0 || end < start || end > len(body) {
		return 0, 0, false
	}
	part := body[start:end]
	bestAt := -1
	separatorLength := 0
	for _, separator := range []string{"\r\n\r\n", "\n\n"} {
		if relative := strings.Index(part, separator); relative >= 0 &&
			(bestAt < 0 || relative < bestAt) {
			bestAt = relative
			separatorLength = len(separator)
		}
	}
	if bestAt < 0 {
		return 0, 0, false
	}
	return start + bestAt, start + bestAt + separatorLength, true
}

func multipartHeaderLineEnd(body string, start, end int) int {
	if start < 0 || end < start || end > len(body) {
		return start
	}
	if relative := strings.IndexByte(body[start:end], '\n'); relative >= 0 {
		return start + relative + 1
	}
	return end
}

func trimMultipartBoundaryNewline(body string, valueStart, valueEnd int) int {
	if valueEnd <= valueStart || valueEnd > len(body) {
		return valueEnd
	}
	if valueEnd-valueStart >= 2 && body[valueEnd-2:valueEnd] == "\r\n" {
		return valueEnd - 2
	}
	if body[valueEnd-1] == '\n' {
		return valueEnd - 1
	}
	return valueEnd
}

// multipartFieldSensitive considers every bounded Content-Disposition header
// and every name parameter within it. Duplicate headers and duplicate
// parameters are ambiguous input; allowing the first safe declaration to hide
// a later credential declaration would leak the part body.
func multipartFieldSensitive(headerBlock string, sensitiveName func(string) bool) bool {
	for _, rawValue := range multipartContentDispositionValues(headerBlock) {
		trimmed := strings.TrimSpace(rawValue)
		mediaType, params, err := mime.ParseMediaType(trimmed)
		if err == nil {
			if !strings.EqualFold(mediaType, "form-data") {
				continue
			}
			if sensitiveName(params["name"]) {
				return true
			}
		} else if !multipartFormDataPrefix(trimmed) {
			continue
		}

		// ParseMediaType rejects duplicate parameters and malformed/truncated
		// quoting. Scan every bounded name declaration as a fail-closed fallback
		// instead of selecting only the first one.
		for _, match := range multipartNameFallback.FindAllStringSubmatch(rawValue, -1) {
			if len(match) != 4 {
				continue
			}
			for _, candidate := range match[1:] {
				candidate = strings.TrimSpace(candidate)
				if candidate == "" {
					continue
				}
				if sensitiveName(candidate) {
					return true
				}
				// A truncated quote is part of the malformed token captured by
				// the unquoted fallback. Strip only edge quotes for
				// classification; the original header bytes remain untouched.
				if fields := strings.Fields(candidate); len(fields) > 0 &&
					sensitiveName(strings.Trim(fields[0], `"'`)) {
					return true
				}
			}
		}
	}
	return false
}

func multipartFormDataPrefix(value string) bool {
	token := value
	if end := strings.IndexAny(token, ";\t "); end >= 0 {
		token = token[:end]
	}
	return strings.EqualFold(strings.TrimSpace(token), "form-data")
}

func multipartContentDispositionValues(headerBlock string) []string {
	values := make([]string, 0, 1)
	var unfolded strings.Builder
	collecting := false
	flush := func() {
		if !collecting {
			return
		}
		values = append(values, strings.TrimSpace(unfolded.String()))
		unfolded.Reset()
		collecting = false
	}
	lineStart := 0
	for lineStart < len(headerBlock) {
		lineEnd := strings.IndexByte(headerBlock[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(headerBlock)
		} else {
			lineEnd += lineStart
		}
		line := strings.TrimSuffix(headerBlock[lineStart:lineEnd], "\r")
		continuation := len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
		if continuation {
			if collecting {
				if folded := strings.TrimSpace(line); folded != "" {
					if unfolded.Len() > 0 {
						unfolded.WriteByte(' ')
					}
					unfolded.WriteString(folded)
				}
			}
			if lineEnd >= len(headerBlock) {
				break
			}
			lineStart = lineEnd + 1
			continue
		}
		flush()
		name, rawValue, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "Content-Disposition") {
			collecting = true
			unfolded.WriteString(strings.TrimSpace(rawValue))
		}
		if lineEnd >= len(headerBlock) {
			break
		}
		lineStart = lineEnd + 1
	}
	flush()
	return values
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

// URLTokenSensitive reports whether a decoded or percent-encoded URL component
// has the same credential shape used by URL scrubbing. Importers use this to
// govern values before placing them into executable artifacts.
func URLTokenSensitive(token string) bool {
	return urlTokenLooksSensitive(token)
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

func redactSensitiveJSON(value string) (string, bool, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
		return value, false, false
	}
	var parsed any
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&parsed); err != nil {
		return value, false, false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return value, false, false
	}
	if !redactSensitiveJSONNode(parsed) {
		return value, false, true
	}
	out, err := json.Marshal(parsed)
	if err != nil {
		return value, false, true
	}
	return string(out), true, true
}

func redactSensitiveJSONFallback(value string) string {
	return redactJSONFallbackFields(value, credential.SensitiveName)
}

func redactJSONFallbackFields(value string, sensitiveName func(string) bool) string {
	if sensitiveName == nil {
		return value
	}
	// Handle invalid bare scalars first. The ordinary fallback expression can
	// otherwise consume only a valid-looking prefix (for example 123 from
	// 123abc) and leave the remainder of a credential visible.
	value = redactMalformedJSONBareScalars(value, sensitiveName)
	// A syntactically closed quoted prefix is not a complete JSON value unless
	// the next non-space byte is a structural delimiter. Redact malformed
	// suffixes before the ordinary fallback can preserve them.
	value = redactMalformedJSONStrings(value, sensitiveName)
	value = jsonFallbackFieldPattern.ReplaceAllStringFunc(value, func(field string) string {
		matches := jsonFallbackFieldPattern.FindStringSubmatch(field)
		if len(matches) != 5 {
			return field
		}
		name, err := strconv.Unquote(`"` + matches[2] + `"`)
		if err != nil || !sensitiveName(name) {
			return field
		}
		return matches[1] + `"` + Redacted + `"` + matches[4]
	})
	return redactJSONCompoundFields(value, sensitiveName)
}

// redactMalformedJSONBareScalars fails closed once a sensitive JSON-ish
// key-and-colon pair is established but the following value is neither a
// quoted string, compound value, nor a complete JSON scalar. Its scan is
// bounded by the next top-level value delimiter (or EOF), preserving the
// delimiter and trailing formatting whitespace.
func redactMalformedJSONBareScalars(value string, sensitiveName func(string) bool) string {
	matches := jsonAnyFieldStart.FindAllStringSubmatchIndex(value, -1)
	spans := make([]redactionSpan, 0, 1)
	coveredThrough := 0
	for _, match := range matches {
		if len(match) != 6 {
			continue
		}
		name, err := strconv.Unquote(`"` + value[match[4]:match[5]] + `"`)
		if err != nil || !sensitiveName(name) {
			continue
		}
		valueStart := match[1]
		if valueStart < coveredThrough || valueStart >= len(value) {
			continue
		}
		switch value[valueStart] {
		case '"', '{', '[':
			continue
		}

		valueEnd := malformedJSONScalarBoundary(value, valueStart)
		trimmedEnd := valueEnd
		for trimmedEnd > valueStart && isJSONSpace(value[trimmedEnd-1]) {
			trimmedEnd--
		}
		if trimmedEnd <= valueStart {
			continue
		}
		raw := value[valueStart:trimmedEnd]
		if raw == "true" || raw == "false" || raw == "null" ||
			jsonFallbackScalar.MatchString(raw) {
			continue
		}
		spans = append(spans, redactionSpan{start: valueStart, end: trimmedEnd})
		coveredThrough = valueEnd
	}
	return applyRedactionSpans(value, spans)
}

func malformedJSONScalarBoundary(value string, start int) int {
	inString := false
	escaped := false
	for index := start; index < len(value); index++ {
		ch := value[index]
		if inString {
			switch {
			case escaped:
				escaped = false
			case ch == '\\':
				escaped = true
			case ch == '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case ',', '}', ']':
			return index
		}
	}
	return len(value)
}

func isJSONSpace(ch byte) bool {
	switch ch {
	case ' ', '\t', '\r', '\n':
		return true
	default:
		return false
	}
}

func redactMalformedJSONStrings(value string, sensitiveName func(string) bool) string {
	matches := jsonStringFieldStart.FindAllStringSubmatchIndex(value, -1)
	spans := make([]redactionSpan, 0, 1)
	coveredThrough := 0
	for _, match := range matches {
		if len(match) != 6 {
			continue
		}
		name, err := strconv.Unquote(`"` + value[match[4]:match[5]] + `"`)
		if err != nil || !sensitiveName(name) {
			continue
		}
		valueStart := match[1]
		quoteStart := valueStart - 1
		if quoteStart < coveredThrough || quoteStart < 0 {
			continue
		}
		closingQuote, closed := closingJSONStringQuote(value, valueStart)
		valueEnd := len(value)
		if closed {
			afterValue := closingQuote + 1
			for afterValue < len(value) && isJSONSpace(value[afterValue]) {
				afterValue++
			}
			if afterValue == len(value) ||
				value[afterValue] == ',' ||
				value[afterValue] == '}' ||
				value[afterValue] == ']' {
				continue
			}
			valueEnd = malformedJSONStringBoundary(value, afterValue)
		}
		spans = append(spans, redactionSpan{start: quoteStart, end: valueEnd})
		coveredThrough = valueEnd
		if valueEnd == len(value) {
			break
		}
	}
	return applyRedactionSpansWithReplacement(value, spans, `"`+Redacted+`"`)
}

// malformedJSONStringBoundary returns the next delimiter that can safely begin
// another JSON field or close the surrounding structure. Quotes are already
// contradictory once this scan starts, so blindly toggling an in-string state
// would let an attacker hide the real next-field comma behind an extra quote.
func malformedJSONStringBoundary(value string, start int) int {
	for index := start; index < len(value); index++ {
		switch value[index] {
		case ',':
			if jsonFieldAtStart.MatchString(value[index+1:]) {
				return index
			}
		case '}', ']':
			after := index + 1
			for after < len(value) && isJSONSpace(value[after]) {
				after++
			}
			if after == len(value) ||
				value[after] == ',' ||
				value[after] == '}' ||
				value[after] == ']' {
				return index
			}
		}
	}
	return len(value)
}

// redactJSONCompoundFields handles JSON-ish payloads that fail strict parsing
// while a sensitive field owns an object or array value. The scalar fallback
// above cannot associate `"password":{...` with its nested content. Preserve a
// complete compound boundary when one can be found; otherwise redact through
// EOF so a truncated credential container cannot leak.
func redactJSONCompoundFields(value string, sensitiveName func(string) bool) string {
	matches := jsonAnyFieldStart.FindAllStringSubmatchIndex(value, -1)
	spans := make([]redactionSpan, 0, 1)
	coveredThrough := 0
	for _, match := range matches {
		if len(match) != 6 {
			continue
		}
		name, err := strconv.Unquote(`"` + value[match[4]:match[5]] + `"`)
		if err != nil || !sensitiveName(name) {
			continue
		}
		valueStart := match[1]
		if valueStart < coveredThrough ||
			valueStart >= len(value) ||
			(value[valueStart] != '{' && value[valueStart] != '[') {
			continue
		}
		valueEnd, closed := closingJSONCompound(value, valueStart)
		spans = append(spans, redactionSpan{start: valueStart, end: valueEnd})
		coveredThrough = valueEnd
		if !closed {
			break
		}
	}
	return applyRedactionSpans(value, spans)
}

func closingJSONCompound(value string, start int) (int, bool) {
	if start < 0 || start >= len(value) || (value[start] != '{' && value[start] != '[') {
		return start, false
	}
	stack := []byte{value[start]}
	inString := false
	escaped := false
	for index := start + 1; index < len(value); index++ {
		ch := value[index]
		if inString {
			switch {
			case escaped:
				escaped = false
			case ch == '\\':
				escaped = true
			case ch == '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, ch)
		case '}', ']':
			open := stack[len(stack)-1]
			if (open == '{' && ch != '}') || (open == '[' && ch != ']') {
				return len(value), false
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return index + 1, true
			}
		}
	}
	return len(value), false
}

func closingJSONStringQuote(value string, start int) (int, bool) {
	escaped := false
	for index := start; index < len(value); index++ {
		switch {
		case escaped:
			escaped = false
		case value[index] == '\\':
			escaped = true
		case value[index] == '"':
			return index, true
		}
	}
	return 0, false
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
			cp.scrubHARBody(entry, postData)
			cp.scrubNameValueList(postData, "params")
		}
	}
	if response, ok := entry["response"].(map[string]any); ok {
		cp.scrubNameValueList(response, "headers")
		cp.scrubNameValueList(response, "cookies")
		if content, ok := response["content"].(map[string]any); ok {
			cp.scrubHARBody(entry, content)
		}
	}
	cp.scrubWebSocketMessages(entry, "_webSocketMessages")
	cp.scrubWebSocketMessages(entry, "websocket_messages")
}

func (cp compiledPolicy) scrubHARBody(entry, container map[string]any) {
	text, ok := container["text"].(string)
	if !ok {
		return
	}
	textualMIME, knownMIME := classifyHARBodyMIME(container["mimeType"])
	encoding, _ := container["encoding"].(string)
	if !strings.EqualFold(strings.TrimSpace(encoding), "base64") {
		if knownMIME && !textualMIME {
			return
		}
		container["text"] = cp.value(text)
		return
	}
	decoded, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		// Malformed base64 is retained unless its visible representation itself
		// matches a sensitive pattern. In that case retain a valid encoded marker
		// and an explicit artifact diagnostic instead of malformed secret bytes.
		if cp.value(text) != text {
			container["text"] = base64.StdEncoding.EncodeToString([]byte(Redacted))
			container["encoding"] = "base64"
			appendHARScrubDiagnostic(entry, "malformed base64 body containing sensitive content was replaced")
		}
		return
	}
	if knownMIME && !textualMIME {
		// A successfully decoded printable binary body is still binary. Do not
		// let token-shaped bytes inside an octet stream change the captured
		// payload that replay/import must preserve byte-for-byte.
		return
	}
	if !utf8.Valid(decoded) || (!knownMIME && !isTextualHARBody(decoded)) {
		return
	}
	scrubbed := cp.value(string(decoded))
	container["text"] = base64.StdEncoding.EncodeToString([]byte(scrubbed))
	container["encoding"] = "base64"
}

// classifyHARBodyMIME returns whether a recognized media type is textual and
// whether the classification is authoritative. Vendor JSON/XML suffixes are
// textual, while well-known binary families remain binary even when their bytes
// happen to be valid, mostly printable UTF-8. Multipart and unknown application
// subtypes stay ambiguous because they may contain either kind of payload.
func classifyHARBodyMIME(raw any) (textual, known bool) {
	value, _ := raw.(string)
	value = strings.TrimSpace(value)
	if value == "" {
		return false, false
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return false, false
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if strings.HasPrefix(mediaType, "text/") ||
		strings.HasSuffix(mediaType, "+json") ||
		strings.HasSuffix(mediaType, "+xml") {
		return true, true
	}
	switch mediaType {
	case "application/json", "application/xml",
		"application/x-www-form-urlencoded",
		"application/javascript", "application/ecmascript",
		"application/graphql", "application/sql",
		"application/yaml", "application/x-yaml", "application/toml",
		"application/x-ndjson", "application/json-seq":
		return true, true
	case "application/octet-stream", "application/pdf",
		"application/zip", "application/gzip", "application/x-gzip",
		"application/x-7z-compressed", "application/vnd.rar", "application/x-rar-compressed",
		"application/x-tar", "application/wasm",
		"application/cbor", "application/msgpack", "application/x-msgpack",
		"application/protobuf", "application/x-protobuf":
		return false, true
	}
	for _, prefix := range []string{"image/", "audio/", "video/", "font/"} {
		if strings.HasPrefix(mediaType, prefix) {
			return false, true
		}
	}
	return false, false
}

func isTextualHARBody(data []byte) bool {
	if len(data) == 0 || !utf8.Valid(data) {
		return len(data) == 0
	}
	printable := 0
	total := 0
	for _, value := range string(data) {
		total++
		if unicode.IsPrint(value) || value == '\n' || value == '\r' || value == '\t' {
			printable++
		}
	}
	return total == 0 || printable*100/total >= 90
}

func appendHARScrubDiagnostic(entry map[string]any, message string) {
	if entry == nil || message == "" {
		return
	}
	values, _ := entry["_apexloadScrubDiagnostics"].([]any)
	entry["_apexloadScrubDiagnostics"] = append(values, message)
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
