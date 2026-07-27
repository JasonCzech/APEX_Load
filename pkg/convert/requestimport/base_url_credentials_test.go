package requestimport

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestFinalizeScriptProjectsBaseURLCredentialsAndGatesUserinfo(t *testing.T) {
	const canary = "base-url-password-canary"
	script := validBaseScript()
	script.GeneratedFrom = "postman"
	script.Config.BaseURL = "https://service:" + canary + "@api.example.test?api_key=query%2Dsecret"
	script.Actions = append(script.Actions, dsl.Action{
		Name:   "nested target",
		Type:   "request",
		Method: "GET",
		Target: "/orders",
	})

	result, err := FinalizeScript(script, nil, FinalizeOptions{})
	if err != nil {
		t.Fatalf("FinalizeScript returned error: %v", err)
	}
	if strings.Contains(result.Script.Config.BaseURL, canary) ||
		strings.Contains(result.Script.Config.BaseURL, "query%2Dsecret") {
		t.Fatalf("base URL retained source credentials: %q", result.Script.Config.BaseURL)
	}
	if !strings.Contains(result.Script.Config.BaseURL, "{{secret.") {
		t.Fatalf("base URL = %q, want governed references", result.Script.Config.BaseURL)
	}
	if len(result.Requirements) != 2 {
		t.Fatalf("requirements = %#v, want userinfo and query requirements", result.Requirements)
	}
	values := map[string]bool{}
	for _, requirement := range result.Requirements {
		values[requirement.Value] = true
	}
	if !values["service:"+canary] || !values["query%2Dsecret"] {
		t.Fatalf("protected requirement values = %#v", values)
	}
	for index, action := range result.Script.Actions {
		if action.Metadata["execution_blocker"] != "credential_url_userinfo_review_required" {
			t.Fatalf("actions[%d] metadata = %#v, want durable base-URL userinfo gate", index, action.Metadata)
		}
	}
}

func TestFinalizeScriptBaseURLSecretNameDoesNotCollideWithExistingReference(t *testing.T) {
	script := validBaseScript()
	script.Config.BaseURL = "https://api.example.test?api_key=base-secret"
	script.Actions = []dsl.Action{{
		Name:   "request",
		Type:   "request",
		Method: "GET",
		Target: "/",
		Headers: map[string]string{
			"X-Existing": "{{secret.IMPORT_BASE_URL_API_KEY}}",
		},
		HeaderList: dsl.HeaderList{{
			Name:  "X-Existing",
			Value: "{{secret.IMPORT_BASE_URL_API_KEY}}",
		}},
	}}

	result, err := FinalizeScript(script, nil, FinalizeOptions{})
	if err != nil {
		t.Fatalf("FinalizeScript returned error: %v", err)
	}
	if len(result.Requirements) != 1 {
		t.Fatalf("requirements = %#v, want one base URL requirement", result.Requirements)
	}
	if result.Requirements[0].SuggestedName == "IMPORT_BASE_URL_API_KEY" {
		t.Fatalf("base URL requirement collided with existing script reference: %#v", result.Requirements[0])
	}
}

func TestDraftFinalizeProjectsBaseURLAndCountsUnresolvedBinding(t *testing.T) {
	script := validBaseScript()
	script.GeneratedFrom = "wsdl"
	script.Config.BaseURL = "https://api.example.test?api_key=draft%2Dsecret"
	script.Actions = []dsl.Action{{
		Name:   "request",
		Type:   "request",
		Method: "GET",
		Target: "/",
	}}

	result, err := (&Draft{Script: script}).Finalize(FinalizeOptions{})
	if err != nil {
		t.Fatalf("Draft.Finalize returned error: %v", err)
	}
	if len(result.Requirements) != 1 ||
		result.Requirements[0].Value != "draft%2Dsecret" ||
		strings.Contains(result.Script.Config.BaseURL, "draft%2Dsecret") {
		t.Fatalf("base URL projection = %q / %#v", result.Script.Config.BaseURL, result.Requirements)
	}
	if result.Readiness.State != importreview.Blocked ||
		!reflect.DeepEqual(result.Readiness.BlockingCodes, []string{"credential_bindings_required"}) {
		t.Fatalf("readiness = %#v, want unresolved base credential binding", result.Readiness)
	}
}

