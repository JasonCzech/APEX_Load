package soap

import (
	"context"
	"fmt"
	stdhttp "net/http"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/stormrunner/pkg/protocol"
	httpproto "github.com/jasonczech/stormrunner/pkg/protocol/http"
)

func init() {
	protocol.Register("soap", func() protocol.Protocol {
		return &SOAP{}
	})
}

type SOAP struct {
	http           *httpproto.HTTP
	version        string
	failOnFault    bool
	defaultCharset string
	namespaces     map[string]string
}

func (s *SOAP) Name() string { return "soap" }

func (s *SOAP) Init(ctx context.Context, config map[string]any) error {
	s.version = "1.1"
	if version, ok := config["soap_version"].(string); ok && strings.TrimSpace(version) != "" {
		s.version = strings.TrimSpace(version)
	}
	s.failOnFault, _ = config["soap_fault_fail_on_fault"].(bool)
	s.defaultCharset = "utf-8"
	s.namespaces = readNamespaces(config)
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
	prepared.Body = shapeSOAPBody(action, s.version, s.namespaces)
	applySOAPDefaults(&prepared, s.version, s.defaultCharset)

	result, err := s.http.Execute(ctx, &prepared)
	if err != nil || result == nil {
		return result, err
	}

	if s.failOnFault && result.StatusCode >= 500 && result.ErrorMessage == "" {
		result.ErrorMessage = "soap fault response"
	}

	return result, nil
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

func shapeSOAPBody(action *protocol.Action, version string, namespaces map[string]string) []byte {
	if action == nil || action.SOAP == nil {
		return action.Body
	}
	if strings.TrimSpace(action.SOAP.BodyMode) != "generated" {
		return action.Body
	}

	body := strings.TrimSpace(string(action.Body))
	if body == "" || looksLikeSOAPEnvelope(body) {
		return action.Body
	}

	envelopePrefix, allNamespaces := mergeSOAPNamespaces(version, namespaces)
	body = wrapSOAPOperation(body, action.SOAP.Operation)

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
	return []byte(envelope)
}

func looksLikeSOAPEnvelope(body string) bool {
	trimmed := strings.TrimSpace(body)
	return strings.Contains(trimmed, ":Envelope") || strings.Contains(trimmed, "<Envelope")
}

func mergeSOAPNamespaces(version string, namespaces map[string]string) (string, map[string]string) {
	allNamespaces := make(map[string]string, len(namespaces)+1)
	for key, value := range namespaces {
		allNamespaces[key] = value
	}
	envelopeURI := soapEnvelopeURI(version)
	for key, value := range allNamespaces {
		if value == envelopeURI {
			return key, allNamespaces
		}
	}
	allNamespaces["soapenv"] = envelopeURI
	return "soapenv", allNamespaces
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