package api

// Script response projection and masked-write reconciliation live here rather
// than beside execution-time masking. Runtime scripts must retain credentials;
// viewer, export, federation, and source-control representations must not.

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

const maskedScriptSecret = scrub.Redacted

var scriptSecretTemplatePattern = regexp.MustCompile(`\{\{\s*secret\.(` + engine.LogicalSecretNameInputPattern + `)\s*\}\}`)
var scriptPortableTemplatePattern = regexp.MustCompile(`\{\{[^{}\r\n]{1,256}\}\}`)
var scriptPortableReferencePattern = regexp.MustCompile(`^(?:(?:env|data|var|param|file)\.[A-Za-z0-9_.:/@-]+|secret\.` + engine.LogicalSecretNameInputPattern + `)$`)
var scriptPortableRandomPattern = regexp.MustCompile(`^fn\.random\(\s*-?\d+\s*,\s*-?\d+\s*\)$`)
var scriptPortableRandomStringPattern = regexp.MustCompile(`^fn\.random_string\(\s*\d+\s*\)$`)

// maskScriptSecrets returns a deep, portable copy of script with inline
// credentials removed. It intentionally preserves explicit secret references
// (for example {{secret.api_token}} and *_secret reference fields) so an export
// describes how to bind a secret without disclosing its value.
func maskScriptSecrets(script *dsl.Script) *dsl.Script {
	if script == nil {
		return nil
	}
	payload, err := json.Marshal(script)
	if err != nil {
		return nil
	}
	var raw any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil
	}
	projected := projectScriptNode(raw, scriptProjectionContext{})
	projectedPayload, err := json.Marshal(projected)
	if err != nil {
		return nil
	}
	var masked dsl.Script
	if err := json.Unmarshal(projectedPayload, &masked); err != nil {
		return nil
	}
	return &masked
}

func maskStoredScript(stored *StoredScript) *StoredScript {
	if stored == nil {
		return nil
	}
	masked := cloneStoredScriptForReadLocked(stored)
	masked.Script = maskScriptSecrets(masked.Script)
	return masked
}

func maskStoredScripts(scripts []*StoredScript) []*StoredScript {
	if scripts == nil {
		return nil
	}
	masked := make([]*StoredScript, len(scripts))
	for i, script := range scripts {
		masked[i] = maskStoredScript(script)
	}
	return masked
}

func maskAuthoringSession(session *storage.AuthoringSession) *storage.AuthoringSession {
	if session == nil {
		return nil
	}
	masked := cloneAuthoringSession(session)
	masked.Draft = maskScriptSecrets(masked.Draft)
	masked.ReviewState = projectAuthoringReviewState(masked.ReviewState)
	return masked
}

func maskAuthoringSessions(sessions []*storage.AuthoringSession) []*storage.AuthoringSession {
	if sessions == nil {
		return nil
	}
	masked := make([]*storage.AuthoringSession, len(sessions))
	for i, session := range sessions {
		masked[i] = maskAuthoringSession(session)
	}
	return masked
}

// projectAuthoringReviewState makes the client-extensible review_state map a
// disclosure-safe authoring surface. It preserves ordinary workflow metadata
// while scrubbing credential-named fields and secret-shaped free text. The map
// is never executable, so retained {{secret.*}} descriptors are informational
// and do not become runtime requirements.
func projectAuthoringReviewState(state map[string]any) map[string]any {
	if len(state) == 0 {
		return nil
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil
	}
	projected, _ := projectScriptNode(raw, scriptProjectionContext{opaqueMap: true}).(map[string]any)
	return projected
}

type scriptProjectionContext struct {
	key            string
	opaqueMap      bool
	headers        bool
	headerList     bool
	secretBindings bool
}

