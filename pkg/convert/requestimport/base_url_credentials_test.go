package requestimport

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
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
	if got := result.Script.Config.BaseURL; got != script.Config.BaseURL {
		t.Fatalf("base URL = %q, want imported bytes %q", got, script.Config.BaseURL)
	}
	if len(result.Requirements) != 0 {
		t.Fatalf("requirements = %#v, preserved values must not require reentry", result.Requirements)
	}
	for index, action := range result.Script.Actions {
		if blocker := action.Metadata["execution_blocker"]; blocker != nil {
			t.Fatalf("actions[%d] metadata = %#v, preserved credential must not block execution", index, action.Metadata)
		}
	}
}

func TestFinalizeActionDoesNotAttachEffectiveBaseURLLocatorToAction(t *testing.T) {
	t.Parallel()

	action, report, err := FinalizeAction(EffectiveRequest{
		Key:     "relative-orders",
		Name:    "Orders",
		Method:  "GET",
		BaseURL: "https://operator:base-url-canary-17f4@api.example.test",
		Target:  "/orders",
		Source:  SourceRef{Format: "fixture", Pointer: "/requests/0"},
	}, FinalizeOptions{
		Credentials: &CredentialProjectionOptions{Mode: credentialpolicy.Preserve},
		ActionPath:  "actions[0]",
	})
	if err != nil {
		t.Fatalf("FinalizeAction: %v", err)
	}
	if action.Target != "/orders" || report.Disposition != ExecutableExact {
		t.Fatalf("action/report = %#v / %#v", action, report)
	}
	for _, target := range preservedCredentialTargetsFromMetadata(action.Metadata) {
		if strings.Contains(strings.ToLower(target.Field), "base_url") {
			t.Fatalf("base URL locator was attached to action target: %#v", target)
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
	if got := result.Script.Config.BaseURL; got != script.Config.BaseURL {
		t.Fatalf("base URL = %q, want imported value %q", got, script.Config.BaseURL)
	}
	if len(result.Requirements) != 0 {
		t.Fatalf("requirements = %#v, preserved base URL must not require reentry", result.Requirements)
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
	if result.Script.Config.BaseURL != script.Config.BaseURL || len(result.Requirements) != 0 {
		t.Fatalf("base URL preservation = %q / %#v", result.Script.Config.BaseURL, result.Requirements)
	}
	if result.Readiness.State == importreview.Blocked {
		t.Fatalf("readiness = %#v, preserved credential must not require binding", result.Readiness)
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
	if len(result.Requirements) != 0 {
		t.Fatalf("requirements = %#v, preserved OAuth URL must not require reentry", result.Requirements)
	}
	init := result.Script.Init[0]
	if init.Target != rawTokenURL || init.Params["token_url"] != rawTokenURL {
		t.Fatalf("OAuth init lost imported URL bytes: %#v", init)
	}
	if blocker := init.Metadata["execution_blocker"]; blocker != nil {
		t.Fatalf("OAuth init received credential execution blocker: %#v", init.Metadata)
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
	if len(result.Requirements) != 0 {
		t.Fatalf("requirements = %#v, preserved OAuth URL must not require reentry", result.Requirements)
	}
	init := result.Script.Init[0]
	tokenURL, _ := init.Params["token_url"].(string)
	if tokenURL != rawTokenURL {
		t.Fatalf("OAuth params.token_url = %q, want imported value %q", tokenURL, rawTokenURL)
	}
	if blocker := init.Metadata["execution_blocker"]; blocker != nil {
		t.Fatalf("OAuth init received credential execution blocker: %#v", init.Metadata)
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
	if len(result.Requirements) != 0 {
		t.Fatalf("OAuth requirements = %#v, preserved OAuth URL must not require reentry", result.Requirements)
	}
	init := result.Script.Init[0]
	if init.Target != init.Params["token_url"] ||
		init.Target != rawTokenURL {
		t.Fatalf("OAuth mirror did not retain imported executable URL: %#v", init)
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
	if result.Script.Config.BaseURL != baseURL {
		t.Fatalf("base URL = %q, want imported expression %q", result.Script.Config.BaseURL, baseURL)
	}
	if len(result.Requirements) != 0 {
		t.Fatalf("requirements = %#v, preserved expression must not require reentry", result.Requirements)
	}
	if blocker := result.Script.Init[0].Metadata["execution_blocker"]; blocker != nil {
		t.Fatalf("absolute bootstrap received unrelated base URL blocker %v: %#v", blocker, result.Script.Init[0].Metadata)
	}
	assertBaseURLRuntimeTemplateGate(t, result.Script.Actions[0])
	assertBaseURLRuntimeTemplateFinding(t, result.Findings)
	if result.Disposition != BlockedDraft ||
		result.Readiness.State != importreview.Blocked ||
		result.Readiness.ReplayReady {
		t.Fatalf("result remained executable: disposition=%q readiness=%#v", result.Disposition, result.Readiness)
	}
}

func TestBaseURLRuntimeTemplateAvailabilityMatchesProtocolInitialization(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		value   string
		blocked bool
	}{
		{name: "literal", value: "https://api.example.test", blocked: false},
		{name: "project secret", value: "https://{{secret.API_HOST}}/v1", blocked: false},
		{name: "environment", value: "https://{{env.API_HOST}}/v1", blocked: true},
		{name: "data", value: "https://{{data.hosts.name}}/v1", blocked: true},
		{name: "variable", value: "https://{{var.host}}/v1", blocked: true},
		{name: "parameter", value: "https://{{param.host}}/v1", blocked: true},
		{name: "function", value: "https://api.example.test/{{fn.uuid()}}", blocked: true},
		{name: "fake", value: "https://{{fake.domain()}}/v1", blocked: true},
		{name: "file", value: "https://{{file.host}}/v1", blocked: true},
		{name: "mixed secret and environment", value: "https://{{secret.API_HOST}}/{{env.PATH}}", blocked: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := baseURLHasUnresolvableRuntimeTemplate(test.value); got != test.blocked {
				t.Fatalf("baseURLHasUnresolvableRuntimeTemplate(%q) = %t, want %t", test.value, got, test.blocked)
			}
		})
	}
}

func TestFinalizeScriptGatesEnvironmentTemplateInBaseURL(t *testing.T) {
	t.Parallel()
	script := validBaseScript()
	script.GeneratedFrom = "har"
	script.Config.BaseURL = "https://{{env.API_HOST}}/v1"
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
	if result.Script.Config.BaseURL != "https://{{env.API_HOST}}/v1" {
		t.Fatalf("base URL = %q, want exact imported template", result.Script.Config.BaseURL)
	}
	assertBaseURLRuntimeTemplateGate(t, result.Script.Actions[0])
	assertBaseURLRuntimeTemplateFinding(t, result.Findings)
	if result.Disposition != BlockedDraft || result.Readiness.ReplayReady {
		t.Fatalf("environment base URL remained executable: disposition=%q readiness=%#v", result.Disposition, result.Readiness)
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
	if got, want := result.Script.Config.BaseURL, script.Config.BaseURL; got != want {
		t.Fatalf("base URL = %q, want imported expression %q", got, want)
	}
	if len(result.Requirements) != 0 ||
		len(result.Script.Parameters) != 1 ||
		result.Script.Parameters[0].Values[0] != "static-token-value" {
		t.Fatalf("preserved parameter source = %#v / %#v", result.Requirements, result.Script.Parameters)
	}
	assertBaseURLRuntimeTemplateGate(t, result.Script.Actions[0])
	assertBaseURLRuntimeTemplateFinding(t, result.Findings)
	if result.Disposition != BlockedDraft || result.Readiness.ReplayReady {
		t.Fatalf("result remained executable: disposition=%q readiness=%#v", result.Disposition, result.Readiness)
	}
}

func TestFinalizeScriptBlocksParameterTemplateInBaseURL(t *testing.T) {
	script := validBaseScript()
	const baseURL = "https://api.example.test?access_token={{param.access_token}}"
	script.GeneratedFrom = "har"
	script.Config.BaseURL = baseURL
	script.Parameters = []dsl.Parameter{{
		Name:   "access_token",
		Type:   "list",
		Values: []string{"runtime-token-value"},
	}}
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
	if result.Script.Config.BaseURL != baseURL {
		t.Fatalf("base URL = %q, want imported expression %q", result.Script.Config.BaseURL, baseURL)
	}
	if len(result.Requirements) != 0 {
		t.Fatalf("requirements = %#v, preserved expression must not require reentry", result.Requirements)
	}
	assertBaseURLRuntimeTemplateGate(t, result.Script.Actions[0])
	if blocker := result.Script.Actions[1].Metadata["execution_blocker"]; blocker != nil {
		t.Fatalf("absolute request received unrelated base URL blocker %v: %#v", blocker, result.Script.Actions[1].Metadata)
	}
	assertBaseURLRuntimeTemplateFinding(t, result.Findings)
	if result.Disposition != BlockedDraft || result.Readiness.ReplayReady {
		t.Fatalf("result remained executable: disposition=%q readiness=%#v", result.Disposition, result.Readiness)
	}
}

func TestFinalizeScriptKeepsResolvableSecretBaseURLExecutable(t *testing.T) {
	script := validBaseScript()
	const baseURL = "https://api.example.test?access_token={{secret.BASE_ACCESS_TOKEN}}"
	script.GeneratedFrom = "har"
	script.Config.BaseURL = baseURL
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
	if result.Script.Config.BaseURL != baseURL {
		t.Fatalf("base URL = %q, want resolvable secret expression %q", result.Script.Config.BaseURL, baseURL)
	}
	if len(result.Requirements) != 0 {
		t.Fatalf("requirements = %#v, existing secret reference must not require reentry", result.Requirements)
	}
	if blocker := result.Script.Actions[0].Metadata["execution_blocker"]; blocker != nil {
		t.Fatalf("secret-backed base URL received blocker %v: %#v", blocker, result.Script.Actions[0].Metadata)
	}
	if result.Disposition != ExecutableExact || !result.Readiness.ReplayReady {
		t.Fatalf("secret-backed base URL is not executable: disposition=%q readiness=%#v", result.Disposition, result.Readiness)
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
	if result.Script.Config.BaseURL != script.Config.BaseURL {
		t.Fatalf("base URL = %q, want imported value %q", result.Script.Config.BaseURL, script.Config.BaseURL)
	}
	if blocker := result.Script.Actions[0].Metadata["execution_blocker"]; blocker != nil {
		t.Fatalf("relative request metadata = %#v, preserved credential must not gate execution", result.Script.Actions[0].Metadata)
	}
	if blocker := result.Script.Actions[1].Metadata["execution_blocker"]; blocker != nil {
		t.Fatalf("absolute request received unrelated base URL blocker %v: %#v", blocker, result.Script.Actions[1].Metadata)
	}
}

func assertBaseURLRuntimeTemplateGate(t *testing.T, action dsl.Action) {
	t.Helper()
	blockers, _ := action.Metadata["execution_blockers"].([]string)
	foundBlocker := action.Metadata["execution_blocker"] == importReviewRequiredBlocker
	for _, blocker := range blockers {
		if blocker == importReviewRequiredBlocker {
			foundBlocker = true
		}
	}
	if !foundBlocker {
		t.Fatalf("execution blockers = %#v, want %q: %#v", blockers, importReviewRequiredBlocker, action.Metadata)
	}
	review, _ := action.Metadata["import_review"].(map[string]any)
	reasons, _ := review["reason_codes"].([]string)
	foundReason := false
	for _, reason := range reasons {
		if reason == reasonBaseURLRuntimeTemplateUnavailable {
			foundReason = true
		}
	}
	if !foundReason {
		t.Fatalf("import review = %#v, want base URL runtime-template reason", review)
	}
}

func assertBaseURLRuntimeTemplateFinding(t *testing.T, findings []importreview.Finding) {
	t.Helper()
	for _, finding := range findings {
		if finding.ReasonCode == reasonBaseURLRuntimeTemplateUnavailable &&
			finding.Code == importReviewRequiredBlocker &&
			finding.Gate == importreview.GateExecution {
			return
		}
	}
	t.Fatalf("findings = %#v, want durable base URL runtime-template blocker", findings)
}
