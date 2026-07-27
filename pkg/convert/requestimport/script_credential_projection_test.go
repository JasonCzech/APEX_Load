package requestimport

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestProjectScriptCredentialsCoversExistingRequestSurfaces(t *testing.T) {
	t.Parallel()
	const (
		urlPassword   = "script-url-password-canary"
		queryToken    = "script-query-token-canary"
		headerToken   = "script-header-token-canary"
		formPassword  = "script-form-password-canary"
		authPassword  = "script-auth-password-canary"
		resourceToken = "script-resource-token-canary"
	)
	script := validBaseScript()
	script.GeneratedFrom = "test_import"
	script.Config.BaseURL = "https://api.example.test"
	script.Actions = []dsl.Action{{
		Name:   "request",
		Type:   "POST",
		Method: "POST",
		Target: "https://user:" + urlPassword + "@api.example.test/login?api_key=" + queryToken,
		HeaderList: dsl.HeaderList{
			{Name: "X-Trace", Value: "first"},
			{Name: "Authorization", Value: "Bearer " + headerToken},
			{Name: "x-trace", Value: "last"},
			{Name: "Content-Type", Value: "application/x-www-form-urlencoded"},
		},
		Headers: map[string]string{
			"X-Trace":       "first",
			"Authorization": "Bearer " + headerToken,
			"x-trace":       "last",
			"Content-Type":  "application/x-www-form-urlencoded",
		},
		Body: "username=demo&password=" + formPassword,
		Auth: &dsl.AuthConfig{
			Type:     "basic",
			Username: "demo",
			Password: authPassword,
		},
	}}

	result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "candidate",
		NamePrefix:    "TEST_IMPORT",
		AdditionalURLs: []ScriptCredentialURL{{
			Value:       "https://cdn.example.test/app.js?token=" + resourceToken,
			TargetField: "metadata.import.preserved.recorded_resource_urls[0]",
			Element:     "recorded resource URL",
		}},
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	payload, err := json.Marshal(struct {
		Script *dsl.Script
		URLs   []string
	}{Script: script, URLs: result.AdditionalURLs})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	for _, canary := range []string{
		urlPassword,
		queryToken,
		headerToken,
		formPassword,
		authPassword,
		resourceToken,
	} {
		if strings.Contains(string(payload), canary) {
			t.Fatalf("projected public surfaces retained %q: %s", canary, payload)
		}
	}
	if got := script.Actions[0].HeaderList; len(got) != 4 ||
		got[0].Name != "X-Trace" ||
		got[1].Name != "Authorization" ||
		got[2].Name != "x-trace" ||
		got[3].Name != "Content-Type" {
		t.Fatalf("header_list = %#v, want source order/casing/duplicates preserved", got)
	}
	if script.Actions[0].Metadata["execution_blocker"] == nil {
		t.Fatalf("metadata = %#v, URL userinfo must block execution", script.Actions[0].Metadata)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
	protected := make(map[string]bool)
	for _, requirement := range result.Requirements {
		for _, canary := range []string{
			urlPassword,
			queryToken,
			headerToken,
			formPassword,
			authPassword,
			resourceToken,
		} {
			if strings.Contains(requirement.Value, canary) {
				protected[canary] = true
			}
		}
	}
	for _, canary := range []string{
		urlPassword,
		queryToken,
		headerToken,
		formPassword,
		authPassword,
		resourceToken,
	} {
		if !protected[canary] {
			t.Errorf("protected requirements omitted %q: %#v", canary, result.Requirements)
		}
	}
}

func TestProjectScriptCredentialsPreservesInvalidHeaderUntilRuntimeFidelityReview(t *testing.T) {
	t.Parallel()
	const canary = "invalid-header-value-canary"
	script := validBaseScript()
	script.GeneratedFrom = "test_import"
	script.Actions = []dsl.Action{{
		Name:       "request",
		Type:       "request",
		Method:     "GET",
		Target:     "/",
		HeaderList: dsl.HeaderList{{Name: "", Value: canary}},
	}}

	if _, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "candidate",
		NamePrefix:    "TEST_IMPORT",
	}); err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	if got := script.Actions[0].HeaderList; len(got) != 1 ||
		got[0].Name != "" || got[0].Value != canary {
		t.Fatalf("pre-fidelity header_list = %#v, want authoritative malformed occurrence preserved", got)
	}

	findings, _, err := ApplyRuntimeFidelityReview(
		script,
		RuntimeFidelityOptions{SourceProfile: "test_import"},
	)
	if err != nil {
		t.Fatalf("ApplyRuntimeFidelityReview: %v", err)
	}
	if len(script.Actions[0].HeaderList) != 0 ||
		script.Actions[0].Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("post-fidelity action = %#v, want sanitized blocked draft", script.Actions[0])
	}
	if len(findings) != 1 || findings[0].ReasonCode != "http_header_invalid_header_name" {
		t.Fatalf("runtime findings = %#v", findings)
	}
	payload, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), canary) {
		t.Fatalf("blocked public draft retained invalid header value: %s", payload)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
}