func projectScriptNode(value any, ctx scriptProjectionContext) any {
	switch typed := value.(type) {
	case map[string]any:
		if ctx.headerList {
			return projectHeaderEntry(typed)
		}
		credentialValue := scriptMapHasCredentialValue(typed)
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if ctx.secretBindings {
				if text, ok := child.(string); ok && isPortableSecretReference(text) {
					out[key] = text
				} else {
					// Bruno forwards every map value as executable credential
					// material regardless of its caller-controlled alias key.
					// Only an exact governed reference may survive projection.
					out[key] = maskedScriptSecret
				}
				continue
			}
			// Import review evidence is client-extensible and frequently embeds
			// raw source snippets or sibling name/value pairs that the ordinary
			// script projection cannot associate. Apply the import-specific,
			// recursive disclosure boundary before this metadata can be persisted.
			if isReviewEvidenceSubtreeKey(key) {
				out[key] = sanitizeImportReviewEvidence(child)
				continue
			}
			if credentialValue && strings.EqualFold(key, "value") {
				if text, ok := child.(string); ok && isPortableTemplateBinding(text) {
					out[key] = text
				} else if text, ok := child.(string); ok {
					out[key] = projectSensitiveScriptValue(text)
				} else {
					out[key] = maskedScriptSecret
				}
				continue
			}
			childCtx := scriptProjectionContext{
				key:            key,
				opaqueMap:      ctx.opaqueMap || isOpaqueScriptMap(key),
				headers:        isScriptHeaderMap(key),
				headerList:     isScriptHeaderList(key),
				secretBindings: isScriptSecretBindingsMap(key),
			}
			if ctx.headers {
				out[key] = projectHeaderValue(key, child)
				continue
			}
			out[key] = projectScriptNode(child, childCtx)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = projectScriptNode(child, ctx)
		}
		return out
	case string:
		return projectScriptString(typed, ctx)
	default:
		if isSensitiveScriptKey(ctx.key) && !isSecretReferenceKey(ctx.key) {
			return maskedScriptSecret
		}
		return value
	}
}

