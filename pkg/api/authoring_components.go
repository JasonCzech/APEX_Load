package api

import (
	"net/http"
	"strings"

	"github.com/jasonczech/stormrunner/pkg/dsl"
)

type AuthoringComponent struct {
	ID          string            `json:"id"`
	Kind        string            `json:"kind"`
	Name        string            `json:"name"`
	Protocols   []string          `json:"protocols"`
	Summary     string            `json:"summary"`
	Tags        []string          `json:"tags,omitempty"`
	Config      map[string]any    `json:"config,omitempty"`
	DataSources []dsl.DataSource  `json:"data_sources,omitempty"`
	Parameters  []dsl.Parameter   `json:"parameters,omitempty"`
	Init        []dsl.Action      `json:"init,omitempty"`
	Actions     []dsl.Action      `json:"actions,omitempty"`
	Teardown    []dsl.Action      `json:"teardown,omitempty"`
	UsageNotes  []string          `json:"usage_notes,omitempty"`
	Variables   map[string]string `json:"variables,omitempty"`
}

func (s *Server) handleAuthoringComponents(w http.ResponseWriter, r *http.Request) {
	kind := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("kind")))
	protocol := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("protocol")))
	components := authoringComponentCatalog()
	filtered := make([]AuthoringComponent, 0, len(components))
	for _, component := range components {
		if kind != "" && strings.ToLower(component.Kind) != kind {
			continue
		}
		if protocol != "" && !componentSupportsProtocol(component, protocol) {
			continue
		}
		filtered = append(filtered, component)
	}
	writeJSON(w, http.StatusOK, map[string]any{"components": filtered, "count": len(filtered)})
}

func componentSupportsProtocol(component AuthoringComponent, protocol string) bool {
	for _, supported := range component.Protocols {
		if strings.EqualFold(supported, protocol) || supported == "*" {
			return true
		}
	}
	return false
}

