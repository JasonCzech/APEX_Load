package api

// Masked-secret protocol for runtime overrides.
//
// RuntimeSettingsOverride contains secret-bearing environment, URL, HTTP,
// browser, authentication, proxy, and header fields. This file keeps stored
// secrets from leaking on read and keeps a client that echoes the redaction
// sentinel back on write from clobbering the stored value:
//
//   - maskRuntimeSettings / maskStoredScenario redact sensitive Env values
//     (scrub.Redacted) on every scenario read, operating on clones so stored
//     state is never mutated.
//   - reconcileGroupRuntimeSecrets folds an incoming (possibly masked) override
//     against the prior stored one before persist: a value equal to the sentinel
//     means "unchanged" (keep stored), "" means "clear", anything else is a real
//     new value.
//   - existingGroupRuntime/existingRootRuntime locate the prior stored override by
//     a STABLE key (ScriptID+Name), not array index, so reorder/insert/delete on
//     update never misroutes a preserved secret to the wrong group.
//
// The override contract — and therefore this masking surface — widens with the
// secret-bearing sections (auth/proxy/TLS) added in Phase 2.

import (
	"net/url"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

const defaultManagedTestSparklinePoints = 60

type managedTestMaskOptions struct {
	IncludeHistory   bool
	HistorySince     *time.Time
	HistoryLimit     int
	IncludeSparkline bool
}

// sensitiveEnvKey reports whether a runtime Env variable name looks secret-bearing
// and so its value must be redacted before it leaves the server.
func sensitiveEnvKey(name string) bool {
	if scrub.SensitiveName(name) {
		return true
	}
	normalized := strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(strings.ToLower(strings.TrimSpace(name)))
	// URL-shaped deployment variables frequently embed credentials while their
	// names contain none of the traditional password/token markers. Mask these
	// database/cache/broker DSN aliases as a class; public runtime projections do
	// not need controller-internal connection topology even when a particular
	// value happens to be passwordless.
	for _, marker := range []string{
		"database_url", "db_url", "jdbc_url", "redis_url", "cache_url",
		"broker_url", "amqp_url", "dsn", "connection_string",
	} {
		if normalized == marker || strings.HasSuffix(normalized, "_"+marker) {
			return true
		}
	}
	return false
}

func sensitiveEnvValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "bearer ") || strings.HasPrefix(lower, "basic ") || strings.HasPrefix(lower, "apikey ") ||
		strings.Contains(lower, "sharedaccesskey=") || strings.Contains(lower, "accountkey=") || strings.Contains(lower, "sig=") {
		return true
	}
	if (strings.HasPrefix(trimmed, "AKIA") || strings.HasPrefix(trimmed, "ASIA")) && len(trimmed) >= 16 {
		return true
	}
	if scrub.Value(trimmed, scrub.Policy{}) != trimmed {
		return true
	}
	// net/url represents scheme-less userinfo strings such as
	// "user:password@example.test" as opaque URLs with no parsed User field.
	// They are still credential-bearing and maskURLUserinfo deliberately treats
	// them fail-closed. Reuse that boundary for Env projections too.
	if maskURLUserinfo(trimmed) != trimmed {
		return true
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return false
	}
	if parsed.User != nil {
		return true
	}
	for key, values := range parsed.Query() {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if !scrub.SensitiveName(key) && normalized != "sig" && !strings.HasSuffix(normalized, "signature") {
			continue
		}
		for _, candidate := range values {
			if strings.TrimSpace(candidate) != "" {
				return true
			}
		}
	}
	return false
}

// maskRuntimeSettings returns a clone of the override with sensitive Env values
// replaced by scrub.Redacted. The input is never mutated; a nil input returns nil.
func maskRuntimeSettings(o *RuntimeSettingsOverride) *RuntimeSettingsOverride {
	if o == nil {
		return nil
	}
	clone := cloneRuntimeSettings(o)
	for key, value := range clone.Env {
		if value != "" && (sensitiveEnvKey(key) || sensitiveEnvValue(value)) {
			clone.Env[key] = scrub.Redacted
		}
	}
	clone.BaseURL = maskURLSecrets(clone.BaseURL)
	maskHTTPSecrets(clone.HTTP)
	maskBrowserSecrets(clone.BrowserConfig)
	return clone
}

