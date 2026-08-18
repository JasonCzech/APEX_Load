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
					// Left unset so the scenario continue-on-error policy decides.
					ErrorHandling: dsl.ErrorHandlingConfig{},
				},
				Actions: []dsl.Action{
					httpTemplateAction("GET", "Health Check", "/health", func(action *dsl.Action) {
						action.Assertions = []dsl.Assertion{{Type: "status_code", Expected: 200}}
					}),
					httpTemplateAction("GET", "Business Transaction", "/api/orders", func(action *dsl.Action) {
						action.Assertions = []dsl.Assertion{{Type: "status_code", Expected: 200}}
					}),
				},
			},
			RecommendedScenario: AuthoringTemplateScenario{Duration: "2m", VUsers: 5, RampUp: "30s", Iterations: 1},
		},
		{
			ID:          "agentforce-streaming-agent",
			Name:        "Agentforce Streaming Agent",
			Protocol:    "http",
			UseCase:     "enterprise_flow",
			Description: "Agent API session lifecycle with OAuth2 token acquisition, SSE message streaming, and end-session cleanup.",
			Tags:        []string{"salesforce", "agentforce", "sse", "oauth2"},
			Script: dsl.Script{
				Name:     "Agentforce Streaming Agent",
				Version:  "1.0.0",
				Protocol: "http",
				Config: dsl.ScriptConfig{
					BaseURL: "{{env.SF_API_HOST}}",
					ThinkTime: dsl.ThinkTimeConfig{
						Type:   "fixed",
						MeanMs: 500,
					},
					Pacing: dsl.PacingConfig{Type: "none"},
					RunLogic: dsl.RunLogicConfig{
						Iterations:       1,
						AutoTransactions: "per_action",
					},
					// Left unset so the scenario continue-on-error policy decides.
					ErrorHandling: dsl.ErrorHandlingConfig{},
				},
				Init: []dsl.Action{
					templateAction("http_oauth2_token", "Get Agent API Token", func(action *dsl.Action) {
						action.Params = map[string]any{
							"token_url":     "{{env.SALESFORCE_TOKEN_URL}}",
							"grant_type":    "client_credentials",
							"client_id":     "{{env.SALESFORCE_CLIENT_ID}}",
							"client_secret": "{{secret.SALESFORCE_CLIENT_SECRET}}",
							"scope":         "{{env.SALESFORCE_SCOPE}}",
							"save_as":       "access_token",
						}
					}),
				},
				Actions: []dsl.Action{
					httpTemplateAction("POST", "Start Agent Session", "/einstein/ai-agent/v1/agents/{{env.AGENT_ID}}/sessions", func(action *dsl.Action) {
						action.Headers = map[string]string{
							"Authorization": "Bearer {{var.access_token}}",
							"Content-Type":  "application/json",
							"Accept":        "application/json",
						}
						action.Body = `{"externalSessionKey":"{{fn.uuid()}}","instanceConfig":{"endpoint":"{{env.MY_DOMAIN_URL}}"},"featureSupport":"Streaming","bypassUser":true}`
						action.Extractors = []dsl.Extractor{{Name: "session_id", Source: "body", Type: "jsonpath", Expression: "$.sessionId", OnMiss: "fail"}}
						action.Assertions = []dsl.Assertion{{Type: "status_code", Expected: 200}}
					}),
					httpTemplateAction("POST", "Send Streaming Message", "/einstein/ai-agent/v1/sessions/{{var.session_id}}/messages/stream", func(action *dsl.Action) {
						action.Headers = map[string]string{
							"Authorization": "Bearer {{var.access_token}}",
							"Content-Type":  "application/json",
							"Accept":        "text/event-stream",
						}
						action.Params = map[string]any{
							"stream":            "sse",
							"stop_on_event":     "EndOfTurn",
							"max_events":        200,
							"stream_timeout_ms": 60000,
						}
						action.Body = `{"message":{"sequenceId":{{fn.timestamp.unix()}},"type":"Text","text":"{{env.AGENT_PROMPT}}"},"variables":[]}`
						action.Extractors = []dsl.Extractor{
							{Name: "agent_text", Source: "body", Type: "sse_concat", Expression: "TextChunk", OnMiss: "empty"},
							{Name: "end_of_turn", Source: "body", Type: "sse_event", Expression: "EndOfTurn", OnMiss: "empty"},
						}
						action.Assertions = []dsl.Assertion{
							{Type: "status_code", Expected: 200},
							{Type: "body_not_contains", Value: "ValidationFailureChunk"},
						}
					}),
				},
				Teardown: []dsl.Action{
					httpTemplateAction("DELETE", "End Agent Session", "/einstein/ai-agent/v1/sessions/{{var.session_id}}", func(action *dsl.Action) {
						action.Headers = map[string]string{
							"Authorization":        "Bearer {{var.access_token}}",
							"x-session-end-reason": "UserRequest",
						}
						action.Assertions = []dsl.Assertion{{Type: "status_code", Expected: 200}}
					}),
				},
			},
			RecommendedScenario: AuthoringTemplateScenario{Duration: "5m", VUsers: 5, RampUp: "1m", Iterations: 1},
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
					templateAction("browser_navigate", "Open Home", func(action *dsl.Action) {
						action.Target = "/"
						action.Capture = &dsl.BrowserCaptureOptions{Screenshot: true}
					}),
					templateAction("browser_wait", "Wait For App Ready", func(action *dsl.Action) {
						action.WaitFor = &dsl.WaitCondition{Type: "selector", Value: "[data-testid='app-ready']"}
					}),
					templateAction("browser_click", "Open Checkout", func(action *dsl.Action) {
						action.Target = ""
						action.Selector = "[data-testid='checkout']"
					}),
				},
			},
			RecommendedScenario: AuthoringTemplateScenario{Duration: "5m", VUsers: 3, RampUp: "1m", Iterations: 1},
		},
		{
			ID:          "web2-browser-review-profile",
			Name:        "Web 2.0 Browser Profile Review",
			Protocol:    "browser",
			UseCase:     "migration_review",
			Description: "Review-only browser profile aligned with Web 2.0 source archives, replay evidence, and selector repair.",
			Tags:        []string{"web2", "browser-profile", "enterprise-archive", "migration"},
			Script: dsl.Script{
				Name:     "Web 2.0 Browser Profile Review",
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
					templateAction("browser_navigate", "Open Imported Start URL", func(action *dsl.Action) {
						action.Target = "{{env.START_PATH}}"
						action.Capture = &dsl.BrowserCaptureOptions{Screenshot: true}
					}),
					templateAction("browser_wait", "Wait For App Ready", func(action *dsl.Action) {
						action.WaitFor = &dsl.WaitCondition{Type: "selector", Value: "[data-testid='app-ready']", TimeoutMs: 10000}
					}),
					templateAction("browser_screenshot", "Replay Checkpoint", func(action *dsl.Action) {
						action.Capture = &dsl.BrowserCaptureOptions{Screenshot: true, FullPage: true}
					}),
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
					httpTemplateAction("POST", "Get Account", "/services/account", func(action *dsl.Action) {
						action.Headers = map[string]string{"Content-Type": "text/xml", "SOAPAction": "GetAccount"}
						action.Body = `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><GetAccount><accountId>{{env.ACCOUNT_ID}}</accountId></GetAccount></soapenv:Body></soapenv:Envelope>`
						action.SOAP = &dsl.SOAPActionConfig{Action: "GetAccount", Operation: "GetAccount", BodyMode: "raw_xml"}
						action.Assertions = []dsl.Assertion{
							{Type: "status_code", Expected: 200},
							{Type: "body_not_contains", Value: "Fault"},
						}
					}),
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
						HelperMode:   "embedded",
						BodyEncoding: "base64",
					},
					RunLogic: dsl.RunLogicConfig{Iterations: 1, AutoTransactions: "per_action"},
				},
				Actions: []dsl.Action{httpTemplateAction("POST", "Invoke Serialized Operation", "/rpc/checkout", func(action *dsl.Action) {
					action.Headers = map[string]string{
						"Content-Type": "application/x-java-serialized-object",
					}
					action.Body = "{{env.JAVA_SERIALIZED_REQUEST_BASE64}}"
					action.Assertions = []dsl.Assertion{{Type: "status_code", Expected: 200}}
				})},
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
					templateAction("ws_connect", "Connect", func(action *dsl.Action) {
						action.Target = "/ws"
					}),
					templateAction("ws_send", "Subscribe", func(action *dsl.Action) {
						action.Body = `{"type":"subscribe","channel":"orders"}`
						action.Params = map[string]any{"type": "text"}
					}),
					templateAction("ws_receive", "Receive Event", func(action *dsl.Action) {
						action.Params = map[string]any{"timeout_ms": 1000}
					}),
					templateAction("ws_ping", "Ping", nil),
					templateAction("ws_close", "Close", nil),
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
				Actions: []dsl.Action{templateAction("grpc_unary", "Authorize Payment", func(action *dsl.Action) {
					action.Target = "payments.v1.PaymentService/Authorize"
					action.Body = `{"account_id":"{{env.ACCOUNT_ID}}","amount_cents":100}`
					action.Params = map[string]any{"timeout_ms": 1000}
				})},
			},
			RecommendedScenario: AuthoringTemplateScenario{Duration: "3m", VUsers: 10, RampUp: "45s", Iterations: 1},
		},
		{
			ID:          "database-sql-fixture-smoke",
			Name:        "Database SQL Fixture Smoke",
			Protocol:    "database",
			UseCase:     "migration_review",
			Description: "SQLite-safe database starter with health, query, bind, extraction, and row-count evidence.",
			Tags:        []string{"database", "sql", "fixture"},
			Script: dsl.Script{
				Name:     "Database SQL Fixture Smoke",
				Version:  "1.0.0",
				Protocol: "database",
				Config: dsl.ScriptConfig{
					Database: dsl.DatabaseConfig{
						Driver:             "sqlite",
						DSN:                "{{env.DATABASE_DSN}}",
						QueryTimeoutMs:     5000,
						MaxRows:            25,
						ReadOnly:           true,
						RollbackOnTeardown: true,
					},
					RunLogic: dsl.RunLogicConfig{Iterations: 1, AutoTransactions: "per_action"},
				},
				Actions: []dsl.Action{
					templateAction("db_health", "Database Health", nil),
					templateAction("db_query", "Lookup Customer", func(action *dsl.Action) {
						action.Target = "select id, name from customers where id = ?"
						action.Params = map[string]any{"binds": []any{"{{data.customers.id}}"}, "max_rows": 10}
						action.Extractors = []dsl.Extractor{{
							Name:       "customer_name",
							Source:     "body",
							Type:       "jsonpath",
							Expression: "$.rows[0].name",
						}}
						action.Assertions = []dsl.Assertion{{Type: "row_count", Expected: 1}}
					}),
				},
				DataSources: []dsl.DataSource{{
					Name:           "customers",
					Type:           "csv",
					Strategy:       "sequential",
					Delimiter:      ",",
					FirstRowHeader: true,
					Columns:        []string{"id"},
				}},
			},
			RecommendedScenario: AuthoringTemplateScenario{Duration: "2m", VUsers: 3, RampUp: "30s", Iterations: 1},
		},
		{
			ID:          "network-dns-tcp-canary",
			Name:        "Network DNS/TCP Canary",
			Protocol:    "network",
			UseCase:     "ci_smoke",
			Description: "Controlled network protocol starter for DNS lookup plus TCP request transcript evidence.",
			Tags:        []string{"network", "dns", "tcp"},
			Script: dsl.Script{
				Name:     "Network DNS TCP Canary",
				Version:  "1.0.0",
				Protocol: "network",
				Config: dsl.ScriptConfig{
					Network: dsl.NetworkConfig{
						Subprotocol: "dns",
						Host:        "{{env.NETWORK_HOST}}",
						TimeoutMs:   3000,
					},
					RunLogic: dsl.RunLogicConfig{Iterations: 1, AutoTransactions: "per_action"},
				},
				Actions: []dsl.Action{
					templateAction("dns_query", "Resolve Host", func(action *dsl.Action) {
						action.Target = "{{env.NETWORK_HOST}}"
						action.Params = map[string]any{"record_type": "A"}
						action.Extractors = []dsl.Extractor{{Name: "resolved_ip", Source: "body", Type: "jsonpath", Expression: "$.records[0]"}}
						action.Assertions = []dsl.Assertion{{Type: "record_count", Expected: 1}}
					}),
					templateAction("tcp_request", "TCP Probe", func(action *dsl.Action) {
						action.Target = "{{env.NETWORK_TCP_TARGET}}"
						action.Body = "ping"
						action.Params = map[string]any{"read": true, "read_bytes": 1024}
						action.Assertions = []dsl.Assertion{{Type: "messages_received", Expected: 1}}
					}),
				},
			},
			RecommendedScenario: AuthoringTemplateScenario{Duration: "2m", VUsers: 5, RampUp: "30s", Iterations: 1},
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
					templateAction("jms_publish", "Publish Order", func(action *dsl.Action) {
						action.Body = `{"order_id":"{{fn.uuid()}}","sku":"ABC-123"}`
					}),
					templateAction("jms_request_reply", "Request Confirmation", func(action *dsl.Action) {
						action.Body = `{"order_id":"{{var.order_id}}"}`
					}),
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
		Actions: []dsl.Action{httpTemplateAction("GET", actionName, "/api/example", func(action *dsl.Action) {
			action.Assertions = []dsl.Assertion{{Type: "status_code", Expected: 200}}
		})},
	}
}

func templateAction(actionType string, name string, mutate func(*dsl.Action)) dsl.Action {
	action := dsl.SkeletonAction(actionType)
	action.Name = name
	if mutate != nil {
		mutate(&action)
	}
	return action
}

func httpTemplateAction(method string, name string, target string, mutate func(*dsl.Action)) dsl.Action {
	action := templateAction(method, name, nil)
	action.Method = method
	action.Target = target
	if mutate != nil {
		mutate(&action)
	}
	return action
}

func boolPtr(value bool) *bool {
	return &value
}
