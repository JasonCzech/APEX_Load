package soap

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"html"
	stdhttp "net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/boundedcache"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpproto "github.com/jasonczech/apexload/pkg/protocol/http"
)

func init() {
	protocol.Register("soap", func() protocol.Protocol {
		return &SOAP{}
	})
}

type SOAP struct {
	unredactedDebugSnapshots bool
	http                     *httpproto.HTTP
	baseURL                  string
	version                  string
	failOnFault              bool
	defaultCharset           string
	namespaces               map[string]string
	security                 soapSecurityConfig
	now                      func() time.Time
	nonceGenerator           func() ([]byte, error)
	// authoredRedactor is forwarded to the HTTP transport, which may bind it
	// before Init creates that transport.
	authoredRedactor protocol.TextRedactor
}

var _ protocol.RuntimeAuthoredTextRedactorSetter = (*SOAP)(nil)

// SetRuntimeAuthoredTextRedactor forwards the engine's exact-match authored
// redactor to the HTTP transport (Digest auth evidence relies on it).
func (s *SOAP) SetRuntimeAuthoredTextRedactor(redactor protocol.TextRedactor) {
	s.authoredRedactor = redactor
	if s.http != nil {
		s.http.SetRuntimeAuthoredTextRedactor(redactor)
	}
}

type soapSecurityConfig struct {
	username       string
	password       string
	passwordType   string
	includeNonce   bool
	includeCreated bool
	timestamp      bool
	timestampTTL   time.Duration
	// passwordFromSecretStore marks a password authored as a {{secret.NAME}}
	// reference and resolved from the project secret store. Its Password
	// element (text or digest, plus a digest's Nonce) is withheld from
	// snapshots and evidence, in unredacted debug too; a script-sourced
	// password is never withheld here.
	passwordFromSecretStore bool
}

// SecretStorePasswordConfigKey marks, in the protocol config, that
// soap_ws_security_password was authored as a secret-store reference.
const SecretStorePasswordConfigKey = "soap_ws_security_password_secret_store"

// redactedSOAPPassword replaces a secret-store Password element value in
// snapshots and evidence.
const redactedSOAPPassword = "[REDACTED]"

var envelopePrefixPattern = regexp.MustCompile(`<([A-Za-z_][A-Za-z0-9_.-]*):Envelope\b`)
var soapNamespaceDeclPattern = regexp.MustCompile(`xmlns:([A-Za-z_][A-Za-z0-9_.-]*)\s*=\s*['"][^'"]+['"]`)
var soapElementPrefixPattern = regexp.MustCompile(`</?([A-Za-z_][A-Za-z0-9_.-]*):[A-Za-z_][A-Za-z0-9_.-]*\b`)
var soapAttributePrefixPattern = regexp.MustCompile(`\s([A-Za-z_][A-Za-z0-9_.-]*):[A-Za-z_][A-Za-z0-9_.-]*\s*=`)
var soapGeneratedPatternCache = boundedcache.NewStringCache[*regexp.Regexp](0, 0, 0)

func compileGeneratedSOAPPattern(key string, pattern string) *regexp.Regexp {
	if cached, ok := soapGeneratedPatternCache.Load(key); ok {
		return cached
	}
	re := regexp.MustCompile(pattern)
	actual, _ := soapGeneratedPatternCache.LoadOrStore(key, re)
	return actual
}

var (
	_ protocol.CookieController                 = (*SOAP)(nil)
	_ protocol.CookieSetter                     = (*SOAP)(nil)
	_ protocol.CacheController                  = (*SOAP)(nil)
	_ protocol.ConnectionController             = (*SOAP)(nil)
	_ protocol.VerificationController           = (*SOAP)(nil)
	_ protocol.VerificationSnapshotter          = (*SOAP)(nil)
	_ protocol.ParallelSafetyProvider           = (*SOAP)(nil)
	_ protocol.SharedSessionConcurrencyProvider = (*SOAP)(nil)
)

func (s *SOAP) Name() string { return "soap" }

func (s *SOAP) ParallelSafe() bool {
	if s != nil && s.http != nil {
		return s.http.ParallelSafe()
	}
	return (*httpproto.HTTP)(nil).ParallelSafe()
}

func (s *SOAP) SharedSessionConcurrentSafe() bool {
	return s != nil && s.http != nil && s.http.SharedSessionConcurrentSafe()
}

func (s *SOAP) ValidateSharedSessionAction(action *protocol.Action) error {
	if !s.SharedSessionConcurrentSafe() {
		return fmt.Errorf("HTTP session is not initialized")
	}
	if action == nil {
		return fmt.Errorf("soap action is required")
	}
	prepared := *action
	prepared.Method = stdhttp.MethodPost
	prepared.Headers = cloneHeaders(action.Headers)
	preparedBody, _ := shapeSOAPBody(action, s.version, s.namespaces)
	prepared.Body = s.applySecurity(preparedBody)
	s.applyDefaults(&prepared)
	return s.http.ValidateSharedSessionAction(&prepared)
}

