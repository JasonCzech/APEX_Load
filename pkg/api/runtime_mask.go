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
	"github.com/jasonczech/apexload/pkg/javarunner"
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
// Library export, sync-link and import-claim hashes, manifests and provenance
// use this form, so it never changes for stored content.
func maskRuntimeSettings(o *RuntimeSettingsOverride) *RuntimeSettingsOverride {
	return maskRuntimeSettingsShowingReferences(o, false)
}

// maskRuntimeSettingsForEditor is maskRuntimeSettings for a scenario response
// the editor loads. A value that is only a project-secret reference names a
// secret rather than holding one, so it stays readable, as in a script, and a
// reopened scenario shows which secret it binds; a controller-owned runtime
// reference stays masked, since an editor cannot author it.
func maskRuntimeSettingsForEditor(o *RuntimeSettingsOverride) *RuntimeSettingsOverride {
	return maskRuntimeSettingsShowingReferences(o, true)
}

func maskRuntimeSettingsShowingReferences(o *RuntimeSettingsOverride, showReferences bool) *RuntimeSettingsOverride {
	if o == nil {
		return nil
	}
	clone := cloneRuntimeSettings(o)
	for key, value := range clone.Env {
		if showReferences && runtimeOverrideExactSecretPattern.MatchString(strings.TrimSpace(value)) && !isReservedRuntimeOverrideSecretReference(value) {
			continue
		}
		if value != "" && (sensitiveEnvKey(key) || sensitiveEnvValue(value)) {
			clone.Env[key] = scrub.Redacted
		}
	}
	clone.BaseURL = maskURLSecrets(clone.BaseURL)
	maskHTTPSecrets(clone.HTTP)
	maskBrowserSecrets(clone.BrowserConfig)
	maskJavaVuserRuntimeSecrets(clone.JavaVuser)
	return clone
}

func maskJavaVuserRuntimeSecrets(java *RuntimeJavaVuserOverride) {
	if java == nil {
		return
	}
	for index, parameter := range java.AdditionalVMParameters {
		if javarunner.ContainsSensitiveVMParameterValue(parameter) {
			java.AdditionalVMParameters[index] = scrub.Redacted
		}
	}
}

