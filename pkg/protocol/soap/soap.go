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

	"github.com/jasonczech/apexload/pkg/protocol"
	httpproto "github.com/jasonczech/apexload/pkg/protocol/http"
)

func init() {
	protocol.Register("soap", func() protocol.Protocol {
		return &SOAP{}
	})
}

type SOAP struct {
	http           *httpproto.HTTP
	baseURL        string
	version        string
	failOnFault    bool
	defaultCharset string
	namespaces     map[string]string
	security       soapSecurityConfig
	now            func() time.Time
	nonceGenerator func() ([]byte, error)
}

type soapSecurityConfig struct {
	username       string
	password       string
	passwordType   string
	includeNonce   bool
	includeCreated bool
	timestamp      bool
	timestampTTL   time.Duration
}

var envelopePrefixPattern = regexp.MustCompile(`<([A-Za-z_][A-Za-z0-9_.-]*):Envelope\b`)
var soapNamespaceDeclPattern = regexp.MustCompile(`xmlns:([A-Za-z_][A-Za-z0-9_.-]*)\s*=\s*['"][^'"]+['"]`)
var soapElementPrefixPattern = regexp.MustCompile(`</?([A-Za-z_][A-Za-z0-9_.-]*):[A-Za-z_][A-Za-z0-9_.-]*\b`)
var soapAttributePrefixPattern = regexp.MustCompile(`\s([A-Za-z_][A-Za-z0-9_.-]*):[A-Za-z_][A-Za-z0-9_.-]*\s*=`)

func (s *SOAP) Name() string { return "soap" }

func (s *SOAP) Init(ctx context.Context, config map[string]any) error {
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
	preparedBody, namespaceConflict := shapeSOAPBody(action, s.version, s.namespaces)
	prepared.Body = preparedBody
	prepared.Body = s.applySecurity(prepared.Body)
	applySOAPDefaults(&prepared, s.version, s.defaultCharset)
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
		redactedPrepared.Body = redactedPreparedBody
		applySOAPDefaults(&redactedPrepared, s.version, s.defaultCharset)
		prepared.RedactedBody = redactedPreparedBody
		prepared.RedactedHeaders = cloneHeaders(redactedPrepared.Headers)
	}
	targetURL := resolveSOAPURL(s.baseURL, prepared.Target)
	envelopeOverheadKB := soapEnvelopeOverheadKB(action, prepared.Body)

	result, err := s.http.Execute(ctx, &prepared)
	if err != nil || result == nil {
		return result, err
	}
	result.DependencyType = "soap"
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
		result.Extractions["soap_namespace_conflict"] = namespaceConflict
	}
	if operation := resolveSOAPOperation(action, prepared.Body); operation != "" {
		result.DependencyTarget = appendSOAPOperationTarget(result.DependencyTarget, operation)
		result.Extractions["soap_operation"] = operation
	}

	if faultCode, faultCategory, fault, ok := detectSOAPFault(result.ResponseBody); ok {
		if result.ServerTelemetry == nil {
			result.ServerTelemetry = &protocol.ServerTelemetry{}
		}
		result.ServerTelemetry.SOAPFaultCode = faultCode
		result.ServerTelemetry.SOAPFaultCategory = faultCategory
		result.Extractions["soap_fault_code"] = faultCode
		result.Extractions["soap_fault_category"] = faultCategory
		if s.failOnFault {
			result.Success = false
			if result.ErrorMessage == "" {
				result.ErrorMessage = fault
			}
			if result.Snapshot == nil {
				result.Snapshot = buildSOAPSnapshot(&prepared, targetURL, result)
			}
		}
	} else if s.failOnFault && result.StatusCode >= 500 && result.ErrorMessage == "" {
		result.ErrorMessage = "soap fault response"
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
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return target
	}
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(target, "/")
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

