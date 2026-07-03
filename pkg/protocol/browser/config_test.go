package browser

import (
	"strings"
	"testing"
	"time"
)

func TestBrowserApplyConfigDefaults(t *testing.T) {
	var b Browser
	if err := b.applyConfig(nil); err != nil {
		t.Fatalf("applyConfig returned error: %v", err)
	}

	if b.engine != "chromium" || !b.headless {
		t.Fatalf("engine/headless = %q/%v, want chromium/true", b.engine, b.headless)
	}
	if !b.cacheEnabled || !b.cacheContentURLs || b.checkNewerVersions {
		t.Fatalf("cache defaults = enabled:%v content:%v check:%v", b.cacheEnabled, b.cacheContentURLs, b.checkNewerVersions)
	}
	if b.downloadResources != "all" || b.orientation != "landscape" {
		t.Fatalf("download/orientation = %q/%q, want all/landscape", b.downloadResources, b.orientation)
	}
	if b.viewportW != 1440 || b.viewportH != 900 || b.deviceScaleFactor != 1 || b.cpuThrottleRate != 1 {
		t.Fatalf("viewport/perf defaults = %dx%d scale %v cpu %v", b.viewportW, b.viewportH, b.deviceScaleFactor, b.cpuThrottleRate)
	}
	if b.resourceValidators == nil {
		t.Fatal("resourceValidators = nil, want initialized map")
	}
}

func TestBrowserApplyConfigOverrides(t *testing.T) {
	var b Browser
	err := b.applyConfig(map[string]any{
		"base_url":                              " https://app.example/root ",
		"browser_engine":                        " Chrome ",
		"browser_headless":                      false,
		"browser_cache_enabled":                 false,
		"browser_cache_content_urls":            false,
		"browser_check_newer_versions":          true,
		"browser_recorded_resources_available":  true,
		"browser_recorded_resources":            []string{"https://cdn.example/app.js?x=1"},
		"web_download_filters_mode":             " Allow_List ",
		"web_download_filters_hosts":            []string{" HTTPS://CDN.EXAMPLE/ ", "cdn.example"},
		"web_network_simulation_mode":           " Enabled ",
		"web_network_simulation_preset":         " 4G ",
		"web_network_simulation_bandwidth_kbps": float64(256),
		"web_network_simulation_latency_ms":     int64(75),
		"browser_download_resources":            " Recorded ",
		"browser_slow_mo_ms":                    float64(125),
		"browser_user_agent":                    "CustomUA",
		"browser_touch_mode":                    true,
		"browser_orientation":                   " portrait ",
		"browser_client_metrics":                true,
		"browser_experience_canary":             true,
		"browser_har_capture_policy":            "on_sla_breach",
		"browser_har_sla_breach_ms":             float64(1750),
		"browser_capture_screenshots":           "per_action",
		"browser_viewport_width":                800,
		"browser_viewport_height":               600,
		"browser_device_scale_factor":           float64(2.5),
		"browser_cpu_throttle_rate":             float64(3),
		"browser_device_platform":               "desktop",
		"browser_device_tier":                   "mid",
	})
	if err != nil {
		t.Fatalf("applyConfig returned error: %v", err)
	}

	if b.baseURL != "https://app.example/root" || b.engine != "chrome" || b.headless {
		t.Fatalf("base/engine/headless = %q/%q/%v", b.baseURL, b.engine, b.headless)
	}
	if b.cacheEnabled || b.cacheContentURLs || !b.checkNewerVersions {
		t.Fatalf("cache overrides = enabled:%v content:%v check:%v", b.cacheEnabled, b.cacheContentURLs, b.checkNewerVersions)
	}
	if b.downloadFilterMode != "allow_list" || len(b.downloadFilterHosts) != 1 || b.downloadFilterHosts[0] != "cdn.example" {
		t.Fatalf("download filters = %q/%v, want allow_list/[cdn.example]", b.downloadFilterMode, b.downloadFilterHosts)
	}
	if b.networkMode != "enabled" || b.networkPreset != "4g" || b.networkBandwidthKB != 256 || b.networkLatencyMS != 75 {
		t.Fatalf("network config = %q/%q/%d/%d", b.networkMode, b.networkPreset, b.networkBandwidthKB, b.networkLatencyMS)
	}
	if b.downloadResources != "recorded" || b.slowMo != 125*time.Millisecond || b.userAgent != "CustomUA" {
		t.Fatalf("runtime config = resources:%q slow:%v ua:%q", b.downloadResources, b.slowMo, b.userAgent)
	}
	if !b.touchMode || b.orientation != "portrait" || !b.clientMetricsEnabled || b.captureScreenshots != "per_action" {
		t.Fatalf("interaction config = touch:%v orientation:%q metrics:%v screenshots:%q", b.touchMode, b.orientation, b.clientMetricsEnabled, b.captureScreenshots)
	}
	if !b.experienceCanary || !b.traceparentEnabled || b.harCapturePolicy != HARCaptureOnSLABreach || b.harSLABreachThreshold != 1750*time.Millisecond {
		t.Fatalf("canary config = canary:%v traceparent:%v har:%q threshold:%v", b.experienceCanary, b.traceparentEnabled, b.harCapturePolicy, b.harSLABreachThreshold)
	}
	if b.viewportW != 800 || b.viewportH != 600 || b.deviceScaleFactor != 2.5 || b.cpuThrottleRate != 3 {
		t.Fatalf("viewport/perf config = %dx%d scale %v cpu %v", b.viewportW, b.viewportH, b.deviceScaleFactor, b.cpuThrottleRate)
	}
	if _, ok := b.recordedResources["cdn.example/app.js?x=1"]; !ok {
		t.Fatalf("recordedResources = %v, want host-qualified resource key", b.recordedResources)
	}
}

