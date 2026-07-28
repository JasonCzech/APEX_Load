package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestRetainedSchemaCredentialRedactsEveryRuntimeBoundary(t *testing.T) {
	const pin = "7312049876"
	protocolName := registerReplayProtocolWithInitError(
		t,
		errors.New("remote rejected imported pin "+pin),
	)
	script := &dsl.Script{
		Name:     "retained-schema-credential",
		Protocol: protocolName,
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "openapi",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{{
			Name:   "request",
			Type:   "POST",
			Method: "POST",
			Target: "/verify",
			HeaderList: dsl.HeaderList{{
				Name:  "Content-Type",
				Value: "application/json",
			}},
			Body: `{"account":"42","pin":` + pin + `}`,
			Metadata: map[string]any{
				dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
					map[string]any{
						"request_key": "openapi:verify",
						"field":       "body",
						"pointer":     "/pin",
						"index":       2,
						"occurrence":  2,
					},
				},
			},
		}},
	}

	values, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	if !containsExactString(values, pin) {
		t.Fatalf("retained runtime values = %#v, want schema-annotated pin", values)
	}

	assertRedacted := func(label string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s returned nil error", label)
		}
		if strings.Contains(err.Error(), pin) || !strings.Contains(err.Error(), redactedPayloadValue) {
			t.Fatalf("%s error = %q, want retained pin redacted", label, err)
		}
	}

	_, err = ReplayScript(context.Background(), script, ReplayOptions{})
	assertRedacted("replay", err)
	assertRedacted(
		"shakeout",
		RunScriptShakeoutWithSecretResolver(context.Background(), "shakeout", script, nil),
	)

	load, err := New(Config{Script: script, VUsers: 1, Duration: time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = load.Run(context.Background())
	assertRedacted("load", err)

	multi, err := NewMultiGroup(MultiGroupConfig{
		Duration: time.Second,
		Groups: []GroupConfig{{
			Name:     "group",
			Script:   script,
			VUsers:   1,
			RampUp:   0,
			RampDown: 0,
		}},
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	_, err = multi.Run(context.Background())
	assertRedacted("multi-group load", err)
}

func TestImportedCredentialValuesIncludesRecordedResourceURLCredentials(t *testing.T) {
	const (
		password = "cdn-password-7319"
		token    = "cdn-token-6248"
	)
	script := &dsl.Script{
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "browser",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
				"recorded_resource_urls": []any{
					"https://operator:" + password + "@cdn.example.test/app.js?token=" + token + "&variant=modern",
					42,
				},
			},
		}},
	}

	values, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	for _, want := range []string{password, token} {
		if !containsExactString(values, want) {
			t.Fatalf("retained runtime values = %#v, want %q", values, want)
		}
	}
	if containsExactString(values, "modern") {
		t.Fatalf("retained runtime values = %#v, nonsensitive query value was included", values)
	}
}

func TestImportedCredentialValuesDereferencesExecutableURLSurfaces(t *testing.T) {
	const (
		basePassword     = "base-url-password-63d1"
		tokenURLPassword = "token-url-password-82f4"
		provenanceOnly   = "original-url-password-29a6"
	)
	script := &dsl.Script{
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: "https://base-user:" + basePassword + "@api.example.test",
		},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "fixture",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
				dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
					map[string]any{
						"request_key": "fixture:base",
						"field":       "config.base_url.userinfo",
					},
				},
			},
		}},
		Init: []dsl.Action{{
			Name: "OAuth token",
			Type: "http_oauth2_token",
			Params: map[string]any{
				"token_url": "https://oauth-user:" + tokenURLPassword + "@auth.example.test/token",
			},
			Metadata: map[string]any{
				dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
					map[string]any{
						"request_key": "fixture:oauth",
						"field":       "params.token_url.userinfo",
					},
				},
			},
		}},
		Actions: []dsl.Action{{
			Name:   "Orders",
			Type:   "GET",
			Method: "GET",
			Target: "/orders",
			Metadata: map[string]any{
				// Provenance is deliberately not a runtime locator.
				"original_url": "https://source-user:" + provenanceOnly + "@source.example.test/orders",
			},
		}},
	}

	values, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	for _, want := range []string{basePassword, tokenURLPassword} {
		if !containsExactString(values, want) {
			t.Fatalf("retained values = %#v, want executable URL credential %q", values, want)
		}
	}
	if containsExactString(values, provenanceOnly) {
		t.Fatalf("retained values = %#v, provenance-only original URL was treated as executable", values)
	}
}

