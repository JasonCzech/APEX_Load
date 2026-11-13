package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/scrub"
)

var cliEnvPattern = regexp.MustCompile(`\{\{\s*env\.([^{}\s]+)\s*\}\}`)

type uploadScriptWrapper struct {
	Script *dsl.Script `json:"script"`
}

func loadScriptForRun(path string, args runArgs) (*dsl.Script, error) {
	script, err := dsl.ParseFile(path)
	if err != nil {
		return nil, err
	}
	return applyRuntimeOverrides(script, args)
}

func loadScriptSnapshotForRun(path string, data []byte, args runArgs) (*dsl.Script, error) {
	script, err := dsl.ParseFileSnapshot(path, data)
	if err != nil {
		return nil, err
	}
	return applyRuntimeOverrides(script, args)
}

func applyRuntimeOverrides(script *dsl.Script, args runArgs) (*dsl.Script, error) {
	if script == nil {
		return nil, fmt.Errorf("script is required")
	}
	cloned, err := cloneScript(script)
	if err != nil {
		return nil, err
	}
	if len(args.env) > 0 {
		payload, err := json.Marshal(cloned)
		if err != nil {
			return nil, fmt.Errorf("marshal script for overrides: %w", err)
		}
		var generic any
		if err := json.Unmarshal(payload, &generic); err != nil {
			return nil, fmt.Errorf("prepare script overrides: %w", err)
		}
		generic = replaceEnvTemplates(generic, args.env)
		payload, err = json.Marshal(generic)
		if err != nil {
			return nil, fmt.Errorf("marshal overridden script: %w", err)
		}
		var overridden dsl.Script
		if err := json.Unmarshal(payload, &overridden); err != nil {
			return nil, fmt.Errorf("decode overridden script: %w", err)
		}
		cloned = &overridden
	}
	if args.baseURL != "" {
		cloned.Config.BaseURL = args.baseURL
	}
	return cloned, nil
}

func cloneScript(script *dsl.Script) (*dsl.Script, error) {
	payload, err := json.Marshal(script)
	if err != nil {
		return nil, fmt.Errorf("clone script: %w", err)
	}
	var cloned dsl.Script
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return nil, fmt.Errorf("clone script: %w", err)
	}
	return &cloned, nil
}

