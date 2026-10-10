package dsl

import (
	"strings"
	"testing"
)

func http3Script(version string, actions ...Action) *Script {
	if len(actions) == 0 {
		actions = []Action{{Name: "home", Type: "GET", Target: "/"}}
	}
	return &Script{Version: "1", Name: "h3", Protocol: "http",
		Config:  ScriptConfig{BaseURL: "https://app.example.test", HTTP: HTTPConfig{Version: version}},
		Actions: actions}
}

func leafVersion(t *testing.T, policy string) OptionalHTTPVersion {
	t.Helper()
	value, err := NewHTTPVersionOverride(policy)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func http3Issues(script *Script) []string {
	var found []string
	for _, issue := range ValidateScriptIssues(script) {
		if strings.Contains(issue, "HTTP/3") || strings.Contains(issue, "http_version 3") || strings.Contains(issue, "https://") {
			found = append(found, issue)
		}
	}
	return found
}

func TestValidateHTTP3PoliciesSaveOnEveryInstallation(t *testing.T) {
	for _, version := range []string{"3", "auto+h3"} {
		script := http3Script(version, Action{Name: "leaf", Type: "GET", Target: "/x", HTTPVersion: leafVersion(t, version)})
		if issues := ValidateScriptIssues(script); len(issues) != 0 {
			t.Fatalf("%s: issues %v", version, issues)
		}
	}
}

func TestValidateHTTP3RejectsWhatExactHTTP3CannotHonor(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Script)
		want   string
	}{
		{"http base url", func(s *Script) { s.Config.BaseURL = "http://app.example.test" }, "config.base_url must use https://"},
		{"tls maximum below 1.3", func(s *Script) { s.Config.HTTP.TLS.MaxVersion = "1.2" }, "config.http.tls.max_version must be 1.3 or unset"},
		{"custom proxy", func(s *Script) { s.Config.HTTP.Proxy.URL = "http://proxy.corp:8080" }, "http3_proxy_unsupported"},
		{"integrated auth", func(s *Script) { s.Config.HTTP.Auth = AuthConfig{Type: "ntlm", Username: "u", Password: "p"} }, "config.http.auth.type ntlm cannot run over HTTP/3"},
		{"network shaping", func(s *Script) {
			s.Config.WebRuntime.NetworkSimulation = NetworkSimulationConfig{Mode: "preset", Preset: "dsl"}
		}, "config.web_runtime.network_simulation cannot be combined"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := http3Script("3")
			tc.mutate(script)
			issues := http3Issues(script)
			if len(issues) != 1 || !strings.Contains(issues[0], tc.want) {
				t.Fatalf("issues = %v, want one containing %q", issues, tc.want)
			}
			// auto+h3 keeps these requests on TCP; only shaping is refused.
			script.Config.HTTP.Version = "auto+h3"
			issues = http3Issues(script)
			if tc.name == "network shaping" {
				if len(issues) != 1 {
					t.Fatalf("auto+h3 shaping issues = %v", issues)
				}
			} else if len(issues) != 0 {
				t.Fatalf("auto+h3 issues = %v", issues)
			}
		})
	}
}

func TestValidateHTTP3ChecksEachLeafAfterPrecedence(t *testing.T) {
	// A leaf-level 3 with a relative target takes the base URL's scheme.
	relative := Action{Name: "relative", Type: "GET", Target: "/x", HTTPVersion: leafVersion(t, "3")}
	plainBase := http3Script("", relative)
	plainBase.Config.BaseURL = "http://app.example.test"
	if issues := http3Issues(plainBase); len(issues) != 1 || !strings.Contains(issues[0], `"relative".target must use https://`) {
		t.Fatalf("relative leaf issues = %v", issues)
	}
	plainLeaf := Action{Name: "plain", Type: "GET", Target: "http://legacy.example.test/", HTTPVersion: leafVersion(t, "3")}
	issues := http3Issues(http3Script("", plainLeaf))
	if len(issues) != 1 || !strings.Contains(issues[0], `actions[0] "plain".target must use https://`) {
		t.Fatalf("leaf issues = %v", issues)
	}
	// A leaf that overrides an exact-3 script to 1.1 may use http://.
	override := Action{Name: "legacy", Type: "GET", Target: "http://legacy.example.test/", HTTPVersion: leafVersion(t, "1.1")}
	if issues := http3Issues(http3Script("3", override)); len(issues) != 0 {
		t.Fatalf("override issues = %v", issues)
	}
	// Unresolved templates are checked again after resolution.
	templated := Action{Name: "templated", Type: "GET", Target: "{{var.scheme}}://app.example.test/", HTTPVersion: leafVersion(t, "3")}
	if issues := http3Issues(http3Script("", templated)); len(issues) != 0 {
		t.Fatalf("template issues = %v", issues)
	}
	leafAuth := Action{Name: "sso", Type: "GET", Target: "/sso", HTTPVersion: leafVersion(t, "3"), Auth: &AuthConfig{Type: "negotiate"}}
	if issues := http3Issues(http3Script("", leafAuth)); len(issues) != 1 || !strings.Contains(issues[0], `"sso".auth.type negotiate`) {
		t.Fatalf("leaf auth issues = %v", issues)
	}
}

