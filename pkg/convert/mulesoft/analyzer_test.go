package mulesoft

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestAnalyzeDetectsAnypointHostsPoliciesAndAuth(t *testing.T) {
	script := &dsl.Script{
		Name:     "Mule API",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://accounts-api.us-e2.cloudhub.io/api"},
		Init: []dsl.Action{{
			Name: "Token",
			Type: "http_oauth2_token",
			Params: map[string]any{
				"grant_type": "client_credentials",
				"token_url":  "https://login.example.test/oauth/token",
			},
		}},
		Actions: []dsl.Action{{
			Name:   "List Accounts",
			Type:   "GET",
			Method: "GET",
			Target: "/accounts",
			Headers: map[string]string{
				"Authorization": "Bearer {{var.access_token}}",
				"client_id":     "{{env.MULE_CLIENT_ID}}",
				"client_secret": "{{secret.MULE_CLIENT_SECRET}}",
			},
		}},
	}

	review := Analyze(script)
	if !review.Detected {
		t.Fatal("MuleSoft traffic not detected")
	}
	if len(review.Domains) != 1 || review.Domains[0].Kind != "cloudhub" {
		t.Fatalf("domains = %#v, want one cloudhub host", review.Domains)
	}

	policies := map[string]bool{}
	for _, finding := range review.PolicyFindings {
		policies[finding.Policy] = true
	}
	if !policies["client_id_enforcement"] {
		t.Fatalf("policy findings = %#v, want client_id_enforcement", review.PolicyFindings)
	}

	auths := map[string]bool{}
	for _, finding := range review.AuthFindings {
		auths[finding.Type] = true
	}
	if !auths["oauth2_client_credentials"] || !auths["bearer"] {
		t.Fatalf("auth findings = %#v, want oauth2 client-credentials + bearer", review.AuthFindings)
	}
	if len(review.Blockers) != 0 {
		t.Fatalf("blockers = %#v, want zero credential blockers", review.Blockers)
	}
}

func TestAnalyzeAllowsLiteralClientCredentialsAndBearerWithoutBlocker(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{BaseURL: "https://api.anypoint.mulesoft.com/v1"},
		Actions: []dsl.Action{{
			Name:   "Call",
			Type:   "GET",
			Method: "GET",
			Target: "/resource?client_id=literal-client&client_secret=literal-secret",
			Headers: map[string]string{
				"Authorization": "Bearer literal-access-token",
				"client_id":     "literal-client",
				"client_secret": "literal-secret",
			},
		}},
	}
	review := Analyze(script)
	if !review.Detected {
		t.Fatal("anypoint host not detected")
	}
	if len(review.Blockers) != 0 {
		t.Fatalf("blockers = %#v, want none when literal credentials are present", review.Blockers)
	}
	for _, finding := range review.PolicyFindings {
		if strings.Contains(finding.Detail, "move to env/secret") || strings.Contains(finding.Detail, "supply via env/secret") {
			t.Fatalf("policy finding demands removing literal credentials: %#v", finding)
		}
	}
}

func TestAnalyzeFlagsUnresolvedBearerWithoutTokenAcquisition(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{BaseURL: "https://api.anypoint.mulesoft.com/v1"},
		Actions: []dsl.Action{{
			Name:    "Call",
			Type:    "GET",
			Method:  "GET",
			Target:  "/resource",
			Headers: map[string]string{"Authorization": "Bearer {{var.access_token}}"},
		}},
	}
	review := Analyze(script)
	if !review.Detected {
		t.Fatal("anypoint host not detected")
	}
	foundBlocker := false
	for _, blocker := range review.Blockers {
		if strings.Contains(blocker, "http_oauth2_token") {
			foundBlocker = true
		}
	}
	if !foundBlocker {
		t.Fatalf("blockers = %#v, want missing-token-acquisition blocker for unresolved {{var.access_token}}", review.Blockers)
	}
}

func TestAnalyzeTreatsBasicAuthOnMuleSoftHostAsClientIDPolicySignal(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{BaseURL: "https://accounts.us-e2.cloudhub.io/api"},
		Actions: []dsl.Action{{
			Name:    "Call",
			Type:    "GET",
			Method:  "GET",
			Target:  "/resource",
			Headers: map[string]string{"Authorization": "Basic Y2xpZW50OnNlY3JldA=="},
		}},
	}
	review := Analyze(script)
	for _, finding := range review.PolicyFindings {
		if finding.Policy == "client_id_enforcement" && strings.Contains(finding.Evidence, "basic authorization") {
			return
		}
	}
	t.Fatalf("policy findings = %#v, want Basic Authorization client_id_enforcement signal", review.PolicyFindings)
}