func replaceEnvTemplates(value any, overrides map[string]string) any {
	switch typed := value.(type) {
	case string:
		return cliEnvPattern.ReplaceAllStringFunc(typed, func(token string) string {
			match := cliEnvPattern.FindStringSubmatch(token)
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
			resolved[i] = replaceEnvTemplates(typed[i], overrides)
		}
		return resolved
	case map[string]any:
		resolved := make(map[string]any, len(typed))
		for key, item := range typed {
			resolved[key] = replaceEnvTemplates(item, overrides)
		}
		return resolved
	default:
		return value
	}
}

func marshalScriptForUpload(script *dsl.Script) ([]byte, error) {
	payload, err := json.Marshal(uploadScriptWrapper{Script: script})
	if err != nil {
		return nil, fmt.Errorf("marshal upload script: %w", err)
	}
	return payload, nil
}

func parseEnvAssignment(raw string) (string, string, bool) {
	key, value, ok := strings.Cut(raw, "=")
	key = strings.TrimSpace(key)
	if !ok || key == "" {
		return "", "", false
	}
	return key, value, true
}

// cliRuntimeSettingsOverride mirrors the controller's RuntimeSettingsOverride
// (pkg/api/runner.go), which the CLI cannot import. It is used to check the
// JSON's types and to find the credentials it carries; the controller receives
// the operator's JSON object as written, so a field this copy lacks still
// reaches it. TestCLIRuntimeSettingsOverrideMirrorsControllerFields keeps the
// field sets equal.
type cliRuntimeSettingsOverride struct {
	Results          *dsl.ResultsConfig           `json:"results,omitempty"`
	WebSocket        *dsl.WebSocketConfig         `json:"websocket,omitempty"`
	Iterations       *int                         `json:"iterations,omitempty"`
	Pacing           *dsl.PacingConfig            `json:"pacing,omitempty"`
	ThinkTime        *dsl.ThinkTimeConfig         `json:"think_time,omitempty"`
	ErrorHandling    *dsl.ErrorHandlingConfig     `json:"error_handling,omitempty"`
	Log              *dsl.LogConfig               `json:"log,omitempty"`
	RunLogic         *dsl.RunLogicConfig          `json:"run_logic,omitempty"`
	HTTP             *dsl.HTTPConfig              `json:"http,omitempty"`
	WebRuntime       *dsl.WebRuntimeConfig        `json:"web_runtime,omitempty"`
	SOAP             *dsl.SOAPConfig              `json:"soap,omitempty"`
	JavaVuser        *cliRuntimeJavaVuserOverride `json:"java_vuser,omitempty"`
	BrowserConfig    *dsl.BrowserConfig           `json:"browser_config,omitempty"`
	Browser          *cliRuntimeBrowserOverride   `json:"browser,omitempty"`
	BrowserEmulation *dsl.BrowserEmulationConfig  `json:"browser_emulation,omitempty"`
	LogLevel         string                       `json:"log_level,omitempty"`
	AutoTransactions string                       `json:"auto_transactions,omitempty"`
	BaseURL          string                       `json:"base_url,omitempty"`
	Env              map[string]string            `json:"env,omitempty"`
	RedactionRules   []engine.RedactionRule       `json:"redaction_rules,omitempty"`
	OverrideFields   []string                     `json:"override_fields,omitempty"`
}

// cliRuntimeJavaVuserOverride mirrors the controller's RuntimeJavaVuserOverride.
type cliRuntimeJavaVuserOverride struct {
	JDKSelection            string   `json:"jdk_selection,omitempty"`
	JDKHome                 string   `json:"jdk_home,omitempty"`
	AdditionalVMParameters  []string `json:"additional_vm_parameters,omitempty"`
	Classpath               []string `json:"classpath,omitempty"`
	UseXBootClasspath       *bool    `json:"use_xbootclasspath,omitempty"`
	DedicatedClassLoader    *bool    `json:"dedicated_class_loader,omitempty"`
	SkipParameterEvaluation *bool    `json:"skip_parameter_evaluation,omitempty"`
	JVMSharing              string   `json:"jvm_sharing,omitempty"`
	VUsersPerJVM            int      `json:"vusers_per_jvm,omitempty"`
}

// cliRuntimeBrowserOverride mirrors the controller's RuntimeBrowserOverride.
type cliRuntimeBrowserOverride struct {
	Headless      *bool  `json:"headless,omitempty"`
	DeviceProfile string `json:"device_profile,omitempty"`
	TouchMode     *bool  `json:"touch_mode,omitempty"`
	Orientation   string `json:"orientation,omitempty"`
}

// cliRuntimeOverrides is a validated --runtime-overrides(-file) value.
type cliRuntimeOverrides struct {
	// raw is the operator's JSON object, compacted; it is sent to the
	// controller as the created run's runtime_settings.
	raw    json.RawMessage
	parsed cliRuntimeSettingsOverride
	// unknownField names a field this apexctl does not know. The controller
	// ignores fields it does not know, as it does for every API client, so
	// the run still starts; runTest warns about it.
	unknownField string
	// credentials are the credential values the overrides carry; the
	// controller stores them as platform secrets, and the CLI keeps them out
	// of everything it prints.
	credentials []string
}

const runtimeOverrideReservedSecretPrefix = "APEX_RUNTIME_OVERRIDE_"

var cliRuntimeOverrideSecretReferencePattern = regexp.MustCompile(`\{\{\s*secret\.\s*(` + engine.LogicalSecretNameInputPattern + `)\s*\}\}`)
var cliRuntimeOverrideExactSecretPattern = regexp.MustCompile(`^\{\{\s*secret\.(` + engine.LogicalSecretNameInputPattern + `)\s*\}\}$`)
var cliUnknownJSONFieldPattern = regexp.MustCompile(`json: unknown field "([^"]*)"`)

// loadRuntimeOverrides reads --runtime-overrides or --runtime-overrides-file.
// It returns nil when neither flag was given.
func loadRuntimeOverrides(inline, path string) (*cliRuntimeOverrides, error) {
	inlineSet := strings.TrimSpace(inline) != ""
	pathSet := strings.TrimSpace(path) != ""
	if inlineSet && pathSet {
		return nil, fmt.Errorf("use --runtime-overrides or --runtime-overrides-file, not both")
	}
	flagName := "--runtime-overrides"
	data := []byte(inline)
	if pathSet {
		flagName = "--runtime-overrides-file"
		var err error
		data, err = os.ReadFile(strings.TrimSpace(path))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", flagName, err)
		}
	} else if !inlineSet {
		return nil, nil
	}
	overrides, err := parseRuntimeOverridesJSON(data)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", flagName, err)
	}
	return overrides, nil
}

