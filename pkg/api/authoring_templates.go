package api

import (
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

type AuthoringTemplate struct {
	ID                  string                    `json:"id"`
	Name                string                    `json:"name"`
	Protocol            string                    `json:"protocol"`
	UseCase             string                    `json:"use_case"`
	Description         string                    `json:"description"`
	Tags                []string                  `json:"tags"`
	Script              dsl.Script                `json:"script"`
	RecommendedScenario AuthoringTemplateScenario `json:"recommended_scenario"`
}

type AuthoringTemplateScenario struct {
	Duration   string `json:"duration"`
	VUsers     int    `json:"vusers"`
	RampUp     string `json:"ramp_up,omitempty"`
	Iterations int    `json:"iterations,omitempty"`
}

func (s *Server) handleAuthoringTemplates(w http.ResponseWriter, r *http.Request) {
	protocol := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("protocol")))
	useCase := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("use_case")))
	templates := authoringTemplateCatalog()
	filtered := make([]AuthoringTemplate, 0, len(templates))
	for _, template := range templates {
		if protocol != "" && strings.ToLower(template.Protocol) != protocol {
			continue
		}
		if useCase != "" && strings.ToLower(template.UseCase) != useCase {
			continue
		}
		filtered = append(filtered, template)
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": filtered, "count": len(filtered)})
}

