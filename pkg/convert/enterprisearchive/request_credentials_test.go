package enterprisearchive

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestEnterpriseHTTPRequestCredentialsNeverReachPublicConversionJSON(t *testing.T) {
	t.Parallel()
	const (
		urlPassword    = "url-password-canary-7c91"
		queryAPIKey    = "query-api-key-canary-3a82"
		headerToken    = "authorization-header-canary-5e73"
		formPassword   = "form-password-canary-1b64"
		globalPassword = "global-auth-password-canary-9d55"
	)
	source := `Action()
{
	web_set_user("auth-user", "` + globalPassword + `", "secure.example.test:443");
	web_add_header("Authorization", "Bearer ` + headerToken + `");
	web_custom_request("login",
		"URL=https://url-user:` + urlPassword + `@secure.example.test/login?api_key=` + queryAPIKey + `",
		"Method=POST",
		"EncType=application/x-www-form-urlencoded",
		"Body=username=demo&password=` + formPassword + `",
		LAST);
	return 0;
}`
	canaries := []string{urlPassword, queryAPIKey, headerToken, formPassword, globalPassword}

	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatalf("ParseActionFile: %v", err)
	}
	direct, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	assertEnterpriseCredentialSafeResult(t, direct, canaries)

	archive := buildSourceZip(t, map[string]string{
		"CredentialSafety.usr": `[General]
Type=Web
[Actions]
Action1=Action.c
`,
		"Action.c": source,
	})
	zipped, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertEnterpriseCredentialSafeResult(t, zipped, canaries)
}

func assertEnterpriseCredentialSafeResult(t *testing.T, result *ConvertResult, canaries []string) {
	t.Helper()
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	for _, canary := range canaries {
		if strings.Contains(string(payload), canary) {
			t.Fatalf("public conversion JSON retained canary %q: %s", canary, payload)
		}
	}
	if result.Script == nil || len(result.Script.Actions) == 0 {
		t.Fatalf("script = %#v, want disclosure-safe request draft", result.Script)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
	if got := result.Script.Actions[0].Metadata["execution_blocker"]; got == nil {
		t.Fatalf("request metadata = %#v, URL userinfo must durably block execution", result.Script.Actions[0].Metadata)
	}

	requirements := result.ProtectedRequirements.Take()
	protected := make(map[string]bool, len(canaries))
	for _, requirement := range requirements {
		for _, canary := range canaries {
			if strings.Contains(requirement.Value, canary) {
				protected[canary] = true
			}
		}
	}
	for _, canary := range canaries {
		if !protected[canary] {
			t.Errorf("protected requirements omitted canary %q: %#v", canary, requirements)
		}
	}
}

func TestEnterpriseReviewPlaceholdersRetainOnlyDisclosureSafeArgumentShape(t *testing.T) {
	t.Parallel()
	const canary = "review-placeholder-canary-4f20"
	result := convertSourceExecutionFixture(t, `
Action()
{
	lr_get_transaction_duration("`+canary+`");
	custom_secret_helper("`+canary+`");
	web_url("request", "URL=https://example.test/", LAST);
	return 0;
}`)
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(payload), canary) {
		t.Fatalf("review placeholder retained source argument canary: %s", payload)
	}
	for _, action := range result.Script.Actions[:2] {
		if _, retained := action.Metadata["source_call"]; retained {
			t.Fatalf("review action retained raw source_call: %#v", action.Metadata)
		}
		if strings.Contains(action.Name, canary) {
			t.Fatalf("review action name retained source argument: %q", action.Name)
		}
	}
}

func TestEnterpriseSpecializedReviewConstructorsDoNotRetainSourceStatements(t *testing.T) {
	t.Parallel()
	const canary = "specialized-review-canary-2d18"
	fn := ParsedFunction{
		Name:       "custom_review",
		StepName:   canary,
		RawArgs:    []string{canary},
		RawBlock:   `custom_review("` + canary + `");`,
		Filename:   "Action.c",
		LineNumber: 7,
	}
	databaseResult := &ConvertResult{Script: &dsl.Script{}}
	appendDatabaseReviewAction(databaseResult, fn, "dynamic_sql", "review")
	sapAction, _ := sapGUIFallbackAction(fn)
	javaAction := javaSourceReviewAction(javaStatement{
		File:      "Actions.java",
		StartLine: 9,
		EndLine:   9,
		Text:      `client.call("` + canary + `");`,
	}, "action", "unresolved")

	payload, err := json.Marshal([]any{
		databaseResult.Script.Actions,
		sapAction,
		javaAction,
	})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(payload), canary) {
		t.Fatalf("specialized review constructor retained source statement: %s", payload)
	}
}