func TestBrowserApplyConfigDeviceProfile(t *testing.T) {
	var b Browser
	if err := b.applyConfig(map[string]any{"browser_device_profile": "pixel_8"}); err != nil {
		t.Fatalf("applyConfig returned error: %v", err)
	}

	if b.devicePlatform != "android" || b.deviceTier != "high" {
		t.Fatalf("device platform/tier = %q/%q, want android/high", b.devicePlatform, b.deviceTier)
	}
	if b.viewportW != 412 || b.viewportH != 915 || b.deviceScaleFactor != 2.625 {
		t.Fatalf("profile viewport = %dx%d scale %v, want pixel_8 values", b.viewportW, b.viewportH, b.deviceScaleFactor)
	}
	if b.networkMode != "preset" || b.networkPreset != "10mbps" || b.networkLatencyMS != 5 || b.networkBandwidthKB != 10240 {
		t.Fatalf("profile network = mode:%q preset:%q latency:%d bandwidth:%d, want 10mbps preset", b.networkMode, b.networkPreset, b.networkLatencyMS, b.networkBandwidthKB)
	}
	if !b.mobile || !b.touchMode || b.userAgent == "" {
		t.Fatalf("profile mobile/touch/ua = %v/%v/%q", b.mobile, b.touchMode, b.userAgent)
	}
}

func TestBrowserApplyConfigNetworkPreset(t *testing.T) {
	var b Browser
	if err := b.applyConfig(map[string]any{
		"web_network_simulation_mode":   "preset",
		"web_network_simulation_preset": "dsl",
	}); err != nil {
		t.Fatalf("applyConfig returned error: %v", err)
	}
	if b.networkLatencyMS != 50 || b.networkBandwidthKB != 1536 {
		t.Fatalf("network profile = latency:%d bandwidth:%d, want DSL defaults", b.networkLatencyMS, b.networkBandwidthKB)
	}

	var explicit Browser
	if err := explicit.applyConfig(map[string]any{
		"web_network_simulation_mode":           "preset",
		"web_network_simulation_preset":         "dsl",
		"web_network_simulation_latency_ms":     float64(75),
		"web_network_simulation_bandwidth_kbps": float64(2048),
	}); err != nil {
		t.Fatalf("explicit applyConfig returned error: %v", err)
	}
	if explicit.networkLatencyMS != 75 || explicit.networkBandwidthKB != 2048 {
		t.Fatalf("explicit network profile = latency:%d bandwidth:%d, want explicit values preserved", explicit.networkLatencyMS, explicit.networkBandwidthKB)
	}
}