func TestFinalizeScriptProjectsBaseCredentialFromOAuthInitURL(t *testing.T) {
	const canary = "oauth-base-password"
	rawBase := "https://service:" + canary + "@api.example.test"
	rawTokenURL := rawBase + "/oauth/token"
	script := validBaseScript()
	script.GeneratedFrom = "openapi"
	script.Config.BaseURL = rawBase
	script.Init = []dsl.Action{{
		Name:   "OAuth token",
		Type:   "http_oauth2_token",
		Target: rawTokenURL,
		Params: map[string]any{
			"token_url":     rawTokenURL,
			"grant_type":    "client_credentials",
			"client_id":     "{{env.CLIENT_ID}}",
			"client_secret": "{{secret.CLIENT_SECRET}}",
			"save_as":       "access_token",
		},
	}}
	script.Actions = []dsl.Action{{
		Name:   "request",
		Type:   "request",
		Method: "GET",
		Target: "/orders",
	}}

	result, err := FinalizeScript(script, nil, FinalizeOptions{})
	if err != nil {
		t.Fatalf("FinalizeScript returned error: %v", err)
	}
	if len(result.Requirements) != 1 {
		t.Fatalf("requirements = %#v, want one structurally reused base credential", result.Requirements)
	}
	init := result.Script.Init[0]
	if strings.Contains(init.Target, canary) ||
		init.Params["token_url"] != init.Target {
		t.Fatalf("OAuth init retained or diverged from the projected URL: %#v", init)
	}
	if !strings.Contains(init.Target, result.Requirements[0].Replacement) {
		t.Fatalf("OAuth init target = %q, want its occurrence-scoped replacement %#v", init.Target, result.Requirements)
	}
	if init.Metadata["execution_blocker"] != "credential_url_userinfo_review_required" {
		t.Fatalf("OAuth init did not receive the durable userinfo gate: %#v", init.Metadata)
	}
}

func TestFinalizeScriptProjectsDistinctOAuthTokenURLWithoutTarget(t *testing.T) {
	const canary = "oauth-token-url-password"
	rawTokenURL := "https://oauth-user:" + canary + "@auth.example.test/oauth/token"
	script := validBaseScript()
	script.GeneratedFrom = "openapi"
	script.Config.BaseURL = "https://api.example.test"
	script.Init = []dsl.Action{{
		Name: "OAuth token",
		Type: "http_oauth2_token",
		Params: map[string]any{
			"token_url":     rawTokenURL,
			"grant_type":    "client_credentials",
			"client_id":     "{{env.CLIENT_ID}}",
			"client_secret": "{{secret.CLIENT_SECRET}}",
			"save_as":       "access_token",
		},
	}}
	script.Actions = []dsl.Action{{
		Name:   "request",
		Type:   "GET",
		Method: "GET",
		Target: "/orders",
	}}

	result, err := FinalizeScript(script, nil, FinalizeOptions{})
	if err != nil {
		t.Fatalf("FinalizeScript returned error: %v", err)
	}
	if len(result.Requirements) != 1 || result.Requirements[0].Value != "oauth-user:"+canary {
		t.Fatalf("requirements = %#v, want protected OAuth token URL userinfo", result.Requirements)
	}
	init := result.Script.Init[0]
	tokenURL, _ := init.Params["token_url"].(string)
	if strings.Contains(tokenURL, canary) ||
		!strings.Contains(tokenURL, result.Requirements[0].Replacement) {
		t.Fatalf("OAuth params.token_url retained source credential: %#v", init)
	}
	if len(result.Requirements[0].Targets) != 1 ||
		result.Requirements[0].Targets[0].Field != "params.token_url.userinfo" {
		t.Fatalf("OAuth requirement targets = %#v", result.Requirements[0].Targets)
	}
	if init.Metadata["execution_blocker"] != "credential_url_userinfo_review_required" {
		t.Fatalf("OAuth init did not receive a durable token-URL gate: %#v", init.Metadata)
	}
}

func TestFinalizeScriptTreatsOAuthTokenURLAsAuthoritativeWhenTargetMirrorsIt(t *testing.T) {
	const canary = "oauth-mirrored-password"
	rawTokenURL := "https://oauth-user:" + canary + "@auth.example.test/oauth/token"
	script := validBaseScript()
	script.GeneratedFrom = "openapi"
	script.Init = []dsl.Action{{
		Name:   "OAuth token",
		Type:   "http_oauth2_token",
		Target: rawTokenURL,
		Params: map[string]any{
			"token_url":     rawTokenURL,
			"grant_type":    "client_credentials",
			"client_id":     "{{env.CLIENT_ID}}",
			"client_secret": "{{secret.CLIENT_SECRET}}",
			"save_as":       "access_token",
		},
	}}
	script.Actions = []dsl.Action{{
		Name:   "request",
		Type:   "GET",
		Method: "GET",
		Target: "/orders",
	}}

	result, err := FinalizeScript(script, nil, FinalizeOptions{})
	if err != nil {
		t.Fatalf("FinalizeScript returned error: %v", err)
	}
	if len(result.Requirements) != 1 ||
		len(result.Requirements[0].Targets) != 1 ||
		result.Requirements[0].Targets[0].Field != "params.token_url.userinfo" {
		t.Fatalf("OAuth requirements = %#v, want one executable token-URL target", result.Requirements)
	}
	init := result.Script.Init[0]
	if init.Target != init.Params["token_url"] ||
		strings.Contains(init.Target, canary) ||
		!strings.Contains(init.Target, result.Requirements[0].Replacement) {
		t.Fatalf("OAuth mirror did not follow projected executable URL: %#v", init)
	}
}

