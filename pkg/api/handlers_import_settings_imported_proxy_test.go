package api

import (
	"testing"
)

// An imported proxy username or proxy URL userinfo is script-sourced, never a
// secret: the import must let the user replace it with a {{secret.*}}
// reference, clear it, or drop the userinfo, while still refusing a new typed
// credential literal.
func TestImportSettingsImportedProxyCredentialsStayEditable(t *testing.T) {
	source := settingsTestScript()
	source.Config.HTTP.Proxy.URL = "http://svc:pw@proxy.example.test:8080"
	source.Config.HTTP.Proxy.Username = "alice"

	for _, tc := range []struct {
		name   string
		change ImportSettingsChange
		check  func(t *testing.T, username, url string)
	}{
		{"username to reference", settingChange("config.http.proxy.username", `"{{secret.PROXY_USER}}"`), func(t *testing.T, username, _ string) {
			if username != "{{secret.PROXY_USER}}" {
				t.Fatalf("username=%q", username)
			}
		}},
		{"username reset to default", settingChange("config.http.proxy.username", `null`), func(t *testing.T, username, _ string) {
			if username != "" {
				t.Fatalf("username=%q", username)
			}
		}},
		{"url reset to default", settingChange("config.http.proxy.url", `null`), func(t *testing.T, _, url string) {
			if url != "" {
				t.Fatalf("url=%q", url)
			}
		}},
		{"url without userinfo", settingChange("config.http.proxy.url", `"http://proxy2.example.test:8080"`), func(t *testing.T, _, url string) {
			if url != "http://proxy2.example.test:8080" {
				t.Fatalf("url=%q", url)
			}
		}},
		{"imported url resent unchanged", settingChange("config.http.proxy.url", `"http://svc:pw@proxy.example.test:8080"`), func(t *testing.T, _, url string) {
			if url != "http://svc:pw@proxy.example.test:8080" {
				t.Fatalf("url=%q", url)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft, errs := applyImportSettings(cloneScriptDefinition(source), "all", []ImportSettingsChange{tc.change})
			if len(errs) != 0 || draft == nil {
				t.Fatalf("applyImportSettings refused an edit of a script-sourced value: %v", errs)
			}
			tc.check(t, draft.Config.HTTP.Proxy.Username, draft.Config.HTTP.Proxy.URL)
		})
	}

	for _, tc := range []struct {
		name   string
		change ImportSettingsChange
	}{
		{"typed username literal", settingChange("config.http.proxy.username", `"bob"`)},
		{"typed url userinfo", settingChange("config.http.proxy.url", `"http://other:pw2@proxy.example.test:8080"`)},
		{"mask placeholder over imported username", settingChange("config.http.proxy.username", `"[REDACTED]"`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if draft, errs := applyImportSettings(cloneScriptDefinition(source), "all", []ImportSettingsChange{tc.change}); len(errs) == 0 || draft != nil {
				t.Fatalf("expected a refusal, got draft=%v errs=%v", draft != nil, errs)
			}
		})
	}

	// After an apply the reference can be cleared or swapped for another one.
	applied, errs := applyImportSettings(cloneScriptDefinition(source), "all", []ImportSettingsChange{settingChange("config.http.proxy.username", `"{{secret.PROXY_USER}}"`)})
	if len(errs) != 0 || applied == nil {
		t.Fatalf("first apply: %v", errs)
	}
	for _, value := range []string{`null`, `"{{secret.OTHER_PROXY_USER}}"`} {
		if draft, errs := applyImportSettings(cloneScriptDefinition(applied), "all", []ImportSettingsChange{settingChange("config.http.proxy.username", value)}); len(errs) != 0 || draft == nil {
			t.Fatalf("second apply %s: %v", value, errs)
		}
	}
}
