package enterprisearchive

// protocol_soap.go — split from converter.go (same package, behavior-preserving).

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func detectImportProtocol(functions []ParsedFunction) (string, *dsl.SOAPConfig, string) {
	requestCount := 0
	soapCount := 0
	soapVersion := ""

	for _, fn := range functions {
		if !isRequestFunction(fn.Name) {
			continue
		}
		requestCount++
		if !isSOAPFunction(fn) {
			continue
		}
		soapCount++
		if version := detectSOAPVersion(fn); version == "1.2" {
			soapVersion = "1.2"
		} else if soapVersion == "" {
			soapVersion = "1.1"
		}
	}

	if requestCount == 0 || soapCount == 0 {
		return "http", nil, ""
	}
	if soapCount != requestCount {
		return "http", nil, fmt.Sprintf("detected %d SOAP-style requests and %d non-SOAP requests; keeping HTTP protocol because the imported script is mixed", soapCount, requestCount-soapCount)
	}
	if soapVersion == "" {
		soapVersion = "1.1"
	}
	return "soap", &dsl.SOAPConfig{
		Version: soapVersion,
		FaultHandling: dsl.SOAPFaultHandlingConfig{
			FailOnFault: true,
		},
	}, ""
}

func isRequestFunction(name string) bool {
	switch name {
	case "web_url", "web_submit_data", "web_submit_form", "web_custom_request", "web_rest", "web_service_call":
		return true
	default:
		return false
	}
}

func isSOAPFunction(fn ParsedFunction) bool {
	if fn.Name == "web_service_call" {
		return true
	}
	if strings.TrimSpace(firstNonEmptyArg(fn, "SOAPAction")) != "" {
		return true
	}
	for key := range fn.Headers {
		if strings.EqualFold(key, "SOAPAction") {
			return true
		}
	}
	contentType := strings.ToLower(strings.TrimSpace(firstHeaderValue(fn.Headers, "Content-Type")))
	if strings.Contains(contentType, "application/soap+xml") {
		return true
	}
	body := strings.ToLower(strings.TrimSpace(fn.Body))
	if strings.Contains(body, ":envelope") || strings.Contains(body, "<envelope") {
		return true
	}
	if strings.Contains(contentType, "text/xml") && body != "" {
		return strings.Contains(body, "soap") || strings.Contains(body, "<envelope")
	}
	return false
}

func detectSOAPVersion(fn ParsedFunction) string {
	contentType := strings.ToLower(strings.TrimSpace(firstHeaderValue(fn.Headers, "Content-Type")))
	if strings.Contains(contentType, "application/soap+xml") {
		return "1.2"
	}
	body := strings.ToLower(strings.TrimSpace(fn.Body))
	if strings.Contains(body, "http://www.w3.org/2003/05/soap-envelope") {
		return "1.2"
	}
	if isSOAPFunction(fn) {
		return "1.1"
	}
	return ""
}

func firstHeaderValue(headers map[string]string, key string) string {
	for headerKey, value := range headers {
		if strings.EqualFold(headerKey, key) {
			return value
		}
	}
	return ""
}

func headerValueCaseInsensitive(headers map[string]string, key string) (string, bool) {
	for headerKey, value := range headers {
		if strings.EqualFold(headerKey, key) {
			return value, true
		}
	}
	return "", false
}

func deleteHeaderCaseInsensitive(headers map[string]string, key string) {
	for headerKey := range headers {
		if strings.EqualFold(headerKey, key) {
			delete(headers, headerKey)
		}
	}
}

func applySOAPImportHints(action *dsl.Action, fn ParsedFunction) {
	if action == nil || !isSOAPFunction(fn) {
		return
	}
	if action.Method == "" || strings.EqualFold(action.Method, "GET") {
		action.Method = "POST"
		action.Type = "POST"
	}
	if action.Metadata == nil {
		action.Metadata = map[string]any{}
	}
	soapAction := strings.TrimSpace(firstNonEmptyArg(fn, "SOAPAction"))
	if soapAction == "" {
		soapAction = strings.TrimSpace(firstHeaderValue(action.Headers, "SOAPAction"))
	}
	soapMethod := strings.TrimSpace(firstNonEmptyArg(fn, "SOAPMethod"))
	service, binding, operation := splitSourceSOAPMethod(soapMethod)
	if soapMethod != "" {
		action.Metadata["soap_method"] = soapMethod
	}
	if operation == "" && soapAction != "" {
		operation = strings.Trim(soapAction, `"`)
	}
	if operation == "" {
		operation = strings.TrimSpace(firstNonEmptyArg(fn, "Operation"))
	}
	if operation == "" {
		operation = action.Name
	}
	if explicitService := strings.TrimSpace(firstNonEmptyArg(fn, "Service")); explicitService != "" {
		service = explicitService
	}
	if service != "" {
		action.Metadata["soap_service"] = service
	}
	if binding != "" {
		action.Metadata["soap_binding"] = binding
	}
	if responseParam := strings.TrimSpace(firstNonEmptyArg(fn, "ResponseParam")); responseParam != "" {
		action.Metadata["soap_response_param"] = responseParam
	}
	generatedBody := false
	if strings.TrimSpace(action.Body) == "" {
		if body := generateSourceSOAPBody(fn, operation); body != "" {
			action.Body = body
			generatedBody = true
			action.Metadata["soap_body_generated"] = true
		}
	}
	bodyMode := "generated"
	if strings.TrimSpace(action.Body) != "" && !generatedBody {
		bodyMode = "raw_xml"
	}
	action.SOAP = &dsl.SOAPActionConfig{
		Action:    strings.Trim(soapAction, `"`),
		Operation: operation,
		BodyMode:  bodyMode,
	}
	if action.Headers == nil {
		action.Headers = make(map[string]string)
	}
	if contentType := firstHeaderValue(action.Headers, "Content-Type"); contentType == "" {
		if detectSOAPVersion(fn) == "1.2" {
			action.Headers["Content-Type"] = "application/soap+xml"
		} else {
			action.Headers["Content-Type"] = "text/xml"
		}
	}
}