// maskHTTPSecrets redacts the secret-bearing fields and header values of an HTTP
// override section in place on a cloned section.
func maskHTTPSecrets(http *dsl.HTTPConfig) {
	if http == nil {
		return
	}
	if http.Auth.Password != "" {
		http.Auth.Password = scrub.Redacted
	}
	if http.Auth.Token != "" {
		http.Auth.Token = scrub.Redacted
	}
	if http.Proxy.Username != "" {
		http.Proxy.Username = scrub.Redacted
	}
	if http.Proxy.Password != "" {
		http.Proxy.Password = scrub.Redacted
	}
	http.Proxy.URL = maskURLSecrets(http.Proxy.URL)
	policy := scrub.Policy{}
	for name, value := range http.DefaultHeaders {
		if value != "" && policy.SensitiveHeader(name) {
			if strings.EqualFold(strings.TrimSpace(name), "Cookie") {
				if masked, ok := maskCookieHeaderValue(value); ok {
					http.DefaultHeaders[name] = masked
					continue
				}
			}
			http.DefaultHeaders[name] = scrub.Redacted
		}
	}
	for index := range http.DefaultHeaderList {
		header := &http.DefaultHeaderList[index]
		if header.Value != "" && policy.SensitiveHeader(header.Name) {
			if strings.EqualFold(strings.TrimSpace(header.Name), "Cookie") {
				if masked, ok := maskCookieHeaderValue(header.Value); ok {
					header.Value = masked
					continue
				}
			}
			header.Value = scrub.Redacted
		}
	}
}

type cookieHeaderPair struct {
	name  string
	value string
}

func parseCookieHeaderValue(value string) ([]cookieHeaderPair, bool) {
	parts := strings.Split(value, ";")
	pairs := make([]cookieHeaderPair, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		separator := strings.IndexByte(part, '=')
		if separator <= 0 {
			return nil, false
		}
		name := strings.TrimSpace(part[:separator])
		if name == "" || strings.ContainsAny(name, "\r\n") {
			return nil, false
		}
		pairs = append(pairs, cookieHeaderPair{
			name:  name,
			value: strings.TrimSpace(part[separator+1:]),
		})
	}
	return pairs, len(pairs) > 0
}

func formatCookieHeaderValue(pairs []cookieHeaderPair) string {
	values := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		values = append(values, pair.name+"="+pair.value)
	}
	return strings.Join(values, "; ")
}

func maskCookieHeaderValue(value string) (string, bool) {
	pairs, ok := parseCookieHeaderValue(value)
	if !ok {
		return "", false
	}
	for index := range pairs {
		pairs[index].value = scrub.Redacted
	}
	return formatCookieHeaderValue(pairs), true
}

func maskBrowserSecrets(browser *dsl.BrowserConfig) {
	if browser == nil {
		return
	}
	if browser.HTTPCredentials.Password != "" {
		browser.HTTPCredentials.Password = scrub.Redacted
	}
	if browser.Proxy.Username != "" {
		browser.Proxy.Username = scrub.Redacted
	}
	if browser.Proxy.Password != "" {
		browser.Proxy.Password = scrub.Redacted
	}
	browser.Proxy.Server = maskURLSecrets(browser.Proxy.Server)
	for index := range browser.ClientCertificates {
		if browser.ClientCertificates[index].Passphrase != "" {
			browser.ClientCertificates[index].Passphrase = scrub.Redacted
		}
	}
}

