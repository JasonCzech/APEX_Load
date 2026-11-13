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
	"sync"
	"sync/atomic"

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
	// runtimePublicationUnavailable is an immutable publication-only sentinel.
	// It carries no values and cannot replace the exact execution matcher.
	runtimePublicationUnavailable bool
	// runtimeScriptSourcedValues is the subset of runtimeSecretValues on THIS
	// layer whose origin is the script itself — literals an import retained,
	// credentials the operator authored, and values a script-sourced template
	// namespace resolved to. It is always a subset of runtimeSecretValues.
	//
	// Membership is identical for text scrubbing: script-sourced values are
	// replaced in evidence exactly like platform-managed ones. The subset exists
	// only for the PRESENCE tests that suppress binary evidence — screenshots and
	// browser HAR/trace capture — which cannot be scrubbed and therefore fail
	// closed. Suppressing them for a value the operator already reads in their own
	// script would silently disable evidence capture that works today, so those
	// tests consult platform-managed membership only.
	runtimeScriptSourcedValues map[string]struct{}
	// runtimeParent is an immutable exact-value matcher shared by every VUser.
	// Per-VUser session/dynamic overlays point at that base instead of copying a
	// potentially large imported matcher after every cookie or extraction update.
	runtimeParent *RedactionConfig
	// dynamic holds an atomic, lock-free overlay for secrets minted by protocol
	// adapters at runtime (such as OAuth2 access/refresh tokens or session
	// exchange URLs) without mutating immutable matcher layers or increasing
	// runtimeParent depth.
	dynamic *dynamicSecretRegistry
	// authoredEvidenceVisible marks a config whose script left
	// config.log.redact_run_evidence off. Built-in NAME/SHAPE scrubbing
	// (sensitive header names, password=/client_secret JSON keys, URL userinfo
	// and the like) then no longer applies to script-authored REQUEST content.
	// Exact runtime values (platform-managed secrets, dynamic tokens) and the
	// project's own redaction rules still apply everywhere, and server RESPONSE
	// content keeps the full scrubber. Derived layers inherit it through
	// cloneRedactionPolicy and the runtimeParent chain.
	authoredEvidenceVisible bool
}

type dynamicSecretRegistry struct {
	mu      sync.Mutex
	fence   sync.RWMutex
	gen     atomic.Uint64
	scopes  map[string][]string
	overlay atomic.Pointer[RedactionConfig]
}

func ensureDynamicSecretRegistry(config *RedactionConfig) *RedactionConfig {
	if config.publicationUnavailable() {
		return unavailableRuntimePublicationConfig
	}
	existing := config.findDynamicRegistry()
	if existing != nil && config != nil && config.dynamic == existing {
		return config
	}
	detached := &RedactionConfig{}
	if config != nil {
		*detached = *config
		detached.Rules = append([]RedactionRule(nil), config.Rules...)
		detached.compiled = append([]payloadReplacement(nil), config.compiled...)
	}
	if existing != nil {
		detached.dynamic = existing
	} else {
		detached.dynamic = &dynamicSecretRegistry{}
	}
	return detached
}

func attachDynamicSecretRegistry(config *RedactionConfig, reg *dynamicSecretRegistry) *RedactionConfig {
	if config.publicationUnavailable() {
		return unavailableRuntimePublicationConfig
	}
	if reg == nil || (config != nil && config.findDynamicRegistry() != nil) {
		return config
	}
	detached := &RedactionConfig{}
	if config != nil {
		*detached = *config
		detached.Rules = append([]RedactionRule(nil), config.Rules...)
		detached.compiled = append([]payloadReplacement(nil), config.compiled...)
	}
	detached.dynamic = reg
	return detached
}

func (c *RedactionConfig) findDynamicRegistry() *dynamicSecretRegistry {
	for depth := 0; c != nil && depth < maxRuntimeSecretRedactionLayers; depth++ {
		if c.dynamic != nil {
			return c.dynamic
		}
		c = c.runtimeParent
	}
	return nil
}

func (c *RedactionConfig) dynamicOverlay() *RedactionConfig {
	if reg := c.findDynamicRegistry(); reg != nil {
		return reg.overlay.Load()
	}
	return nil
}

func (c *RedactionConfig) dynamicGeneration() uint64 {
	if reg := c.findDynamicRegistry(); reg != nil {
		return reg.gen.Load()
	}
	return 0
}

// RegisterDynamicSecrets records runtime-minted platform secrets (e.g., OAuth
// bearer/refresh tokens) into the run's dynamic redaction overlay.
func (c *RedactionConfig) RegisterDynamicSecrets(scope string, values []string) {
	reg := c.findDynamicRegistry()
	if reg == nil {
		return
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "dynamic"
	}
	cleaned := uniqueSortedRuntimeCredentialValues(values)
	if len(cleaned) == 0 {
		return
	}

	reg.mu.Lock()
	defer reg.mu.Unlock()
	if cur := reg.overlay.Load(); cur != nil && cur.runtimePublicationUnavailable {
		return
	}
	if reg.scopes == nil {
		reg.scopes = make(map[string][]string)
	}
	prev := reg.scopes[scope]
	combined := uniqueSortedRuntimeCredentialValues(append(append([]string(nil), prev...), cleaned...))
	if len(prev) == len(combined) {
		same := true
		for i := range prev {
			if prev[i] != combined[i] {
				same = false
				break
			}
		}
		if same {
			return
		}
	}
	reg.scopes[scope] = combined
	flatSet := make(map[string]struct{})
	for _, list := range reg.scopes {
		for _, v := range list {
			if v != "" {
				flatSet[v] = struct{}{}
			}
		}
	}
	var compiled *RedactionConfig
	if len(flatSet) > 0 {
		flat := make([]string, 0, len(flatSet))
		for v := range flatSet {
			flat = append(flat, v)
		}
		sort.Strings(flat)
		var err error
		compiled, err = WithSecretValueRedactionChecked(nil, flat)
		if err != nil {
			compiled = unavailableRuntimePublicationConfig
		}
	}
	reg.fence.Lock()
	reg.overlay.Store(compiled)
	reg.gen.Add(1)
	reg.fence.Unlock()
}

var unavailableRuntimePublicationConfig = &RedactionConfig{runtimePublicationUnavailable: true}

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

// ApplyPatterns applies only the configured payload rules, in order, with no
// runtime values and no shared scrubber: for a caller that chains several
// configurations before scrubbing once.
func (c *RedactionConfig) ApplyPatterns(value string) string {
	if c == nil {
		return value
	}
	for _, pattern := range c.compiled {
		value = pattern.re.ReplaceAllString(value, pattern.repl)
	}
	return value
}