func sourceSOAPEndpointPlaceholder(fn ParsedFunction) string {
	service := strings.TrimSpace(firstNonEmptyArg(fn, "Service"))
	if service == "" {
		service, _, _ = splitSourceSOAPMethod(strings.TrimSpace(firstNonEmptyArg(fn, "SOAPMethod")))
	}
	if service == "" {
		service = firstNonEmpty(fn.StepName, "soap_service")
	}
	key := strings.Trim(sanitizeEnvToken(service), "_")
	if key == "" {
		key = "SOAP_SERVICE"
	}
	return "{{env." + key + "_URL}}"
}

func splitSourceSOAPMethod(value string) (service, binding, operation string) {
	parts := strings.Split(value, "|")
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	if len(cleaned) > 0 {
		service = cleaned[0]
	}
	if len(cleaned) > 1 {
		binding = cleaned[1]
	}
	if len(cleaned) > 2 {
		operation = cleaned[len(cleaned)-1]
	} else if len(cleaned) == 1 {
		operation = cleaned[0]
	}
	return service, binding, operation
}

func generateSourceSOAPBody(fn ParsedFunction, operation string) string {
	operation = sanitizeXMLName(firstNonEmpty(operation, fn.StepName, "Operation"))
	if operation == "" {
		return ""
	}
	payload := webServicePayloadArgs(fn)
	if len(payload) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><`)
	b.WriteString(operation)
	b.WriteString(">")
	for _, arg := range payload {
		name := sanitizeXMLName(arg.Name)
		value := convertParamReferences(unescapeSourceCString(arg.Value))
		if strings.HasPrefix(strings.ToLower(arg.Name), "xml:") {
			b.WriteString(value)
			continue
		}
		if name == "" {
			continue
		}
		b.WriteString("<")
		b.WriteString(name)
		b.WriteString(">")
		b.WriteString(escapeXMLText(value))
		b.WriteString("</")
		b.WriteString(name)
		b.WriteString(">")
	}
	b.WriteString("</")
	b.WriteString(operation)
	b.WriteString(`></soapenv:Body></soapenv:Envelope>`)
	return b.String()
}

type webServicePayloadArg struct {
	Name  string
	Value string
}

func webServicePayloadArgs(fn ParsedFunction) []webServicePayloadArg {
	skip := map[string]struct{}{
		"stepname":           {},
		"soapmethod":         {},
		"responseparam":      {},
		"service":            {},
		"expectedresponse":   {},
		"snapshot":           {},
		"soapaction":         {},
		"url":                {},
		"action":             {},
		"body":               {},
		"contenttype":        {},
		"content-type":       {},
		"method":             {},
		"reccontenttype":     {},
		"resource":           {},
		"referer":            {},
		"mode":               {},
		"enctype":            {},
		"targetframe":        {},
		"targetbrowser":      {},
		"ordinal":            {},
		"savecount":          {},
		"scope":              {},
		"requesturl":         {},
		"search":             {},
		"fail":               {},
		"ignoreredirections": {},
	}
	args := make([]webServicePayloadArg, 0)
	for _, arg := range fn.CallArgs {
		if arg.Kind != "key_value" || arg.Name == "" {
			continue
		}
		if _, ok := skip[strings.ToLower(arg.Name)]; ok {
			continue
		}
		args = append(args, webServicePayloadArg{Name: arg.Name, Value: arg.Value})
	}
	return args
}

func sanitizeEnvToken(value string) string {
	var b strings.Builder
	prevUnderscore := false
	for _, r := range strings.ToUpper(value) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevUnderscore = false
			continue
		}
		if !prevUnderscore {
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	return b.String()
}

func sanitizeXMLName(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), "xml:") {
		value = value[4:]
	}
	var b strings.Builder
	for i, r := range value {
		valid := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_' || r == ':' || (i > 0 && ((r >= '0' && r <= '9') || r == '-' || r == '.'))
		if valid {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func escapeXMLText(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	value = strings.ReplaceAll(value, `"`, "&quot;")
	value = strings.ReplaceAll(value, "'", "&apos;")
	return value
}

func requestContentTypeFromFunction(fn ParsedFunction) string {
	for _, key := range []string{"ContentType", "Content-Type"} {
		if value := firstNonEmptyArg(fn, key); strings.TrimSpace(value) != "" {
			return unescapeSourceCString(value)
		}
	}
	if encType := firstNonEmptyArg(fn, "EncType"); strings.TrimSpace(encType) != "" && !strings.EqualFold(strings.TrimSpace(encType), "raw") {
		return unescapeSourceCString(encType)
	}
	return ""
}