func TestEnterpriseCredentialProjectionRequirementValuesRemainJSONExcluded(t *testing.T) {
	t.Parallel()
	requirement, _, err := credentialpolicy.NewRequirement(credentialpolicy.RequirementSpec{
		Key:      "password",
		NameHint: "ENTERPRISE_TEST_PASSWORD",
		Value:    "protected-value-canary",
		Targets: []credentialpolicy.TargetRef{{
			Field: "config.http.auth.password",
		}},
		Mode: credentialpolicy.SecretReference,
	})
	if err != nil {
		t.Fatalf("NewRequirement: %v", err)
	}
	result := &ConvertResult{}
	result.ProtectedRequirements.Store([]credentialpolicy.Requirement{requirement})
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(payload), "protected-value-canary") {
		t.Fatalf("protected requirement value reached public JSON: %s", payload)
	}
}

func TestEnterpriseMalformedBase64BodyBecomesValidBlockedDraft(t *testing.T) {
	t.Parallel()
	result := &ConvertResult{Script: &dsl.Script{
		Name:          "malformed-base64",
		Version:       "1.0.0",
		Protocol:      "http",
		GeneratedFrom: "enterprise_script_archive",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
		},
		Actions: []dsl.Action{{
			Name:         "request",
			Type:         "POST",
			Method:       "POST",
			Target:       "/upload",
			Body:         "not-valid-base64%%",
			BodyEncoding: "base64",
		}},
	}}
	applyEnterpriseRuntimeBindings(
		result,
		[]*dsl.Script{result.Script},
		archiveFinalizationContext{},
	)

	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
	action := result.Script.Actions[0]
	decoded, err := base64.StdEncoding.DecodeString(action.Body)
	if err != nil || string(decoded) != "[REDACTED]" {
		t.Fatalf("body = %q, want valid encoded disclosure sentinel: decoded=%q err=%v", action.Body, decoded, err)
	}
	if action.Metadata["execution_blocker"] == nil {
		t.Fatalf("metadata = %#v, malformed encoded body must durably block execution", action.Metadata)
	}
}

func TestEnterpriseUnsafeProxyDoesNotBypassRequestCredentialProjection(t *testing.T) {
	t.Parallel()
	const (
		proxyCanary  = "unsafe-proxy-fragment-canary"
		headerCanary = "request-header-after-proxy-canary"
		bodyCanary   = "request-body-after-proxy-canary"
	)
	parsed, err := ParseActionFile("Action.c", `Action()
{
	web_add_header("Authorization", "Bearer `+headerCanary+`");
	web_custom_request("login",
		"URL=https://secure.example.test/login",
		"Method=POST",
		"EncType=application/x-www-form-urlencoded",
		"Body=username=demo&password=`+bodyCanary+`",
		LAST);
	return 0;
}`)
	if err != nil {
		t.Fatalf("ParseActionFile: %v", err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{
		HTTPConfig: &dsl.HTTPConfig{
			ProxyMode: "custom",
			Proxy: dsl.ProxyConfig{
				URL: "http://proxy.example.test:8080/?api_key=" + proxyCanary + "\xff",
			},
		},
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	for _, canary := range []string{proxyCanary, headerCanary, bodyCanary} {
		if strings.Contains(string(payload), canary) {
			t.Fatalf("public conversion JSON retained canary %q after proxy rejection: %s", canary, payload)
		}
	}
	if result.Script.Config.HTTP.ProxyMode != "none" ||
		result.Script.Config.HTTP.Proxy.URL != "" {
		t.Fatalf("unsafe proxy was not disabled: %#v", result.Script.Config.HTTP)
	}
	requirements := result.ProtectedRequirements.Take()
	protectedHeader, protectedBody := false, false
	for _, requirement := range requirements {
		protectedHeader = protectedHeader || strings.Contains(requirement.Value, headerCanary)
		protectedBody = protectedBody || strings.Contains(requirement.Value, bodyCanary)
	}
	if !protectedHeader || !protectedBody {
		t.Fatalf("request projection was skipped after proxy rejection: %#v", requirements)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
}