func (s *SOAP) Init(ctx context.Context, config map[string]any) error {
	s.unredactedDebugSnapshots, _ = config[protocol.ReplayUnredactedDebugConfigKey].(bool)
	if s.now == nil {
		s.now = time.Now
	}
	if s.nonceGenerator == nil {
		s.nonceGenerator = defaultNonceGenerator
	}
	s.version = "1.1"
	if version, ok := config["soap_version"].(string); ok && strings.TrimSpace(version) != "" {
		s.version = strings.TrimSpace(version)
	}
	s.failOnFault, _ = config["soap_fault_fail_on_fault"].(bool)
	s.defaultCharset = "utf-8"
	if baseURL, ok := config["base_url"].(string); ok {
		s.baseURL = strings.TrimSpace(baseURL)
	}
	s.namespaces = readNamespaces(config)
	s.security = readSecurityConfig(config)
	s.http = &httpproto.HTTP{}
	s.http.SetRuntimeAuthoredTextRedactor(s.authoredRedactor)
	return s.http.Init(ctx, config)
}

func (s *SOAP) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	if action == nil {
		return &protocol.Result{
			StartTime:    time.Now(),
			ErrorMessage: "soap action is required",
		}, nil
	}

	prepared := *action
	prepared.Method = stdhttp.MethodPost
	prepared.Headers = cloneHeaders(action.Headers)
	prepared.RedactedTarget = action.RedactedTarget
	prepared.RedactedHeaders = cloneHeaders(action.RedactedHeaders)
	prepared.RedactedSOAP = action.RedactedSOAP
	// SOAP fault handling is an application-layer concern. Keep successful
	// response bytes through the HTTP adapter so a 2xx SOAP Fault cannot be
	// mistaken for a passing sample, then re-apply the caller's retention policy
	// after fault detection and protocol evidence have been built.
	discardSuccessfulBody := prepared.DiscardResponseBodyOnSuccess
	prepared.DiscardResponseBodyOnSuccess = false
	preparedBody, namespaceConflict := shapeSOAPBody(action, s.version, s.namespaces)
	// Build the WS-Security header once per request so the evidence/redacted
	// body carries the same Created/Expires (and Nonce, unless it belongs to a
	// secret-store digest) the server actually received.
	// The decision is by origin, never by shape: a script-sourced password is
	// not a secret and stays in the evidence header (it is redacted from run
	// evidence only through the engine's gated imported-credential matcher).
	// A password resolved from the secret store has its Password element value
	// (PasswordText, or a PasswordDigest the exact-value secret matcher cannot
	// recognize) replaced in every evidence copy, including unredacted debug;
	// a PasswordDigest's Nonce is withheld with it, Created is kept. The wire
	// body is never altered.
	securityXML, evidenceSecurityXML := s.securityHeaders()
	prepared.Body = insertSOAPSecurityHeader(preparedBody, securityXML)
	s.applyDefaults(&prepared)
	if len(prepared.RedactedBody) == 0 {
		redactedPrepared := *action
		redactedPrepared.Method = stdhttp.MethodPost
		redactedPrepared.Headers = cloneHeaders(action.Headers)
		if len(action.RedactedHeaders) > 0 {
			redactedPrepared.Headers = cloneHeaders(action.RedactedHeaders)
		}
		redactedPrepared.SOAP = action.SOAP
		if action.RedactedSOAP != nil {
			redactedPrepared.SOAP = action.RedactedSOAP
		}
		redactedPreparedBody, _ := shapeSOAPBody(&redactedPrepared, s.version, s.namespaces)
		redactedPreparedBody = insertSOAPSecurityHeader(redactedPreparedBody, evidenceSecurityXML)
		redactedPrepared.Body = redactedPreparedBody
		s.applyDefaults(&redactedPrepared)
		prepared.RedactedBody = redactedPreparedBody
		prepared.RedactedHeaders = cloneHeaders(redactedPrepared.Headers)
	} else {
		prepared.RedactedBody = insertSOAPSecurityHeader(prepared.RedactedBody, evidenceSecurityXML)
	}
	// Evidence copies of the wire body are rebuilt from the shaped body with
	// the evidence header rather than derived by text matching, so a
	// secret-store password is withheld regardless of its bytes. Any other
	// copy (for example a truncated or re-encoded HAR body) that carries
	// neither the wire header verbatim nor the evidence header is withheld
	// entirely: fail closed.
	wireBodyText := string(prepared.Body)
	evidenceWireBodyText := string(insertSOAPSecurityHeader(preparedBody, evidenceSecurityXML))
	maskWireSecurity := func(text string) string {
		if evidenceSecurityXML == securityXML || securityXML == "" || text == "" {
			return text
		}
		if text == wireBodyText {
			return evidenceWireBodyText
		}
		if strings.Contains(text, securityXML) {
			return strings.ReplaceAll(text, securityXML, evidenceSecurityXML)
		}
		if strings.Contains(text, evidenceSecurityXML) || text == string(prepared.RedactedBody) {
			// Already an evidence copy (the redacted snapshot body).
			return text
		}
		return ""
	}
	targetURL := resolveSOAPURL(s.baseURL, prepared.Target)
	envelopeOverheadKB := soapEnvelopeOverheadKB(action, prepared.Body)

	result, err := s.http.Execute(ctx, &prepared)
	if err != nil || result == nil {
		return result, err
	}
	// The HTTP adapter's unredacted debug snapshot and replay HAR copy the
	// wire body; withhold a secret-store password there too.
	if result.Snapshot != nil {
		result.Snapshot.RequestBody = maskWireSecurity(result.Snapshot.RequestBody)
	}
	for i := range result.NetworkEntries {
		if har := result.NetworkEntries[i].HAR; har != nil && har.RequestBody != nil {
			har.RequestBody.Text = maskWireSecurity(har.RequestBody.Text)
		}
	}
	result.DependencyType = "soap"
	storeExtraction := func(key, value string) bool {
		if storeErr := protocol.StoreExtraction(result, key, value); storeErr != nil {
			result.Success = false
			result.Extractions = nil
			result.ErrorMessage = fmt.Sprintf("SOAP metadata extraction %q rejected: %v", key, storeErr)
			return false
		}
		return true
	}
	if !storeExtraction("soap_version", s.version) || !storeExtraction("soap_endpoint", targetURL) {
		return result, nil
	}
	if envelopeOverheadKB > 0 {
		if result.ServerTelemetry == nil {
			result.ServerTelemetry = &protocol.ServerTelemetry{}
		}
		result.ServerTelemetry.EnvelopeOverheadKB = envelopeOverheadKB
	}
	if namespaceConflict != "" {
		if result.ServerTelemetry == nil {
			result.ServerTelemetry = &protocol.ServerTelemetry{}
		}
		result.ServerTelemetry.SOAPNamespaceConflict = namespaceConflict
		if !storeExtraction("soap_namespace_conflict", namespaceConflict) {
			return result, nil
		}
	}
	if operation := resolveSOAPOperation(action, prepared.Body); operation != "" {
		result.DependencyTarget = appendSOAPOperationTarget(result.DependencyTarget, operation)
		if !storeExtraction("soap_operation", operation) {
			return result, nil
		}
	}

	if faultCode, faultCategory, fault, ok := detectSOAPFault(result.ResponseBody); ok {
		if result.ServerTelemetry == nil {
			result.ServerTelemetry = &protocol.ServerTelemetry{}
		}
		result.ServerTelemetry.SOAPFaultCode = faultCode
		result.ServerTelemetry.SOAPFaultCategory = faultCategory
		if !storeExtraction("soap_fault_code", faultCode) || !storeExtraction("soap_fault_category", faultCategory) {
			return result, nil
		}
		if s.failOnFault {
			result.Success = false
			if result.ErrorMessage == "" {
				result.ErrorMessage = fault
			}
			if result.Snapshot == nil {
				result.Snapshot = buildSOAPSnapshot(&prepared, targetURL, result, s.unredactedDebugSnapshots)
				result.Snapshot.RequestBody = maskWireSecurity(result.Snapshot.RequestBody)
			}
		}
	} else if s.failOnFault && result.StatusCode >= 500 && result.ErrorMessage == "" {
		result.ErrorMessage = "soap fault response"
	}
	evidenceAction := prepared
	if s.unredactedDebugSnapshots {
		evidenceAction.RedactedTarget = ""
		evidenceAction.RedactedHeaders = nil
		evidenceAction.RedactedParams = nil
		evidenceAction.RedactedSOAP = nil
		evidenceAction.RedactedBody = []byte(maskWireSecurity(string(prepared.Body)))
	}
	result.ProtocolEvidence = protocol.BuildProtocolEvidence(&evidenceAction, result)
	if discardSuccessfulBody && result.Success {
		result.ResponseBody = ""
	}

	return result, nil
}