func maskURLUserinfo(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return raw
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		if strings.Contains(trimmed, "@") {
			return scrub.Redacted
		}
		return raw
	}
	if parsed.User == nil {
		if strings.Contains(trimmed, "@") {
			return scrub.Redacted
		}
		return raw
	}
	parsed.User = url.User(scrub.Redacted)
	return parsed.String()
}

// maskURLSecrets redacts both authority userinfo and credential-shaped query or
// fragment values. Query credentials are common in generated service URLs, and
// runtime overrides are returned to project viewers and persisted in archives.
func maskURLSecrets(raw string) string {
	masked := maskURLUserinfo(raw)
	if masked == scrub.Redacted {
		return masked
	}
	return scrub.ValueURL(masked, scrub.Policy{})
}

func isMaskedURLUserinfo(raw string) bool {
	if raw == scrub.Redacted {
		return true
	}
	parsed, err := parseURLWithMaskedUserinfo(strings.TrimSpace(raw))
	return err == nil && parsed.User != nil && parsed.User.Username() == scrub.Redacted
}

// parseURLWithMaskedUserinfo accepts the display-safe raw sentinel emitted by
// scrub.ValueURL in an authority. net/url correctly rejects raw '[' and ']'
// inside userinfo, so encode only the authority sentinel before parsing; query
// and fragment sentinels must remain byte-identical for component reconciliation.
func parseURLWithMaskedUserinfo(raw string) (*url.URL, error) {
	escapedUser := url.User(scrub.Redacted).String()
	escapedPassword := url.UserPassword(scrub.Redacted, scrub.Redacted).String()
	raw = strings.ReplaceAll(raw, scrub.Redacted+":"+scrub.Redacted+"@", escapedPassword+"@")
	raw = strings.ReplaceAll(raw, scrub.Redacted+"@", escapedUser+"@")
	return url.Parse(raw)
}

// reconcileMaskedURL restores only redacted URL components from the prior
// stored value. Non-secret edits to the host, path, or other query parameters
// survive an ordinary read/edit/write cycle, while an orphaned sentinel is
// removed rather than persisted as a runnable credential.
func reconcileMaskedURL(incoming, prior string) string {
	if !strings.Contains(incoming, scrub.Redacted) && !isMaskedURLUserinfo(incoming) {
		return incoming
	}
	if incoming == scrub.Redacted {
		return prior
	}
	parsed, err := parseURLWithMaskedUserinfo(strings.TrimSpace(incoming))
	if err != nil {
		return prior
	}
	var previous *url.URL
	if strings.TrimSpace(prior) != "" {
		previous, err = url.Parse(strings.TrimSpace(prior))
		if err != nil {
			return prior
		}
	}
	if parsed.User != nil && parsed.User.Username() == scrub.Redacted {
		parsed.User = nil
		if previous != nil {
			parsed.User = previous.User
		}
	}
	priorQuery := ""
	priorFragment := ""
	if previous != nil {
		priorQuery = previous.RawQuery
		priorFragment = previous.Fragment
	}
	parsed.RawQuery = reconcileMaskedURLSegment(parsed.RawQuery, priorQuery)
	parsed.Fragment = reconcileMaskedURLSegment(parsed.Fragment, priorFragment)
	parsed.RawFragment = ""
	return parsed.String()
}

func reconcileMaskedURLSegment(incoming, prior string) string {
	if !strings.Contains(incoming, scrub.Redacted) {
		return incoming
	}
	priorValues := make(map[string][]string)
	for _, part := range strings.Split(prior, "&") {
		key, value, hasValue := strings.Cut(part, "=")
		if !hasValue {
			priorValues[""] = append(priorValues[""], part)
			continue
		}
		priorValues[normalizedURLParamKey(key)] = append(priorValues[normalizedURLParamKey(key)], value)
	}
	occurrences := make(map[string]int)
	parts := strings.Split(incoming, "&")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		key, value, hasValue := strings.Cut(part, "=")
		lookupKey := ""
		if hasValue {
			lookupKey = normalizedURLParamKey(key)
		}
		index := occurrences[lookupKey]
		occurrences[lookupKey] = index + 1
		if (hasValue && value == scrub.Redacted) || (!hasValue && part == scrub.Redacted) {
			values := priorValues[lookupKey]
			if index >= len(values) {
				continue
			}
			if hasValue {
				out = append(out, key+"="+values[index])
			} else {
				out = append(out, values[index])
			}
			continue
		}
		out = append(out, part)
	}
	return strings.Join(out, "&")
}

