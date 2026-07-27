package api

// Script response projection and masked-write reconciliation live here rather
// than beside execution-time masking. Runtime scripts must retain credentials;
// viewer, export, federation, and source-control representations must not.

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

const maskedScriptSecret = scrub.Redacted

var scriptSecretTemplatePattern = regexp.MustCompile(`\{\{\s*secret\.(` + engine.LogicalSecretNameInputPattern + `)\s*\}\}`)
var scriptPortableTemplatePattern = regexp.MustCompile(`\{\{[^{}\r\n]{1,256}\}\}`)
var scriptPortableReferencePattern = regexp.MustCompile(`^(?:(?:env|data|var|param|file)\.[A-Za-z0-9_.:/@-]+|secret\.` + engine.LogicalSecretNameInputPattern + `)$`)
var scriptPortableRandomPattern = regexp.MustCompile(`^fn\.random\(\s*-?\d+\s*,\s*-?\d+\s*\)$`)
var scriptPortableRandomStringPattern = regexp.MustCompile(`^fn\.random_string\(\s*\d+\s*\)$`)
var redactedXMLMessageIDPattern = regexp.MustCompile(`(?is)(<(?:(?:[\w.-]+):)?messageId\b[^>]*>)\s*\[REDACTED\]\s*(</(?:(?:[\w.-]+):)?messageId\s*>)`)
var xmlMessageIDValuePattern = regexp.MustCompile(`(?is)(<(?:(?:[\w.-]+):)?messageId\b[^>]*>)([^<>]*)(</(?:(?:[\w.-]+):)?messageId\s*>)`)
var portableXMLSecretElementPattern = regexp.MustCompile(`(?is)<(?:[\w.-]+:)?[\w.-]+\b[^>]*>\s*\{\{\s*secret\.` + engine.LogicalSecretNameInputPattern + `\s*\}\}\s*</(?:[\w.-]+:)?[\w.-]+\s*>`)
var portableXMLSecretAttributePattern = regexp.MustCompile(`(?is)(?:[\w.-]+:)?[\w.-]+\s*=\s*(?:"\s*\{\{\s*secret\.` + engine.LogicalSecretNameInputPattern + `\s*\}\}\s*"|'\s*\{\{\s*secret\.` + engine.LogicalSecretNameInputPattern + `\s*\}\}\s*')`)
var portableXMLTemplateElementPattern = regexp.MustCompile(`(?is)<(?:[\w.-]+:)?[\w.-]+\b[^>]*>\s*\{\{[^{}\r\n]{1,256}\}\}\s*</(?:[\w.-]+:)?[\w.-]+\s*>`)
var portableXMLTemplateAttributePattern = regexp.MustCompile(`(?is)(?:[\w.-]+:)?[\w.-]+\s*=\s*(?:"\s*\{\{[^{}\r\n]{1,256}\}\}\s*"|'\s*\{\{[^{}\r\n]{1,256}\}\}\s*')`)

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
	var working dsl.Script
	if err := json.Unmarshal(payload, &working); err != nil {
		return nil
	}
	maskJavaVuserVMParameterValues(&working)
	protectedBodies, ok := projectScriptRequestBodies(&working)
	if !ok {
		return nil
	}
	payload, err = json.Marshal(&working)
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
	restored := 0
	if err := dslwalk.Walk(&masked, func(ref dslwalk.SurfaceRef) error {
		body, exists := protectedBodies[ref.Path]
		if !exists {
			return nil
		}
		setImportCommitSurfaceBody(ref, body)
		restored++
		return nil
	}); err != nil || restored != len(protectedBodies) {
		return nil
	}
	restoreBrunoNativeCompactProvenance(&masked, script)
	return &masked
}

