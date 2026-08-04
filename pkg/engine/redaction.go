package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"sort"
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

	// Runtime secret matchers are derived from write-only values and include
	// reversible encodings that can appear in retained protocol evidence. The raw
	// limits cover one maximum-sized shared-import request plus bounded live
	// session/config material. Derived spellings consume only the remaining
	// pattern budget; raw exact values are always admitted first and remain
	// mandatory.
	maxRuntimeSecretRedactionPatternBytes = 32 << 20
	maxRuntimeSecretRedactionRawBytes     = 16 << 20
	maxRuntimeSecretRedactionValues       = 101_024
	maxRuntimeSecretRedactionReplacers    = 32
	maxRuntimeSecretRedactionLayers       = 4
)

type RedactionRule struct {
	Name        string `json:"name,omitempty"`
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement,omitempty"`
}

type RedactionConfig struct {
	Rules                  []RedactionRule
	compiled               []payloadReplacement
	secretReplacers        []*strings.Replacer
	runtimeSecretValues    map[string]struct{}
	runtimeSecretVariants  map[string]struct{}
	runtimeSecretRawBytes  int
	runtimePatternBytes    int
	runtimeMatcherComplete bool
	// runtimeParent is an immutable exact-value matcher shared by every VUser.
	// Per-VUser session/dynamic overlays point at that base instead of copying a
	// potentially large imported matcher after every cookie or extraction update.
	runtimeParent *RedactionConfig
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
}