func normalizedURLParamKey(raw string) string {
	if decoded, err := url.QueryUnescape(raw); err == nil {
		raw = decoded
	}
	return strings.ToLower(raw)
}

// maskRunArchiveRecord preserves portable run facts while removing controller
// filesystem and object-store topology from viewer-facing test representations.
func maskRunArchiveRecord(record *storage.RunArchiveRecord) *storage.RunArchiveRecord {
	if record == nil {
		return nil
	}
	masked := *record
	masked.ArchiveBackend = ""
	masked.ArchivePath = ""
	masked.ManifestPath = ""
	masked.ArchiveURI = ""
	masked.ManifestURI = ""
	return &masked
}

// maskStoredScenario returns a shallow copy of the scenario with root and
// per-group runtime Env secrets redacted, so a GET never leaks stored secrets.
// The original scenario (and its overrides) are left untouched.
func maskStoredScenario(scenario *StoredScenario) *StoredScenario {
	if scenario == nil {
		return nil
	}
	masked := *scenario
	masked.RuntimeSettings = maskRuntimeSettings(scenario.RuntimeSettings)
	if len(scenario.Scripts) > 0 {
		refs := cloneScenarioScriptRefs(scenario.Scripts)
		for i := range refs {
			refs[i].RuntimeSettings = maskRuntimeSettings(refs[i].RuntimeSettings)
		}
		masked.Scripts = refs
	}
	return &masked
}

// maskStoredScenarios masks each scenario in a list (used by list endpoints).
func maskStoredScenarios(scenarios []*StoredScenario) []*StoredScenario {
	if scenarios == nil {
		return nil
	}
	masked := make([]*StoredScenario, len(scenarios))
	for i, scenario := range scenarios {
		masked[i] = maskStoredScenario(scenario)
	}
	return masked
}

// maskManagedTest returns a shallow copy of a test with its runtime-override secrets
// (root and per-group) redacted, so test reads never leak stored secrets. The
// unexported compiled-script field is not JSON-serialized, so only RuntimeSettings
// needs masking.
func maskManagedTest(test *ManagedTest) *ManagedTest {
	return maskManagedTestWithOptions(test, managedTestMaskOptions{IncludeSparkline: true})
}

func maskManagedTestWithOptions(test *ManagedTest, opts managedTestMaskOptions) *ManagedTest {
	if test == nil {
		return nil
	}
	masked := *test
	if opts.IncludeHistory {
		masked.History = selectLiveMetricsHistory(test.History, opts.HistorySince, opts.HistoryLimit)
	} else {
		masked.History = nil
	}
	if opts.IncludeSparkline {
		if len(test.History) == 0 && len(test.Sparkline) > 0 {
			masked.Sparkline = append([]TestSparklinePoint(nil), test.Sparkline...)
		} else {
			masked.Sparkline = buildManagedTestSparkline(test.History, test.LiveMetrics, defaultManagedTestSparklinePoints)
		}
	} else {
		masked.Sparkline = nil
	}
	masked.RuntimeSettings = maskRuntimeSettings(test.RuntimeSettings)
	masked.Archive = maskRunArchiveRecord(test.Archive)
	if len(test.Groups) > 0 {
		groups := make([]TestGroup, len(test.Groups))
		copy(groups, test.Groups)
		for i := range groups {
			groups[i].RuntimeSettings = maskRuntimeSettings(groups[i].RuntimeSettings)
		}
		masked.Groups = groups
	}
	return &masked
}

