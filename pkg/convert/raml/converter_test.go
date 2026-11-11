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
	if len(result.SecretRequirements) != 1 {
		t.Fatalf("secret requirements = %#v, want one grouped RAML client secret", result.SecretRequirements)
	}
	requirement := result.SecretRequirements[0]
	if requirement.SuggestedName != "RAML_CLIENT_SECRET" ||
		requirement.Replacement != "{{secret.RAML_CLIENT_SECRET}}" ||
		requirement.SourceValueAvailable || requirement.Value != "" ||
		requirement.Source.Format != "raml" || requirement.Source.Path != "raml.yaml" ||
		len(requirement.Targets) != 1 || requirement.Targets[0].Field != "actions[0].headers.client_secret" ||
		!strings.Contains(requirement.Context, "does not contain a credential value") {
		t.Fatalf("RAML client-secret requirement lacks descriptive provenance: %#v", requirement)
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

func TestLookupIncludeRejectsBasenameSubstitution(t *testing.T) {
	files := map[string][]byte{
		"d2/w.json": []byte("two"),
		"d1/w.json": []byte("one"),
		"d3/w.json": []byte("three"),
	}
	for i := 0; i < 50; i++ {
		content, ok := lookupInclude(files, "x/w.json")
		if ok {
			t.Fatalf("lookupInclude = %q, %v; an unrelated basename must not resolve", string(content), ok)
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

func TestConvertRAMLClientIDEnforcementPreservesAuthoredDefaults(t *testing.T) {
	content := `#%RAML 1.0
title: Authored Scheme API
baseUri: https://api.example.test
securitySchemes:
  client_id_required:
    type: Pass Through
    describedBy:
      headers:
        client_id:
          type: string
          default: scheme-client-id
        client_secret:
          type: string
          example: scheme-client-secret
/things:
  get:
    displayName: List Things
`
	result, err := Convert(ImportRequest{Content: content})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	headers := result.Script.Actions[0].Headers
	if headers["client_id"] != "scheme-client-id" || headers["client_secret"] != "scheme-client-secret" {
		t.Fatalf("headers = %#v, want preserved scheme client credentials", headers)
	}
	if len(result.SecretRequirements) != 0 {
		t.Fatalf("secret requirements = %#v, want none when scheme credentials have authored values", result.SecretRequirements)
	}
}

func TestRAMLGeneratedCredentialRequirementsDeterministicHeaderOrder(t *testing.T) {
	for i := 0; i < 32; i++ {
		script := &dsl.Script{
			Name:     "Multi Header Secret",
			Protocol: "http",
			Actions: []dsl.Action{{
				Name:   "Call",
				Type:   "GET",
				Method: "GET",
				Target: "/things",
				Headers: map[string]string{
					"z_client_secret": "{{secret.RAML_CLIENT_SECRET}}",
					"a_client_secret": "{{secret.RAML_CLIENT_SECRET}}",
					"m_client_secret": "{{secret.RAML_CLIENT_SECRET}}",
				},
			}},
		}
		reqs, err := ramlGeneratedCredentialRequirements(script, "api.raml")
		if err != nil {
			t.Fatalf("iter %d ramlGeneratedCredentialRequirements: %v", i, err)
		}
		if len(reqs) != 1 || len(reqs[0].Targets) != 3 {
			t.Fatalf("iter %d reqs = %#v, want 1 requirement with 3 sorted targets", i, reqs)
		}
		if reqs[0].Targets[0].Field != "actions[0].headers.a_client_secret" ||
			reqs[0].Targets[1].Field != "actions[0].headers.m_client_secret" ||
			reqs[0].Targets[2].Field != "actions[0].headers.z_client_secret" {
			t.Fatalf("iter %d targets = %#v, want deterministic alphabetical header order", i, reqs[0].Targets)
		}
	}
}

func TestRAMLAuthoredClientSecretTemplateInBodyOrCompositeHeaderDoesNotFail(t *testing.T) {
	content := `#%RAML 1.0
title: Authored Secret Template API
baseUri: https://api.example.test
/tokens:
  post:
    headers:
      Authorization:
        type: string
        example: "Basic {{secret.RAML_CLIENT_SECRET}}"
    body:
      application/json:
        example: '{"client_secret":"{{secret.RAML_CLIENT_SECRET}}"}'
`
	result, err := Convert(ImportRequest{Content: content})
	if err != nil {
		t.Fatalf("Convert returned error on authored {{secret.RAML_CLIENT_SECRET}}: %v", err)
	}
	if len(result.SecretRequirements) != 1 || len(result.SecretRequirements[0].Targets) != 2 {
		t.Fatalf("SecretRequirements = %#v, want 1 requirement covering composite header and body targets", result.SecretRequirements)
	}
}

func TestRAMLClientIDEnforcementLocationsAndSpellings(t *testing.T) {
	t.Run("query_parameters_unauthored_and_authored", func(t *testing.T) {
		unauthored := `#%RAML 1.0
title: Query Policy API
baseUri: https://api.example.test
securitySchemes:
  client_id_enforcement:
    type: x-client-id-enforcement
    describedBy:
      queryParameters:
        client_id:
          type: string
        client_secret:
          type: string
/orders:
  get:
    displayName: List Orders
`
		res, err := Convert(ImportRequest{Content: unauthored})
		if err != nil {
			t.Fatalf("Convert unauthored query policy: %v", err)
		}
		act := res.Script.Actions[0]
		if _, hasHeader := act.Headers["client_id"]; hasHeader {
			t.Fatalf("unexpected client_id header when scheme uses queryParameters: %#v", act.Headers)
		}
		if _, hasHeader := act.Headers["client_secret"]; hasHeader {
			t.Fatalf("unexpected client_secret header when scheme uses queryParameters: %#v", act.Headers)
		}
		if act.Target != "/orders?client_id={{env.RAML_CLIENT_ID}}&client_secret={{secret.RAML_CLIENT_SECRET}}" {
			t.Fatalf("act.Target = %q, want query client_id and client_secret placeholders", act.Target)
		}
		if len(res.SecretRequirements) != 1 || len(res.SecretRequirements[0].Targets) != 1 || res.SecretRequirements[0].Targets[0].Field != "actions[0].target" {
			t.Fatalf("SecretRequirements = %#v, want actions[0].target", res.SecretRequirements)
		}

		authored := `#%RAML 1.0
title: Authored Query Policy API
baseUri: https://api.example.test
securitySchemes:
  client_id_enforcement:
    type: x-client-id-enforcement
    describedBy:
      queryParameters:
        client_id:
          type: string
          example: query-app-id
        client_secret:
          type: string
          default: query-app-secret
/orders:
  get:
    displayName: List Orders
`
		resAuthored, err := Convert(ImportRequest{Content: authored})
		if err != nil {
			t.Fatalf("Convert authored query policy: %v", err)
		}
		actAuthored := resAuthored.Script.Actions[0]
		if actAuthored.Target != "/orders?client_id=query-app-id&client_secret=query-app-secret" {
			t.Fatalf("actAuthored.Target = %q, want preserved query client credentials", actAuthored.Target)
		}
		if len(resAuthored.SecretRequirements) != 0 {
			t.Fatalf("SecretRequirements = %#v, want none when query credentials are authored", resAuthored.SecretRequirements)
		}
	})

	t.Run("custom_header_spellings_and_dotted_traits_without_duplicate_injection", func(t *testing.T) {
		customHeaders := `#%RAML 1.0
title: Custom Header Policy API
baseUri: https://api.example.test
securitySchemes:
  custom_client_policy:
    type: Pass Through
    describedBy:
      headers:
        X-Client-Id:
          type: string
          example: custom-id
        X-Client-Secret:
          type: string
/items:
  get:
    displayName: List Items
  post:
    displayName: Create Item With Dotted Override
    headers:
      client.id:
        type: string
        example: dotted-id
      client.secret:
        type: string
        example: dotted-secret
`
		res, err := Convert(ImportRequest{Content: customHeaders})
		if err != nil {
			t.Fatalf("Convert custom headers policy: %v", err)
		}
		getHeaders := res.Script.Actions[0].Headers
		if getHeaders["X-Client-Id"] != "custom-id" || getHeaders["X-Client-Secret"] != "{{secret.RAML_CLIENT_SECRET}}" {
			t.Fatalf("GET headers = %#v, want X-Client-Id and X-Client-Secret", getHeaders)
		}
		if _, dup := getHeaders["client_id"]; dup {
			t.Fatalf("GET headers injected duplicate client_id: %#v", getHeaders)
		}
		if _, dup := getHeaders["client_secret"]; dup {
			t.Fatalf("GET headers injected duplicate client_secret: %#v", getHeaders)
		}
		postHeaders := res.Script.Actions[1].Headers
		if postHeaders["client.id"] != "dotted-id" || postHeaders["client.secret"] != "dotted-secret" {
			t.Fatalf("POST headers = %#v, want preserved dotted client credentials", postHeaders)
		}
		if _, dup := postHeaders["X-Client-Id"]; dup {
			t.Fatalf("POST headers injected duplicate X-Client-Id alongside client.id: %#v", postHeaders)
		}
		if _, dup := postHeaders["X-Client-Secret"]; dup {
			t.Fatalf("POST headers injected duplicate X-Client-Secret alongside client.secret: %#v", postHeaders)
		}
		if len(res.SecretRequirements) != 1 || len(res.SecretRequirements[0].Targets) != 1 || res.SecretRequirements[0].Targets[0].Field != "actions[0].headers.X-Client-Secret" {
			t.Fatalf("SecretRequirements = %#v, want single target actions[0].headers.X-Client-Secret", res.SecretRequirements)
		}
	})

	t.Run("oauth_client_name_client_id_only_secured_by_null_optional_auth_empty_example_and_composite_templates", func(t *testing.T) {
		spec := `#%RAML 1.0
title: Service {{run.id}}
baseUri: https://api.example.test
securitySchemes:
  oauth_client_id:
    type: OAuth 2.0
    settings:
      accessTokenUri: https://auth.example.test/oauth/token
      authorizationGrants: [client_credentials]
  only_client_id_policy:
    type: x-client-id-enforcement
    describedBy:
      headers:
        client_id:
          type: string
          example: only-id-val
/secured:
  get:
    displayName: Secured Endpoint
    headers:
      Authorization?:
        type: string
      X-Optional-Secret:
        type: string
        required: false
      X-Empty-Secret:
        type: string
        example: ""
    queryParameters:
      composite:
        type: string
        example: "{{env.A}}&{{env.B}}"
      tpl_body:
        type: string
        example: "{{run.name}}:{{file.missing}}"
/public:
  securedBy: [null]
  get:
    displayName: Public Endpoint
`
		res, err := Convert(ImportRequest{Content: spec})
		if err != nil {
			t.Fatalf("Convert: %v", err)
		}
		if issues := dsl.ValidateScriptIssues(res.Script); len(issues) != 0 {
			t.Fatalf("ValidateScriptIssues = %#v, want empty", issues)
		}
		securedAct := res.Script.Actions[1]
		if securedAct.Headers["client_id"] != "only-id-val" {
			t.Fatalf("securedAct client_id = %q, want only-id-val", securedAct.Headers["client_id"])
		}
		if _, hasSecret := securedAct.Headers["client_secret"]; hasSecret {
			t.Fatalf("securedAct unexpectedly injected undeclared client_secret: %#v", securedAct.Headers)
		}
		if _, hasOptSecret := securedAct.Headers["X-Optional-Secret"]; hasOptSecret {
			t.Fatalf("securedAct unexpectedly synthesized optional secret header: %#v", securedAct.Headers)
		}
		if gotEmpty, exists := securedAct.Headers["X-Empty-Secret"]; !exists || gotEmpty != "" {
			t.Fatalf("securedAct X-Empty-Secret = %q (exists=%v), want empty string preserved", gotEmpty, exists)
		}
		if !strings.Contains(securedAct.Target, "composite=%7B%7Benv.A%7D%7D%26%7B%7Benv.B%7D%7D") {
			t.Fatalf("securedAct.Target = %q, want composite query template URL-escaped", securedAct.Target)
		}
		publicAct := res.Script.Actions[0]
		if _, hasAuth := publicAct.Headers["Authorization"]; hasAuth {
			t.Fatalf("publicAct with securedBy:[null] unexpectedly received Authorization header: %#v", publicAct.Headers)
		}
		if _, hasClientID := publicAct.Headers["client_id"]; hasClientID {
			t.Fatalf("publicAct with securedBy:[null] unexpectedly received client_id header: %#v", publicAct.Headers)
		}
	})

	t.Run("plural_examples_wrapped_uri_params_oauth_header_preservation_and_unsecured_client_id_fallback", func(t *testing.T) {
		spec := `#%RAML 1.0
title: Plural Examples Service
baseUri: https://{env}.example.test/{tenant}
baseUriParameters:
  env:
    type: string
    examples:
      prod:
        value: api-prod
  tenant:
    type: string
    example:
      value: acme
securitySchemes:
  oauth_2_0:
    type: OAuth 2.0
    settings:
      accessTokenUri: https://auth.example.test/oauth/token
      authorizationGrants: [client_credentials]
  client_id_scheme:
    type: x-client-id-enforcement
    describedBy:
      headers:
        client_id:
          type: string
          examples:
            default:
              value: scheme-client-id-from-examples
securedBy: [oauth_2_0, client_id_scheme]
/items/{itemId}:
  uriParameters:
    itemId:
      type: string
      examples:
        sample:
          value: item-42
  get:
    displayName: Get Item
    headers:
      Authorization:
        type: string
/unsecured:
  securedBy: [null]
  get:
    displayName: Unsecured With Local Client ID
    headers:
      client_id:
        type: string
`
		res, err := Convert(ImportRequest{Content: spec})
		if err != nil {
			t.Fatalf("Convert: %v", err)
		}
		if issues := dsl.ValidateScriptIssues(res.Script); len(issues) != 0 {
			t.Fatalf("ValidateScriptIssues = %#v, want empty", issues)
		}
		if res.Script.Config.BaseURL != "https://api-prod.example.test/acme" {
			t.Fatalf("BaseURL = %q, want https://api-prod.example.test/acme", res.Script.Config.BaseURL)
		}
		var getItem, unsecured *dsl.Action
		for i := range res.Script.Actions {
			switch res.Script.Actions[i].Name {
			case "Get Item":
				getItem = &res.Script.Actions[i]
			case "Unsecured With Local Client ID":
				unsecured = &res.Script.Actions[i]
			}
		}
		if getItem == nil || unsecured == nil {
			t.Fatalf("missing expected actions in %+v", res.Script.Actions)
		}
		if getItem.Headers["Authorization"] != "Bearer {{var.access_token}}" {
			t.Fatalf("getItem Authorization = %q, want Bearer {{var.access_token}} (not overwritten by {{secret.RAML_AUTHORIZATION}})", getItem.Headers["Authorization"])
		}
		if getItem.Headers["client_id"] != "scheme-client-id-from-examples" {
			t.Fatalf("getItem client_id = %q, want scheme-client-id-from-examples", getItem.Headers["client_id"])
		}
		if unsecured.Headers["client_id"] != "{{env.RAML_CLIENT_ID}}" {
			t.Fatalf("unsecured client_id = %q, want {{env.RAML_CLIENT_ID}} when securedBy:[null] bypasses scheme", unsecured.Headers["client_id"])
		}
	})
}
