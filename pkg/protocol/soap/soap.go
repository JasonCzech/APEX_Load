package soap

import (
	"context"
	stdhttp "net/http"
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
}

func (s *SOAP) Name() string { return "soap" }

func (s *SOAP) Init(ctx context.Context, config map[string]any) error {
	s.version = "1.1"
	if version, ok := config["soap_version"].(string); ok && strings.TrimSpace(version) != "" {
		s.version = strings.TrimSpace(version)
	}
	s.failOnFault, _ = config["soap_fault_fail_on_fault"].(bool)
	s.defaultCharset = "utf-8"
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