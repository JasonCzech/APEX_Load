package raml

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestConvertRAMLResourcesAndOAuth2(t *testing.T) {
	content := `#%RAML 1.0
title: Accounts API
version: v1
baseUri: https://api.example.test/v1
securitySchemes:
  oauth_2_0:
    type: OAuth 2.0
    settings:
      accessTokenUri: https://login.example.test/oauth/token
/accounts:
  get:
    displayName: List Accounts
  post:
    displayName: Create Account
    body:
      application/json:
        example: |
          {"name":"Acme"}
  /{accountId}:
    get:
      displayName: Get Account
`
	result, err := Convert(ImportRequest{Content: content})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script == nil {
		t.Fatalf("script missing")
	}
	if result.Script.Config.BaseURL != "https://api.example.test/v1" {
		t.Fatalf("base_url = %q", result.Script.Config.BaseURL)
	}
	if len(result.Script.Init) != 1 || result.Script.Init[0].Type != "http_oauth2_token" {
		t.Fatalf("init = %#v, want OAuth token action", result.Script.Init)
	}
	if got := result.Script.Init[0].Params["token_url"]; got != "https://login.example.test/oauth/token" {
		t.Fatalf("token_url = %#v", got)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions = %d, want 3: %#v", len(result.Script.Actions), result.Script.Actions)
	}
	if result.Script.Actions[0].Target != "/accounts" || result.Script.Actions[0].Method != "GET" {
		t.Fatalf("first action = %#v", result.Script.Actions[0])
	}
	if result.Script.Actions[1].Target != "/accounts" || result.Script.Actions[1].Method != "POST" {
		t.Fatalf("second action = %#v", result.Script.Actions[1])
	}
	if !strings.Contains(result.Script.Actions[1].Body, `"name":"Acme"`) {
		t.Fatalf("post body = %q", result.Script.Actions[1].Body)
	}
	if result.Script.Actions[2].Target != "/accounts/{accountId}" {
		t.Fatalf("nested target = %q", result.Script.Actions[2].Target)
	}
	if result.Stats.ConvertedActions != 3 || result.Stats.RequestBodies != 1 {
		t.Fatalf("stats = %#v", result.Stats)
	}
}

func TestConvertRAMLRejectsEmptyResources(t *testing.T) {
	_, err := Convert(ImportRequest{Content: "#%RAML 1.0\ntitle: Empty\n"})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestConvertRAMLBindsPathParametersAndUsesDeclaredStatus(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `#%RAML 1.0
title: Orders
baseUri: https://example.test
/orders:
  /{orderId}:
    uriParameters:
      orderId:
        type: string
        example: ORD-7
    post:
      responses:
        201:
          description: created
`})
	if err != nil {
		t.Fatal(err)
	}
	action := result.Script.Actions[0]
	if action.Target != "/orders/{{param.orderId}}" || len(result.Script.Parameters) != 1 || result.Script.Parameters[0].Values[0] != "ORD-7" {
		t.Fatalf("path binding = action %#v parameters %#v", action, result.Script.Parameters)
	}
	if len(action.Assertions) != 1 || action.Assertions[0].Expected != 201 {
		t.Fatalf("assertions = %#v", action.Assertions)
	}
}

func TestConvertRAMLScopesConflictingPathParameterExamples(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `#%RAML 1.0
title: Scoped IDs
/orders/{id}:
  uriParameters:
    id:
      example: ORD-1
  get:
/users/{id}:
  uriParameters:
    id:
      example: USR-1
  get:
`})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Script.Actions) != 2 || len(result.Script.Parameters) != 2 {
		t.Fatalf("actions/parameters = %#v / %#v", result.Script.Actions, result.Script.Parameters)
	}
	if result.Script.Actions[0].Target == result.Script.Actions[1].Target {
		t.Fatalf("conflicting resources share target binding: %#v", result.Script.Actions)
	}
	values := map[string]string{}
	for _, parameter := range result.Script.Parameters {
		values[parameter.Name] = parameter.Values[0]
	}
	if values["id"] != "ORD-1" {
		t.Fatalf("first binding = %#v", values)
	}
	if !strings.Contains(result.Script.Actions[1].Target, "{{param.id_get_users_id}}") || values["id_get_users_id"] != "USR-1" {
		t.Fatalf("second binding/action = %#v / %#v", values, result.Script.Actions[1])
	}
}

func TestConvertRAMLScopedPathParameterNamesRemainUniqueAfterNormalizationCollision(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `#%RAML 1.0
title: Colliding scoped IDs
/foo--bar/{id}:
  uriParameters:
    id: {example: DOUBLE}
  get:
/foo-bar/{id}:
  uriParameters:
    id: {example: DASH}
  get:
/foo_bar/{id}:
  uriParameters:
    id: {example: UNDERSCORE}
  get:
`})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Script.Actions) != 3 || len(result.Script.Parameters) != 3 {
		t.Fatalf("actions/parameters = %#v / %#v", result.Script.Actions, result.Script.Parameters)
	}
	values := make(map[string]string, len(result.Script.Parameters))
	for _, parameter := range result.Script.Parameters {
		if _, duplicate := values[parameter.Name]; duplicate {
			t.Fatalf("duplicate scoped parameter name %q: %#v", parameter.Name, result.Script.Parameters)
		}
		if len(parameter.Values) != 1 {
			t.Fatalf("parameter values = %#v", parameter)
		}
		values[parameter.Name] = parameter.Values[0]
	}
	for _, action := range result.Script.Actions {
		start := strings.Index(action.Target, "{{param.")
		end := strings.Index(action.Target, "}}")
		if start < 0 || end <= start+8 {
			t.Fatalf("action has no parameter binding: %#v", action)
		}
		name := action.Target[start+8 : end]
		if _, ok := values[name]; !ok {
			t.Fatalf("action references missing parameter %q: parameters=%#v", name, result.Script.Parameters)
		}
	}
}

