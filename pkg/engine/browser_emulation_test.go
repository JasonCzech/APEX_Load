package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func browserEmulationScript(emu *dsl.BrowserEmulationConfig, http dsl.HTTPConfig) *dsl.Script {
	return &dsl.Script{
		Name:     "be",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL:          "https://example.test",
			HTTP:             http,
			BrowserEmulation: emu,
		},
	}
}

func TestAppendBrowserEmulationHTTPConfigUnsetLeavesLegacyBehavior(t *testing.T) {
	cfg := buildProtoConfig(browserEmulationScript(nil, dsl.HTTPConfig{UserAgent: "Legacy/1.0", AcceptLanguage: "fr-FR"}))
	if cfg["user_agent"] != "Legacy/1.0" {
		t.Fatalf("user_agent = %v, want Legacy/1.0", cfg["user_agent"])
	}
	if cfg["accept_language"] != "fr-FR" {
		t.Fatalf("accept_language = %v, want fr-FR", cfg["accept_language"])
	}
	for _, key := range []string{"http_cache_content_urls_only", "http_cache_check_newer_versions", "http_cache_clear_per_iteration", "http_download_non_html_resources", "http_simulate_prefetch"} {
		if _, present := cfg[key]; present {
			t.Errorf("%s should not be emitted without browser_emulation", key)
		}
	}
}

func TestAppendBrowserEmulationHTTPConfigCustomModeOverridesUA(t *testing.T) {
	emu := &dsl.BrowserEmulationConfig{Mode: "custom", CustomUserAgent: "MyAgent/2.0", Language: "de-DE"}
	cfg := buildProtoConfig(browserEmulationScript(emu, dsl.HTTPConfig{UserAgent: "Legacy/1.0", AcceptLanguage: "fr-FR"}))
	if cfg["user_agent"] != "MyAgent/2.0" {
		t.Fatalf("user_agent = %v, want MyAgent/2.0", cfg["user_agent"])
	}
	if cfg["accept_language"] != "de-DE,de;q=0.9" {
		t.Fatalf("accept_language = %v, want de-DE,de;q=0.9", cfg["accept_language"])
	}
}

func TestAppendBrowserEmulationHTTPConfigSpecificModeDerivesUA(t *testing.T) {
	emu := &dsl.BrowserEmulationConfig{Mode: "specific", BrowserType: "chrome", BrowserVersion: "126", Platform: "windows"}
	cfg := buildProtoConfig(browserEmulationScript(emu, dsl.HTTPConfig{UserAgent: "Legacy/1.0"}))
	ua, _ := cfg["user_agent"].(string)
	if !strings.Contains(ua, "Chrome/126.0.0.0") || !strings.Contains(ua, "Windows NT") {
		t.Fatalf("user_agent = %q, want catalog-derived Chrome 126 on Windows", ua)
	}
}

func TestAppendBrowserEmulationHTTPConfigCacheFlags(t *testing.T) {
	on, off := true, false
	emu := &dsl.BrowserEmulationConfig{Cache: &dsl.BrowserCacheSimulationConfig{
		SimulateCache:        &off,
		CacheContentURLs:     &on,
		CheckNewerVersions:   &on,
		NewUserEachIteration: &on,
		DownloadNonHTML:      &on,
		SimulatePrefetch:     &on,
	}}
	// SimulateCache=false must win over http.cache_enabled=true.
	cfg := buildProtoConfig(browserEmulationScript(emu, dsl.HTTPConfig{CacheEnabled: &on}))
	if cfg["http_cache_enabled"] != false {
		t.Fatalf("http_cache_enabled = %v, want false (SimulateCache override)", cfg["http_cache_enabled"])
	}
	for key, want := range map[string]bool{
		"http_cache_content_urls_only":     true,
		"http_cache_check_newer_versions":  true,
		"http_cache_clear_per_iteration":   true,
		"http_download_non_html_resources": true,
		"http_simulate_prefetch":           true,
	} {
		if cfg[key] != want {
			t.Errorf("%s = %v, want %v", key, cfg[key], want)
		}
	}
}

// cacheAndCookieControllerProtocol records both clear capabilities for the
// iteration-reset assertions.
type cacheAndCookieControllerProtocol struct {
	stubProtocol
	cookieClears int
	cacheClears  int
}

func (c *cacheAndCookieControllerProtocol) ClearCookies(context.Context) error {
	c.cookieClears++
	return nil
}

func (c *cacheAndCookieControllerProtocol) ClearCache(context.Context) error {
	c.cacheClears++
	return nil
}

func TestVUserRunNewUserEachIterationClearsCacheAndCookies(t *testing.T) {
	on := true
	proto := &cacheAndCookieControllerProtocol{}
	vu := &VUser{
		ID:    1,
		Group: "g",
		Proto: proto,
		Script: &dsl.Script{
			Name:     "s",
			Protocol: "http",
			Config: dsl.ScriptConfig{
				RunLogic: dsl.RunLogicConfig{Iterations: 3},
				BrowserEmulation: &dsl.BrowserEmulationConfig{
					Cache: &dsl.BrowserCacheSimulationConfig{NewUserEachIteration: &on},
				},
			},
			Actions: []dsl.Action{{Name: "GET /", Type: "request", Method: "GET", Target: "/"}},
		},
		Metrics: NewMetricsCollector(16),
	}

	vu.Run(context.Background())

	if proto.cookieClears != 2 {
		t.Fatalf("ClearCookies calls = %d, want 2 between 3 iterations", proto.cookieClears)
	}
	if proto.cacheClears != 2 {
		t.Fatalf("ClearCache calls = %d, want 2 between 3 iterations", proto.cacheClears)
	}
}

func TestVUserRunNewUserWithoutCacheControllerIsNoop(t *testing.T) {
	on := true
	proto := &cookieControllerProtocol{}
	vu := &VUser{
		ID:    1,
		Group: "g",
		Proto: proto,
		Script: &dsl.Script{
			Name:     "s",
			Protocol: "http",
			Config: dsl.ScriptConfig{
				RunLogic: dsl.RunLogicConfig{Iterations: 2},
				BrowserEmulation: &dsl.BrowserEmulationConfig{
					Cache: &dsl.BrowserCacheSimulationConfig{NewUserEachIteration: &on},
				},
			},
			Actions: []dsl.Action{{Name: "GET /", Type: "request", Method: "GET", Target: "/"}},
		},
		Metrics: NewMetricsCollector(16),
	}

	vu.Run(context.Background())

	if proto.clearCalls != 1 {
		t.Fatalf("ClearCookies calls = %d, want 1 between 2 iterations", proto.clearCalls)
	}
}
