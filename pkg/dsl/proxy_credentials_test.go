package dsl

import (
	"reflect"
	"strings"
	"testing"
)

func TestHTTPProxyCredentialsActiveMatchesRuntimeProxyModes(t *testing.T) {
	for _, test := range []struct {
		mode   string
		url    string
		active bool
		label  string
	}{
		{mode: "custom", url: "http://proxy.example.test:8080", active: true, label: "Custom"},
		{mode: " custom ", active: true, label: "Custom"},
		{mode: "", url: "http://proxy.example.test:8080", active: true, label: "Custom"},
		{mode: "", url: "  ", active: false, label: "No proxy"},
		{mode: "none", url: "http://proxy.example.test:8080", active: false, label: "No proxy"},
		{mode: "system", url: "http://proxy.example.test:8080", active: false, label: "System"},
		{mode: "pac", active: false, label: "PAC"},
	} {
		cfg := HTTPConfig{ProxyMode: test.mode, Proxy: ProxyConfig{URL: test.url}}
		if got := HTTPProxyCredentialsActive(cfg); got != test.active {
			t.Errorf("HTTPProxyCredentialsActive(mode %q, url %q) = %v, want %v", test.mode, test.url, got, test.active)
		}
		if got := HTTPProxyModeLabel(cfg); got != test.label {
			t.Errorf("HTTPProxyModeLabel(mode %q, url %q) = %q, want %q", test.mode, test.url, got, test.label)
		}
	}
	if BrowserProxyCredentialsActive(BrowserProxyConfig{Server: " "}) {
		t.Error("browser proxy without a server reported active credentials")
	}
	if !BrowserProxyCredentialsActive(BrowserProxyConfig{Server: "http://proxy.example.test:3128"}) {
		t.Error("browser proxy with a server reported inactive credentials")
	}
}

func TestInactiveProxyCredentialPaths(t *testing.T) {
	script := &Script{Config: ScriptConfig{
		HTTP:    HTTPConfig{ProxyMode: "system"},
		Browser: BrowserConfig{Proxy: BrowserProxyConfig{Server: "http://proxy.example.test:3128"}},
	}}
	if got, want := InactiveProxyCredentialPaths(script), []string{HTTPProxyUsernamePath, HTTPProxyPasswordPath}; !reflect.DeepEqual(got, want) {
		t.Fatalf("InactiveProxyCredentialPaths(system, browser server) = %v, want %v", got, want)
	}
	script.Config.HTTP.ProxyMode = "custom"
	script.Config.Browser.Proxy.Server = ""
	if got, want := InactiveProxyCredentialPaths(script), []string{BrowserProxyUsernamePath, BrowserProxyPasswordPath}; !reflect.DeepEqual(got, want) {
		t.Fatalf("InactiveProxyCredentialPaths(custom, no browser server) = %v, want %v", got, want)
	}
}

func TestProxyCredentialSecretReferenceWording(t *testing.T) {
	script := &Script{Config: ScriptConfig{
		HTTP: HTTPConfig{ProxyMode: "custom", Proxy: ProxyConfig{
			URL:      "http://proxy.example.test:8080",
			Username: "{{ secret.PROXY_USER }}",
			Password: "{{secret.ENTERPRISE_PROXY_PASSWORD}}",
		}},
		Browser: BrowserConfig{Proxy: BrowserProxyConfig{Password: "{{secret.BROWSER_PROXY_PASSWORD}}"}},
	}}
	refs := ProxyCredentialSecretReferences(script)
	if len(refs) != 3 {
		t.Fatalf("ProxyCredentialSecretReferences = %#v, want 3 references", refs)
	}
	password := refs[1]
	if password.Name != "ENTERPRISE_PROXY_PASSWORD" || password.Path != HTTPProxyPasswordPath || !password.Active {
		t.Fatalf("http password reference = %#v", password)
	}
	if got, want := password.BlockerTitle(), "Custom proxy credential not bound"; got != want {
		t.Fatalf("BlockerTitle = %q, want %q", got, want)
	}
	if got, want := password.BlockerDetail(), "The proxy password uses the secret ENTERPRISE_PROXY_PASSWORD, which is not bound to a project secret. Bind a project secret to it, or change Proxy mode to No proxy, System or PAC."; got != want {
		t.Fatalf("BlockerDetail = %q, want %q", got, want)
	}
	if got, want := refs[0].BlockerDetail(), "The proxy username uses the secret PROXY_USER, which is not bound to a project secret. Bind a project secret to it, or change Proxy mode to No proxy, System or PAC."; got != want {
		t.Fatalf("username BlockerDetail = %q, want %q", got, want)
	}
	browser := refs[2]
	if browser.Active || !browser.Browser {
		t.Fatalf("browser reference = %#v, want inactive browser reference", browser)
	}
	if got, want := browser.BlockerTitle(), "Browser proxy credential not bound"; got != want {
		t.Fatalf("browser BlockerTitle = %q, want %q", got, want)
	}
	if got, want := browser.BlockerDetail(), "The browser proxy password uses the secret BROWSER_PROXY_PASSWORD, which is not bound to a project secret. Bind a project secret to it, or clear the browser proxy server."; got != want {
		t.Fatalf("browser BlockerDetail = %q, want %q", got, want)
	}

	script.Config.HTTP.ProxyMode = "pac"
	refs = ProxyCredentialSecretReferences(script)
	if refs[1].Active {
		t.Fatalf("PAC proxy password reference = %#v, want inactive", refs[1])
	}
	if got, want := refs[1].InactiveNote(), "The proxy password uses the secret ENTERPRISE_PROXY_PASSWORD, but Proxy mode is PAC, so it is not needed. It becomes required if you switch Proxy mode to Custom."; got != want {
		t.Fatalf("InactiveNote = %q, want %q", got, want)
	}
	script.Config.HTTP.ProxyMode = "none"
	if got, want := ProxyCredentialSecretReferences(script)[1].InactiveNote(), "The proxy password uses the secret ENTERPRISE_PROXY_PASSWORD, but Proxy mode is No proxy, so it is not needed. It becomes required if you switch Proxy mode to Custom."; got != want {
		t.Fatalf("none InactiveNote = %q, want %q", got, want)
	}
}