func TestConvertRAMLMultipleSuccessResponsesUseSuccessRange(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `#%RAML 1.0
title: Multiple outcomes
/jobs:
  post:
    responses:
      201: {}
      202: {}
`})
	if err != nil {
		t.Fatal(err)
	}
	assertions := result.Script.Actions[0].Assertions
	if len(assertions) != 1 || assertions[0].Type != "status_code_range" || assertions[0].Min != 200 || assertions[0].Max != 299 {
		t.Fatalf("assertions = %#v, want 2xx range", assertions)
	}
}

func TestConvertRAMLReviewGatesUnboundPathParameter(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `#%RAML 1.0
title: Orders
/orders/{orderId}:
  get:
`})
	if err != nil {
		t.Fatal(err)
	}
	action := result.Script.Actions[0]
	if action.Metadata["execution_blocker"] != "import_review_required" || len(result.Unsupported) == 0 {
		t.Fatalf("unbound action = %#v diagnostics=%#v", action, result.Unsupported)
	}
}

func TestConvertRAMLClientIDEnforcement(t *testing.T) {
	content := `#%RAML 1.0
title: Secured API
baseUri: https://api.example.test
securitySchemes:
  client_id_required:
    type: Pass Through
    describedBy:
      headers:
        client_id:
          type: string
        client_secret:
          type: string
/things:
  get:
    displayName: List Things
`
	result, err := Convert(ImportRequest{Content: content})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Summary.Policies) != 1 || result.Summary.Policies[0].Policy != "client_id_enforcement" {
		t.Fatalf("policies = %#v, want client_id_enforcement", result.Summary.Policies)
	}
	if len(result.Script.Actions) == 0 {
		t.Fatalf("no actions converted")
	}
	headers := result.Script.Actions[0].Headers
	if headers["client_id"] != "{{env.RAML_CLIENT_ID}}" || headers["client_secret"] != "{{secret.RAML_CLIENT_SECRET}}" {
		t.Fatalf("headers = %#v, want env/secret client credentials", headers)
	}
	if len(result.Warnings) == 0 {
		t.Fatalf("warnings = %#v, want client-credential warning", result.Warnings)
	}
}

func TestConvertRAMLResolvesIncludesFromFiles(t *testing.T) {
	content := `#%RAML 1.0
title: Inc API
baseUri: https://api.example.test
/widgets:
  post:
    displayName: Create Widget
    body:
      application/json:
        example: !include examples/widget.json
/orders: !include resources/orders.raml
`
	files := map[string][]byte{
		"examples/widget.json":  []byte(`{"name":"Widget"}`),
		"resources/orders.raml": []byte("get:\n  displayName: List Orders\n"),
	}
	result, err := Convert(ImportRequest{Content: content, Files: files})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	var widget, orders *dsl.Action
	for i := range result.Script.Actions {
		action := &result.Script.Actions[i]
		switch action.Target {
		case "/widgets":
			widget = action
		case "/orders":
			orders = action
		}
	}
	if widget == nil || widget.Method != "POST" || !strings.Contains(widget.Body, `"name":"Widget"`) {
		t.Fatalf("widget action = %#v, want POST with included JSON body", widget)
	}
	if orders == nil || orders.Method != "GET" {
		t.Fatalf("orders action = %#v, want GET from included resource", orders)
	}
}

func TestConvertRAMLUnresolvedIncludeErrors(t *testing.T) {
	content := "#%RAML 1.0\ntitle: Inc\nbaseUri: https://api.example.test\n/orders: !include resources/orders.raml\n"
	if _, err := Convert(ImportRequest{Content: content}); err == nil {
		t.Fatalf("expected unresolved-include error")
	}
}

func TestConvertRAMLKeepsNonStructuredIncludeAsRawText(t *testing.T) {
	content := `#%RAML 1.0
title: Raw Body API
baseUri: https://api.example.test
/things:
  post:
    body:
      text/plain:
        example: !include body.txt
`
	// body.txt is valid YAML (a mapping) but must be preserved verbatim, not
	// reparsed into a JSON object.
	files := map[string][]byte{"body.txt": []byte("key: value\nother: 2")}
	result, err := Convert(ImportRequest{Content: content, Files: files})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	body := result.Script.Actions[0].Body
	if body != "key: value\nother: 2" {
		t.Fatalf("body = %q, want verbatim raw text", body)
	}
}

func TestLookupIncludeBasenameCollisionIsDeterministic(t *testing.T) {
	files := map[string][]byte{
		"d2/w.json": []byte("two"),
		"d1/w.json": []byte("one"),
		"d3/w.json": []byte("three"),
	}
	for i := 0; i < 50; i++ {
		content, ok := lookupInclude(files, "x/w.json")
		if !ok || string(content) != "one" {
			t.Fatalf("lookupInclude = %q, %v; want deterministic 'one' (smallest key)", string(content), ok)
		}
	}
}

func TestConvertRAMLApiKeySchemeIsNotClientIDEnforcement(t *testing.T) {
	content := `#%RAML 1.0
title: API-Key API
baseUri: https://api.example.test
securitySchemes:
  api_key:
    type: Pass Through
    describedBy:
      headers:
        api_key:
          type: string
/things:
  get:
    displayName: List Things
`
	result, err := Convert(ImportRequest{Content: content})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Summary.Policies) != 0 {
		t.Fatalf("policies = %#v, want no client_id_enforcement for a pure api-key scheme", result.Summary.Policies)
	}
	if got := result.Script.Actions[0].Headers["client_id"]; got != "" {
		t.Fatalf("client_id header = %q, want none for api-key scheme", got)
	}
}