func TestAnalyzeDoesNotFlagMissingTokenWhenRefreshTokenGrantPresent(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{BaseURL: "https://api.anypoint.mulesoft.com/v1"},
		Init:   []dsl.Action{{Name: "Token", Type: "http_oauth2_token", Params: map[string]any{"grant_type": "refresh_token"}}},
		Actions: []dsl.Action{{
			Name: "Call", Type: "GET", Method: "GET", Target: "/resource",
			Headers: map[string]string{"Authorization": "Bearer {{var.access_token}}"},
		}},
	}
	review := Analyze(script)
	for _, blocker := range review.Blockers {
		if strings.Contains(blocker, "http_oauth2_token") {
			t.Fatalf("blockers = %#v, should not claim missing token when refresh_token init present", review.Blockers)
		}
	}
}

func TestAnalyzeDoesNotDetectGenericApiKeyServices(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{BaseURL: "https://api.example.com/v1"},
		Actions: []dsl.Action{{
			Name: "Call", Type: "GET", Method: "GET", Target: "/resource",
			Headers: map[string]string{"x-api-key": "{{env.API_KEY}}"},
		}},
	}
	if review := Analyze(script); review.Detected {
		t.Fatalf("review = %#v, want a generic api-key service NOT detected as MuleSoft", review)
	}
}

func TestAnalyzeDoesNotDetectNonAnypointMulesoftSubstringHosts(t *testing.T) {
	script := &dsl.Script{
		Config:  dsl.ScriptConfig{BaseURL: "https://mulesoft-proxy.acme.com/api"},
		Actions: []dsl.Action{{Type: "GET", Method: "GET", Target: "/resource"}},
	}
	if review := Analyze(script); review.Detected {
		t.Fatalf("review = %#v, want non-Anypoint host containing 'mulesoft' NOT detected", review)
	}
}

func TestAnalyzeIgnoresNonMuleScripts(t *testing.T) {
	script := &dsl.Script{
		Config:  dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions: []dsl.Action{{Type: "GET", Method: "GET", Target: "/health"}},
	}
	review := Analyze(script)
	if review.Detected {
		t.Fatalf("review = %#v, want not detected for non-Mule script", review)
	}
}

func TestAnalyzeDetectsClientIDEnforcementInRequestBody(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantEvidPfx string
	}{
		{
			name:        "json body",
			body:        `{"grant_type":"client_credentials","client_id":"abc","client_secret":"xyz"}`,
			wantEvidPfx: "body client_",
		},
		{
			name:        "nested json body",
			body:        `{"auth":{"client_secret":"xyz"}}`,
			wantEvidPfx: "body client_secret",
		},
		{
			name:        "json array body",
			body:        `[{"auth":{"client_id":"abc"}}]`,
			wantEvidPfx: "body client_id",
		},
		{
			name:        "form encoded body",
			body:        `grant_type=client_credentials&client_id=abc&client_secret=xyz`,
			wantEvidPfx: "body client_",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := &dsl.Script{
				Config: dsl.ScriptConfig{BaseURL: "https://accounts-api.us-e2.cloudhub.io/api"},
				Actions: []dsl.Action{{
					Name:   "Token",
					Type:   "POST",
					Method: "POST",
					Target: "/token",
					Body:   tc.body,
				}},
			}
			review := Analyze(script)
			var found *PolicyFinding
			for i := range review.PolicyFindings {
				if review.PolicyFindings[i].Policy == "client_id_enforcement" {
					found = &review.PolicyFindings[i]
				}
			}
			if found == nil {
				t.Fatalf("client_id_enforcement not detected from body; findings = %#v", review.PolicyFindings)
			}
			if !strings.HasPrefix(found.Evidence, tc.wantEvidPfx) {
				t.Fatalf("evidence = %q, want prefix %q", found.Evidence, tc.wantEvidPfx)
			}
		})
	}
}

func TestBodyCredentialKeyIgnoresPlainText(t *testing.T) {
	// Free-form text that merely mentions client_id but is neither JSON nor a
	// key=value form must not trigger detection.
	if key, ok := bodyCredentialKey("please provide your client_id to the admin"); ok {
		t.Fatalf("plain text should not match, got key %q", key)
	}
	if key, ok := bodyCredentialKey(""); ok {
		t.Fatalf("empty body should not match, got key %q", key)
	}
	if key, ok := bodyCredentialKey(`{"order_id":"123"}`); ok {
		t.Fatalf("unrelated JSON should not match, got key %q", key)
	}
}

