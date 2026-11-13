package engine

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/bodyencoding"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// extendRuntimeRedactionForResolvedAction validates the retained credential
// locators of a resolved action after template interpolation but before protocol
// execution, and installs that action's two credential scopes.
//
// runtimeCredentialActionScope (platform-managed, never gated) carries the
// composite spellings of a per-action credential surface whose AUTHORED field
// defers to the project secret store. Those surfaces are enumerated by
// actionCredentialCompositeSurfaces (pkg/engine/platform_managed_credential_composites.go)
// — the auth block and the target's URL userinfo — and they share their origin
// test and derivation with the script-config surfaces. Their composites embed a
// value the operator does not otherwise hold, and the exact-value matcher cannot
// derive base64("user:password") from the bare password registered by
// snapshotReferencedSecrets. platformDerivedActionComposites adds the same
// spellings when a RESOLVED half carries a platform value the authored field
// does not name (a variable assigned from {{secret.*}}).
//
// runtimeCredentialScriptActionScope (script-sourced, gated) carries what
// resolvedActionCredentialValues walked out of the resolved action. Those returns
// are a MIXTURE of origins: values retained from an import and credentials
// written into the script alongside anything a {{secret.NAME}} reference resolved
// to. The mixture is safe to file as script-sourced because the funnel drops any
// value the base matcher already covers, and the platform-managed part is exactly
// what the base matcher holds — bare values from snapshotReferencedSecrets,
// composites from the surface registration above. Whatever survives that filter
// is script-sourced material the operator reads in their own script.
//
// Both scopes are replaced wholesale, so nothing a previous action installed can
// leak into this one.
//
// The locator walk runs regardless of the setting because a stale locator still
// signals that an imported request no longer matches its recorded shape, which is
// a correctness failure independent of redaction.
func (vu *VUser) extendRuntimeRedactionForResolvedAction(action *protocol.Action) error {
	scriptSourced, err := resolvedActionCredentialValues(vu.Script, action)
	if err != nil {
		return fmt.Errorf("prepare action credential redaction: %s", scrubSensitiveText(err.Error()))
	}
	if err := vu.replaceRuntimeCredentialScopes(map[string][]string{
		runtimeCredentialActionScope:       vu.platformDerivedActionComposites(action, vu.platformManagedActionCredentialValues(action)),
		runtimeCredentialScriptActionScope: append(scriptSourced, vu.scriptSourcedActionBodySpellings(action)...),
	}); err != nil {
		return fmt.Errorf("prepare action credential redaction: %s", err)
	}
	return nil
}

// platformManagedActionCredentialValues returns the composite spellings
// resolveAction recorded for this action. It answers nil for any action that is
// not the one the most recent resolveAction produced, so a stale registration
// can never be applied to a different action.
func (vu *VUser) platformManagedActionCredentialValues(action *protocol.Action) []string {
	if vu == nil || action == nil || vu.actionCredentialComposites.action != action {
		return nil
	}
	return vu.actionCredentialComposites.values
}

// scriptSourcedActionBodySpellings returns the transformed-body spellings of
// script-sourced values resolveAction recorded for this action (only with
// config.log.redact_run_evidence on). Like platformManagedActionCredentialValues
// it answers nil for any other action.
func (vu *VUser) scriptSourcedActionBodySpellings(action *protocol.Action) []string {
	if vu == nil || action == nil || vu.actionCredentialComposites.action != action {
		return nil
	}
	return vu.actionCredentialComposites.scriptValues
}

