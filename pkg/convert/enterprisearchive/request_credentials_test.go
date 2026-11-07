package enterprisearchive

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
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

func TestConvertZipDisabledProxyDropsEncryptedPasswordSecret(t *testing.T) {
	const encryptedCanary = "encrypted-proxy-password-canary-91d3"
	for _, test := range []struct {
		name    string
		section string
		flags   string
	}{
		// VuGen leaves both portable selectors on after an operator clears the
		// proxy host; the conflict disables proxying for review.
		{name: "browser and PAC selectors conflict", section: "WEB", flags: "ProxyUseBrowser=1\nProxyUseProxy=1\nProxyUseAutoConfigScript=1\nProxyAutoConfigScriptURL=http://autoproxy.example.test/autoproxy.cgi\nProxyUseProxyServer=0\n"},
		{name: "malformed enable flag", section: "WEB", flags: "ProxyUseProxy=maybe\nProxyHTTPHost=proxy.example.test\nProxyHTTPPort=8080\n"},
		{name: "Proxy section custom mode without server", section: "Proxy", flags: "Mode=custom\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			archive := buildSourceZip(t, map[string]string{
				"DisabledProxy.usr": "[General]\nType=Web\nRuntimeSettings=default.cfg\n[Actions]\nAction1=Action.c\n",
				"Action.c": `Action()
{
	web_url("request", "URL=https://example.test/orders", LAST);
	return 0;
}
`,
				"default.cfg": "[" + test.section + "]\n" + test.flags +
					"ProxyUserName=proxy-user\nProxyPassword=" + encryptedCanary +
					"\nProxyPasswordIsEncrypted=true\n",
			})
			result, err := ConvertZip(archive, ConvertOptions{})
			if err != nil {
				t.Fatalf("ConvertZip: %v", err)
			}
			httpConfig := result.Script.Config.HTTP
			if httpConfig.ProxyMode != "none" || httpConfig.Proxy.URL != "" {
				t.Fatalf("unrepresentable proxy stayed enabled: %#v", httpConfig)
			}
			if httpConfig.Proxy.Password != "" {
				t.Fatalf("disabled proxy kept an unrecoverable password reference: %q", httpConfig.Proxy.Password)
			}
			for _, action := range result.Script.Init {
				if action.Name == "Restore proxy credential" {
					t.Fatalf("disabled proxy still gates execution on its password: %#v", action)
				}
			}
			// Source-review evidence keeps the raw cfg by design; only the
			// executable draft must be free of the password and its secret.
			payload, err := json.Marshal(struct {
				Config  dsl.ScriptConfig `json:"config"`
				Init    []dsl.Action     `json:"init"`
				Actions []dsl.Action     `json:"actions"`
			}{result.Script.Config, result.Script.Init, result.Script.Actions})
			if err != nil {
				t.Fatalf("marshal script: %v", err)
			}
			if strings.Contains(string(payload), encryptedCanary) ||
				strings.Contains(string(payload), enterpriseProxyPasswordSecretName) {
				t.Fatalf("executable draft retained the encrypted proxy password or its secret: %s", payload)
			}
			if httpConfig.Proxy.Username != "proxy-user" {
				t.Fatalf("plaintext proxy username review record = %q, want retained", httpConfig.Proxy.Username)
			}
			row := encryptedProxyMarkerDisposition(t, result.Script, test.section)
			if row.Status == RuntimeSettingDispositionMapped ||
				strings.Contains(strings.Join(row.Fields, ","), "config.http.proxy.password") ||
				!strings.Contains(row.Note, "not imported") {
				t.Fatalf("ProxyPasswordIsEncrypted disposition still demands a proxy secret: %#v", row)
			}
		})
	}
}