func TestGenericOAuthDoesNotImplyAnypoint(t *testing.T) {
	for _, host := range []string{"login.salesforce.com", "oauth.example.com", "api.cloudhub.io.evil.test", "anypoint.mulesoft.com.evil.test"} {
		t.Run(host, func(t *testing.T) {
			script := &dsl.Script{Config: dsl.ScriptConfig{BaseURL: "https://" + host}, Actions: []dsl.Action{{Target: "/authorize?client_id=app", Headers: map[string]string{"client_secret": "secret", "Authorization": "Bearer token"}, Body: `{"client_id":"app"}`}}}
			review := Analyze(script)
			if review.Detected || len(review.PolicyFindings) != 0 || len(review.Blockers) != 0 || len(review.NextActions) != 0 {
				t.Fatalf("generic OAuth received vendor guidance: %#v", review)
			}
		})
	}
	// A genuine MuleSoft call must not attribute unrelated OAuth traffic to it.
	script := &dsl.Script{Config: dsl.ScriptConfig{BaseURL: "https://api.cloudhub.io"}, Actions: []dsl.Action{{Target: "https://login.salesforce.com/authorize?client_id=app"}}}
	if review := Analyze(script); !review.Detected || len(review.PolicyFindings) != 0 {
		t.Fatalf("cross-host policy attribution: %#v", review)
	}
}

func TestMixedServiceAuthIsScopedToMuleSoftDestination(t *testing.T) {
	for _, target := range []string{"https://login.salesforce.com/resource", "//login.salesforce.com/resource"} {
		t.Run(target, func(t *testing.T) {
			script := &dsl.Script{Config: dsl.ScriptConfig{BaseURL: "https://api.cloudhub.io"}, Actions: []dsl.Action{
				{Target: "/health"},
				{Target: target, Headers: map[string]string{"Authorization": "Bearer token", "client_id": "app"}},
				{Target: target, Headers: map[string]string{"Authorization": "Basic credential"}},
			}}
			review := Analyze(script)
			if !review.Detected || len(review.AuthFindings) != 0 || len(review.PolicyFindings) != 0 || len(review.Blockers) != 0 {
				t.Fatalf("unrelated service authentication attributed to MuleSoft: %#v", review)
			}
		})
	}
}

