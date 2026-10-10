package dsl

import (
	"slices"
	"testing"
)

func TestScriptUsesHTTP3FollowsPrecedence(t *testing.T) {
	leaf := func(name, policy string) Action {
		action := Action{Name: name, Type: "GET", Target: "/" + name}
		if policy != "" {
			action.HTTPVersion = leafVersion(t, policy)
		}
		return action
	}
	exactOnly := []string{FeatureNativeHTTP3V1}
	both := []string{FeatureNativeHTTP3V1, FeatureHTTP3UpgradeV1}
	cases := []struct {
		name     string
		script   *Script
		features []string
	}{
		{"plain", http3Script("", leaf("a", "")), nil},
		{"script auto+h3", http3Script("auto+h3", leaf("a", "")), both},
		{"one leaf 3", http3Script("2", leaf("a", ""), leaf("b", "3")), exactOnly},
		{"3 and auto+h3", http3Script("3", leaf("a", ""), leaf("b", "auto+h3")), both},
		{"script 3 overridden everywhere", http3Script("3", leaf("a", "1.1"), leaf("b", "auto")), nil},
		{"sequence child", http3Script("", Action{Name: "seq", Type: "http_sequence", Params: map[string]any{"requests": []any{
			map[string]any{"name": "c", "type": "GET", "target": "https://app.example.test/c", "http_version": "auto+h3"},
		}}}), both},
	}
	for _, tc := range cases {
		if got := ScriptUsesHTTP3(tc.script); got != (tc.features != nil) {
			t.Fatalf("%s: ScriptUsesHTTP3 = %v", tc.name, got)
		}
		if features := RequiredHTTPRuntimeFeatures(tc.script); !slices.Equal(features, tc.features) {
			t.Fatalf("%s: required features = %v, want %v", tc.name, features, tc.features)
		}
	}
	browser := &Script{Name: "b", Protocol: "browser", Config: ScriptConfig{HTTP: HTTPConfig{Version: "3"}}, Actions: []Action{{Name: "open", Type: "browser_navigate", Target: "https://app.example.test/"}}}
	if ScriptUsesHTTP3(browser) || ScriptUsesHTTP3(nil) {
		t.Fatal("a browser or nil script was reported as using native HTTP/3")
	}
}

func TestHTTP3AliasIsAnHTTPProtocolDefaultingToExact3(t *testing.T) {
	script := &Script{Version: "1", Name: "alias", Protocol: "http/3", Config: ScriptConfig{BaseURL: "https://app.example.test"},
		Actions: []Action{{Name: "home", Type: "GET", Target: "/"}}}
	if ProtocolFamily("http/3") != "http" || InheritedHTTPVersion(script) != "3" {
		t.Fatalf("family=%q inherited=%q", ProtocolFamily("http/3"), InheritedHTTPVersion(script))
	}
	if exact, upgrade := ScriptHTTP3Policies(script); !exact || upgrade {
		t.Fatalf("policies exact=%v upgrade=%v", exact, upgrade)
	}
	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("issues = %v", issues)
	}
	script.Config.BaseURL = "http://app.example.test"
	if issues := http3Issues(script); len(issues) != 1 {
		t.Fatalf("an http:// base under the http/3 alias: %v", issues)
	}
}
