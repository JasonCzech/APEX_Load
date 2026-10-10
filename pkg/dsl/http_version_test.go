package dsl

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustDSLHTTPVersion(t *testing.T, policy string) OptionalHTTPVersion {
	t.Helper()
	value, err := NewHTTPVersionOverride(policy)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestHTTPVersionLeafValidationAndSequenceChildren(t *testing.T) {
	for _, raw := range []string{`null`, `""`, `"h3"`, `"h3-29"`, `"HTTP/3"`, `2`, `false`, `{}`, `[]`} {
		for _, child := range []bool{false, true} {
			action := `{"name":"request","type":"GET","target":"/","http_version":` + raw + `}`
			if child {
				action = `{"name":"group","type":"http_sequence","params":{"requests":[` + action + `]}}`
			}
			payload := `{"name":"script","protocol":"http","config":{"base_url":"https://example.test"},"actions":[` + action + `]}`
			if _, err := Parse([]byte(payload)); err == nil {
				t.Fatalf("accepted child=%v %s", child, raw)
			}
		}
	}
	for _, policy := range []string{"auto", "1.1", "2"} {
		script := httpContractScript("2", Action{HTTPVersion: mustDSLHTTPVersion(t, policy)})
		if issues := ValidateScriptIssues(script); len(issues) != 0 {
			t.Fatalf("policy=%s issues=%v", policy, issues)
		}
		script.Actions[0] = Action{Name: "legacy", Type: "request", Method: "POST", Target: "/", HTTPVersion: mustDSLHTTPVersion(t, policy)}
		if issues := ValidateScriptIssues(script); len(issues) != 0 {
			t.Fatalf("legacy policy=%s issues=%v", policy, issues)
		}
	}
	for _, kind := range []string{"http_oauth2_token", "http_sequence", "control_loop", "cookie_clear"} {
		script := httpContractScript("", Action{Type: kind, HTTPVersion: mustDSLHTTPVersion(t, "1.1")})
		if issues := strings.Join(ValidateScriptIssues(script), " "); !strings.Contains(issues, "http_version is supported only") {
			t.Fatalf("kind=%s issues=%s", kind, issues)
		}
	}
}

func TestHTTPVersionDefaultHeadersUseEffectiveLeafWinners(t *testing.T) {
	script := httpContractScript("2", Action{HTTPVersion: mustDSLHTTPVersion(t, "1.1")})
	script.Config.HTTP.DefaultHeaders = map[string]string{"Connection": "keep-alive"}
	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("explicit H1 rejected: %v", issues)
	}
	script.Actions[0].HTTPVersion = mustDSLHTTPVersion(t, "2")
	if issues := strings.Join(ValidateScriptIssues(script), " "); !strings.Contains(issues, "hop_by_hop_header") {
		t.Fatalf("H2 defaults accepted: %s", issues)
	}
	disabled := false
	script.Actions[0].HeaderList = HeaderList{{Name: "Connection", Value: "ignored", Enabled: &disabled}}
	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("shadowed default rejected: %v", issues)
	}
	script.Actions[0].HeaderList = nil
	script.Actions[0] = Action{Name: "group", Type: "http_sequence", Params: map[string]any{"requests": []Action{{Name: "child", Type: "GET", Target: "/", HTTPVersion: mustDSLHTTPVersion(t, "1.1")}}}}
	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("H1 child rejected: %v", issues)
	}
}

func TestInheritedHTTPVersionAliasAndExplicitConfigurationPrecedence(t *testing.T) {
	for _, tc := range []struct{ alias, config, want string }{{"http/2", "", "2"}, {"http/1.1", "", "1.1"}, {"http/2", "1.1", "1.1"}, {"http/1.1", "2", "2"}, {"http/2", " ", ""}, {"http", "", ""}} {
		script := httpContractScript(tc.config, Action{})
		script.Protocol = tc.alias
		if got := InheritedHTTPVersion(script); got != tc.want {
			t.Fatalf("alias=%s config=%q got=%q", tc.alias, tc.config, got)
		}
		if issues := ValidateScriptIssues(script); len(issues) != 0 {
			t.Fatalf("alias=%s issues=%v", tc.alias, issues)
		}
		script.Actions[0].HTTPVersion = mustDSLHTTPVersion(t, "auto")
		if got, err := EffectiveHTTPVersion(script.Actions[0], InheritedHTTPVersion(script)); err != nil || got != "auto" {
			t.Fatalf("leaf got=%s err=%v", got, err)
		}
	}
	var absent, explicit Action
	if err := json.Unmarshal([]byte(`{"name":"leaf","type":"GET","target":"/"}`), &absent); err != nil {
		t.Fatal(err)
	}
	explicit = absent
	explicit.HTTPVersion = mustDSLHTTPVersion(t, "auto")
	a, _ := json.Marshal(absent)
	b, _ := json.Marshal(explicit)
	if string(a) == string(b) {
		t.Fatal("inherited and explicit auto identity collapsed")
	}
}