// maskManagedTests masks each test in a list (used by the test list endpoints).
func maskManagedTests(tests []*ManagedTest) []*ManagedTest {
	if tests == nil {
		return nil
	}
	masked := make([]*ManagedTest, len(tests))
	for i, t := range tests {
		masked[i] = maskManagedTestWithOptions(t, managedTestMaskOptions{IncludeSparkline: true})
	}
	return masked
}

// maskTestGroupsRuntimeSettings clones test groups and redacts every runtime
// secret-bearing field. Archive and manifest surfaces use this helper because
// they serialize TestGroup directly instead of going through maskManagedTest.
func maskTestGroupsRuntimeSettings(groups []TestGroup) []TestGroup {
	masked := cloneArchiveTestGroups(groups)
	for i := range masked {
		masked[i].RuntimeSettings = maskRuntimeSettings(masked[i].RuntimeSettings)
	}
	return masked
}

// maskRunArchiveProvenance returns a copy safe to persist or return to readers.
// Provenance is long-lived evidence and may be downloaded by project viewers,
// so it must never retain runnable credentials.
func maskRunArchiveProvenance(provenance RunArchiveProvenance) RunArchiveProvenance {
	provenance.RuntimeSettings = maskRuntimeSettings(provenance.RuntimeSettings)
	provenance.Groups = maskTestGroupsRuntimeSettings(provenance.Groups)
	return provenance
}

func maskRunArchiveManifest(manifest *RunArchiveManifest) *RunArchiveManifest {
	if manifest == nil {
		return nil
	}
	masked := *manifest
	masked.Provenance = maskRunArchiveProvenance(manifest.Provenance)
	return &masked
}

func selectLiveMetricsHistory(history []*LiveMetrics, since *time.Time, limit int) []*LiveMetrics {
	if len(history) == 0 {
		return nil
	}
	start := 0
	if since != nil && !since.IsZero() {
		start = len(history)
		for i, point := range history {
			if point != nil && point.Timestamp.After(*since) {
				start = i
				break
			}
		}
	}
	selected := history[start:]
	if limit > 0 && len(selected) > limit {
		selected = selected[len(selected)-limit:]
	}
	if len(selected) == 0 {
		return nil
	}
	return append([]*LiveMetrics(nil), selected...)
}

func buildManagedTestSparkline(history []*LiveMetrics, live *LiveMetrics, limit int) []TestSparklinePoint {
	if limit <= 0 {
		return nil
	}
	points := history
	if len(points) == 0 && live != nil {
		points = []*LiveMetrics{live}
	}
	if len(points) == 0 {
		return nil
	}
	count := len(points)
	if count > limit {
		count = limit
	}
	out := make([]TestSparklinePoint, 0, count)
	if count == 1 {
		if point := points[len(points)-1]; point != nil {
			out = append(out, TestSparklinePoint{Timestamp: point.Timestamp, TPS: point.TPS})
		}
		return out
	}
	last := len(points) - 1
	for i := 0; i < count; i++ {
		index := i * last / (count - 1)
		point := points[index]
		if point == nil {
			continue
		}
		out = append(out, TestSparklinePoint{Timestamp: point.Timestamp, TPS: point.TPS})
	}
	return out
}