func TestBrowserContextOptionsApplyScaleAndOrientation(t *testing.T) {
	b := Browser{
		viewportW:         900,
		viewportH:         600,
		orientation:       "portrait",
		deviceScaleFactor: 2.5,
		touchMode:         true,
		mobile:            true,
	}
	options := b.browserContextOptions()
	if options.Viewport == nil || options.Viewport.Width != 600 || options.Viewport.Height != 900 {
		t.Fatalf("viewport = %+v, want portrait 600x900", options.Viewport)
	}
	if options.Screen == nil || options.Screen.Width != 600 || options.Screen.Height != 900 {
		t.Fatalf("screen = %+v, want portrait 600x900", options.Screen)
	}
	if options.DeviceScaleFactor == nil || *options.DeviceScaleFactor != 2.5 {
		t.Fatalf("DeviceScaleFactor = %v, want 2.5", options.DeviceScaleFactor)
	}
	if options.HasTouch == nil || !*options.HasTouch || options.IsMobile == nil || !*options.IsMobile {
		t.Fatalf("touch/mobile options = %v/%v, want true/true", options.HasTouch, options.IsMobile)
	}
}

func TestBrowserApplyDeviceProfile(t *testing.T) {
	b := &Browser{}
	b.userAgent = ""
	b.applyDeviceProfile(DeviceProfile{
		Name:               "iphone_se",
		Width:              375,
		Height:             667,
		DeviceScaleFactor:  2,
		Mobile:             true,
		HasTouch:           true,
		DefaultOrientation: "portrait",
		UserAgent:          "MobileUA",
		CPUThrottleRate:    4,
		DeviceTier:         "low",
	})
	if b.deviceProfile != "iphone_se" {
		t.Fatalf("deviceProfile = %q, want iphone_se", b.deviceProfile)
	}
	if b.viewportW != 375 || b.viewportH != 667 {
		t.Fatalf("viewport = %dx%d, want 375x667", b.viewportW, b.viewportH)
	}
	if !b.mobile || !b.touchMode {
		t.Fatalf("mobile/touchMode = %v/%v, want true/true", b.mobile, b.touchMode)
	}
	if b.orientation != "portrait" {
		t.Fatalf("orientation = %q, want portrait", b.orientation)
	}
	if b.userAgent != "MobileUA" {
		t.Fatalf("userAgent = %q, want MobileUA", b.userAgent)
	}
	if b.cpuThrottleRate != 4 {
		t.Fatalf("cpuThrottleRate = %v, want 4", b.cpuThrottleRate)
	}
	if b.deviceTier != "low" {
		t.Fatalf("deviceTier = %q, want low", b.deviceTier)
	}
}

func TestBrowserApplyConfigValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		config  map[string]any
		wantErr string
	}{
		{name: "orientation", config: map[string]any{"browser_orientation": "diagonal"}, wantErr: "unsupported browser orientation"},
		{name: "engine", config: map[string]any{"browser_engine": "netscape"}, wantErr: "unsupported browser engine"},
		{name: "unknown profile", config: map[string]any{"browser_device_profile": "unknown"}, wantErr: "unknown browser device profile"},
		{name: "incompatible profile", config: map[string]any{"browser_device_profile": "iphone_se"}, wantErr: "incompatible device profile"},
		{name: "recorded resources unavailable", config: map[string]any{"browser_download_resources": "recorded"}, wantErr: "recorded resource metadata not available"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b Browser
			err := b.applyConfig(tt.config)
			if err == nil {
				t.Fatal("applyConfig returned nil error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}