func TestProjectScriptCredentialsMalformedBase64IsValidBlockedDraft(t *testing.T) {
	t.Parallel()
	script := validBaseScript()
	script.Actions = []dsl.Action{{
		Name:         "request",
		Type:         "POST",
		Method:       "POST",
		Target:       "/upload",
		Body:         "invalid-base64%%",
		BodyEncoding: "base64",
	}}

	result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "base64",
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	if result.FallbackDraft {
		t.Fatal("malformed request body should block its request, not replace the whole script")
	}
	action := script.Actions[0]
	decoded, decodeErr := base64.StdEncoding.DecodeString(action.Body)
	if decodeErr != nil || string(decoded) != "[REDACTED]" {
		t.Fatalf("body = %q, want encoded disclosure sentinel: decoded=%q err=%v", action.Body, decoded, decodeErr)
	}
	if action.Metadata["execution_blocker"] == nil {
		t.Fatalf("metadata = %#v, malformed encoding must block execution", action.Metadata)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
}

func TestProjectScriptCredentialsProjectsHTTPSequenceChildren(t *testing.T) {
	t.Parallel()
	const canary = "sequence-authorization-canary"
	script := validBaseScript()
	script.Actions = []dsl.Action{{
		Name:   "sequence",
		Type:   "http_sequence",
		Method: "SEQUENCE",
		Target: "/raw",
		Params: map[string]any{
			"requests": []map[string]any{{
				"name":   "child",
				"type":   "GET",
				"method": "GET",
				"target": "/orders",
				"headers": map[string]string{
					"Authorization": "Bearer " + canary,
				},
			}},
		},
	}}

	result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "sequence",
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	payload, err := json.Marshal(script)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(payload), canary) {
		t.Fatalf("sequence child retained credential: %s", payload)
	}
	if script.Actions[0].Target != "/orders" {
		t.Fatalf("sequence target = %q, want synchronized projected child target", script.Actions[0].Target)
	}
	found := false
	for _, requirement := range result.Requirements {
		if requirement.Value == "Bearer "+canary {
			found = true
		}
	}
	if !found {
		t.Fatalf("requirements = %#v, want sequence child credential", result.Requirements)
	}
}

