package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/scrub"
)

var runtimeOverrideEnvPattern = regexp.MustCompile(`\{\{\s*env\.([^{}\s]+)\s*\}\}`)
var runtimeOverrideExactSecretPattern = regexp.MustCompile(`^\{\{\s*secret\.(` + engine.LogicalSecretNameInputPattern + `)\s*\}\}$`)
var runtimeOverrideAnySecretPattern = regexp.MustCompile(`\{\{\s*secret\.`)

const runtimeOverrideSecretPrefix = "APEX_RUNTIME_OVERRIDE_"

// protectRuntimeOverrideSecrets returns an execution-only override projection:
// secret-bearing values become deterministic {{secret.*}} references while
// their plaintext material is retained only in the returned in-memory map.
// The logical names depend on group/field identity, never on credential bytes,
// so script hashes and caches cannot fingerprint values.
func protectRuntimeOverrideSecrets(scope string, groupIndex int, script *dsl.Script, overrides *RuntimeSettingsOverride, rejectExistingRefs bool) (*RuntimeSettingsOverride, map[string]string, error) {
	protected, values, _, err := protectRuntimeOverrideSecretsWithOptions(scope, groupIndex, script, overrides, rejectExistingRefs, false)
	return protected, values, err
}

// protectLegacyRuntimeOverrideSecrets has one deliberately narrower exception
// than the request-time validator: an already-persisted [REDACTED] sentinel is
// converted to a deterministic reference with no value. That makes the legacy
// row non-executable until an operator configures the secret, without treating
// the sentinel itself as credential material.
func protectLegacyRuntimeOverrideSecrets(scope string, groupIndex int, script *dsl.Script, overrides *RuntimeSettingsOverride) (*RuntimeSettingsOverride, map[string]string, bool, error) {
	return protectRuntimeOverrideSecretsWithOptions(scope, groupIndex, script, overrides, false, true)
}