// maskJavaVuserVMParameterValues is a read/export defense for legacy rows and
// any response assembled before current DSL validation runs. JVM arguments are
// observable in the operating-system process table, so an unsafe value must
// never be copied back through an API projection. Safe JVM tuning flags and
// ordinary configuration values remain visible and round-trip unchanged.
func maskJavaVuserVMParameterValues(script *dsl.Script) {
	if script == nil {
		return
	}
	for index, parameter := range script.Config.JavaVuser.AdditionalVMParameters {
		if javarunner.ContainsSensitiveVMParameterValue(parameter) {
			script.Config.JavaVuser.AdditionalVMParameters[index] = maskedScriptSecret
		}
	}
}

// validateJavaVMParameterDisclosureSafety is the write-side companion to the
// legacy response masker above. JVM arguments intentionally do not support
// {{secret.*}} resolution because argv is process-observable. Reject unsafe
// values with value-free diagnostics before generic secret-binding discovery
// can offer an invalid project-secret binding for this surface.
func validateJavaVMParameterDisclosureSafety(script *dsl.Script) error {
	if script == nil {
		return nil
	}
	issues := javarunner.ValidateVMParameters(script.Config.JavaVuser.AdditionalVMParameters)
	if len(issues) == 0 {
		return nil
	}
	issue := issues[0]
	if issue.Index < 0 {
		return fmt.Errorf(
			"config.java_vuser.additional_vm_parameters is invalid (%s) and cannot be stored or injected securely; correct or remove the argument list",
			issue.Category,
		)
	}
	return fmt.Errorf(
		"config.java_vuser.additional_vm_parameters[%d] is invalid (%s) and cannot be stored or injected securely; use a non-sensitive governed option or remove the argument",
		issue.Index,
		issue.Category,
	)
}

// projectScriptRequestBodies keeps every request body out of the generic
// free-text projector. Shared structured codecs preserve source bytes and
// rewrite only credential spans; unrecognized raw/file bytes pass through
// exactly. Unsafe typed or mixed content fails closed.
func projectScriptRequestBodies(script *dsl.Script) (map[string]string, bool) {
	protected := make(map[string]string)
	err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		body, ok, err := decodeImportCommitProjectionBody(ref)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		projected, result, err := credentialpolicy.ProjectRequestBody(
			body.wire,
			importCommitSurfaceContentType(ref),
			credentialpolicy.Options{
				Mode: credentialpolicy.Redact,
				Source: credentialpolicy.SourceRef{
					Format:  "script_projection",
					Path:    ref.Path,
					Pointer: "/body",
				},
				RequestKey:         ref.Path,
				TrustedWholeValues: portableBodyTemplateValues(body.text()),
				TargetField:        "body",
			},
		)
		for index := range result.Requirements {
			result.Requirements[index].Value = ""
		}
		if err != nil || result.Disposition == credentialpolicy.Rejected || projected == nil {
			return errors.New("request body cannot be projected safely")
		}
		if result.Disposition == credentialpolicy.BlockedDraft {
			applyImportCommitBodyReviewGate(ref, "script_projection", result)
		}
		projected = projectMaskedXMLMessageIDs(projected)
		protected[ref.Path] = body.encode(projected)
		setImportCommitSurfaceBody(ref, "")
		return nil
	})
	if err != nil {
		return nil, false
	}
	return protected, true
}

func projectMaskedXMLMessageIDs(payload []byte) []byte {
	value := string(payload)
	matches := xmlMessageIDValuePattern.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return payload
	}
	var builder strings.Builder
	builder.Grow(len(value))
	cursor := 0
	changed := false
	for _, match := range matches {
		if len(match) != 8 {
			continue
		}
		content := value[match[4]:match[5]]
		if scrubScriptText(content, false) != maskedScriptSecret {
			continue
		}
		builder.WriteString(value[cursor:match[4]])
		builder.WriteString("{{fn.uuid()}}")
		cursor = match[5]
		changed = true
	}
	if !changed {
		return payload
	}
	builder.WriteString(value[cursor:])
	return []byte(builder.String())
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
	delete(masked.ReviewState, authoringSessionIdempotencyReviewKey)
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
	metadata       bool
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
			// Java Vuser approval provenance is a server-authored governance
			// envelope. Its authoring-session UUID is an identifier, not runtime
			// session material. Preserve only the two exact, validated scalar
			// fields here; ordinary session_id keys continue through the normal
			// credential projection and remain masked.
			if key == javaVuserApprovalEvidenceKey {
				out[key] = projectJavaVuserApprovalEvidence(child)
				continue
			}
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
				metadata:       ctx.metadata || normalizeScriptFieldName(key) == "metadata",
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