func authoringComponentCatalog() []AuthoringComponent {
	return []AuthoringComponent{
		{
			ID:        "http-core-api-request-group",
			Kind:      "request_group",
			Name:      "Core API Request Group",
			Protocols: []string{"http", "devweb", "har", "jmeter"},
			Summary:   "Reusable health, lookup, and write transaction group for service smoke and release gates.",
			Tags:      []string{"api", "smoke", "transactions"},
			Actions: []dsl.Action{
				{Name: "Health Check", Type: "request", Method: "GET", Target: "/health", Assertions: []dsl.Assertion{{Type: "status_code", Expected: 200}}},
				{Name: "Lookup Entity", Type: "request", Method: "GET", Target: "/api/entities/{{data.users.entity_id}}", Assertions: []dsl.Assertion{{Type: "status_code", Expected: 200}}},
				{Name: "Create Transaction", Type: "request", Method: "POST", Target: "/api/transactions", Headers: map[string]string{"Content-Type": "application/json"}, Body: `{"customer":"{{data.users.customer_id}}","amount":{{data.users.amount}}}`, Assertions: []dsl.Assertion{{Type: "status_code", Expected: 201}}},
			},
			UsageNotes: []string{"Requires a users data source with entity_id, customer_id, and amount columns.", "Use per-action transactions when this group is part of a larger business flow."},
		},
		{
			ID:        "http-oauth-bearer-auth-flow",
			Kind:      "auth_flow",
			Name:      "OAuth Bearer Token Flow",
			Protocols: []string{"http", "soap", "java_http", "devweb", "har", "jmeter"},
			Summary:   "Token acquisition action with extraction and reusable Authorization header wiring.",
			Tags:      []string{"auth", "oauth", "correlation"},
			Config: map[string]any{
				"http": map[string]any{
					"default_headers": map[string]string{"Authorization": "Bearer {{var.access_token}}"},
				},
			},
			Init: []dsl.Action{{
				Name:    "Acquire Access Token",
				Type:    "request",
				Method:  "POST",
				Target:  "/oauth/token",
				Headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
				Body:    "grant_type=client_credentials&client_id={{env.CLIENT_ID}}&client_secret={{env.CLIENT_SECRET}}",
				Extractors: []dsl.Extractor{{
					Name:       "access_token",
					Source:     "body",
					Type:       "jsonpath",
					Expression: "$.access_token",
				}},
				Assertions: []dsl.Assertion{{Type: "status_code", Expected: 200}},
			}},
			Variables:  map[string]string{"access_token": "Extracted during init and reused by request actions."},
			UsageNotes: []string{"Keep credentials in environment variables.", "Run in init when token reuse matches the target system policy."},
		},
		{
			ID:        "browser-login-transaction",
			Kind:      "login_transaction",
			Name:      "Browser Login Transaction",
			Protocols: []string{"browser", "trueclient_mobile_web", "trueclient2_web"},
			Summary:   "Browser login sequence with page-ready wait, credentials, submit, and post-login assertion.",
			Tags:      []string{"browser", "login", "canary"},
			Actions: []dsl.Action{
				{Name: "Open Login", Type: "browser_navigate", Target: "/login", Capture: &dsl.BrowserCaptureOptions{Screenshot: true}},
				{Name: "Username", Type: "browser_fill", Target: "", Selector: "[name='username']", Value: "{{data.users.username}}"},
				{Name: "Password", Type: "browser_fill", Target: "", Selector: "[name='password']", Value: "{{data.users.password}}"},
				{Name: "Submit Login", Type: "browser_click", Target: "", Selector: "[type='submit']"},
				{Name: "Wait For Account", Type: "browser_wait", Target: "", WaitFor: &dsl.WaitCondition{Type: "selector", Value: "[data-testid='account-home']", TimeoutMs: 10000}},
			},
			UsageNotes: []string{"Use browser VUsers as canaries and keep density expectations separate from HTTP load.", "Pair with a data pool when each virtual user needs unique credentials."},
		},
		{
			ID:        "shared-user-data-pool",
			Kind:      "data_pool",
			Name:      "Shared User Data Pool",
			Protocols: []string{"*"},
			Summary:   "CSV-backed user and account data source with unique-row selection for login and account flows.",
			Tags:      []string{"data", "parameters", "unique"},
			DataSources: []dsl.DataSource{{
				Name:           "users",
				Type:           "csv",
				Strategy:       "unique",
				Delimiter:      ",",
				FirstRowHeader: true,
				Columns:        []string{"username", "password", "customer_id", "entity_id", "amount"},
			}},
			Parameters: []dsl.Parameter{
				{Name: "username", Type: "file", Update: "each_iteration", Selection: "unique", OnExhaustion: "error", DataSource: "users", Column: "username"},
				{Name: "customer_id", Type: "file", Update: "each_iteration", Selection: "unique", OnExhaustion: "error", DataSource: "users", Column: "customer_id"},
			},
			UsageNotes: []string{"Pre-run validation blocks scenarios when unique rows cannot satisfy modeled demand.", "Keep secrets out of committed inline data; point path to an environment-mounted file."},
		},
		{
			ID:        "logout-teardown-cleanup",
			Kind:      "teardown",
			Name:      "Logout And Cleanup Teardown",
			Protocols: []string{"http", "browser", "devweb", "har", "jmeter"},
			Summary:   "End-of-iteration cleanup actions for logout, cart/session reset, and graceful close.",
			Tags:      []string{"teardown", "cleanup", "session"},
			Teardown: []dsl.Action{
				{Name: "Logout", Type: "request", Method: "POST", Target: "/logout", Assertions: []dsl.Assertion{{Type: "status_code", Expected: 200}}},
				{Name: "Cleanup Session", Type: "request", Method: "DELETE", Target: "/api/test-session/{{var.session_id}}", Assertions: []dsl.Assertion{{Type: "status_code", Expected: 204}}},
			},
			UsageNotes: []string{"Place protocol-specific cleanup in teardown so failure behavior is reported separately from business transactions.", "For browser scripts, replace request actions with browser_click or browser_navigate equivalents when logout is UI-only."},
		},
	}
}