func buildSOAPSnapshot(action *protocol.Action, targetURL string, result *protocol.Result) *protocol.ErrorSnapshot {
	if action == nil || result == nil {
		return nil
	}
	requestURL := targetURL
	if action.RedactedTarget != "" {
		requestURL = action.RedactedTarget
	}
	headers := cloneHeaders(action.Headers)
	if len(action.RedactedHeaders) > 0 {
		headers = cloneHeaders(action.RedactedHeaders)
	}
	body := string(action.Body)
	if len(action.RedactedBody) > 0 {
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
	security.includeNonce, _ = config["soap_ws_security_nonce"].(bool)
	security.includeCreated, _ = config["soap_ws_security_created"].(bool)
	if ttl, ok := config["soap_ws_security_timestamp_ttl_seconds"].(float64); ok {
		security.timestamp = true
		security.timestampTTL = time.Duration(ttl) * time.Second
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
	if !s.security.enabled() {
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
	securityXML, err := buildSecurityHeaderXML(s.security, s.now().UTC(), s.nonceGenerator)
	if err != nil || securityXML == "" {
		return body
	}

	headerOpen := fmt.Sprintf("<%s:Header>", prefix)
	headerClose := fmt.Sprintf("</%s:Header>", prefix)
	bodyOpen := fmt.Sprintf("<%s:Body>", prefix)

	if strings.Contains(bodyStr, headerOpen) && strings.Contains(bodyStr, headerClose) {
		return []byte(strings.Replace(bodyStr, headerClose, securityXML+headerClose, 1))
	}
	if strings.Contains(bodyStr, bodyOpen) {
		replacement := headerOpen + securityXML + headerClose + bodyOpen
		return []byte(strings.Replace(bodyStr, bodyOpen, replacement, 1))
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
	parts := make([]string, 0, 2)
	created := now.UTC().Format(time.RFC3339)

	if security.timestamp {
		expires := now.UTC().Add(security.timestampTTL).Format(time.RFC3339)
		parts = append(parts,
			`<wsu:Timestamp>`+
				`<wsu:Created>`+xmlEscape(created)+`</wsu:Created>`+
				`<wsu:Expires>`+xmlEscape(expires)+`</wsu:Expires>`+
				`</wsu:Timestamp>`,
		)
	}

	if security.username != "" || security.password != "" {
		nonceBytes := []byte(nil)
		nonceText := ""
		if security.includeNonce {
			var err error
			nonceBytes, err = nonceGenerator()
			if err != nil {
				return "", err
			}
			nonceText = base64.StdEncoding.EncodeToString(nonceBytes)
		}

		passwordValue, passwordURI := security.passwordValue(now, created, nonceBytes)
		usernameToken := `<wsse:UsernameToken>` +
			`<wsse:Username>` + xmlEscape(security.username) + `</wsse:Username>` +
			`<wsse:Password Type="` + passwordURI + `">` + xmlEscape(passwordValue) + `</wsse:Password>`
		if security.includeNonce {
			usernameToken += `<wsse:Nonce>` + nonceText + `</wsse:Nonce>`
		}
		if security.includeCreated {
			usernameToken += `<wsu:Created>` + xmlEscape(created) + `</wsu:Created>`
		}
		usernameToken += `</wsse:UsernameToken>`
		parts = append(parts, usernameToken)
	}

	if len(parts) == 0 {
		return "", nil
	}

	return `<wsse:Security xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd" xmlns:wsu="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">` + strings.Join(parts, "") + `</wsse:Security>`, nil
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
	reason := firstSOAPTagText(body, "faultstring")
	if reason == "" {
		reason = firstSOAPTagText(body, "Text")
	}
	code := firstSOAPTagText(body, "faultcode")
	if code == "" {
		code = firstSOAPTagText(body, "Value")
	}
	if reason == "" && code == "" {
		return "", "", "", false
	}
	message := "soap fault"
	if code != "" && reason != "" {
		message = fmt.Sprintf("soap fault: %s: %s", code, reason)
	} else if reason != "" {
		message = fmt.Sprintf("soap fault: %s", reason)
	} else {
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
	re := regexp.MustCompile(pattern)
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

func applySOAPDefaults(action *protocol.Action, version, charset string) {
	if action.Headers == nil {
		action.Headers = make(map[string]string)
	}

	soapAction := ""
	if action.SOAP != nil {
		soapAction = strings.TrimSpace(action.SOAP.Action)
	}

	if _, exists := action.Headers["Content-Type"]; !exists {
		action.Headers["Content-Type"] = defaultSOAPContentType(version, charset, soapAction)
	}
	if version == "1.1" && soapAction != "" {
		if _, exists := action.Headers["SOAPAction"]; !exists {
			action.Headers["SOAPAction"] = soapAction
		}
	}
	if _, exists := action.Headers["Accept"]; !exists {
		action.Headers["Accept"] = "text/xml, application/soap+xml"
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