func projectJavaVuserApprovalEvidence(value any) map[string]any {
	evidence, ok := value.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	out := make(map[string]any, 2)
	sessionID, sessionOK := evidence["session_id"].(string)
	sessionID = strings.TrimSpace(sessionID)
	if sessionOK {
		if _, err := uuid.Parse(sessionID); err == nil {
			out["session_id"] = sessionID
		}
	}
	contentHash, hashOK := evidence["content_hash"].(string)
	contentHash = strings.ToLower(strings.TrimSpace(contentHash))
	if hashOK && isCanonicalSHA256(contentHash) {
		out["content_hash"] = contentHash
	}
	return out
}

const sha256HexLength = 64

func isCanonicalSHA256(value string) bool {
	const prefix = "sha256:"
	return strings.HasPrefix(value, prefix) && isLowerHexDigest(strings.TrimPrefix(value, prefix), sha256HexLength)
}

func isLowerHexDigest(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
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
	if isNonSecretIdentifierHeader(name) {
		return scrub.ValueURL(text, scrub.Policy{})
	}
	return scrubScriptText(text, false)
}

func projectScriptString(value string, ctx scriptProjectionContext) string {
	if value == "" || isPortableTemplateBinding(value) {
		return value
	}
	if ctx.metadata && isPortableImportProvenanceIdentifierKey(ctx.key) {
		return scrub.ValueURL(value, scrub.Policy{})
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

func isNonSecretIdentifierHeader(name string) bool {
	normalized := normalizeScriptFieldName(name)
	if normalized == "" {
		return false
	}
	for _, sensitive := range []string{"authorization", "password", "secret", "token", "cookie", "session", "credential", "apikey", "accesskey", "privatekey"} {
		if strings.Contains(normalized, sensitive) {
			return false
		}
	}
	for _, suffix := range []string{"messageid", "traceid", "correlationid", "requestid"} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
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
	// by URL/body/archive scrubbers. Usernames are identity material rather than
	// credentials and must survive the legacy inspection/commit round trip.
	if scrub.SensitiveName(key) {
		return true
	}
	switch normalized {
	case "authorization", "proxyauthorization", "password", "passwd", "passphrase",
		"pwd", "pass",
		"token", "accesstoken", "refreshtoken", "idtoken", "authtoken",
		"apikey", "accesskey", "accesskeyid", "secretkey", "secretaccesskey",
		"awsaccesskeyid", "awssecretaccesskey", "clientsecret", "privatekey",
		"credential", "credentials", "connectionstring", "dsn", "saslpassword",
		"cookie", "setcookie", "session", "sessionid", "csrftoken", "xsrftoken":
		return true
	default:
		for _, suffix := range []string{"authorization", "password", "passwd", "passphrase", "token", "secret", "apikey", "credential", "cookie", "sessionid", "privatekey", "accesskey", "accesskeyid", "secretaccesskey"} {
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
				projected, result, err := credentialpolicy.ProjectJSON(
					[]byte(value),
					credentialpolicy.Options{
						Mode:               credentialpolicy.Redact,
						Source:             credentialpolicy.SourceRef{Format: "script_projection"},
						RequestKey:         "script_projection",
						TrustedWholeValues: portableBodyTemplateValues(value),
						TargetField:        "text",
					},
				)
				if err == nil && result.Disposition != credentialpolicy.Rejected && projected != nil {
					return string(projected)
				}
			}
		}
	}
	var urlSeparators map[string]string
	if urlValue {
		// A URL has only one formal query delimiter, but imported and generated
		// targets sometimes repeat "?name=value" instead of joining later
		// parameters with '&'. ValueURL correctly treats everything after the
		// first '?' as the query; without this reversible normalization, many
		// protected template bindings become one high-entropy query value and
		// the safe references are redacted together. Expose each later segment
		// to the ordinary parameter scrubber, then restore the source bytes.
		value, urlSeparators = protectAdditionalURLParamSeparators(value)
	}
	value, xmlSecretBindings := protectPortableXMLSecretBindings(value)
	value, templateBindings := protectTemplateBindings(value)
	references := portableScriptTemplateReferences(value)
	protected := value
	referencePlaceholderPrefix := scriptProjectionPlaceholderPrefix(value, "binding")
	for i, reference := range references {
		placeholder := referencePlaceholderPrefix + strconv.Itoa(i) + "Z"
		protected = strings.Replace(protected, reference, placeholder, 1)
	}
	if urlValue {
		protected = scrub.ValueURL(protected, scrub.Policy{})
	} else {
		protected = scrub.Value(protected, scrub.Policy{})
	}
	for i, reference := range references {
		placeholder := referencePlaceholderPrefix + strconv.Itoa(i) + "Z"
		protected = strings.Replace(protected, placeholder, reference, 1)
	}
	for placeholder, binding := range templateBindings {
		protected = strings.ReplaceAll(protected, placeholder, binding)
	}
	for placeholder, binding := range xmlSecretBindings {
		protected = strings.ReplaceAll(protected, placeholder, binding)
	}
	for placeholder, separator := range urlSeparators {
		protected = strings.ReplaceAll(protected, placeholder, separator)
	}
	protected = redactedXMLMessageIDPattern.ReplaceAllString(protected, `${1}{{fn.uuid()}}${2}`)
	return protected
}

func portableBodyTemplateValues(value string) map[string]struct{} {
	decoder := json.NewDecoder(strings.NewReader(value))
	trusted := make(map[string]struct{})
	for _, reference := range portableScriptTemplateReferences(value) {
		trusted[strings.TrimSpace(reference)] = struct{}{}
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		text, ok := token.(string)
		if !ok || !isPortableTemplateBinding(text) {
			continue
		}
		trusted[strings.TrimSpace(text)] = struct{}{}
	}
	if len(trusted) == 0 {
		return nil
	}
	return trusted
}

// protectAdditionalURLParamSeparators temporarily represents every '?' after
// the URL's first query delimiter (and every '?' in the fragment) as an
// ordinary '&'-delimited, low-entropy parameter. This lets ValueURL inspect
// each imported parameter independently while the collision-safe marker makes
// restoration exact even when the source already contains marker-like text.
func protectAdditionalURLParamSeparators(value string) (string, map[string]string) {
	if !strings.Contains(value, "?") {
		return value, nil
	}
	placeholderPrefix := scriptProjectionPlaceholderPrefix(value, "urlseparator")
	replacements := map[string]string{}
	var protected strings.Builder
	protected.Grow(len(value))
	queryStarted := false
	inFragment := false
	for offset := 0; offset < len(value); offset++ {
		switch value[offset] {
		case '#':
			inFragment = true
			protected.WriteByte(value[offset])
		case '?':
			if !queryStarted && !inFragment {
				queryStarted = true
				protected.WriteByte(value[offset])
				continue
			}
			placeholder := "&" + placeholderPrefix + strconv.Itoa(len(replacements)) + "Z=safe&"
			replacements[placeholder] = "?"
			protected.WriteString(placeholder)
		default:
			protected.WriteByte(value[offset])
		}
	}
	if len(replacements) == 0 {
		return value, nil
	}
	return protected.String(), replacements
}

// protectPortableXMLSecretBindings preserves already-governed secret
// references when they are the complete value of an XML element or attribute.
// The generic scrubber intentionally replaces the entire value of
// credential-named XML nodes, so protecting only the inner {{secret.*}} token
// would still lose the executable reference.
func protectPortableXMLSecretBindings(value string) (string, map[string]string) {
	protected := value
	replacements := map[string]string{}
	placeholderPrefix := scriptProjectionPlaceholderPrefix(value, "xmlbinding")
	nextPlaceholder := func(binding string) string {
		// The trailing delimiter keeps index 1 from being a prefix of index 10,
		// so restoration remains safe regardless of map iteration order.
		placeholder := placeholderPrefix + strconv.Itoa(len(replacements)) + "Z"
		replacements[placeholder] = binding
		return placeholder
	}
	protected = portableXMLSecretElementPattern.ReplaceAllStringFunc(protected, func(candidate string) string {
		return nextPlaceholder(scrubPortableXMLElementOpeningTag(candidate))
	})
	protected = portableXMLSecretAttributePattern.ReplaceAllStringFunc(protected, nextPlaceholder)
	protectPortable := func(candidate string) string {
		reference := scriptPortableTemplatePattern.FindString(candidate)
		if !isPortableScriptTemplateReference(reference) {
			return candidate
		}
		return nextPlaceholder(scrubPortableXMLElementOpeningTag(candidate))
	}
	protected = portableXMLTemplateElementPattern.ReplaceAllStringFunc(protected, protectPortable)
	protected = portableXMLTemplateAttributePattern.ReplaceAllStringFunc(protected, protectPortable)
	return protected, replacements
}

// scrubPortableXMLElementOpeningTag keeps whole-element template protection from
// hiding plaintext credentials in sibling attributes. Only the element text
// template needs preservation; the opening tag must still pass through the
// ordinary credential scrubber before the complete element is hidden behind a
// placeholder.
func scrubPortableXMLElementOpeningTag(candidate string) string {
	end := strings.IndexByte(candidate, '>')
	if end < 0 {
		return candidate
	}
	return scrub.Value(candidate[:end+1], scrub.Policy{}) + candidate[end+1:]
}

func protectTemplateBindings(value string) (string, map[string]string) {
	replacements := map[string]string{}
	placeholderPrefix := scriptProjectionPlaceholderPrefix(value, "querybinding")
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '?' || r == '&' || r == '#' })
	for _, part := range parts {
		_, rawValue, ok := strings.Cut(part, "=")
		if !ok || !isPortableTemplateBinding(rawValue) {
			continue
		}
		placeholder := placeholderPrefix + strconv.Itoa(len(replacements)) + "=safe"
		replacements[placeholder] = part
		value = strings.Replace(value, part, placeholder, 1)
	}
	return value, replacements
}