// reconcileGroupRuntimeSecrets folds an incoming (possibly client-masked) override
// against the prior stored override for the same group before persist. Per Env key:
//
//	value == scrub.Redacted -> keep the prior stored value (client echoed the mask)
//	value == ""             -> clear (drop the key)
//	any other value         -> persist verbatim (a real new value)
//
// Non-Env fields pass through unchanged. The returned override is a clone; inputs
// are never mutated.
func reconcileGroupRuntimeSecrets(incoming, prior *RuntimeSettingsOverride) *RuntimeSettingsOverride {
	if incoming == nil {
		return nil
	}
	result := cloneRuntimeSettings(incoming)
	if result.HTTP != nil {
		var priorHTTP *dsl.HTTPConfig
		if prior != nil {
			priorHTTP = prior.HTTP
		}
		reconcileHTTPSecrets(result.HTTP, priorHTTP)
	}
	if result.BrowserConfig != nil {
		var priorBrowser *dsl.BrowserConfig
		if prior != nil {
			priorBrowser = prior.BrowserConfig
		}
		reconcileBrowserSecrets(result.BrowserConfig, priorBrowser)
	}
	priorBaseURL := ""
	if prior != nil {
		priorBaseURL = prior.BaseURL
	}
	result.BaseURL = reconcileMaskedURL(result.BaseURL, priorBaseURL)
	if len(result.Env) > 0 {
		var priorEnv map[string]string
		if prior != nil {
			priorEnv = prior.Env
		}
		for key, value := range result.Env {
			switch {
			case value == scrub.Redacted:
				if priorValue, ok := priorEnv[key]; ok {
					result.Env[key] = priorValue
				} else {
					delete(result.Env, key)
				}
			case value == "":
				delete(result.Env, key)
			}
		}
		if len(result.Env) == 0 {
			result.Env = nil
		}
	}
	return result
}

// reconcileHTTPSecrets restores HTTP secret fields the client echoed back as the
// redaction sentinel from the prior stored section; "" clears, any other value persists.
func reconcileHTTPSecrets(incoming, prior *dsl.HTTPConfig) {
	if incoming == nil {
		return
	}
	priorAuthPassword, priorAuthToken, priorProxyUsername, priorProxyPassword := "", "", "", ""
	if prior != nil {
		priorAuthPassword = prior.Auth.Password
		priorAuthToken = prior.Auth.Token
		priorProxyUsername = prior.Proxy.Username
		priorProxyPassword = prior.Proxy.Password
	}
	if incoming.Auth.Password == scrub.Redacted {
		incoming.Auth.Password = priorAuthPassword
	}
	if incoming.Auth.Token == scrub.Redacted {
		incoming.Auth.Token = priorAuthToken
	}
	if incoming.Proxy.Username == scrub.Redacted {
		incoming.Proxy.Username = priorProxyUsername
	}
	if incoming.Proxy.Password == scrub.Redacted {
		incoming.Proxy.Password = priorProxyPassword
	}
	priorProxyURL := ""
	if prior != nil {
		priorProxyURL = prior.Proxy.URL
	}
	incoming.Proxy.URL = reconcileMaskedURL(incoming.Proxy.URL, priorProxyURL)
	policy := scrub.Policy{}
	for name, value := range incoming.DefaultHeaders {
		if !policy.SensitiveHeader(name) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "Cookie") && value != scrub.Redacted && value != "" {
			priorValue, _ := caseInsensitiveMapValue(priorDefaultHeaders(prior), name)
			if reconciled, keep := reconcileMaskedCookieHeader(value, []string{priorValue}); keep {
				incoming.DefaultHeaders[name] = reconciled
			} else {
				delete(incoming.DefaultHeaders, name)
			}
			continue
		}
		switch value {
		case scrub.Redacted:
			if priorValue, ok := caseInsensitiveMapValue(priorDefaultHeaders(prior), name); ok {
				incoming.DefaultHeaders[name] = priorValue
			} else {
				delete(incoming.DefaultHeaders, name)
			}
		case "":
			delete(incoming.DefaultHeaders, name)
		}
	}
	if len(incoming.DefaultHeaders) == 0 {
		incoming.DefaultHeaders = nil
	}
	priorOccurrences := orderedHeaderOccurrenceCounts(prior)
	incomingOccurrences := make(map[string]int)
	for _, header := range incoming.DefaultHeaderList {
		if policy.SensitiveHeader(header.Name) {
			incomingOccurrences[strings.ToLower(strings.TrimSpace(header.Name))]++
		}
	}
	priorCookieValues := priorOrderedCookieHeaderValues(prior)
	occurrences := make(map[string]int)
	filtered := incoming.DefaultHeaderList[:0]
	for _, header := range incoming.DefaultHeaderList {
		canonical := strings.ToLower(strings.TrimSpace(header.Name))
		ordinal := occurrences[canonical]
		occurrences[canonical]++
		if !policy.SensitiveHeader(header.Name) {
			filtered = append(filtered, header)
			continue
		}
		if canonical == "cookie" && header.Value != scrub.Redacted && header.Value != "" {
			if reconciled, keep := reconcileMaskedCookieHeader(header.Value, priorCookieValues); keep {
				header.Value = reconciled
				filtered = append(filtered, header)
			}
			continue
		}
		switch header.Value {
		case scrub.Redacted:
			// With duplicate sensitive names, an ordinal is trustworthy only
			// while the occurrence count is unchanged. If a client deleted one
			// of several masked rows, fail closed instead of restoring a
			// different stored secret into the surviving row.
			countStable := priorOccurrences[canonical] == 1 ||
				priorOccurrences[canonical] == incomingOccurrences[canonical]
			if priorValue, ok := priorDefaultHeaderListValue(prior, header.Name, ordinal); ok && countStable {
				header.Value = priorValue
				filtered = append(filtered, header)
			}
		case "":
			// Empty sensitive values explicitly remove the occurrence.
		default:
			filtered = append(filtered, header)
		}
	}
	if len(filtered) == 0 {
		incoming.DefaultHeaderList = nil
	} else {
		incoming.DefaultHeaderList = filtered
	}
}