func soapEnvelopeOverheadKB(action *protocol.Action, preparedBody []byte) float64 {
	preparedText := strings.TrimSpace(string(preparedBody))
	if preparedText != "" && looksLikeSOAPEnvelope(preparedText) {
		innerBody := strings.TrimSpace(firstSOAPTagInnerXML(preparedText, "Body"))
		overheadBytes := len(preparedText) - len(innerBody)
		if overheadBytes > 0 {
			return float64(overheadBytes) / 1024.0
		}
	}
	if action == nil {
		return 0
	}
	payloadBytes := len(action.Body)
	overheadBytes := len(preparedBody) - payloadBytes
	if overheadBytes <= 0 {
		return 0
	}
	return float64(overheadBytes) / 1024.0
}

func (s *SOAP) Teardown(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	return s.http.Teardown(ctx)
}

// ClearCookies / ClearCache delegate to the wrapped HTTP protocol so that
// browser-emulation's "simulate a new user on each iteration" (and the
// cookie_clear action) actually reset SOAP session state — the wrapped HTTP
// instance holds the cookie jar and response cache.
func (s *SOAP) ClearCookies(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	return s.http.ClearCookies(ctx)
}

func (s *SOAP) ClearCache(ctx context.Context) error {
	if s == nil || s.http == nil {
		return nil
	}
	return s.http.ClearCache(ctx)
}