func protectRuntimeOverrideSecretsWithOptions(scope string, groupIndex int, script *dsl.Script, overrides *RuntimeSettingsOverride, rejectExistingRefs, deferRedacted bool) (*RuntimeSettingsOverride, map[string]string, bool, error) {
	protected := cloneRuntimeSettings(overrides)
	if protected == nil {
		return nil, nil, false, nil
	}
	values := make(map[string]string)
	changed := false
	existing := make(map[string]struct{})
	if rejectExistingRefs && script != nil {
		names, err := engine.ReferencedSecretNames(script)
		if err != nil {
			return nil, nil, false, fmt.Errorf("inspect existing secret references")
		}
		for _, name := range names {
			existing[name] = struct{}{}
		}
	}
	protect := func(path, value string, sensitive bool) (string, error) {
		if value == "" || !sensitive {
			return value, nil
		}
		if runtimeOverrideExactSecretPattern.MatchString(strings.TrimSpace(value)) {
			return value, nil
		}
		if runtimeOverrideAnySecretPattern.MatchString(value) {
			return "", fmt.Errorf("runtime override %s mixes literal material with a secret reference; use one exact {{secret.NAME}} reference", path)
		}
		if strings.EqualFold(strings.TrimSpace(value), scrub.Redacted) {
			if !deferRedacted {
				return "", fmt.Errorf("runtime override %s contains a redaction sentinel; enter or clear the credential", path)
			}
			name := runtimeOverrideSecretName(scope, groupIndex, path)
			if _, collision := existing[name]; collision {
				return "", fmt.Errorf("runtime override %s conflicts with reserved secret reference %s", path, name)
			}
			changed = true
			return "{{secret." + name + "}}", nil
		}
		name := runtimeOverrideSecretName(scope, groupIndex, path)
		if _, collision := existing[name]; collision {
			return "", fmt.Errorf("runtime override %s conflicts with reserved secret reference %s", path, name)
		}
		values[name] = value
		changed = true
		return "{{secret." + name + "}}", nil
	}
	var err error
	for key, value := range protected.Env {
		protected.Env[key], err = protect("env."+key, value, sensitiveEnvKey(key) || sensitiveEnvValue(value))
		if err != nil {
			return nil, nil, false, err
		}
	}
	protected.BaseURL, err = protect("base_url", protected.BaseURL, maskURLSecrets(protected.BaseURL) != protected.BaseURL)
	if err != nil {
		return nil, nil, false, err
	}
	if protected.HTTP != nil {
		http := protected.HTTP
		authActive := http.Auth.Type != "" || http.Auth.Password != "" || http.Auth.Token != ""
		http.Auth.Username, err = protect("http.auth.username", http.Auth.Username, authActive)
		if err != nil {
			return nil, nil, false, err
		}
		http.Auth.Password, err = protect("http.auth.password", http.Auth.Password, true)
		if err != nil {
			return nil, nil, false, err
		}
		http.Auth.Token, err = protect("http.auth.token", http.Auth.Token, true)
		if err != nil {
			return nil, nil, false, err
		}
		http.Proxy.URL, err = protect("http.proxy.url", http.Proxy.URL, maskURLSecrets(http.Proxy.URL) != http.Proxy.URL)
		if err != nil {
			return nil, nil, false, err
		}
		http.Proxy.Username, err = protect("http.proxy.username", http.Proxy.Username, true)
		if err != nil {
			return nil, nil, false, err
		}
		http.Proxy.Password, err = protect("http.proxy.password", http.Proxy.Password, true)
		if err != nil {
			return nil, nil, false, err
		}
		policy := scrub.Policy{}
		for name, value := range http.DefaultHeaders {
			http.DefaultHeaders[name], err = protect("http.default_headers."+name, value, policy.SensitiveHeader(name))
			if err != nil {
				return nil, nil, false, err
			}
		}
	}
	if protected.BrowserConfig != nil {
		browser := protected.BrowserConfig
		credentialsActive := browser.HTTPCredentials.Username != "" || browser.HTTPCredentials.Password != ""
		browser.HTTPCredentials.Username, err = protect("browser.http_credentials.username", browser.HTTPCredentials.Username, credentialsActive)
		if err != nil {
			return nil, nil, false, err
		}
		browser.HTTPCredentials.Password, err = protect("browser.http_credentials.password", browser.HTTPCredentials.Password, true)
		if err != nil {
			return nil, nil, false, err
		}
		browser.Proxy.Server, err = protect("browser.proxy.server", browser.Proxy.Server, maskURLSecrets(browser.Proxy.Server) != browser.Proxy.Server)
		if err != nil {
			return nil, nil, false, err
		}
		browser.Proxy.Username, err = protect("browser.proxy.username", browser.Proxy.Username, true)
		if err != nil {
			return nil, nil, false, err
		}
		browser.Proxy.Password, err = protect("browser.proxy.password", browser.Proxy.Password, true)
		if err != nil {
			return nil, nil, false, err
		}
		for index := range browser.ClientCertificates {
			path := fmt.Sprintf("browser.client_certificates.%d.passphrase", index)
			browser.ClientCertificates[index].Passphrase, err = protect(path, browser.ClientCertificates[index].Passphrase, true)
			if err != nil {
				return nil, nil, false, err
			}
		}
	}
	return protected, values, changed, nil
}

func runtimeOverrideSecretName(scope string, groupIndex int, path string) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("scope:%s:group:%d:%s", strings.TrimSpace(scope), groupIndex, path)))
	return fmt.Sprintf("%sG%d_%X", runtimeOverrideSecretPrefix, groupIndex+1, digest[:12])
}

func filterReferencedRuntimeSecretValues(script *dsl.Script, values map[string]string) (map[string]string, error) {
	if len(values) == 0 || script == nil {
		return nil, nil
	}
	names, err := engine.ReferencedSecretNames(script)
	if err != nil {
		return nil, err
	}
	referenced := make(map[string]string)
	for _, name := range names {
		if value, ok := values[name]; ok {
			referenced[name] = value
		}
	}
	if len(referenced) == 0 {
		return nil, nil
	}
	return referenced, nil
}

func runtimeSecretValueList(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, values[name])
	}
	return out
}