// An encrypted proxy password is called out only for an active custom proxy,
// and the callout never blocks: the live required-secret check is the only
// proxy credential blocker. A System or PAC proxy keeps the reference (ready
// if the mode later becomes Custom) with no callout.
func TestConvertZipEnabledProxyEncryptedPasswordCallout(t *testing.T) {
	const encryptedCanary = "encrypted-proxy-password-canary-4f20"
	for _, test := range []struct {
		name        string
		flags       string
		wantMode    string
		wantURL     string
		wantPAC     string
		wantCallout bool
		// userinfo carries the encrypted password in the ProxyServer URL
		// instead of a ProxyPassword key.
		userinfo bool
	}{
		{name: "custom proxy", flags: "ProxyUseProxy=1\nProxyUseProxyServer=1\nProxyHTTPHost=proxy.example.test\nProxyHTTPPort=8080\n", wantMode: "custom", wantURL: "http://proxy.example.test:8080", wantCallout: true},
		{name: "custom proxy, encrypted password in ProxyServer userinfo", flags: "ProxyUseProxy=1\nProxyUseProxyServer=1\nProxyServer=http://proxy-user:" + encryptedCanary + "@proxy.example.test:8080\n", wantMode: "custom", wantURL: "http://proxy.example.test:8080", wantCallout: true, userinfo: true},
		{name: "PAC proxy", flags: "ProxyUseBrowser=0\nProxyUseProxy=1\nProxyUseProxyServer=0\nProxyUseAutoConfigScript=1\nProxyAutoConfigScriptURL=https://proxy.example.test/config.pac\n", wantMode: "pac", wantPAC: "https://proxy.example.test/config.pac"},
		{name: "system proxy", flags: "ProxyUseBrowser=1\nProxyUseProxy=1\nProxyUseProxyServer=0\nProxyUseAutoConfigScript=0\n", wantMode: "system"},
		// A portable selector clears the ProxyServer URL, so the userinfo
		// username must survive with the password reference.
		{name: "system proxy, encrypted password in ProxyServer userinfo", flags: "ProxyUseBrowser=1\nProxyUseProxy=1\nProxyUseProxyServer=0\nProxyServer=http://proxy-user:" + encryptedCanary + "@proxy.example.test:8080\n", wantMode: "system", userinfo: true},
		{name: "PAC proxy, encrypted password in ProxyServer userinfo", flags: "ProxyUseBrowser=0\nProxyUseProxy=1\nProxyUseProxyServer=0\nProxyUseAutoConfigScript=1\nProxyAutoConfigScriptURL=https://proxy.example.test/config.pac\nProxyServer=http://proxy-user:" + encryptedCanary + "@proxy.example.test:8080\n", wantMode: "pac", wantPAC: "https://proxy.example.test/config.pac", userinfo: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			archive := buildSourceZip(t, map[string]string{
				"EnabledProxy.usr": "[General]\nType=Web\nRuntimeSettings=default.cfg\n[Actions]\nAction1=Action.c\n",
				"Action.c": `Action()
{
	web_url("request", "URL=https://example.test/orders", LAST);
	return 0;
}
`,
				"default.cfg": "[WEB]\n" + test.flags +
					map[bool]string{false: "ProxyUserName=proxy-user\nProxyPassword=" + encryptedCanary + "\n"}[test.userinfo] +
					"ProxyPasswordIsEncrypted=true\n",
			})
			result, err := ConvertZip(archive, ConvertOptions{})
			if err != nil {
				t.Fatalf("ConvertZip: %v", err)
			}
			httpConfig := result.Script.Config.HTTP
			if httpConfig.ProxyMode != test.wantMode || httpConfig.Proxy.URL != test.wantURL ||
				httpConfig.Proxy.PACURL != test.wantPAC {
				t.Fatalf("enabled proxy = %#v, want mode %q url %q pac %q", httpConfig, test.wantMode, test.wantURL, test.wantPAC)
			}
			if httpConfig.Proxy.Password != encryptedRuntimeProxyPasswordPlaceholder {
				t.Fatalf("enabled proxy password = %q, want the retained %q reference", httpConfig.Proxy.Password, encryptedRuntimeProxyPasswordPlaceholder)
			}
			// The username travels with the retained password reference on
			// every path, including a userinfo-only source under a portable
			// selector that drops the ProxyServer URL.
			if httpConfig.Proxy.Username != "proxy-user" {
				t.Fatalf("enabled proxy username = %q, want proxy-user kept with the password reference", httpConfig.Proxy.Username)
			}
			callouts := 0
			for _, action := range result.Script.Init {
				if action.Name != "Restore proxy credential" {
					continue
				}
				callouts++
				payload, err := json.Marshal(action)
				if err != nil {
					t.Fatalf("marshal callout: %v", err)
				}
				if !strings.Contains(string(payload), dsl.EncryptedProxyPasswordFindingCode) ||
					!strings.Contains(string(payload), "replace_proxy_secret") {
					t.Fatalf("proxy credential callout lost its finding or repair: %s", payload)
				}
				// A generator built before the callout became advisory blocks
				// every execution_blocker code, so the import must write the
				// callout under its own key only.
				if _, ok := action.Metadata["execution_blocker"]; ok {
					t.Fatalf("callout stamped as execution_blocker: %s", payload)
				}
				if _, ok := action.Metadata["execution_blockers"]; ok {
					t.Fatalf("callout stamped as execution_blockers: %s", payload)
				}
				if codes, _ := action.Metadata[dsl.AdvisoryCalloutsMetadataKey].([]string); !slices.Equal(codes, []string{dsl.EncryptedProxyPasswordFindingCode}) {
					t.Fatalf("%s = %#v, want the callout code", dsl.AdvisoryCalloutsMetadataKey, action.Metadata[dsl.AdvisoryCalloutsMetadataKey])
				}
			}
			if want := map[bool]int{true: 1, false: 0}[test.wantCallout]; callouts != want {
				t.Fatalf("Restore proxy credential callouts = %d, want %d: %#v", callouts, want, result.Script.Init)
			}
			// The callout is visible as a finding with its repair, but never
			// blocks readiness by itself.
			findings, err := importreview.FindingsFromScript(result.Script)
			if err != nil {
				t.Fatalf("FindingsFromScript: %v", err)
			}
			found := false
			for _, finding := range findings {
				if finding.Code != dsl.EncryptedProxyPasswordFindingCode {
					continue
				}
				found = true
				if finding.Severity == diag.Blocker || finding.Gate == importreview.GateExecution ||
					finding.Resolution != "replace_proxy_secret" {
					t.Fatalf("encrypted proxy password finding = %#v, want a non-blocking callout with its repair", finding)
				}
			}
			if found != test.wantCallout {
				t.Fatalf("encrypted proxy password finding present = %v, want %v: %#v", found, test.wantCallout, findings)
			}
			readiness := importreview.Evaluate(importreview.EvaluationInput{Findings: findings})
			if slices.Contains(readiness.BlockingCodes, dsl.EncryptedProxyPasswordFindingCode) {
				t.Fatalf("readiness blocks on the encrypted proxy password callout: %#v", readiness)
			}
			payload, err := json.Marshal(struct {
				Config  dsl.ScriptConfig `json:"config"`
				Init    []dsl.Action     `json:"init"`
				Actions []dsl.Action     `json:"actions"`
			}{result.Script.Config, result.Script.Init, result.Script.Actions})
			if err != nil {
				t.Fatalf("marshal script: %v", err)
			}
			if strings.Contains(string(payload), encryptedCanary) {
				t.Fatalf("executable draft leaked the encrypted proxy password: %s", payload)
			}
			row := encryptedProxyMarkerDisposition(t, result.Script, "WEB")
			if row.Status != RuntimeSettingDispositionMapped ||
				strings.Join(row.Fields, ",") != "config.http.proxy.password" {
				t.Fatalf("ProxyPasswordIsEncrypted disposition = %#v, want the mapped protected-password row", row)
			}
			if test.wantCallout {
				if !strings.Contains(row.Note, "provision the protected proxy password") {
					t.Fatalf("custom proxy disposition = %#v, want the provision note", row)
				}
			} else if strings.Contains(row.Note, "provision") ||
				!strings.Contains(row.Note, "only if you switch Proxy mode to Custom") ||
				!strings.Contains(row.Note, "Proxy mode "+dsl.HTTPProxyModeLabel(httpConfig)+" does not send") {
				t.Fatalf("%s proxy disposition = %#v, want the only-needed-for-Custom note", test.wantMode, row)
			}
		})
	}
}

