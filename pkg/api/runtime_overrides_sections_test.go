package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// Phase 2: full HTTP / WebRuntime / BrowserConfig override sections (section-replace),
// the field-to-protocol applicability matrix, and HTTP-secret masking.

func TestApplyRuntimeOverridesHTTPSectionWholeReplace(t *testing.T) {
	script := &dsl.Script{}
	script.Config.HTTP = dsl.HTTPConfig{Version: "1.1", RequestTimeoutMs: 1000}
	o := &RuntimeSettingsOverride{
		HTTP: &dsl.HTTPConfig{
			Version:          "2",
			RequestTimeoutMs: 5000,
			Auth:             dsl.AuthConfig{Type: "bearer", Token: "abc"},
			Proxy:            dsl.ProxyConfig{URL: "http://proxy:8080", NoProxy: []string{"localhost"}},
			DefaultHeaders:   map[string]string{"X-Env": "prod"},
		},
	}
	if err := applyRuntimeOverrides(script, o); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := script.Config.HTTP
	if got.Version != "2" || got.RequestTimeoutMs != 5000 || got.Auth.Token != "abc" || got.DefaultHeaders["X-Env"] != "prod" {
		t.Fatalf("http section not applied as whole replace: %#v", got)
	}
}

func TestApplyRuntimeOverridesWebRuntimeSection(t *testing.T) {
	script := &dsl.Script{}
	o := &RuntimeSettingsOverride{
		WebRuntime: &dsl.WebRuntimeConfig{SourceIPs: []string{"10.0.0.1", "10.0.0.2"}},
	}
	if err := applyRuntimeOverrides(script, o); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(script.Config.WebRuntime.SourceIPs) != 2 {
		t.Fatalf("web_runtime section not applied: %#v", script.Config.WebRuntime)
	}
}

func TestApplyRuntimeOverridesBrowserConfigWinsOverLegacy(t *testing.T) {
	script := &dsl.Script{}
	script.Config.Browser = dsl.BrowserConfig{Engine: "chromium", DeviceProfile: "desktop_1080p"}
	headless := true
	o := &RuntimeSettingsOverride{
		BrowserConfig: &dsl.BrowserConfig{DeviceProfile: "pixel_8"},
		Browser:       &RuntimeBrowserOverride{DeviceProfile: "galaxy_s24", Headless: &headless},
	}
	if err := applyRuntimeOverrides(script, o); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Full BrowserConfig wins and replaces the whole section (Engine cleared, legacy ignored).
	if script.Config.Browser.DeviceProfile != "pixel_8" {
		t.Fatalf("browser_config should win, got device=%q", script.Config.Browser.DeviceProfile)
	}
	if script.Config.Browser.Engine != "" {
		t.Fatalf("browser_config is a whole-section replace; engine should be cleared, got %q", script.Config.Browser.Engine)
	}
}

func TestMergeRuntimeSettingsReplacesHTTPAndWebRuntime(t *testing.T) {
	base := &RuntimeSettingsOverride{
		HTTP:       &dsl.HTTPConfig{Version: "1.1"},
		WebRuntime: &dsl.WebRuntimeConfig{SourceIPs: []string{"1.1.1.1"}},
	}
	group := &RuntimeSettingsOverride{
		HTTP:       &dsl.HTTPConfig{Version: "2", DefaultHeaders: map[string]string{"A": "b"}},
		WebRuntime: &dsl.WebRuntimeConfig{SourceIPs: []string{"2.2.2.2", "3.3.3.3"}},
	}
	merged := mergeRuntimeSettings(base, group)
	if merged.HTTP.Version != "2" || merged.HTTP.DefaultHeaders["A"] != "b" {
		t.Fatalf("merged http not group's: %#v", merged.HTTP)
	}
	if len(merged.WebRuntime.SourceIPs) != 2 {
		t.Fatalf("merged web_runtime not group's: %#v", merged.WebRuntime)
	}
	if base.HTTP.Version != "1.1" || len(base.WebRuntime.SourceIPs) != 1 {
		t.Fatalf("merge mutated base: %#v %#v", base.HTTP, base.WebRuntime)
	}
}

func TestCloneRuntimeSettingsDeepClonesHTTPMapsAndSlices(t *testing.T) {
	o := &RuntimeSettingsOverride{
		HTTP: &dsl.HTTPConfig{
			DefaultHeaders: map[string]string{"X": "1"},
			Proxy:          dsl.ProxyConfig{NoProxy: []string{"a"}},
		},
	}
	clone := cloneRuntimeSettings(o)
	clone.HTTP.DefaultHeaders["X"] = "MUT"
	clone.HTTP.Proxy.NoProxy[0] = "MUT"
	if o.HTTP.DefaultHeaders["X"] != "1" || o.HTTP.Proxy.NoProxy[0] != "a" {
		t.Fatalf("clone aliases the original HTTP map/slice: %#v", o.HTTP)
	}
}

