package dsl

import (
	"strings"
	"testing"
)

func appiumObserverScript() *Script {
	return &Script{
		Name:     "SearchAuth on a real iPad",
		Protocol: "appium",
		Config: ScriptConfig{Appium: AppiumConfig{
			ServerURL:    "https://{{secret.KOBITON_USER}}:{{secret.KOBITON_KEY}}@api.kobiton.com/wd/hub",
			Capabilities: map[string]any{"platformName": "iOS", "appium:bundleId": "com.example.searchauth"},
		}},
		Actions: []Action{
			{Name: "Launch", Type: "appium_activate_app", Params: map[string]any{"bundle_id": "com.example.searchauth"}},
			{Name: "Start Search", Type: "source_transaction", Params: map[string]any{"function": "lr_start_transaction", "args": []any{"Search"}}},
			{Name: "Query", Type: "appium_type", Target: "searchField", Value: "acme", Params: map[string]any{"clear": true}},
			{Name: "Go", Type: "appium_tap", Target: "type == 'XCUIElementTypeButton' AND label == 'Search'", Params: map[string]any{"using": "-ios predicate string"}},
			{Name: "Results", Type: "appium_wait", Target: "resultsList", Params: map[string]any{"text": "results", "timeout_ms": float64(30000)}},
			{Name: "End Search", Type: "source_transaction", Params: map[string]any{"function": "lr_end_transaction", "args": []any{"Search"}}},
			{Name: "Scroll", Type: "appium_execute", Params: map[string]any{"script": "mobile: scroll", "args": map[string]any{"direction": "down"}}},
			{Name: "Evidence", Type: "appium_screenshot"},
		},
	}
}

func TestAppiumObserverScriptValidates(t *testing.T) {
	if issues := ValidateScriptIssues(appiumObserverScript()); len(issues) != 0 {
		t.Fatalf("issues = %v", issues)
	}
}

func TestAppiumObserverScriptIssues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Script)
		want   string
	}{
		{"no server", func(s *Script) { s.Config.Appium.ServerURL = "" }, "config.appium.server_url is required"},
		{"not a URL", func(s *Script) { s.Config.Appium.ServerURL = "ftp://cloud" }, "must be an http or https URL"},
		{"no platform", func(s *Script) { delete(s.Config.Appium.Capabilities, "platformName") }, "capabilities.platformName is required"},
		{"long session timeout", func(s *Script) { s.Config.Appium.SessionTimeoutMs = 3_600_000 }, "session_timeout_ms must be a whole number"},
		{"unknown step", func(s *Script) { s.Actions[0].Type = "http" }, `unsupported Appium action type "http"`},
		{"no target", func(s *Script) { s.Actions[3].Target = " " }, "appium_tap requires a target element"},
		{"bad locator", func(s *Script) { s.Actions[3].Params["using"] = "css selector" }, "params.using must be one of"},
		{"bad timeout", func(s *Script) { s.Actions[4].Params["timeout_ms"] = float64(-1) }, "params.timeout_ms must be a whole number"},
		{"text not a string", func(s *Script) { s.Actions[4].Params["text"] = float64(3) }, "params.text must be a string"},
		{"no bundle", func(s *Script) { s.Actions[0].Params = nil }, "appium_activate_app requires params.bundle_id"},
		{"no script", func(s *Script) { delete(s.Actions[6].Params, "script") }, "appium_execute requires params.script"},
		{"args not an object", func(s *Script) { s.Actions[6].Params["args"] = []any{"down"} }, "params.args must be an object"},
	} {
		script := appiumObserverScript()
		tc.mutate(script)
		if issues := strings.Join(ValidateScriptIssues(script), "\n"); !strings.Contains(issues, tc.want) {
			t.Fatalf("%s: issues = %q, want %q", tc.name, issues, tc.want)
		}
	}
}
