package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func proxySecretScript(mode, proxyURL string) *dsl.Script {
	return &dsl.Script{
		Name:     "proxy",
		Protocol: "http",
		Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{
			ProxyMode: mode,
			Proxy: dsl.ProxyConfig{
				URL:      proxyURL,
				Username: "{{secret.PROXY_USER}}",
				Password: "{{secret.ENTERPRISE_PROXY_PASSWORD}}",
			},
		}},
		Actions: []dsl.Action{{Name: "request", Type: "GET", Target: "https://example.test/{{secret.API_TOKEN}}"}},
	}
}

func TestRequiredSecretNamesDropsOnlyInactiveProxyCredentials(t *testing.T) {
	all := []string{"API_TOKEN", "ENTERPRISE_PROXY_PASSWORD", "PROXY_USER"}
	for _, test := range []struct {
		name     string
		mode     string
		url      string
		required []string
	}{
		{name: "no proxy", mode: "none", required: []string{"API_TOKEN"}},
		{name: "system", mode: "system", url: "http://proxy.example.test:8080", required: []string{"API_TOKEN"}},
		{name: "pac", mode: "pac", required: []string{"API_TOKEN"}},
		{name: "empty mode without url", mode: "", required: []string{"API_TOKEN"}},
		{name: "custom", mode: "custom", url: "http://proxy.example.test:8080", required: all},
		{name: "legacy empty mode with url", mode: "", url: "http://proxy.example.test:8080", required: all},
	} {
		t.Run(test.name, func(t *testing.T) {
			script := proxySecretScript(test.mode, test.url)
			referenced, err := ReferencedSecretNames(script)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(referenced, all) {
				t.Fatalf("ReferencedSecretNames = %v, want the full list %v", referenced, all)
			}
			required, err := RequiredSecretNames(script)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(required, test.required) {
				t.Fatalf("RequiredSecretNames = %v, want %v", required, test.required)
			}
		})
	}
}

func TestRequiredSecretNamesKeepsInactiveProxySecretUsedElsewhere(t *testing.T) {
	script := proxySecretScript("system", "")
	script.Actions[0].Headers = map[string]string{"X-Proxy-Password": "{{secret.ENTERPRISE_PROXY_PASSWORD}}"}
	required, err := RequiredSecretNames(script)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"API_TOKEN", "ENTERPRISE_PROXY_PASSWORD"}; !reflect.DeepEqual(required, want) {
		t.Fatalf("RequiredSecretNames = %v, want %v", required, want)
	}
	// A name also used outside the proxy keeps the generic wording.
	proxyMessages, others := MissingSecretProxyBlockers(proxySecretScript("custom", "http://proxy.example.test:8080"), []string{"ENTERPRISE_PROXY_PASSWORD"})
	if len(proxyMessages) != 1 || len(others) != 0 {
		t.Fatalf("custom proxy-only name: proxy %v others %v", proxyMessages, others)
	}
	custom := proxySecretScript("custom", "http://proxy.example.test:8080")
	custom.Actions[0].Headers = script.Actions[0].Headers
	proxyMessages, others = MissingSecretProxyBlockers(custom, []string{"ENTERPRISE_PROXY_PASSWORD"})
	if len(proxyMessages) != 0 || !reflect.DeepEqual(others, []string{"ENTERPRISE_PROXY_PASSWORD"}) {
		t.Fatalf("name also used elsewhere: proxy %v others %v, want the generic wording", proxyMessages, others)
	}
}

func TestRequiredSecretNamesBrowserProxyFollowsServer(t *testing.T) {
	script := &dsl.Script{Protocol: "browser", Config: dsl.ScriptConfig{Browser: dsl.BrowserConfig{Proxy: dsl.BrowserProxyConfig{
		Username: "{{secret.BROWSER_PROXY_USER}}",
		Password: "{{secret.BROWSER_PROXY_PASSWORD}}",
	}}}}
	required, err := RequiredSecretNames(script)
	if err != nil {
		t.Fatal(err)
	}
	if len(required) != 0 {
		t.Fatalf("browser proxy without a server: RequiredSecretNames = %v, want none", required)
	}
	script.Config.Browser.Proxy.Server = "http://proxy.example.test:3128"
	required, err = RequiredSecretNames(script)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"BROWSER_PROXY_PASSWORD", "BROWSER_PROXY_USER"}; !reflect.DeepEqual(required, want) {
		t.Fatalf("browser proxy with a server: RequiredSecretNames = %v, want %v", required, want)
	}
	err = MissingSecretsError(script, "required project secret(s) are not configured: ", []string{"BROWSER_PROXY_PASSWORD"})
	want := "Browser proxy credential not bound: The browser proxy password uses the secret BROWSER_PROXY_PASSWORD, which is not bound to a project secret. Bind a project secret to it, or clear the browser proxy server."
	if err == nil || err.Error() != want {
		t.Fatalf("MissingSecretsError = %v, want %q", err, want)
	}
}