func TestFieldApplicableForProtocolMatrix(t *testing.T) {
	cases := []struct {
		field, protocol string
		want            bool
	}{
		{"http", "http", true},
		{"http", "soap", true},
		{"http", "browser", false},
		{"http", "grpc", false},
		{"browser_config", "browser", true},
		{"browser_config", "http", false},
		{"web_runtime", "http", true},
		{"web_runtime", "browser", true},
		{"web_runtime", "grpc", false},
		{"pacing", "grpc", true},     // universal
		{"run_logic", "kafka", true}, // universal
	}
	for _, c := range cases {
		if got := fieldApplicableForProtocol(c.field, c.protocol); got != c.want {
			t.Errorf("fieldApplicableForProtocol(%q,%q)=%v want %v", c.field, c.protocol, got, c.want)
		}
	}
}

func TestMaskRuntimeSettingsRedactsHTTPSecrets(t *testing.T) {
	o := &RuntimeSettingsOverride{
		HTTP: &dsl.HTTPConfig{
			Auth:  dsl.AuthConfig{Type: "basic", Username: "u", Password: "pw", Token: "tok"},
			Proxy: dsl.ProxyConfig{URL: "http://p:8080", Password: "ppw"},
		},
	}
	masked := maskRuntimeSettings(o)
	if masked.HTTP.Auth.Password != scrub.Redacted || masked.HTTP.Auth.Token != scrub.Redacted || masked.HTTP.Proxy.Password != scrub.Redacted {
		t.Fatalf("http secrets not redacted: %#v", masked.HTTP)
	}
	if masked.HTTP.Auth.Username != "u" || masked.HTTP.Proxy.URL != "http://p:8080" {
		t.Fatalf("non-secret http fields should survive: %#v", masked.HTTP)
	}
	if o.HTTP.Auth.Password != "pw" {
		t.Fatalf("masking mutated the input: %q", o.HTTP.Auth.Password)
	}
}

func TestReconcileGroupRuntimeSecretsHTTP(t *testing.T) {
	prior := &RuntimeSettingsOverride{HTTP: &dsl.HTTPConfig{Auth: dsl.AuthConfig{Password: "real", Token: "realtok"}, Proxy: dsl.ProxyConfig{Password: "realppw"}}}
	incoming := &RuntimeSettingsOverride{HTTP: &dsl.HTTPConfig{
		Auth:  dsl.AuthConfig{Password: scrub.Redacted, Token: "newtok"}, // echoed mask -> keep; new -> persist
		Proxy: dsl.ProxyConfig{Password: ""},                             // cleared
	}}
	result := reconcileGroupRuntimeSecrets(incoming, prior)
	if result.HTTP.Auth.Password != "real" {
		t.Fatalf("auth password should be restored from prior, got %q", result.HTTP.Auth.Password)
	}
	if result.HTTP.Auth.Token != "newtok" {
		t.Fatalf("auth token should persist new value, got %q", result.HTTP.Auth.Token)
	}
	if result.HTTP.Proxy.Password != "" {
		t.Fatalf("proxy password should be cleared, got %q", result.HTTP.Proxy.Password)
	}
}

func TestMaskManagedTestRedactsHTTPSecrets(t *testing.T) {
	test := &ManagedTest{
		RuntimeSettings: &RuntimeSettingsOverride{HTTP: &dsl.HTTPConfig{Auth: dsl.AuthConfig{Password: "pw"}}},
		Groups: []TestGroup{
			{RuntimeSettings: &RuntimeSettingsOverride{HTTP: &dsl.HTTPConfig{Auth: dsl.AuthConfig{Token: "tok"}}}},
		},
	}
	masked := maskManagedTest(test)
	if masked.RuntimeSettings.HTTP.Auth.Password != scrub.Redacted {
		t.Fatalf("root http password not masked: %q", masked.RuntimeSettings.HTTP.Auth.Password)
	}
	if masked.Groups[0].RuntimeSettings.HTTP.Auth.Token != scrub.Redacted {
		t.Fatalf("group http token not masked: %q", masked.Groups[0].RuntimeSettings.HTTP.Auth.Token)
	}
	if test.RuntimeSettings.HTTP.Auth.Password != "pw" {
		t.Fatalf("masking mutated the input test")
	}
}
