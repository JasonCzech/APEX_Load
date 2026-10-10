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
	cases := []struct {
		name   string
		script *Script
		want   bool
	}{
		{"plain", http3Script("", leaf("a", "")), false},
		{"script auto+h3", http3Script("auto+h3", leaf("a", "")), true},
		{"one leaf 3", http3Script("2", leaf("a", ""), leaf("b", "3")), true},
		{"script 3 overridden everywhere", http3Script("3", leaf("a", "1.1"), leaf("b", "auto")), false},
		{"sequence child", http3Script("", Action{Name: "seq", Type: "http_sequence", Params: map[string]any{"requests": []any{
			map[string]any{"name": "c", "type": "GET", "target": "https://app.example.test/c", "http_version": "auto+h3"},
		}}}), true},
	}
	for _, tc := range cases {
		if got := ScriptUsesHTTP3(tc.script); got != tc.want {
			t.Fatalf("%s: ScriptUsesHTTP3 = %v, want %v", tc.name, got, tc.want)
		}
		features := RequiredHTTPRuntimeFeatures(tc.script)
		if tc.want != slices.Equal(features, []string{FeatureNativeHTTP3V1}) {
			t.Fatalf("%s: required features = %v", tc.name, features)
		}
	}
	browser := &Script{Name: "b", Protocol: "browser", Config: ScriptConfig{HTTP: HTTPConfig{Version: "3"}}, Actions: []Action{{Name: "open", Type: "browser_navigate", Target: "https://app.example.test/"}}}
	if ScriptUsesHTTP3(browser) || ScriptUsesHTTP3(nil) {
		t.Fatal("a browser or nil script was reported as using native HTTP/3")
	}
}
