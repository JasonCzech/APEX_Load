package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/scrub"
)

var runtimeOverrideEnvPattern = regexp.MustCompile(`\{\{\s*env\.([^{}\s]+)\s*\}\}`)
var runtimeOverrideExactSecretPattern = regexp.MustCompile(`^\{\{\s*secret\.(` + engine.LogicalSecretNameInputPattern + `)\s*\}\}$`)
var runtimeOverrideSecretReferencePattern = regexp.MustCompile(`\{\{\s*secret\.\s*(` + engine.LogicalSecretNameInputPattern + `)\s*\}\}`)
var runtimeOverrideAnySecretPattern = regexp.MustCompile(`\{\{\s*secret\.`)

const runtimeOverrideSecretPrefix = "APEX_RUNTIME_OVERRIDE_"

// rejectReservedRuntimeOverrideSecretReferences keeps the controller-owned
// runtime-secret namespace out of user-authored settings. Those deterministic
// names may resolve governed credentials created for another protected field,
// so accepting one in an arbitrary executable value would let an editor
// retarget that credential. Command-center masked echoes use scrub.Redacted;
// same-path reconciliation restores the prior internal reference afterward.
func rejectReservedRuntimeOverrideSecretReferences(overrides *RuntimeSettingsOverride) error {
	if overrides == nil {
		return nil
	}
	payload, err := json.Marshal(overrides)
	if err != nil {
		return fmt.Errorf("inspect runtime secret references")
	}
	for _, match := range runtimeOverrideSecretReferencePattern.FindAllSubmatch(payload, -1) {
		if len(match) < 2 {
			continue
		}
		name, normalizeErr := engine.NormalizeLogicalSecretName(string(match[1]))
		if normalizeErr == nil && strings.HasPrefix(name, runtimeOverrideSecretPrefix) {
			return fmt.Errorf("runtime settings cannot author reserved internal secret references; keep masked credentials as %s or use a configured project secret with a non-reserved name", scrub.Redacted)
		}
	}
	return nil
}

func isReservedRuntimeOverrideSecretReference(value string) bool {
	trimmed := strings.TrimSpace(value)
	match := runtimeOverrideSecretReferencePattern.FindStringSubmatch(trimmed)
	if len(match) != 2 || match[0] != trimmed {
		return false
	}
	name, err := engine.NormalizeLogicalSecretName(match[1])
	return err == nil && strings.HasPrefix(name, runtimeOverrideSecretPrefix)
}

// protectRuntimeOverrideSecrets returns an execution-only override projection:
// secret-bearing values become deterministic {{secret.*}} references while
// their plaintext material is retained only in the returned in-memory map.
// The logical names depend on group/field identity, never on credential bytes,
// so script hashes and caches cannot fingerprint values.
func protectRuntimeOverrideSecrets(scope string, groupIndex int, script *dsl.Script, overrides *RuntimeSettingsOverride, rejectExistingRefs bool) (*RuntimeSettingsOverride, map[string]string, error) {
	protected, values, _, err := protectRuntimeOverrideSecretsWithOptions(scope, groupIndex, script, overrides, rejectExistingRefs, false, false)
	return protected, values, err
}

// protectLegacyRuntimeOverrideSecrets has one deliberately narrower exception
// than the request-time validator: an already-persisted [REDACTED] sentinel is
// converted to a deterministic reference with no value. That makes the legacy
// row non-executable until an operator configures the secret, without treating
// the sentinel itself as credential material.
func protectLegacyRuntimeOverrideSecrets(scope string, groupIndex int, script *dsl.Script, overrides *RuntimeSettingsOverride) (*RuntimeSettingsOverride, map[string]string, bool, error) {
	return protectRuntimeOverrideSecretsWithOptions(scope, groupIndex, script, overrides, false, true, false)
}

// protectStoredRuntimeOverrideSecrets repairs a row loaded from storage. It
// classifies env keys by the rule the row was written under
// (legacySensitiveEnvKey), so a literal stored before readable credential
// names existed is kept rather than replaced by an unattributed reference.
func protectStoredRuntimeOverrideSecrets(scope string, groupIndex int, script *dsl.Script, overrides *RuntimeSettingsOverride) (*RuntimeSettingsOverride, map[string]string, bool, error) {
	return protectRuntimeOverrideSecretsWithOptions(scope, groupIndex, script, overrides, false, true, true)
}