func TestProjectScriptCredentialsRemovesMalformedSequenceBodyShape(t *testing.T) {
	t.Parallel()
	const canary = "malformed-sequence-body-canary"
	script := validBaseScript()
	script.Actions = []dsl.Action{{
		Name:   "sequence",
		Type:   "http_sequence",
		Method: "SEQUENCE",
		Target: "/raw",
		Params: map[string]any{
			"requests": []map[string]any{{
				"name":   "child",
				"type":   "POST",
				"method": "POST",
				"target": "/upload",
				"body": map[string]any{
					"password": canary,
				},
			}},
		},
	}}

	_, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "malformed-sequence-body",
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	payload, marshalErr := json.Marshal(script)
	if marshalErr != nil {
		t.Fatalf("json.Marshal: %v", marshalErr)
	}
	if strings.Contains(string(payload), canary) {
		t.Fatalf("malformed sequence body retained credential material: %s", payload)
	}
	requests, _ := script.Actions[0].Params["requests"].([]map[string]any)
	if len(requests) != 1 || requests[0]["body"] != "[REDACTED]" {
		t.Fatalf("sequence request = %#v, want disclosure-safe body sentinel", requests)
	}
	metadata, _ := requests[0]["metadata"].(map[string]any)
	if metadata["execution_blocker"] == nil {
		t.Fatalf("metadata = %#v, malformed body shape must block execution", metadata)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
}

func TestProjectScriptCredentialsRespectsSharedRegistry(t *testing.T) {
	t.Parallel()
	registry := credentialpolicy.NewNameRegistry()
	registry.Reserve("TEST_IMPORT_AUTHORIZATION")
	script := validBaseScript()
	script.Actions = []dsl.Action{{
		Name:   "request",
		Type:   "GET",
		Method: "GET",
		Target: "/",
		HeaderList: dsl.HeaderList{{
			Name:  "Authorization",
			Value: "Bearer token",
		}},
	}}
	result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "registry",
		NamePrefix:    "TEST_IMPORT",
		Registry:      registry,
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	if len(result.Requirements) != 1 {
		t.Fatalf("requirements = %#v, want one", result.Requirements)
	}
	if result.Requirements[0].SuggestedName == "TEST_IMPORT_AUTHORIZATION" {
		t.Fatalf("shared registry collision was not avoided: %#v", result.Requirements[0])
	}
}

func TestProjectScriptCredentialsUsesPathGuaranteedExtractorProvenance(t *testing.T) {
	t.Parallel()
	const variable = "{{var.branch_token}}"
	extractor := dsl.Extractor{
		Name:       "branch_token",
		Source:     "body",
		Type:       "regex",
		Expression: "(.*)",
	}
	credentialRequest := func(name string) dsl.Action {
		return dsl.Action{
			Name:   name,
			Type:   "GET",
			Method: "GET",
			Target: "/" + name,
			HeaderList: dsl.HeaderList{{
				Name:  "Authorization",
				Value: variable,
			}},
		}
	}
	extractorRequest := func(name string) dsl.Action {
		return dsl.Action{
			Name:       name,
			Type:       "GET",
			Method:     "GET",
			Target:     "/" + name,
			Extractors: []dsl.Extractor{extractor},
		}
	}

	tests := []struct {
		name        string
		actions     []dsl.Action
		value       func(*dsl.Script) string
		wantTrusted bool
	}{
		{
			name: "then extractor is not available in else",
			actions: []dsl.Action{{
				Name: "branch",
				Type: "control_if",
				Control: &dsl.ControlFlow{
					Condition: &dsl.Condition{Left: "yes", Op: "eq", Right: "yes"},
					Steps:     []dsl.Action{extractorRequest("then")},
					Else:      []dsl.Action{credentialRequest("else")},
				},
			}},
			value: func(script *dsl.Script) string {
				return script.Actions[0].Control.Else[0].HeaderList[0].Value
			},
		},
		{
			name: "one-sided branch extractor is not guaranteed afterward",
			actions: []dsl.Action{
				{
					Name: "branch",
					Type: "control_if",
					Control: &dsl.ControlFlow{
						Condition: &dsl.Condition{Left: "yes", Op: "eq", Right: "yes"},
						Steps:     []dsl.Action{extractorRequest("then")},
					},
				},
				credentialRequest("after"),
			},
			value: func(script *dsl.Script) string {
				return script.Actions[1].HeaderList[0].Value
			},
		},
		{
			name: "zero-iteration loop extractor is not guaranteed afterward",
			actions: []dsl.Action{
				{
					Name: "loop",
					Type: "control_loop",
					Control: &dsl.ControlFlow{
						Condition: &dsl.Condition{Left: "no", Op: "eq", Right: "yes"},
						Steps:     []dsl.Action{extractorRequest("inside")},
					},
				},
				credentialRequest("after"),
			},
			value: func(script *dsl.Script) string {
				return script.Actions[1].HeaderList[0].Value
			},
		},
		{
			name: "unconditional prior extractor is trusted",
			actions: []dsl.Action{
				extractorRequest("before"),
				credentialRequest("after"),
			},
			value: func(script *dsl.Script) string {
				return script.Actions[1].HeaderList[0].Value
			},
			wantTrusted: true,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			script := validBaseScript()
			script.Actions = test.actions
			result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
				SourceProfile: "test_import",
				CandidateKey:  "provenance",
			})
			if err != nil {
				t.Fatalf("ProjectScriptCredentials: %v", err)
			}
			got := test.value(script)
			if test.wantTrusted {
				if got != variable {
					t.Fatalf("Authorization = %q, want proven response variable unchanged", got)
				}
				if len(result.Requirements) != 0 {
					t.Fatalf("requirements = %#v, want no static credential binding", result.Requirements)
				}
				return
			}
			if got == variable || !strings.Contains(got, "{{secret.") {
				t.Fatalf("Authorization = %q, want unproven variable governed as a secret", got)
			}
			// A source template is not a migratable secret value: advertising
			// "{{var.branch_token}}" as Requirement.Value would falsely complete
			// binding. The projector retains it as the deferred expression so an
			// adapter that attaches parameters later can reconcile it.
			if len(result.Requirements) != 1 ||
				result.Requirements[0].DeferredSourceExpression != variable ||
				result.Requirements[0].Value != "" ||
				result.Requirements[0].SourceValueAvailable {
				t.Fatalf("requirements = %#v, want protected unproven variable", result.Requirements)
			}
		})
	}
}