// TestFinalizeScriptGovernsRuntimeVariableInSensitiveBaseURL pins the same rule
// as TestProjectScriptCredentialsGovernsBaseURLRuntimeVariables on the
// FinalizeScript path: config.base_url is flattened once at protocol init and
// only {{secret.*}} is resolved there, so an extractor-derived {{var.*}} in the
// base URL can never be executable and must become a binding.
func TestFinalizeScriptGovernsRuntimeVariableInSensitiveBaseURL(t *testing.T) {
	const baseURL = "https://api.example.test?access_token={{var.access_token}}"
	script := validBaseScript()
	script.GeneratedFrom = "har"
	script.Config.BaseURL = baseURL
	script.Init = []dsl.Action{{
		Name:   "bootstrap",
		Type:   "GET",
		Method: "GET",
		Target: "https://auth.example.test/bootstrap",
		Extractors: []dsl.Extractor{{
			Name:       "access_token",
			Type:       "jsonpath",
			Expression: "$.access_token",
		}},
	}}
	script.Actions = []dsl.Action{{
		Name:   "request",
		Type:   "GET",
		Method: "GET",
		Target: "/orders",
	}}

	result, err := FinalizeScript(script, nil, FinalizeOptions{})
	if err != nil {
		t.Fatalf("FinalizeScript returned error: %v", err)
	}
	if !strings.Contains(result.Script.Config.BaseURL, "{{secret.") {
		t.Fatalf("base URL = %q, want runtime variable governed as a secret", result.Script.Config.BaseURL)
	}
	if len(result.Requirements) != 1 ||
		result.Requirements[0].DeferredSourceExpression != "{{var.access_token}}" {
		t.Fatalf("requirements = %#v, want protected base URL variable", result.Requirements)
	}
}

func TestFinalizeScriptDoesNotTrustStaticParameterAsBaseURLCredentialProvenance(t *testing.T) {
	script := validBaseScript()
	script.GeneratedFrom = "har"
	script.Config.BaseURL = "https://api.example.test?access_token={{var.access_token}}"
	script.Parameters = []dsl.Parameter{{
		Name:   "access_token",
		Type:   "list",
		Values: []string{"static-token-value"},
	}}
	script.Actions = []dsl.Action{{
		Name:   "request",
		Type:   "GET",
		Method: "GET",
		Target: "/orders",
	}}

	result, err := FinalizeScript(script, nil, FinalizeOptions{})
	if err != nil {
		t.Fatalf("FinalizeScript returned error: %v", err)
	}
	if !strings.Contains(result.Script.Config.BaseURL, "{{secret.") {
		t.Fatalf("base URL = %q, want static parameter governed as a secret", result.Script.Config.BaseURL)
	}
	if len(result.Requirements) != 1 {
		t.Fatalf("requirements = %#v, want static base credential binding", result.Requirements)
	}
}

func TestFinalizeScriptScopesBaseURLUserinfoGateToAffectedRequests(t *testing.T) {
	script := validBaseScript()
	script.Config.BaseURL = "https://base-user:base-password@api.example.test"
	script.Actions = []dsl.Action{
		{
			Name:   "relative",
			Type:   "GET",
			Method: "GET",
			Target: "/orders",
		},
		{
			Name:   "absolute",
			Type:   "GET",
			Method: "GET",
			Target: "https://other.example.test/status",
		},
	}

	result, err := FinalizeScript(script, nil, FinalizeOptions{})
	if err != nil {
		t.Fatalf("FinalizeScript returned error: %v", err)
	}
	if result.Script.Actions[0].Metadata["execution_blocker"] != "credential_url_userinfo_review_required" {
		t.Fatalf("relative request metadata = %#v, want base URL gate", result.Script.Actions[0].Metadata)
	}
	if blocker := result.Script.Actions[1].Metadata["execution_blocker"]; blocker != nil {
		t.Fatalf("absolute request received unrelated base URL blocker %v: %#v", blocker, result.Script.Actions[1].Metadata)
	}
}