// parseRuntimeOverridesJSON decodes a runtime settings override object as
// the controller decodes a run's runtime_settings (fields it does not know
// are ignored), and refuses what the controller would refuse for a new run.
func parseRuntimeOverridesJSON(data []byte) (*cliRuntimeOverrides, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("runtime overrides must be a JSON object")
	}
	var parsed cliRuntimeSettingsOverride
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	if err := decoder.Decode(&parsed); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("runtime overrides must be a single JSON object")
	}
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, trimmed); err != nil {
		return nil, err
	}
	overrides := &cliRuntimeOverrides{raw: json.RawMessage(compacted.Bytes()), parsed: parsed}

	strict := json.NewDecoder(bytes.NewReader(trimmed))
	strict.DisallowUnknownFields()
	var probe cliRuntimeSettingsOverride
	if err := strict.Decode(&probe); err != nil {
		if match := cliUnknownJSONFieldPattern.FindStringSubmatch(err.Error()); len(match) == 2 {
			overrides.unknownField = match[1]
		}
	}

	for _, field := range parsed.OverrideFields {
		if strings.TrimSpace(field) == "" {
			return nil, fmt.Errorf("override_fields cannot contain empty field paths")
		}
	}
	// The controller owns the APEX_RUNTIME_OVERRIDE_* secret names it gives
	// credentials typed into runtime settings; naming one would point this
	// run at another run's credential. Both the decoded settings (what the
	// controller inspects) and the JSON as written are checked.
	reEncoded, err := json.Marshal(parsed)
	if err != nil {
		return nil, fmt.Errorf("inspect runtime secret references")
	}
	if reservedRuntimeOverrideSecretReference(reEncoded) || reservedRuntimeOverrideSecretReference(trimmed) {
		return nil, fmt.Errorf("runtime overrides cannot name reserved internal secret references; use a configured project secret with a non-reserved name")
	}
	credentials, err := runtimeOverrideCredentials(&parsed)
	if err != nil {
		return nil, err
	}
	overrides.credentials = credentials
	return overrides, nil
}

func reservedRuntimeOverrideSecretReference(payload []byte) bool {
	for _, match := range cliRuntimeOverrideSecretReferencePattern.FindAllSubmatch(payload, -1) {
		if len(match) < 2 {
			continue
		}
		name, err := engine.NormalizeLogicalSecretName(string(match[1]))
		if err == nil && strings.HasPrefix(name, runtimeOverrideReservedSecretPrefix) {
			return true
		}
	}
	return false
}

