package enterprisearchive

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestEnterpriseHTTPRequestCredentialsRemainExecutableAndPreserved(t *testing.T) {
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
	assertEnterpriseCredentialPreservedResult(
		t,
		direct,
		urlPassword,
		queryAPIKey,
		headerToken,
		formPassword,
		globalPassword,
		canaries,
	)

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
	assertEnterpriseCredentialPreservedResult(
		t,
		zipped,
		urlPassword,
		queryAPIKey,
		headerToken,
		formPassword,
		globalPassword,
		canaries,
	)
}

func TestEnterpriseRuntimeBindingMarksOpaqueDatabaseDSNForRuntimeDisclosure(t *testing.T) {
	const password = "enterprise-opaque-dsn-password-5f72"
	dsn := "postgres://db-user:" + password + "@db.example.test/orders"
	script := &dsl.Script{
		Name:          "database",
		Protocol:      "database",
		GeneratedFrom: "enterprise_script_archive",
		Config: dsl.ScriptConfig{Database: dsl.DatabaseConfig{
			Driver: "postgres",
			DSN:    dsn,
		}},
	}
	result := &ConvertResult{Script: script}
	applyEnterpriseRuntimeBindings(
		result,
		[]*dsl.Script{script},
		archiveFinalizationContext{},
	)
	if script.Config.Database.DSN != dsn {
		t.Fatalf("durable database DSN changed: %q", script.Config.Database.DSN)
	}
	preserved := script.Metadata.Import.Preserved
	if preserved[dsl.ImportPreservedCredentialPolicyKey] !=
		dsl.ImportPreservedCredentialPolicyPreserve {
		t.Fatalf("database credential preservation policy = %#v", preserved)
	}
	targetsPayload, err := json.Marshal(
		preserved[dsl.ImportPreservedCredentialTargetsMetadataKey],
	)
	if err != nil {
		t.Fatalf("marshal database credential locators: %v", err)
	}
	if !strings.Contains(string(targetsPayload), "config.database.dsn.opaque") {
		t.Fatalf("database DSN credential locator was discarded: %s", targetsPayload)
	}
	if requirements := result.ProtectedRequirements.Take(); len(requirements) != 0 {
		t.Fatalf("retained database DSN requires operator reentry: %#v", requirements)
	}
}

func TestConvertZipUnsupportedProxyRetainsCredentialsInDisabledRecord(t *testing.T) {
	const (
		proxyUsername = "unsupported-proxy-user-canary-4c17"
		proxyPassword = "unsupported-proxy-password-canary-8e62"
	)
	archive := buildSourceZip(t, map[string]string{
		"UnsupportedProxy.usr": `[General]
Type=Web
RuntimeSettings=default.cfg
[Actions]
Action1=Action.c
`,
		"Action.c": `Action()
{
	web_url("request", "URL=https://example.test/orders", LAST);
	return 0;
}
`,
		"default.cfg": `[Proxy]
Mode=automatic
ProxyUseProxy=1
ProxyServer=http://proxy.example.test:8080
ProxyUserName=` + proxyUsername + `
ProxyPassword=` + proxyPassword + `
`,
	})

	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	if result.Script == nil {
		t.Fatal("unsupported proxy conversion returned no script")
	}
	proxyConfig := result.Script.Config.HTTP.Proxy
	if result.Script.Config.HTTP.ProxyMode != "none" || proxyConfig.URL != "" {
		t.Fatalf(
			"unsupported proxy transport remained enabled: mode=%q proxy=%#v",
			result.Script.Config.HTTP.ProxyMode,
			proxyConfig,
		)
	}
	if proxyConfig.Username != proxyUsername || proxyConfig.Password != proxyPassword {
		t.Fatalf(
			"disabled proxy record lost exact source credentials: %#v",
			proxyConfig,
		)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("disabled proxy draft is not valid DSL: %#v", issues)
	}
	if len(result.Script.Actions) == 0 {
		t.Fatal("unsupported proxy conversion returned no request")
	}
	proxyParity := runtimeParityEntryByID(
		t,
		enterpriseRuntimeParity(t, result.Script),
		"proxy",
	)
	if proxyParity.Status != RuntimeSettingStatusPartial ||
		strings.Contains(proxyParity.Notes, proxyUsername) ||
		strings.Contains(proxyParity.Notes, proxyPassword) {
		t.Fatalf("unsupported proxy review provenance = %#v", proxyParity)
	}
}