func TestValidateHTTP3ReachesSequenceChildrenAndBranches(t *testing.T) {
	sequence := Action{Name: "batch", Type: "http_sequence", Params: map[string]any{"requests": []any{
		map[string]any{"name": "one", "type": "GET", "target": "https://app.example.test/1", "http_version": "3"},
		map[string]any{"name": "two", "type": "GET", "target": "http://app.example.test/2", "http_version": "3"},
	}}}
	issues := http3Issues(http3Script("", sequence))
	if len(issues) != 1 || !strings.Contains(issues[0], `params.requests[1] "two".target must use https://`) {
		t.Fatalf("sequence issues = %v", issues)
	}
	branch := Action{Name: "loop", Type: "control_loop", Control: &ControlFlow{Count: 2, Steps: []Action{
		{Name: "inner", Type: "GET", Target: "http://app.example.test/in", HTTPVersion: leafVersion(t, "3")},
	}}}
	issues = http3Issues(http3Script("", branch))
	if len(issues) != 1 || !strings.Contains(issues[0], `"inner".target must use https://`) {
		t.Fatalf("branch issues = %v", issues)
	}
}

func TestValidateHTTP3ShapingFollowsRuntimeActivity(t *testing.T) {
	for _, tc := range []struct {
		name    string
		network NetworkSimulationConfig
		want    bool
	}{
		{"unset", NetworkSimulationConfig{}, false},
		{"max bandwidth", NetworkSimulationConfig{Mode: "max"}, false},
		{"preset", NetworkSimulationConfig{Mode: "preset", Preset: "56k"}, true},
		{"custom latency", NetworkSimulationConfig{Mode: "custom", LatencyMs: 40}, true},
		{"max with loss", NetworkSimulationConfig{Mode: "max", PacketLossPct: 1}, true},
	} {
		script := http3Script("auto+h3")
		script.Config.WebRuntime.NetworkSimulation = tc.network
		if got := len(http3Issues(script)) == 1; got != tc.want {
			t.Fatalf("%s: shaping refused = %v, want %v", tc.name, got, tc.want)
		}
	}
	script := http3Script("3")
	script.Config.WebRuntime.NetworkProfiles = []NetworkProfileWeight{{Name: "slow", Weight: 1, NetworkSimulationConfig: NetworkSimulationConfig{Mode: "preset", Preset: "dsl"}}}
	if issues := http3Issues(script); len(issues) != 1 || !strings.Contains(issues[0], "network_profiles[0]") {
		t.Fatalf("profile issues = %v", issues)
	}
}

func TestValidateHTTP3ConfigAuthOnlyWhenAnExactRequestInheritsIt(t *testing.T) {
	ownAuth := Action{Name: "own", Type: "GET", Target: "/own", HTTPVersion: leafVersion(t, "3"), Auth: &AuthConfig{Type: "bearer", Token: "{{secret.token}}"}}
	tcpLeaf := Action{Name: "tcp", Type: "GET", Target: "/tcp"}
	script := http3Script("2", ownAuth, tcpLeaf)
	script.Config.HTTP.Auth = AuthConfig{Type: "ntlm", Username: "u", Password: "p"}
	if issues := http3Issues(script); len(issues) != 0 {
		t.Fatalf("issues = %v; the only exact-3 request sets its own auth", issues)
	}
	inherits := Action{Name: "inherits", Type: "GET", Target: "/x", HTTPVersion: leafVersion(t, "3")}
	script.Actions = append(script.Actions, inherits)
	if issues := http3Issues(script); len(issues) != 1 || !strings.Contains(issues[0], "config.http.auth.type ntlm") {
		t.Fatalf("issues = %v", issues)
	}
}

func TestHTTP3UseIgnoresWebSocketSteps(t *testing.T) {
	ws := Action{Name: "socket", Type: "ws_connect", Target: "wss://app.example.test/socket"}
	plain := Action{Name: "plain", Type: "GET", Target: "/", HTTPVersion: leafVersion(t, "1.1")}
	if ScriptUsesHTTP3(http3Script("3", ws, plain)) {
		t.Fatal("a WebSocket step inheriting 3 counted as HTTP/3 work")
	}
}

func TestValidateHTTP3ConfigAuthFollowsItsHostScope(t *testing.T) {
	leaf := Action{Name: "public", Type: "GET", Target: "https://public.example.test/", HTTPVersion: leafVersion(t, "3")}
	script := http3Script("", leaf)
	script.Config.HTTP.Auth = AuthConfig{Type: "ntlm", Username: "u", Password: "p", Hosts: []string{"intranet.example.test"}}
	if issues := http3Issues(script); len(issues) != 0 {
		t.Fatalf("NTLM scoped to another host blocked exact 3: %v", issues)
	}
	for _, hosts := range [][]string{{"public.example.test"}, {"PUBLIC.example.test:443"}, nil} {
		script.Config.HTTP.Auth.Hosts = hosts
		if issues := http3Issues(script); len(issues) != 1 || !strings.Contains(issues[0], "config.http.auth.type ntlm") {
			t.Fatalf("hosts %v: issues = %v", hosts, issues)
		}
	}
	script.Config.HTTP.Auth.Hosts = []string{"public.example.test:8443"}
	if issues := http3Issues(script); len(issues) != 0 {
		t.Fatalf("a scope on another port blocked exact 3: %v", issues)
	}
	// A relative target takes the base URL's host.
	relative := http3Script("", Action{Name: "rel", Type: "GET", Target: "/x", HTTPVersion: leafVersion(t, "3")})
	relative.Config.HTTP.Auth = AuthConfig{Type: "negotiate", Hosts: []string{"app.example.test"}}
	if issues := http3Issues(relative); len(issues) != 1 {
		t.Fatalf("relative target issues = %v", issues)
	}
}