// runtimeOverrideCredentials lists the credential values in the overrides,
// at the paths and by the sensitivity rules the controller uses to turn them
// into platform secrets for a new run (protectRuntimeOverrideSecretsWithOptions
// with sensitiveEnvKey, sensitiveEnvValue and maskURLSecrets in pkg/api). Each
// value is listed whole and by its secret parts (an Authorization token, a
// userinfo password, a signature or credential query value), so an echo of
// either is scrubbed. Every credential in the JSON is listed, also one outside
// override_fields the controller drops, since the JSON still reaches it.
//
// A run apexctl creates has no earlier credential for a [REDACTED] marker to
// keep, so the controller refuses one at a path it keeps; it is refused here
// first, before the script is uploaded. An exact {{secret.NAME}} reference
// names a secret and is not one.
func runtimeOverrideCredentials(o *cliRuntimeSettingsOverride) ([]string, error) {
	coverage := newCLIRuntimeOverrideCoverage(o)
	var values []string
	add := func(path, value string, sensitive bool) error {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || !sensitive {
			return nil
		}
		if strings.EqualFold(trimmed, scrub.Redacted) {
			if !coverage.keeps(path) {
				return nil
			}
			return fmt.Errorf("runtime override %s is %s; a new run has no saved credential to keep, so enter the credential or leave it out", path, scrub.Redacted)
		}
		if cliRuntimeOverrideExactSecretPattern.MatchString(trimmed) {
			return nil
		}
		values = append(values, value)
		values = append(values, cliCredentialParts(trimmed)...)
		return nil
	}
	type entry struct {
		path      string
		value     string
		sensitive bool
	}
	var entries []entry
	for key, value := range o.Env {
		entries = append(entries, entry{"env." + key, value, cliSensitiveEnvKey(key) || cliSensitiveValue(value)})
	}
	entries = append(entries, entry{"base_url", o.BaseURL, cliSensitiveURL(o.BaseURL)})
	if o.HTTP != nil {
		h := o.HTTP
		authActive := h.Auth.Type != "" || h.Auth.Password != "" || h.Auth.Token != ""
		entries = append(entries,
			entry{"http.auth.username", h.Auth.Username, authActive},
			entry{"http.auth.password", h.Auth.Password, true},
			entry{"http.auth.token", h.Auth.Token, true},
			entry{"http.proxy.url", h.Proxy.URL, cliSensitiveURL(h.Proxy.URL)},
			entry{"http.proxy.username", h.Proxy.Username, true},
			entry{"http.proxy.password", h.Proxy.Password, true},
		)
		policy := scrub.Policy{}
		for name, value := range h.DefaultHeaders {
			entries = append(entries, entry{"http.default_headers." + name, value, policy.SensitiveHeader(name) || cliSensitiveValue(value)})
		}
		for index, header := range h.DefaultHeaderList {
			entries = append(entries, entry{fmt.Sprintf("http.default_header_list.%d.%s", index, header.Name), header.Value, policy.SensitiveHeader(header.Name) || cliSensitiveValue(header.Value)})
		}
	}
	if o.SOAP != nil && o.SOAP.WSSecurity != nil && o.SOAP.WSSecurity.UsernameToken != nil {
		token := o.SOAP.WSSecurity.UsernameToken
		authActive := token.PasswordType != "" || token.Password != ""
		entries = append(entries,
			entry{"soap.ws_security.username_token.username", token.Username, authActive || cliSensitiveValue(token.Username)},
			entry{"soap.ws_security.username_token.password", token.Password, true},
		)
	}
	if o.BrowserConfig != nil {
		b := o.BrowserConfig
		credentialsActive := b.HTTPCredentials.Username != "" || b.HTTPCredentials.Password != ""
		entries = append(entries,
			entry{"browser.http_credentials.username", b.HTTPCredentials.Username, credentialsActive},
			entry{"browser.http_credentials.password", b.HTTPCredentials.Password, true},
			entry{"browser.proxy.server", b.Proxy.Server, cliSensitiveURL(b.Proxy.Server)},
			entry{"browser.proxy.username", b.Proxy.Username, true},
			entry{"browser.proxy.password", b.Proxy.Password, true},
		)
		for index, certificate := range b.ClientCertificates {
			entries = append(entries, entry{fmt.Sprintf("browser.client_certificates.%d.passphrase", index), certificate.Passphrase, true})
		}
	}
	for _, item := range entries {
		if err := add(item.path, item.value, item.sensitive); err != nil {
			return nil, err
		}
	}
	return values, nil
}