func TestProjectScriptCredentialsHandlesDeferredBase64Templates(t *testing.T) {
	t.Parallel()
	t.Run("governed full-body secret is a valid deferred encoding", func(t *testing.T) {
		t.Parallel()
		script := validBaseScript()
		script.Actions = []dsl.Action{{
			Name:         "request",
			Type:         "POST",
			Method:       "POST",
			Target:       "/upload",
			Body:         "{{secret.ENCODED_BODY}}",
			BodyEncoding: "base64",
		}}

		result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
			SourceProfile: "test_import",
			CandidateKey:  "base64-secret",
		})
		if err != nil {
			t.Fatalf("ProjectScriptCredentials: %v", err)
		}
		if result.FallbackDraft || len(result.Requirements) != 0 {
			t.Fatalf("projection = %#v, governed deferred body should remain executable", result)
		}
		if script.Actions[0].Body != "{{secret.ENCODED_BODY}}" {
			t.Fatalf("body = %q, want governed deferred template unchanged", script.Actions[0].Body)
		}
		if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
			t.Fatalf("DSL validation issues = %#v", issues)
		}
	})

	t.Run("unproven variable is not a trusted deferred encoding", func(t *testing.T) {
		t.Parallel()
		script := validBaseScript()
		script.Actions = []dsl.Action{{
			Name:         "request",
			Type:         "POST",
			Method:       "POST",
			Target:       "/upload",
			Body:         "{{var.encoded_body}}",
			BodyEncoding: "base64",
		}}

		result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
			SourceProfile: "test_import",
			CandidateKey:  "base64-variable",
		})
		if err != nil {
			t.Fatalf("ProjectScriptCredentials: %v", err)
		}
		if result.FallbackDraft {
			t.Fatal("invalid encoded body should block its request, not replace the whole script")
		}
		decoded, decodeErr := base64.StdEncoding.DecodeString(script.Actions[0].Body)
		if decodeErr != nil || string(decoded) != "[REDACTED]" {
			t.Fatalf("body = %q, want encoded disclosure sentinel: decoded=%q err=%v", script.Actions[0].Body, decoded, decodeErr)
		}
		if script.Actions[0].Metadata["execution_blocker"] == nil {
			t.Fatalf("metadata = %#v, invalid encoded body must block execution", script.Actions[0].Metadata)
		}
		if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
			t.Fatalf("DSL validation issues = %#v", issues)
		}
	})
}