// The encrypted-password lift from ProxyServer userinfo attributes both
// lifted credentials to ProxyServer, the key the source actually carried.
func TestEnterpriseRuntimeSourceValuesAttributeLiftedUserinfoToProxyServer(t *testing.T) {
	for _, selector := range []string{
		"ProxyUseBrowser=1",
		"ProxyUseAutoConfigScript=1\nProxyAutoConfigScriptURL=https://proxy.example.test/config.pac",
		"ProxyUseProxyServer=1",
	} {
		t.Run(strings.SplitN(selector, "\n", 2)[0], func(t *testing.T) {
			rc := ParseRuntimeCfg("[WEB]\nProxyUseProxy=1\n" + selector +
				"\nProxyServer=http://proxy-user:encrypted-canary@proxy.example.test:8080\nProxyPasswordIsEncrypted=true\n")
			values := enterpriseRuntimeSourceValues(archiveFinalizationContext{RuntimeFileName: "default.cfg", RuntimeConfig: rc})
			user, pass := values["config.http.proxy.username"], values["config.http.proxy.password"]
			if user.value != "proxy-user" || user.source.Pointer != "default.cfg#Proxy/ProxyServer" {
				t.Fatalf("username source = %q %#v, want proxy-user from Proxy/ProxyServer", user.value, user.source)
			}
			if pass.value != encryptedRuntimeProxyPasswordPlaceholder || pass.source.Pointer != "default.cfg#Proxy/ProxyServer" {
				t.Fatalf("password source = %q %#v, want the placeholder from Proxy/ProxyServer", pass.value, pass.source)
			}
		})
	}
}