func protectRuntimeOverrideSecretsWithOptions(scope string, groupIndex int, script *dsl.Script, overrides *RuntimeSettingsOverride, rejectExistingRefs, deferRedacted, storedKeyRule bool) (*RuntimeSettingsOverride, map[string]string, bool, error) {
	protected := cloneRuntimeSettings(overrides)
	if protected == nil {
		return nil, nil, false, nil
	}
	if len(protected.OverrideFields) > 0 {
		if _, err := normalizeRuntimeOverrideFields(protected.OverrideFields); err != nil {
			return nil, nil, false, err
		}
		protected = mergeRuntimeSettingsPartial(nil, protected)
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
		keySensitive := sensitiveEnvKey(key)
		if storedKeyRule {
			keySensitive = legacySensitiveEnvKey(key)
		}
		protected.Env[key], err = protect("env."+key, value, keySensitive || sensitiveEnvValue(value))
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
			http.DefaultHeaders[name], err = protect(
				"http.default_headers."+name,
				value,
				policy.SensitiveHeader(name) || sensitiveEnvValue(value),
			)
			if err != nil {
				return nil, nil, false, err
			}
		}
		for index := range http.DefaultHeaderList {
			header := &http.DefaultHeaderList[index]
			header.Value, err = protect(
				fmt.Sprintf("http.default_header_list.%d.%s", index, header.Name),
				header.Value,
				policy.SensitiveHeader(header.Name) || sensitiveEnvValue(header.Value),
			)
			if err != nil {
				return nil, nil, false, err
			}
		}
	}
	if protected.SOAP != nil && protected.SOAP.WSSecurity != nil && protected.SOAP.WSSecurity.UsernameToken != nil {
		token := protected.SOAP.WSSecurity.UsernameToken
		authActive := token.PasswordType != "" || token.Password != ""
		token.Username, err = protect("soap.ws_security.username_token.username", token.Username, authActive || sensitiveEnvValue(token.Username))
		if err != nil {
			return nil, nil, false, err
		}
		token.Password, err = protect("soap.ws_security.username_token.password", token.Password, true)
		if err != nil {
			return nil, nil, false, err
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
		// Playwright session state and the session strategy's credentials
		// are catalog Secret leaves: seal them like the passwords above so
		// a run registers them as platform secrets.
		browser.StorageState, err = protect("browser.storage_state", browser.StorageState, true)
		if err != nil {
			return nil, nil, false, err
		}
		if session := browser.Session; session != nil {
			for index := range session.Identities {
				identity := &session.Identities[index]
				identity.Password, err = protect(fmt.Sprintf("browser.session.identities.%d.password", index), identity.Password, true)
				if err != nil {
					return nil, nil, false, err
				}
				identity.StorageState, err = protect(fmt.Sprintf("browser.session.identities.%d.storage_state", index), identity.StorageState, true)
				if err != nil {
					return nil, nil, false, err
				}
			}
			if session.JWT != nil {
				session.JWT.PrivateKey, err = protect("browser.session.jwt.private_key", session.JWT.PrivateKey, true)
				if err != nil {
					return nil, nil, false, err
				}
			}
		}
	}
	if err := protectRuntimeProtocolSecrets(protected, protect); err != nil {
		return nil, nil, false, err
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
	if err := applyRuntimeOverridesLayer(script, overrides); err != nil {
		return err
	}
	referenceRuntimeOverrideSecretNames(&script.Config, overrides)
	return nil
}

func applyRuntimeOverridesLayer(script *dsl.Script, overrides *RuntimeSettingsOverride) error {
	if len(overrides.OverrideFields) > 0 {
		return applyRuntimeOverridesPartial(script, overrides)
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
	if overrides.WebSocket != nil {
		script.Config.WebSocket = *cloneViaJSON(overrides.WebSocket)
	}
	if overrides.HTTP != nil {
		script.Config.HTTP = *cloneViaJSON(overrides.HTTP)
	}
	if overrides.WebRuntime != nil {
		script.Config.WebRuntime = mergeWebRuntime(script.Config.WebRuntime, *overrides.WebRuntime)
	}
	if overrides.SOAP != nil {
		script.Config.SOAP = *cloneViaJSON(overrides.SOAP)
	}
	if overrides.JavaVuser != nil {
		// Scenario overrides are authored at execution time and are not covered
		// by a source-import attestation. Keep strict credential rejection here
		// even when the durable base script legitimately preserves imported
		// credential-bearing JVM arguments.
		overrideProbe := &dsl.Script{}
		overrideProbe.Config.JavaVuser.AdditionalVMParameters =
			append([]string(nil), overrides.JavaVuser.AdditionalVMParameters...)
		if err := validateJavaVMParameterDisclosureSafety(overrideProbe); err != nil {
			return err
		}
		applyJavaVuserRuntimeOverride(&script.Config.JavaVuser, overrides.JavaVuser)
	}
	if overrides.BrowserEmulation != nil {
		script.Config.BrowserEmulation = cloneViaJSON(overrides.BrowserEmulation)
	}
	applyRuntimeProtocolSections(&script.Config, overrides)
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
	if overrides.Results != nil {
		script.Config.Results = *cloneViaJSON(overrides.Results)
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
	clone.Results = cloneViaJSON(overrides.Results)
	clone.Log = cloneLogConfig(overrides.Log)
	clone.RunLogic = cloneRunLogicConfig(overrides.RunLogic)
	clone.WebSocket = cloneViaJSON(overrides.WebSocket)
	clone.HTTP = cloneViaJSON(overrides.HTTP)
	clone.WebRuntime = cloneViaJSON(overrides.WebRuntime)
	clone.SOAP = cloneViaJSON(overrides.SOAP)
	clone.BrowserConfig = cloneViaJSON(overrides.BrowserConfig)
	clone.BrowserEmulation = cloneViaJSON(overrides.BrowserEmulation)
	clone.JavaVuser = cloneViaJSON(overrides.JavaVuser)
	cloneRuntimeProtocolSections(&clone, overrides)
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
	if len(overrides.OverrideFields) > 0 {
		clone.OverrideFields = append([]string(nil), overrides.OverrideFields...)
	}
	return &clone
}

func cloneRuntimeSettingsOverride(overrides *RuntimeSettingsOverride) *RuntimeSettingsOverride {
	return cloneRuntimeSettings(overrides)
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
	// `clone := *c` copies the pointer, not the bool, so every clone would alias
	// one *bool and a later edit at any layer would leak across all of them.
	// Re-point it, the same way the group merge does for Browser.Headless.
	if c.RedactRunEvidence != nil {
		redact := *c.RedactRunEvidence
		clone.RedactRunEvidence = &redact
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

// applyJavaVuserRuntimeOverride field-merges a scenario-level Java Env override
// onto the script's Java Vuser config with non-empty/non-nil-wins semantics
// (mirroring the importer's mergeJavaVuserVMSettings): an override that only
// sets one field must not wipe imported values such as the classpath. Operator
// fields (enablement, runner command, harness, work dir, capacity) have no
// override counterpart and are untouched.
func applyJavaVuserRuntimeOverride(dst *dsl.JavaVuserConfig, src *RuntimeJavaVuserOverride) {
	if dst == nil || src == nil {
		return
	}
	if src.JDKSelection != "" {
		dst.JDKSelection = src.JDKSelection
	}
	if src.JDKHome != "" {
		dst.JDKHome = src.JDKHome
	}
	if len(src.AdditionalVMParameters) > 0 {
		dst.AdditionalVMParameters = append([]string(nil), src.AdditionalVMParameters...)
	}
	if len(src.Classpath) > 0 {
		dst.Classpath = append([]string(nil), src.Classpath...)
	}
	if src.UseXBootClasspath != nil {
		useXBoot := *src.UseXBootClasspath
		dst.UseXBootClasspath = &useXBoot
	}
	if src.DedicatedClassLoader != nil {
		dedicated := *src.DedicatedClassLoader
		dst.DedicatedClassLoader = &dedicated
	}
	if src.SkipParameterEvaluation != nil {
		skip := *src.SkipParameterEvaluation
		dst.SkipParameterEvaluation = &skip
	}
	if src.JVMSharing != "" {
		dst.JVMSharing = src.JVMSharing
		if !dsl.JavaVuserSharesJVMs(src.JVMSharing) {
			dst.VUsersPerJVM = 0
		}
	}
	if src.VUsersPerJVM != 0 {
		dst.VUsersPerJVM = src.VUsersPerJVM
	}
	if src.StartupTimeoutMs != 0 {
		dst.StartupTimeoutMs = src.StartupTimeoutMs
	}
	if src.ActionTimeoutMs != 0 {
		dst.ActionTimeoutMs = src.ActionTimeoutMs
	}
	if src.ParamLeftBrace != "" {
		dst.ParamLeftBrace = src.ParamLeftBrace
	}
	if src.ParamRightBrace != "" {
		dst.ParamRightBrace = src.ParamRightBrace
	}
}

// mergeJavaVuserRuntimeOverride combines root- and group-level Java Env
// overrides at field granularity (group wins where specified), mirroring the
// Browser partial-merge precedent above.
func mergeJavaVuserRuntimeOverride(base *RuntimeJavaVuserOverride, group *RuntimeJavaVuserOverride) *RuntimeJavaVuserOverride {
	if base == nil {
		return cloneViaJSON(group)
	}
	merged := cloneViaJSON(base)
	if group == nil || merged == nil {
		return merged
	}
	if group.JDKSelection != "" {
		merged.JDKSelection = group.JDKSelection
	}
	if group.JDKHome != "" {
		merged.JDKHome = group.JDKHome
	}
	if len(group.AdditionalVMParameters) > 0 {
		merged.AdditionalVMParameters = append([]string(nil), group.AdditionalVMParameters...)
	}
	if len(group.Classpath) > 0 {
		merged.Classpath = append([]string(nil), group.Classpath...)
	}
	if group.UseXBootClasspath != nil {
		useXBoot := *group.UseXBootClasspath
		merged.UseXBootClasspath = &useXBoot
	}
	if group.DedicatedClassLoader != nil {
		dedicated := *group.DedicatedClassLoader
		merged.DedicatedClassLoader = &dedicated
	}
	if group.SkipParameterEvaluation != nil {
		skip := *group.SkipParameterEvaluation
		merged.SkipParameterEvaluation = &skip
	}
	if group.JVMSharing != "" {
		merged.JVMSharing = group.JVMSharing
		if !dsl.JavaVuserSharesJVMs(group.JVMSharing) {
			merged.VUsersPerJVM = 0
		}
	}
	if group.VUsersPerJVM != 0 {
		merged.VUsersPerJVM = group.VUsersPerJVM
	}
	if group.StartupTimeoutMs != 0 {
		merged.StartupTimeoutMs = group.StartupTimeoutMs
	}
	if group.ActionTimeoutMs != 0 {
		merged.ActionTimeoutMs = group.ActionTimeoutMs
	}
	if group.ParamLeftBrace != "" {
		merged.ParamLeftBrace = group.ParamLeftBrace
	}
	if group.ParamRightBrace != "" {
		merged.ParamRightBrace = group.ParamRightBrace
	}
	return merged
}

func mergeRuntimeSettings(base *RuntimeSettingsOverride, group *RuntimeSettingsOverride) *RuntimeSettingsOverride {
	if base == nil && group == nil {
		return nil
	}
	if (base != nil && len(base.OverrideFields) > 0) || (group != nil && len(group.OverrideFields) > 0) {
		return mergeRuntimeSettingsPartial(base, group)
	}
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
	if group.SOAP != nil {
		merged.SOAP = cloneViaJSON(group.SOAP)
	}
	if group.Results != nil {
		merged.Results = cloneViaJSON(group.Results)
	}
	if group.WebSocket != nil {
		merged.WebSocket = cloneViaJSON(group.WebSocket)
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
	if group.BrowserEmulation != nil {
		merged.BrowserEmulation = cloneViaJSON(group.BrowserEmulation)
	}
	mergeRuntimeProtocolSections(merged, group)
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
	if group.JavaVuser != nil {
		merged.JavaVuser = mergeJavaVuserRuntimeOverride(merged.JavaVuser, group.JavaVuser)
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

var allowedScriptConfigOverrideSections = map[string]bool{
	"base_url":          true,
	"results":           true,
	"websocket":         true,
	"pacing":            true,
	"think_time":        true,
	"error_handling":    true,
	"log":               true,
	"run_logic":         true,
	"http":              true,
	"web_runtime":       true,
	"soap":              true,
	"browser":           true,
	"browser_emulation": true,
	// Native protocol sections (runtimeProtocolOverrideSections).
	"kafka":     true,
	"jms":       true,
	"eventhubs": true,
	"grpc":      true,
	"database":  true,
	"network":   true,
	"appium":    true,
	"java_http": true,
}

func normalizeRuntimeOverrideFields(fields []string) ([]string, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(fields))
	seen := make(map[string]bool, len(fields))
	for _, raw := range fields {
		canonical, err := normalizeOneRuntimeOverrideField(raw)
		if err != nil {
			return nil, err
		}
		if !seen[canonical] {
			seen[canonical] = true
			out = append(out, canonical)
		}
	}
	return out, nil
}

func normalizeOneRuntimeOverrideField(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("override_fields cannot contain empty field paths")
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "config.") {
		trimmed = strings.TrimSpace(trimmed[len("config."):])
		if trimmed == "" {
			return "", fmt.Errorf("unknown runtime override field %q", raw)
		}
	}
	lower := strings.ToLower(trimmed)
	switch lower {
	case "iterations":
		return "run_logic.iterations", nil
	case "log_level":
		return "log.level", nil
	case "auto_transactions":
		return "run_logic.auto_transactions", nil
	case "browser_config":
		return "browser", nil
	case "env", "redaction_rules":
		return lower, nil
	}
	if strings.HasPrefix(lower, "browser_config.") {
		trimmed = "browser." + trimmed[len("browser_config."):]
		lower = strings.ToLower(trimmed)
	}
	if strings.HasPrefix(lower, "env.") {
		key := strings.TrimSpace(trimmed[len("env."):])
		if key == "" {
			return "", fmt.Errorf("unknown runtime override field %q", raw)
		}
		return "env." + key, nil
	}
	if lower == "java_vuser" {
		return "java_vuser", nil
	}
	if strings.HasPrefix(lower, "java_vuser.") {
		sub := strings.TrimSpace(lower[len("java_vuser."):])
		if sub == "" || strings.Contains(sub, ".") {
			return "", fmt.Errorf("unknown runtime override field %q", raw)
		}
		if _, ok := findStructFieldByJSONTag(reflect.TypeOf(RuntimeJavaVuserOverride{}), sub); !ok {
			return "", fmt.Errorf("unknown runtime override field %q", raw)
		}
		return "java_vuser." + sub, nil
	}
	segments := strings.Split(trimmed, ".")
	firstSeg := strings.ToLower(strings.TrimSpace(segments[0]))
	if !allowedScriptConfigOverrideSections[firstSeg] {
		return "", fmt.Errorf("unknown runtime override field %q", raw)
	}
	canonicalSegments := make([]string, 0, len(segments))
	currentType := reflect.TypeOf(dsl.ScriptConfig{})
	for i := 0; i < len(segments); i++ {
		seg := strings.TrimSpace(segments[i])
		if seg == "" {
			return "", fmt.Errorf("unknown runtime override field %q", raw)
		}
		for currentType.Kind() == reflect.Pointer {
			currentType = currentType.Elem()
		}
		if currentType.Kind() != reflect.Struct {
			return "", fmt.Errorf("unknown runtime override field %q", raw)
		}
		field, ok := findStructFieldByJSONTag(currentType, strings.ToLower(seg))
		if !ok {
			return "", fmt.Errorf("unknown runtime override field %q", raw)
		}
		tagName := jsonTagName(field)
		canonicalSegments = append(canonicalSegments, tagName)
		fieldType := field.Type
		for fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
		}
		if i == len(segments)-1 {
			break
		}
		if fieldType.Kind() == reflect.Map {
			mapKey := strings.TrimSpace(strings.Join(segments[i+1:], "."))
			if mapKey == "" {
				return "", fmt.Errorf("unknown runtime override field %q", raw)
			}
			canonicalSegments = append(canonicalSegments, mapKey)
			break
		}
		if fieldType.Kind() != reflect.Struct {
			return "", fmt.Errorf("unknown runtime override field %q", raw)
		}
		currentType = fieldType
	}
	return strings.Join(canonicalSegments, "."), nil
}

func findStructFieldByJSONTag(structType reflect.Type, tag string) (reflect.StructField, bool) {
	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		if field.PkgPath != "" {
			continue
		}
		if jsonTagName(field) == tag {
			return field, true
		}
	}
	return reflect.StructField{}, false
}

func jsonTagName(field reflect.StructField) string {
	raw := field.Tag.Get("json")
	if raw == "" || raw == "-" {
		return ""
	}
	return strings.Split(raw, ",")[0]
}

func overrideFieldTopSection(canonicalPath string) string {
	if idx := strings.IndexByte(canonicalPath, '.'); idx > 0 {
		return canonicalPath[:idx]
	}
	return canonicalPath
}

func isCompactBrowserOverridePath(path string) bool {
	switch path {
	case "browser.headless", "browser.device_profile", "browser.touch_mode", "browser.orientation":
		return true
	default:
		return false
	}
}

func compactBrowserOverridePathsFromOverride(o *RuntimeSettingsOverride) []string {
	if o == nil {
		return nil
	}
	var b *RuntimeBrowserOverride
	if o.Browser != nil {
		b = o.Browser
	} else if o.BrowserConfig != nil {
		b = &RuntimeBrowserOverride{
			Headless:      o.BrowserConfig.Headless,
			DeviceProfile: o.BrowserConfig.DeviceProfile,
			TouchMode:     o.BrowserConfig.TouchMode,
			Orientation:   o.BrowserConfig.Orientation,
		}
	}
	if b == nil {
		return nil
	}
	var paths []string
	if b.Headless != nil {
		paths = append(paths, "browser.headless")
	}
	if b.DeviceProfile != "" {
		paths = append(paths, "browser.device_profile")
	}
	if b.TouchMode != nil {
		paths = append(paths, "browser.touch_mode")
	}
	if b.Orientation != "" {
		paths = append(paths, "browser.orientation")
	}
	return paths
}

func appendImplicitCompactOverrideFields(fields []string, o *RuntimeSettingsOverride) []string {
	if o == nil {
		return fields
	}
	hasRunLogicSubfield := slices.ContainsFunc(fields, func(p string) bool {
		return strings.HasPrefix(p, "run_logic.")
	})
	if (o.Iterations != nil || (o.RunLogic != nil && o.RunLogic.Iterations > 0 && hasRunLogicSubfield)) &&
		!slices.Contains(fields, "run_logic") && !slices.Contains(fields, "run_logic.iterations") {
		fields = append(fields, "run_logic.iterations")
	}
	if o.LogLevel != "" && !slices.Contains(fields, "log") && !slices.Contains(fields, "log.level") {
		fields = append(fields, "log.level")
	}
	if o.AutoTransactions != "" && !slices.Contains(fields, "run_logic") && !slices.Contains(fields, "run_logic.auto_transactions") {
		fields = append(fields, "run_logic.auto_transactions")
	}
	if o.Browser != nil && !slices.Contains(fields, "browser") {
		for _, bp := range compactBrowserOverridePathsFromOverride(o) {
			if !slices.Contains(fields, bp) {
				fields = append(fields, bp)
			}
		}
	}
	return fields
}

func applyRuntimeOverridesPartial(script *dsl.Script, overrides *RuntimeSettingsOverride) error {
	normalized, err := normalizeRuntimeOverrideFields(overrides.OverrideFields)
	if err != nil {
		return err
	}
	normalized = appendImplicitCompactOverrideFields(normalized, overrides)
	if clonedCfg := cloneViaJSON(&script.Config); clonedCfg != nil {
		script.Config = *clonedCfg
	}
	hasEnvAll := false
	var envKeys []string
	for _, path := range normalized {
		if path == "env" {
			hasEnvAll = true
		} else if strings.HasPrefix(path, "env.") {
			envKeys = append(envKeys, strings.TrimPrefix(path, "env."))
		}
	}
	if hasEnvAll || (len(envKeys) == 0 && len(overrides.Env) > 0) {
		if len(overrides.Env) > 0 {
			overridden, err := applyRuntimeEnvOverrides(script, overrides.Env)
			if err != nil {
				return err
			}
			*script = *overridden
		}
	} else if len(envKeys) > 0 {
		filteredEnv := make(map[string]string, len(envKeys))
		for _, k := range envKeys {
			filteredEnv[k] = overrides.Env[k]
		}
		overridden, err := applyRuntimeEnvOverrides(script, filteredEnv)
		if err != nil {
			return err
		}
		*script = *overridden
	}

	srcCfg := overrideToScriptConfig(overrides)
	for _, path := range normalized {
		if path == "env" || strings.HasPrefix(path, "env.") || path == "redaction_rules" {
			continue
		}
		if path == "java_vuser" || strings.HasPrefix(path, "java_vuser.") {
			if (path == "java_vuser" || path == "java_vuser.additional_vm_parameters") &&
				overrides.JavaVuser != nil && len(overrides.JavaVuser.AdditionalVMParameters) > 0 {
				overrideProbe := &dsl.Script{}
				overrideProbe.Config.JavaVuser.AdditionalVMParameters =
					append([]string(nil), overrides.JavaVuser.AdditionalVMParameters...)
				if err := validateJavaVMParameterDisclosureSafety(overrideProbe); err != nil {
					return err
				}
			}
			applyJavaVuserOverrideField(&script.Config.JavaVuser, overrides.JavaVuser, path)
			continue
		}
		if err := applyConfigOverrideField(&script.Config, &srcCfg, path); err != nil {
			return err
		}
	}
	return nil
}

func overrideToScriptConfig(overrides *RuntimeSettingsOverride) dsl.ScriptConfig {
	var cfg dsl.ScriptConfig
	if overrides == nil {
		return cfg
	}
	cfg.BaseURL = overrides.BaseURL
	if overrides.Results != nil {
		cfg.Results = *cloneViaJSON(overrides.Results)
	}
	if overrides.WebSocket != nil {
		cfg.WebSocket = *cloneViaJSON(overrides.WebSocket)
	}
	if overrides.Pacing != nil {
		cfg.Pacing = *overrides.Pacing
	}
	if overrides.ThinkTime != nil {
		cfg.ThinkTime = *overrides.ThinkTime
	}
	if overrides.ErrorHandling != nil {
		cfg.ErrorHandling = *overrides.ErrorHandling
	}
	if overrides.Log != nil {
		cfg.Log = *cloneLogConfig(overrides.Log)
	}
	if overrides.LogLevel != "" && (overrides.Log == nil || overrides.Log.Level == "") {
		cfg.Log.Level = overrides.LogLevel
	}
	if overrides.RunLogic != nil {
		cfg.RunLogic = *cloneRunLogicConfig(overrides.RunLogic)
	}
	if overrides.Iterations != nil && (overrides.RunLogic == nil || overrides.RunLogic.Iterations == 0) {
		cfg.RunLogic.Iterations = *overrides.Iterations
	}
	if overrides.AutoTransactions != "" && (overrides.RunLogic == nil || overrides.RunLogic.AutoTransactions == "") {
		cfg.RunLogic.AutoTransactions = overrides.AutoTransactions
	}
	if overrides.HTTP != nil {
		cfg.HTTP = *cloneViaJSON(overrides.HTTP)
	}
	if overrides.WebRuntime != nil {
		cfg.WebRuntime = *cloneViaJSON(overrides.WebRuntime)
	}
	if overrides.SOAP != nil {
		cfg.SOAP = *cloneViaJSON(overrides.SOAP)
	}
	if overrides.BrowserEmulation != nil {
		cfg.BrowserEmulation = cloneViaJSON(overrides.BrowserEmulation)
	}
	applyRuntimeProtocolSections(&cfg, overrides)
	if overrides.BrowserConfig != nil {
		cfg.Browser = *cloneViaJSON(overrides.BrowserConfig)
	}
	if overrides.Browser != nil {
		if overrides.BrowserConfig == nil || overrides.BrowserConfig.Headless == nil {
			if overrides.Browser.Headless != nil {
				headless := *overrides.Browser.Headless
				cfg.Browser.Headless = &headless
			}
		}
		if overrides.BrowserConfig == nil || overrides.BrowserConfig.DeviceProfile == "" {
			if overrides.Browser.DeviceProfile != "" {
				cfg.Browser.DeviceProfile = overrides.Browser.DeviceProfile
			}
		}
		if overrides.BrowserConfig == nil || overrides.BrowserConfig.TouchMode == nil {
			if overrides.Browser.TouchMode != nil {
				touchMode := *overrides.Browser.TouchMode
				cfg.Browser.TouchMode = &touchMode
			}
		}
		if overrides.BrowserConfig == nil || overrides.BrowserConfig.Orientation == "" {
			if overrides.Browser.Orientation != "" {
				cfg.Browser.Orientation = overrides.Browser.Orientation
			}
		}
	}
	return cfg
}

func applyJavaVuserOverrideField(dst *dsl.JavaVuserConfig, src *RuntimeJavaVuserOverride, path string) {
	if dst == nil {
		return
	}
	if path == "java_vuser" {
		if src == nil {
			*dst = dsl.JavaVuserConfig{}
			return
		}
		applyJavaVuserRuntimeOverride(dst, src)
		return
	}
	var zeroSrc RuntimeJavaVuserOverride
	if src != nil {
		zeroSrc = *src
	}
	switch strings.TrimPrefix(path, "java_vuser.") {
	case "jdk_selection":
		dst.JDKSelection = zeroSrc.JDKSelection
	case "jdk_home":
		dst.JDKHome = zeroSrc.JDKHome
	case "additional_vm_parameters":
		if len(zeroSrc.AdditionalVMParameters) > 0 {
			dst.AdditionalVMParameters = append([]string(nil), zeroSrc.AdditionalVMParameters...)
		} else {
			dst.AdditionalVMParameters = nil
		}
	case "classpath":
		if len(zeroSrc.Classpath) > 0 {
			dst.Classpath = append([]string(nil), zeroSrc.Classpath...)
		} else {
			dst.Classpath = nil
		}
	case "use_xbootclasspath":
		dst.UseXBootClasspath = cloneBoolPointer(zeroSrc.UseXBootClasspath)
	case "dedicated_class_loader":
		dst.DedicatedClassLoader = cloneBoolPointer(zeroSrc.DedicatedClassLoader)
	case "skip_parameter_evaluation":
		dst.SkipParameterEvaluation = cloneBoolPointer(zeroSrc.SkipParameterEvaluation)
	case "jvm_sharing":
		dst.JVMSharing = zeroSrc.JVMSharing
		if !dsl.JavaVuserSharesJVMs(zeroSrc.JVMSharing) {
			dst.VUsersPerJVM = 0
		}
	case "vusers_per_jvm":
		dst.VUsersPerJVM = zeroSrc.VUsersPerJVM
	case "startup_timeout_ms":
		dst.StartupTimeoutMs = zeroSrc.StartupTimeoutMs
	case "action_timeout_ms":
		dst.ActionTimeoutMs = zeroSrc.ActionTimeoutMs
	case "param_left_brace":
		dst.ParamLeftBrace = zeroSrc.ParamLeftBrace
	case "param_right_brace":
		dst.ParamRightBrace = zeroSrc.ParamRightBrace
	}
}

func applyConfigOverrideField(dstCfg, srcCfg *dsl.ScriptConfig, canonicalPath string) error {
	segments := strings.Split(canonicalPath, ".")
	dstVal := reflect.ValueOf(dstCfg).Elem()
	srcVal := reflect.ValueOf(srcCfg).Elem()

	for i := 0; i < len(segments); i++ {
		seg := segments[i]
		// dst and src share a type at every level, so their pointers align.
		for dstVal.Kind() == reflect.Pointer {
			srcNil := srcVal.IsNil()
			if dstVal.IsNil() {
				if srcNil {
					// The parent block exists in neither layer: the listed
					// leaf copies that absence. Allocating it would turn an
					// absent block (no Kafka OAuth, no JMS TLS) into an
					// explicit empty one that validation and engines read
					// as configured.
					return nil
				}
				dstVal.Set(reflect.New(dstVal.Type().Elem()))
			}
			dstVal = dstVal.Elem()
			if srcNil {
				srcVal = reflect.Zero(srcVal.Type().Elem())
			} else {
				srcVal = srcVal.Elem()
			}
		}
		field, ok := findStructFieldByJSONTag(dstVal.Type(), strings.ToLower(seg))
		if !ok {
			return fmt.Errorf("unknown runtime override field %q", canonicalPath)
		}
		dstField := dstVal.FieldByIndex(field.Index)
		srcField := srcVal.FieldByIndex(field.Index)
		if i == len(segments)-1 {
			// A listed path with no value copies that absence: for the
			// tri-state continue_on_error a nil means "not set at this layer",
			// never an explicit opt-out.
			deepCopyReflectValue(dstField, srcField)
			return nil
		}
		if dstField.Kind() == reflect.Map {
			mapKey := strings.Join(segments[i+1:], ".")
			keyVal := reflect.ValueOf(mapKey)
			if srcField.IsValid() && !srcField.IsNil() {
				elemVal := srcField.MapIndex(keyVal)
				if elemVal.IsValid() {
					if dstField.IsNil() {
						dstField.Set(reflect.MakeMap(dstField.Type()))
					}
					clonedElem := reflect.New(dstField.Type().Elem()).Elem()
					deepCopyReflectValue(clonedElem, elemVal)
					dstField.SetMapIndex(keyVal, clonedElem)
					return nil
				}
			}
			if !dstField.IsNil() {
				dstField.SetMapIndex(keyVal, reflect.Value{})
			}
			return nil
		}
		dstVal = dstField
		srcVal = srcField
	}
	return nil
}

func deepCopyReflectValue(dst, src reflect.Value) {
	if !src.IsValid() {
		dst.Set(reflect.Zero(dst.Type()))
		return
	}
	switch src.Kind() {
	case reflect.Pointer, reflect.Map:
		if src.IsNil() {
			dst.Set(reflect.Zero(dst.Type()))
			return
		}
	case reflect.Slice:
		if src.IsNil() {
			dst.Set(reflect.Zero(dst.Type()))
			return
		}
		if src.Len() == 0 {
			dst.Set(reflect.MakeSlice(dst.Type(), 0, 0))
			return
		}
	}
	data, err := json.Marshal(src.Interface())
	if err != nil {
		dst.Set(src)
		return
	}
	ptr := reflect.New(dst.Type())
	if err := json.Unmarshal(data, ptr.Interface()); err != nil {
		dst.Set(src)
		return
	}
	dst.Set(ptr.Elem())
}

func effectiveLegacyOverrideFields(o *RuntimeSettingsOverride) []string {
	if o == nil {
		return nil
	}
	var fields []string
	if o.BaseURL != "" {
		fields = append(fields, "base_url")
	}
	if o.Results != nil {
		fields = append(fields, "results")
	}
	if o.WebSocket != nil {
		fields = append(fields, "websocket")
	}
	if o.Pacing != nil {
		fields = append(fields, "pacing")
	}
	if o.ThinkTime != nil {
		fields = append(fields, "think_time")
	}
	if o.ErrorHandling != nil {
		fields = append(fields, "error_handling")
	}
	if o.Log != nil {
		fields = append(fields, "log")
	} else if o.LogLevel != "" {
		fields = append(fields, "log.level")
	}
	if o.RunLogic != nil {
		fields = append(fields, "run_logic")
	} else {
		if o.Iterations != nil {
			fields = append(fields, "run_logic.iterations")
		}
		if o.AutoTransactions != "" {
			fields = append(fields, "run_logic.auto_transactions")
		}
	}
	if o.HTTP != nil {
		fields = append(fields, "http")
	}
	if o.SOAP != nil {
		fields = append(fields, "soap")
	}
	if o.WebRuntime != nil {
		wr := o.WebRuntime
		if len(wr.AdditionalAttributes) > 0 {
			fields = append(fields, "web_runtime.additional_attributes")
		}
		if wr.DownloadFilters.Mode != "" || len(wr.DownloadFilters.Hosts) > 0 {
			fields = append(fields, "web_runtime.download_filters")
		}
		if len(wr.ContentChecks) > 0 {
			fields = append(fields, "web_runtime.content_checks")
		}
		if wr.NetworkSimulation != (dsl.NetworkSimulationConfig{}) {
			fields = append(fields, "web_runtime.network_simulation")
		}
		if wr.Streaming != (dsl.StreamingConfig{}) {
			fields = append(fields, "web_runtime.streaming")
		}
		if wr.DataFormatting.AutoDetect || len(wr.DataFormatting.Chain) > 0 {
			fields = append(fields, "web_runtime.data_formatting")
		}
		if len(wr.SourceIPs) > 0 {
			fields = append(fields, "web_runtime.source_ips")
		}
		if len(wr.NetworkProfiles) > 0 {
			fields = append(fields, "web_runtime.network_profiles")
		}
	}
	if o.BrowserEmulation != nil {
		fields = append(fields, "browser_emulation")
	}
	fields = append(fields, presentRuntimeProtocolSections(o)...)
	if o.BrowserConfig != nil {
		fields = append(fields, "browser")
	} else if o.Browser != nil {
		if o.Browser.Headless != nil {
			fields = append(fields, "browser.headless")
		}
		if o.Browser.DeviceProfile != "" {
			fields = append(fields, "browser.device_profile")
		}
		if o.Browser.TouchMode != nil {
			fields = append(fields, "browser.touch_mode")
		}
		if o.Browser.Orientation != "" {
			fields = append(fields, "browser.orientation")
		}
	}
	if o.JavaVuser != nil {
		jv := o.JavaVuser
		if jv.JDKSelection != "" {
			fields = append(fields, "java_vuser.jdk_selection")
		}
		if jv.JDKHome != "" {
			fields = append(fields, "java_vuser.jdk_home")
		}
		if len(jv.AdditionalVMParameters) > 0 {
			fields = append(fields, "java_vuser.additional_vm_parameters")
		}
		if len(jv.Classpath) > 0 {
			fields = append(fields, "java_vuser.classpath")
		}
		if jv.UseXBootClasspath != nil {
			fields = append(fields, "java_vuser.use_xbootclasspath")
		}
		if jv.DedicatedClassLoader != nil {
			fields = append(fields, "java_vuser.dedicated_class_loader")
		}
		if jv.SkipParameterEvaluation != nil {
			fields = append(fields, "java_vuser.skip_parameter_evaluation")
		}
		if jv.JVMSharing != "" {
			fields = append(fields, "java_vuser.jvm_sharing")
		}
		if jv.VUsersPerJVM != 0 {
			fields = append(fields, "java_vuser.vusers_per_jvm")
		}
		if jv.StartupTimeoutMs != 0 {
			fields = append(fields, "java_vuser.startup_timeout_ms")
		}
		if jv.ActionTimeoutMs != 0 {
			fields = append(fields, "java_vuser.action_timeout_ms")
		}
		if jv.ParamLeftBrace != "" {
			fields = append(fields, "java_vuser.param_left_brace")
		}
		if jv.ParamRightBrace != "" {
			fields = append(fields, "java_vuser.param_right_brace")
		}
	}
	if len(o.Env) > 0 {
		fields = append(fields, "env")
	}
	if len(o.RedactionRules) > 0 {
		fields = append(fields, "redaction_rules")
	}
	return fields
}

func resolveEffectiveOverrideFields(o *RuntimeSettingsOverride) []string {
	if o == nil {
		return nil
	}
	if len(o.OverrideFields) > 0 {
		fields, err := normalizeRuntimeOverrideFields(o.OverrideFields)
		if err != nil {
			fields = append([]string(nil), o.OverrideFields...)
		}
		if len(o.Env) > 0 {
			hasEnv := false
			for _, f := range fields {
				if f == "env" || strings.HasPrefix(f, "env.") {
					hasEnv = true
					break
				}
			}
			if !hasEnv {
				fields = append(fields, "env")
			}
		}
		if len(o.RedactionRules) > 0 && !slices.Contains(fields, "redaction_rules") {
			fields = append(fields, "redaction_rules")
		}
		fields = appendImplicitCompactOverrideFields(fields, o)
		return fields
	}
	return effectiveLegacyOverrideFields(o)
}

func combineOverrideFields(baseFields, groupFields []string) []string {
	out := make([]string, 0, len(baseFields)+len(groupFields))
	for _, bf := range baseFields {
		replacedByGroup := false
		for _, gf := range groupFields {
			if bf == gf || strings.HasPrefix(bf, gf+".") {
				replacedByGroup = true
				break
			}
		}
		if !replacedByGroup {
			out = append(out, bf)
		}
	}
	for _, gf := range groupFields {
		coveredByBase := false
		for _, existing := range out {
			if gf == existing || strings.HasPrefix(gf, existing+".") {
				coveredByBase = true
				break
			}
		}
		if !coveredByBase {
			out = append(out, gf)
		}
	}
	return out
}

// v1RowForPartialMerge returns o unchanged when it is nil or already partial,
// and otherwise a clone without the compact aliases v1 ignores beside their
// section, so the partial merge applies exactly what v1 applied. The caller's
// row is never modified.
func v1RowForPartialMerge(o *RuntimeSettingsOverride) *RuntimeSettingsOverride {
	if o == nil || len(o.OverrideFields) > 0 {
		return o
	}
	shadowed := (o.Log != nil && o.LogLevel != "") ||
		(o.RunLogic != nil && (o.Iterations != nil || o.AutoTransactions != "")) ||
		(o.BrowserConfig != nil && o.Browser != nil)
	if !shadowed {
		return o
	}
	clone := cloneRuntimeSettings(o)
	dropV1ShadowedRuntimeAliases(clone)
	return clone
}

func mergeRuntimeSettingsPartial(base, group *RuntimeSettingsOverride) *RuntimeSettingsOverride {
	// Either side may still be a v1 row (no override_fields) merged beside a
	// partial one. v1 lets a section win over its compact alias, while the
	// partial path fills a zero section value from the alias, so normalize a
	// v1 side the same way a converted row is normalized before reading it.
	base = v1RowForPartialMerge(base)
	group = v1RowForPartialMerge(group)
	baseFields := resolveEffectiveOverrideFields(base)
	groupFields := resolveEffectiveOverrideFields(group)

	baseSrcCfg := overrideToScriptConfig(base)
	groupSrcCfg := overrideToScriptConfig(group)
	var mergedCfg dsl.ScriptConfig

	var mergedJavaCfg dsl.JavaVuserConfig
	for _, path := range baseFields {
		if path == "env" || strings.HasPrefix(path, "env.") || path == "redaction_rules" {
			continue
		}
		if path == "java_vuser" || strings.HasPrefix(path, "java_vuser.") {
			if base != nil {
				applyJavaVuserOverrideField(&mergedJavaCfg, base.JavaVuser, path)
			}
			continue
		}
		_ = applyConfigOverrideField(&mergedCfg, &baseSrcCfg, path)
	}

	for _, path := range groupFields {
		if path == "env" || strings.HasPrefix(path, "env.") || path == "redaction_rules" {
			continue
		}
		if path == "java_vuser" || strings.HasPrefix(path, "java_vuser.") {
			if group != nil {
				applyJavaVuserOverrideField(&mergedJavaCfg, group.JavaVuser, path)
			}
			continue
		}
		_ = applyConfigOverrideField(&mergedCfg, &groupSrcCfg, path)
	}

	combinedFields := combineOverrideFields(baseFields, groupFields)
	merged := &RuntimeSettingsOverride{
		OverrideFields: combinedFields,
	}
	touchedSections := make(map[string]bool, len(combinedFields))
	for _, path := range combinedFields {
		touchedSections[overrideFieldTopSection(path)] = true
	}

	if touchedSections["base_url"] {
		merged.BaseURL = mergedCfg.BaseURL
	}
	if touchedSections["results"] {
		merged.Results = cloneViaJSON(&mergedCfg.Results)
	}
	if touchedSections["websocket"] {
		merged.WebSocket = cloneViaJSON(&mergedCfg.WebSocket)
	}
	if touchedSections["pacing"] {
		pacing := mergedCfg.Pacing
		merged.Pacing = &pacing
	}
	if touchedSections["think_time"] {
		thinkTime := mergedCfg.ThinkTime
		merged.ThinkTime = &thinkTime
	}
	if touchedSections["error_handling"] {
		errorHandling := mergedCfg.ErrorHandling
		merged.ErrorHandling = &errorHandling
	}
	if touchedSections["log"] {
		merged.Log = cloneLogConfig(&mergedCfg.Log)
	}
	if touchedSections["run_logic"] {
		onlyAutoTransactions := true
		for _, path := range combinedFields {
			if overrideFieldTopSection(path) == "run_logic" && path != "run_logic.auto_transactions" {
				onlyAutoTransactions = false
				break
			}
		}
		if onlyAutoTransactions {
			merged.AutoTransactions = mergedCfg.RunLogic.AutoTransactions
		} else {
			merged.RunLogic = cloneRunLogicConfig(&mergedCfg.RunLogic)
		}
	}
	if touchedSections["http"] {
		merged.HTTP = cloneViaJSON(&mergedCfg.HTTP)
	}
	if touchedSections["web_runtime"] {
		merged.WebRuntime = cloneViaJSON(&mergedCfg.WebRuntime)
	}
	if touchedSections["soap"] {
		merged.SOAP = cloneViaJSON(&mergedCfg.SOAP)
	}
	if touchedSections["browser"] {
		merged.BrowserConfig = cloneViaJSON(&mergedCfg.Browser)
	}
	if touchedSections["browser_emulation"] {
		merged.BrowserEmulation = cloneViaJSON(mergedCfg.BrowserEmulation)
	}
	runtimeProtocolSectionsFromMergedConfig(merged, &mergedCfg, touchedSections)
	if touchedSections["java_vuser"] {
		merged.JavaVuser = &RuntimeJavaVuserOverride{
			JDKSelection:            mergedJavaCfg.JDKSelection,
			JDKHome:                 mergedJavaCfg.JDKHome,
			AdditionalVMParameters:  append([]string(nil), mergedJavaCfg.AdditionalVMParameters...),
			Classpath:               append([]string(nil), mergedJavaCfg.Classpath...),
			UseXBootClasspath:       cloneBoolPointer(mergedJavaCfg.UseXBootClasspath),
			DedicatedClassLoader:    cloneBoolPointer(mergedJavaCfg.DedicatedClassLoader),
			SkipParameterEvaluation: cloneBoolPointer(mergedJavaCfg.SkipParameterEvaluation),
			JVMSharing:              mergedJavaCfg.JVMSharing,
			VUsersPerJVM:            mergedJavaCfg.VUsersPerJVM,
			StartupTimeoutMs:        mergedJavaCfg.StartupTimeoutMs,
			ActionTimeoutMs:         mergedJavaCfg.ActionTimeoutMs,
			ParamLeftBrace:          mergedJavaCfg.ParamLeftBrace,
			ParamRightBrace:         mergedJavaCfg.ParamRightBrace,
		}
	}

	if base != nil {
		for _, path := range baseFields {
			if path == "env" {
				if len(base.Env) > 0 {
					if merged.Env == nil {
						merged.Env = make(map[string]string, len(base.Env))
					}
					for k, v := range base.Env {
						merged.Env[k] = v
					}
				}
			} else if strings.HasPrefix(path, "env.") {
				key := strings.TrimPrefix(path, "env.")
				if merged.Env == nil {
					merged.Env = make(map[string]string)
				}
				merged.Env[key] = base.Env[key]
			}
		}
	}
	if group != nil {
		for _, path := range groupFields {
			if path == "env" {
				if len(group.OverrideFields) > 0 && len(group.Env) == 0 {
					merged.Env = nil
				} else if len(group.Env) > 0 {
					if merged.Env == nil {
						merged.Env = make(map[string]string, len(group.Env))
					}
					for k, v := range group.Env {
						merged.Env[k] = v
					}
				}
			} else if strings.HasPrefix(path, "env.") {
				key := strings.TrimPrefix(path, "env.")
				if merged.Env == nil {
					merged.Env = make(map[string]string)
				}
				merged.Env[key] = group.Env[key]
			}
		}
	}
	if slices.Contains(baseFields, "redaction_rules") && base != nil && len(base.RedactionRules) > 0 {
		merged.RedactionRules = cloneRedactionRules(base.RedactionRules)
	}
	if slices.Contains(groupFields, "redaction_rules") && group != nil {
		merged.RedactionRules = cloneRedactionRules(group.RedactionRules)
	}
	return merged
}

// overridePathBelongsToRuntimeField reports whether a canonical
// override_fields path is owned by a Command Center / replace-style runtime
// field name (a section name or one of the compact aliases).
func overridePathBelongsToRuntimeField(path, field string) bool {
	top := overrideFieldTopSection(path)
	if top == "browser" {
		return field == "browser_config" || (field == "browser" && isCompactBrowserOverridePath(path))
	}
	switch field {
	case top:
		return true
	case "iterations":
		return path == "run_logic.iterations"
	case "log_level":
		return path == "log.level"
	case "auto_transactions":
		return path == "run_logic.auto_transactions"
	}
	return false
}

func reconcileReplacedOverrideFields(target, next, src *RuntimeSettingsOverride, replacedFields []string) []string {
	targetFields := resolveEffectiveOverrideFields(target)
	srcFields := resolveEffectiveOverrideFields(src)
	replacedSet := make(map[string]bool, len(replacedFields))
	for _, f := range replacedFields {
		replacedSet[f] = true
	}
	matchesReplaced := func(path string) bool {
		return slices.ContainsFunc(replacedFields, func(field string) bool {
			return overridePathBelongsToRuntimeField(path, field)
		})
	}
	var out []string
	seen := make(map[string]bool)
	addField := func(path string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		out = append(out, path)
	}
	for _, path := range targetFields {
		if !matchesReplaced(path) {
			addField(path)
		}
	}
	for _, path := range srcFields {
		if matchesReplaced(path) {
			addField(path)
		}
	}
	if next != nil {
		if replacedSet["iterations"] && !seen["run_logic"] &&
			(next.Iterations != nil || (next.RunLogic != nil && next.RunLogic.Iterations > 0)) {
			addField("run_logic.iterations")
		}
		if replacedSet["log_level"] && !seen["log"] &&
			(next.LogLevel != "" || (next.Log != nil && next.Log.Level != "")) {
			addField("log.level")
		}
		if replacedSet["auto_transactions"] && !seen["run_logic"] &&
			(next.AutoTransactions != "" || (next.RunLogic != nil && next.RunLogic.AutoTransactions != "")) {
			addField("run_logic.auto_transactions")
		}
		if replacedSet["browser"] && !seen["browser"] {
			for _, bp := range compactBrowserOverridePathsFromOverride(next) {
				addField(bp)
			}
		}
	}
	return out
}

// runtimeOverrideAppliesField reports whether overrides applies canonicalPath
// at all. A legacy (v1) override with no override_fields applies every section
// it carries, so the caller's own section-presence check decides; a partial
// override applies only the listed paths (and the compact aliases it carries),
// exactly as applyRuntimeOverridesPartial and the save-time projection do.
func runtimeOverrideAppliesField(overrides *RuntimeSettingsOverride, canonicalPath string) bool {
	if overrides == nil {
		return false
	}
	if len(overrides.OverrideFields) == 0 {
		return true
	}
	for _, path := range resolveEffectiveOverrideFields(overrides) {
		if path == canonicalPath || strings.HasPrefix(canonicalPath, path+".") {
			return true
		}
	}
	return false
}

func runtimeOverridesSpecifiesIterations(overrides *RuntimeSettingsOverride) bool {
	if overrides == nil {
		return false
	}
	if len(overrides.OverrideFields) == 0 {
		return (overrides.RunLogic != nil && overrides.RunLogic.Iterations > 0) ||
			(overrides.Iterations != nil && *overrides.Iterations > 0)
	}
	// Partial overrides: iterations apply when run_logic.iterations is listed
	// (directly or via run_logic) or implied by a compact iterations alias,
	// exactly as applyRuntimeOverridesPartial applies them.
	return runtimeOverrideAppliesField(overrides, "run_logic.iterations")
}

func runtimeOverridesSpecifiesThinkTime(overrides *RuntimeSettingsOverride) bool {
	if overrides == nil {
		return false
	}
	if len(overrides.OverrideFields) == 0 {
		return overrides.ThinkTime != nil
	}
	normalized, err := normalizeRuntimeOverrideFields(overrides.OverrideFields)
	if err != nil {
		return false
	}
	for _, path := range normalized {
		if path == "think_time" || strings.HasPrefix(path, "think_time.") {
			return true
		}
	}
	return false
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