func cloneRuntimeSecretValues(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for name, value := range values {
		cloned[name] = value
	}
	return cloned
}

func overlayRuntimeSecretResolver(values map[string]string, fallback engine.SecretResolver) engine.SecretResolver {
	if len(values) == 0 {
		return fallback
	}
	snapshot := cloneRuntimeSecretValues(values)
	return engine.ContextSecretResolverFunc(func(ctx context.Context, name string) (string, bool) {
		if value, ok := snapshot[strings.TrimSpace(name)]; ok {
			return value, true
		}
		if fallback == nil {
			return "", false
		}
		return engine.ResolveSecret(ctx, fallback, name)
	})
}

func runtimeSecretValueAppearsInScript(script *dsl.Script, values map[string]string) bool {
	if script == nil || len(values) == 0 {
		return false
	}
	payload, err := json.Marshal(script)
	if err != nil {
		return true
	}
	var document any
	if err := json.Unmarshal(payload, &document); err != nil {
		return true
	}
	var inspect func(any) bool
	inspect = func(value any) bool {
		switch typed := value.(type) {
		case string:
			for _, secret := range values {
				if secret != "" && strings.Contains(typed, secret) {
					return true
				}
			}
		case []any:
			for _, item := range typed {
				if inspect(item) {
					return true
				}
			}
		case map[string]any:
			for _, item := range typed {
				if inspect(item) {
					return true
				}
			}
		}
		return false
	}
	return inspect(document)
}

// applyRuntimeOverrides merges test-level runtime settings into a script config,
// mirroring LoadRunner Controller's override behavior (test-level wins where specified).
func applyRuntimeOverrides(script *dsl.Script, overrides *RuntimeSettingsOverride) error {
	if script == nil || overrides == nil {
		return nil
	}
	if len(overrides.Env) > 0 {
		overridden, err := applyRuntimeEnvOverrides(script, overrides.Env)
		if err != nil {
			return err
		}
		*script = *overridden
	}
	if overrides.BaseURL != "" {
		script.Config.BaseURL = overrides.BaseURL
	}
	if overrides.RunLogic != nil {
		script.Config.RunLogic = *cloneRunLogicConfig(overrides.RunLogic)
	} else if overrides.Iterations != nil {
		script.Config.RunLogic.Iterations = *overrides.Iterations
	}
	if overrides.Pacing != nil {
		script.Config.Pacing = *overrides.Pacing
	}
	if overrides.ThinkTime != nil {
		script.Config.ThinkTime = *overrides.ThinkTime
	}
	if overrides.ErrorHandling != nil {
		script.Config.ErrorHandling = *overrides.ErrorHandling
	}
	if overrides.HTTP != nil {
		script.Config.HTTP = *cloneViaJSON(overrides.HTTP)
	}
	if overrides.WebRuntime != nil {
		script.Config.WebRuntime = mergeWebRuntime(script.Config.WebRuntime, *overrides.WebRuntime)
	}
	if overrides.BrowserConfig != nil {
		script.Config.Browser = *cloneViaJSON(overrides.BrowserConfig)
	} else if overrides.Browser != nil {
		if overrides.Browser.Headless != nil {
			headless := *overrides.Browser.Headless
			script.Config.Browser.Headless = &headless
		}
		if overrides.Browser.DeviceProfile != "" {
			script.Config.Browser.DeviceProfile = overrides.Browser.DeviceProfile
		}
		if overrides.Browser.TouchMode != nil {
			touchMode := *overrides.Browser.TouchMode
			script.Config.Browser.TouchMode = &touchMode
		}
		if overrides.Browser.Orientation != "" {
			script.Config.Browser.Orientation = overrides.Browser.Orientation
		}
	}
	if overrides.Log != nil {
		script.Config.Log = *cloneLogConfig(overrides.Log)
	} else if overrides.LogLevel != "" {
		script.Config.Log.Level = overrides.LogLevel
	}
	if overrides.RunLogic == nil && overrides.AutoTransactions != "" {
		script.Config.RunLogic.AutoTransactions = overrides.AutoTransactions
	}
	return nil
}