func scriptMapHasCredentialValue(value map[string]any) bool {
	if _, hasType := value["type"]; !hasType {
		return false
	}
	var hints strings.Builder
	for _, key := range []string{"name", "type", "target", "selector", "object_ref"} {
		if text, ok := value[key].(string); ok {
			hints.WriteByte(' ')
			hints.WriteString(strings.ToLower(text))
		}
	}
	normalized := normalizeScriptFieldName(hints.String())
	for _, marker := range []string{"password", "passwd", "passphrase", "secret", "token", "credential", "authorization", "cookie"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func projectHeaderEntry(entry map[string]any) map[string]any {
	out := make(map[string]any, len(entry))
	name, _ := entry["name"].(string)
	for key, value := range entry {
		if strings.EqualFold(key, "value") {
			out[key] = projectHeaderValue(name, value)
			continue
		}
		out[key] = projectScriptNode(value, scriptProjectionContext{key: key})
	}
	return out
}

func projectHeaderValue(name string, value any) any {
	text, ok := value.(string)
	if !ok {
		return projectScriptNode(value, scriptProjectionContext{key: name, opaqueMap: true})
	}
	if (scrub.Policy{}).SensitiveHeader(name) || isSensitiveScriptKey(name) {
		if isPortableTemplateBinding(text) {
			return text
		}
		return projectSensitiveScriptValue(text)
	}
	return scrubScriptText(text, false)
}

func projectScriptString(value string, ctx scriptProjectionContext) string {
	if value == "" || isPortableTemplateBinding(value) {
		return value
	}
	if isSecretReferenceKey(ctx.key) && isNamedSecretReference(value) {
		return value
	}
	if isSensitiveScriptKey(ctx.key) {
		return projectSensitiveScriptValue(value)
	}
	if isScriptURLKey(ctx.key) {
		return maskScriptURL(value)
	}
	if ctx.opaqueMap || isScriptFreeTextKey(ctx.key) {
		return scrubScriptText(value, false)
	}
	return value
}

func maskScriptURL(value string) string {
	value = scrubScriptText(value, true)
	// ValueURL deliberately preserves the authority. Script configuration is a
	// different trust boundary: URL userinfo is itself a credential and must be
	// removed without normalizing the remainder of an operator-authored URL.
	scheme := strings.Index(value, "://")
	if scheme < 0 {
		return value
	}
	authorityStart := scheme + 3
	authorityEnd := len(value)
	if slash := strings.IndexAny(value[authorityStart:], "/?#"); slash >= 0 {
		authorityEnd = authorityStart + slash
	}
	if at := strings.LastIndex(value[authorityStart:authorityEnd], "@"); at >= 0 {
		at += authorityStart
		value = value[:authorityStart] + maskedScriptSecret + value[at:]
	}
	return value
}

func normalizeScriptFieldName(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isSensitiveScriptKey(key string) bool {
	normalized := normalizeScriptFieldName(key)
	// OAuth token_url identifies the remote endpoint; it is not token material.
	// Bruno's persist-session setting is likewise a boolean runtime control, not
	// session material. Keep these explicit exceptions ahead of the shared
	// component-aware vocabulary, which correctly treats standalone token and
	// session components as sensitive in otherwise opaque extension maps.
	if normalized == "tokenurl" || normalized == "persistsessionacrossiterations" {
		return false
	}
	// Keep script projection aligned with the shared credential vocabulary used
	// by URL/body/archive scrubbers. Script-specific handling below deliberately
	// adds usernames and common password aliases because source exports should
	// not disclose even low-entropy static login material.
	if scrub.SensitiveName(key) {
		return true
	}
	switch normalized {
	case "authorization", "proxyauthorization", "password", "passwd", "passphrase",
		"pwd", "pass", "username",
		"token", "accesstoken", "refreshtoken", "idtoken", "authtoken",
		"apikey", "accesskey", "accesskeyid", "secretkey", "secretaccesskey",
		"awsaccesskeyid", "awssecretaccesskey", "clientsecret", "privatekey",
		"credential", "credentials", "connectionstring", "dsn", "saslpassword",
		"cookie", "setcookie", "session", "sessionid", "csrftoken", "xsrftoken":
		return true
	default:
		for _, suffix := range []string{"authorization", "password", "passwd", "passphrase", "username", "token", "secret", "apikey", "credential", "cookie", "sessionid", "privatekey", "accesskey", "accesskeyid", "secretaccesskey"} {
			if strings.HasSuffix(normalized, suffix) {
				return true
			}
		}
		return false
	}
}

func isSecretReferenceKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "dsn_secret", "sasl_password_secret", "connection_string_secret", "ca_cert_secret",
		"trust_store_ref", "credential_ref", "storage_ref":
		return true
	default:
		return false
	}
}

func isPortableSecretReference(value string) bool {
	trimmed := strings.TrimSpace(value)
	return scriptSecretTemplatePattern.MatchString(trimmed) && scriptSecretTemplatePattern.FindString(trimmed) == trimmed
}

func isPortableSecretBinding(value string) bool {
	if strings.Contains(value, maskedScriptSecret) || !scriptSecretTemplatePattern.MatchString(value) {
		return false
	}
	remainder := strings.TrimSpace(scriptSecretTemplatePattern.ReplaceAllString(value, ""))
	switch strings.ToLower(remainder) {
	case "", "bearer", "basic", "digest":
		return true
	default:
		return false
	}
}

func isPortableTemplateBinding(value string) bool {
	if strings.Contains(value, maskedScriptSecret) {
		return false
	}
	references := portableScriptTemplateReferences(value)
	if len(references) == 0 {
		return false
	}
	remainder := value
	for _, reference := range references {
		remainder = strings.Replace(remainder, reference, "", 1)
	}
	remainder = strings.TrimSpace(remainder)
	switch strings.ToLower(remainder) {
	case "", "bearer", "basic", "digest":
		return true
	default:
		return false
	}
}

// projectSensitiveScriptValue retains only a non-secret authentication scheme
// wrapper. This lets guided binding turn `Bearer [REDACTED]` into
// `Bearer {{secret.NAME}}` without restoring or disclosing the credential.
func projectSensitiveScriptValue(value string) string {
	trimmed := strings.TrimSpace(value)
	for _, scheme := range []string{"Bearer", "Basic", "Digest"} {
		if len(trimmed) > len(scheme) && strings.EqualFold(trimmed[:len(scheme)], scheme) && unicode.IsSpace(rune(trimmed[len(scheme)])) {
			return trimmed[:len(scheme)] + " " + maskedScriptSecret
		}
	}
	return maskedScriptSecret
}

func portableScriptTemplateReferences(value string) []string {
	candidates := scriptPortableTemplatePattern.FindAllString(value, -1)
	references := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if isPortableScriptTemplateReference(candidate) {
			references = append(references, candidate)
		}
	}
	return references
}