var jwtCandidatePattern = regexp.MustCompile(`\b[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)

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

// HasRuntimeSecretMaterial reports whether the config contains exact,
// memory-only runtime values. Protocols use this signal to suppress binary
// evidence (notably screenshots), which cannot be safely text-redacted.
func (c *RedactionConfig) HasRuntimeSecretMaterial() bool {
	for depth := 0; c != nil && depth < maxRuntimeSecretRedactionLayers; depth++ {
		if len(c.runtimeSecretValues) > 0 {
			return true
		}
		c = c.runtimeParent
	}
	return false
}

// WithSecretValueRedaction returns a detached runtime-only redaction config
// that replaces exact resolved credential values anywhere retained evidence is
// scrubbed. Values are kept out of Rules so they cannot be serialized into
// assignments, archives, API responses, or policy snapshots.
func WithSecretValueRedaction(config *RedactionConfig, values []string) *RedactionConfig {
	detached, _ := withSecretValueRedaction(config, values)
	return detached
}

// WithSecretValueRedactionChecked constructs a complete raw exact-value matcher
// and a bounded set of common reversible spellings. Runtime entry points use
// this form before executing untrusted protocol code. Optional encoded variants
// never displace a raw value from the fixed matcher budget.
func WithSecretValueRedactionChecked(config *RedactionConfig, values []string) (*RedactionConfig, error) {
	return withSecretValueRedaction(config, values)
}

func withSecretValueRedaction(config *RedactionConfig, values []string) (*RedactionConfig, error) {
	existingValues := allRuntimeSecretValues(config)
	rawBytes := runtimeSecretRawValueBytes(existingValues)
	if len(existingValues) > maxRuntimeSecretRedactionValues {
		return nil, fmt.Errorf(
			"resolved secrets exceed the runtime redaction value limit (%d)",
			maxRuntimeSecretRedactionValues,
		)
	}
	if rawBytes > maxRuntimeSecretRedactionRawBytes {
		return nil, errors.New("resolved secrets exceed the runtime redaction raw-value limit")
	}
	newValues := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := existingValues[value]; exists {
			continue
		}
		if _, exists := newValues[value]; exists {
			continue
		}
		if len(existingValues)+len(newValues)+1 > maxRuntimeSecretRedactionValues {
			return nil, fmt.Errorf(
				"resolved secrets exceed the runtime redaction value limit (%d)",
				maxRuntimeSecretRedactionValues,
			)
		}
		if len(value) > maxRuntimeSecretRedactionRawBytes-rawBytes {
			return nil, errors.New("resolved secrets exceed the runtime redaction raw-value limit")
		}
		newValues[value] = struct{}{}
		rawBytes += len(value)
	}
	matcherReady := config != nil &&
		(len(existingValues) == 0 || hasRuntimeSecretReplacers(config))
	if len(newValues) == 0 &&
		(config == nil || matcherReady) {
		return config, nil
	}

	unique := make(map[string]struct{}, len(existingValues)+len(newValues))
	for value := range existingValues {
		unique[value] = struct{}{}
	}
	for value := range newValues {
		unique[value] = struct{}{}
	}
	if len(unique) == 0 {
		return config, nil
	}

	// Recompile the bounded set as one longest-first matcher. Besides avoiding
	// cross-chunk substring ambiguity, rebuilding ensures optional variants from
	// an older snapshot can never consume capacity needed by a newly added raw
	// value.
	variants, replacer, patternBytes, complete, err := compileRuntimeSecretReplacement(
		sortedRuntimeSecretValues(unique),
		nil,
		0,
	)
	if err != nil {
		return nil, err
	}
	detached := cloneRedactionPolicy(config)
	detached.runtimeSecretValues = unique
	detached.runtimeSecretVariants = variants
	detached.runtimeSecretRawBytes = rawBytes
	detached.runtimePatternBytes = patternBytes
	detached.runtimeMatcherComplete = complete
	detached.secretReplacers = []*strings.Replacer{replacer}
	return detached, nil
}

func sortedRuntimeSecretValues(values map[string]struct{}) []string {
	ordered := make([]string, 0, len(values))
	for value := range values {
		ordered = append(ordered, value)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if len(ordered[i]) == len(ordered[j]) {
			return ordered[i] < ordered[j]
		}
		return len(ordered[i]) > len(ordered[j])
	})
	return ordered
}

func compileRuntimeSecretReplacement(
	values []string,
	existing map[string]struct{},
	patternBytes int,
) (map[string]struct{}, *strings.Replacer, int, bool, error) {
	variants := make(map[string]struct{}, len(values)*8)
	ordered := make([]string, 0, len(values)*8)
	complete := true
	addVariant := func(variant string, required bool) error {
		if variant == "" || variant == redactedPayloadValue {
			return nil
		}
		if _, exists := existing[variant]; exists {
			return nil
		}
		if _, exists := variants[variant]; exists {
			return nil
		}
		replacement := runtimeSecretPlaceholder(variant)
		candidateBytes := len(variant) + len(replacement)
		if candidateBytes > maxRuntimeSecretRedactionPatternBytes-patternBytes {
			if required {
				return errors.New("resolved secrets exceed the runtime redaction matcher limit")
			}
			complete = false
			return nil
		}
		patternBytes += candidateBytes
		variants[variant] = struct{}{}
		ordered = append(ordered, variant)
		return nil
	}

	// Raw spellings are mandatory even for the compatibility wrapper.
	for _, value := range values {
		if err := addVariant(value, true); err != nil {
			return nil, nil, patternBytes, false, err
		}
	}
	for _, value := range values {
		for _, variant := range runtimeSecretRedactionVariants(value) {
			if err := addVariant(variant, false); err != nil {
				return nil, nil, patternBytes, false, err
			}
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		if len(ordered[i]) == len(ordered[j]) {
			return ordered[i] < ordered[j]
		}
		return len(ordered[i]) > len(ordered[j])
	})
	replacements := make([]string, 0, len(ordered)*2)
	for _, value := range ordered {
		replacements = append(replacements, value, runtimeSecretPlaceholder(value))
	}
	return variants, strings.NewReplacer(replacements...), patternBytes, complete, nil
}

func cloneRedactionPolicy(config *RedactionConfig) *RedactionConfig {
	detached := &RedactionConfig{}
	if config != nil {
		detached.Rules = append([]RedactionRule(nil), config.Rules...)
		detached.compiled = append([]payloadReplacement(nil), config.compiled...)
	}
	return detached
}

// withRuntimeSecretValueOverlay attaches a bounded, per-VUser matcher to an
// immutable shared base. The overlay owns only current dynamic/session values;
// it never copies the source-import matcher or its raw values.
func withRuntimeSecretValueOverlay(
	base *RedactionConfig,
	values []string,
) (*RedactionConfig, error) {
	if len(values) == 0 {
		return base, nil
	}
	runtime, err := withSecretValueRedaction(nil, values)
	if err != nil {
		return nil, err
	}
	if runtime == nil {
		return base, nil
	}
	if base == nil {
		return runtime, nil
	}
	detached := cloneRedactionPolicy(base)
	detached.secretReplacers = runtime.secretReplacers
	detached.runtimeSecretValues = runtime.runtimeSecretValues
	detached.runtimeSecretVariants = runtime.runtimeSecretVariants
	detached.runtimeSecretRawBytes = runtime.runtimeSecretRawBytes
	detached.runtimePatternBytes = runtime.runtimePatternBytes
	detached.runtimeMatcherComplete = runtime.runtimeMatcherComplete
	detached.runtimeParent = base
	return detached, nil
}

func allRuntimeSecretValues(config *RedactionConfig) map[string]struct{} {
	values := make(map[string]struct{})
	for depth := 0; config != nil && depth < maxRuntimeSecretRedactionLayers; depth++ {
		for value := range config.runtimeSecretValues {
			if value != "" {
				values[value] = struct{}{}
			}
		}
		config = config.runtimeParent
	}
	return values
}

func runtimeSecretRawValueBytes(values map[string]struct{}) int {
	total := 0
	for value := range values {
		total += len(value)
	}
	return total
}

func hasRuntimeSecretReplacers(config *RedactionConfig) bool {
	for depth := 0; config != nil && depth < maxRuntimeSecretRedactionLayers; depth++ {
		if len(config.secretReplacers) > 0 {
			return true
		}
		config = config.runtimeParent
	}
	return false
}

func runtimeSecretPlaceholder(value string) string {
	// strings.Replacer performs the multi-pattern match efficiently. A
	// same-width internal placeholder guarantees that even a one-byte secret
	// cannot amplify attacker-controlled evidence before retained bounds apply.
	return strings.Repeat("\x00", len(value))
}

func mergedRuntimeSecretValues(config *RedactionConfig, values []string) []string {
	unique := make(map[string]struct{}, len(values))
	for value := range allRuntimeSecretValues(config) {
		if value != "" {
			unique[value] = struct{}{}
		}
	}
	for _, value := range values {
		if value != "" {
			unique[value] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(unique))
	for value := range unique {
		ordered = append(ordered, value)
	}
	sort.Strings(ordered)
	return ordered
}

func runtimeSecretValueSetKey(values []string) [sha256.Size]byte {
	hash := sha256.New()
	var length [8]byte
	for _, value := range values {
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	var key [sha256.Size]byte
	copy(key[:], hash.Sum(nil))
	return key
}

// attachRuntimeSecretRedaction keeps custom policy state detached per group
// while sharing the immutable exact-secret matcher for identical value sets.
func attachRuntimeSecretRedaction(config, runtime *RedactionConfig) *RedactionConfig {
	if runtime == nil {
		return config
	}
	if config == nil || (len(config.Rules) == 0 && len(config.compiled) == 0) {
		return runtime
	}
	detached := &RedactionConfig{
		Rules:                  append([]RedactionRule(nil), config.Rules...),
		compiled:               append([]payloadReplacement(nil), config.compiled...),
		secretReplacers:        runtime.secretReplacers,
		runtimeSecretValues:    runtime.runtimeSecretValues,
		runtimeSecretVariants:  runtime.runtimeSecretVariants,
		runtimeSecretRawBytes:  runtime.runtimeSecretRawBytes,
		runtimePatternBytes:    runtime.runtimePatternBytes,
		runtimeMatcherComplete: runtime.runtimeMatcherComplete,
		runtimeParent:          runtime.runtimeParent,
	}
	return detached
}

func runtimeSecretRedactionVariants(value string) []string {
	// Keep this expansion explicitly bounded. Resolved secret snapshots are also
	// byte/entry bounded, but a Cartesian expansion of every reversible encoding
	// would still create excessive per-result replacement work.
	const maxVariants = 64
	variants := make([]string, 0, maxVariants)
	seen := make(map[string]struct{}, maxVariants)
	add := func(candidate string) {
		if candidate == "" || len(variants) >= maxVariants {
			return
		}
		if _, exists := seen[candidate]; exists {
			return
		}
		seen[candidate] = struct{}{}
		variants = append(variants, candidate)
	}
	addEncoded := func(candidate string, includeHex bool) {
		add(candidate)
		queryEscaped := url.QueryEscape(candidate)
		percentSpaceEscaped := strings.ReplaceAll(queryEscaped, "+", "%20")
		pathEscaped := url.PathEscape(candidate)
		add(queryEscaped)
		add(lowerPercentEscapes(queryEscaped))
		add(percentSpaceEscaped)
		add(lowerPercentEscapes(percentSpaceEscaped))
		add(pathEscaped)
		add(lowerPercentEscapes(pathEscaped))
		add(base64.StdEncoding.EncodeToString([]byte(candidate)))
		add(base64.RawStdEncoding.EncodeToString([]byte(candidate)))
		add(base64.URLEncoding.EncodeToString([]byte(candidate)))
		add(base64.RawURLEncoding.EncodeToString([]byte(candidate)))
		if includeHex {
			hexValue := hex.EncodeToString([]byte(candidate))
			add(hexValue)
			add(strings.ToUpper(hexValue))
		}
	}

	// Raw reversible encodings are the most common and retain priority if the
	// fixed variant budget is exhausted.
	addEncoded(value, true)

	// Evidence often contains a response body as text rather than as a parsed
	// object. In that representation a quote/control character from a secret is
	// JSON escaped, and HTML/XML echoes entity-escape metacharacters. Include both
	// Go's HTML-safe JSON form and the common non-HTML-safe form, plus the named,
	// decimal, and hexadecimal entity spellings used by browsers and servers.
	jsonHTMLSafe := jsonEscapedSecret(value, true)
	jsonPlain := jsonEscapedSecret(value, false)
	htmlNamed := escapeRuntimeSecretEntities(value, "&quot;", "&#39;")
	xmlNamed := escapeRuntimeSecretEntities(value, "&quot;", "&apos;")
	escaped := []string{
		jsonHTMLSafe,
		upperJSONUnicodeEscapes(jsonHTMLSafe),
		jsonPlain,
		strings.ReplaceAll(jsonPlain, "/", `\/`),
		html.EscapeString(value),
		htmlNamed,
		xmlNamed,
		escapeRuntimeSecretEntities(value, "&#x22;", "&#x27;"),
	}
	// Reserve space for every direct escaped spelling before adding composite
	// encodings. Otherwise early candidates could exhaust the fixed budget and
	// leave a later HTML/XML spelling unprotected.
	for _, candidate := range escaped {
		add(candidate)
	}
	// Prioritize representative composite spellings that commonly appear when
	// neutral JSON/HTML evidence is subsequently URL- or base64-encoded.
	for _, candidate := range []string{
		url.QueryEscape(jsonPlain),
		strings.ReplaceAll(url.QueryEscape(jsonPlain), "+", "%20"),
		base64.StdEncoding.EncodeToString([]byte(htmlNamed)),
		base64.RawStdEncoding.EncodeToString([]byte(htmlNamed)),
		base64.StdEncoding.EncodeToString([]byte(xmlNamed)),
		url.QueryEscape(xmlNamed),
	} {
		add(candidate)
	}
	for _, candidate := range escaped {
		// URL/base64 encodings of the escaped representation cover the common
		// composite cases without recursively expanding the entire set.
		addEncoded(candidate, false)
	}
	return variants
}

func upperJSONUnicodeEscapes(value string) string {
	if !strings.Contains(value, `\u`) {
		return value
	}
	bytes := []byte(value)
	for index := 0; index+5 < len(bytes); index++ {
		if bytes[index] != '\\' || bytes[index+1] != 'u' {
			continue
		}
		for offset := 2; offset <= 5; offset++ {
			if bytes[index+offset] >= 'a' && bytes[index+offset] <= 'f' {
				bytes[index+offset] -= 'a' - 'A'
			}
		}
		index += 5
	}
	return string(bytes)
}

func jsonEscapedSecret(value string, escapeHTML bool) string {
	var encoded []byte
	if escapeHTML {
		encoded, _ = json.Marshal(value)
	} else {
		var buffer bytes.Buffer
		encoder := json.NewEncoder(&buffer)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(value); err != nil {
			return ""
		}
		encoded = bytes.TrimSpace(buffer.Bytes())
	}
	if len(encoded) < 2 || encoded[0] != '"' || encoded[len(encoded)-1] != '"' {
		return ""
	}
	return string(encoded[1 : len(encoded)-1])
}

func escapeRuntimeSecretEntities(value, quote, apostrophe string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, quote,
		"'", apostrophe,
	).Replace(value)
}

func lowerPercentEscapes(value string) string {
	if !strings.Contains(value, "%") {
		return value
	}
	buf := []byte(value)
	for index := 0; index+2 < len(buf); index++ {
		if buf[index] != '%' {
			continue
		}
		for offset := 1; offset <= 2; offset++ {
			if buf[index+offset] >= 'A' && buf[index+offset] <= 'F' {
				buf[index+offset] += 'a' - 'A'
			}
		}
		index += 2
	}
	return string(buf)
}

func scrubSensitiveResult(result *protocol.Result, config *RedactionConfig) {
	scrubSensitiveResultWithOptions(result, config, true)
}

func scrubSensitiveResultWithOptions(result *protocol.Result, config *RedactionConfig, retainResponseBody bool) {
	scrubSensitiveResultWithDimensionConfig(result, config, config, retainResponseBody)
}

func scrubSensitiveResultWithDimensionConfig(
	result *protocol.Result,
	config *RedactionConfig,
	dimensionConfig *RedactionConfig,
	retainResponseBody bool,
) {
	if result == nil {
		return
	}

	scrubSensitiveResultMetadata(result, dimensionConfig, config)
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
	if config.HasRuntimeSecretMaterial() {
		// Screenshots are opaque pixels/base64. Exact text replacement cannot
		// prove the rendered secret is gone, so governed runtime material makes
		// screenshot retention fail closed.
		result.ScreenshotBase64 = ""
	}
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
	if config.HasRuntimeSecretMaterial() {
		result.Snapshot.ScreenshotBase64 = ""
	}
}

// scrubSensitiveResultMetadata covers the scalar dimensions copied into live
// metrics and generator archives. These fields bypass response/snapshot
// evidence handling, so leaving them untouched would let a resolved secret in
// a correlation header, dependency label, adapter label, or imported source
// identifier survive an otherwise valid custom redaction policy.
func scrubSensitiveResultMetadata(result *protocol.Result, dimensionConfig, evidenceConfig *RedactionConfig) {
	result.ActionName = scrubSensitiveTextWithConfig(result.ActionName, dimensionConfig)
	result.SampleKind = scrubSensitiveTextWithConfig(result.SampleKind, dimensionConfig)
	result.ParentActionName = scrubSensitiveTextWithConfig(result.ParentActionName, dimensionConfig)
	result.Group = scrubSensitiveTextWithConfig(result.Group, dimensionConfig)
	result.Region = scrubSensitiveTextWithConfig(result.Region, dimensionConfig)
	// Correlation IDs are response evidence rather than an authored metric
	// dimension. Keep the full protocol-session matcher here even though stable
	// labels deliberately exclude cookie snapshots.
	result.CorrelationID = scrubSensitiveTextWithConfig(result.CorrelationID, evidenceConfig)
	result.NetworkProfile = scrubSensitiveTextWithConfig(result.NetworkProfile, dimensionConfig)
	result.DependencyType = scrubSensitiveTextWithConfig(result.DependencyType, dimensionConfig)
	result.DependencyTarget = scrubSensitiveTextWithConfig(result.DependencyTarget, dimensionConfig)
	result.SourceStepID = scrubSensitiveTextWithConfig(result.SourceStepID, dimensionConfig)
	result.SourceAction = scrubSensitiveTextWithConfig(result.SourceAction, dimensionConfig)
	result.SourceActionName = scrubSensitiveTextWithConfig(result.SourceActionName, dimensionConfig)
	result.SourceTransaction = scrubSensitiveTextWithConfig(result.SourceTransaction, dimensionConfig)
	result.SourceProfile = scrubSensitiveTextWithConfig(result.SourceProfile, dimensionConfig)
	result.SourcePath = scrubSensitiveTextWithConfig(result.SourcePath, dimensionConfig)
	result.SourceUID = scrubSensitiveTextWithConfig(result.SourceUID, dimensionConfig)
	result.RequestType = scrubSensitiveTextWithConfig(result.RequestType, dimensionConfig)
	result.OperationKind = scrubSensitiveTextWithConfig(result.OperationKind, dimensionConfig)

	if result.ClientMetrics != nil {
		result.ClientMetrics.DeviceProfile = scrubSensitiveTextWithConfig(result.ClientMetrics.DeviceProfile, dimensionConfig)
		result.ClientMetrics.DevicePlatform = scrubSensitiveTextWithConfig(result.ClientMetrics.DevicePlatform, dimensionConfig)
		result.ClientMetrics.DeviceTier = scrubSensitiveTextWithConfig(result.ClientMetrics.DeviceTier, dimensionConfig)
		result.ClientMetrics.BrowserEngine = scrubSensitiveTextWithConfig(result.ClientMetrics.BrowserEngine, dimensionConfig)
	}
	if result.ServerTelemetry != nil {
		result.ServerTelemetry.RequestSizeClass = scrubSensitiveTextWithConfig(result.ServerTelemetry.RequestSizeClass, dimensionConfig)
		result.ServerTelemetry.ResponseSizeClass = scrubSensitiveTextWithConfig(result.ServerTelemetry.ResponseSizeClass, dimensionConfig)
		result.ServerTelemetry.SOAPFaultCode = scrubSensitiveTextWithConfig(result.ServerTelemetry.SOAPFaultCode, dimensionConfig)
		result.ServerTelemetry.SOAPFaultCategory = scrubSensitiveTextWithConfig(result.ServerTelemetry.SOAPFaultCategory, dimensionConfig)
		result.ServerTelemetry.SOAPNamespaceConflict = scrubSensitiveTextWithConfig(result.ServerTelemetry.SOAPNamespaceConflict, dimensionConfig)
		result.ServerTelemetry.PartitionID = scrubSensitiveTextWithConfig(result.ServerTelemetry.PartitionID, dimensionConfig)
	}
}

// scrubSensitiveStructuredResult runs before the bounded projection. It
// handles key-aware extraction values and typed/heterogeneous adapter evidence
// that the generic evidence bounder cannot otherwise preserve safely. Runtime
// variables have already received the raw extraction values at this point.
func scrubSensitiveStructuredResult(result *protocol.Result, config *RedactionConfig) {
	if result == nil {
		return
	}
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
	// Apply exact runtime variants before the generic structured scrubber. A
	// high-entropy pre-pass can otherwise redact only the unpadded portion of a
	// base64 value, preventing the exact composite variant from matching while
	// leaving encoding punctuation behind.
	runtimeSafe, _ := scrubRuntimeSecretEvidenceValue(input, config).(map[string]any)
	builtIn, _ := scrub.Structured(runtimeSafe, scrub.Policy{}).(map[string]any)
	if len(builtIn) == 0 {
		return nil
	}
	for key, value := range builtIn {
		builtIn[key] = scrubSensitiveEvidenceValue(value, config)
	}
	return builtIn
}

func scrubRuntimeSecretEvidenceValue(value any, config *RedactionConfig) any {
	if !hasRuntimeSecretReplacers(config) {
		return value
	}
	switch typed := value.(type) {
	case string:
		return scrubRuntimeSecretText(typed, config)
	case []any:
		out := make([]any, len(typed))
		for index := range typed {
			out[index] = scrubRuntimeSecretEvidenceValue(typed[index], config)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, entry := range typed {
			out[key] = scrubRuntimeSecretEvidenceValue(entry, config)
		}
		return out
	case map[string]string:
		out := make(map[string]string, len(typed))
		for key, entry := range typed {
			out[key] = scrubRuntimeSecretText(entry, config)
		}
		return out
	default:
		return value
	}
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

	// Exact runtime values and their reversible encodings must run first.
	// Generic credential regexes can otherwise rewrite only a prefix (for
	// example, at an escaped JSON quote) and prevent the exact matcher from
	// finding the remaining secret.
	redacted := scrubRuntimeSecretText(value, config)
	for _, pattern := range payloadRedactionPatterns {
		redacted = pattern.re.ReplaceAllString(redacted, pattern.repl)
	}
	redacted = scrubStructurallyValidJWTs(redacted)
	if config != nil {
		for _, pattern := range config.compiled {
			redacted = pattern.re.ReplaceAllString(redacted, pattern.repl)
		}
	}
	redacted = scrubRuntimeSecretText(redacted, config)
	// The shared scrubber performs URL-aware bare-token redaction. Keeping a
	// second context-free high-entropy pass here caused ordinary URL paths such
	// as :443/Services/... to be mistaken for credentials before the URL-aware
	// layer could preserve their structure.
	return scrub.Value(redacted, scrub.Policy{})
}

func scrubStructurallyValidJWTs(value string) string {
	return jwtCandidatePattern.ReplaceAllStringFunc(value, func(candidate string) string {
		segments := strings.Split(candidate, ".")
		if len(segments) != 3 {
			return candidate
		}
		headerBytes, err := base64.RawURLEncoding.DecodeString(segments[0])
		if err != nil {
			return candidate
		}
		var header map[string]json.RawMessage
		if err := json.Unmarshal(headerBytes, &header); err != nil || header == nil {
			return candidate
		}
		var algorithm string
		if err := json.Unmarshal(header["alg"], &algorithm); err != nil || strings.TrimSpace(algorithm) == "" {
			return candidate
		}
		payloadBytes, err := base64.RawURLEncoding.DecodeString(segments[1])
		if err != nil {
			return candidate
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(payloadBytes, &payload); err != nil || payload == nil {
			return candidate
		}
		// The signature is opaque rather than JSON, but it is still a base64url
		// JWT segment. Requiring it to decode prevents dotted identifiers with a
		// JSON-looking prefix from being classified as tokens solely because their
		// final component happens to use URL-safe characters.
		if _, err := base64.RawURLEncoding.DecodeString(segments[2]); err != nil {
			return candidate
		}
		return redactedPayloadValue
	})
}

func scrubRuntimeSecretText(value string, config *RedactionConfig) string {
	if value == "" || !hasRuntimeSecretReplacers(config) {
		return value
	}

	// Every immutable matcher layer sees the same original evidence. Replacing a
	// shorter parent value first must not prevent a longer overlay value from
	// matching (or vice versa), so union their same-width placeholder spans
	// before exposing the public marker.
	original := value
	matchedOffsets := make([]bool, len(value))
	matched := false
	for depth := 0; config != nil && depth < maxRuntimeSecretRedactionLayers; depth++ {
		for _, replacer := range config.secretReplacers {
			candidate := replacer.Replace(original)
			if len(candidate) != len(original) {
				// Runtime replacements are constructed same-width. Ignore an
				// impossible malformed layer rather than indexing mismatched
				// evidence; the ordinary scrubbers still run afterwards.
				continue
			}
			for index := range candidate {
				if candidate[index] != 0 || original[index] == 0 {
					continue
				}
				matchedOffsets[index] = true
				matched = true
			}
		}
		// A same-width zero placeholder cannot distinguish an authored NUL from
		// a NUL inside the matched secret. Resolve only those unusual variants by
		// exact offsets; ordinary evidence NULs remain untouched.
		for variant := range config.runtimeSecretVariants {
			if !strings.ContainsRune(variant, '\x00') {
				continue
			}
			for offset := 0; offset <= len(original)-len(variant); {
				index := strings.Index(original[offset:], variant)
				if index < 0 {
					break
				}
				index += offset
				for matchIndex := index; matchIndex < index+len(variant); matchIndex++ {
					matchedOffsets[matchIndex] = true
				}
				matched = true
				offset = index + 1
			}
		}
		config = config.runtimeParent
	}
	if !matched {
		return value
	}
	return expandRuntimeSecretMatches(original, matchedOffsets)
}

func expandRuntimeSecretMatches(value string, matched []bool) string {
	// Preserve the public [REDACTED] contract for ordinary matches, but cap
	// growth to one marker beyond the original evidence size. Once that budget
	// would be exceeded, a same-width opaque run remains fail-closed without
	// causing replacement amplification.
	maxOutput := len(value) + len(redactedPayloadValue)
	var out strings.Builder
	out.Grow(min(maxOutput, len(value)+len(redactedPayloadValue)))
	for offset := 0; offset < len(value); {
		index := offset
		for index < len(value) && !matched[index] {
			index++
		}
		if index == len(value) {
			out.WriteString(value[offset:])
			break
		}
		out.WriteString(value[offset:index])
		end := index + 1
		for end < len(value) && matched[end] {
			end++
		}
		remaining := len(value) - end
		if out.Len()+len(redactedPayloadValue)+remaining <= maxOutput {
			out.WriteString(redactedPayloadValue)
		} else {
			out.WriteString(strings.Repeat("*", end-index))
		}
		offset = end
	}
	return out.String()
}