// An explicit [Proxy] Mode=system selection drops the source credentials
// rather than keeping the reference (unlike ProxyUseBrowser). The row must
// then say the encrypted password was not imported, never that the source had
// none.
func TestConvertZipExplicitSystemProxyDropsEncryptedPassword(t *testing.T) {
	const encryptedCanary = "encrypted-proxy-password-canary-91c3"
	archive := buildSourceZip(t, map[string]string{
		"SystemProxy.usr": "[General]\nType=Web\nRuntimeSettings=default.cfg\n[Actions]\nAction1=Action.c\n",
		"Action.c": `Action()
{
	web_url("request", "URL=https://example.test/orders", LAST);
	return 0;
}
`,
		"default.cfg": "[Proxy]\nMode=system\nProxyUserName=proxy-user\nProxyPassword=" + encryptedCanary + "\nProxyPasswordIsEncrypted=true\n",
	})
	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	httpConfig := result.Script.Config.HTTP
	if httpConfig.ProxyMode != "system" || httpConfig.Proxy.Password != "" {
		t.Fatalf("proxy = %#v, want an explicit System proxy without the encrypted password", httpConfig)
	}
	for _, action := range result.Script.Init {
		if action.Name == "Restore proxy credential" {
			t.Fatalf("System proxy called out its dropped password: %#v", action)
		}
	}
	payload, err := json.Marshal(result.Script.Config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), encryptedCanary) {
		t.Fatalf("draft leaked the encrypted proxy password: %s", payload)
	}
	row := encryptedProxyMarkerDisposition(t, result.Script, "Proxy")
	if row.Status == RuntimeSettingDispositionMapped || strings.Contains(row.Note, "marker only") ||
		strings.Contains(row.Note, "provision") ||
		!strings.Contains(row.Note, "Encrypted proxy password not imported: Proxy mode System does not send proxy credentials") ||
		!strings.Contains(row.Note, "If you switch Proxy mode to Custom, add its password as a project secret.") {
		t.Fatalf("explicit System proxy row = %#v, want the not-imported note", row)
	}
}