func isPortableScriptTemplateReference(value string) bool {
	if len(value) < 5 || !strings.HasPrefix(value, "{{") || !strings.HasSuffix(value, "}}") {
		return false
	}
	expr := strings.TrimSpace(value[2 : len(value)-2])
	if scriptPortableReferencePattern.MatchString(expr) || scriptPortableRandomPattern.MatchString(expr) || scriptPortableRandomStringPattern.MatchString(expr) {
		return true
	}
	switch expr {
	case "fn.uuid()", "fn.uuid", "fn.uuid.v4",
		"fn.timestamp.unix", "fn.timestamp.unix_ms", "fn.timestamp.iso8601", "fn.timestamp.rfc3339",
		"fake.uuid", "fake.uuid()", "fake.email", "fake.email()":
		return true
	default:
		return false
	}
}

func scrubScriptText(value string, urlValue bool) string {
	if value == "" {
		return value
	}
	if !urlValue {
		var structured any
		if json.Unmarshal([]byte(value), &structured) == nil {
			switch structured.(type) {
			case map[string]any, []any:
				projected := projectScriptNode(structured, scriptProjectionContext{opaqueMap: true})
				if payload, err := json.Marshal(projected); err == nil {
					return string(payload)
				}
			}
		}
	}
	value, templateBindings := protectTemplateBindings(value)
	references := portableScriptTemplateReferences(value)
	protected := value
	for i, reference := range references {
		placeholder := fmt.Sprintf("apexbindingref%d", i)
		protected = strings.Replace(protected, reference, placeholder, 1)
	}
	if urlValue {
		protected = scrub.ValueURL(protected, scrub.Policy{})
	} else {
		protected = scrub.Value(protected, scrub.Policy{})
	}
	for i, reference := range references {
		placeholder := fmt.Sprintf("apexbindingref%d", i)
		protected = strings.Replace(protected, placeholder, reference, 1)
	}
	for placeholder, binding := range templateBindings {
		protected = strings.ReplaceAll(protected, placeholder, binding)
	}
	return protected
}

func protectTemplateBindings(value string) (string, map[string]string) {
	replacements := map[string]string{}
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '?' || r == '&' || r == '#' })
	for _, part := range parts {
		_, rawValue, ok := strings.Cut(part, "=")
		if !ok || !isPortableTemplateBinding(rawValue) {
			continue
		}
		placeholder := fmt.Sprintf("apexsafe%d=apexref%d", len(replacements), len(replacements))
		replacements[placeholder] = part
		value = strings.Replace(value, part, placeholder, 1)
	}
	return value, replacements
}

func isNamedSecretReference(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, maskedScriptSecret) || len(value) > 512 {
		return false
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._:/@-{}", r) {
			continue
		}
		return false
	}
	return true
}