func (s *SOAP) SetCookie(ctx context.Context, cookie protocol.ScriptCookie) error {
	if s == nil || s.http == nil {
		return nil
	}
	return s.http.SetCookie(ctx, cookie)
}

func (s *SOAP) CloseIdleConnections(ctx context.Context) error {
	if s == nil || s.http == nil {
		return nil
	}
	return s.http.CloseIdleConnections(ctx)
}

func (s *SOAP) ConfigureVerification(v protocol.Verification) error {
	if s == nil || s.http == nil {
		return fmt.Errorf("soap protocol is not initialized")
	}
	return s.http.ConfigureVerification(v)
}

func (s *SOAP) VerificationSnapshot() []protocol.Verification {
	if s == nil || s.http == nil {
		return nil
	}
	return s.http.VerificationSnapshot()
}

func (s *SOAP) OAuthHTTPClient(allowedTargetHosts []string) (*stdhttp.Client, error) {
	if s == nil || s.http == nil {
		if len(allowedTargetHosts) > 0 {
			return nil, fmt.Errorf("cannot enforce the CI runtime target policy for uninitialized protocol %T", s)
		}
		return nil, nil
	}
	client := s.http.OAuthHTTPClient()
	if client == nil {
		if len(allowedTargetHosts) > 0 {
			return nil, fmt.Errorf("cannot enforce the CI runtime target policy for uninitialized protocol %T", s)
		}
		return nil, nil
	}
	if len(allowedTargetHosts) > 0 && client.Transport != nil {
		client.Transport = protocol.NewTargetPolicyRoundTripper(client.Transport, allowedTargetHosts)
	}
	return client, nil
}

func (s *SOAP) HealthCheck(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	return s.http.HealthCheck(ctx)
}

func cloneHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return map[string]string{}
	}
	cloned := make(map[string]string, len(headers))
	for key, value := range headers {
		cloned[key] = value
	}
	return cloned
}

func resolveSOAPURL(baseURL, target string) string {
	return httpproto.ResolveURL(baseURL, target)
}

func resolveSOAPOperation(action *protocol.Action, body []byte) string {
	if action == nil {
		return ""
	}
	if action.SOAP != nil {
		if operation := strings.TrimSpace(action.SOAP.Operation); operation != "" {
			return operation
		}
		if operation := soapOperationFromAction(strings.TrimSpace(action.SOAP.Action)); operation != "" {
			return operation
		}
	}
	return soapOperationFromBody(body)
}

func soapOperationFromAction(action string) string {
	if action == "" {
		return ""
	}
	for _, separator := range []string{"#", "/", ":"} {
		if idx := strings.LastIndex(action, separator); idx >= 0 && idx < len(action)-1 {
			return strings.TrimSpace(action[idx+1:])
		}
	}
	return strings.TrimSpace(action)
}

func soapOperationFromBody(body []byte) string {
	text := string(body)
	bodyStart := strings.Index(strings.ToLower(text), "<body")
	if bodyStart == -1 {
		return ""
	}
	segment := text[bodyStart:]
	gt := strings.Index(segment, ">")
	if gt == -1 || gt == len(segment)-1 {
		return ""
	}
	segment = segment[gt+1:]
	start := strings.Index(segment, "<")
	if start == -1 || start == len(segment)-1 {
		return ""
	}
	segment = segment[start+1:]
	end := strings.IndexAny(segment, " >/\t\r\n")
	if end == -1 {
		end = len(segment)
	}
	name := strings.TrimSpace(segment[:end])
	if idx := strings.Index(name, ":"); idx >= 0 && idx < len(name)-1 {
		name = name[idx+1:]
	}
	return name
}

func appendSOAPOperationTarget(target string, operation string) string {
	if operation == "" {
		return target
	}
	if target == "" {
		return operation
	}
	return target + "#" + operation
}

func classifySOAPFaultCategory(fault string) string {
	lower := strings.ToLower(fault)
	switch {
	case strings.Contains(lower, "client") || strings.Contains(lower, "sender"):
		return "client"
	case strings.Contains(lower, "server") || strings.Contains(lower, "receiver"):
		return "server"
	case strings.Contains(lower, "version"):
		return "version"
	case strings.Contains(lower, "mustunderstand"):
		return "mustunderstand"
	default:
		return "unknown"
	}
}

func buildSOAPSnapshot(action *protocol.Action, targetURL string, result *protocol.Result, unredactedDebug ...bool) *protocol.ErrorSnapshot {
	raw := len(unredactedDebug) > 0 && unredactedDebug[0]
	if action == nil || result == nil {
		return nil
	}
	requestURL := targetURL
	if !raw && action.RedactedTarget != "" {
		requestURL = action.RedactedTarget
	}
	headers := cloneHeaders(action.Headers)
	if !raw && len(action.RedactedHeaders) > 0 {
		headers = cloneHeaders(action.RedactedHeaders)
	}
	body := string(action.Body)
	if !raw && len(action.RedactedBody) > 0 {
		body = string(action.RedactedBody)
	}
	return &protocol.ErrorSnapshot{
		RequestMethod:  action.Method,
		RequestURL:     requestURL,
		RequestHeaders: headers,
		RequestBody:    body,
		ResponseStatus: result.StatusCode,
		ResponseBody:   result.ResponseBody,
	}
}