func authoringTemplateCatalog() []AuthoringTemplate {
	return []AuthoringTemplate{
		{
			ID:          "http-api-smoke",
			Name:        "HTTP API Smoke",
			Protocol:    "http",
			UseCase:     "ci_smoke",
			Description: "Minimal API smoke test with health and authenticated business transaction placeholders.",
			Tags:        []string{"api", "ci", "smoke"},
			Script: dsl.Script{
				Name:     "HTTP API Smoke",
				Version:  "1.0.0",
				Protocol: "http",
				Config: dsl.ScriptConfig{
					BaseURL: "{{env.BASE_URL}}",
					ThinkTime: dsl.ThinkTimeConfig{
						Type:   "fixed",
						MeanMs: 250,
					},
					Pacing: dsl.PacingConfig{Type: "none"},
					RunLogic: dsl.RunLogicConfig{
						Iterations:       1,
						AutoTransactions: "per_action",
					},
					ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: false},
				},
				Actions: []dsl.Action{
					{Name: "Health Check", Type: "request", Method: "GET", Target: "/health", Assertions: []dsl.Assertion{{Type: "status_code", Expected: 200}}},
					{Name: "Business Transaction", Type: "request", Method: "GET", Target: "/api/orders", Assertions: []dsl.Assertion{{Type: "status_code", Expected: 200}}},
				},
			},
			RecommendedScenario: AuthoringTemplateScenario{Duration: "2m", VUsers: 5, RampUp: "30s", Iterations: 1},
		},
		{
			ID:          "browser-checkout-canary",
			Name:        "Browser Checkout Canary",
			Protocol:    "browser",
			UseCase:     "enterprise_flow",
			Description: "Browser canary for a login-to-checkout path with screenshots and page-ready waits.",
			Tags:        []string{"browser", "checkout", "canary"},
			Script: dsl.Script{
				Name:     "Browser Checkout Canary",
				Version:  "1.0.0",
				Protocol: "browser",
				Config: dsl.ScriptConfig{
					BaseURL: "{{env.BASE_URL}}",
					Browser: dsl.BrowserConfig{
						Engine:        "chromium",
						Headless:      boolPtr(true),
						ClientMetrics: boolPtr(true),
					},
					ThinkTime: dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 500},
					RunLogic:  dsl.RunLogicConfig{Iterations: 1, AutoTransactions: "per_action"},
				},
				Actions: []dsl.Action{
					{Name: "Open Home", Type: "browser_navigate", Target: "/", Capture: &dsl.BrowserCaptureOptions{Screenshot: true}},
					{Name: "Wait For App Ready", Type: "browser_wait", WaitFor: &dsl.WaitCondition{Type: "selector", Value: "[data-testid='app-ready']"}},
					{Name: "Open Checkout", Type: "browser_click", Selector: "[data-testid='checkout']"},
				},
			},
			RecommendedScenario: AuthoringTemplateScenario{Duration: "5m", VUsers: 3, RampUp: "1m", Iterations: 1},
		},
		{
			ID:          "vugen-web2-review-profile",
			Name:        "VuGen Web 2.0 Review Profile",
			Protocol:    "browser",
			UseCase:     "migration_review",
			Description: "Review-only browser profile aligned with TruClient 2.0 - Web imports, replay evidence, and selector repair.",
			Tags:        []string{"vugen", "web2", "truclient", "migration"},
			Script: dsl.Script{
				Name:     "VuGen Web 2.0 Review Profile",
				Version:  "1.0.0",
				Protocol: "browser",
				Config: dsl.ScriptConfig{
					BaseURL: "{{env.BASE_URL}}",
					Browser: dsl.BrowserConfig{
						Engine:             "chromium",
						Headless:           boolPtr(true),
						ClientMetrics:      boolPtr(true),
						CaptureScreenshots: "on_failure",
					},
					ThinkTime: dsl.ThinkTimeConfig{Type: "recorded"},
					RunLogic:  dsl.RunLogicConfig{Iterations: 1, AutoTransactions: "per_action"},
				},
				DataSources: []dsl.DataSource{{
					Name:           "users",
					Type:           "csv",
					Strategy:       "unique",
					Delimiter:      ",",
					FirstRowHeader: true,
					Columns:        []string{"username", "password"},
				}},
				Actions: []dsl.Action{
					{Name: "Open Imported Start URL", Type: "browser_navigate", Target: "{{env.START_PATH}}", Capture: &dsl.BrowserCaptureOptions{Screenshot: true}},
					{Name: "Wait For App Ready", Type: "browser_wait", WaitFor: &dsl.WaitCondition{Type: "selector", Value: "[data-testid='app-ready']", TimeoutMs: 10000}},
					{Name: "Replay Checkpoint", Type: "browser_screenshot", Capture: &dsl.BrowserCaptureOptions{Screenshot: true, FullPage: true}},
				},
			},
			RecommendedScenario: AuthoringTemplateScenario{Duration: "5m", VUsers: 1, RampUp: "30s", Iterations: 1},
		},
		{
			ID:          "soap-service-smoke",
			Name:        "SOAP Service Smoke",
			Protocol:    "soap",
			UseCase:     "ci_smoke",
			Description: "SOAP service smoke template with raw XML body, SOAPAction, and fault-aware assertions.",
			Tags:        []string{"soap", "ci", "smoke"},
			Script: dsl.Script{
				Name:     "SOAP Service Smoke",
				Version:  "1.0.0",
				Protocol: "soap",
				Config: dsl.ScriptConfig{
					BaseURL: "{{env.BASE_URL}}",
					SOAP: dsl.SOAPConfig{
						Version: "1.1",
						FaultHandling: dsl.SOAPFaultHandlingConfig{
							FailOnFault: true,
						},
					},
					RunLogic: dsl.RunLogicConfig{Iterations: 1, AutoTransactions: "per_action"},
				},
				Actions: []dsl.Action{
					{
						Name:    "Get Account",
						Type:    "request",
						Method:  "POST",
						Target:  "/services/account",
						Headers: map[string]string{"Content-Type": "text/xml", "SOAPAction": "GetAccount"},
						Body:    `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><GetAccount><accountId>{{env.ACCOUNT_ID}}</accountId></GetAccount></soapenv:Body></soapenv:Envelope>`,
						SOAP:    &dsl.SOAPActionConfig{Action: "GetAccount", Operation: "GetAccount", BodyMode: "raw_xml"},
						Assertions: []dsl.Assertion{
							{Type: "status_code", Expected: 200},
							{Type: "body_not_contains", Value: "Fault"},
						},
					},
				},
			},
			RecommendedScenario: AuthoringTemplateScenario{Duration: "2m", VUsers: 3, RampUp: "30s", Iterations: 1},
		},
		{
			ID:          "java-http-rpc-smoke",
			Name:        "Java over HTTP RPC Smoke",
			Protocol:    "java_http",
			UseCase:     "enterprise_flow",
			Description: "Java-over-HTTP template for serialized request payloads on the HTTP transport.",
			Tags:        []string{"java", "rpc", "serialized"},
			Script: dsl.Script{
				Name:     "Java over HTTP RPC Smoke",
				Version:  "1.0.0",
				Protocol: "java_http",
				Config: dsl.ScriptConfig{
					BaseURL: "{{env.BASE_URL}}",
					JavaHTTP: dsl.JavaHTTPConfig{
						Codec:        "java_serialized_object_base64",
						HelperMode:   "sidecar",
						BodyEncoding: "base64",
					},
					RunLogic: dsl.RunLogicConfig{Iterations: 1, AutoTransactions: "per_action"},
				},
				Actions: []dsl.Action{{
					Name:   "Invoke Serialized Operation",
					Type:   "request",
					Method: "POST",
					Target: "/rpc/checkout",
					Headers: map[string]string{
						"Content-Type": "application/x-java-serialized-object",
					},
					Body:       "{{env.JAVA_SERIALIZED_REQUEST_BASE64}}",
					Assertions: []dsl.Assertion{{Type: "status_code", Expected: 200}},
				}},
			},
			RecommendedScenario: AuthoringTemplateScenario{Duration: "3m", VUsers: 5, RampUp: "45s", Iterations: 1},
		},
		{
			ID:          "websocket-stream-canary",
			Name:        "WebSocket Stream Canary",
			Protocol:    "websocket",
			UseCase:     "enterprise_flow",
			Description: "Connect, send, receive, ping, and close flow for persistent WebSocket paths.",
			Tags:        []string{"websocket", "streaming", "canary"},
			Script: dsl.Script{
				Name:     "WebSocket Stream Canary",
				Version:  "1.0.0",
				Protocol: "websocket",
				Config: dsl.ScriptConfig{
					BaseURL: "{{env.WS_BASE_URL}}",
					WebSocket: dsl.WebSocketConfig{
						ReadTimeoutMs:  5000,
						WriteTimeoutMs: 2500,
						MaxMessageSize: 65536,
					},
					RunLogic: dsl.RunLogicConfig{Iterations: 1, AutoTransactions: "per_action"},
				},
				Actions: []dsl.Action{
					{Name: "Connect", Type: "ws_connect", Target: "/ws"},
					{Name: "Subscribe", Type: "ws_send", Body: `{"type":"subscribe","channel":"orders"}`, Params: map[string]any{"type": "text"}},
					{Name: "Receive Event", Type: "ws_receive", Params: map[string]any{"timeout_ms": 1000}},
					{Name: "Ping", Type: "ws_ping"},
					{Name: "Close", Type: "ws_close"},
				},
			},
			RecommendedScenario: AuthoringTemplateScenario{Duration: "5m", VUsers: 25, RampUp: "1m", Iterations: 1},
		},
		{
			ID:          "grpc-unary-smoke",
			Name:        "gRPC Unary Smoke",
			Protocol:    "grpc",
			UseCase:     "ci_smoke",
			Description: "Unary gRPC call template using reflection-friendly service/method targets.",
			Tags:        []string{"grpc", "api", "smoke"},
			Script: dsl.Script{
				Name:     "gRPC Unary Smoke",
				Version:  "1.0.0",
				Protocol: "grpc",
				Config: dsl.ScriptConfig{
					BaseURL: "{{env.GRPC_TARGET}}",
					GRPC: dsl.GRPCConfig{
						UseReflection:  true,
						PlainText:      true,
						MaxRecvMsgSize: 1048576,
						TimeoutMs:      5000,
					},
					RunLogic: dsl.RunLogicConfig{Iterations: 1, AutoTransactions: "per_action"},
				},
				Actions: []dsl.Action{{
					Name:   "Authorize Payment",
					Type:   "grpc_unary",
					Target: "payments.v1.PaymentService/Authorize",
					Body:   `{"account_id":"{{env.ACCOUNT_ID}}","amount_cents":100}`,
					Params: map[string]any{"timeout_ms": 1000},
				}},
			},
			RecommendedScenario: AuthoringTemplateScenario{Duration: "3m", VUsers: 10, RampUp: "45s", Iterations: 1},
		},
		{
			ID:          "jms-solace-request-reply",
			Name:        "JMS Solace Request Reply",
			Protocol:    "jms-solace",
			UseCase:     "enterprise_flow",
			Description: "Mock-safe Solace JMS publish/request-reply starter with queue defaults.",
			Tags:        []string{"jms", "solace", "request-reply"},
			Script: dsl.Script{
				Name:     "JMS Solace Request Reply",
				Version:  "1.0.0",
				Protocol: "jms-solace",
				Config: dsl.ScriptConfig{
					JMS: dsl.JMSConfig{
						Provider:        "solace",
						Destination:     "orders.in",
						DestinationType: "queue",
						ReplyTo:         "orders.reply",
						Acknowledgement: "auto",
						DeliveryMode:    "persistent",
						Mock:            true,
					},
					RunLogic: dsl.RunLogicConfig{Iterations: 1, AutoTransactions: "per_action"},
				},
				Actions: []dsl.Action{
					{Name: "Publish Order", Type: "jms_publish", Body: `{"order_id":"{{fn.uuid()}}","sku":"ABC-123"}`},
					{Name: "Request Confirmation", Type: "jms_request_reply", Body: `{"order_id":"{{var.order_id}}"}`},
				},
			},
			RecommendedScenario: AuthoringTemplateScenario{Duration: "3m", VUsers: 5, RampUp: "45s", Iterations: 1},
		},
		{
			ID:                  "jmeter-http-migration-starter",
			Name:                "JMeter HTTP Migration Starter",
			Protocol:            "jmeter",
			UseCase:             "migration_review",
			Description:         "HTTP DSL starter shaped for reviewing JMeter-imported thread groups and assertions.",
			Tags:                []string{"jmeter", "migration", "http"},
			Script:              importedHTTPTemplateScript("JMeter HTTP Migration Starter", "jmeter", "JMeter Thread Group Request"),
			RecommendedScenario: AuthoringTemplateScenario{Duration: "5m", VUsers: 10, RampUp: "1m", Iterations: 1},
		},
		{
			ID:                  "har-api-replay-starter",
			Name:                "HAR API Replay Starter",
			Protocol:            "har",
			UseCase:             "migration_review",
			Description:         "HTTP DSL starter for turning curated HAR traffic into reusable API scripts.",
			Tags:                []string{"har", "api", "replay"},
			Script:              importedHTTPTemplateScript("HAR API Replay Starter", "har", "Replay Captured Request"),
			RecommendedScenario: AuthoringTemplateScenario{Duration: "3m", VUsers: 5, RampUp: "30s", Iterations: 1},
		},
		{
			ID:                  "devweb-api-migration-starter",
			Name:                "DevWeb API Migration Starter",
			Protocol:            "devweb",
			UseCase:             "migration_review",
			Description:         "HTTP DSL starter for DevWeb action/replay parity review before promotion.",
			Tags:                []string{"devweb", "migration", "api"},
			Script:              importedHTTPTemplateScript("DevWeb API Migration Starter", "devweb", "DevWeb Step"),
			RecommendedScenario: AuthoringTemplateScenario{Duration: "5m", VUsers: 10, RampUp: "1m", Iterations: 1},
		},
	}
}

func importedHTTPTemplateScript(name string, source string, actionName string) dsl.Script {
	return dsl.Script{
		Name:     name,
		Version:  "1.0.0",
		Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: source,
			Preserved: map[string]any{
				"template_source": source,
				"review_required": true,
			},
		}},
		Config: dsl.ScriptConfig{
			BaseURL:  "{{env.BASE_URL}}",
			RunLogic: dsl.RunLogicConfig{Iterations: 1, AutoTransactions: "per_action"},
		},
		Actions: []dsl.Action{{
			Name:       actionName,
			Type:       "request",
			Method:     "GET",
			Target:     "/api/example",
			Assertions: []dsl.Assertion{{Type: "status_code", Expected: 200}},
		}},
	}
}

func boolPtr(value bool) *bool {
	return &value
}
