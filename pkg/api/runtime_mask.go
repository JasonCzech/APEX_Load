package api

// Masked-secret protocol for runtime overrides.
//
// The only secret-bearing field in the current RuntimeSettingsOverride contract
// is Env (template values injected via {{env.KEY}}). This file keeps stored
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
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// sensitiveEnvMarkers are case-insensitive substrings that mark a runtime Env
// variable name as secret-bearing.
var sensitiveEnvMarkers = []string{
	"password", "passwd", "secret", "token",
	"apikey", "api_key", "credential", "private_key", "client_secret",
}

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
	lower := strings.ToLower(name)
	for _, marker := range sensitiveEnvMarkers {
		if strings.Contains(lower, marker) {
			return true
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
		if value != "" && sensitiveEnvKey(key) {
			clone.Env[key] = scrub.Redacted
		}
	}
	maskHTTPSecrets(clone.HTTP)
	return clone
}

// maskHTTPSecrets redacts the secret-bearing fields of an HTTP override section
// (auth password/token, proxy username/password) in place on a cloned section.
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
		masked.Sparkline = buildManagedTestSparkline(test.History, test.LiveMetrics, defaultManagedTestSparklinePoints)
	} else {
		masked.Sparkline = nil
	}
	masked.RuntimeSettings = maskRuntimeSettings(test.RuntimeSettings)
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