func assertEnterpriseCredentialPreservedResult(
	t *testing.T,
	result *ConvertResult,
	urlPassword string,
	queryAPIKey string,
	headerToken string,
	formPassword string,
	globalPassword string,
	canaries []string,
) {
	t.Helper()
	if result.Script == nil || len(result.Script.Actions) == 0 {
		t.Fatalf("script = %#v, want executable request", result.Script)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
	if got := result.Script.Config.HTTP.Auth.Password; got != globalPassword {
		t.Fatalf("config HTTP auth password = %q, want imported executable bytes", got)
	}
	action := result.Script.Actions[0]
	if blocker := action.Metadata["execution_blocker"]; blocker != nil {
		t.Fatalf("request metadata = %#v, exact preserved credentials must remain executable", action.Metadata)
	}
	if got := action.Headers["Authorization"]; got != "Bearer "+headerToken {
		t.Fatalf("Authorization header = %q, want imported executable bytes", got)
	}
	if got := action.Body; got != "username=demo&password="+formPassword {
		t.Fatalf("request body = %q, want imported executable bytes", got)
	}
	if !strings.Contains(action.Target, "url-user:"+urlPassword+"@") ||
		!strings.Contains(action.Target, "api_key="+queryAPIKey) {
		t.Fatalf("request target = %q, want imported userinfo and query bytes", action.Target)
	}
	if len(action.HeaderList) == 0 ||
		action.HeaderList[0].Name != "Authorization" ||
		action.HeaderList[0].Value != "Bearer "+headerToken {
		t.Fatalf("header list = %#v, want ordered imported Authorization bytes", action.HeaderList)
	}
	originalURL, _ := action.Metadata["original_url"].(string)
	if !strings.Contains(originalURL, urlPassword) ||
		!strings.Contains(originalURL, queryAPIKey) {
		t.Fatalf("original URL = %q, want exact imported provenance", originalURL)
	}
	scriptPayload, err := json.Marshal(result.Script)
	if err != nil {
		t.Fatalf("json.Marshal(script): %v", err)
	}
	for _, canary := range canaries {
		if !strings.Contains(string(scriptPayload), canary) {
			t.Errorf("executable script omitted imported credential %q: %s", canary, scriptPayload)
		}
	}

	assertEnterpriseCredentialProvenancePreserved(t, result, canaries)
	preservedPayload, err := json.Marshal(result.Script.Metadata.Import.Preserved)
	if err != nil {
		t.Fatalf("json.Marshal(preserved provenance): %v", err)
	}
	for _, canary := range canaries {
		if !strings.Contains(string(preservedPayload), canary) && !strings.Contains(string(scriptPayload), canary) {
			t.Fatalf("source-authentication provenance lost imported value %q: %s", canary, preservedPayload)
		}
	}

	if requirements := result.ProtectedRequirements.Take(); len(requirements) != 0 {
		t.Fatalf("protected requirements = %#v, want no re-entry requirements for retained imported credentials", requirements)
	}
}

func assertEnterpriseCredentialProvenancePreserved(
	t *testing.T,
	result *ConvertResult,
	canaries []string,
) {
	t.Helper()
	if result == nil {
		t.Fatal("conversion result is nil")
	}
	var preserved map[string]any
	if result.Script != nil &&
		result.Script.Metadata != nil &&
		result.Script.Metadata.Import != nil {
		preserved = result.Script.Metadata.Import.Preserved
	}
	provenancePayload, err := json.Marshal([]any{
		result.Script,
		result.SourceReview,
		result.ReviewArtifact,
		result.FidelityReport,
		result.Warnings,
		preserved,
	})
	if err != nil {
		t.Fatalf("json.Marshal(provenance): %v", err)
	}
	for _, canary := range canaries {
		if !strings.Contains(string(provenancePayload), canary) {
			t.Errorf("source-review provenance lost imported value %q: %s", canary, provenancePayload)
		}
	}
}

func TestEnterpriseReviewPlaceholdersRetainImportedArgumentShape(t *testing.T) {
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
	if !strings.Contains(string(payload), canary) {
		t.Fatalf("review placeholder lost source argument canary: %s", payload)
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

func TestEnterpriseSpecializedReviewConstructorsRetainSourceStatements(t *testing.T) {
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
	if !strings.Contains(string(payload), canary) {
		t.Fatalf("specialized review constructor lost source statement: %s", payload)
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

func TestEnterpriseRuntimeBindingsNeverRestoreDisclosurePlaceholderAsCredential(t *testing.T) {
	for _, value := range []string{
		credential.Redacted,
		"Bearer [redacted]",
		"%5BREDACTED%5D",
		"W1JFREFDVEVEXQ==",
	} {
		t.Run(value, func(t *testing.T) {
			script := &dsl.Script{
				Name:     "runtime-sentinel",
				Protocol: "http",
				Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{
					Auth: dsl.AuthConfig{Type: "basic", Password: value},
				}},
				Actions: []dsl.Action{{
					Name: "request", Type: "request", Method: "GET", Target: "https://example.test",
				}},
			}
			result := &ConvertResult{Script: script}
			applyEnterpriseRuntimeBindings(
				result,
				[]*dsl.Script{script},
				archiveFinalizationContext{RuntimeConfig: &RuntimeConfig{AuthPass: value}},
			)

			password := script.Config.HTTP.Auth.Password
			if name, ok := credential.ParseReference(password); !ok || name != enterpriseHTTPAuthPasswordSecretName {
				t.Fatalf("password = %q, want unconfigured governed reference", password)
			}
			requirements := result.ProtectedRequirements.Take()
			if len(requirements) != 1 {
				t.Fatalf("requirements = %#v, want unavailable-value guidance", requirements)
			}
			requirement := requirements[0]
			if requirement.Value != "" || requirement.SourceValueAvailable ||
				requirement.SourceValueStatus != credentialpolicy.SourceCredentialValueRedactedPlaceholder ||
				requirement.Label != "HTTP authentication password" ||
				!strings.Contains(requirement.Context, "disclosure placeholder") ||
				!strings.Contains(requirement.Context, "cannot be preserved") ||
				len(requirement.Targets) != 1 ||
				requirement.Targets[0].Field != "config.http.auth.password" {
				t.Fatalf("unavailable runtime credential lacks descriptive reason/provenance: %#v", requirement)
			}
		})
	}
}

func TestEnterpriseMalformedBase64BodyRetainsExactSourceInValidBlockedDraft(t *testing.T) {
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
	if err != nil || string(decoded) != "not-valid-base64%%" {
		t.Fatalf("body = %q, want exact source in valid base64 envelope: decoded=%q err=%v", action.Body, decoded, err)
	}
	if action.Metadata["execution_blocker"] == nil {
		t.Fatalf("metadata = %#v, malformed encoded body must durably block execution", action.Metadata)
	}
}

func TestEnterpriseUnsafeProxyIsGatedWithoutDiscardingValidRequestCredentials(t *testing.T) {
	t.Parallel()
	const (
		proxyCanary  = "unsafe-proxy-fragment-canary"
		headerCanary = "request-header-after-proxy-canary"
		bodyCanary   = "request-body-after-proxy-canary"
	)
	proxyURL := "http://proxy.example.test:8080/?api_key=" + proxyCanary + "\xff"
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
				URL: proxyURL,
			},
		},
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if result.Script.Config.HTTP.ProxyMode != "none" ||
		result.Script.Config.HTTP.Proxy.URL != "" ||
		result.Script.Config.HTTP.Proxy.Username != "" ||
		result.Script.Config.HTTP.Proxy.Password != "" {
		t.Fatalf("unsafe proxy was not disabled: %#v", result.Script.Config.HTTP)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %#v, want valid imported request retained", result.Script.Actions)
	}
	action := result.Script.Actions[0]
	if got := action.Headers["Authorization"]; got != "Bearer "+headerCanary {
		t.Fatalf("Authorization header = %q, want valid request credential retained", got)
	}
	if got := action.Body; got != "username=demo&password="+bodyCanary {
		t.Fatalf("body = %q, want valid request credential retained", got)
	}
	if got := action.Metadata["execution_blocker"]; got != "import_review_required" {
		t.Fatalf("request metadata = %#v, want durable gate after rejecting malformed proxy", action.Metadata)
	}
	review, _ := action.Metadata["import_review"].(map[string]any)
	if !reflect.DeepEqual(
		review["reason_codes"],
		[]string{"runtime_proxy_url_credential_projection_failed"},
	) {
		t.Fatalf("import review = %#v, want stable malformed-proxy reason", review)
	}
	scriptPayload, err := json.Marshal(result.Script)
	if err != nil {
		t.Fatalf("json.Marshal(script): %v", err)
	}
	if strings.Contains(string(scriptPayload), proxyCanary) {
		t.Fatalf("blocked script retained malformed proxy credential: %s", scriptPayload)
	}
	assertEnterpriseUnsupportedProxySource(
		t,
		result.Script,
		"custom",
		proxyURL,
		"",
		"",
	)
	warningsJSON, err := json.Marshal(result.Warnings)
	if err != nil {
		t.Fatalf("json.Marshal(warnings): %v", err)
	}
	if strings.Contains(string(warningsJSON), proxyCanary) {
		t.Fatalf("warning disclosed malformed proxy credential: %s", warningsJSON)
	}
	for _, canary := range []string{headerCanary, bodyCanary} {
		if !strings.Contains(string(scriptPayload), canary) {
			t.Fatalf("blocked script discarded valid request credential %q: %s", canary, scriptPayload)
		}
	}
	if requirements := result.ProtectedRequirements.Take(); len(requirements) != 0 {
		t.Fatalf("protected requirements = %#v, want no re-entry requirements for retained request credentials", requirements)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
}

func TestEnterpriseStructurallyUnsupportedProxyIsOmittedFromGatedDraft(t *testing.T) {
	t.Parallel()
	const (
		proxyURL      = "ftp://proxy.example.test:21?token=proxy-query-canary"
		proxyUsername = "proxy-user-canary"
		proxyPassword = "proxy-password-canary"
	)
	parsed, err := ParseActionFile("Action.c", `Action()
{
	web_url("request", "URL=https://example.test/orders", LAST);
	return 0;
}`)
	if err != nil {
		t.Fatalf("ParseActionFile: %v", err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{
		HTTPConfig: &dsl.HTTPConfig{
			ProxyMode: "custom",
			Proxy: dsl.ProxyConfig{
				URL:      proxyURL,
				Username: proxyUsername,
				Password: proxyPassword,
			},
		},
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if result.Script == nil || len(result.Script.Actions) != 1 {
		t.Fatalf("script = %#v, want valid blocked draft", result.Script)
	}
	proxy := result.Script.Config.HTTP.Proxy
	if result.Script.Config.HTTP.ProxyMode != "none" ||
		proxy.URL != "" ||
		proxy.Username != "" ||
		proxy.Password != "" {
		t.Fatalf("proxy = %#v mode=%q, want structurally invalid proxy omitted", proxy, result.Script.Config.HTTP.ProxyMode)
	}
	assertEnterpriseUnsupportedProxySource(
		t,
		result.Script,
		"custom",
		proxyURL,
		proxyUsername,
		proxyPassword,
	)
	action := result.Script.Actions[0]
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("action metadata = %#v, want unsupported proxy gate", action.Metadata)
	}
	review, _ := action.Metadata["import_review"].(map[string]any)
	if !reflect.DeepEqual(
		review["reason_codes"],
		[]string{"runtime_proxy_url_credential_projection_failed"},
	) {
		t.Fatalf("import review = %#v, want stable proxy reason", review)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
	if requirements := result.ProtectedRequirements.Take(); len(requirements) != 0 {
		t.Fatalf("protected requirements = %#v, omitted invalid proxy must not request re-entry", requirements)
	}
}

func assertEnterpriseUnsupportedProxySource(
	t *testing.T,
	script *dsl.Script,
	wantMode string,
	wantURL string,
	wantUsername string,
	wantPassword string,
) {
	t.Helper()
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		t.Fatalf("script import metadata = %#v, want protected proxy source", script)
	}
	raw, ok := script.Metadata.Import.Preserved[dsl.ImportPreservedEnterpriseUnsupportedProxySourceKey].(map[string]any)
	if !ok {
		t.Fatalf(
			"unsupported proxy source = %#v",
			script.Metadata.Import.Preserved[dsl.ImportPreservedEnterpriseUnsupportedProxySourceKey],
		)
	}
	decode := func(field string) string {
		t.Helper()
		encoded, ok := raw[field].(string)
		if !ok {
			t.Fatalf("unsupported proxy %s = %#v", field, raw[field])
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("decode unsupported proxy %s: %v", field, err)
		}
		return string(decoded)
	}
	if got := decode("proxy_mode_base64"); got != wantMode {
		t.Fatalf("unsupported proxy mode = %q, want %q", got, wantMode)
	}
	if got := decode("url_base64"); got != wantURL {
		t.Fatalf("unsupported proxy URL bytes = %q, want %q", got, wantURL)
	}
	if got := decode("username_base64"); got != wantUsername {
		t.Fatalf("unsupported proxy username = %q, want %q", got, wantUsername)
	}
	if got := decode("password_base64"); got != wantPassword {
		t.Fatalf("unsupported proxy password = %q, want %q", got, wantPassword)
	}
}

func TestEnterpriseCallerHTTPConfigInvalidAuthIsRetainedAndGated(t *testing.T) {
	t.Parallel()
	const tokenCanary = "caller-bearer-canary\r\nX-Injected: yes"
	parsed, err := ParseActionFile("Action.c", `Action()
{
	web_url("request", "URL=https://example.test/orders", LAST);
	return 0;
}`)
	if err != nil {
		t.Fatalf("ParseActionFile: %v", err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{
		HTTPConfig: &dsl.HTTPConfig{
			Auth: dsl.AuthConfig{Type: "bearer", Token: tokenCanary},
		},
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	assertEnterpriseInvalidGlobalAuthGate(t, result, dsl.AuthConfig{
		Type:  "bearer",
		Token: tokenCanary,
	})
}

func TestConvertZipInvalidRuntimeAuthIsRetainedAndGated(t *testing.T) {
	t.Parallel()
	const passwordCanary = "runtime-invalid-auth-password-canary"
	archive := buildSourceZip(t, map[string]string{
		"InvalidAuth.usr": `[General]
Type=Web
RuntimeSettings=default.cfg
[Actions]
Action1=Action.c
`,
		"Action.c": `Action()
{
	web_url("request", "URL=https://example.test/orders", LAST);
	return 0;
}
`,
		"default.cfg": `[Authentication]
UserName=alice:admin
Password=` + passwordCanary + `
`,
	})
	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertEnterpriseInvalidGlobalAuthGate(t, result, dsl.AuthConfig{
		Type:     "basic",
		Username: "alice:admin",
		Password: passwordCanary,
	})
}

func assertEnterpriseInvalidGlobalAuthGate(
	t *testing.T,
	result *ConvertResult,
	wantAuth dsl.AuthConfig,
) {
	t.Helper()
	if result == nil || result.Script == nil || len(result.Script.Actions) == 0 {
		t.Fatalf("result = %#v, want a valid blocked draft", result)
	}
	auth := result.Script.Config.HTTP.Auth
	if !reflect.DeepEqual(auth, wantAuth) {
		t.Fatalf("global HTTP auth = %#v, want retained imported bytes %#v", auth, wantAuth)
	}
	action := result.Script.Actions[0]
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("action metadata = %#v, want invalid global-auth execution gate", action.Metadata)
	}
	review, _ := action.Metadata["import_review"].(map[string]any)
	if !reflect.DeepEqual(review["reason_codes"], []string{"invalid_auth_wire_value"}) {
		t.Fatalf("import review = %#v, want stable invalid-auth reason", review)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
	if requirements := result.ProtectedRequirements.Take(); len(requirements) != 0 {
		t.Fatalf("protected requirements = %#v, invalid auth must not request re-entry", requirements)
	}
}