func reconcileMaskedCookieHeader(incoming string, priorValues []string) (string, bool) {
	pairs, ok := parseCookieHeaderValue(incoming)
	if !ok {
		return incoming, incoming != ""
	}
	hasSentinel := false
	for _, pair := range pairs {
		if pair.value == scrub.Redacted {
			hasSentinel = true
			break
		}
	}
	if !hasSentinel {
		return incoming, true
	}
	priorByName := make(map[string][]string)
	for _, value := range priorValues {
		priorPairs, parsed := parseCookieHeaderValue(value)
		if !parsed {
			continue
		}
		for _, pair := range priorPairs {
			key := strings.TrimSpace(pair.name)
			priorByName[key] = append(priorByName[key], pair.value)
		}
	}
	filtered := pairs[:0]
	for _, pair := range pairs {
		if pair.value != scrub.Redacted {
			filtered = append(filtered, pair)
			continue
		}
		// Cookie names provide a stable reconciliation key. Duplicate stored
		// names are ambiguous without row IDs, so their sentinels are removed.
		values := priorByName[strings.TrimSpace(pair.name)]
		if len(values) == 1 {
			pair.value = values[0]
			filtered = append(filtered, pair)
		}
	}
	if len(filtered) == 0 {
		return "", false
	}
	return formatCookieHeaderValue(filtered), true
}

func priorDefaultHeaders(prior *dsl.HTTPConfig) map[string]string {
	if prior == nil {
		return nil
	}
	return prior.DefaultHeaders
}

func priorDefaultHeaderListValue(prior *dsl.HTTPConfig, name string, ordinal int) (string, bool) {
	if prior == nil {
		return "", false
	}
	match := 0
	for _, header := range prior.DefaultHeaderList {
		if !strings.EqualFold(strings.TrimSpace(header.Name), strings.TrimSpace(name)) {
			continue
		}
		if match == ordinal {
			return header.Value, true
		}
		match++
	}
	return "", false
}

func orderedHeaderOccurrenceCounts(prior *dsl.HTTPConfig) map[string]int {
	counts := make(map[string]int)
	if prior == nil {
		return counts
	}
	policy := scrub.Policy{}
	for _, header := range prior.DefaultHeaderList {
		if policy.SensitiveHeader(header.Name) {
			counts[strings.ToLower(strings.TrimSpace(header.Name))]++
		}
	}
	return counts
}