// The docs' System rule, pinned: a System mode key alone ([Proxy] Mode or
// [WEB] ProxyMode) drops the encrypted password, but the same mode beside
// ProxyUseBrowser=1 keeps the {{secret.ENTERPRISE_PROXY_PASSWORD}} reference
// with only the needed-for-Custom note.
func TestConvertZipSystemProxyEncryptedPasswordDocumentedRule(t *testing.T) {
	const encryptedCanary = "encrypted-proxy-password-canary-5d27"
	credentials := "ProxyUserName=proxy-user\nProxyPassword=" + encryptedCanary + "\nProxyPasswordIsEncrypted=true\n"
	for _, test := range []struct {
		name     string
		cfg      string
		section  string
		wantKept bool
	}{
		{name: "[WEB] ProxyMode=system", cfg: "[WEB]\nProxyMode=system\n" + credentials, section: "WEB"},
		{name: "[Proxy] Mode=system with ProxyUseBrowser", cfg: "[Proxy]\nMode=system\n[WEB]\nProxyUseBrowser=1\n" + credentials, section: "WEB", wantKept: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			archive := buildSourceZip(t, map[string]string{
				"SystemProxy.usr": "[General]\nType=Web\nRuntimeSettings=default.cfg\n[Actions]\nAction1=Action.c\n",
				"Action.c":        "Action()\n{\n\tweb_url(\"request\", \"URL=https://example.test/orders\", LAST);\n\treturn 0;\n}\n",
				"default.cfg":     test.cfg,
			})
			result, err := ConvertZip(archive, ConvertOptions{})
			if err != nil {
				t.Fatalf("ConvertZip: %v", err)
			}
			httpConfig := result.Script.Config.HTTP
			wantPassword := ""
			if test.wantKept {
				wantPassword = encryptedRuntimeProxyPasswordPlaceholder
			}
			if httpConfig.ProxyMode != "system" || httpConfig.Proxy.Password != wantPassword {
				t.Fatalf("proxy = %#v, want System with password %q", httpConfig, wantPassword)
			}
			for _, action := range result.Script.Init {
				if action.Name == "Restore proxy credential" {
					t.Fatalf("System proxy produced a callout: %#v", action)
				}
			}
			row := encryptedProxyMarkerDisposition(t, result.Script, test.section)
			if strings.Contains(row.Note, "provision") {
				t.Fatalf("System proxy row asks to provision: %#v", row)
			}
			if test.wantKept && !strings.Contains(row.Note, "needed only if you switch Proxy mode to Custom") {
				t.Fatalf("kept reference row = %#v, want the needed-for-Custom note", row)
			}
			if !test.wantKept && !strings.Contains(row.Note, "Encrypted proxy password not imported") {
				t.Fatalf("dropped password row = %#v, want the not-imported note", row)
			}
		})
	}
}

// A dropped encrypted password under a draft that uses no proxy names the
// only mode that would need it (Custom) and never says "turn the proxy back
// on": a dormant source never had the proxy on.
func TestConvertZipDroppedEncryptedProxyPasswordNoteNamesCustom(t *testing.T) {
	const encryptedCanary = "encrypted-proxy-password-canary-0b6e"
	const want = "Encrypted proxy password not imported: this draft does not use a proxy, so no proxy secret is needed. If you switch Proxy mode to Custom, add its password as a project secret."
	credentials := "ProxyUserName=proxy-user\nProxyPassword=" + encryptedCanary + "\nProxyPasswordIsEncrypted=true\n"
	for _, test := range []struct {
		name  string
		flags string
	}{
		{name: "dormant source", flags: ""},
		{name: "disabled for review", flags: "ProxyUseBrowser=1\nProxyUseProxy=1\nProxyUseAutoConfigScript=1\nProxyAutoConfigScriptURL=http://autoproxy.example.test/autoproxy.cgi\nProxyUseProxyServer=0\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			archive := buildSourceZip(t, map[string]string{
				"DormantProxy.usr": "[General]\nType=Web\nRuntimeSettings=default.cfg\n[Actions]\nAction1=Action.c\n",
				"Action.c":         "Action()\n{\n\tweb_url(\"request\", \"URL=https://example.test/orders\", LAST);\n\treturn 0;\n}\n",
				"default.cfg":      "[WEB]\n" + test.flags + credentials,
			})
			result, err := ConvertZip(archive, ConvertOptions{})
			if err != nil {
				t.Fatalf("ConvertZip: %v", err)
			}
			if password := result.Script.Config.HTTP.Proxy.Password; password != "" {
				t.Fatalf("proxy password = %q, want the encrypted placeholder dropped", password)
			}
			row := encryptedProxyMarkerDisposition(t, result.Script, "WEB")
			if row.Status != RuntimeSettingDispositionNonApplicable || row.Note != want {
				t.Fatalf("ProxyPasswordIsEncrypted disposition = %#v, want non-applicable with note %q", row, want)
			}
		})
	}
}

