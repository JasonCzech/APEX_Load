package dsl

import (
	"strings"
	"testing"
)

func browserEmulationTestScript(cfg *BrowserEmulationConfig) *Script {
	return &Script{
		Name:     "be-test",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test", BrowserEmulation: cfg},
		Actions:  []Action{{Type: "http_request", Method: "GET", Target: "/"}},
	}
}

func browserEmulationIssues(t *testing.T, cfg *BrowserEmulationConfig) []string {
	t.Helper()
	return validateBrowserEmulationIssues(browserEmulationTestScript(cfg))
}

func requireIssueContaining(t *testing.T, issues []string, want string) {
	t.Helper()
	for _, issue := range issues {
		if strings.Contains(issue, want) {
			return
		}
	}
	t.Fatalf("no issue containing %q in %v", want, issues)
}

func TestValidateBrowserEmulationNilAndValid(t *testing.T) {
	if issues := browserEmulationIssues(t, nil); len(issues) != 0 {
		t.Fatalf("nil section produced issues: %v", issues)
	}
	valid := []*BrowserEmulationConfig{
		{Mode: "specific", BrowserType: "chrome", BrowserVersion: "126", Platform: "windows", Language: "en-US"},
		{Mode: "specific", BrowserType: "safari"},
		// mode specific with no browser yet is a valid no-op (editor transient state).
		{Mode: "specific"},
		{Mode: "custom", CustomUserAgent: "MyAgent/1.0"},
		{Cache: &BrowserCacheSimulationConfig{SimulateCache: boolPtrForTest(true), CacheContentURLs: boolPtrForTest(true)}},
		// Child flags left on while the cache is off are inert, not invalid: the
		// protocol never reads them with h.cache nil. Blocking save here would
		// break API/CLI/IaC clients that toggle simulate_cache without also
		// clearing the children.
		{Cache: &BrowserCacheSimulationConfig{SimulateCache: boolPtrForTest(false), CacheContentURLs: boolPtrForTest(true)}},
		{Cache: &BrowserCacheSimulationConfig{SimulateCache: boolPtrForTest(false), CheckNewerVersions: boolPtrForTest(true)}},
	}
	for i, cfg := range valid {
		if issues := browserEmulationIssues(t, cfg); len(issues) != 0 {
			t.Errorf("valid[%d] produced issues: %v", i, issues)
		}
	}
}

func TestValidateBrowserEmulationRejections(t *testing.T) {
	tests := []struct {
		name string
		cfg  *BrowserEmulationConfig
		want string
	}{
		{"bad mode", &BrowserEmulationConfig{Mode: "auto"}, "mode must be specific or custom"},
		{"custom missing ua", &BrowserEmulationConfig{Mode: "custom"}, "custom_user_agent is required"},
		{"custom with specific fields", &BrowserEmulationConfig{Mode: "custom", CustomUserAgent: "x", BrowserType: "chrome"}, "must be empty when mode is custom"},
		{"specific unknown type", &BrowserEmulationConfig{Mode: "specific", BrowserType: "netscape"}, "not in the browser catalog"},
		{"specific bad platform", &BrowserEmulationConfig{Mode: "specific", BrowserType: "safari", Platform: "windows"}, "not available for browser_type"},
		{"specific bad version", &BrowserEmulationConfig{Mode: "specific", BrowserType: "chrome", BrowserVersion: "latest"}, "numeric major version"},
		{"specific with custom ua", &BrowserEmulationConfig{Mode: "specific", BrowserType: "chrome", CustomUserAgent: "x"}, "must be empty when mode is specific"},
		{"bad language", &BrowserEmulationConfig{Language: "not a tag!"}, "BCP-47"},
		{"control chars in ua", &BrowserEmulationConfig{Mode: "custom", CustomUserAgent: "bad\x00agent"}, "control characters"},
		{"oversize ua", &BrowserEmulationConfig{Mode: "custom", CustomUserAgent: strings.Repeat("a", 1025)}, "must not exceed 1024 bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireIssueContaining(t, browserEmulationIssues(t, tt.cfg), tt.want)
		})
	}
}

func TestValidateScriptIssuesIncludesBrowserEmulation(t *testing.T) {
	script := browserEmulationTestScript(&BrowserEmulationConfig{Mode: "bogus"})
	requireIssueContaining(t, ValidateScriptIssues(script), "config.browser_emulation.mode")
}

func boolPtrForTest(b bool) *bool { return &b }
