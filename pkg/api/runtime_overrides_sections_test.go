package api

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
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
		{"http", "browser", true}, // HTTP transport now hosts browser-emulation settings
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
		BaseURL: "https://base-user:base-pass@example.test",
		HTTP: &dsl.HTTPConfig{
			Auth:  dsl.AuthConfig{Type: "basic", Username: "u", Password: "pw", Token: "tok"},
			Proxy: dsl.ProxyConfig{URL: "http://url-user:url-pass@p:8080", Username: "puser", Password: "ppw"},
			DefaultHeaders: map[string]string{
				"Authorization": "Bearer secret", "Cookie": "session=secret", "X-API-Key": "secret", "Accept": "application/json",
			},
		},
		BrowserConfig: &dsl.BrowserConfig{
			HTTPCredentials: dsl.BrowserHTTPCredentials{Username: "browser-user", Password: "browser-password"},
			Proxy:           dsl.BrowserProxyConfig{Server: "http://url-user:url-pass@browser-proxy:8080", Username: "browser-proxy-user", Password: "browser-proxy-password"},
			ClientCertificates: []dsl.BrowserClientCertificate{{
				Origin: "https://example.test", PfxPath: "client.pfx", Passphrase: "certificate-passphrase",
			}},
		},
	}
	masked := maskRuntimeSettings(o)
	if masked.HTTP.Auth.Password != scrub.Redacted || masked.HTTP.Auth.Token != scrub.Redacted || masked.HTTP.Proxy.Username != scrub.Redacted || masked.HTTP.Proxy.Password != scrub.Redacted {
		t.Fatalf("http secrets not redacted: %#v", masked.HTTP)
	}
	if masked.HTTP.Auth.Username != "u" || masked.HTTP.DefaultHeaders["Accept"] != "application/json" {
		t.Fatalf("non-secret http fields should survive: %#v", masked.HTTP)
	}
	for _, header := range []string{"Authorization", "Cookie", "X-API-Key"} {
		if masked.HTTP.DefaultHeaders[header] != scrub.Redacted {
			t.Fatalf("sensitive header %s was not redacted: %#v", header, masked.HTTP.DefaultHeaders)
		}
	}
	for label, value := range map[string]string{"base_url": masked.BaseURL, "http_proxy_url": masked.HTTP.Proxy.URL, "browser_proxy_server": masked.BrowserConfig.Proxy.Server} {
		if strings.Contains(value, "url-pass") || strings.Contains(value, "base-pass") || !strings.Contains(value, "REDACTED") {
			t.Fatalf("%s userinfo was not safely redacted: %q", label, value)
		}
	}
	if masked.BrowserConfig.HTTPCredentials.Password != scrub.Redacted || masked.BrowserConfig.Proxy.Username != scrub.Redacted || masked.BrowserConfig.Proxy.Password != scrub.Redacted || masked.BrowserConfig.ClientCertificates[0].Passphrase != scrub.Redacted {
		t.Fatalf("browser secrets not redacted: %#v", masked.BrowserConfig)
	}
	if o.HTTP.Auth.Password != "pw" {
		t.Fatalf("masking mutated the input: %q", o.HTTP.Auth.Password)
	}
	opaque := &RuntimeSettingsOverride{BaseURL: "user:password@example.test"}
	if got := maskRuntimeSettings(opaque).BaseURL; got != scrub.Redacted {
		t.Fatalf("opaque credential-like base URL = %q, want fail-closed redaction", got)
	}
	if got := reconcileGroupRuntimeSecrets(maskRuntimeSettings(opaque), opaque).BaseURL; got != opaque.BaseURL {
		t.Fatalf("opaque masked URL echo = %q, want prior value %q", got, opaque.BaseURL)
	}
}