// HasRuntimeSecretMaterial reports whether the config contains exact,
// memory-only PLATFORM-MANAGED runtime values. Protocols use this signal to
// suppress binary evidence (notably screenshots and browser HAR/trace capture),
// which cannot be safely text-redacted.
//
// Script-sourced values are deliberately excluded. They are scrubbed from text
// evidence like any other runtime value, but they are readable by the operator
// in the script they are running, so letting them reach this presence test would
// silently turn screenshot and HAR capture OFF for scripts where it works today
// — a capability loss, not a protection.
//
// runtimeScriptSourcedValues is a subset of runtimeSecretValues on each layer, so
// the comparison stays O(1) per layer.
func (c *RedactionConfig) publicationUnavailable() bool {
	if c == nil {
		return false
	}
	if dyn := c.dynamicOverlay(); dyn != nil && dyn.runtimePublicationUnavailable {
		return true
	}
	for depth := 0; c != nil; depth++ {
		if depth >= maxRuntimeSecretRedactionLayers || c.runtimePublicationUnavailable {
			return true
		}
		c = c.runtimeParent
	}
	return false
}

func (c *RedactionConfig) HasRuntimeSecretMaterial() bool {
	if c.publicationUnavailable() {
		return true
	}
	if dyn := c.dynamicOverlay(); dyn != nil && dyn.HasRuntimeSecretMaterial() {
		return true
	}
	for depth := 0; c != nil && depth < maxRuntimeSecretRedactionLayers; depth++ {
		if c.runtimePublicationUnavailable || len(c.runtimeSecretValues) > len(c.runtimeScriptSourcedValues) {
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

// WithScriptSourcedSecretValueRedaction adds values whose origin is the script
// itself. They join the same exact-value text matcher, but they never satisfy
// HasRuntimeSecretMaterial, so enabling script-sourced run-evidence redaction
// cannot disable screenshot or HAR capture. Platform-managed registration of the
// same literal always wins.
func WithScriptSourcedSecretValueRedaction(
	config *RedactionConfig,
	values []string,
) (*RedactionConfig, error) {
	return withSecretValueRedactionOrigin(config, values, true)
}

// WithSecretValueRedactionChecked constructs a complete raw exact-value matcher
// and a bounded set of common reversible spellings. Runtime entry points use
// this form before executing untrusted protocol code. Optional encoded variants
// never displace a raw value from the fixed matcher budget.
func WithSecretValueRedactionChecked(config *RedactionConfig, values []string) (*RedactionConfig, error) {
	return withSecretValueRedaction(config, values)
}

func withSecretValueRedaction(config *RedactionConfig, values []string) (*RedactionConfig, error) {
	return withSecretValueRedactionOrigin(config, values, false)
}

// withSecretValueRedactionOrigin is the single matcher builder. scriptSourced
// marks only the values passed in THIS call; membership already carried by
// config is preserved, and a value registered as platform-managed anywhere is
// never demoted to script-sourced.
func withSecretValueRedactionOrigin(
	config *RedactionConfig,
	values []string,
	scriptSourced bool,
) (*RedactionConfig, error) {
	existingValues := allRuntimeSecretValues(config)
	existingScriptSourced := allRuntimeScriptSourcedValues(config)
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
	// A platform-managed registration of a literal already carried as
	// script-sourced must still promote it, or a project secret that happens to
	// equal a retained import literal would keep binary-evidence capture enabled.
	promotesOrigin := false
	if !scriptSourced && len(existingScriptSourced) > 0 {
		for _, value := range values {
			if _, demoted := existingScriptSourced[value]; demoted {
				promotesOrigin = true
				break
			}
		}
	}
	if len(newValues) == 0 && !promotesOrigin &&
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
	// This layer now carries the flattened value set, so it must also carry the
	// flattened origin set. Values added by this call take THIS call's origin;
	// everything else keeps the origin it already had.
	marked := make(map[string]struct{}, len(existingScriptSourced))
	for value := range existingScriptSourced {
		if _, present := unique[value]; present {
			marked[value] = struct{}{}
		}
	}
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, present := unique[value]; !present {
			continue
		}
		if !scriptSourced {
			delete(marked, value)
			continue
		}
		// Only a value this call INTRODUCED takes this call's script-sourced
		// origin. A value the config already carried keeps whatever the seed loop
		// above copied for it, so re-listing a literal that is already registered
		// platform-managed can never demote it — the ordering production actually
		// uses is platform-managed first, script-sourced second.
		if _, isNew := newValues[value]; isNew {
			marked[value] = struct{}{}
		}
	}
	if len(marked) > 0 {
		detached.runtimeScriptSourcedValues = marked
	}
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
		detached.dynamic = config.findDynamicRegistry()
		detached.authoredEvidenceVisible = !config.scrubsAuthoredEvidence()
	}
	return detached
}

// scrubsAuthoredEvidence reports whether built-in name/shape scrubbing applies to
// script-authored request content under this config. It is true unless the
// config (or a layer it was derived from) was built for a script that leaves
// config.log.redact_run_evidence off, so a nil or unrelated config keeps the
// historical full scrub.
func (c *RedactionConfig) scrubsAuthoredEvidence() bool {
	for depth := 0; c != nil && depth < maxRuntimeSecretRedactionLayers; depth++ {
		if c.authoredEvidenceVisible {
			return false
		}
		c = c.runtimeParent
	}
	return true
}

// withAuthoredEvidenceScrubbing applies the script's config.log.redact_run_evidence
// setting to a run's root redaction config. With the setting on the config is
// returned unchanged. With it off a detached copy is marked so request-side
// evidence keeps the values the script authored (see authoredEvidenceVisible).
// It never fails: the setting only ever relaxes name/shape scrubbing of authored
// content, never exact platform-managed matching.
func withAuthoredEvidenceScrubbing(config *RedactionConfig, redactRunEvidence bool) *RedactionConfig {
	if redactRunEvidence || config.publicationUnavailable() || !config.scrubsAuthoredEvidence() {
		return config
	}
	detached := &RedactionConfig{}
	if config != nil {
		*detached = *config
		detached.Rules = append([]RedactionRule(nil), config.Rules...)
		detached.compiled = append([]payloadReplacement(nil), config.compiled...)
	}
	detached.authoredEvidenceVisible = true
	return detached
}

// withRuntimeSecretValueOverlay attaches a bounded, per-VUser matcher to an
// immutable shared base. The overlay owns only current dynamic/session values;
// it never copies the source-import matcher or its raw values.
func withRuntimeSecretValueOverlay(
	base *RedactionConfig,
	platformManaged []string,
	scriptSourced []string,
) (*RedactionConfig, error) {
	if len(platformManaged) == 0 && len(scriptSourced) == 0 {
		return base, nil
	}
	runtime, err := withSecretValueRedactionOrigin(nil, platformManaged, false)
	if err != nil {
		return nil, err
	}
	runtime, err = withSecretValueRedactionOrigin(runtime, scriptSourced, true)
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
	detached.runtimeScriptSourcedValues = runtime.runtimeScriptSourcedValues
	detached.runtimeParent = base
	return detached, nil
}

func allRuntimeSecretValues(config *RedactionConfig) map[string]struct{} {
	values := make(map[string]struct{})
	if dyn := config.dynamicOverlay(); dyn != nil {
		for value := range dyn.runtimeSecretValues {
			if value != "" {
				values[value] = struct{}{}
			}
		}
	}
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

// allRuntimeScriptSourcedValues flattens script-sourced membership across the
// layer chain. A value counts as script-sourced only when EVERY layer that
// carries it marks it script-sourced: platform-managed membership on any layer
// wins, so flattening can never downgrade a project secret.
func allRuntimeScriptSourcedValues(config *RedactionConfig) map[string]struct{} {
	scriptSourced := make(map[string]struct{})
	var platformManaged map[string]struct{}
	if dyn := config.dynamicOverlay(); dyn != nil {
		for value := range dyn.runtimeSecretValues {
			if value == "" {
				continue
			}
			if platformManaged == nil {
				platformManaged = make(map[string]struct{})
			}
			platformManaged[value] = struct{}{}
		}
	}
	for depth := 0; config != nil && depth < maxRuntimeSecretRedactionLayers; depth++ {
		for value := range config.runtimeSecretValues {
			if value == "" {
				continue
			}
			if _, marked := config.runtimeScriptSourcedValues[value]; marked {
				scriptSourced[value] = struct{}{}
				continue
			}
			if platformManaged == nil {
				platformManaged = make(map[string]struct{})
			}
			platformManaged[value] = struct{}{}
		}
		config = config.runtimeParent
	}
	for value := range platformManaged {
		delete(scriptSourced, value)
	}
	return scriptSourced
}

func runtimeSecretRawValueBytes(values map[string]struct{}) int {
	total := 0
	for value := range values {
		total += len(value)
	}
	return total
}

func hasRuntimeSecretReplacers(config *RedactionConfig) bool {
	if dyn := config.dynamicOverlay(); dyn != nil && len(dyn.secretReplacers) > 0 {
		return true
	}
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

// mergedRuntimeSecretValuesByOrigin folds an inbound config's runtime values
// together with the values a caller derived for the same group, WITHOUT losing
// origin.
//
// The inbound config is the only place the origin of its own values still exists,
// so it is read rather than flattened: a value it carries as script-sourced stays
// script-sourced, and everything else is platform-managed. Platform-managed always
// wins the collision, in either direction, so a project secret can never be
// downgraded by a retained import literal that happens to equal it.
//
// redactScriptSourced is the operator's config.log.redact_run_evidence setting
// for the script the values belong to. When it is false the script-sourced half
// is dropped entirely — including script-sourced values an inbound config
// (apexctl, generator RPC staging) carried in — so a caller can never re-enable
// literal redaction the script turned off. Platform-managed values are kept
// either way.
func mergedRuntimeSecretValuesByOrigin(
	config *RedactionConfig,
	platformManaged []string,
	scriptSourced []string,
	redactScriptSourced bool,
) ([]string, []string) {
	inboundScriptSourced := allRuntimeScriptSourcedValues(config)
	platformSet := make(map[string]struct{}, len(platformManaged))
	for value := range allRuntimeSecretValues(config) {
		if value == "" {
			continue
		}
		if _, marked := inboundScriptSourced[value]; marked {
			continue
		}
		platformSet[value] = struct{}{}
	}
	for _, value := range platformManaged {
		if value != "" {
			platformSet[value] = struct{}{}
		}
	}
	scriptSet := make(map[string]struct{}, len(inboundScriptSourced)+len(scriptSourced))
	for value := range inboundScriptSourced {
		scriptSet[value] = struct{}{}
	}
	for _, value := range scriptSourced {
		if value != "" {
			scriptSet[value] = struct{}{}
		}
	}
	for value := range platformSet {
		delete(scriptSet, value)
	}
	if !redactScriptSourced {
		return sortedRuntimeSecretValueList(platformSet), nil
	}
	return sortedRuntimeSecretValueList(platformSet), sortedRuntimeSecretValueList(scriptSet)
}

// inboundRuntimeRedactionBase returns the base config attachRuntimeSecretRedaction
// layers the merged runtime matcher onto. attachRuntimeSecretRedaction keeps the
// inbound config verbatim when runtime is nil, which would keep an inbound
// script-sourced matcher alive after mergedRuntimeSecretValuesByOrigin dropped
// it. When nothing survived the merge, the inbound matcher held no
// platform-managed value, so it is detached and only its rules and dynamic
// registry are kept.
func inboundRuntimeRedactionBase(config, runtime *RedactionConfig) *RedactionConfig {
	if runtime != nil || config == nil || config.publicationUnavailable() {
		return config
	}
	if len(allRuntimeSecretValues(config)) == 0 {
		return config
	}
	return cloneRedactionPolicy(config)
}

func sortedRuntimeSecretValueList(values map[string]struct{}) []string {
	ordered := make([]string, 0, len(values))
	for value := range values {
		ordered = append(ordered, value)
	}
	sort.Strings(ordered)
	return ordered
}

// runtimeSecretValueSetKey identifies one shared matcher. Origin is part of that
// identity: two groups whose platform-managed sets match but whose script-sourced
// sets differ must not share a compiled matcher, so each set is hashed with its
// own length-prefixed separator.
func runtimeSecretValueSetKey(sets ...[]string) [sha256.Size]byte {
	hash := sha256.New()
	var length [8]byte
	for index, values := range sets {
		binary.BigEndian.PutUint64(length[:], uint64(index))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(length[:])
		for _, value := range values {
			binary.BigEndian.PutUint64(length[:], uint64(len(value)))
			_, _ = hash.Write(length[:])
			_, _ = hash.Write([]byte(value))
		}
	}
	var key [sha256.Size]byte
	copy(key[:], hash.Sum(nil))
	return key
}

// attachRuntimeSecretRedaction keeps custom policy state detached per group
// while sharing the immutable exact-secret matcher for identical value sets.
func attachRuntimeSecretRedaction(config, runtime *RedactionConfig) *RedactionConfig {
	if config.publicationUnavailable() || runtime.publicationUnavailable() {
		return unavailableRuntimePublicationConfig
	}
	if runtime == nil {
		return config
	}
	if config == nil || (len(config.Rules) == 0 && len(config.compiled) == 0 && config.findDynamicRegistry() == nil) {
		if config != nil && !config.scrubsAuthoredEvidence() {
			return withAuthoredEvidenceScrubbing(runtime, false)
		}
		return runtime
	}
	detached := &RedactionConfig{
		authoredEvidenceVisible:    !config.scrubsAuthoredEvidence(),
		Rules:                      append([]RedactionRule(nil), config.Rules...),
		compiled:                   append([]payloadReplacement(nil), config.compiled...),
		secretReplacers:            runtime.secretReplacers,
		runtimeSecretValues:        runtime.runtimeSecretValues,
		runtimeSecretVariants:      runtime.runtimeSecretVariants,
		runtimeSecretRawBytes:      runtime.runtimeSecretRawBytes,
		runtimePatternBytes:        runtime.runtimePatternBytes,
		runtimeMatcherComplete:     runtime.runtimeMatcherComplete,
		runtimeScriptSourcedValues: runtime.runtimeScriptSourcedValues,
		runtimeParent:              runtime.runtimeParent,
		dynamic:                    config.findDynamicRegistry(),
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
	if result == nil {
		return
	}

	scrubSensitiveResultMetadata(result, config)
	result.ErrorMessage = scrubSensitiveTextWithConfig(result.ErrorMessage, config)
	if retainResponseBody {
		if strings.EqualFold(strings.TrimSpace(result.DependencyType), "java_http") && javaBinaryPayloadContainsSecret(result.ResponseBody, config) {
			result.ResponseBody = redactedPayloadValue
		} else {
			result.ResponseBody = scrubSensitiveBodyTextWithConfig(result.ResponseBody, config)
		}
	} else {
		result.ResponseBody = ""
	}
	result.PageURL = scrubSensitiveTextWithConfig(result.PageURL, config)
	result.PageTitle = scrubSensitiveTextWithConfig(result.PageTitle, config)
	result.HTML = scrubSensitiveBodyTextWithConfig(result.HTML, config)
	result.ConsoleEntries = scrubSensitiveConsoleEntries(result.ConsoleEntries, config)
	result.NetworkEntries = scrubSensitiveNetworkEntries(result.NetworkEntries, config)
	result.NetworkTrace = scrubSensitiveNetworkTrace(result.NetworkTrace, config)
	result.DOMSummary = scrubSensitiveDOMSummary(result.DOMSummary, config)
	result.Extractions = scrubSensitiveExtractions(result.Extractions, config)
	result.ProtocolEvidence = scrubSensitiveProtocolEvidence(result.ProtocolEvidence, config)
	if config.HasRuntimeSecretMaterial() {
		// Screenshots are opaque pixels/base64. Exact text replacement cannot
		// prove the rendered secret is gone, so governed runtime material makes
		// screenshot retention fail closed.
		result.ScreenshotBase64 = ""
		result.ScreenshotOmitted = false
	}
	if result.Snapshot == nil {
		return
	}

	// Request-side surfaces carry what the script authored; they follow
	// config.log.redact_run_evidence (scrubAuthored*). Response surfaces below
	// keep the full scrubber whatever the setting.
	if result.Snapshot.RequestURLNotAuthored {
		// A server-named URL (meta-refresh follow-up) is not script content.
		result.Snapshot.RequestURL = scrubSensitiveTextWithConfig(result.Snapshot.RequestURL, config)
	} else {
		result.Snapshot.RequestURL = scrubAuthoredTextWithConfig(result.Snapshot.RequestURL, config)
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(result.Snapshot.RequestMethod)), "grpc_") {
		// Binary metadata is base64-encoded as a whole value. Even a known
		// secret inside it may have no matching encoded substring because its
		// position changes the base64 alignment. Opaque values cannot safely be
		// published by the ordinary result and snapshot sinks.
		redactGRPCBinaryMetadata(result.Snapshot.RequestHeaders)
		redactGRPCBinaryMetadata(result.Snapshot.ResponseHeaders)
	}
	result.Snapshot.RequestHeaders = scrubAuthoredHeaders(result.Snapshot.RequestHeaders, config)
	if strings.EqualFold(strings.TrimSpace(result.DependencyType), "java_http") && javaBinaryAuthoredPayloadContainsSecret(result.Snapshot.RequestBody, config) {
		result.Snapshot.RequestBody = redactedPayloadValue
	} else {
		result.Snapshot.RequestBody = scrubAuthoredBodyTextWithConfig(result.Snapshot.RequestBody, config)
	}
	result.Snapshot.ResponseHeaders = scrubSensitiveHeaders(result.Snapshot.ResponseHeaders, config)
	previousResponseBody := result.Snapshot.ResponseBody
	if strings.EqualFold(strings.TrimSpace(result.DependencyType), "java_http") && javaBinaryPayloadContainsSecret(result.Snapshot.ResponseBody, config) {
		result.Snapshot.ResponseBody = redactedPayloadValue
	} else {
		result.Snapshot.ResponseBody = scrubSensitiveBodyTextWithConfig(result.Snapshot.ResponseBody, config)
	}
	if previousResponseBody != result.Snapshot.ResponseBody {
		result.Snapshot.ResponseBodyState = "redacted"
	}
	for i := range result.Snapshot.Resources {
		r := &result.Snapshot.Resources[i]
		r.RequestURL = scrubSensitiveTextWithConfig(r.RequestURL, config)
		r.RequestHeaders = scrubSensitiveHeaders(r.RequestHeaders, config)
		r.ResponseHeaders = scrubSensitiveHeaders(r.ResponseHeaders, config)
		previous := r.ResponseBody
		r.ResponseBody = scrubSensitiveBodyTextWithConfig(r.ResponseBody, config)
		if previous != r.ResponseBody {
			r.ResponseBodyState = "redacted"
		}
	}
	result.Snapshot.PageURL = scrubSensitiveTextWithConfig(result.Snapshot.PageURL, config)
	result.Snapshot.PageTitle = scrubSensitiveTextWithConfig(result.Snapshot.PageTitle, config)
	result.Snapshot.HTML = scrubSensitiveBodyTextWithConfig(result.Snapshot.HTML, config)
	result.Snapshot.ConsoleEntries = scrubSensitiveConsoleEntries(result.Snapshot.ConsoleEntries, config)
	result.Snapshot.NetworkEntries = scrubSensitiveNetworkEntries(result.Snapshot.NetworkEntries, config)
	result.Snapshot.DOMSummary = scrubSensitiveDOMSummary(result.Snapshot.DOMSummary, config)
	if config.HasRuntimeSecretMaterial() {
		result.Snapshot.ScreenshotBase64 = ""
		result.Snapshot.ScreenshotOmitted = false
	}
	result.Snapshot.AuthoredRequestRetained = authoredRequestRetained(config)
}

// authoredRequestRetained is the marker published with request-side evidence
// scrubbed under config: true only when the script left
// config.log.redact_run_evidence off, so downstream archive and report
// sanitizers know the request side already went through the run's exact
// platform-secret matcher and must keep its authored values.
func authoredRequestRetained(config *RedactionConfig) bool {
	return !config.scrubsAuthoredEvidence() && !config.publicationUnavailable()
}

func redactGRPCBinaryMetadata(headers map[string]string) {
	for key := range headers {
		if strings.HasSuffix(strings.ToLower(strings.TrimSpace(key)), "-bin") {
			headers[key] = redactedPayloadValue
		}
	}
}

// javaBinaryAuthoredPayloadContainsSecret is the request-side form: with the
// script's config.log.redact_run_evidence off, only exact runtime values and
// project rules (not the name/shape scrubber) decide whether an opaque authored
// payload is withheld.
func javaBinaryAuthoredPayloadContainsSecret(payload string, config *RedactionConfig) bool {
	if config.scrubsAuthoredEvidence() {
		return javaBinaryPayloadContainsSecret(payload, config)
	}
	return javaBinaryPayloadContainsSecretWith(payload, config, func(value string) string {
		return scrubAuthoredTextWithConfig(value, config)
	})
}

func javaBinaryPayloadContainsSecret(payload string, config *RedactionConfig) bool {
	if config == nil {
		return false
	}
	return javaBinaryPayloadContainsSecretWith(payload, config, config.ScrubText)
}

func javaBinaryPayloadContainsSecretWith(payload string, config *RedactionConfig, scrubText func(string) string) bool {
	if payload == "" || config == nil {
		return false
	}
	if scrubText(payload) != payload {
		return true
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
	if err != nil {
		raw = []byte(payload)
	}
	if len(raw) == 0 {
		return false
	}
	rawText := string(raw)
	if scrubText(rawText) != rawText {
		return true
	}
	start := -1
	for index, b := range raw {
		if b >= 0x20 && b <= 0x7e {
			if start < 0 {
				start = index
			}
			continue
		}
		if start >= 0 {
			if index-start >= 4 {
				segment := string(raw[start:index])
				if scrubText(segment) != segment {
					return true
				}
			}
			start = -1
		}
	}
	if start >= 0 && len(raw)-start >= 4 {
		segment := string(raw[start:])
		if scrubText(segment) != segment {
			return true
		}
	}
	return false
}

// scrubSensitiveResultMetadata covers the scalar dimensions copied into live
// metrics and generator archives. Business-transaction ActionName values,
// Group, and SourceTransaction are authored identities and must remain exactly
// as configured so metrics, debugger events, and reports always join back to
// the configured script and group. Ordinary and parent action names remain
// fully scrubbed because adapters can supply them dynamically.
//
// The remaining fields bypass response/snapshot evidence handling, so leaving
// them untouched would let a resolved secret in a correlation header,
// dependency label, adapter label, or imported source identifier survive an
// otherwise valid custom redaction policy.
//
// One matcher governs both dimensions and evidence. The earlier two-config form
// existed to keep HTTP cookie-jar snapshots out of stable metric labels; nothing
// harvested from the system under test enters the matcher any more, so the
// remaining material (platform secrets and sensitively NAMED extractions) is
// suppressed uniformly.
func scrubSensitiveResultMetadata(result *protocol.Result, config *RedactionConfig) {
	if result.BusinessTransaction {
		result.ActionName = scrubSensitiveIdentityWithConfig(result.ActionName, config)
	} else {
		result.ActionName = scrubSensitiveTextWithConfig(result.ActionName, config)
	}
	result.SampleKind = scrubSensitiveTextWithConfig(result.SampleKind, config)
	result.ParentActionName = scrubSensitiveTextWithConfig(result.ParentActionName, config)
	result.Group = scrubSensitiveIdentityWithConfig(result.Group, config)
	result.Region = scrubSensitiveTextWithConfig(result.Region, config)
	result.CorrelationID = scrubSensitiveTextWithConfig(result.CorrelationID, config)
	result.NetworkProfile = scrubSensitiveTextWithConfig(result.NetworkProfile, config)
	result.DependencyType = scrubSensitiveTextWithConfig(result.DependencyType, config)
	result.DependencyTarget = scrubSensitiveTextWithConfig(result.DependencyTarget, config)
	result.SourceStepID = scrubSensitiveTextWithConfig(result.SourceStepID, config)
	result.SourceAction = scrubSensitiveTextWithConfig(result.SourceAction, config)
	result.SourceActionName = scrubSensitiveTextWithConfig(result.SourceActionName, config)
	result.SourceTransaction = scrubSensitiveIdentityWithConfig(result.SourceTransaction, config)
	result.SourceProfile = scrubSensitiveTextWithConfig(result.SourceProfile, config)
	result.SourcePath = scrubSensitiveTextWithConfig(result.SourcePath, config)
	result.Phase = scrubSensitiveTextWithConfig(result.Phase, config)
	result.Hierarchy = scrubSensitiveTextWithConfig(result.Hierarchy, config)
	result.SourceUID = scrubSensitiveTextWithConfig(result.SourceUID, config)
	result.RequestType = scrubSensitiveTextWithConfig(result.RequestType, config)
	result.OperationKind = scrubSensitiveTextWithConfig(result.OperationKind, config)

	if result.ClientMetrics != nil {
		result.ClientMetrics.DocumentEvidence = scrubBrowserDocumentEvidence(result.ClientMetrics.DocumentEvidence, config)
		result.ClientMetrics.DeviceProfile = scrubSensitiveTextWithConfig(result.ClientMetrics.DeviceProfile, config)
		result.ClientMetrics.DevicePlatform = scrubSensitiveTextWithConfig(result.ClientMetrics.DevicePlatform, config)
		result.ClientMetrics.DeviceTier = scrubSensitiveTextWithConfig(result.ClientMetrics.DeviceTier, config)
		result.ClientMetrics.BrowserEngine = scrubSensitiveTextWithConfig(result.ClientMetrics.BrowserEngine, config)
	}
	if result.ServerTelemetry != nil {
		result.ServerTelemetry.JavaHTTP = result.ServerTelemetry.JavaHTTP.Retained()
		result.ServerTelemetry.WebSocket = result.ServerTelemetry.WebSocket.Clone()
		if result.ServerTelemetry.Messaging != nil && !result.ServerTelemetry.Messaging.Valid() {
			result.ServerTelemetry.Messaging = &protocol.MessagingDiagnostics{}
		} else {
			result.ServerTelemetry.Messaging = result.ServerTelemetry.Messaging.Clone()
		}
		if result.ServerTelemetry.GRPC != nil {
			result.ServerTelemetry.GRPC = result.ServerTelemetry.GRPC.Clone()
			result.ServerTelemetry.GRPC.Method = scrubSensitiveTextWithConfig(result.ServerTelemetry.GRPC.Method, config)
		}
		if result.ServerTelemetry.LLM != nil {
			result.ServerTelemetry.LLM = result.ServerTelemetry.LLM.Clone()
			result.ServerTelemetry.LLM.Model = scrubSensitiveTextWithConfig(result.ServerTelemetry.LLM.Model, config)
			result.ServerTelemetry.LLM.Dialect = scrubSensitiveTextWithConfig(result.ServerTelemetry.LLM.Dialect, config)
			result.ServerTelemetry.LLM.FinishReason = scrubSensitiveTextWithConfig(result.ServerTelemetry.LLM.FinishReason, config)
			result.ServerTelemetry.LLM.ErrorCode = scrubSensitiveTextWithConfig(result.ServerTelemetry.LLM.ErrorCode, config)
			result.ServerTelemetry.LLM.Region = scrubSensitiveTextWithConfig(result.ServerTelemetry.LLM.Region, config)
		}
		result.ServerTelemetry.RequestSizeClass = scrubSensitiveTextWithConfig(result.ServerTelemetry.RequestSizeClass, config)
		result.ServerTelemetry.ResponseSizeClass = scrubSensitiveTextWithConfig(result.ServerTelemetry.ResponseSizeClass, config)
		result.ServerTelemetry.SOAPFaultCode = scrubSensitiveTextWithConfig(result.ServerTelemetry.SOAPFaultCode, config)
		result.ServerTelemetry.SOAPFaultCategory = scrubSensitiveTextWithConfig(result.ServerTelemetry.SOAPFaultCategory, config)
		result.ServerTelemetry.SOAPNamespaceConflict = scrubSensitiveTextWithConfig(result.ServerTelemetry.SOAPNamespaceConflict, config)
		result.ServerTelemetry.SubscriptionID = scrubSensitiveTextWithConfig(result.ServerTelemetry.SubscriptionID, config)
		result.ServerTelemetry.PartitionID = scrubSensitiveTextWithConfig(result.ServerTelemetry.PartitionID, config)
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
	result.ProtocolEvidence = scrubSensitiveProtocolEvidence(result.ProtocolEvidence, config)
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
		if evidence := entries[i].BrowserEvidence.Clone(); evidence != nil {
			evidence.Origin = scrubSensitiveTextWithConfig(evidence.Origin, config)
			// An initiating origin that names a secret is withheld; the
			// initiator type alone stays valid.
			if evidence.InitiatorOrigin != "" && scrubSensitiveTextWithConfig(evidence.InitiatorOrigin, config) != evidence.InitiatorOrigin {
				evidence.InitiatorOrigin = ""
			}
			entries[i].BrowserEvidence = evidence.Clone()
		} else {
			entries[i].BrowserEvidence = nil
		}
		if har := entries[i].HAR.Clone(); har != nil {
			for _, body := range []*protocol.ReplayHARBody{har.RequestBody, har.ResponseBody} {
				if body != nil {
					body.MimeType = scrubSensitiveTextWithConfig(body.MimeType, config)
				}
			}
			if body := har.RequestBody; body != nil && (body.State == "retained" || body.Text != "") {
				body.Text = scrubNetworkEntryRequestBody(entries[i], body.Text, config)
			}
			if body := har.ResponseBody; body != nil && (body.State == "retained" || body.Text != "") {
				body.Text = scrubSensitiveBodyTextWithConfig(body.Text, config)
			}
			entries[i].HAR = har
		}
		entries[i].Trace = scrubSensitiveNetworkTrace(entries[i].Trace, config)
		// The request side of the action's own request (ScriptRequest) follows
		// config.log.redact_run_evidence. Page, redirect and other observed
		// traffic, and every response side, keep the full scrubber.
		if entries[i].ScriptRequest {
			entries[i].URL = scrubAuthoredTextWithConfig(entries[i].URL, config)
			entries[i].RequestHeaders = scrubScriptRequestHeaders(entries[i].RequestHeaders, entries[i].RequestCookieNotAuthored, config)
		} else {
			entries[i].URL = scrubSensitiveTextWithConfig(entries[i].URL, config)
			entries[i].RequestHeaders = scrubSensitiveHeaders(entries[i].RequestHeaders, config)
		}
		entries[i].RequestBody = scrubNetworkEntryRequestBody(entries[i], entries[i].RequestBody, config)
		entries[i].ResponseHeaders = scrubSensitiveHeaders(entries[i].ResponseHeaders, config)
		entries[i].AuthoredRequestRetained = entries[i].ScriptRequest && authoredRequestRetained(config)
	}
	return entries
}

// scrubScriptRequestHeaders scrubs the request headers of a ScriptRequest
// entry. They follow config.log.redact_run_evidence (scrubAuthoredHeaders),
// except a Cookie header the cookie jar filled from earlier server responses
// (cookieNotAuthored), which is server-sourced and keeps the full name mask. An
// Authorization header built from script-resolved credential halves (URL
// userinfo, the NTLM/Negotiate pre-challenge Basic) follows the setting like
// the halves; its platform-derived spellings are in the exact matcher.
func scrubScriptRequestHeaders(headers map[string]string, cookieNotAuthored bool, config *RedactionConfig) map[string]string {
	if !cookieNotAuthored || len(headers) == 0 {
		return scrubAuthoredHeaders(headers, config)
	}
	var observed map[string]string
	for key, value := range headers {
		name := strings.TrimSpace(key)
		if strings.EqualFold(name, "Cookie") {
			if observed == nil {
				observed = map[string]string{}
			}
			observed[key] = value
			delete(headers, key)
		}
	}
	headers = scrubAuthoredHeaders(headers, config)
	if len(observed) == 0 {
		return headers
	}
	if headers == nil {
		headers = make(map[string]string, len(observed))
	}
	for key, value := range scrubSensitiveHeaders(observed, config) {
		headers[key] = value
	}
	return headers
}

func scrubNetworkEntryRequestBody(entry protocol.NetworkEntry, body string, config *RedactionConfig) string {
	if entry.ScriptRequest {
		return scrubAuthoredBodyTextWithConfig(body, config)
	}
	return scrubSensitiveBodyTextWithConfig(body, config)
}

func scrubSensitiveNetworkTrace(trace *protocol.HTTPNetworkTrace, config *RedactionConfig) *protocol.HTTPNetworkTrace {
	if trace == nil {
		return nil
	}
	return trace.TransformStrings(func(value string) string {
		return scrubSensitiveTextWithConfig(value, config)
	})
}

// networkTraceEvidenceKey is the protocol_evidence entry that carries an
// HTTPNetworkTrace evidence map.
const networkTraceEvidenceKey = "network_trace"

// scrubSensitiveNetworkTraceEvidence scrubs a network_trace evidence entry
// through its typed model. The generic key-name rule would replace the
// cookie_names_sent and set_cookie_names_received lists with a redaction
// string because their keys contain "cookie"; those lists hold only cookie
// names (never values), so they stay lists and each name is value-scrubbed.
// Keys the model does not know are dropped. The result is JSON-native (lists
// are []any, numbers float64), the same shape normalizeEvidenceValue gives
// every other entry, so bounding it later is a no-op and completeness checks
// that compare scrubbed evidence with its bounded copy still hold.
func scrubSensitiveNetworkTraceEvidence(value any, config *RedactionConfig) (any, bool) {
	trace := protocol.HTTPNetworkTraceFromEvidence(value)
	if trace == nil {
		return nil, false
	}
	evidence := scrubSensitiveNetworkTrace(trace, config).EvidenceMap()
	if len(evidence) == 0 {
		return nil, false
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return nil, false
	}
	var normalized map[string]any
	if err := json.Unmarshal(encoded, &normalized); err != nil || len(normalized) == 0 {
		return nil, false
	}
	return normalized, true
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
	out := make(map[string]any, len(input))
	for key, value := range input {
		if key == networkTraceEvidenceKey {
			if trace, ok := scrubSensitiveNetworkTraceEvidence(value, config); ok {
				out[key] = trace
			}
			continue
		}
		if scrub.SensitiveName(key) {
			out[key] = redactedPayloadValue
			continue
		}
		out[key] = scrubSensitiveEvidenceValue(normalizeEvidenceValue(value), config)
	}
	return out
}

func normalizeEvidenceValue(value any) any {
	switch value.(type) {
	case nil, string, bool, float64, map[string]any, map[string]string, []any:
		return value
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var normalized any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return nil
	}
	return normalized
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
	if (scrub.Policy{}).SensitiveHeader(key) {
		return true
	}
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
	if value == "" {
		return ""
	}
	if config.publicationUnavailable() {
		return "runtime evidence detail unavailable"
	}
	if strings.TrimSpace(value) == "" {
		return value
	}

	// Exact runtime values and their reversible encodings must run first.
	// Generic credential regexes can otherwise rewrite only a prefix (for
	// example, at an escaped JSON quote) and prevent the exact matcher from
	// finding the remaining secret.
	redacted := scrubRuntimeSecretText(value, config)
	if config != nil {
		for _, pattern := range config.compiled {
			redacted = pattern.re.ReplaceAllString(redacted, pattern.repl)
		}
	}
	redacted = scrubRuntimeSecretText(redacted, config)
	// Preserve ordinary evidence verbatim. The label-mode scrubber removes
	// values in explicitly sensitive fields and URL credentials without applying
	// generic high-entropy or JWT guesses to unrelated evidence.
	return scrub.ValueLabel(redacted, scrub.Policy{})
}

// scrubAuthoredTextWithConfig scrubs script-authored REQUEST content: request
// URLs, request headers' values, request bodies, typed form values and action
// params. When the script turned config.log.redact_run_evidence on (or the
// config carries no script decision) it is scrubSensitiveTextWithConfig. When the
// setting is off, the built-in name/shape scrubber is skipped, so a password,
// client_secret or token the script itself authored stays visible; exact runtime
// values (platform-managed secrets and runtime-minted tokens) and the project's
// own redaction rules are still applied.
func scrubAuthoredTextWithConfig(value string, config *RedactionConfig) string {
	if config.scrubsAuthoredEvidence() {
		return scrubSensitiveTextWithConfig(value, config)
	}
	if value == "" {
		return ""
	}
	if config.publicationUnavailable() {
		return "runtime evidence detail unavailable"
	}
	if strings.TrimSpace(value) == "" {
		return value
	}
	redacted := scrubRuntimeSecretText(value, config)
	for _, pattern := range config.compiled {
		redacted = pattern.re.ReplaceAllString(redacted, pattern.repl)
	}
	return scrubRuntimeSecretText(redacted, config)
}

// scrubAuthoredBodyTextWithConfig is the request-body form of
// scrubAuthoredTextWithConfig. With the setting off it also withholds a
// WS-Security PasswordDigest derived from a value the exact matcher withholds
// (maskPlatformDerivedWSSecurityDigests): the keyed hash cannot be matched by
// value, and the element-name mask no longer applies to authored bodies.
func scrubAuthoredBodyTextWithConfig(value string, config *RedactionConfig) string {
	if config.scrubsAuthoredEvidence() {
		return scrubSensitiveBodyTextWithConfig(value, config)
	}
	if value != "" && !config.publicationUnavailable() {
		value = maskPlatformDerivedWSSecurityDigests(value, config)
	}
	return scrubAuthoredTextWithConfig(value, config)
}

// scrubAuthoredHeaders scrubs REQUEST headers. With authored scrubbing on it is
// scrubSensitiveHeaders. With it off a sensitively NAMED header (Authorization,
// Cookie, X-Api-Key, ...) keeps its authored value, but any exact runtime match
// in it — a platform-managed secret, its Basic/SASL composite, a runtime-minted
// token — masks the whole value, so a header carrying a platform secret is still
// withheld by name.
func scrubAuthoredHeaders(headers map[string]string, config *RedactionConfig) map[string]string {
	if config.scrubsAuthoredEvidence() {
		return scrubSensitiveHeaders(headers, config)
	}
	if len(headers) == 0 {
		return headers
	}
	for key, value := range headers {
		scrubbed := scrubAuthoredTextWithConfig(value, config)
		if scrubbed != value && isSensitiveHeaderName(key) {
			scrubbed = redactedPayloadValue
		}
		headers[key] = scrubbed
	}
	return headers
}

func scrubSensitiveBodyTextWithConfig(value string, config *RedactionConfig) string {
	if value == "" {
		return ""
	}
	if config.publicationUnavailable() {
		return "runtime evidence detail unavailable"
	}
	if strings.TrimSpace(value) == "" {
		return value
	}
	return scrub.BodyText(scrubSensitiveTextWithConfig(value, config))
}

// scrubSensitiveIdentityWithConfig marks an explicit no-redaction boundary for
// authored transaction and group identities. These names are user-visible
// aggregation keys and must never be rewritten, including when their text
// resembles a credential or matches a project redaction rule.
func scrubSensitiveIdentityWithConfig(value string, config *RedactionConfig) string {
	return value
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

func applyRuntimeSecretLayer(original string, layer *RedactionConfig, matchedOffsets []bool) bool {
	if layer == nil {
		return false
	}
	matched := false
	for _, replacer := range layer.secretReplacers {
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
	for variant := range layer.runtimeSecretVariants {
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
	return matched
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
	if dyn := config.dynamicOverlay(); dyn != nil {
		if applyRuntimeSecretLayer(original, dyn, matchedOffsets) {
			matched = true
		}
	}
	for depth := 0; config != nil && depth < maxRuntimeSecretRedactionLayers; depth++ {
		if applyRuntimeSecretLayer(original, config, matchedOffsets) {
			matched = true
		}
		config = config.runtimeParent
	}
	if !matched {
		return value
	}
	return expandRuntimeSecretMatches(original, matchedOffsets)
}

// runtimeSecretMatchedRuns returns the maximal spans of value that config's
// runtime matcher (every layer and the dynamic overlay) matches.
func runtimeSecretMatchedRuns(value string, config *RedactionConfig) []string {
	if value == "" || !hasRuntimeSecretReplacers(config) {
		return nil
	}
	matchedOffsets := make([]bool, len(value))
	matched := false
	if dyn := config.dynamicOverlay(); dyn != nil && applyRuntimeSecretLayer(value, dyn, matchedOffsets) {
		matched = true
	}
	for layer, depth := config, 0; layer != nil && depth < maxRuntimeSecretRedactionLayers; depth++ {
		if applyRuntimeSecretLayer(value, layer, matchedOffsets) {
			matched = true
		}
		layer = layer.runtimeParent
	}
	if !matched {
		return nil
	}
	var runs []string
	for offset := 0; offset < len(value); {
		if !matchedOffsets[offset] {
			offset++
			continue
		}
		end := offset
		for end < len(value) && matchedOffsets[end] {
			end++
		}
		runs = append(runs, value[offset:end])
		offset = end
	}
	return runs
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

func scrubBrowserDocumentEvidence(evidence *protocol.BrowserDocumentEvidence, config *RedactionConfig) *protocol.BrowserDocumentEvidence {
	evidence = evidence.Clone()
	if evidence == nil {
		return nil
	}
	for i := range evidence.Observations {
		o := &evidence.Observations[i]
		profile, group, region := o.DeviceProfile, o.Group, o.Region
		o.DeviceProfile = scrubSensitiveTextWithConfig(profile, config)
		o.Group = scrubSensitiveTextWithConfig(group, config)
		o.Region = scrubSensitiveTextWithConfig(region, config)
		selectorChanged := profile != o.DeviceProfile || group != o.Group || region != o.Region
		if o.Cohort != nil {
			version := o.Cohort.BrowserVersion
			o.Cohort.BrowserVersion = scrubSensitiveTextWithConfig(version, config)
			selectorChanged = selectorChanged || version != o.Cohort.BrowserVersion
		}
		if selectorChanged {
			// Redacted comparison identities cannot certify runtime equivalence.
			// Keep observed values while excluding them from qualified baselines.
			o.Qualified = false
			if o.Cohort != nil {
				o.Cohort.UnavailableReason = "cohort_unavailable"
			}
			evidence.Incomplete = true
		}
	}
	return evidence.Clone()
}

// ProtocolEvidenceAuthoredRequestMarker is the protocol_evidence key the engine
// sets (to true) when it scrubbed the request-describing entries of the
// evidence under a script that left config.log.redact_run_evidence off. Those
// entries (protocolEvidenceAuthoredKeys, and metadata.headers/params/soap) then
// hold what the script authored with only exact platform-managed values and
// project rules applied, so the archive sanitizer keeps them as they are.
const ProtocolEvidenceAuthoredRequestMarker = "authored_request_retained"

// protocolEvidenceAuthoredMetadataKeys are the protocol_evidence.metadata
// entries that describe the request the action authored (protocol.evidenceMetadata).
// Every other metadata entry (error, extractions, status, ...) is result or
// response content and keeps the full scrub.
var protocolEvidenceAuthoredMetadataKeys = map[string]struct{}{"headers": {}, "params": {}, "soap": {}}

// scrubSensitiveProtocolEvidence scrubs a result's protocol_evidence. With
// authored scrubbing on (or no script decision) it is scrubSensitiveEvidenceMap.
// With config.log.redact_run_evidence off the request-describing entries —
// target, operation, metadata.headers, metadata.params, metadata.soap — follow
// the authored rule (scrubAuthoredEvidenceValue): a sensitively named value is
// masked only when the exact runtime matcher or a project rule changes it.
// Response-derived entries (rows, records, transcript, extractions, error,
// network_trace, ...) keep the full scrubber.
func scrubSensitiveProtocolEvidence(input map[string]any, config *RedactionConfig) map[string]any {
	if len(input) == 0 {
		return input
	}
	if config.scrubsAuthoredEvidence() || config.publicationUnavailable() {
		if _, marked := input[ProtocolEvidenceAuthoredRequestMarker]; marked {
			unmarked := make(map[string]any, len(input))
			for key, value := range input {
				if key != ProtocolEvidenceAuthoredRequestMarker {
					unmarked[key] = value
				}
			}
			input = unmarked
		}
		return scrubSensitiveEvidenceMap(input, config)
	}
	rest := make(map[string]any, len(input))
	authored := map[string]any{}
	var metadata map[string]any
	for key, value := range input {
		switch key {
		case ProtocolEvidenceAuthoredRequestMarker:
		case "target", "operation":
			authored[key] = value
		case "metadata":
			if typed, ok := normalizeEvidenceValue(value).(map[string]any); ok {
				metadata = typed
			} else {
				rest[key] = value
			}
		default:
			rest[key] = value
		}
	}
	out := scrubSensitiveEvidenceMap(rest, config)
	if out == nil {
		out = make(map[string]any, len(authored)+2)
	}
	for key, value := range authored {
		out[key], _ = scrubAuthoredEvidenceValue(normalizeEvidenceValue(value), config)
	}
	if metadata != nil {
		metadataRest := make(map[string]any, len(metadata))
		metadataAuthored := map[string]any{}
		for key, value := range metadata {
			if _, ok := protocolEvidenceAuthoredMetadataKeys[key]; ok {
				metadataAuthored[key] = value
			} else {
				metadataRest[key] = value
			}
		}
		scrubbed := scrubSensitiveEvidenceMap(metadataRest, config)
		if scrubbed == nil {
			scrubbed = make(map[string]any, len(metadataAuthored))
		}
		for key, value := range metadataAuthored {
			scrubbed[key], _ = scrubAuthoredEvidenceValue(normalizeEvidenceValue(value), config)
		}
		out["metadata"] = scrubbed
	}
	out[ProtocolEvidenceAuthoredRequestMarker] = true
	return out
}

// scrubAuthoredEvidenceValue scrubs authored request evidence (see
// scrubSensitiveProtocolEvidence). Strings go through scrubAuthoredTextWithConfig.
// A sensitively named key keeps its authored value unless that scrub changed
// something inside it, in which case the whole value is withheld, matching
// scrubAuthoredHeaders. It reports whether anything changed.
func scrubAuthoredEvidenceValue(value any, config *RedactionConfig) (any, bool) {
	switch typed := value.(type) {
	case string:
		scrubbed := scrubAuthoredTextWithConfig(typed, config)
		return scrubbed, scrubbed != typed
	case []any:
		out := make([]any, len(typed))
		changed := false
		for index := range typed {
			var itemChanged bool
			out[index], itemChanged = scrubAuthoredEvidenceValue(typed[index], config)
			changed = changed || itemChanged
		}
		return out, changed
	case map[string]any:
		out := make(map[string]any, len(typed))
		changed := false
		for key, entry := range typed {
			scrubbed, entryChanged := scrubAuthoredEvidenceValue(normalizeEvidenceValue(entry), config)
			if entryChanged && (scrub.SensitiveName(key) || isSensitiveHeaderName(key)) {
				scrubbed = redactedPayloadValue
			}
			out[key] = scrubbed
			changed = changed || entryChanged
		}
		return out, changed
	case map[string]string:
		out := make(map[string]string, len(typed))
		changed := false
		for key, entry := range typed {
			scrubbed := scrubAuthoredTextWithConfig(entry, config)
			entryChanged := scrubbed != entry
			if entryChanged && (scrub.SensitiveName(key) || isSensitiveHeaderName(key)) {
				scrubbed = redactedPayloadValue
			}
			out[key] = scrubbed
			changed = changed || entryChanged
		}
		return out, changed
	default:
		return value, false
	}
}