// platformDerivedActionComposites extends an action's platform-managed
// composites with the wire spellings built from RESOLVED credential halves that
// carry a platform value. A half can carry a secret-store value without naming
// {{secret.*}} — a variable assigned from one — so the origin test on the
// authored field registers no composite for it, and base64(user:secret) would
// reach run evidence (and the unredacted debugger) raw. The halves tested are
// the typed auth block, the target's URL userinfo (net/http synthesizes the
// Basic header from it) and the network-auth params (SASL). A half is tested
// against platform values only (platformSecretCarrier), so a script literal
// keeps following config.log.redact_run_evidence and stays visible in the
// debugger. This runs in every mode.
func (vu *VUser) platformDerivedActionComposites(action *protocol.Action, values []string) []string {
	if vu == nil || action == nil {
		return values
	}
	var carries func(string) bool
	carrier := func() func(string) bool {
		if carries == nil {
			carries = vu.platformSecretCarrier()
		}
		return carries
	}
	anyCarries := func(halves ...string) bool {
		for _, half := range halves {
			if half != "" && carrier()(half) {
				return true
			}
		}
		return false
	}
	var derived []string
	if auth := action.Auth; auth != nil && (auth.Username != "" || auth.Password != "") && anyCarries(auth.Username, auth.Password, auth.Domain) {
		derived = appendTypedAuthComposites(derived, auth)
		if len(derived) == 0 {
			// Any other scheme that sends the pair still gets its Basic forms.
			derived = appendBasicPairComposites(derived, auth.Username, auth.Password)
		}
	}
	if parsed, err := url.Parse(strings.TrimSpace(action.Target)); err == nil && parsed.User != nil {
		if password, ok := parsed.User.Password(); ok && anyCarries(parsed.User.Username(), password) {
			derived = appendBasicPairComposites(derived, parsed.User.Username(), password)
		}
	}
	paramUser, hasUser := resolvedActionParamString(action, "username")
	paramPass, hasPass := resolvedActionParamString(action, "password")
	if (hasUser || hasPass) && anyCarries(paramUser, paramPass) {
		derived = appendMailSASLComposites(derived, paramUser, paramPass)
	}
	derived = vu.appendPlatformDerivedBodySpellings(derived, action, carrier)
	if len(derived) == 0 {
		return values
	}
	return append(append(make([]string, 0, len(values)+len(derived)), values...), derived...)
}

// platformSecretCarrier reports whether a resolved value contains a platform
// value. The unredacted debugger tests against its mask, which holds platform
// values only. Every other mode tests against the run's matcher minus the
// script-sourced values it holds, so a script literal never counts.
//
// It runs for every authenticated action execution, so the answer is memoized
// per matcher publication (platformCarrierCache): a value is first tested with
// the precompiled matcher, which answers "no registered value at all" without
// touching the value set, and only a value that matches is checked against the
// platform-only set, built once per publication.
func (vu *VUser) platformSecretCarrier() func(string) bool {
	cache := vu.platformCarrierState()
	return cache.carries
}

// platformSecretRuns returns the spans of value that the platform matcher
// matches (adjacent matches merge into one span), for deriving the transformed
// spellings a body encoding gives them. Spans of script-sourced values only are
// left out.
func (vu *VUser) platformSecretRuns(value string) []string {
	cache := vu.platformCarrierState()
	matcher := cache.config
	if cache.unredacted {
		matcher = cache.mask
	}
	var runs []string
	for _, run := range runtimeSecretMatchedRuns(value, matcher) {
		if cache.carries(run) {
			runs = append(runs, run)
		}
	}
	return runs
}

// platformCarrierCache is the memoized state behind platformSecretCarrier. It
// is valid for one matcher publication: the run matcher pointer, the dynamic
// overlay generation, the debugger mask and the debug mode it was built for.
type platformCarrierCache struct {
	config     *RedactionConfig
	generation uint64
	mask       *RedactionConfig
	unredacted bool
	// platform is the run matcher's value set minus its script-sourced
	// values, built on the first value the matcher matches.
	platform map[string]struct{}
	results  map[string]bool
}

// maxPlatformCarrierResults bounds the memoized answers; the map is reset when
// it fills, so data-driven halves cannot grow it without bound.
const maxPlatformCarrierResults = 1024

// maxPlatformCarrierMemoBytes bounds the values the memo keeps: credential
// halves are short, and a request body must never be copied into it.
const maxPlatformCarrierMemoBytes = 4 << 10

func (vu *VUser) platformCarrierState() *platformCarrierCache {
	config := vu.RedactionConfig
	generation := config.dynamicGeneration()
	if cache := vu.platformCarrier; cache != nil && cache.config == config && cache.generation == generation &&
		cache.mask == vu.debugSecretMask && cache.unredacted == vu.unredactedDebug {
		return cache
	}
	cache := &platformCarrierCache{config: config, generation: generation, mask: vu.debugSecretMask, unredacted: vu.unredactedDebug}
	vu.platformCarrier = cache
	return cache
}

func (cache *platformCarrierCache) carries(value string) bool {
	if value == "" {
		return false
	}
	if len(value) > maxPlatformCarrierMemoBytes {
		return cache.compute(value)
	}
	if answer, ok := cache.results[value]; ok {
		return answer
	}
	answer := cache.compute(value)
	if cache.results == nil || len(cache.results) >= maxPlatformCarrierResults {
		cache.results = make(map[string]bool)
	}
	cache.results[value] = answer
	return answer
}