func readSecurityConfig(config map[string]any) soapSecurityConfig {
	security := soapSecurityConfig{}
	if username, ok := config["soap_ws_security_username"].(string); ok {
		security.username = username
	}
	if password, ok := config["soap_ws_security_password"].(string); ok {
		security.password = password
	}
	if passwordType, ok := config["soap_ws_security_password_type"].(string); ok {
		security.passwordType = strings.TrimSpace(passwordType)
	}
	if security.passwordType == "" && (security.username != "" || security.password != "") {
		security.passwordType = "text"
	}
	security.passwordFromSecretStore, _ = config[SecretStorePasswordConfigKey].(bool)
	security.includeNonce, _ = config["soap_ws_security_nonce"].(bool)
	security.includeCreated, _ = config["soap_ws_security_created"].(bool)
	if ttl, ok := config["soap_ws_security_timestamp_ttl_seconds"].(float64); ok {
		security.timestamp = true
		security.timestampTTL = time.Duration(ttl) * time.Second
	}
	if security.timestamp && security.timestampTTL <= 0 {
		security.timestampTTL = 5 * time.Minute
	}
	if security.passwordType == "digest" {
		security.includeNonce = true
		security.includeCreated = true
	}
	return security
}

func readNamespaces(config map[string]any) map[string]string {
	raw, ok := config["soap_namespaces"]
	if !ok {
		return nil
	}
	namespaces, ok := raw.(map[string]string)
	if !ok || len(namespaces) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(namespaces))
	for key, value := range namespaces {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		cloned[key] = value
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

func shapeSOAPBody(action *protocol.Action, version string, namespaces map[string]string) ([]byte, string) {
	if action == nil || action.SOAP == nil {
		if action == nil {
			return nil, ""
		}
		return action.Body, ""
	}
	if strings.TrimSpace(action.SOAP.BodyMode) != "generated" {
		return action.Body, ""
	}

	body := strings.TrimSpace(string(action.Body))
	if body == "" || looksLikeSOAPEnvelope(body) {
		return action.Body, ""
	}

	envelopePrefix, allNamespaces, namespaceConflict := mergeSOAPNamespaces(version, namespaces)
	body = wrapSOAPOperation(body, action.SOAP.Operation)
	if bodyConflict := detectSOAPBodyNamespaceConflict(body, allNamespaces); bodyConflict != "" {
		namespaceConflict = joinSOAPNamespaceConflicts(namespaceConflict, bodyConflict)
	}

	var attrs []string
	keys := make([]string, 0, len(allNamespaces))
	for key := range allNamespaces {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		attrs = append(attrs, fmt.Sprintf(`xmlns:%s="%s"`, key, allNamespaces[key]))
	}

	envelope := fmt.Sprintf(
		`<%[1]s:Envelope %[2]s><%[1]s:Body>%[3]s</%[1]s:Body></%[1]s:Envelope>`,
		envelopePrefix,
		strings.Join(attrs, " "),
		body,
	)
	return []byte(envelope), namespaceConflict
}

func (s *SOAP) applySecurity(body []byte) []byte {
	wire, _ := s.securityHeaders()
	return insertSOAPSecurityHeader(body, wire)
}

// securityHeaders builds one WS-Security header (fresh Nonce/Created) for the
// wire and its evidence copy, or "" for both when security is disabled or the
// header cannot be built. The evidence copy equals the wire header except that
// a secret-store password's Password element value is withheld.
func (s *SOAP) securityHeaders() (wire, evidence string) {
	if !s.security.enabled() {
		return "", ""
	}
	wire, evidence, err := buildSecurityHeaders(s.security, s.now().UTC(), s.nonceGenerator)
	if err != nil {
		return "", ""
	}
	return wire, evidence
}

// insertSOAPSecurityHeader places a prebuilt WS-Security header into a SOAP
// envelope body. Non-envelope bodies and an empty header are returned as-is.
func insertSOAPSecurityHeader(body []byte, securityXML string) []byte {
	if securityXML == "" {
		return body
	}
	bodyStr := strings.TrimSpace(string(body))
	if bodyStr == "" || !looksLikeSOAPEnvelope(bodyStr) {
		return body
	}
	prefix := detectEnvelopePrefix(bodyStr)
	if prefix == "" {
		prefix = "soapenv"
	}

	quotedPrefix := regexp.QuoteMeta(prefix)
	headerClosePattern := compileGeneratedSOAPPattern("header-close:"+prefix, fmt.Sprintf(`(?is)</%s:Header\s*>`, quotedPrefix))
	// The header is spliced in literally: a regexp replacement template would
	// expand '$' sequences inside a password (or any other header text) and
	// put a different value on the wire than the one configured.
	if headerClosePattern.MatchString(bodyStr) {
		return []byte(headerClosePattern.ReplaceAllStringFunc(bodyStr, func(match string) string {
			return securityXML + match
		}))
	}
	selfClosingHeaderPattern := compileGeneratedSOAPPattern("header-self-close:"+prefix, fmt.Sprintf(`(?is)<%s:Header\b([^>]*)/>`, quotedPrefix))
	if selfClosingHeaderPattern.MatchString(bodyStr) {
		return []byte(selfClosingHeaderPattern.ReplaceAllStringFunc(bodyStr, func(match string) string {
			attrs := ""
			if sub := selfClosingHeaderPattern.FindStringSubmatch(match); len(sub) > 1 {
				attrs = sub[1]
			}
			return "<" + prefix + ":Header" + attrs + ">" + securityXML + "</" + prefix + ":Header>"
		}))
	}
	bodyOpenPattern := compileGeneratedSOAPPattern("body-open:"+prefix, fmt.Sprintf(`(?is)<%s:Body\b[^>]*>`, quotedPrefix))
	if bodyOpen := bodyOpenPattern.FindString(bodyStr); bodyOpen != "" {
		header := fmt.Sprintf("<%s:Header>%s</%s:Header>", prefix, securityXML, prefix)
		return []byte(strings.Replace(bodyStr, bodyOpen, header+bodyOpen, 1))
	}
	return body
}

func looksLikeSOAPEnvelope(body string) bool {
	trimmed := strings.TrimSpace(body)
	return strings.Contains(trimmed, ":Envelope") || strings.Contains(trimmed, "<Envelope")
}

func detectEnvelopePrefix(body string) string {
	matches := envelopePrefixPattern.FindStringSubmatch(body)
	if len(matches) == 2 {
		return matches[1]
	}
	return ""
}

func mergeSOAPNamespaces(version string, namespaces map[string]string) (string, map[string]string, string) {
	allNamespaces := make(map[string]string, len(namespaces)+1)
	for key, value := range namespaces {
		allNamespaces[key] = value
	}
	envelopeURI := soapEnvelopeURI(version)
	namespaceConflict := ""
	if configuredURI, ok := allNamespaces["soapenv"]; ok && configuredURI != envelopeURI {
		namespaceConflict = fmt.Sprintf("prefix soapenv already mapped to %s", configuredURI)
	}
	for key, value := range allNamespaces {
		if value == envelopeURI {
			return key, allNamespaces, namespaceConflict
		}
	}
	allNamespaces["soapenv"] = envelopeURI
	return "soapenv", allNamespaces, namespaceConflict
}

func detectSOAPBodyNamespaceConflict(body string, namespaces map[string]string) string {
	declared := map[string]struct{}{"xml": {}}
	for key := range namespaces {
		declared[key] = struct{}{}
	}
	for _, match := range soapNamespaceDeclPattern.FindAllStringSubmatch(body, -1) {
		if len(match) == 2 {
			declared[match[1]] = struct{}{}
		}
	}

	missing := make(map[string]struct{})
	for _, match := range soapElementPrefixPattern.FindAllStringSubmatch(body, -1) {
		if len(match) == 2 {
			prefix := match[1]
			if _, ok := declared[prefix]; !ok {
				missing[prefix] = struct{}{}
			}
		}
	}
	for _, match := range soapAttributePrefixPattern.FindAllStringSubmatch(body, -1) {
		if len(match) == 2 {
			prefix := match[1]
			if prefix == "xmlns" {
				continue
			}
			if _, ok := declared[prefix]; !ok {
				missing[prefix] = struct{}{}
			}
		}
	}
	if len(missing) == 0 {
		return ""
	}
	prefixes := make([]string, 0, len(missing))
	for prefix := range missing {
		prefixes = append(prefixes, prefix)
	}
	sort.Strings(prefixes)
	return "undeclared prefixes: " + strings.Join(prefixes, ", ")
}

func joinSOAPNamespaceConflicts(existing string, next string) string {
	if existing == "" {
		return next
	}
	if next == "" {
		return existing
	}
	return existing + "; " + next
}

func soapEnvelopeURI(version string) string {
	if version == "1.2" {
		return "http://www.w3.org/2003/05/soap-envelope"
	}
	return "http://schemas.xmlsoap.org/soap/envelope/"
}

func wrapSOAPOperation(body, operation string) string {
	operation = strings.TrimSpace(operation)
	if operation == "" {
		return body
	}
	if strings.Contains(body, "<"+operation) || strings.Contains(body, ":"+operation) {
		return body
	}
	return fmt.Sprintf("<%s>%s</%s>", operation, body, operation)
}

func buildSecurityHeaderXML(security soapSecurityConfig, now time.Time, nonceGenerator func() ([]byte, error)) (string, error) {
	wire, _, err := buildSecurityHeaders(security, now, nonceGenerator)
	return wire, err
}

// buildSecurityHeaders returns the wire header and its evidence copy, built
// from the same Nonce/Created values (a secret-store PasswordDigest's Nonce is
// withheld in the evidence copy).
func buildSecurityHeaders(security soapSecurityConfig, now time.Time, nonceGenerator func() ([]byte, error)) (string, string, error) {
	parts := make([]string, 0, 2)
	evidenceParts := make([]string, 0, 2)
	created := now.UTC().Format(time.RFC3339)

	if security.timestamp {
		expires := now.UTC().Add(security.timestampTTL).Format(time.RFC3339)
		timestamp := `<wsu:Timestamp>` +
			`<wsu:Created>` + xmlEscape(created) + `</wsu:Created>` +
			`<wsu:Expires>` + xmlEscape(expires) + `</wsu:Expires>` +
			`</wsu:Timestamp>`
		parts = append(parts, timestamp)
		evidenceParts = append(evidenceParts, timestamp)
	}

	if security.username != "" || security.password != "" {
		nonceBytes := []byte(nil)
		nonceText := ""
		if security.includeNonce {
			var err error
			nonceBytes, err = nonceGenerator()
			if err != nil {
				return "", "", err
			}
			nonceText = base64.StdEncoding.EncodeToString(nonceBytes)
		}

		passwordValue, passwordURI := security.passwordValue(now, created, nonceBytes)
		usernameToken := func(password, nonce string) string {
			token := `<wsse:UsernameToken>` +
				`<wsse:Username>` + xmlEscape(security.username) + `</wsse:Username>` +
				`<wsse:Password Type="` + passwordURI + `">` + password + `</wsse:Password>`
			if security.includeNonce {
				token += `<wsse:Nonce>` + nonce + `</wsse:Nonce>`
			}
			if security.includeCreated {
				token += `<wsu:Created>` + xmlEscape(created) + `</wsu:Created>`
			}
			return token + `</wsse:UsernameToken>`
		}
		wireToken := usernameToken(xmlEscape(passwordValue), nonceText)
		parts = append(parts, wireToken)
		if security.passwordFromSecretStore && passwordValue != "" {
			// The adapter knows the origin, so it withholds by origin: the
			// Password element, and for a PasswordDigest also the Nonce it
			// was computed from. The engine never infers origin from the
			// placeholder's shape, so an authored "[REDACTED]" digest keeps
			// its script-sourced nonce.
			evidenceNonce := nonceText
			if security.passwordType == "digest" {
				evidenceNonce = redactedSOAPPassword
			}
			evidenceParts = append(evidenceParts, usernameToken(redactedSOAPPassword, evidenceNonce))
		} else {
			evidenceParts = append(evidenceParts, wireToken)
		}
	}

	if len(parts) == 0 {
		return "", "", nil
	}

	const open = `<wsse:Security xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd" xmlns:wsu="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">`
	return open + strings.Join(parts, "") + `</wsse:Security>`, open + strings.Join(evidenceParts, "") + `</wsse:Security>`, nil
}

func (security soapSecurityConfig) enabled() bool {
	return security.timestamp || security.username != "" || security.password != ""
}

func (security soapSecurityConfig) passwordValue(now time.Time, created string, nonce []byte) (string, string) {
	if security.passwordType == "digest" {
		hash := sha1.Sum(append(append(nonce, []byte(created)...), []byte(security.password)...))
		return base64.StdEncoding.EncodeToString(hash[:]), "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest"
	}
	return security.password, "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordText"
}

func defaultNonceGenerator() ([]byte, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return nonce, nil
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(value)
}

func detectSOAPFault(body string) (string, string, string, bool) {
	if !looksLikeSOAPEnvelope(body) {
		return "", "", "", false
	}
	faultXML := firstSOAPTagInnerXML(body, "Fault")
	if strings.TrimSpace(faultXML) == "" {
		return "", "", "", false
	}
	reason := firstSOAPTagText(faultXML, "faultstring")
	if reason == "" {
		reason = firstSOAPTagText(faultXML, "Text")
	}
	code := firstSOAPTagText(faultXML, "faultcode")
	if code == "" {
		code = firstSOAPTagText(faultXML, "Value")
	}
	message := "soap fault"
	switch {
	case code != "" && reason != "":
		message = fmt.Sprintf("soap fault: %s: %s", code, reason)
	case reason != "":
		message = fmt.Sprintf("soap fault: %s", reason)
	case code != "":
		message = fmt.Sprintf("soap fault: %s", code)
	}
	return code, classifySOAPFaultCategory(code), message, true
}

func firstSOAPTagText(body, tag string) string {
	inner := firstSOAPTagInnerXML(body, tag)
	if inner == "" {
		return ""
	}
	text := strings.TrimSpace(stripXMLTags(inner))
	if text == "" {
		return ""
	}
	return html.UnescapeString(text)
}

func firstSOAPTagInnerXML(body, tag string) string {
	pattern := fmt.Sprintf(`(?is)<(?:[A-Za-z_][A-Za-z0-9_.-]*:)?%s\b[^>]*>(.*?)</(?:[A-Za-z_][A-Za-z0-9_.-]*:)?%s>`, regexp.QuoteMeta(tag), regexp.QuoteMeta(tag))
	re := compileGeneratedSOAPPattern("tag-inner:"+tag, pattern)
	matches := re.FindStringSubmatch(body)
	if len(matches) != 2 {
		return ""
	}
	return matches[1]
}

func stripXMLTags(value string) string {
	var builder strings.Builder
	inTag := false
	for _, r := range value {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				builder.WriteRune(r)
			}
		}
	}
	return builder.String()
}