func TestProjectScriptCredentialsProjectsMirroredOAuthTokenURLOnce(t *testing.T) {
	t.Parallel()
	const canary = "oauth-token-url-canary"
	raw := "https://oauth-user:" + canary + "@auth.example.test/token"
	script := validBaseScript()
	script.Init = []dsl.Action{{
		Name:   "OAuth token",
		Type:   "http_oauth2_token",
		Target: raw,
		Params: map[string]any{
			"token_url":     raw,
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

	result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "oauth",
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	tokenURL, _ := script.Init[0].Params["token_url"].(string)
	if script.Init[0].Target != tokenURL ||
		strings.Contains(tokenURL, canary) ||
		!strings.Contains(tokenURL, "{{secret.") {
		t.Fatalf("OAuth URL mirrors were not projected together: %#v", script.Init[0])
	}
	if len(result.Requirements) != 1 ||
		result.Requirements[0].Value != "oauth-user:"+canary {
		t.Fatalf("requirements = %#v, want one protected mirrored URL occurrence", result.Requirements)
	}
	if script.Init[0].Metadata["execution_blocker"] == nil {
		t.Fatalf("metadata = %#v, OAuth userinfo must block execution", script.Init[0].Metadata)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
}

// TestProjectScriptCredentialsGovernsBaseURLRuntimeVariables pins that a
// credential-shaped {{var.*}} reference in config.base_url is always governed,
// no matter how well an extractor's execution path is guaranteed.
// buildProtoConfigWithSecretResolver (pkg/engine/config.go) flattens base_url
// once at protocol init and resolves only {{secret.*}} there; HTTP.ResolveURL
// then concatenates the stored string verbatim per request. A preserved
// {{var.*}} would therefore go on the wire literally, so extractor provenance
// cannot make it executable — only a secret binding can.
func TestProjectScriptCredentialsGovernsBaseURLRuntimeVariables(t *testing.T) {
	t.Parallel()
	const rawBaseURL = "https://api.example.test?access_token={{var.access_token}}"
	extractorRequest := dsl.Action{
		Name:   "bootstrap",
		Type:   "GET",
		Method: "GET",
		Target: "https://auth.example.test/bootstrap",
		Extractors: []dsl.Extractor{{
			Name:       "access_token",
			Source:     "body",
			Type:       "jsonpath",
			Expression: "$.access_token",
		}},
	}
	relativeRequest := dsl.Action{
		Name:   "request",
		Type:   "GET",
		Method: "GET",
		Target: "/orders",
	}

	t.Run("unconditional prior extractor", func(t *testing.T) {
		t.Parallel()
		script := validBaseScript()
		script.Config.BaseURL = rawBaseURL
		script.Init = []dsl.Action{extractorRequest}
		script.Actions = []dsl.Action{relativeRequest}
		result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
			SourceProfile: "test_import",
			CandidateKey:  "base-proven",
		})
		if err != nil {
			t.Fatalf("ProjectScriptCredentials: %v", err)
		}
		if !strings.Contains(script.Config.BaseURL, "{{secret.") {
			t.Fatalf("base URL = %q, want config variable governed as a secret", script.Config.BaseURL)
		}
		if len(result.Requirements) != 1 ||
			result.Requirements[0].DeferredSourceExpression != "{{var.access_token}}" {
			t.Fatalf("requirements = %#v, want protected base URL variable", result.Requirements)
		}
	})

	t.Run("optional branch extractor", func(t *testing.T) {
		t.Parallel()
		script := validBaseScript()
		script.Config.BaseURL = rawBaseURL
		script.Actions = []dsl.Action{
			{
				Name: "branch",
				Type: "control_if",
				Control: &dsl.ControlFlow{
					Condition: &dsl.Condition{Left: "yes", Op: "eq", Right: "yes"},
					Steps:     []dsl.Action{extractorRequest},
				},
			},
			relativeRequest,
		}
		result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
			SourceProfile: "test_import",
			CandidateKey:  "base-optional",
		})
		if err != nil {
			t.Fatalf("ProjectScriptCredentials: %v", err)
		}
		if !strings.Contains(script.Config.BaseURL, "{{secret.") {
			t.Fatalf("base URL = %q, want non-guaranteed variable governed as a secret", script.Config.BaseURL)
		}
		if len(result.Requirements) != 1 ||
			result.Requirements[0].DeferredSourceExpression != "{{var.access_token}}" {
			t.Fatalf("requirements = %#v, want protected non-guaranteed base variable", result.Requirements)
		}
	})
}

func TestProjectScriptCredentialsGovernsTemplatedOriginalURLUserinfo(t *testing.T) {
	t.Parallel()
	const sourceValue = "user:{{var.static_password}}"
	script := validBaseScript()
	script.Parameters = []dsl.Parameter{{
		Name:   "static_password",
		Type:   "list",
		Values: []string{"source-password"},
	}}
	script.Actions = []dsl.Action{{
		Name:   "request",
		Type:   "GET",
		Method: "GET",
		Target: "/orders",
		Metadata: map[string]any{
			"original_url": "https://" + sourceValue + "@api.example.test/orders",
		},
	}}

	result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "templated-original-url",
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	originalURL, _ := script.Actions[0].Metadata["original_url"].(string)
	if strings.Contains(originalURL, "{{var.static_password}}") ||
		!strings.Contains(originalURL, "{{secret.") {
		t.Fatalf("projected original URL = %q, want governed whole userinfo", originalURL)
	}
	if len(result.Requirements) != 1 ||
		result.Requirements[0].Value != "user:source-password" {
		t.Fatalf("requirements = %#v, want materialized protected userinfo", result.Requirements)
	}
	if script.Actions[0].Metadata["execution_blocker"] == nil {
		t.Fatalf("metadata = %#v, URL userinfo must block execution", script.Actions[0].Metadata)
	}
	payload, marshalErr := json.Marshal(script)
	if marshalErr != nil {
		t.Fatalf("json.Marshal: %v", marshalErr)
	}
	if strings.Contains(string(payload), "source-password") ||
		strings.Contains(string(payload), "{{var.static_password}}") {
		t.Fatalf("public script retained static credential material: %s", payload)
	}
}