func (cache *platformCarrierCache) compute(value string) bool {
	if cache.unredacted {
		return cache.mask != nil && scrubRuntimeSecretText(value, cache.mask) != value
	}
	// The matcher holds every raw value (and more spellings), so a value it
	// leaves unchanged contains no platform value.
	if scrubRuntimeSecretText(value, cache.config) == value {
		return false
	}
	if cache.platform == nil {
		cache.platform = allRuntimeSecretValues(cache.config)
		for scriptSourced := range allRuntimeScriptSourcedValues(cache.config) {
			delete(cache.platform, scriptSourced)
		}
	}
	for secret := range cache.platform {
		if secret != "" && strings.Contains(value, secret) {
			return true
		}
	}
	return false
}

// appendPlatformDerivedBodySpellings derives the wire spellings a body
// encoding gives the platform values in a resolved body. The exact matcher
// holds a platform value and its text encodings, never the bytes a transformed
// body sends:
//
//   - base64: a body whose encoded text carries a platform value (the trusted
//     full-body template {{secret.BASE64_PAYLOAD}}, or a variable assigned from
//     one) is decoded on the wire, and the decoded text is wholly derived from
//     that value whatever keys it holds;
//   - utf-16le: each platform span is sent as UTF-16LE code units.
//
// These spellings are filed with the action's platform-managed composites, so
// the run matcher and the debugger mask cover them in every mode.
//
// A body whose authored text has no template that can insert a runtime value
// (resolveAction records this per action) is skipped: it holds no platform
// value the authored-surface registration does not cover, and the scan would
// otherwise run over every request body of a load test.
func (vu *VUser) appendPlatformDerivedBodySpellings(derived []string, action *protocol.Action, carrier func() func(string) bool) []string {
	if action == nil || len(action.Body) == 0 {
		return derived
	}
	if vu.actionCredentialComposites.action == action && !vu.actionCredentialComposites.bodyMayCarryPlatformValue {
		return derived
	}
	switch {
	case strings.EqualFold(strings.TrimSpace(action.BodyEncoding), "base64"):
		encoded := strings.TrimSpace(string(action.Body))
		if !carrier()(encoded) {
			return derived
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err == nil && len(decoded) > 0 && utf8.Valid(decoded) {
			derived = append(derived, string(decoded))
		}
	case bodyencoding.IsUTF16LE(action.BodyEncoding):
		body := string(action.Body)
		if !carrier()(body) {
			return derived
		}
		for _, run := range vu.platformSecretRuns(body) {
			if encoded, err := bodyencoding.EncodeUTF16LE(run); err == nil && len(encoded) > 0 {
				derived = append(derived, string(encoded))
			}
		}
	}
	return derived
}

// resolvedActionCredentialValues walks the credential surface of a resolved
// action and reports any locator that no longer matches the executable request.
//
// It runs AFTER interpolation, so the values it returns carry no single origin:
// most are script-sourced, but any field that referenced {{secret.NAME}} yields
// the project-secret value it resolved to, and collectResolvedURL /
// collectResolvedAuth additionally derive "user:password" composites from it.
// extendRuntimeRedactionForResolvedAction therefore files them under the
// SCRIPT-SOURCED scope only: the platform-managed ones are already covered by the
// base matcher and by their own surface registration, and the funnel drops
// anything the base matcher covers before the script-sourced half is built.
// Callers that need the values for an explicit disclosure surface obtain them
// through ImportedCredentialValuesForDisclosure.
func resolvedActionCredentialValues(script *dsl.Script, action *protocol.Action) ([]string, error) {
	if action == nil {
		return nil, nil
	}
	collector := importedCredentialCollector{
		script:   script,
		resolved: true,
		seen:     make(map[string]struct{}),
	}

	targets, err := decodeImportedCredentialTargets(
		action.Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey],
	)
	if err != nil {
		targets = nil
	}
	collector.collectResolvedActionKnownCredentials(action)
	if len(action.Body) > 0 && actionCarriesExecutablePayload(action.Type, action.Method) {
		if body, mediaType, err := resolvedActionBody(action); err == nil {
			_ = collector.collectLegacyRequestBodyCredentials(
				body,
				mediaType,
				0,
				&legacyCredentialBodyBudget{},
			)
		}
	}
	for _, target := range targets {
		if err := collector.collectResolvedActionTarget(action, target); err != nil {
			if isUnsupportedCredentialLocatorFieldError(err) {
				return nil, err
			}
		}
	}
	return collector.values, nil
}

