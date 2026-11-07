package importreview

import (
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

func proxyPreflightScript(mode, proxyURL string) *dsl.Script {
	return &dsl.Script{
		Name: "proxy", Version: "1.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test", HTTP: dsl.HTTPConfig{
			ProxyMode: mode,
			Proxy:     dsl.ProxyConfig{URL: proxyURL, Password: "{{secret.ENTERPRISE_PROXY_PASSWORD}}"},
		}},
		Actions: []dsl.Action{{Name: "request", Type: "GET", Method: "GET", Target: "/"}},
	}
}

func proxySecretCheck(t *testing.T, preflight *ReplayPreflight) ReplayPreflightCheck {
	t.Helper()
	for _, check := range preflight.Checks {
		if check.ID == "secrets" {
			return check
		}
	}
	t.Fatal("preflight has no secrets check")
	return ReplayPreflightCheck{}
}

const customProxyBlocker = "Custom proxy credential not bound: The proxy password uses the secret ENTERPRISE_PROXY_PASSWORD, which is not bound to a project secret. Bind a project secret to it, or change Proxy mode to No proxy, System or PAC."

func TestReplayPreflightUnboundProxySecretBlocksOnlyForCustomProxy(t *testing.T) {
	unbound := engine.SecretResolverFunc(func(string) (string, bool) { return "", false })
	bound := engine.SecretResolverFunc(func(name string) (string, bool) { return name + "-value", true })

	for _, test := range []struct {
		mode, url, label string
	}{
		{mode: "none", label: "No proxy"},
		{mode: "system", url: "http://proxy.example.test:8080", label: "System"},
		{mode: "pac", label: "PAC"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			script := proxyPreflightScript(test.mode, test.url)
			if test.mode == "pac" {
				script.Config.HTTP.Proxy.PACURL = "https://proxy.example.test/config.pac"
			}
			preflight := BuildReplayPreflight(script, nil, nil, nil, unbound)
			if !preflight.Valid || len(preflight.RequiredSecrets) != 0 || len(preflight.MissingSecrets) != 0 {
				t.Fatalf("%s proxy with an unbound proxy secret: valid %v required %v missing %v", test.mode, preflight.Valid, preflight.RequiredSecrets, preflight.MissingSecrets)
			}
			note := "The proxy password uses the secret ENTERPRISE_PROXY_PASSWORD, but Proxy mode is " + test.label + ", so it is not needed. It becomes required if you switch Proxy mode to Custom."
			if !slices.Contains(preflight.Warnings, note) {
				t.Fatalf("warnings = %#v, want the non-blocking note %q", preflight.Warnings, note)
			}
			if check := proxySecretCheck(t, preflight); check.Status == "fail" {
				t.Fatalf("secrets check = %#v, want not failed", check)
			}
			// A bound inactive secret needs no note.
			if preflight := BuildReplayPreflight(script, nil, nil, nil, bound); slices.Contains(preflight.Warnings, note) {
				t.Fatalf("bound inactive proxy secret still noted: %#v", preflight.Warnings)
			}
		})
	}

	custom := proxyPreflightScript("custom", "http://proxy.example.test:8080")
	preflight := BuildReplayPreflight(custom, nil, nil, nil, unbound)
	if preflight.Valid || !slices.Equal(preflight.MissingSecrets, []string{"ENTERPRISE_PROXY_PASSWORD"}) {
		t.Fatalf("custom proxy with an unbound secret: valid %v missing %v", preflight.Valid, preflight.MissingSecrets)
	}
	if !slices.Contains(preflight.Warnings, customProxyBlocker) {
		t.Fatalf("custom proxy warnings = %#v, want %q", preflight.Warnings, customProxyBlocker)
	}
	for _, line := range preflight.Warnings {
		if strings.HasPrefix(line, ReplayMissingSecretsWarning) {
			t.Fatalf("proxy-only missing secret also got the generic line: %q", line)
		}
	}
	if check := proxySecretCheck(t, preflight); check.Detail != customProxyBlocker {
		t.Fatalf("secrets check detail = %q, want %q", check.Detail, customProxyBlocker)
	}

	if preflight := BuildReplayPreflight(custom, nil, nil, nil, bound); !preflight.Valid || len(preflight.MissingSecrets) != 0 {
		t.Fatalf("custom proxy with a bound secret: valid %v missing %v", preflight.Valid, preflight.MissingSecrets)
	}

	// An import preview (no resolver) stays saveable and words the proxy
	// requirement the same way.
	preview := BuildReplayPreflight(custom, nil, nil, nil)
	if !slices.Contains(preview.Warnings, customProxyBlocker) {
		t.Fatalf("import preview warnings = %#v, want %q", preview.Warnings, customProxyBlocker)
	}
}

