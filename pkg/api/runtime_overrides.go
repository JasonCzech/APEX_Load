package api

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

var runtimeOverrideEnvPattern = regexp.MustCompile(`\{\{\s*env\.([^{}\s]+)\s*\}\}`)

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
		merged.Iterations = &iterations
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
	if group.LogLevel != "" {
		merged.LogLevel = group.LogLevel
	}
	if group.AutoTransactions != "" {
		merged.AutoTransactions = group.AutoTransactions
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
