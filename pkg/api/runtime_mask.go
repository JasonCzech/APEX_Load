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

	"github.com/jasonczech/apexload/pkg/scrub"
)

// sensitiveEnvMarkers are case-insensitive substrings that mark a runtime Env
// variable name as secret-bearing.
var sensitiveEnvMarkers = []string{
	"password", "passwd", "secret", "token",
	"apikey", "api_key", "credential", "private_key", "client_secret",
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
	return clone
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
		refs := make([]ScenarioScriptRef, len(scenario.Scripts))
		copy(refs, scenario.Scripts)
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
	if len(result.Env) == 0 {
		return result
	}
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
	return result
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