func TestSnapshotSecretsIgnoresUnboundInactiveProxyCredentials(t *testing.T) {
	resolver := SecretResolverFunc(func(name string) (string, bool) {
		if name == "API_TOKEN" {
			return "api-token-value", true
		}
		return "", false
	})
	for _, mode := range []string{"none", "system", "pac"} {
		if _, _, err := snapshotReferencedSecrets(proxySecretScript(mode, "http://proxy.example.test:8080"), resolver); err != nil {
			t.Fatalf("%s proxy with unbound proxy secrets: snapshot error = %v", mode, err)
		}
	}
	_, _, err := snapshotReferencedSecrets(proxySecretScript("custom", "http://proxy.example.test:8080"), resolver)
	if err == nil {
		t.Fatal("custom proxy with unbound proxy secrets: snapshot succeeded")
	}
	for _, want := range []string{
		"Custom proxy credential not bound: The proxy password uses the secret ENTERPRISE_PROXY_PASSWORD, which is not bound to a project secret. Bind a project secret to it, or change Proxy mode to No proxy, System or PAC.",
		"Custom proxy credential not bound: The proxy username uses the secret PROXY_USER, which is not bound to a project secret.",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("custom proxy snapshot error = %q, want it to contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "required project secret(s) are not configured") {
		t.Fatalf("proxy-only names also got the generic wording: %q", err)
	}
	bound := SecretResolverFunc(func(name string) (string, bool) { return name + "-value", true })
	if _, _, err := snapshotReferencedSecrets(proxySecretScript("custom", "http://proxy.example.test:8080"), bound); err != nil {
		t.Fatalf("custom proxy with bound proxy secrets: snapshot error = %v", err)
	}
}

func TestProtocolConfigOmitsInactiveProxyCredentials(t *testing.T) {
	for _, test := range []struct {
		mode   string
		active bool
	}{{"none", false}, {"system", false}, {"pac", false}, {"custom", true}} {
		cfg := buildProtoConfig(proxySecretScript(test.mode, "http://proxy.example.test:8080"))
		_, hasUser := cfg["proxy_username"]
		_, hasPassword := cfg["proxy_password"]
		if hasUser != test.active || hasPassword != test.active {
			t.Fatalf("%s proxy config carries username %v password %v, want %v", test.mode, hasUser, hasPassword, test.active)
		}
	}
	browser := &dsl.Script{Protocol: "browser", Config: dsl.ScriptConfig{Browser: dsl.BrowserConfig{Proxy: dsl.BrowserProxyConfig{
		Username: "browser-user",
		Password: "{{secret.BROWSER_PROXY_PASSWORD}}",
	}}}}
	cfg := buildProtoConfig(browser)
	if _, ok := cfg["browser_proxy_password"]; ok {
		t.Fatal("browser proxy without a server still hands its password to the runtime")
	}
	browser.Config.Browser.Proxy.Server = "http://proxy.example.test:3128"
	cfg = buildProtoConfig(browser)
	if cfg["browser_proxy_username"] != "browser-user" {
		t.Fatalf("browser proxy with a server lost its username: %#v", cfg["browser_proxy_username"])
	}
}

func TestReplayRunsWithUnboundSecretBehindSystemProxy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	script := &dsl.Script{
		Name:     "system proxy",
		Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: server.URL, HTTP: dsl.HTTPConfig{
			ProxyMode: "none",
			Proxy:     dsl.ProxyConfig{Password: "{{secret.ENTERPRISE_PROXY_PASSWORD}}"},
		}},
		Actions: []dsl.Action{{Name: "request", Type: "GET", Target: "/"}},
	}
	report, err := ReplayScript(context.Background(), script, ReplayOptions{
		SecretResolver: SecretResolverFunc(func(string) (string, bool) { return "", false }),
	})
	if err != nil {
		t.Fatalf("replay with an unbound secret behind No proxy: %v", err)
	}
	if report == nil {
		t.Fatal("replay returned no report")
	}
}

func TestImportExecutionGateSkipsAdvisoryEncryptedProxyPassword(t *testing.T) {
	legacy := &dsl.Script{
		Name:     "legacy",
		Protocol: "http",
		Init: []dsl.Action{{
			Name: "Restore proxy credential", Type: "source_review",
			Metadata: map[string]any{
				"execution_blocker":  "encrypted_proxy_password",
				"execution_blockers": []any{"encrypted_proxy_password"},
			},
		}},
	}
	if err := importExecutionGateError(legacy, "replay"); err != nil {
		t.Fatalf("legacy encrypted_proxy_password metadata still blocks: %v", err)
	}
	legacy.Init[0].Metadata["execution_blockers"] = []any{"encrypted_proxy_password", "legacy_tls_replacement"}
	if err := importExecutionGateError(legacy, "replay"); err == nil ||
		!strings.Contains(err.Error(), "legacy_tls_replacement") ||
		strings.Contains(err.Error(), "encrypted_proxy_password") {
		t.Fatalf("mixed blockers error = %v, want only legacy_tls_replacement", err)
	}
}

// A browser-protocol script whose proxy credentials name unbound secrets but
// set no server must pass every step a run takes: DSL validation, the
// required-secret snapshot, and the protocol config handed to the browser.
func TestBrowserProxyCredentialsWithoutServerDoNotBlockRun(t *testing.T) {
	script := &dsl.Script{
		Name:     "browser-proxy-inactive",
		Protocol: "browser",
		Config: dsl.ScriptConfig{Browser: dsl.BrowserConfig{Proxy: dsl.BrowserProxyConfig{
			Username: "{{secret.BROWSER_PROXY_USER}}",
			Password: "{{secret.BROWSER_PROXY_PASSWORD}}",
		}}},
		Actions: []dsl.Action{{Name: "open", Type: "browser_navigate", Target: "https://example.test/"}},
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("ValidateScriptIssues = %v, want none", issues)
	}
	unbound := SecretResolverFunc(func(string) (string, bool) { return "", false })
	if _, _, err := snapshotReferencedSecrets(script, unbound); err != nil {
		t.Fatalf("snapshot with unbound inactive browser proxy secrets: %v", err)
	}
	cfg := buildProtoConfigWithSecretResolver(script, unbound)
	for _, key := range []string{"browser_proxy_server", "browser_proxy_username", "browser_proxy_password"} {
		if _, ok := cfg[key]; ok {
			t.Fatalf("protocol config carries %s = %v for a proxy with no server", key, cfg[key])
		}
	}
}