func TestReplayPreflightBrowserProxySecretFollowsServer(t *testing.T) {
	unbound := engine.SecretResolverFunc(func(string) (string, bool) { return "", false })
	script := &dsl.Script{Name: "browser", Version: "1.0", Protocol: "browser", Config: dsl.ScriptConfig{
		BaseURL: "https://example.test",
		Browser: dsl.BrowserConfig{Proxy: dsl.BrowserProxyConfig{Password: "{{secret.BROWSER_PROXY_PASSWORD}}"}},
	}, Actions: []dsl.Action{{Name: "open", Type: "browser_navigate", Target: "https://example.test"}}}
	preflight := BuildReplayPreflight(script, nil, nil, nil, unbound)
	if len(preflight.MissingSecrets) != 0 {
		t.Fatalf("browser proxy without a server: missing %v", preflight.MissingSecrets)
	}
	script.Config.Browser.Proxy.Server = "http://proxy.example.test:3128"
	preflight = BuildReplayPreflight(script, nil, nil, nil, unbound)
	want := "Browser proxy credential not bound: The browser proxy password uses the secret BROWSER_PROXY_PASSWORD, which is not bound to a project secret. Bind a project secret to it, or clear the browser proxy server."
	if !slices.Contains(preflight.Warnings, want) || preflight.Valid {
		t.Fatalf("browser proxy with a server: valid %v warnings %#v, want %q", preflight.Valid, preflight.Warnings, want)
	}
}

func TestScriptReviewGateIssuesSkipLegacyEncryptedProxyPasswordBlocker(t *testing.T) {
	script := proxyPreflightScript("system", "")
	script.Init = []dsl.Action{{
		Name: "Restore proxy credential", Type: "source_review",
		Metadata: map[string]any{
			"execution_blocker":  "encrypted_proxy_password",
			"execution_blockers": []any{"encrypted_proxy_password"},
			"import_review":      map[string]any{"reason_codes": []any{"encrypted_proxy_password"}, "resolution": "replace_proxy_secret"},
		},
	}}
	if issues := ScriptReviewGateIssues(script, "replay"); len(issues) != 0 {
		t.Fatalf("legacy encrypted_proxy_password metadata blocks replay: %v", issues)
	}
	if issues := ScriptReviewGateIssues(script, "launch"); len(issues) != 0 {
		t.Fatalf("legacy encrypted_proxy_password metadata blocks launch: %v", issues)
	}
	script.Init[0].Metadata["execution_blockers"] = []any{"encrypted_proxy_password", "legacy_tls_replacement"}
	issues := ScriptReviewGateIssues(script, "replay")
	if len(issues) != 1 || !strings.Contains(issues[0], "legacy_tls_replacement") || strings.Contains(issues[0], "encrypted_proxy_password") {
		t.Fatalf("mixed blockers = %v, want only legacy_tls_replacement", issues)
	}
}

func TestReplaySecretCheckDetailNamesInactiveProxySecret(t *testing.T) {
	unbound := engine.SecretResolverFunc(func(string) (string, bool) { return "", false })
	const inactive = "No required project secrets. Not needed for the current Proxy mode: ENTERPRISE_PROXY_PASSWORD"
	script := proxyPreflightScript("system", "")
	for name, preflight := range map[string]*ReplayPreflight{
		"resolver":    BuildReplayPreflight(script, nil, nil, nil, unbound),
		"no resolver": BuildReplayPreflight(script, nil, nil, nil),
	} {
		check := proxySecretCheck(t, preflight)
		if check.Detail != inactive || check.Status == "fail" {
			t.Fatalf("%s: secrets check = %#v, want detail %q and not failed", name, check, inactive)
		}
	}

	// A required name keeps its wording, with the inactive proxy name after it.
	script.Actions[0].Headers = map[string]string{"Authorization": "{{secret.API_TOKEN}}"}
	check := proxySecretCheck(t, BuildReplayPreflight(script, nil, nil, nil, unbound))
	if want := "Missing: API_TOKEN; Not needed for the current Proxy mode: ENTERPRISE_PROXY_PASSWORD"; check.Detail != want {
		t.Fatalf("secrets check detail = %q, want %q", check.Detail, want)
	}

	// A name also used outside the inactive proxy field stays required and is
	// not called "not needed".
	script.Actions[0].Headers = map[string]string{"X-Proxy": "{{secret.ENTERPRISE_PROXY_PASSWORD}}"}
	check = proxySecretCheck(t, BuildReplayPreflight(script, nil, nil, nil, unbound))
	if want := "Missing: ENTERPRISE_PROXY_PASSWORD"; check.Detail != want {
		t.Fatalf("secrets check detail = %q, want %q", check.Detail, want)
	}

	browser := &dsl.Script{Name: "browser", Version: "1.0", Protocol: "browser", Config: dsl.ScriptConfig{
		BaseURL: "https://example.test",
		Browser: dsl.BrowserConfig{Proxy: dsl.BrowserProxyConfig{Password: "{{secret.BROWSER_PROXY_PASSWORD}}"}},
	}, Actions: []dsl.Action{{Name: "open", Type: "browser_navigate", Target: "https://example.test"}}}
	check = proxySecretCheck(t, BuildReplayPreflight(browser, nil, nil, nil, unbound))
	if want := "No required project secrets. Not needed without a browser proxy server: BROWSER_PROXY_PASSWORD"; check.Detail != want {
		t.Fatalf("browser secrets check detail = %q, want %q", check.Detail, want)
	}

	// A script with no secret references keeps the old wording.
	if got := ReplaySecretCheckDetail(proxyPreflightScript("system", ""), nil, nil); got == "No project secret references detected." {
		t.Fatalf("inactive proxy reference reported as no references")
	}
	plain := proxyPreflightScript("system", "")
	plain.Config.HTTP.Proxy.Password = ""
	if got := ReplaySecretCheckDetail(plain, nil, nil); got != "No project secret references detected." {
		t.Fatalf("no-reference detail = %q", got)
	}
}