func TestImportedCredentialValuesIncludesMarkedBrowserActionValues(t *testing.T) {
	const (
		fillSecret   = "captured-fill-secret-7319"
		selectSecret = "captured-select-secret-6248"
		secondSecret = "captured-select-secret-1957"
	)
	protocolName := registerReplayProtocolWithInitError(
		t,
		errors.New("browser retained "+fillSecret+" "+selectSecret+" "+secondSecret),
	)
	script := &dsl.Script{
		Name:     "retained-browser-credentials",
		Protocol: protocolName,
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "browser",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{
			{
				Name:  "Fill password",
				Type:  "browser_fill",
				Value: fillSecret,
				Metadata: map[string]any{
					"source_marked_secret": true,
				},
			},
			{
				Name:  "Select credentials",
				Type:  "browser_select",
				Value: selectSecret,
				Params: map[string]any{
					// []any is the representation after a durable JSON
					// round-trip through storage.
					"values": []any{selectSecret, secondSecret, 42},
				},
				Metadata: map[string]any{
					"source_masked_secret": true,
				},
			},
			{
				Name:  "Unmarked value",
				Type:  "browser_fill",
				Value: "ordinary-browser-value",
			},
		},
	}

	values, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	for _, want := range []string{fillSecret, selectSecret, secondSecret} {
		if !containsExactString(values, want) {
			t.Fatalf("retained runtime values = %#v, want %q", values, want)
		}
	}
	if containsExactString(values, "ordinary-browser-value") {
		t.Fatalf("retained runtime values = %#v, unmarked browser value was included", values)
	}
	if script.Actions[0].Value != fillSecret ||
		script.Actions[1].Value != selectSecret ||
		script.Actions[1].Params["values"].([]any)[1] != secondSecret {
		t.Fatalf("credential collection mutated durable browser actions: %#v", script.Actions)
	}

	_, err = ReplayScript(context.Background(), script, ReplayOptions{})
	if err == nil {
		t.Fatal("ReplayScript returned nil error")
	}
	for _, secret := range []string{fillSecret, selectSecret, secondSecret} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("ReplayScript error leaked retained browser credential %q: %q", secret, err)
		}
	}
	if !strings.Contains(err.Error(), redactedPayloadValue) {
		t.Fatalf("ReplayScript error = %q, want runtime redaction marker", err)
	}
}

func TestImportedCredentialValuesIncludesSensitiveJavaVMArguments(t *testing.T) {
	const (
		password = "java-vm-password-7319"
		token    = "java-vm-token-6248"
	)
	passwordArgument := "-Dservice.password=" + password
	urlValue := "https://service.example.test/connect?api_key=" + token
	urlArgument := "-Dservice.endpoint=" + urlValue
	protocolName := registerReplayProtocolWithInitError(
		t,
		errors.New("runner rejected "+passwordArgument+" and "+urlArgument),
	)
	script := &dsl.Script{
		Name:     "retained-java-vm-credentials",
		Protocol: protocolName,
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "enterprise_archive",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Config: dsl.ScriptConfig{JavaVuser: dsl.JavaVuserConfig{
			AdditionalVMParameters: []string{
				passwordArgument,
				urlArgument,
				"-Dservice.label=ordinary",
			},
		}},
		Actions: []dsl.Action{{Name: "noop", Type: "noop"}},
	}

	values, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	for _, want := range []string{
		passwordArgument,
		password,
		urlArgument,
		urlValue,
	} {
		if !containsExactString(values, want) {
			t.Fatalf("retained runtime values = %#v, want exact JVM value %q", values, want)
		}
	}
	if containsExactString(values, "-Dservice.label=ordinary") ||
		containsExactString(values, "ordinary") {
		t.Fatalf("retained runtime values = %#v, nonsensitive JVM argument was included", values)
	}
	if got := script.Config.JavaVuser.AdditionalVMParameters; len(got) != 3 ||
		got[0] != passwordArgument || got[1] != urlArgument {
		t.Fatalf("credential collection mutated durable JVM arguments: %#v", got)
	}

	_, err = ReplayScript(context.Background(), script, ReplayOptions{})
	if err == nil {
		t.Fatal("ReplayScript returned nil error")
	}
	for _, secret := range []string{passwordArgument, password, urlArgument, urlValue, token} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("ReplayScript error leaked retained JVM credential %q: %q", secret, err)
		}
	}
	if !strings.Contains(err.Error(), redactedPayloadValue) {
		t.Fatalf("ReplayScript error = %q, want runtime redaction marker", err)
	}
}