func (s *SOAP) applyDefaults(action *protocol.Action) {
	var defaultHeaders map[string]string
	var defaultHeaderList protocol.HeaderList
	if s != nil && s.http != nil {
		defaultHeaders, defaultHeaderList = s.http.EffectiveDefaultHeaders(action)
	} else if action != nil && action.ResolvedDefaultHeaders {
		defaultHeaders = action.DefaultHeaders
		defaultHeaderList = action.DefaultHeaderList
	}
	version := "1.1"
	charset := "utf-8"
	if s != nil {
		if s.version != "" {
			version = s.version
		}
		if s.defaultCharset != "" {
			charset = s.defaultCharset
		}
	}
	applySOAPDefaultsWithDefaultHeaders(action, version, charset, defaultHeaders, defaultHeaderList)
}

func applySOAPDefaults(action *protocol.Action, version, charset string) {
	var defaultHeaders map[string]string
	var defaultHeaderList protocol.HeaderList
	if action != nil && action.ResolvedDefaultHeaders {
		defaultHeaders = action.DefaultHeaders
		defaultHeaderList = action.DefaultHeaderList
	}
	applySOAPDefaultsWithDefaultHeaders(action, version, charset, defaultHeaders, defaultHeaderList)
}

func applySOAPDefaultsWithDefaultHeaders(action *protocol.Action, version, charset string, defaultHeaders map[string]string, defaultHeaderList protocol.HeaderList) {
	if action == nil {
		return
	}
	if action.Headers == nil {
		action.Headers = make(map[string]string)
	}

	soapAction := ""
	if action.SOAP != nil {
		soapAction = strings.TrimSpace(action.SOAP.Action)
	}

	if !hasConfiguredHeader("Content-Type", action.Headers, action.HeaderList, defaultHeaders, defaultHeaderList) {
		setHeaderCanonical(action.Headers, "Content-Type", defaultSOAPContentType(version, charset, soapAction))
	}
	if version == "1.1" {
		if soapAction == "" {
			if existing, ok := getHeaderValue(action.Headers, "SOAPAction"); ok {
				soapAction = strings.Trim(existing, `"`)
			}
		}
		setHeaderCanonical(action.Headers, "SOAPAction", quoteSOAPAction(soapAction))
	} else {
		deleteHeaderCanonical(action.Headers, "SOAPAction")
	}
	if !hasConfiguredHeader("Accept", action.Headers, action.HeaderList, defaultHeaders, defaultHeaderList) {
		setHeaderCanonical(action.Headers, "Accept", "text/xml, application/soap+xml")
	}
}