func isScriptURLKey(key string) bool {
	switch normalizeScriptFieldName(key) {
	case "baseurl", "url", "brokerurl", "schemaregistryurl", "server", "origin", "target", "address":
		return true
	default:
		return false
	}
}

func isScriptFreeTextKey(key string) bool {
	switch normalizeScriptFieldName(key) {
	case "body", "value", "expected", "inlinedata", "schema", "objectschema", "expression", "left", "right":
		return true
	default:
		return false
	}
}

func isOpaqueScriptMap(key string) bool {
	switch normalizeScriptFieldName(key) {
	case "params", "metadata", "preserved", "properties", "labels", "options",
		"additionalattributes", "requestmapping", "responsemapping", "namespaces":
		return true
	default:
		return false
	}
}

func isScriptHeaderMap(key string) bool {
	switch normalizeScriptFieldName(key) {
	case "headers", "defaultheaders":
		return true
	default:
		return false
	}
}

func isScriptHeaderList(key string) bool {
	return normalizeScriptFieldName(key) == "headerlist"
}

func isScriptSecretBindingsMap(key string) bool {
	return normalizeScriptFieldName(key) == "secretbindings"
}

// reconcileMaskedScriptSecrets restores only executable sentinels that exactly
// match the projection of a prior value. A new/orphan executable sentinel is
// rejected, preventing a downloaded or federated masked script from executing
// "[REDACTED]" as though it were a credential. Redacted import/source-review
// evidence is retained as non-executable provenance and intentionally skipped.
func reconcileMaskedScriptSecrets(incoming *dsl.Script, prior *dsl.Script) error {
	if incoming == nil {
		return fmt.Errorf("script is required")
	}
	incomingJSON, err := scriptAsJSONValue(incoming)
	if err != nil {
		return err
	}
	if !jsonValueContainsExecutableMaskedSecret(incomingJSON, "") {
		return nil
	}
	if prior == nil {
		return fmt.Errorf("script contains an orphan %s sentinel; bind a secret reference instead", maskedScriptSecret)
	}
	rawPrior, err := scriptAsJSONValue(prior)
	if err != nil {
		return err
	}
	projectedPrior, err := scriptAsJSONValue(maskScriptSecrets(prior))
	if err != nil {
		return err
	}
	reconciled, err := reconcileProjectedJSON(incomingJSON, projectedPrior, rawPrior, "script")
	if err != nil {
		return err
	}
	payload, err := json.Marshal(reconciled)
	if err != nil {
		return err
	}
	return json.Unmarshal(payload, incoming)
}