// scriptProjectionPlaceholderPrefix returns a deterministic marker namespace
// that cannot occur in the input. Digest groups are deliberately shorter than
// the generic high-entropy-token threshold so the scrubber cannot redact its
// own temporary markers.
func scriptProjectionPlaceholderPrefix(value, domain string) string {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(domain+"\x00"+value)))
	var prefix strings.Builder
	prefix.WriteString("apex")
	prefix.WriteString(domain)
	prefix.WriteByte('~')
	for offset := 0; offset < len(digest); offset += 4 {
		prefix.WriteString(digest[offset : offset+4])
		prefix.WriteByte('~')
	}
	result := prefix.String()
	for strings.Contains(value, result) {
		result += "~"
	}
	return result
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
	// Reconciliation cannot be gated on the presence of a [REDACTED] sentinel.
	// scrubScriptText also emits SENTINEL-FREE projections (a redacted
	// <messageId> becomes {{fn.uuid()}}), so a read-modify-write of such a script
	// carries no sentinel at all; skipping reconciliation there persisted the
	// projection over the stored value and destroyed it. Whenever a prior version
	// exists we reconcile, and reconcileProjectedJSON restores only values the
	// client returned byte-identical to the projection.
	hasMaskedSecret := jsonValueContainsExecutableMaskedSecret(incomingJSON, "")
	if prior == nil {
		if hasMaskedSecret {
			return fmt.Errorf("script contains an orphan %s sentinel; bind a secret reference instead", maskedScriptSecret)
		}
		return nil
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
		projected, projectedOK := projectedPrior.(string)
		raw, rawOK := rawPrior.(string)
		// An unmodified projection must restore the stored value even when the
		// projection carries no [REDACTED] sentinel. scrubScriptText rewrites a
		// redacted <messageId> into {{fn.uuid()}}, which is sentinel-free; without
		// this branch a masked read-modify-write would persist the placeholder and
		// destroy the recorded correlation id.
		if projectedOK && rawOK && typed == projected && typed != raw && !strings.Contains(raw, maskedScriptSecret) {
			return raw, nil
		}
		if !strings.Contains(typed, maskedScriptSecret) {
			return typed, nil
		}
		if projectedOK && rawOK && typed == projected && !strings.Contains(raw, maskedScriptSecret) {
			return raw, nil
		}
		return nil, fmt.Errorf("%s contains an orphan %s sentinel; bind a secret reference instead", path, maskedScriptSecret)
	case map[string]any:
		projectedMap, _ := projectedPrior.(map[string]any)
		rawMap, _ := rawPrior.(map[string]any)
		priorWasReviewOnly := isNonExecutableReviewAction(projectedMap) ||
			isNonExecutableReviewAction(rawMap) ||
			isDurablyImportReviewGatedAction(projectedMap) ||
			isDurablyImportReviewGatedAction(rawMap)
		incomingReviewOnly := isNonExecutableReviewAction(typed) ||
			isDurablyImportReviewGatedAction(typed)
		if priorWasReviewOnly && !incomingReviewOnly && jsonValueContainsExecutableMaskedSecret(typed, path) {
			return nil, fmt.Errorf("%s promotes redacted review evidence into executable fields; bind a secret reference instead", path)
		}
		if incomingReviewOnly {
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
				priorWasReviewOnly := isNonExecutableReviewAction(projectedMap) ||
					isNonExecutableReviewAction(rawMap) ||
					isDurablyImportReviewGatedAction(projectedMap) ||
					isDurablyImportReviewGatedAction(rawMap)
				incomingReviewOnly := isNonExecutableReviewAction(incomingMap) ||
					isDurablyImportReviewGatedAction(incomingMap)
				if priorWasReviewOnly && !incomingReviewOnly && jsonValueContainsExecutableMaskedSecret(incomingMap, path) {
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
		if isNonExecutableReviewAction(typed) || isDurablyImportReviewGatedAction(typed) {
			return false
		}
		for key, child := range typed {
			if isNonExecutableRedactionSubtree(typed, key, path) {
				continue
			}
			childPath := appendJSONPointer(path, key)
			// Must mirror discoverRedactedValues: a provenance entry raises no
			// binding requirement, so treating it as an orphan sentinel here would
			// reject a save the user has no way to satisfy.
			if isImportProvenanceMetadataPath(childPath) {
				continue
			}
			if jsonValueContainsExecutableMaskedSecret(child, childPath) {
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
			return maskScriptJSONObject(typed)
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

// maskScriptJSONObject routes script-shaped values through the typed projection
// boundary. In particular, maskScriptSecrets decodes body_encoding=base64,
// projects the wire bytes, and re-encodes them; sending an encoded body directly
// through the generic free-text projector would corrupt it as a high-entropy
// token while leaving the base64 encoding marker in place.
func maskScriptJSONObject(value map[string]any) any {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var script dsl.Script
	if err := json.Unmarshal(payload, &script); err != nil {
		return nil
	}
	masked := maskScriptSecrets(&script)
	if masked == nil {
		return nil
	}
	projected, err := scriptAsJSONValue(masked)
	if err != nil {
		return nil
	}
	return projected
}

func isScriptJSONObject(value map[string]any) bool {
	_, hasConfig := value["config"]
	_, hasActions := value["actions"]
	_, hasProtocol := value["protocol"]
	return hasConfig && hasActions && hasProtocol
}