func applyRuntimeEnvOverrides(script *dsl.Script, overrides map[string]string) (*dsl.Script, error) {
	payload, err := json.Marshal(script)
	if err != nil {
		return nil, fmt.Errorf("marshal runtime env overrides: %w", err)
	}
	var generic any
	if err := json.Unmarshal(payload, &generic); err != nil {
		return nil, fmt.Errorf("decode runtime env overrides: %w", err)
	}
	generic = replaceRuntimeEnvTemplates(generic, overrides)
	payload, err = json.Marshal(generic)
	if err != nil {
		return nil, fmt.Errorf("marshal overridden runtime script: %w", err)
	}
	var overridden dsl.Script
	if err := json.Unmarshal(payload, &overridden); err != nil {
		return nil, fmt.Errorf("decode overridden runtime script: %w", err)
	}
	return &overridden, nil
}

func replaceRuntimeEnvTemplates(value any, overrides map[string]string) any {
	switch typed := value.(type) {
	case string:
		return runtimeOverrideEnvPattern.ReplaceAllStringFunc(typed, func(token string) string {
			match := runtimeOverrideEnvPattern.FindStringSubmatch(token)
			if len(match) != 2 {
				return token
			}
			key := strings.TrimSpace(match[1])
			if replacement, ok := overrides[key]; ok {
				return replacement
			}
			return token
		})
	case []any:
		resolved := make([]any, len(typed))
		for i := range typed {
			resolved[i] = replaceRuntimeEnvTemplates(typed[i], overrides)
		}
		return resolved
	case map[string]any:
		resolved := make(map[string]any, len(typed))
		for key, item := range typed {
			resolved[key] = replaceRuntimeEnvTemplates(item, overrides)
		}
		return resolved
	default:
		return value
	}
}

func cloneRuntimeSettings(overrides *RuntimeSettingsOverride) *RuntimeSettingsOverride {
	if overrides == nil {
		return nil
	}
	clone := *overrides
	if overrides.Iterations != nil {
		iterations := *overrides.Iterations
		clone.Iterations = &iterations
	}
	if overrides.Pacing != nil {
		pacing := *overrides.Pacing
		clone.Pacing = &pacing
	}
	if overrides.ThinkTime != nil {
		thinkTime := *overrides.ThinkTime
		clone.ThinkTime = &thinkTime
	}
	if overrides.ErrorHandling != nil {
		errorHandling := *overrides.ErrorHandling
		clone.ErrorHandling = &errorHandling
	}
	clone.Log = cloneLogConfig(overrides.Log)
	clone.RunLogic = cloneRunLogicConfig(overrides.RunLogic)
	clone.HTTP = cloneViaJSON(overrides.HTTP)
	clone.WebRuntime = cloneViaJSON(overrides.WebRuntime)
	clone.BrowserConfig = cloneViaJSON(overrides.BrowserConfig)
	if overrides.Browser != nil {
		browser := *overrides.Browser
		if overrides.Browser.Headless != nil {
			headless := *overrides.Browser.Headless
			browser.Headless = &headless
		}
		if overrides.Browser.TouchMode != nil {
			touchMode := *overrides.Browser.TouchMode
			browser.TouchMode = &touchMode
		}
		clone.Browser = &browser
	}
	if len(overrides.Env) > 0 {
		clone.Env = make(map[string]string, len(overrides.Env))
		for key, value := range overrides.Env {
			clone.Env[key] = value
		}
	}
	if len(overrides.RedactionRules) > 0 {
		clone.RedactionRules = make([]engine.RedactionRule, len(overrides.RedactionRules))
		copy(clone.RedactionRules, overrides.RedactionRules)
	}
	return &clone
}

