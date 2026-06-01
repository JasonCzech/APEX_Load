package api

import (
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
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
			ID:        "web2-search-flow",
			Kind:      "request_group",
			Name:      "Web 2.0 Search Flow",
			Protocols: []string{"browser", "trueclient2_web"},
			Summary:   "Search box entry, submit, results wait, and result assertion for TruClient Web 2.0 migrations.",
			Tags:      []string{"browser", "web2", "search"},
			Actions: []dsl.Action{
				{Name: "Open Search", Type: "browser_navigate", Target: "/search", Capture: &dsl.BrowserCaptureOptions{Screenshot: true}},
				{Name: "Enter Search Term", Type: "browser_fill", Target: "", Selector: "[data-testid='search-input']", Value: "{{data.users.search_term}}"},
				{Name: "Submit Search", Type: "browser_click", Target: "", Selector: "[data-testid='search-submit']"},
				{Name: "Wait For Results", Type: "browser_wait", Target: "", WaitFor: &dsl.WaitCondition{Type: "selector", Value: "[data-testid='search-results']", TimeoutMs: 10000}},
			},
			UsageNotes: []string{"Replace selectors with imported object descriptor candidates when available.", "Keep result waits explicit so replay evidence can distinguish timing from selector failures."},
		},
		{
			ID:        "web2-add-to-cart-flow",
			Kind:      "request_group",
			Name:      "Web 2.0 Add To Cart Flow",
			Protocols: []string{"browser", "trueclient2_web"},
			Summary:   "Product lookup, item selection, add-to-cart, and cart-count wait for ecommerce-style Web 2.0 scripts.",
			Tags:      []string{"browser", "web2", "cart"},
			Actions: []dsl.Action{
				{Name: "Open Product", Type: "browser_navigate", Target: "/products/{{data.users.product_id}}", Capture: &dsl.BrowserCaptureOptions{Screenshot: true}},
				{Name: "Choose Product Option", Type: "browser_select", Target: "", Selector: "[data-testid='product-option']", Value: "{{data.users.product_option}}"},
				{Name: "Add To Cart", Type: "browser_click", Target: "", Selector: "[data-testid='add-to-cart']"},
				{Name: "Wait For Cart Count", Type: "browser_wait", Target: "", WaitFor: &dsl.WaitCondition{Type: "selector", Value: "[data-testid='cart-count']", TimeoutMs: 10000}},
			},
			UsageNotes: []string{"Use as a browser canary around generated HTTP cart APIs.", "Validate product data-source exhaustion before scaling the scenario."},
		},
		{
			ID:        "web2-checkout-flow",
			Kind:      "request_group",
			Name:      "Web 2.0 Checkout Flow",
			Protocols: []string{"browser", "trueclient2_web"},
			Summary:   "Checkout navigation, shipping/payment placeholders, order submit, and confirmation wait.",
			Tags:      []string{"browser", "web2", "checkout"},
			Actions: []dsl.Action{
				{Name: "Open Checkout", Type: "browser_click", Target: "", Selector: "[data-testid='checkout']"},
				{Name: "Wait For Checkout", Type: "browser_wait", Target: "", WaitFor: &dsl.WaitCondition{Type: "selector", Value: "[data-testid='checkout-form']", TimeoutMs: 10000}},
				{Name: "Fill Shipping Zip", Type: "browser_fill", Target: "", Selector: "[name='shipping_zip']", Value: "{{data.users.shipping_zip}}"},
				{Name: "Submit Order", Type: "browser_click", Target: "", Selector: "[data-testid='place-order']"},
				{Name: "Wait For Confirmation", Type: "browser_wait", Target: "", WaitFor: &dsl.WaitCondition{Type: "selector", Value: "[data-testid='order-confirmation']", TimeoutMs: 15000}, Capture: &dsl.BrowserCaptureOptions{Screenshot: true, HTML: true}},
			},
			UsageNotes: []string{"Keep checkout scripts at canary density and move server load to generated HTTP/API drafts.", "Attach screenshot and DOM evidence to approval before promotion."},
		},
		{
			ID:        "web2-iframe-flow",
			Kind:      "request_group",
			Name:      "Web 2.0 Iframe Flow",
			Protocols: []string{"browser", "trueclient2_web"},
			Summary:   "Iframe readiness, framed control selection, and post-frame assertion placeholders.",
			Tags:      []string{"browser", "web2", "iframe"},
			Actions: []dsl.Action{
				{Name: "Open Embedded Flow", Type: "browser_navigate", Target: "/embedded"},
				{Name: "Wait For Frame Host", Type: "browser_wait", Target: "", WaitFor: &dsl.WaitCondition{Type: "selector", Value: "[data-testid='embedded-frame']", TimeoutMs: 10000}},
				{Name: "Click Framed Continue", Type: "browser_click", Target: "", Selector: "[data-testid='embedded-frame'] >> [data-testid='continue']"},
				{Name: "Wait For Frame Result", Type: "browser_wait", Target: "", WaitFor: &dsl.WaitCondition{Type: "selector", Value: "[data-testid='frame-complete']", TimeoutMs: 10000}},
			},
			UsageNotes: []string{"Replace frame selectors with replay-confirmed frame context when object diagnostics are available.", "Treat frame mismatch replay failures as selector repair work, not protocol load readiness."},
		},
		{
			ID:        "web2-modal-flow",
			Kind:      "request_group",
			Name:      "Web 2.0 Modal Flow",
			Protocols: []string{"browser", "trueclient2_web"},
			Summary:   "Open modal, wait for focusable content, confirm, and verify modal closure.",
			Tags:      []string{"browser", "web2", "modal"},
			Actions: []dsl.Action{
				{Name: "Open Modal", Type: "browser_click", Target: "", Selector: "[data-testid='open-modal']"},
				{Name: "Wait For Modal", Type: "browser_wait", Target: "", WaitFor: &dsl.WaitCondition{Type: "selector", Value: "[role='dialog']", TimeoutMs: 10000}},
				{Name: "Confirm Modal", Type: "browser_click", Target: "", Selector: "[role='dialog'] [data-testid='confirm']"},
				{Name: "Wait For Modal Closed", Type: "browser_wait", Target: "", WaitFor: &dsl.WaitCondition{Type: "selector", Value: "[data-testid='modal-complete']", TimeoutMs: 10000}},
			},
			UsageNotes: []string{"Use visible dialog selectors instead of brittle ordinal object descriptors.", "Capture replay evidence when animations or detached DOM cause intermittent failures."},
		},
		{
			ID:        "web2-auth-callback-flow",
			Kind:      "auth_flow",
			Name:      "Web 2.0 Auth Callback Flow",
			Protocols: []string{"browser", "trueclient2_web"},
			Summary:   "External auth redirect/callback placeholders with readiness waits and post-login assertion.",
			Tags:      []string{"browser", "web2", "auth", "callback"},
			Actions: []dsl.Action{
				{Name: "Start Auth Redirect", Type: "browser_click", Target: "", Selector: "[data-testid='login-sso']"},
				{Name: "Wait For Callback", Type: "browser_wait", Target: "", WaitFor: &dsl.WaitCondition{Type: "url", Value: "/auth/callback", TimeoutMs: 30000}},
				{Name: "Wait For Authenticated Shell", Type: "browser_wait", Target: "", WaitFor: &dsl.WaitCondition{Type: "selector", Value: "[data-testid='authenticated-shell']", TimeoutMs: 15000}},
			},
			UsageNotes: []string{"Use upstream SAML/OIDC gateways and test credentials approved for automation.", "Never store OTPs or production secrets in inline parameter data."},
		},
		{
			ID:        "websocket-subscribe-receive-flow",
			Kind:      "request_group",
			Name:      "WebSocket Subscribe Receive Flow",
			Protocols: []string{"websocket"},
			Summary:   "Connect, subscribe, receive, and close block with message evidence.",
			Tags:      []string{"websocket", "stream", "evidence"},
			Actions: []dsl.Action{
				{Name: "WS Connect", Type: "ws_connect", Target: "/ws"},
				{Name: "WS Subscribe", Type: "ws_send", Body: `{"type":"subscribe","topic":"orders"}`, Params: map[string]any{"type": "text"}},
				{Name: "WS Receive", Type: "ws_receive", Params: map[string]any{"timeout_ms": 5000}, Assertions: []dsl.Assertion{{Type: "message_received"}}},
				{Name: "WS Close", Type: "ws_close"},
			},
			UsageNotes: []string{"Add a message match when the upstream event shape is known.", "Use replay evidence before scaling persistent connections."},
		},
		{
			ID:        "grpc-unary-with-metadata",
			Kind:      "request_group",
			Name:      "gRPC Unary With Metadata",
			Protocols: []string{"grpc"},
			Summary:   "Unary call block with JSON protobuf body, metadata, and status assertion.",
			Tags:      []string{"grpc", "metadata", "status"},
			Actions: []dsl.Action{{
				Name:       "gRPC Unary Call",
				Type:       "grpc_unary",
				Target:     "package.Service/Method",
				Headers:    map[string]string{"authorization": "Bearer {{var.access_token}}"},
				Body:       "{}",
				Params:     map[string]any{"timeout_ms": 30000},
				Assertions: []dsl.Assertion{{Type: "grpc_status", Expected: 0}},
			}},
			UsageNotes: []string{"Attach proto files or enable reflection before replay.", "Keep metadata names lowercase to match gRPC conventions."},
		},
		{
			ID:        "database-query-with-binds",
			Kind:      "request_group",
			Name:      "Database Query With Binds",
			Protocols: []string{"database"},
			Summary:   "Parameterized SQL query with row-count assertion and scalar extraction.",
			Tags:      []string{"database", "sql", "binds"},
			Actions: []dsl.Action{{
				Name:   "Lookup Row",
				Type:   "db_query",
				Target: "select id, name from customers where id = ?",
				Params: map[string]any{"binds": []any{"{{data.customers.id}}"}, "max_rows": 10},
				Extractors: []dsl.Extractor{{
					Name:       "customer_name",
					Source:     "body",
					Type:       "jsonpath",
					Expression: "$.rows[0].name",
				}},
				Assertions: []dsl.Assertion{{Type: "row_count", Expected: 1}},
			}},
			UsageNotes: []string{"Prefer binds over string-concatenated SQL.", "Use read-only mode unless the fixture is approved for writes."},
		},
		{
			ID:        "network-dns-record-check",
			Kind:      "request_group",
			Name:      "Network DNS Record Check",
			Protocols: []string{"network"},
			Summary:   "DNS lookup block with record extraction and count assertion.",
			Tags:      []string{"network", "dns", "records"},
			Actions: []dsl.Action{{
				Name:       "DNS Lookup",
				Type:       "dns_query",
				Target:     "{{env.NETWORK_HOST}}",
				Params:     map[string]any{"record_type": "A"},
				Extractors: []dsl.Extractor{{Name: "resolved_ip", Source: "body", Type: "jsonpath", Expression: "$.records[0]"}},
				Assertions: []dsl.Assertion{{Type: "record_count", Expected: 1}},
			}},
			UsageNotes: []string{"Use local or allow-listed DNS fixtures for CI.", "Review record type and endpoint policy before release gates."},
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
		{
			ID:        "web2-browser-logout-teardown",
			Kind:      "teardown",
			Name:      "Web 2.0 Browser Logout Teardown",
			Protocols: []string{"browser", "trueclient2_web"},
			Summary:   "Browser-native logout, confirmation wait, and final screenshot for review-only Web 2.0 canaries.",
			Tags:      []string{"browser", "web2", "teardown", "logout"},
			Teardown: []dsl.Action{
				{Name: "Open Account Menu", Type: "browser_click", Target: "", Selector: "[data-testid='account-menu']"},
				{Name: "Logout", Type: "browser_click", Target: "", Selector: "[data-testid='logout']"},
				{Name: "Wait For Login Shell", Type: "browser_wait", Target: "", WaitFor: &dsl.WaitCondition{Type: "selector", Value: "[data-testid='login-shell']", TimeoutMs: 10000}, Capture: &dsl.BrowserCaptureOptions{Screenshot: true}},
			},
			UsageNotes: []string{"Prefer this component when the imported TruClient flow logs out through UI controls.", "Keep teardown evidence separate from business transaction success."},
		},
	}
}
