package api

import (
	"net/http"
	"strings"

	"github.com/jasonczech/stormrunner/pkg/dsl"
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
	}
}

func boolPtr(value bool) *bool {
	return &value
}