func TestMaskRuntimeSettingsRedactsURLQuerySecretsAndReconcilesEcho(t *testing.T) {
	const (
		baseSecret    = "base-query-secret-0123456789"
		proxySecret   = "proxy-query-secret-0123456789"
		browserSecret = "browser-query-secret-0123456789"
	)
	prior := &RuntimeSettingsOverride{
		BaseURL: "https://base-user:base-pass@old.example.test/v1?api%5Fkey=" + baseSecret + "&page=1#access_token=" + baseSecret,
		HTTP: &dsl.HTTPConfig{Proxy: dsl.ProxyConfig{
			URL: "https://proxy.example.test/connect?token=" + proxySecret + "&region=east",
		}},
		BrowserConfig: &dsl.BrowserConfig{Proxy: dsl.BrowserProxyConfig{
			Server: "https://browser-proxy.example.test/connect?credential=" + browserSecret + "&pool=blue",
		}},
	}

	masked := maskRuntimeSettings(prior)
	for label, value := range map[string]string{
		"base_url": masked.BaseURL, "http_proxy_url": masked.HTTP.Proxy.URL, "browser_proxy_server": masked.BrowserConfig.Proxy.Server,
	} {
		if strings.Contains(value, "query-secret") || !strings.Contains(value, scrub.Redacted) {
			t.Fatalf("%s query credential was not redacted: %q", label, value)
		}
	}
	if !strings.Contains(masked.BaseURL, "page=1") || !strings.Contains(masked.HTTP.Proxy.URL, "region=east") || !strings.Contains(masked.BrowserConfig.Proxy.Server, "pool=blue") {
		t.Fatalf("non-secret URL parameters were not preserved: %#v", masked)
	}

	// Simulate a dashboard echo that also makes unrelated URL edits. Only the
	// masked components should be restored from the prior stored override.
	masked.BaseURL = strings.ReplaceAll(masked.BaseURL, "old.example.test/v1", "new.example.test/v2")
	masked.BaseURL = strings.ReplaceAll(masked.BaseURL, "page=1", "page=2")
	masked.HTTP.Proxy.URL = strings.ReplaceAll(masked.HTTP.Proxy.URL, "region=east", "region=west")
	masked.BrowserConfig.Proxy.Server = strings.ReplaceAll(masked.BrowserConfig.Proxy.Server, "pool=blue", "pool=green")
	reconciled := reconcileGroupRuntimeSecrets(masked, prior)
	for label, value := range map[string]string{
		"base_url": reconciled.BaseURL, "http_proxy_url": reconciled.HTTP.Proxy.URL, "browser_proxy_server": reconciled.BrowserConfig.Proxy.Server,
	} {
		if strings.Contains(value, scrub.Redacted) {
			t.Fatalf("%s persisted a redaction sentinel: %q", label, value)
		}
	}
	if !strings.Contains(reconciled.BaseURL, "base-user:base-pass@new.example.test/v2") || !strings.Contains(reconciled.BaseURL, "api%5Fkey="+baseSecret) || !strings.Contains(reconciled.BaseURL, "page=2") || !strings.Contains(reconciled.BaseURL, "access_token="+baseSecret) {
		t.Fatalf("base URL masked echo was not merged safely: %q", reconciled.BaseURL)
	}
	if !strings.Contains(reconciled.HTTP.Proxy.URL, "token="+proxySecret) || !strings.Contains(reconciled.HTTP.Proxy.URL, "region=west") {
		t.Fatalf("HTTP proxy masked echo was not merged safely: %q", reconciled.HTTP.Proxy.URL)
	}
	if !strings.Contains(reconciled.BrowserConfig.Proxy.Server, "credential="+browserSecret) || !strings.Contains(reconciled.BrowserConfig.Proxy.Server, "pool=green") {
		t.Fatalf("browser proxy masked echo was not merged safely: %q", reconciled.BrowserConfig.Proxy.Server)
	}

	orphan := reconcileGroupRuntimeSecrets(&RuntimeSettingsOverride{BaseURL: "https://example.test/?api_key=" + scrub.Redacted + "&page=1"}, nil)
	if strings.Contains(orphan.BaseURL, scrub.Redacted) || strings.Contains(orphan.BaseURL, "api_key=") || !strings.Contains(orphan.BaseURL, "page=1") {
		t.Fatalf("orphaned URL sentinel was persisted instead of removed: %q", orphan.BaseURL)
	}
}