func hasConfiguredHeader(name string, actionHeaders map[string]string, actionHeaderList protocol.HeaderList, defaultHeaders map[string]string, defaultHeaderList protocol.HeaderList) bool {
	if hasEnabledHeaderInList(actionHeaderList, name) {
		return true
	}
	if _, ok := getHeaderValue(actionHeaders, name); ok {
		return true
	}
	if hasEnabledHeaderInList(defaultHeaderList, name) {
		return true
	}
	if !hasAnyHeaderInList(defaultHeaderList, name) {
		if _, ok := getHeaderValue(defaultHeaders, name); ok {
			return true
		}
	}
	return false
}

func hasEnabledHeaderInList(headers protocol.HeaderList, name string) bool {
	for _, entry := range headers {
		if entry.Enabled != nil && !*entry.Enabled {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(entry.Name), name) {
			return true
		}
	}
	return false
}

func hasAnyHeaderInList(headers protocol.HeaderList, name string) bool {
	for _, entry := range headers {
		if strings.EqualFold(strings.TrimSpace(entry.Name), name) {
			return true
		}
	}
	return false
}

func quoteSOAPAction(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"`)
	return `"` + value + `"`
}

func getHeaderValue(headers map[string]string, name string) (string, bool) {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value, true
		}
	}
	return "", false
}

func setHeaderCanonical(headers map[string]string, name string, value string) {
	for key := range headers {
		if strings.EqualFold(key, name) && key != name {
			delete(headers, key)
		}
	}
	headers[name] = value
}

func deleteHeaderCanonical(headers map[string]string, name string) {
	for key := range headers {
		if strings.EqualFold(key, name) {
			delete(headers, key)
		}
	}
}

func defaultSOAPContentType(version, charset, soapAction string) string {
	if version == "1.2" {
		contentType := "application/soap+xml"
		if charset != "" {
			contentType += "; charset=" + charset
		}
		if soapAction != "" {
			contentType += "; action=\"" + soapAction + "\""
		}
		return contentType
	}

	contentType := "text/xml"
	if charset != "" {
		contentType += "; charset=" + charset
	}
	return contentType
}

// BeginIteration delegates cache aging to the shared HTTP runtime.
func (s *SOAP) BeginIteration(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	return s.http.BeginIteration(ctx)
}