func (collector *importedCredentialCollector) collectResolvedActionKnownCredentials(action *protocol.Action) {
	if action == nil {
		return
	}
	collector.collectResolvedURL(action.Target)
	for _, header := range action.HeaderList {
		if collector.isSensitiveHeader(header.Name) {
			collector.collectHeaderCredential(header.Name, header.Value)
		}
	}
	for name, value := range action.Headers {
		if collector.isSensitiveHeader(name) {
			collector.collectHeaderCredential(name, value)
		}
	}
	for _, header := range action.DefaultHeaderList {
		if collector.isSensitiveHeader(header.Name) {
			collector.collectHeaderCredential(header.Name, header.Value)
		}
	}
	for name, value := range action.DefaultHeaders {
		if collector.isSensitiveHeader(name) {
			collector.collectHeaderCredential(name, value)
		}
	}
	collector.collectResolvedAuth(action.Auth)
	collector.collectSensitiveTree(action.Params, "params", false)
	paramUser, hasUser := resolvedActionParamString(action, "username")
	paramPass, hasPass := resolvedActionParamString(action, "password")
	if hasUser || hasPass {
		effectiveUser := paramUser
		effectivePass := paramPass
		if effectiveUser == "" && collector.script != nil && !templatePattern.MatchString(collector.script.Config.Network.Username) {
			effectiveUser = collector.script.Config.Network.Username
		}
		if effectivePass == "" && collector.script != nil && !templatePattern.MatchString(collector.script.Config.Network.Password) {
			effectivePass = collector.script.Config.Network.Password
		}
		for _, value := range appendMailSASLComposites(nil, effectiveUser, effectivePass) {
			collector.add(value)
		}
	}

	if !isSensitiveBrowserActionInput(action.Type, action.Name, action.Selector, action.Target, action.Metadata, action.Params) {
		return
	}
	collector.add(action.Value)
	switch values := action.Params["values"].(type) {
	case []string:
		for _, value := range values {
			collector.add(value)
		}
	case []any:
		for _, raw := range values {
			if value, ok := raw.(string); ok {
				collector.add(value)
			}
		}
	}
}

func (collector *importedCredentialCollector) collectResolvedActionTarget(
	action *protocol.Action,
	target importedCredentialTarget,
) error {
	field := strings.ToLower(strings.TrimSpace(target.Field))
	switch {
	case strings.Contains(field, "multipart.parts["):
		body, mediaType, err := resolvedActionBody(action)
		if err != nil {
			return err
		}
		return collector.collectBodyTarget(body, mediaType, target)
	case strings.Contains(field, "header"):
		name, value, ok := resolvedActionHeader(action, target.Index)
		if !ok {
			return errors.New("retained credential header locator no longer matches the executable request")
		}
		collector.collectHeaderCredential(name, value)
		return nil
	case strings.HasSuffix(field, "auth.password"):
		if action.Auth == nil {
			return errors.New("retained credential auth locator no longer matches the executable request")
		}
		return nil
	case strings.HasSuffix(field, "auth.token"):
		if action.Auth == nil {
			return errors.New("retained credential auth locator no longer matches the executable request")
		}
		return nil
	case strings.Contains(field, "params.token_url"):
		value, ok := resolvedActionParamString(action, "token_url")
		if !ok {
			return errors.New("retained credential OAuth URL locator no longer matches the executable request")
		}
		if err := collector.collectURLTarget(value, target); err != nil {
			return err
		}
		collector.collectResolvedURLUserinfo(value)
		return nil
	case strings.Contains(field, "target") || strings.Contains(field, ".url") ||
		strings.HasPrefix(field, "url."):
		if action.Target == "" {
			return errors.New("retained credential URL locator no longer matches the executable request")
		}
		if err := collector.collectURLTarget(action.Target, target); err != nil {
			return err
		}
		collector.collectResolvedURLUserinfo(action.Target)
		return nil
	case strings.Contains(field, "body") || strings.Contains(field, "json") ||
		strings.Contains(field, "xml") || strings.Contains(field, "multipart") ||
		strings.Contains(field, "graphql") || strings.Contains(field, "form"):
		body, mediaType, err := resolvedActionBody(action)
		if err != nil {
			return err
		}
		return collector.collectBodyTarget(body, mediaType, target)
	default:
		return errors.New("retained credential locator names an unsupported executable field")
	}
}

func resolvedActionHeader(action *protocol.Action, index int) (string, string, bool) {
	if action == nil || index < 0 || index >= len(action.HeaderList) {
		return "", "", false
	}
	header := action.HeaderList[index]
	return header.Name, header.Value, true
}

func resolvedActionParamString(action *protocol.Action, key string) (string, bool) {
	if action == nil || action.Params == nil {
		return "", false
	}
	value, ok := action.Params[key].(string)
	return value, ok
}