func TestProjectScriptCredentialsRemovesAllSourceVariablesForDeduplicatedRequirement(t *testing.T) {
	t.Parallel()
	const sourcePassword = "same-source-password"
	script := validBaseScript()
	script.Parameters = []dsl.Parameter{
		{Name: "metadata_password", Type: "list", Values: []string{sourcePassword}},
		{Name: "target_password", Type: "list", Values: []string{sourcePassword}},
	}
	script.Actions = []dsl.Action{{
		Name:   "request",
		Type:   "GET",
		Method: "GET",
		Target: "https://user:{{var.target_password}}@api.example.test/orders",
		Metadata: map[string]any{
			"original_url": "https://user:{{var.metadata_password}}@api.example.test/orders",
		},
	}}

	result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "deduplicated-source-variables",
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	if len(result.Requirements) != 1 {
		t.Fatalf("requirements = %#v, want one deduplicated requirement", result.Requirements)
	}
	if got := result.Requirements[0].SourceVariableNames; !reflect.DeepEqual(
		got,
		[]string{"metadata_password", "target_password"},
	) {
		t.Fatalf("source variable names = %#v", got)
	}
	payload, marshalErr := json.Marshal(script)
	if marshalErr != nil {
		t.Fatalf("json.Marshal: %v", marshalErr)
	}
	if strings.Contains(string(payload), sourcePassword) ||
		strings.Contains(string(payload), "{{var.metadata_password}}") ||
		strings.Contains(string(payload), "{{var.target_password}}") {
		t.Fatalf("public script retained deduplicated credential source: %s", payload)
	}
}

func TestProjectScriptCredentialsRemovesMixedCaseRuntimeParameterCredential(t *testing.T) {
	t.Parallel()
	const sourceToken = "mixed-case-source-token-canary"
	script := validBaseScript()
	script.Parameters = []dsl.Parameter{{
		Name:   "Token",
		Type:   "list",
		Values: []string{sourceToken},
	}}
	script.Actions = []dsl.Action{{
		Name:   "request",
		Type:   "GET",
		Method: "GET",
		Target: "https://api.example.test/orders?access_token={{var.token}}",
	}}

	result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "mixed-case-source-parameter",
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	if len(result.Requirements) != 1 ||
		result.Requirements[0].Value != sourceToken ||
		!reflect.DeepEqual(result.Requirements[0].SourceVariableNames, []string{"Token"}) {
		t.Fatalf("requirements = %#v, want protected mixed-case source parameter", result.Requirements)
	}
	publicResult, marshalErr := json.Marshal(struct {
		Script       *dsl.Script
		Requirements []credentialpolicy.Requirement
	}{
		Script:       script,
		Requirements: result.Requirements,
	})
	if marshalErr != nil {
		t.Fatalf("json.Marshal: %v", marshalErr)
	}
	if strings.Contains(string(publicResult), sourceToken) ||
		strings.Contains(string(publicResult), "{{var.token}}") ||
		len(script.Parameters) != 0 {
		t.Fatalf("public result retained mixed-case credential source: %s", publicResult)
	}
}

