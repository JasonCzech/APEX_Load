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

// extendRuntimeRedactionForResolvedAction installs retained import credentials
// after template interpolation but before protocol execution.
// The protocol.Action remains untouched: these values are memory-only matcher
// inputs, not substitutions in the runnable request.
func (vu *VUser) extendRuntimeRedactionForResolvedAction(action *protocol.Action) error {
	values, err := resolvedActionCredentialValues(vu.Script, action)
	if err != nil {
		vu.takeRuntimeCredentialActionTemplates()
		return fmt.Errorf("prepare action credential redaction: %s", scrubSensitiveText(err.Error()))
	}
	values = append(values, vu.takeRuntimeCredentialActionTemplates()...)
	// The resolved action is a replaceable scope. Keeping every prior action's
	// credential forever makes a long test eventually fail closed solely due to
	// matcher capacity, even though those values can no longer reach transport.
	if err := vu.replaceRuntimeCredentialScope(runtimeCredentialActionScope, values); err != nil {
		return fmt.Errorf("prepare action credential redaction: %s", err)
	}
	if len(values) == 0 {
		return nil
	}
	scrubResolvedActionRedactedProjection(action, vu.RedactionConfig)
	return nil
}

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
		// Basic credentials have always been registered immediately before
		// transport, including for hand-authored scripts. Preserve that contract
		// independently of import provenance.
		if action.Auth != nil && strings.EqualFold(strings.TrimSpace(action.Auth.Type), "basic") {
			collector.collectResolvedAuth(action.Auth)
		}
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

func scrubResolvedActionRedactedProjection(action *protocol.Action, config *RedactionConfig) {
	if action == nil || config == nil {
		return
	}
	action.RedactedTarget = scrubResolvedProjectionString(action.RedactedTarget, action.Target, config)
	action.RedactedValue = scrubResolvedProjectionString(action.RedactedValue, action.Value, config)
	action.RedactedHeaders = scrubResolvedProjectionStringMap(action.RedactedHeaders, action.Headers, config)
	action.RedactedHeaderList = scrubResolvedProjectionHeaderList(
		action.RedactedHeaderList,
		action.HeaderList,
		config,
	)
	action.RedactedBody = scrubResolvedProjectionBody(action, config)
	action.RedactedParams = scrubResolvedProjectionAnyMap(action.RedactedParams, action.Params, config)
	action.RedactedAuth = scrubResolvedProjectionAuth(action.RedactedAuth, action.Auth, config)
	action.RedactedSOAP = scrubResolvedProjectionSOAP(action.RedactedSOAP, action.SOAP, config)
}

func scrubResolvedProjectionString(redacted, raw string, config *RedactionConfig) string {
	if redacted == "" && raw != "" {
		redacted = raw
	}
	return scrubSensitiveTextWithConfig(redacted, config)
}

func scrubResolvedProjectionStringMap(
	redacted map[string]string,
	raw map[string]string,
	config *RedactionConfig,
) map[string]string {
	source := redacted
	if source == nil {
		source = raw
	}
	if source == nil {
		return nil
	}
	out := make(map[string]string, len(source))
	for name, value := range source {
		out[scrubSensitiveTextWithConfig(name, config)] = scrubSensitiveTextWithConfig(value, config)
	}
	return out
}

func scrubResolvedProjectionHeaderList(
	redacted protocol.HeaderList,
	raw protocol.HeaderList,
	config *RedactionConfig,
) protocol.HeaderList {
	source := redacted
	if source == nil {
		source = raw
	}
	if source == nil {
		return nil
	}
	out := make(protocol.HeaderList, len(source))
	for index, header := range source {
		out[index] = protocol.HeaderEntry{
			Name:    scrubSensitiveTextWithConfig(header.Name, config),
			Value:   scrubSensitiveTextWithConfig(header.Value, config),
			Enabled: header.Enabled,
		}
	}
	return out
}

func scrubResolvedProjectionBody(action *protocol.Action, config *RedactionConfig) []byte {
	source := action.RedactedBody
	if source == nil {
		source = action.Body
	}
	if source == nil {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(action.BodyEncoding), "base64") {
		decoded, err := base64.StdEncoding.DecodeString(string(source))
		if err == nil {
			scrubbed := scrubSensitiveTextWithConfig(string(decoded), config)
			return []byte(base64.StdEncoding.EncodeToString([]byte(scrubbed)))
		}
	}
	return []byte(scrubSensitiveTextWithConfig(string(source), config))
}

func scrubResolvedProjectionAnyMap(
	redacted map[string]any,
	raw map[string]any,
	config *RedactionConfig,
) map[string]any {
	source := redacted
	if source == nil {
		source = raw
	}
	if source == nil {
		return nil
	}
	out := make(map[string]any, len(source))
	for name, value := range source {
		out[scrubSensitiveTextWithConfig(name, config)] = scrubResolvedProjectionAny(value, config)
	}
	return out
}

func scrubResolvedProjectionAny(value any, config *RedactionConfig) any {
	switch typed := value.(type) {
	case string:
		return scrubSensitiveTextWithConfig(typed, config)
	case []string:
		out := make([]string, len(typed))
		for index, item := range typed {
			out[index] = scrubSensitiveTextWithConfig(item, config)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = scrubResolvedProjectionAny(item, config)
		}
		return out
	case map[string]string:
		return scrubResolvedProjectionStringMap(typed, nil, config)
	case map[string]any:
		return scrubResolvedProjectionAnyMap(typed, nil, config)
	default:
		return typed
	}
}

func scrubResolvedProjectionAuth(
	redacted *protocol.AuthConfig,
	raw *protocol.AuthConfig,
	config *RedactionConfig,
) *protocol.AuthConfig {
	source := redacted
	if source == nil {
		source = raw
	}
	if source == nil {
		return nil
	}
	return &protocol.AuthConfig{
		Type:     source.Type,
		Username: scrubSensitiveTextWithConfig(source.Username, config),
		Password: scrubSensitiveTextWithConfig(source.Password, config),
		Token:    scrubSensitiveTextWithConfig(source.Token, config),
		Domain:   scrubSensitiveTextWithConfig(source.Domain, config),
	}
}

func scrubResolvedProjectionSOAP(
	redacted *protocol.SOAPActionConfig,
	raw *protocol.SOAPActionConfig,
	config *RedactionConfig,
) *protocol.SOAPActionConfig {
	source := redacted
	if source == nil {
		source = raw
	}
	if source == nil {
		return nil
	}
	return &protocol.SOAPActionConfig{
		Action:    scrubSensitiveTextWithConfig(source.Action, config),
		Operation: scrubSensitiveTextWithConfig(source.Operation, config),
		BodyMode:  scrubSensitiveTextWithConfig(source.BodyMode, config),
	}
}