func resolvedActionBody(action *protocol.Action) ([]byte, string, error) {
	if action == nil {
		return nil, "", errors.New("retained credential body locator no longer matches the executable request")
	}
	payload := action.Body
	if strings.EqualFold(strings.TrimSpace(action.BodyEncoding), "base64") {
		decoded, err := base64.StdEncoding.DecodeString(string(action.Body))
		if err != nil {
			return nil, "", errors.New("retained credential body encoding is invalid")
		}
		payload = decoded
	}
	return payload, resolvedActionContentType(action), nil
}

func resolvedActionContentType(action *protocol.Action) string {
	if action == nil {
		return ""
	}
	for _, header := range action.HeaderList {
		if strings.EqualFold(header.Name, "Content-Type") {
			return header.Value
		}
	}
	for name, value := range action.Headers {
		if strings.EqualFold(name, "Content-Type") {
			return value
		}
	}
	return ""
}

func (collector *importedCredentialCollector) collectHeaderCredential(name, value string) {
	if collector.shownRecordedState(name) {
		return
	}
	// A header is a credential by name, so a value too short to tell apart
	// from ordinary text is not matched (see credential.MatchableNamedValue).
	if credential.MatchableNamedValue(name, value) {
		collector.add(value)
	}
	collector.collectCookieHeaderValues(name, value)
	if !strings.EqualFold(strings.TrimSpace(name), "Authorization") &&
		!strings.EqualFold(strings.TrimSpace(name), "Proxy-Authorization") {
		return
	}
	value = strings.TrimSpace(value)
	separator := strings.IndexFunc(value, func(character rune) bool {
		return character == ' ' || character == '\t'
	})
	if separator < 0 {
		return
	}
	scheme := value[:separator]
	payload := strings.TrimSpace(value[separator:])
	if credential.MatchableNamedValue(name, payload) {
		collector.add(payload)
	}
	if strings.EqualFold(scheme, "basic") {
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err == nil {
			pair := string(decoded)
			collector.add(pair)
			_, password, hasPassword := strings.Cut(pair, ":")
			if hasPassword {
				collector.add(password)
			}
		}
		return
	}
	collector.collectAuthorizationParameters(payload)
}

func (collector *importedCredentialCollector) collectAuthorizationParameters(payload string) {
	for _, parameter := range splitAuthorizationParameters(payload) {
		name, value, found := strings.Cut(parameter, "=")
		if !found {
			continue
		}
		name = strings.TrimSpace(name)
		if !credential.SensitiveName(name) &&
			!strings.EqualFold(name, "response") {
			continue
		}
		value = strings.TrimSpace(value)
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		}
		collector.addNamed(name, value)
	}
}

func splitAuthorizationParameters(value string) []string {
	var parameters []string
	start := 0
	quoted := false
	escaped := false
	for index, character := range value {
		switch {
		case escaped:
			escaped = false
		case character == '\\' && quoted:
			escaped = true
		case character == '"':
			quoted = !quoted
		case character == ',' && !quoted:
			parameters = append(parameters, value[start:index])
			start = index + 1
		}
	}
	parameters = append(parameters, value[start:])
	return parameters
}

func (collector *importedCredentialCollector) collectResolvedAuth(auth *protocol.AuthConfig) {
	if auth == nil {
		return
	}
	collector.add(auth.Password)
	collector.add(auth.Token)
	if auth.Username == "" && auth.Password == "" {
		return
	}
	pair := auth.Username + ":" + auth.Password
	collector.add(pair)
	if strings.EqualFold(strings.TrimSpace(auth.Type), "basic") {
		payload := base64.StdEncoding.EncodeToString([]byte(pair))
		collector.add(payload)
		collector.add("Basic " + payload)
	}
}

func resolvedURLQueryCredentialValues(script *dsl.Script, raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	collector := importedCredentialCollector{
		script:   script,
		resolved: true,
		seen:     make(map[string]struct{}),
	}
	// Collect sensitive query values (userinfo composites are handled separately
	// with authored-vs-secret-store origin classification).
	queryStart := strings.IndexByte(raw, '?')
	if queryStart < 0 {
		return nil
	}
	collector.collectURL("https://placeholder.invalid"+raw[queryStart:], nil)
	return collector.values
}

func (collector *importedCredentialCollector) collectResolvedURL(raw string) {
	collector.collectURL(raw, nil)
	collector.collectResolvedURLUserinfo(raw)
}

func (collector *importedCredentialCollector) collectResolvedURLUserinfo(raw string) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User == nil {
		return
	}
	collector.collectURLUserinfo(parsed.User)
	if password, ok := parsed.User.Password(); ok {
		pair := parsed.User.Username() + ":" + password
		payload := base64.StdEncoding.EncodeToString([]byte(pair))
		collector.add(payload)
		collector.add("Basic " + payload)
	}
}