func TestConvertZipEncryptedProxyMarkerWithoutPassword(t *testing.T) {
	for _, test := range []struct {
		name     string
		flags    string
		wantMode string
	}{
		// The marker alone must not claim a password was dropped.
		{name: "disabled proxy", flags: "ProxyUseBrowser=1\nProxyUseProxy=1\nProxyUseAutoConfigScript=1\nProxyAutoConfigScriptURL=http://autoproxy.example.test/autoproxy.cgi\nProxyUseProxyServer=0\n", wantMode: "none"},
		// An active proxy must never be described as unused.
		{name: "PAC proxy", flags: "ProxyUseBrowser=0\nProxyUseProxy=1\nProxyUseProxyServer=0\nProxyUseAutoConfigScript=1\nProxyAutoConfigScriptURL=https://proxy.example.test/config.pac\n", wantMode: "pac"},
		{name: "system proxy", flags: "ProxyUseBrowser=1\nProxyUseProxy=1\nProxyUseProxyServer=0\nProxyUseAutoConfigScript=0\n", wantMode: "system"},
		// A custom proxy without a password needs no proxy secret: no
		// callout, and the row must not ask to provision one.
		{name: "custom proxy", flags: "ProxyUseProxy=1\nProxyUseProxyServer=1\nProxyHTTPHost=proxy.example.test\nProxyHTTPPort=8080\n", wantMode: "custom"},
		{name: "proxy off", flags: "ProxyUseProxy=0\n", wantMode: "none"},
	} {
		t.Run(test.name, func(t *testing.T) {
			encryptedMarkerOnlyCase(t, test.flags, test.wantMode)
		})
	}
	for _, test := range []struct {
		name     string
		flags    string
		wantMode string
	}{
		{name: "disabled proxy", flags: "ProxyUseBrowser=1\nProxyUseProxy=1\nProxyUseAutoConfigScript=1\nProxyAutoConfigScriptURL=http://autoproxy.example.test/autoproxy.cgi\nProxyUseProxyServer=0\n", wantMode: "none"},
		{name: "PAC proxy", flags: "ProxyUseBrowser=0\nProxyUseProxy=1\nProxyUseProxyServer=0\nProxyUseAutoConfigScript=1\nProxyAutoConfigScriptURL=https://proxy.example.test/config.pac\n", wantMode: "pac"},
		{name: "system proxy", flags: "ProxyUseBrowser=1\nProxyUseProxy=1\nProxyUseProxyServer=0\nProxyUseAutoConfigScript=0\n", wantMode: "system"},
		{name: "custom proxy", flags: "ProxyUseProxy=1\nProxyUseProxyServer=1\nProxyHTTPHost=proxy.example.test\nProxyHTTPPort=8080\n", wantMode: "custom"},
		{name: "proxy off", flags: "ProxyUseProxy=0\n", wantMode: "none"},
	} {
		// VuGen's default ProxyPasswordIsEncrypted=false marker never asks
		// for a protected proxy password, in any proxy mode.
		t.Run("false marker "+test.name, func(t *testing.T) {
			archive := buildSourceZip(t, map[string]string{
				"MarkerProxy.usr": "[General]\nType=Web\nRuntimeSettings=default.cfg\n[Actions]\nAction1=Action.c\n",
				"Action.c":        "Action()\n{\n\tweb_url(\"request\", \"URL=https://example.test/orders\", LAST);\n\treturn 0;\n}\n",
				"default.cfg":     "[WEB]\n" + test.flags + "ProxyPasswordIsEncrypted=false\n",
			})
			result, err := ConvertZip(archive, ConvertOptions{})
			if err != nil {
				t.Fatalf("ConvertZip: %v", err)
			}
			if mode := result.Script.Config.HTTP.ProxyMode; mode != test.wantMode {
				t.Fatalf("proxy mode = %q, want %q", mode, test.wantMode)
			}
			for _, action := range result.Script.Init {
				if action.Name == "Restore proxy credential" {
					t.Fatalf("false marker produced a callout: %#v", action)
				}
			}
			row := encryptedProxyMarkerDisposition(t, result.Script, "WEB")
			if row.Status == RuntimeSettingDispositionMapped || strings.Contains(row.Note, "provision") ||
				row.Note != "Credential encoding marker only: the source proxy password is not encrypted, so no protected proxy secret is needed." {
				t.Fatalf("false marker row = %#v, want the non-applicable not-encrypted note", row)
			}
		})
	}
	// A false marker beside a plaintext custom proxy password must not claim
	// the source has no proxy password. The plaintext password is retained
	// in the draft (credential policy Preserve), so no protected secret is
	// needed for it.
	archive := buildSourceZip(t, map[string]string{
		"MarkerProxy.usr": "[General]\nType=Web\nRuntimeSettings=default.cfg\n[Actions]\nAction1=Action.c\n",
		"Action.c":        "Action()\n{\n\tweb_url(\"request\", \"URL=https://example.test/orders\", LAST);\n\treturn 0;\n}\n",
		"default.cfg":     "[WEB]\nProxyUseProxy=1\nProxyUseProxyServer=1\nProxyHTTPHost=proxy.example.test\nProxyHTTPPort=8080\nProxyUserName=proxy-user\nProxyPassword=plain-proxy-password\nProxyPasswordIsEncrypted=false\n",
	})
	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	if password := result.Script.Config.HTTP.Proxy.Password; password != "plain-proxy-password" {
		t.Fatalf("proxy password = %q, want the plaintext source password retained", password)
	}
	if row := encryptedProxyMarkerDisposition(t, result.Script, "WEB"); row.Status == RuntimeSettingDispositionMapped ||
		strings.Contains(row.Note, "provision") || strings.Contains(row.Note, "has no proxy password") ||
		!strings.Contains(row.Note, "not encrypted") || row.Reason != "source_tool_metadata" {
		t.Fatalf("false marker beside a plaintext password row = %#v", row)
	}
}