func priorOrderedCookieHeaderValues(prior *dsl.HTTPConfig) []string {
	if prior == nil {
		return nil
	}
	values := make([]string, 0)
	for _, header := range prior.DefaultHeaderList {
		if strings.EqualFold(strings.TrimSpace(header.Name), "Cookie") {
			values = append(values, header.Value)
		}
	}
	return values
}

func caseInsensitiveMapValue(values map[string]string, key string) (string, bool) {
	for candidate, value := range values {
		if strings.EqualFold(candidate, key) {
			return value, true
		}
	}
	return "", false
}

func reconcileBrowserSecrets(incoming, prior *dsl.BrowserConfig) {
	if incoming == nil {
		return
	}
	if incoming.HTTPCredentials.Password == scrub.Redacted {
		incoming.HTTPCredentials.Password = ""
		if prior != nil {
			incoming.HTTPCredentials.Password = prior.HTTPCredentials.Password
		}
	}
	if incoming.Proxy.Username == scrub.Redacted {
		incoming.Proxy.Username = ""
		if prior != nil {
			incoming.Proxy.Username = prior.Proxy.Username
		}
	}
	if incoming.Proxy.Password == scrub.Redacted {
		incoming.Proxy.Password = ""
		if prior != nil {
			incoming.Proxy.Password = prior.Proxy.Password
		}
	}
	priorProxyServer := ""
	if prior != nil {
		priorProxyServer = prior.Proxy.Server
	}
	incoming.Proxy.Server = reconcileMaskedURL(incoming.Proxy.Server, priorProxyServer)
	for index := range incoming.ClientCertificates {
		if incoming.ClientCertificates[index].Passphrase != scrub.Redacted {
			continue
		}
		incoming.ClientCertificates[index].Passphrase = ""
		if priorCertificate := matchingPriorBrowserCertificate(prior, incoming.ClientCertificates[index]); priorCertificate != nil {
			incoming.ClientCertificates[index].Passphrase = priorCertificate.Passphrase
		}
	}
}

func matchingPriorBrowserCertificate(prior *dsl.BrowserConfig, incoming dsl.BrowserClientCertificate) *dsl.BrowserClientCertificate {
	if prior == nil {
		return nil
	}
	var match *dsl.BrowserClientCertificate
	for index := range prior.ClientCertificates {
		candidate := &prior.ClientCertificates[index]
		if candidate.Origin != incoming.Origin || candidate.CertPath != incoming.CertPath || candidate.KeyPath != incoming.KeyPath || candidate.PfxPath != incoming.PfxPath {
			continue
		}
		if match != nil {
			return nil
		}
		match = candidate
	}
	return match
}

// existingRootRuntime returns the prior stored scenario-root runtime override.
func existingRootRuntime(existing *StoredScenario) *RuntimeSettingsOverride {
	if existing == nil {
		return nil
	}
	return existing.RuntimeSettings
}

// existingGroupRuntime finds the prior stored per-group runtime override for the
// group being saved, matched by a stable identity (ScriptID+Name, then ScriptID
// alone when unambiguous) rather than array index, so reorder/insert/delete on
// update never misroutes a preserved secret.
func existingGroupRuntime(existing *StoredScenario, group CreateGroupEntry) *RuntimeSettingsOverride {
	if existing == nil {
		return nil
	}
	groupName := strings.TrimSpace(group.Name)
	for _, ref := range existing.Scripts {
		if ref.ScriptID == group.ScriptID && strings.TrimSpace(ref.Name) == groupName {
			return ref.RuntimeSettings
		}
	}
	// Fall back to ScriptID-only when the script slot is unambiguous (name changed
	// or absent). If ambiguous, return nil — a masked value with no prior match is
	// then dropped rather than persisted, which fails closed (secret lost, never
	// leaked or corrupted onto the wrong group).
	var match *RuntimeSettingsOverride
	count := 0
	for _, ref := range existing.Scripts {
		if ref.ScriptID == group.ScriptID {
			match = ref.RuntimeSettings
			count++
		}
	}
	if count == 1 {
		return match
	}
	return nil
}