// cliRuntimeOverrideCoverage answers which paths of the overrides the
// controller keeps. Without override_fields every section given replaces the
// script's whole section (v1), so every path is kept. With override_fields
// the controller keeps only the listed fields (mergeRuntimeSettingsPartial),
// plus the whole env map when env is given and no env field is listed
// (resolveEffectiveOverrideFields).
type cliRuntimeOverrideCoverage struct {
	partial bool
	fields  []string
}

func newCLIRuntimeOverrideCoverage(o *cliRuntimeSettingsOverride) cliRuntimeOverrideCoverage {
	if o == nil || len(o.OverrideFields) == 0 {
		return cliRuntimeOverrideCoverage{}
	}
	coverage := cliRuntimeOverrideCoverage{partial: true}
	hasEnv := false
	for _, raw := range o.OverrideFields {
		field := cliCanonicalOverrideField(raw)
		if field == "env" || strings.HasPrefix(field, "env.") {
			hasEnv = true
		}
		coverage.fields = append(coverage.fields, field)
	}
	if len(o.Env) > 0 && !hasEnv {
		coverage.fields = append(coverage.fields, "env")
	}
	return coverage
}

// keeps reports whether the controller keeps the value at path, a canonical
// override path such as env.KEY, base_url or http.auth.password.
func (c cliRuntimeOverrideCoverage) keeps(path string) bool {
	if !c.partial {
		return true
	}
	for _, field := range c.fields {
		if path == field || strings.HasPrefix(path, field+".") {
			return true
		}
	}
	return false
}

// lists reports whether override_fields names path itself (canonically),
// so the controller keeps it even when the overrides give it no value.
func (c cliRuntimeOverrideCoverage) lists(path string) bool {
	return c.partial && slices.Contains(c.fields, path)
}

// cliCanonicalOverrideField is the controller's normalizeOneRuntimeOverrideField
// for the paths the CLI checks: a config. prefix is dropped, the compact
// aliases map to their section paths, struct segments match case-insensitively
// (JSON tags are lower case), and env keys and default header names keep their
// case. A field the controller would refuse is returned as written; the
// controller reports it.
func cliCanonicalOverrideField(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(trimmed), "config.") {
		trimmed = strings.TrimSpace(trimmed[len("config."):])
	}
	lower := strings.ToLower(trimmed)
	switch lower {
	case "iterations":
		return "run_logic.iterations"
	case "log_level":
		return "log.level"
	case "auto_transactions":
		return "run_logic.auto_transactions"
	case "browser_config":
		return "browser"
	}
	if strings.HasPrefix(lower, "browser_config.") {
		trimmed = "browser." + trimmed[len("browser_config."):]
		lower = strings.ToLower(trimmed)
	}
	if strings.HasPrefix(lower, "env.") {
		return "env." + strings.TrimSpace(trimmed[len("env."):])
	}
	segments := strings.Split(trimmed, ".")
	for index := range segments {
		segments[index] = strings.ToLower(strings.TrimSpace(segments[index]))
		if index == 1 && segments[0] == "http" && segments[1] == "default_headers" && len(segments) > 2 {
			return "http.default_headers." + strings.TrimSpace(strings.Join(strings.Split(trimmed, ".")[2:], "."))
		}
	}
	return strings.Join(segments, ".")
}

// cliLegacyDSNEnvMarkers are the database, cache and broker DSN names the
// controller treats as credentials whatever their value (legacySensitiveEnvKey).
var cliLegacyDSNEnvMarkers = []string{
	"database_url", "db_url", "jdbc_url", "redis_url", "cache_url",
	"broker_url", "amqp_url", "dsn", "connection_string",
}

