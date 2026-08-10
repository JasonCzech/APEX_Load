package engine

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
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
// snapshotReferencedSecrets.
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
		runtimeCredentialActionScope:       vu.platformManagedActionCredentialValues(action),
		runtimeCredentialScriptActionScope: scriptSourced,
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

	if !scriptPreservesImportedCredentialValues(script) {
		// Basic-auth user/password embedded in a script is script-sourced whether
		// the script was imported or hand-authored. It is collected for symmetry
		// with the imported path and then discarded by the caller.
		if action.Auth != nil && strings.EqualFold(strings.TrimSpace(action.Auth.Type), "basic") {
			collector.collectResolvedAuth(action.Auth)
		}
		// URL userinfo is the same Basic surface with no auth block: net/http
		// synthesises "Basic base64(user:password)" from https://user:pass@host
		// inside Client.send. Collecting only the auth block here left the two
		// spellings of one credential obeying different rules — an action-auth
		// literal was scrubbed from run output while an otherwise identical
		// URL-embedded literal was disclosed, at the DEFAULT setting.
		collector.collectResolvedURLUserinfo(action.Target)
		return collector.values, nil
	}

	targets, err := decodeImportedCredentialTargets(
		action.Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey],
	)
	if err != nil {
		return nil, err
	}
	collector.collectResolvedActionKnownCredentials(action)
	if len(targets) == 0 && strings.TrimSpace(action.Method) != "" && len(action.Body) > 0 {
		body, mediaType, err := resolvedActionBody(action)
		if err != nil {
			return nil, err
		}
		if err := collector.collectLegacyRequestBodyCredentials(
			body,
			mediaType,
			0,
			&legacyCredentialBodyBudget{},
		); err != nil {
			return nil, err
		}
	}
	for _, target := range targets {
		if err := collector.collectResolvedActionTarget(action, target); err != nil {
			return nil, err
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
		if credential.SensitiveHeader(header.Name) {
			collector.collectHeaderCredential(header.Name, header.Value)
		}
	}
	for name, value := range action.Headers {
		if credential.SensitiveHeader(name) {
			collector.collectHeaderCredential(name, value)
		}
	}
	collector.collectResolvedAuth(action.Auth)
	collector.collectSensitiveTree(action.Params, "params", false)

	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(action.Type)), "browser_") {
		return
	}
	marked, _ := action.Metadata["source_marked_secret"].(bool)
	masked, _ := action.Metadata["source_masked_secret"].(bool)
	if !marked && !masked {
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
	collector.add(value)
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
	collector.add(payload)
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
		collector.add(value)
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
}