// mergeWebRuntime overlays the non-zero fields of override onto a deep copy of
// base, returning the combined web-runtime config. Unlike a whole-section replace,
// this lets a runtime override that only sets e.g. source_ips (IP spoofing) reach
// the engine without wiping unrelated script settings such as network_simulation or
// content_checks. Each override section wins as a unit when it carries any value.
func mergeWebRuntime(base dsl.WebRuntimeConfig, override dsl.WebRuntimeConfig) dsl.WebRuntimeConfig {
	merged := base
	if clone := cloneViaJSON(&base); clone != nil {
		merged = *clone
	}
	if len(override.AdditionalAttributes) > 0 {
		merged.AdditionalAttributes = override.AdditionalAttributes
	}
	if override.DownloadFilters.Mode != "" || len(override.DownloadFilters.Hosts) > 0 {
		merged.DownloadFilters = override.DownloadFilters
	}
	if len(override.ContentChecks) > 0 {
		merged.ContentChecks = override.ContentChecks
	}
	if override.NetworkSimulation != (dsl.NetworkSimulationConfig{}) {
		merged.NetworkSimulation = override.NetworkSimulation
	}
	if override.Streaming != (dsl.StreamingConfig{}) {
		merged.Streaming = override.Streaming
	}
	if override.DataFormatting.AutoDetect || len(override.DataFormatting.Chain) > 0 {
		merged.DataFormatting = override.DataFormatting
	}
	if len(override.SourceIPs) > 0 {
		merged.SourceIPs = append([]string(nil), override.SourceIPs...)
	}
	if len(override.NetworkProfiles) > 0 {
		merged.NetworkProfiles = append([]dsl.NetworkProfileWeight(nil), override.NetworkProfiles...)
	}
	return merged
}

// cloneViaJSON deep-copies a config section through JSON — the simplest safe deep
// clone for the nested maps/slices in the larger override sections (HTTP default
// headers, web-runtime profile mix, browser viewport, etc.). Returns nil for nil.
func cloneViaJSON[T any](v *T) *T {
	if v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return &out
}

// cloneLogConfig deep-copies a log override section (including ExtendedOptions).
func cloneLogConfig(c *dsl.LogConfig) *dsl.LogConfig {
	if c == nil {
		return nil
	}
	clone := *c
	if len(c.ExtendedOptions) > 0 {
		clone.ExtendedOptions = append([]string(nil), c.ExtendedOptions...)
	}
	return &clone
}

// cloneRunLogicConfig deep-copies a run-logic override section (including ActionSequence).
func cloneRunLogicConfig(c *dsl.RunLogicConfig) *dsl.RunLogicConfig {
	if c == nil {
		return nil
	}
	clone := *c
	if len(c.ActionSequence) > 0 {
		clone.ActionSequence = append([]string(nil), c.ActionSequence...)
	}
	return &clone
}