func TestProjectScriptCredentialsExternalizesConstantDataSourceParameter(t *testing.T) {
	t.Parallel()
	const sourcePassword = "data-source-password-canary"
	script := validBaseScript()
	script.Protocol = "soap"
	script.DataSources = []dsl.DataSource{
		{
			Name:           "credentials",
			Type:           "csv",
			InlineData:     "UserName,Password\nsynthetic-user," + sourcePassword + "\n",
			Strategy:       "sequential",
			FirstRowHeader: true,
			Columns:        []string{"UserName", "Password"},
		},
	}
	script.Parameters = []dsl.Parameter{
		{
			Name:       "UserName",
			Type:       "file",
			DataSource: "credentials",
			Column:     "UserName",
		},
		{
			Name:       "Password",
			Type:       "file",
			DataSource: "credentials",
			Column:     "Password",
		},
	}
	script.Actions = []dsl.Action{{
		Name:       "request",
		Type:       "POST",
		Method:     "POST",
		Target:     "https://api.example.test/login",
		HeaderList: dsl.HeaderList{{Name: "Content-Type", Value: "application/xml"}},
		Headers:    map[string]string{"Content-Type": "application/xml"},
		Body:       "<request><user>{{param.username}}</user><password>{{var.password}}</password></request>",
	}}
	sourceVariables := literalScriptSourceVariables(script, "test_import")
	if sourceVariable, ok := sourceVariables["Password"]; !ok ||
		sourceVariable.Value != sourcePassword {
		t.Fatalf("source variables = %#v, want constant Password data-source value", sourceVariables)
	}

	result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "data-source-parameter",
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	if len(result.Requirements) != 1 ||
		result.Requirements[0].Value != sourcePassword ||
		!reflect.DeepEqual(result.Requirements[0].SourceVariableNames, []string{"Password"}) {
		t.Fatalf("requirements = %#v, want protected data-source password", result.Requirements)
	}
	publicResult, marshalErr := json.Marshal(struct {
		Script       *dsl.Script
		Requirements []credentialpolicy.Requirement
	}{
		Script:       script,
		Requirements: result.Requirements,
	})
	if marshalErr != nil {
		t.Fatalf("json.Marshal: %v", marshalErr)
	}
	if strings.Contains(string(publicResult), sourcePassword) ||
		strings.Contains(string(publicResult), "{{var.password}}") {
		t.Fatalf("public result retained data-source credential: %s", publicResult)
	}
	if len(script.Parameters) != 1 || script.Parameters[0].Name != "UserName" {
		t.Fatalf("parameters = %#v, want only nonsensitive sibling", script.Parameters)
	}
	if len(script.DataSources) != 1 ||
		strings.Contains(script.DataSources[0].InlineData, "Password") ||
		!strings.Contains(script.DataSources[0].InlineData, "synthetic-user") {
		t.Fatalf("filtered data sources = %#v", script.DataSources)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
}

func TestProjectScriptCredentialsPreservesGovernedRuntimeParameters(t *testing.T) {
	t.Parallel()
	const (
		firstPassword  = "rotating-password-one-canary"
		secondPassword = "rotating-password-two-canary"
		unusedSecret   = "unused-client-secret-canary"
	)
	script := validBaseScript()
	script.Protocol = "soap"
	script.DataSources = []dsl.DataSource{{
		Name:           "credentials",
		Type:           "dat",
		InlineData:     "UserName,Password\nalice," + firstPassword + "\nbob," + secondPassword + "\n",
		Strategy:       "sequential",
		FirstRowHeader: true,
		Columns:        []string{"UserName", "Password"},
	}}
	script.Parameters = []dsl.Parameter{
		{
			Name:       "UserName",
			Type:       "file",
			DataSource: "credentials",
			Column:     "UserName",
		},
		{
			Name:       "Password",
			Type:       "file",
			DataSource: "credentials",
			Column:     "Password",
		},
		{
			Name:   "ClientSecret",
			Type:   "list",
			Values: []string{unusedSecret},
		},
	}
	script.Actions = []dsl.Action{{
		Name:       "request",
		Type:       "POST",
		Method:     "POST",
		Target:     "https://api.example.test/login?label={{param.password}}",
		HeaderList: dsl.HeaderList{{Name: "Content-Type", Value: "application/xml"}},
		Headers:    map[string]string{"Content-Type": "application/xml"},
		Body:       "<request><user>{{param.username}}</user><password>{{param.password}}</password></request>",
	}}

	result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile:          "enterprise_script_archive",
		CandidateKey:           "governed-runtime-parameters",
		RuntimeParameterPolicy: RuntimeParametersPreserveGoverned,
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	if len(result.Requirements) != 0 {
		t.Fatalf("requirements = %#v, want no project-secret externalization", result.Requirements)
	}
	if len(script.Parameters) != 3 ||
		!strings.Contains(script.Actions[0].Body, "{{param.password}}") ||
		!strings.Contains(script.Actions[0].Target, "{{param.password}}") {
		t.Fatalf("runtime parameters were deleted or rewritten: %#v / %#v", script.Parameters, script.Actions[0])
	}
	for _, canary := range []string{firstPassword, secondPassword, unusedSecret} {
		payload, marshalErr := json.Marshal(script)
		if marshalErr != nil {
			t.Fatalf("json.Marshal: %v", marshalErr)
		}
		if strings.Contains(string(payload), canary) {
			t.Fatalf("governed runtime draft retained %q: %s", canary, payload)
		}
	}
	if !strings.Contains(script.DataSources[0].InlineData, "alice") ||
		!strings.Contains(script.DataSources[0].InlineData, "bob") ||
		strings.Count(script.DataSources[0].InlineData, credential.Redacted) != 2 {
		t.Fatalf("rotating credential column was not selectively redacted: %#v", script.DataSources[0])
	}
	if len(script.Parameters[2].Values) != 1 ||
		script.Parameters[2].Values[0] != credential.Redacted {
		t.Fatalf("unused credential parameter was not proactively redacted: %#v", script.Parameters[2])
	}
	findings, err := importreview.FindingsFromScript(script)
	if err != nil {
		t.Fatalf("FindingsFromScript: %v", err)
	}
	foundGate := false
	for _, finding := range findings {
		if finding.Code == "credential_parameter_source_redacted" &&
			finding.Gate == importreview.GateExecution {
			foundGate = true
		}
	}
	if !foundGate {
		t.Fatalf("governed parameter redaction did not create execution gate: %#v", findings)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
}

func TestLiteralScriptSourceVariablesReadsSingleColumnDAT(t *testing.T) {
	t.Parallel()
	script := validBaseScript()
	script.DataSources = []dsl.DataSource{{
		Name:           "password",
		Type:           "dat",
		InlineData:     "Password\ndata-source-password-canary\n",
		Strategy:       "sequential",
		FirstRowHeader: true,
	}}
	script.Parameters = []dsl.Parameter{{
		Name:       "password",
		Type:       "file",
		DataSource: "password",
		Column:     "Password",
	}}
	variables := literalScriptSourceVariables(script, "test_import")
	if variable, ok := variables["password"]; !ok ||
		variable.Value != "data-source-password-canary" {
		t.Fatalf("source variables = %#v", variables)
	}
}

func TestReconcileProtectedScriptCredentialSourcesAfterParameterAttachment(t *testing.T) {
	t.Parallel()
	const sourcePassword = "late-data-source-password-canary"

	// The first pass occurs before an archive adapter has attached its .prm
	// table, so it can only protect the unproven runtime template.
	script := validBaseScript()
	script.Protocol = "soap"
	script.Actions = []dsl.Action{{
		Name:       "request",
		Type:       "POST",
		Method:     "POST",
		Target:     "https://api.example.test/login",
		HeaderList: dsl.HeaderList{{Name: "Content-Type", Value: "application/xml"}},
		Headers:    map[string]string{"Content-Type": "application/xml"},
		Body:       "<request><password>{{var.password}}</password></request>",
	}}
	first, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "candidate",
		NamePrefix:    "TEST_IMPORT",
	})
	if err != nil {
		t.Fatalf("first ProjectScriptCredentials: %v", err)
	}
	if len(first.Requirements) != 1 ||
		first.Requirements[0].DeferredSourceExpression != "{{var.password}}" ||
		first.Requirements[0].Value != "" {
		t.Fatalf("first requirements = %#v", first.Requirements)
	}
	requirementID := first.Requirements[0].ID

	// ZIP finalization attaches the source table after the first projection.
	script.DataSources = []dsl.DataSource{{
		Name:           "password",
		Type:           "dat",
		InlineData:     "Password\n" + sourcePassword + "\n",
		Strategy:       "sequential",
		FirstRowHeader: true,
	}}
	script.Parameters = []dsl.Parameter{{
		Name:       "password",
		Type:       "file",
		DataSource: "password",
		Column:     "Password",
	}}
	reconciled := ReconcileProtectedScriptCredentialSources(
		script,
		first.Requirements,
		"test_import",
		"candidate",
	)
	if len(reconciled) != 1 ||
		reconciled[0].ID != requirementID ||
		reconciled[0].Value != sourcePassword ||
		!reflect.DeepEqual(reconciled[0].SourceVariableNames, []string{"password"}) {
		t.Fatalf("reconciled requirements = %#v", reconciled)
	}
	publicResult, marshalErr := json.Marshal(struct {
		Script       *dsl.Script
		Requirements []credentialpolicy.Requirement
	}{
		Script:       script,
		Requirements: reconciled,
	})
	if marshalErr != nil {
		t.Fatalf("json.Marshal: %v", marshalErr)
	}
	if strings.Contains(string(publicResult), sourcePassword) ||
		strings.Contains(string(publicResult), "{{var.password}}") ||
		len(script.Parameters) != 0 ||
		len(script.DataSources) != 0 {
		t.Fatalf("reconciled public result retained credential source: %s", publicResult)
	}
}