func TestReconcileGroupRuntimeSecretsHTTP(t *testing.T) {
	prior := &RuntimeSettingsOverride{
		BaseURL: "https://base-user:base-password@example.test",
		HTTP: &dsl.HTTPConfig{
			Auth:           dsl.AuthConfig{Password: "real", Token: "realtok"},
			Proxy:          dsl.ProxyConfig{URL: "http://proxy-user:proxy-password@proxy:8080", Username: "realpuser", Password: "realppw"},
			DefaultHeaders: map[string]string{"Authorization": "Bearer real", "Cookie": "session=real", "Accept": "application/json"},
		},
		BrowserConfig: &dsl.BrowserConfig{
			HTTPCredentials: dsl.BrowserHTTPCredentials{Password: "browser-real"},
			Proxy:           dsl.BrowserProxyConfig{Server: "http://user:password@browser-proxy:8080", Username: "browser-user", Password: "browser-password"},
			ClientCertificates: []dsl.BrowserClientCertificate{{
				Origin: "https://example.test", PfxPath: "client.pfx", Passphrase: "certificate-real",
			}},
		},
	}
	incoming := maskRuntimeSettings(prior)
	incoming.HTTP.Auth.Token = "newtok"
	incoming.HTTP.Proxy.Password = ""
	incoming.HTTP.DefaultHeaders["Cookie"] = ""
	incoming.BrowserConfig.Proxy.Password = ""
	result := reconcileGroupRuntimeSecrets(incoming, prior)
	if result.HTTP.Auth.Password != "real" {
		t.Fatalf("auth password should be restored from prior, got %q", result.HTTP.Auth.Password)
	}
	if result.HTTP.Auth.Token != "newtok" {
		t.Fatalf("auth token should persist new value, got %q", result.HTTP.Auth.Token)
	}
	if result.HTTP.Proxy.Username != "realpuser" {
		t.Fatalf("proxy username should be restored from prior, got %q", result.HTTP.Proxy.Username)
	}
	if result.HTTP.Proxy.Password != "" {
		t.Fatalf("proxy password should be cleared, got %q", result.HTTP.Proxy.Password)
	}
	if result.BaseURL != prior.BaseURL || result.HTTP.Proxy.URL != prior.HTTP.Proxy.URL || result.BrowserConfig.Proxy.Server != prior.BrowserConfig.Proxy.Server {
		t.Fatalf("masked URL userinfo was not restored: %#v", result)
	}
	if result.HTTP.DefaultHeaders["Authorization"] != "Bearer real" {
		t.Fatalf("authorization header was not restored: %#v", result.HTTP.DefaultHeaders)
	}
	if _, exists := result.HTTP.DefaultHeaders["Cookie"]; exists {
		t.Fatalf("explicitly cleared cookie header persisted: %#v", result.HTTP.DefaultHeaders)
	}
	if result.BrowserConfig.HTTPCredentials.Password != "browser-real" || result.BrowserConfig.Proxy.Username != "browser-user" || result.BrowserConfig.ClientCertificates[0].Passphrase != "certificate-real" {
		t.Fatalf("masked browser credentials were not restored: %#v", result.BrowserConfig)
	}
	if result.BrowserConfig.Proxy.Password != "" {
		t.Fatalf("explicitly cleared browser proxy password persisted: %#v", result.BrowserConfig.Proxy)
	}
}

func TestMaskManagedTestRedactsHTTPSecrets(t *testing.T) {
	test := &ManagedTest{
		RuntimeSettings: &RuntimeSettingsOverride{HTTP: &dsl.HTTPConfig{Auth: dsl.AuthConfig{Password: "pw"}}},
		Archive: &storage.RunArchiveRecord{
			ArchiveBackend: "private-backend", ArchivePath: "/private/archive", ManifestPath: "/private/archive/manifest.json",
			ArchiveURI: "s3://private/archive", ManifestURI: "s3://private/archive/manifest.json", ManifestSHA256: "portable-integrity",
		},
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
	if masked.Archive == nil || masked.Archive.ArchiveBackend != "" || masked.Archive.ArchivePath != "" || masked.Archive.ManifestPath != "" || masked.Archive.ArchiveURI != "" || masked.Archive.ManifestURI != "" {
		t.Fatalf("managed test archive location was not projected: %#v", masked.Archive)
	}
	if masked.Archive.ManifestSHA256 != "portable-integrity" || test.Archive.ArchivePath != "/private/archive" {
		t.Fatalf("archive projection lost portable integrity or mutated the source: masked=%#v source=%#v", masked.Archive, test.Archive)
	}
}

func TestMaskManagedTestStripsHistoryByDefaultAndAddsSparkline(t *testing.T) {
	base := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	test := &ManagedTest{
		ID: "run-1",
		History: []*LiveMetrics{
			{Timestamp: base, TPS: 1},
			{Timestamp: base.Add(time.Second), TPS: 2},
			{Timestamp: base.Add(2 * time.Second), TPS: 3},
		},
	}

	masked := maskManagedTest(test)
	if len(masked.History) != 0 {
		t.Fatalf("default masked history length = %d, want 0", len(masked.History))
	}
	if len(masked.Sparkline) != 3 || masked.Sparkline[2].TPS != 3 {
		t.Fatalf("sparkline = %#v, want compact TPS points", masked.Sparkline)
	}
	if len(test.History) != 3 {
		t.Fatalf("masking mutated source history")
	}
}

func TestMaskManagedTestIncludesHistoryWindowWhenRequested(t *testing.T) {
	base := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	since := base
	test := &ManagedTest{
		ID: "run-1",
		History: []*LiveMetrics{
			{Timestamp: base, TPS: 1},
			{Timestamp: base.Add(time.Second), TPS: 2},
			{Timestamp: base.Add(2 * time.Second), TPS: 3},
		},
	}

	masked := maskManagedTestWithOptions(test, managedTestMaskOptions{
		IncludeHistory: true,
		HistorySince:   &since,
		HistoryLimit:   1,
	})
	if len(masked.History) != 1 || masked.History[0].TPS != 3 {
		t.Fatalf("history window = %#v, want latest point after since", masked.History)
	}
}