func mergeRuntimeSettings(base *RuntimeSettingsOverride, group *RuntimeSettingsOverride) *RuntimeSettingsOverride {
	if base == nil {
		return cloneRuntimeSettings(group)
	}
	merged := cloneRuntimeSettings(base)
	if group == nil {
		return merged
	}
	if merged == nil {
		merged = &RuntimeSettingsOverride{}
	}
	if group.Iterations != nil {
		iterations := *group.Iterations
		if group.RunLogic == nil && merged.RunLogic != nil {
			merged.RunLogic = cloneRunLogicConfig(merged.RunLogic)
			merged.RunLogic.Iterations = iterations
			merged.Iterations = nil
		} else {
			merged.Iterations = &iterations
		}
	}
	if group.Pacing != nil {
		pacing := *group.Pacing
		merged.Pacing = &pacing
	}
	if group.ThinkTime != nil {
		thinkTime := *group.ThinkTime
		merged.ThinkTime = &thinkTime
	}
	if group.ErrorHandling != nil {
		errorHandling := *group.ErrorHandling
		merged.ErrorHandling = &errorHandling
	}
	if group.Log != nil {
		merged.Log = cloneLogConfig(group.Log)
	}
	if group.RunLogic != nil {
		merged.RunLogic = cloneRunLogicConfig(group.RunLogic)
	}
	if group.HTTP != nil {
		merged.HTTP = cloneViaJSON(group.HTTP)
	}
	if group.WebRuntime != nil {
		if merged.WebRuntime != nil {
			combined := mergeWebRuntime(*merged.WebRuntime, *group.WebRuntime)
			merged.WebRuntime = &combined
		} else {
			merged.WebRuntime = cloneViaJSON(group.WebRuntime)
		}
	}
	if group.BrowserConfig != nil {
		merged.BrowserConfig = cloneViaJSON(group.BrowserConfig)
	}
	if group.Browser != nil {
		if group.BrowserConfig == nil && merged.BrowserConfig != nil {
			merged.BrowserConfig = cloneViaJSON(merged.BrowserConfig)
			if group.Browser.Headless != nil {
				headless := *group.Browser.Headless
				merged.BrowserConfig.Headless = &headless
			}
			if group.Browser.DeviceProfile != "" {
				merged.BrowserConfig.DeviceProfile = group.Browser.DeviceProfile
			}
			if group.Browser.TouchMode != nil {
				touchMode := *group.Browser.TouchMode
				merged.BrowserConfig.TouchMode = &touchMode
			}
			if group.Browser.Orientation != "" {
				merged.BrowserConfig.Orientation = group.Browser.Orientation
			}
			merged.Browser = nil
		} else {
			browser := RuntimeBrowserOverride{}
			if merged.Browser != nil {
				browser = *merged.Browser
				if merged.Browser.Headless != nil {
					headless := *merged.Browser.Headless
					browser.Headless = &headless
				}
				if merged.Browser.TouchMode != nil {
					touchMode := *merged.Browser.TouchMode
					browser.TouchMode = &touchMode
				}
			}
			if group.Browser.Headless != nil {
				headless := *group.Browser.Headless
				browser.Headless = &headless
			}
			if group.Browser.DeviceProfile != "" {
				browser.DeviceProfile = group.Browser.DeviceProfile
			}
			if group.Browser.TouchMode != nil {
				touchMode := *group.Browser.TouchMode
				browser.TouchMode = &touchMode
			}
			if group.Browser.Orientation != "" {
				browser.Orientation = group.Browser.Orientation
			}
			merged.Browser = &browser
		}
	}
	if group.LogLevel != "" {
		if group.Log == nil && merged.Log != nil {
			merged.Log = cloneLogConfig(merged.Log)
			merged.Log.Level = group.LogLevel
			merged.LogLevel = ""
		} else {
			merged.LogLevel = group.LogLevel
		}
	}
	if group.AutoTransactions != "" {
		if group.RunLogic == nil && merged.RunLogic != nil {
			merged.RunLogic = cloneRunLogicConfig(merged.RunLogic)
			merged.RunLogic.AutoTransactions = group.AutoTransactions
			merged.AutoTransactions = ""
		} else {
			merged.AutoTransactions = group.AutoTransactions
		}
	}
	if group.BaseURL != "" {
		merged.BaseURL = group.BaseURL
	}
	if len(group.Env) > 0 {
		if merged.Env == nil {
			merged.Env = make(map[string]string, len(group.Env))
		}
		for key, value := range group.Env {
			merged.Env[key] = value
		}
	}
	if len(group.RedactionRules) > 0 {
		merged.RedactionRules = make([]engine.RedactionRule, len(group.RedactionRules))
		copy(merged.RedactionRules, group.RedactionRules)
	}
	return merged
}

func buildRuntimeRedactionConfig(runtimeSettings *RuntimeSettingsOverride) (*engine.RedactionConfig, error) {
	if runtimeSettings == nil || len(runtimeSettings.RedactionRules) == 0 {
		return nil, nil
	}
	return engine.NewRedactionConfig(runtimeSettings.RedactionRules)
}

func cloneRedactionRules(rules []engine.RedactionRule) []engine.RedactionRule {
	if len(rules) == 0 {
		return nil
	}
	clone := make([]engine.RedactionRule, len(rules))
	copy(clone, rules)
	return clone
}

func nilIfNoRules(runtimeSettings *RuntimeSettingsOverride) []engine.RedactionRule {
	if runtimeSettings == nil {
		return nil
	}
	return runtimeSettings.RedactionRules
}