// A disclosure-exported cfg (ProxyPassword=[REDACTED]) with a false marker
// under Custom imports an unbound ENTERPRISE_PROXY_PASSWORD secret that the
// live required-secret check blocks on. The marker row must point at that
// secret, not claim that no proxy secret is needed.
func TestConvertZipFalseProxyMarkerBesideRedactedPasswordPointsAtSecret(t *testing.T) {
	result := falseMarkerProxyPasswordCase(t, "ProxyUseProxy=1\nProxyUseProxyServer=1\nProxyHTTPHost=proxy.example.test\nProxyHTTPPort=8080\n", "[REDACTED]", "custom")
	row := encryptedProxyMarkerDisposition(t, result.Script, "WEB")
	const want = "Credential encoding marker consumed by the importer. The source proxy password could not be imported; bind the secret ENTERPRISE_PROXY_PASSWORD, or change Proxy mode to No proxy, System or PAC."
	if row.Status != RuntimeSettingDispositionMapped || row.ParityID != "proxy" || row.Reason != "" ||
		!reflect.DeepEqual(row.Fields, []string{"config.http.proxy.password"}) || row.Note != want {
		t.Fatalf("custom false-marker redacted-password row = %#v, want mapped to config.http.proxy.password with note %q", row, want)
	}
	if strings.Contains(row.Note, "no protected proxy secret is needed") {
		t.Fatalf("row contradicts the custom required-secret blocker: %#v", row)
	}

	// System keeps the reference without sending it.
	result = falseMarkerProxyPasswordCase(t, "ProxyUseBrowser=1\nProxyUseProxy=1\nProxyUseProxyServer=0\nProxyUseAutoConfigScript=0\n", "[REDACTED]", "system")
	row = encryptedProxyMarkerDisposition(t, result.Script, "WEB")
	const wantSystem = "Credential encoding marker consumed by the importer. The source proxy password is kept as the secret ENTERPRISE_PROXY_PASSWORD, which Proxy mode System does not send; it is needed only if you switch Proxy mode to Custom."
	if row.Status != RuntimeSettingDispositionMapped || !reflect.DeepEqual(row.Fields, []string{"config.http.proxy.password"}) || row.Note != wantSystem {
		t.Fatalf("system false-marker redacted-password row = %#v, want note %q", row, wantSystem)
	}
}

