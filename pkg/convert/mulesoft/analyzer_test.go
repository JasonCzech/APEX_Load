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
	if len(review.Blockers) == 0 {
		t.Fatalf("blockers = %#v, want client-credential blocker", review.Blockers)
	}
}

func TestAnalyzeFlagsBearerWithoutTokenAcquisition(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{BaseURL: "https://api.anypoint.mulesoft.com/v1"},
		Actions: []dsl.Action{{
			Name:    "Call",
			Type:    "GET",
			Method:  "GET",
			Target:  "/resource",
			Headers: map[string]string{"Authorization": "Bearer abc"},
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
		t.Fatalf("blockers = %#v, want missing-token-acquisition blocker", review.Blockers)
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