func TestAnalyzeBearerVarBackedByParameterOrExtractorDoesNotBlock(t *testing.T) {
	t.Run("parameter_backed_bearer_in_header_list_and_auth", func(t *testing.T) {
		script := &dsl.Script{
			Config: dsl.ScriptConfig{BaseURL: "https://orders-api.us-e2.cloudhub.io"},
			Parameters: []dsl.Parameter{{
				Name:   "access_token",
				Type:   "list",
				Values: []string{"eyJ-seeded-token"},
			}},
			Actions: []dsl.Action{
				{
					Name:   "Get Orders",
					Type:   "GET",
					Method: "GET",
					Target: "/v1/orders",
					HeaderList: dsl.HeaderList{
						{Name: "Authorization", Value: "Bearer {{var.access_token}}"},
					},
				},
				{
					Name:   "Get Account",
					Type:   "GET",
					Method: "GET",
					Target: "/v1/account",
					Auth:   &dsl.AuthConfig{Type: "bearer", Token: "{{param.access_token}}"},
				},
			},
		}
		review := Analyze(script)
		if !review.Detected || len(review.Blockers) != 0 {
			t.Fatalf("parameter-backed bearer produced unexpected blockers: %#v", review)
		}
	})

	t.Run("extractor_backed_bearer_in_nested_branch", func(t *testing.T) {
		script := &dsl.Script{
			Config: dsl.ScriptConfig{BaseURL: "https://orders-api.us-e2.cloudhub.io"},
			Init: []dsl.Action{{
				Name:   "Exchange Token",
				Type:   "POST",
				Method: "POST",
				Target: "https://anypoint.mulesoft.com/accounts/api/v2/oauth2/token",
				Extractors: []dsl.Extractor{{
					Name:       "corr_access_token",
					Source:     "body",
					Type:       "jsonpath",
					Expression: "$.access_token",
				}},
			}},
			Actions: []dsl.Action{{
				Name: "Conditional Call",
				Type: "control_if",
				Control: &dsl.ControlFlow{
					Steps: []dsl.Action{{
						Name:    "Nested API",
						Type:    "GET",
						Method:  "GET",
						Target:  "/v1/orders",
						Headers: map[string]string{"Authorization": "Bearer {{var.corr_access_token}}"},
					}},
				},
			}},
		}
		review := Analyze(script)
		if !review.Detected || len(review.AuthFindings) == 0 || len(review.Blockers) != 0 {
			t.Fatalf("extractor-backed nested bearer produced unexpected review: %#v", review)
		}
	})

	t.Run("unbound_bearer_in_nested_branch_still_blocks", func(t *testing.T) {
		script := &dsl.Script{
			Config: dsl.ScriptConfig{BaseURL: "https://orders-api.us-e2.cloudhub.io"},
			Parameters: []dsl.Parameter{{
				Name:   "empty_param",
				Type:   "list",
				Values: []string{"   "},
			}},
			Actions: []dsl.Action{{
				Name: "Conditional Call",
				Type: "control_if",
				Control: &dsl.ControlFlow{
					Steps: []dsl.Action{{
						Name:   "Nested API",
						Type:   "GET",
						Method: "GET",
						Target: "/v1/orders",
						Auth:   &dsl.AuthConfig{Type: "bearer", Token: "{{var.empty_param}}"},
					}},
				},
			}},
		}
		review := Analyze(script)
		if !review.Detected || len(review.Blockers) != 1 {
			t.Fatalf("expected missing-token blocker on unbound nested bearer, got: %#v", review)
		}
	})

	t.Run("mixed_header_list_and_headers_with_disabled_and_shadowed_auth", func(t *testing.T) {
		disabled := false
		script := &dsl.Script{
			Config: dsl.ScriptConfig{BaseURL: "https://orders-api.us-e2.cloudhub.io"},
			Actions: []dsl.Action{
				{
					Name:   "Mixed Headers",
					Type:   "GET",
					Method: "GET",
					Target: "/v1/orders",
					HeaderList: dsl.HeaderList{
						{Name: "Accept", Value: "application/json"},
						{Name: "X-Client-Secret", Value: "disabled-secret", Enabled: &disabled},
					},
					Headers: map[string]string{
						"client_id":     "app-123",
						"Authorization": "Bearer literal-wire-token",
					},
					// Shadowed by authored Headers["Authorization"]: unbound Auth.Token is never sent on the wire.
					Auth: &dsl.AuthConfig{Type: "bearer", Token: "{{var.unbound_shadowed_token}}"},
				},
				{
					Name:   "Disabled HeaderList Authorization Shadows Auth",
					Type:   "GET",
					Method: "GET",
					Target: "/v1/items",
					HeaderList: dsl.HeaderList{
						{Name: "Authorization", Value: "Bearer {{var.disabled_unbound}}", Enabled: &disabled},
					},
					Headers: map[string]string{
						"Authorization": "Bearer {{var.shadowed_map_unbound}}",
					},
					Auth: &dsl.AuthConfig{Type: "bearer", Token: "{{var.shadowed_auth_unbound}}"},
				},
			},
		}
		review := Analyze(script)
		if !review.Detected {
			t.Fatalf("expected MuleSoft detection, got: %#v", review)
		}
		if len(review.PolicyFindings) != 1 || review.PolicyFindings[0].Policy != "client_id_enforcement" {
			t.Fatalf("expected client_id from non-shadowed Headers map without disabled HeaderList noise, got: %#v", review.PolicyFindings)
		}
		if review.PolicyFindings[0].Evidence != "header client_id" {
			t.Fatalf("expected only header client_id evidence (disabled X-Client-Secret ignored), got: %q", review.PolicyFindings[0].Evidence)
		}
		if len(review.Blockers) != 0 {
			t.Fatalf("shadowed/disabled unbound bearer should not block when active wire auth is literal, got: %#v", review.Blockers)
		}
	})

	t.Run("disabled_before_enabled_header_list_and_case_variant_headers_map", func(t *testing.T) {
		disabled := false
		for i := 0; i < 20; i++ {
			script := &dsl.Script{
				Config: dsl.ScriptConfig{BaseURL: "https://orders-api.us-e2.cloudhub.io"},
				Actions: []dsl.Action{
					{
						Name:   "Disabled Before Enabled ClientID",
						Type:   "GET",
						Method: "GET",
						Target: "/v1/orders",
						HeaderList: dsl.HeaderList{
							{Name: "client_id", Value: "old-app", Enabled: &disabled},
							{Name: "client_id", Value: "live-app"},
						},
					},
					{
						Name:   "Case Variant Headers Map",
						Type:   "GET",
						Method: "GET",
						Target: "/v1/items",
						Headers: map[string]string{
							"Authorization": "",
							"authorization": "Bearer literal-active-token",
						},
					},
				},
			}
			review := Analyze(script)
			if !review.Detected || len(review.PolicyFindings) != 1 || len(review.AuthFindings) != 1 || len(review.Blockers) != 0 {
				t.Fatalf("iter %d unexpected MuleSoft review: %#v", i, review)
			}
		}
	})

	t.Run("query_credentials_evidence_is_deterministic", func(t *testing.T) {
		script := &dsl.Script{
			Config: dsl.ScriptConfig{BaseURL: "https://orders-api.us-e2.cloudhub.io"},
			Actions: []dsl.Action{
				{
					Name:   "Query Client Pair",
					Type:   "GET",
					Method: "GET",
					Target: "/v1/orders?client_secret=sec-1&client_id=app-1",
				},
			},
		}
		for i := 0; i < 32; i++ {
			review := Analyze(script)
			if len(review.PolicyFindings) != 1 || review.PolicyFindings[0].Evidence != "query client_id" {
				t.Fatalf("iter %d query evidence = %#v, want deterministic 'query client_id'", i, review.PolicyFindings)
			}
		}
	})
}