// falseMarkerProxyPasswordCase imports a [WEB] proxy under flags with a
// ProxyPasswordIsEncrypted=false marker beside password, and checks that the
// draft keeps the password as the ENTERPRISE_PROXY_PASSWORD reference.
func falseMarkerProxyPasswordCase(t *testing.T, flags, password, wantMode string) *ConvertResult {
	t.Helper()
	archive := buildSourceZip(t, map[string]string{
		"MarkerProxy.usr": "[General]\nType=Web\nRuntimeSettings=default.cfg\n[Actions]\nAction1=Action.c\n",
		"Action.c":        "Action()\n{\n\tweb_url(\"request\", \"URL=https://example.test/orders\", LAST);\n\treturn 0;\n}\n",
		"default.cfg":     "[WEB]\n" + flags + "ProxyUserName=proxy-user\nProxyPassword=" + password + "\nProxyPasswordIsEncrypted=false\n",
	})
	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	httpConfig := result.Script.Config.HTTP
	if httpConfig.ProxyMode != wantMode || httpConfig.Proxy.Password != "{{secret.ENTERPRISE_PROXY_PASSWORD}}" {
		t.Fatalf("proxy = %#v, want mode %q with the ENTERPRISE_PROXY_PASSWORD reference", httpConfig, wantMode)
	}
	return result
}

// encryptedMarkerOnlyCase imports a ProxyPasswordIsEncrypted=true marker
// with no proxy password under flags.
func encryptedMarkerOnlyCase(t *testing.T, flags, wantMode string) {
	t.Helper()
	archive := buildSourceZip(t, map[string]string{
		"MarkerProxy.usr": "[General]\nType=Web\nRuntimeSettings=default.cfg\n[Actions]\nAction1=Action.c\n",
		"Action.c":        "Action()\n{\n\tweb_url(\"request\", \"URL=https://example.test/orders\", LAST);\n\treturn 0;\n}\n",
		"default.cfg":     "[WEB]\n" + flags + "ProxyPasswordIsEncrypted=true\n",
	})
	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	httpConfig := result.Script.Config.HTTP
	if httpConfig.ProxyMode != wantMode || httpConfig.Proxy.Password != "" {
		t.Fatalf("proxy = %#v, want mode %q without a password", httpConfig, wantMode)
	}
	for _, action := range result.Script.Init {
		if action.Name == "Restore proxy credential" {
			t.Fatalf("marker without a password gates execution: %#v", action)
		}
	}
	row := encryptedProxyMarkerDisposition(t, result.Script, "WEB")
	if strings.Contains(row.Note, "not imported") {
		t.Fatalf("marker-only row claims a password was dropped: %#v", row)
	}
	if wantMode == "none" {
		if row.Status == RuntimeSettingDispositionMapped || !strings.Contains(row.Note, "marker only") {
			t.Fatalf("disabled marker-only row = %#v, want the non-applicable marker note", row)
		}
	} else if strings.Contains(row.Note, "does not use a proxy") {
		t.Fatalf("active %s proxy described as unused: %#v", wantMode, row)
	} else if strings.Contains(row.Note, "provision") || !strings.Contains(row.Note, "marker only") {
		t.Fatalf("active %s marker-only row = %#v, want the marker-only note without a provision request", wantMode, row)
	}
}

// encryptedProxyMarkerDisposition reads the default.cfg ProxyPasswordIsEncrypted
// row in section from the script's preserved dispositions.
func encryptedProxyMarkerDisposition(t *testing.T, script *dsl.Script, section string) RuntimeSettingDisposition {
	t.Helper()
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		t.Fatal("script has no import metadata")
	}
	payload, err := json.Marshal(script.Metadata.Import.Preserved["runtime_setting_dispositions"])
	if err != nil {
		t.Fatalf("marshal dispositions: %v", err)
	}
	var dispositions map[string]map[string]map[string]RuntimeSettingDisposition
	if err := json.Unmarshal(payload, &dispositions); err != nil {
		t.Fatalf("decode dispositions %s: %v", payload, err)
	}
	row, ok := dispositions["default.cfg"][section]["ProxyPasswordIsEncrypted"]
	if !ok {
		t.Fatalf("no ProxyPasswordIsEncrypted disposition: %s", payload)
	}
	// The row is rewritten after the explain pass, so it must carry its own
	// reason like every other inactive row.
	if row.Status == RuntimeSettingDispositionNonApplicable && row.Reason == "" {
		t.Fatalf("non-applicable ProxyPasswordIsEncrypted row has no reason: %#v", row)
	}
	return row
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