func TestImportedCredentialValuesResolvesCompositionalMultipartAndXMLLocators(t *testing.T) {
	const (
		outerBoundary  = "outer-runtime-locator"
		innerBoundary  = "inner-runtime-locator"
		partHeader     = "part-header-secret-7319"
		firstPassword  = "first-password-decoy-6248"
		secondPassword = "second-password-secret-1957"
		branchPassword = "branch-password-secret-4802"
		formPassword   = "nested-form-secret-9173"
	)
	inner := "--" + innerBoundary + "\r\n" +
		"Content-Disposition: form-data; name=\"payload\"\r\n" +
		"Content-Type: application/x-www-form-urlencoded\r\n\r\n" +
		"ordinary=keep&password=" + formPassword + "\r\n" +
		"--" + innerBoundary + "--\r\n"
	body := "--" + outerBoundary + "\r\n" +
		"Authorization: Bearer " + partHeader + "\r\n" +
		"Content-Disposition: form-data; name=\"headers\"\r\n\r\n" +
		"ordinary\r\n" +
		"--" + outerBoundary + "\r\n" +
		"Content-Disposition: form-data; name=\"xml\"\r\n" +
		"Content-Type: application/xml\r\n\r\n" +
		"<root><group><password>" + firstPassword + "</password>" +
		"<password>" + secondPassword + "</password></group>" +
		"<other><password>" + branchPassword + "</password></other></root>\r\n" +
		"--" + outerBoundary + "\r\n" +
		"Content-Disposition: form-data; name=\"nested\"\r\n" +
		"Content-Type: multipart/mixed; boundary=" + innerBoundary + "\r\n\r\n" +
		inner +
		"--" + outerBoundary + "--\r\n"
	script := &dsl.Script{
		Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "fixture",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{{
			Name:   "multipart",
			Type:   "POST",
			Method: "POST",
			Target: "/upload",
			HeaderList: dsl.HeaderList{{
				Name:  "Content-Type",
				Value: "multipart/form-data; boundary=" + outerBoundary,
			}},
			Body: body,
			Metadata: map[string]any{
				dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
					map[string]any{
						"field": "body.multipart.parts[0].headers",
						"index": 0,
					},
					map[string]any{
						"field":      "body.multipart.parts[1].body.xml.text",
						"pointer":    "/root/group/password",
						"occurrence": 1,
					},
					map[string]any{
						"field":   "body.multipart.parts[1].body.xml.text",
						"pointer": "/root/other/password",
					},
					map[string]any{
						"field": "body.multipart.parts[2].body.multipart.parts[0].body.form",
						"index": 1,
					},
				},
			},
		}},
	}

	values, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	for _, want := range []string{
		"Bearer " + partHeader,
		secondPassword,
		branchPassword,
		formPassword,
	} {
		if !containsExactString(values, want) {
			t.Fatalf("retained runtime values = %#v, want %q", values, want)
		}
	}
	for _, unwanted := range []string{
		firstPassword,
		"ordinary",
		"keep",
	} {
		if containsExactString(values, unwanted) {
			t.Fatalf("retained runtime values = %#v, unexpectedly included %q", values, unwanted)
		}
	}
}

func TestImportedCredentialValuesDereferencesEnterpriseConfigLocatorsAndComponents(t *testing.T) {
	const (
		proxyToken  = "enterprise-proxy-query-component-4d91"
		dsnPassword = "enterprise-dsn-password-component-8a27"
		dsnOption   = "enterprise-dsn-neutral-option-6c53"
		jvmToken    = "enterprise-jvm-url-token-component-2f84"
	)
	proxyURL := "http://proxy.example.test:8080?display=" + proxyToken
	dsn := "postgres://db-user:" + dsnPassword +
		"@db.example.test/orders?display=" + dsnOption
	jvmValue := "https://service.example.test/connect?api_key=" + jvmToken
	jvmArgument := "-Dservice.endpoint=" + jvmValue
	script := &dsl.Script{
		Name:     "enterprise-config-credentials",
		Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "enterprise_script_archive",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
				dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
					map[string]any{
						"request_key": "enterprise:proxy",
						"field":       "config.http.proxy.url.query",
						"index":       0,
					},
					map[string]any{
						"request_key": "enterprise:dsn",
						"field":       "config.database.dsn.opaque",
					},
					map[string]any{
						"request_key": "enterprise:jvm",
						"field":       "config.java_vuser.additional_vm_parameters.opaque",
						"index":       0,
					},
				},
			},
		}},
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{Proxy: dsl.ProxyConfig{URL: proxyURL}},
			Database: dsl.DatabaseConfig{
				DSN: dsn,
			},
			JavaVuser: dsl.JavaVuserConfig{
				AdditionalVMParameters: []string{jvmArgument},
			},
		},
	}

	values, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	for _, want := range []string{
		proxyToken,
		dsn,
		dsnPassword,
		dsnOption,
		jvmArgument,
		jvmValue,
		jvmToken,
	} {
		if !containsExactString(values, want) {
			t.Fatalf("retained config values = %#v, want %q", values, want)
		}
	}
	if script.Config.HTTP.Proxy.URL != proxyURL ||
		script.Config.Database.DSN != dsn ||
		script.Config.JavaVuser.AdditionalVMParameters[0] != jvmArgument {
		t.Fatalf("credential collection mutated durable config: %#v", script.Config)
	}
}

func containsExactString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