func TestAdvisoryExecutionBlockerCode(t *testing.T) {
	if !AdvisoryExecutionBlockerCode(" encrypted_proxy_password ") {
		t.Fatal("encrypted_proxy_password is not advisory")
	}
	if AdvisoryExecutionBlockerCode("import_review_required") {
		t.Fatal("import_review_required became advisory")
	}
	if hasDurableExecutionBlocker(map[string]any{"execution_blocker": "encrypted_proxy_password", "execution_blockers": []any{"encrypted_proxy_password"}}) {
		t.Fatal("an advisory callout relaxes validation as a durable execution blocker")
	}
	if !hasDurableExecutionBlocker(map[string]any{"execution_blockers": []any{"encrypted_proxy_password", "legacy_tls_replacement"}}) {
		t.Fatal("a real blocker beside the advisory callout no longer counts")
	}
}

func TestMoveAdvisoryExecutionBlockers(t *testing.T) {
	for _, test := range []struct {
		name     string
		metadata map[string]any
		changed  bool
		want     map[string]any
	}{
		{
			name:     "legacy advisory only",
			metadata: map[string]any{"execution_blocker": "encrypted_proxy_password", "execution_blockers": []any{"encrypted_proxy_password"}, "import_review": "kept"},
			changed:  true,
			want:     map[string]any{AdvisoryCalloutsMetadataKey: []string{"encrypted_proxy_password"}, "import_review": "kept"},
		},
		{
			name:     "advisory beside a real blocker",
			metadata: map[string]any{"execution_blocker": "encrypted_proxy_password", "execution_blockers": []string{"legacy_tls_replacement", "encrypted_proxy_password"}},
			changed:  true,
			want:     map[string]any{"execution_blocker": "legacy_tls_replacement", "execution_blockers": []string{"legacy_tls_replacement"}, AdvisoryCalloutsMetadataKey: []string{"encrypted_proxy_password"}},
		},
		{
			name:     "real single blocker kept as written",
			metadata: map[string]any{"execution_blocker": "restored_body_required", "execution_blockers": []any{"encrypted_proxy_password", "restored_body_required"}, AdvisoryCalloutsMetadataKey: []any{"encrypted_proxy_password"}},
			changed:  true,
			want:     map[string]any{"execution_blocker": "restored_body_required", "execution_blockers": []string{"restored_body_required"}, AdvisoryCalloutsMetadataKey: []string{"encrypted_proxy_password"}},
		},
		{
			name:     "no advisory code",
			metadata: map[string]any{"execution_blocker": "legacy_tls_replacement"},
			want:     map[string]any{"execution_blocker": "legacy_tls_replacement"},
		},
		{name: "nil metadata"},
	} {
		if changed := MoveAdvisoryExecutionBlockers(test.metadata); changed != test.changed {
			t.Fatalf("%s: changed = %t, want %t", test.name, changed, test.changed)
		}
		if test.want == nil && test.metadata == nil {
			continue
		}
		if !reflect.DeepEqual(test.metadata, test.want) {
			t.Fatalf("%s: metadata = %#v, want %#v", test.name, test.metadata, test.want)
		}
	}
}

// A browser proxy username/password with no server is inactive (the browser
// never sends it), so it must not fail script validation: otherwise the
// "not needed" note is contradicted by a blocker the user can only clear by
// deleting the credential.
func TestValidateScriptIssuesBrowserProxyCredentialsWithoutServerAreInactive(t *testing.T) {
	script := &Script{
		Name:     "browser-proxy-inactive",
		Protocol: "browser",
		Config: ScriptConfig{Browser: BrowserConfig{Proxy: BrowserProxyConfig{
			Username: "{{secret.BROWSER_PROXY_USER}}",
			Password: "{{secret.BROWSER_PROXY_PASSWORD}}",
		}}},
		Actions: []Action{{Name: "open", Type: "browser_navigate", Target: "https://example.test/"}},
	}
	for _, issue := range ValidateScriptIssues(script) {
		if strings.Contains(issue, "config.browser.proxy") {
			t.Fatalf("browser proxy credentials without a server: issue %q, want none", issue)
		}
	}
	script.Config.Browser.Proxy.Username = ""
	for _, issue := range ValidateScriptIssues(script) {
		if strings.Contains(issue, "config.browser.proxy") {
			t.Fatalf("browser proxy password without a server or username: issue %q, want none", issue)
		}
	}

	script.Config.Browser.Proxy.Bypass = "localhost"
	assertIssue(t, ValidateScriptIssues(script), "config.browser.proxy.server is required when proxy is set")

	script.Config.Browser.Proxy.Bypass = ""
	script.Config.Browser.Proxy.Server = "http://proxy.example.test:3128"
	assertIssue(t, ValidateScriptIssues(script), "config.browser.proxy.password requires username")
}