// maskHTTPSecrets redacts the secret-bearing fields and header values of an HTTP
// override section in place on a cloned section.
func maskHTTPSecrets(http *dsl.HTTPConfig) {
	if http == nil {
		return
	}
	authActive := http.Auth.Type != "" || http.Auth.Password != "" || http.Auth.Token != ""
	if http.Auth.Username != "" &&
		(authActive ||
			isReservedRuntimeOverrideSecretReference(http.Auth.Username) ||
			sensitiveEnvValue(http.Auth.Username)) {
		http.Auth.Username = scrub.Redacted
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
	for name, value := range http.DefaultHeaders {
		if value != "" && runtimeHTTPHeaderSecretBearing(name, value) {
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
		if header.Value != "" && runtimeHTTPHeaderSecretBearing(header.Name, header.Value) {
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

// runtimeHTTPHeaderSecretBearing covers both semantic header names and
// credential-shaped values. The reserved-reference case is the protected form
// of a credential-shaped plaintext value: it must remain masked after the
// plaintext has moved into governed secret storage.
func runtimeHTTPHeaderSecretBearing(name, value string) bool {
	return scrub.Policy{}.SensitiveHeader(name) ||
		sensitiveEnvValue(value) ||
		isReservedRuntimeOverrideSecretReference(value)
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
	if browser.HTTPCredentials.Username != "" {
		browser.HTTPCredentials.Username = scrub.Redacted
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
		if hasSchemeLessURLUserinfo(trimmed) {
			return scrub.Redacted
		}
		return raw
	}
	if parsed.User == nil {
		if hasSchemeLessURLUserinfo(trimmed) {
			return scrub.Redacted
		}
		return raw
	}
	parsed.User = url.User(scrub.Redacted)
	return parsed.String()
}

// url.Parse treats a scheme-less value as a path, so recognize only the
// credential shape user:password@host. A bare '@' is common in email addresses
// and URL paths and is not evidence that the whole value is secret.
func hasSchemeLessURLUserinfo(raw string) bool {
	at := strings.LastIndex(raw, "@")
	if at <= 0 || at == len(raw)-1 {
		return false
	}
	colon := strings.Index(raw[:at], ":")
	if colon <= 0 || colon >= at-1 {
		return false
	}
	// Opaque URI schemes can legitimately contain an email address after their
	// colon. They are not scheme-less authorities and must remain visible.
	switch strings.ToLower(raw[:colon]) {
	case "mailto", "tel", "urn", "data":
		return false
	}
	// In an absolute URL, the colon belongs to the scheme rather than a
	// username/password pair (for example /users/@me after https://host).
	if strings.HasPrefix(raw[colon+1:], "//") {
		return false
	}
	// Userinfo belongs to an authority, so it cannot contain path, query, or
	// fragment delimiters and it must be followed by a real host. Parsing with
	// a synthetic authority prefix also validates bracketed IPv6 and ports.
	if strings.ContainsAny(raw[:at], "/?# \t\r\n") {
		return false
	}
	parsed, err := url.Parse("//" + raw)
	return err == nil && parsed.User != nil && parsed.User.Username() != "" && parsed.Hostname() != ""
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
// per-group runtime Env secrets redacted, so stored secrets never leak. It is
// the form library export and content hashes use; responses use
// maskStoredScenarioForEditor. The original scenario (and its overrides) are
// left untouched.
func maskStoredScenario(scenario *StoredScenario) *StoredScenario {
	return maskStoredScenarioWith(scenario, maskRuntimeSettings)
}

// maskStoredScenarioForEditor is maskStoredScenario for a scenario response,
// which shows project-secret references (maskRuntimeSettingsForEditor).
func maskStoredScenarioForEditor(scenario *StoredScenario) *StoredScenario {
	return maskStoredScenarioWith(scenario, maskRuntimeSettingsForEditor)
}

func maskStoredScenarioWith(scenario *StoredScenario, mask func(*RuntimeSettingsOverride) *RuntimeSettingsOverride) *StoredScenario {
	if scenario == nil {
		return nil
	}
	masked := *scenario
	masked.RuntimeSettings = mask(scenario.RuntimeSettings)
	if len(scenario.Scripts) > 0 {
		refs := cloneScenarioScriptRefs(scenario.Scripts)
		for i := range refs {
			refs[i].RuntimeSettings = mask(refs[i].RuntimeSettings)
		}
		masked.Scripts = refs
	}
	return &masked
}

// maskStoredScenarios masks each scenario in a list response.
func maskStoredScenarios(scenarios []*StoredScenario) []*StoredScenario {
	if scenarios == nil {
		return nil
	}
	masked := make([]*StoredScenario, len(scenarios))
	for i, scenario := range scenarios {
		masked[i] = maskStoredScenarioForEditor(scenario)
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
	provenance.LiveTables = cloneLiveTableArchiveSummaries(provenance.LiveTables)
	provenance.ProjectLiveTables = append([]RunArchiveProjectLiveTableAttachment(nil), provenance.ProjectLiveTables...)
	provenance.TelemetryPolicy = cloneTelemetryBudgetPolicy(provenance.TelemetryPolicy)
	provenance.PolicyPins = cloneRunPolicyPins(provenance.PolicyPins)
	provenance.TelemetryAcceptancePolicy = cloneTelemetryAcceptancePolicySnapshot(provenance.TelemetryAcceptancePolicy)
	provenance.ExecutionSettings.Thresholds = cloneThresholdSettings(provenance.ExecutionSettings.Thresholds)
	provenance.TrafficProfile = cloneTrafficProfile(provenance.TrafficProfile)
	provenance.RuntimeSettings = maskRuntimeSettings(provenance.RuntimeSettings)
	provenance.ELKCapture = cloneELKCaptureConfig(provenance.ELKCapture)
	provenance.DynatraceCapture = cloneDynatraceCaptureConfig(provenance.DynatraceCapture)
	provenance.Groups = maskTestGroupsRuntimeSettings(provenance.Groups)
	return provenance
}

func maskRunArchiveManifest(manifest *RunArchiveManifest) *RunArchiveManifest {
	if manifest == nil {
		return nil
	}
	// Telemetry text is scrubbed with the run's own redaction rules, read
	// before the provenance holding them is masked (TEL-B).
	masked := *scrubTelemetryText(manifest, newTelemetryTextScrubber(manifest))
	masked.Provenance = maskRunArchiveProvenance(masked.Provenance)
	if masked.InitialProvenance != nil {
		initial := maskRunArchiveProvenance(*masked.InitialProvenance)
		masked.InitialProvenance = &initial
	}
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
	if result.JavaVuser != nil {
		var priorJava *RuntimeJavaVuserOverride
		if prior != nil {
			priorJava = prior.JavaVuser
		}
		reconcileJavaVuserRuntimeSecrets(result.JavaVuser, priorJava)
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

// reconcileJavaVuserRuntimeSecrets restores a masked JVM argument only at the
// same list position. Positional matching is intentionally strict: restoring
// by a neighboring value after a reorder could copy a credential into a
// different system property. Cardinality changes are ambiguous even when the
// remaining sentinel occupies a formerly sensitive position (for example,
// deleting one of two adjacent masked arguments), so every sentinel is left
// unresolved in that case. The Java argument validator then rejects the write.
func reconcileJavaVuserRuntimeSecrets(incoming, prior *RuntimeJavaVuserOverride) {
	if incoming == nil || prior == nil ||
		len(incoming.AdditionalVMParameters) != len(prior.AdditionalVMParameters) {
		return
	}
	for index, parameter := range incoming.AdditionalVMParameters {
		if parameter != scrub.Redacted {
			continue
		}
		priorParameter := prior.AdditionalVMParameters[index]
		if !javarunner.ContainsSensitiveVMParameterValue(priorParameter) {
			continue
		}
		incoming.AdditionalVMParameters[index] = priorParameter
	}
}

// reconcileHTTPSecrets restores HTTP secret fields the client echoed back as the
// redaction sentinel from the prior stored section; "" clears, any other value persists.
func reconcileHTTPSecrets(incoming, prior *dsl.HTTPConfig) {
	if incoming == nil {
		return
	}
	priorAuthUsername, priorAuthPassword, priorAuthToken, priorProxyUsername, priorProxyPassword := "", "", "", "", ""
	if prior != nil {
		priorAuthUsername = prior.Auth.Username
		priorAuthPassword = prior.Auth.Password
		priorAuthToken = prior.Auth.Token
		priorProxyUsername = prior.Proxy.Username
		priorProxyPassword = prior.Proxy.Password
	}
	if incoming.Auth.Username == scrub.Redacted {
		incoming.Auth.Username = priorAuthUsername
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
	for name, value := range incoming.DefaultHeaders {
		priorValue, hasPrior := caseInsensitiveMapValue(priorDefaultHeaders(prior), name)
		if strings.EqualFold(strings.TrimSpace(name), "Cookie") && value != scrub.Redacted && value != "" {
			if reconciled, keep := reconcileMaskedCookieHeader(value, []string{priorValue}); keep {
				incoming.DefaultHeaders[name] = reconciled
			} else {
				delete(incoming.DefaultHeaders, name)
			}
			continue
		}
		switch value {
		case scrub.Redacted:
			// A sentinel is a preservation request, never a literal header
			// value. Restore only a unique secret-bearing value at the same
			// case-insensitive header path; otherwise discard it fail-closed.
			if hasPrior && runtimeHTTPHeaderSecretBearing(name, priorValue) {
				incoming.DefaultHeaders[name] = priorValue
			} else {
				delete(incoming.DefaultHeaders, name)
			}
		case "":
			if runtimeHTTPHeaderSecretBearing(name, value) ||
				(hasPrior && runtimeHTTPHeaderSecretBearing(name, priorValue)) {
				delete(incoming.DefaultHeaders, name)
			}
		default:
			// Plaintext credential-shaped values remain in the candidate long
			// enough for protectRuntimeOverrideSecrets to project them. The
			// command center then rejects their execution-only overlay because
			// it cannot durably provision new secret material.
		}
	}
	if len(incoming.DefaultHeaders) == 0 {
		incoming.DefaultHeaders = nil
	}
	priorOccurrences := orderedHeaderOccurrenceCounts(prior)
	incomingOccurrences := orderedHeaderOccurrenceCounts(incoming)
	priorCookieValues := priorOrderedCookieHeaderValues(prior)
	occurrences := make(map[string]int)
	filtered := incoming.DefaultHeaderList[:0]
	for _, header := range incoming.DefaultHeaderList {
		canonical := strings.ToLower(strings.TrimSpace(header.Name))
		ordinal := occurrences[canonical]
		occurrences[canonical]++
		priorValue, hasPrior := priorDefaultHeaderListValue(prior, header.Name, ordinal)
		priorSecretBearing := hasPrior && runtimeHTTPHeaderSecretBearing(header.Name, priorValue)
		if header.Value == scrub.Redacted {
			// Header names plus ordinals are only stable while the number of
			// same-name rows is unchanged. This prevents a remaining sentinel
			// from adopting a deleted sibling's credential.
			countStable := priorOccurrences[canonical] == 1 ||
				priorOccurrences[canonical] == incomingOccurrences[canonical]
			if priorSecretBearing && countStable {
				header.Value = priorValue
				filtered = append(filtered, header)
			}
			continue
		}
		secretBearing := runtimeHTTPHeaderSecretBearing(header.Name, header.Value)
		if !secretBearing {
			if header.Value == "" && priorSecretBearing {
				continue
			}
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
	for _, header := range prior.DefaultHeaderList {
		counts[strings.ToLower(strings.TrimSpace(header.Name))]++
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
	var matched string
	found := false
	for candidate, value := range values {
		if strings.EqualFold(candidate, key) {
			if found {
				return "", false
			}
			matched = value
			found = true
		}
	}
	return matched, found
}

func reconcileBrowserSecrets(incoming, prior *dsl.BrowserConfig) {
	if incoming == nil {
		return
	}
	if incoming.HTTPCredentials.Username == scrub.Redacted {
		incoming.HTTPCredentials.Username = ""
		if prior != nil {
			incoming.HTTPCredentials.Username = prior.HTTPCredentials.Username
		}
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