func scriptAsJSONValue(script *dsl.Script) (any, error) {
	if script == nil {
		return nil, nil
	}
	payload, err := json.Marshal(script)
	if err != nil {
		return nil, err
	}
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func reconcileProjectedJSON(incoming any, projectedPrior any, rawPrior any, path string) (any, error) {
	switch typed := incoming.(type) {
	case string:
		if !strings.Contains(typed, maskedScriptSecret) {
			return typed, nil
		}
		projected, projectedOK := projectedPrior.(string)
		raw, rawOK := rawPrior.(string)
		if projectedOK && rawOK && typed == projected && !strings.Contains(raw, maskedScriptSecret) {
			return raw, nil
		}
		return nil, fmt.Errorf("%s contains an orphan %s sentinel; bind a secret reference instead", path, maskedScriptSecret)
	case map[string]any:
		projectedMap, _ := projectedPrior.(map[string]any)
		rawMap, _ := rawPrior.(map[string]any)
		priorWasReviewOnly := isNonExecutableReviewAction(projectedMap) || isNonExecutableReviewAction(rawMap)
		if priorWasReviewOnly && !isNonExecutableReviewAction(typed) && jsonValueContainsExecutableMaskedSecret(typed, path) {
			return nil, fmt.Errorf("%s promotes redacted review evidence into executable fields; bind a secret reference instead", path)
		}
		if isNonExecutableReviewAction(typed) {
			return incoming, nil
		}
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			skipPath := path
			if path == "script" {
				skipPath = ""
			}
			if isNonExecutableRedactionSubtree(typed, key, skipPath) {
				out[key] = child
				continue
			}
			var projectedChild, rawChild any
			if projectedMap != nil {
				projectedChild = projectedMap[key]
			}
			if rawMap != nil {
				rawChild = rawMap[key]
			}
			reconciled, err := reconcileProjectedJSON(child, projectedChild, rawChild, path+"."+key)
			if err != nil {
				return nil, err
			}
			out[key] = reconciled
		}
		return out, nil
	case []any:
		projectedSlice, projectedOK := projectedPrior.([]any)
		rawSlice, _ := rawPrior.([]any)
		if jsonValueContainsExecutableMaskedSecret(typed, path) && (!projectedOK || !reflect.DeepEqual(typed, projectedSlice)) {
			for i, child := range typed {
				incomingMap, incomingOK := child.(map[string]any)
				if !incomingOK || i >= len(projectedSlice) {
					continue
				}
				projectedMap, _ := projectedSlice[i].(map[string]any)
				var rawMap map[string]any
				if i < len(rawSlice) {
					rawMap, _ = rawSlice[i].(map[string]any)
				}
				priorWasReviewOnly := isNonExecutableReviewAction(projectedMap) || isNonExecutableReviewAction(rawMap)
				if priorWasReviewOnly && !isNonExecutableReviewAction(incomingMap) && jsonValueContainsExecutableMaskedSecret(incomingMap, path) {
					return nil, fmt.Errorf("%s[%d] promotes redacted review evidence into executable fields; bind a secret reference instead", path, i)
				}
			}
			return nil, fmt.Errorf("%s changed while containing redacted credentials; bind secret references before modifying or reordering it", path)
		}
		out := make([]any, len(typed))
		for i, child := range typed {
			var projectedChild, rawChild any
			if i < len(projectedSlice) {
				projectedChild = projectedSlice[i]
			}
			if i < len(rawSlice) {
				rawChild = rawSlice[i]
			}
			reconciled, err := reconcileProjectedJSON(child, projectedChild, rawChild, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return nil, err
			}
			out[i] = reconciled
		}
		return out, nil
	default:
		return incoming, nil
	}
}

func jsonValueContainsExecutableMaskedSecret(value any, path string) bool {
	switch typed := value.(type) {
	case string:
		return strings.Contains(typed, maskedScriptSecret)
	case map[string]any:
		if isNonExecutableReviewAction(typed) {
			return false
		}
		for key, child := range typed {
			if isNonExecutableRedactionSubtree(typed, key, path) {
				continue
			}
			if jsonValueContainsExecutableMaskedSecret(child, appendJSONPointer(path, key)) {
				return true
			}
		}
	case []any:
		for index, child := range typed {
			if jsonValueContainsExecutableMaskedSecret(child, appendJSONPointer(path, strconv.Itoa(index))) {
				return true
			}
		}
	}
	return false
}

// maskScriptResponseValue recursively projects script-shaped objects embedded
// in heterogeneous import/review responses. Converter result structs often
// repeat the same script under result.script, so masking only the top-level
// convenience field would leave a second disclosure path.
func maskScriptResponseValue(value any) any {
	if value == nil {
		return nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var raw any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return value
	}
	return projectEmbeddedScriptNodes(raw)
}

func projectEmbeddedScriptNodes(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		if isScriptJSONObject(typed) {
			return projectScriptNode(typed, scriptProjectionContext{})
		}
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = projectEmbeddedScriptNodes(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = projectEmbeddedScriptNodes(child)
		}
		return out
	default:
		return value
	}
}

func isScriptJSONObject(value map[string]any) bool {
	_, hasConfig := value["config"]
	_, hasActions := value["actions"]
	_, hasProtocol := value["protocol"]
	return hasConfig && hasActions && hasProtocol
}