// cliSensitiveEnvKey mirrors the controller's sensitiveEnvKey (including
// legacySensitiveEnvKey's DSN class).
func cliSensitiveEnvKey(key string) bool {
	if scrub.SensitiveName(key) || credential.SensitiveEnvName(key) {
		return true
	}
	normalized := strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(strings.ToLower(strings.TrimSpace(key)))
	for _, marker := range cliLegacyDSNEnvMarkers {
		if normalized == marker || strings.HasSuffix(normalized, "_"+marker) {
			return true
		}
	}
	return false
}

// cliSensitiveValue mirrors the controller's sensitiveEnvValue, which it
// applies to env values, default header values and a SOAP username.
func cliSensitiveValue(value string) bool {
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
	if cliHasURLUserinfo(trimmed) {
		return true
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return false
	}
	for key, values := range parsed.Query() {
		if !cliSensitiveQueryKey(key) {
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

// cliSensitiveURL mirrors the controller's maskURLSecrets(raw) != raw for
// base_url and proxy URLs: userinfo (also scheme-less user:password@host) or
// a credential-shaped query or fragment value.
func cliSensitiveURL(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	if cliHasURLUserinfo(trimmed) {
		return true
	}
	return scrub.ValueURL(raw, scrub.Policy{}) != raw
}

// cliHasURLUserinfo is the controller's maskURLUserinfo(raw) != raw.
func cliHasURLUserinfo(trimmed string) bool {
	parsed, err := url.Parse(trimmed)
	if err == nil && parsed.User != nil {
		return true
	}
	return cliHasSchemeLessURLUserinfo(trimmed)
}

// cliHasSchemeLessURLUserinfo mirrors the controller's
// hasSchemeLessURLUserinfo: the credential shape user:password@host, which
// net/url parses as a path.
func cliHasSchemeLessURLUserinfo(raw string) bool {
	at := strings.LastIndex(raw, "@")
	if at <= 0 || at == len(raw)-1 {
		return false
	}
	colon := strings.Index(raw[:at], ":")
	if colon <= 0 || colon >= at-1 {
		return false
	}
	switch strings.ToLower(raw[:colon]) {
	case "mailto", "tel", "urn", "data":
		return false
	}
	if strings.HasPrefix(raw[colon+1:], "//") {
		return false
	}
	if strings.ContainsAny(raw[:at], "/?# \t\r\n") {
		return false
	}
	parsed, err := url.Parse("//" + raw)
	return err == nil && parsed.User != nil && parsed.User.Username() != "" && parsed.Hostname() != ""
}

func cliSensitiveQueryKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	return scrub.SensitiveName(key) || normalized == "sig" || strings.HasSuffix(normalized, "signature")
}

// cliCredentialParts lists the secret parts of a credential value that can be
// printed on their own: an Authorization-style token, a userinfo password,
// credential query values, and the key values of a connection string.
func cliCredentialParts(trimmed string) []string {
	var parts []string
	addPart := func(part string) {
		if part = strings.TrimSpace(part); part != "" && part != trimmed {
			parts = append(parts, part)
		}
	}
	if scheme, token, ok := strings.Cut(trimmed, " "); ok && cliAuthorizationScheme(scheme) {
		addPart(token)
	}
	userinfoURL, err := url.Parse(trimmed)
	if (err != nil || userinfoURL.User == nil) && cliHasSchemeLessURLUserinfo(trimmed) {
		userinfoURL, err = url.Parse("//" + trimmed)
	}
	if err == nil && userinfoURL.User != nil {
		if password, ok := userinfoURL.User.Password(); ok {
			addPart(password)
			if escaped := userinfoURL.User.String(); strings.Contains(escaped, ":") {
				_, rawPassword, _ := strings.Cut(escaped, ":")
				addPart(rawPassword)
			}
		}
	}
	if parsed, err := url.Parse(trimmed); err == nil {
		for _, rawQuery := range []string{parsed.RawQuery, parsed.Fragment} {
			for _, pair := range strings.Split(rawQuery, "&") {
				key, value, ok := strings.Cut(pair, "=")
				if !ok || !cliSensitiveQueryKey(key) {
					continue
				}
				addPart(value)
				if decoded, err := url.QueryUnescape(value); err == nil {
					addPart(decoded)
				}
			}
		}
	}
	// Connection strings: Key=value pairs separated by ';' (or '&').
	for _, pair := range strings.FieldsFunc(trimmed, func(r rune) bool { return r == ';' || r == '&' }) {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		normalized := strings.ToLower(strings.TrimSpace(key))
		if normalized == "sharedaccesskey" || normalized == "accountkey" || normalized == "sig" || scrub.SensitiveName(key) {
			addPart(value)
		}
	}
	return parts
}

func cliAuthorizationScheme(scheme string) bool {
	switch strings.ToLower(scheme) {
	case "bearer", "basic", "apikey":
		return true
	}
	return false
}

// validateRuntimeOverridesWithFlags checks the overrides against the other
// run flags. Runtime overrides are applied by the controller to the run it
// creates, so they need --api. --env and --base-url keep their meaning (they
// are applied to the script before it is uploaded) and combine with the
// overrides; a key or base URL set to different values in both places is
// refused rather than one silently winning.
func validateRuntimeOverridesWithFlags(overrides *cliRuntimeOverrides, args runArgs) error {
	if overrides == nil {
		return nil
	}
	if strings.TrimSpace(args.api) == "" {
		return fmt.Errorf("--runtime-overrides requires --api: the controller applies runtime overrides to the run it creates; a local run takes --env and --base-url")
	}
	// Only what the controller keeps can conflict: with override_fields, an
	// unlisted base_url or env key is dropped (env given without any env
	// field is kept whole). A base_url or env.KEY listed by name is kept even
	// with no value: the controller copies the empty value into the run
	// (applyRuntimeOverridesPartial), so it conflicts with a flag that sets one.
	coverage := newCLIRuntimeOverrideCoverage(&overrides.parsed)
	baseURLKept := (overrides.parsed.BaseURL != "" && coverage.keeps("base_url")) || coverage.lists("base_url")
	if args.baseURL != "" && baseURLKept && strings.TrimSpace(args.baseURL) != strings.TrimSpace(overrides.parsed.BaseURL) {
		return fmt.Errorf("--base-url and base_url in the runtime overrides differ; set the base URL in one place")
	}
	for key, value := range args.env {
		override, ok := overrides.parsed.Env[key]
		if !ok && coverage.lists("env."+key) {
			override, ok = "", true
		}
		if ok && coverage.keeps("env."+key) && override != value {
			return fmt.Errorf("--env %s and env.%s in the runtime overrides differ; set it in one place", key, key)
		}
	}
	return nil
}

// runtimeOverrideServiceAccountWarning warns, when apexctl authenticates with
// an API key (a service account on the controller) and the overrides carry a
// credential literal, that the controller refuses to create platform secrets
// for a service account, so the create fails after the script upload.
// A bearer token can belong to a person, so it is not warned about.
func runtimeOverrideServiceAccountWarning(args runArgs) string {
	if args.runtimeOverrides == nil || len(args.runtimeOverrides.credentials) == 0 || strings.TrimSpace(args.apiKey) == "" {
		return ""
	}
	return "warning: the runtime overrides carry credentials, and an API key signs in as a service account; the controller refuses to make platform secrets from a service account's credentials, so reference project secrets with {{secret.NAME}} instead"
}

// withRuntimeOverrideCredentialRedaction keeps the overrides' credentials out
// of CLI output. They become platform secrets on the controller, so they are
// registered as platform-managed values, not script-sourced ones.
func withRuntimeOverrideCredentialRedaction(rc *engine.RedactionConfig, overrides *cliRuntimeOverrides) *engine.RedactionConfig {
	if overrides == nil || len(overrides.credentials) == 0 {
		return rc
	}
	if rc == nil {
		var err error
		if rc, err = engine.NewRedactionConfig(nil); err != nil {
			return nil
		}
	}
	if withCredentials, err := engine.WithSecretValueRedactionChecked(rc, overrides.credentials); err == nil {
		return withCredentials
	}
	return rc
}

// attachRuntimeOverridesToClient makes the client's create-test request
// carry the overrides as the run's runtime_settings.
func attachRuntimeOverridesToClient(client *ApiClient, overrides *cliRuntimeOverrides) error {
	if client == nil || overrides == nil {
		return nil
	}
	base, err := url.Parse(client.BaseURL)
	if err != nil {
		return fmt.Errorf("parse controller URL: %w", err)
	}
	httpClient := http.Client{}
	if client.Client != nil {
		httpClient = *client.Client
	}
	transport := httpClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	httpClient.Transport = &runtimeSettingsTransport{
		base:       transport,
		createPath: strings.TrimRight(base.Path, "/") + "/api/v1/tests",
		settings:   overrides.raw,
	}
	client.Client = &httpClient
	return nil
}

// runtimeSettingsTransport adds runtime_settings to the create-test request
// body (POST {controller}/api/v1/tests, including a same-origin 307/308
// replay of it). Every other request passes through unchanged.
type runtimeSettingsTransport struct {
	base       http.RoundTripper
	createPath string
	settings   json.RawMessage
	// refusedStatus is the status of a create-test response that refused the
	// request carrying the overrides (400 or 403), 0 otherwise.
	refusedStatus atomic.Int32
}

func (t *runtimeSettingsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.isCreateTest(req) {
		return t.base.RoundTrip(req)
	}
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("attach runtime overrides: read create-test request: %w", err)
		}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("attach runtime overrides: create-test request is not a JSON object")
	}
	if _, exists := fields["runtime_settings"]; exists {
		return nil, fmt.Errorf("attach runtime overrides: create-test request already has runtime_settings")
	}
	fields["runtime_settings"] = t.settings
	rewritten, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("attach runtime overrides: %w", err)
	}
	out := req.Clone(req.Context())
	out.Body = io.NopCloser(bytes.NewReader(rewritten))
	out.ContentLength = int64(len(rewritten))
	out.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(rewritten)), nil }
	resp, err := t.base.RoundTrip(out)
	if err == nil && resp != nil && (resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusForbidden) {
		t.refusedStatus.Store(int32(resp.StatusCode))
	}
	return resp, err
}

// classifyRuntimeOverrideCreateRefusal makes a run error from a controller
// that refused (400 or 403) the create-test request carrying the runtime
// overrides a configuration error (exit 2): the controller refuses what the
// operator asked for, such as an unknown override_fields path, a value mixing
// a literal with a secret reference, or a service account's typed credential,
// and retrying will not help. Other errors keep their classification.
func classifyRuntimeOverrideCreateRefusal(client *ApiClient, err error) error {
	if err == nil || client == nil || client.Client == nil {
		return err
	}
	transport, ok := client.Client.Transport.(*runtimeSettingsTransport)
	if !ok || transport.refusedStatus.Load() == 0 {
		return err
	}
	var configurationErr *cliConfigurationError
	if errors.As(err, &configurationErr) {
		return err
	}
	return &cliConfigurationError{cause: err}
}

func (t *runtimeSettingsTransport) isCreateTest(req *http.Request) bool {
	if req == nil || req.Method != http.MethodPost {
		return false
	}
	for current := req; current != nil; {
		if current.Method == http.MethodPost && current.URL != nil && strings.TrimRight(current.URL.Path, "/") == t.createPath {
			return true
		}
		if current.Response == nil {
			return false
		}
		current = current.Response.Request
	}
	return false
}
