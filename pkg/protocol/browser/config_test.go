package browser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/fileasset"
	playwright "github.com/playwright-community/playwright-go"
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
	if b.httpCredentials != nil || b.proxy != nil || len(b.clientCertificates) != 0 {
		t.Fatalf("auth/proxy/certs defaults = %+v/%+v/%+v, want none", b.httpCredentials, b.proxy, b.clientCertificates)
	}
}

func TestBrowserApplyConfigOverrides(t *testing.T) {
	credentialDir := t.TempDir()
	if err := fileasset.RegisterRoot(credentialDir); err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(credentialDir, "client.pem")
	keyPath := filepath.Join(credentialDir, "client-key.pem")
	if err := os.WriteFile(certPath, []byte("test certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("test key"), 0o600); err != nil {
		t.Fatal(err)
	}
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
		"browser_locale":                        " fr-FR ",
		"browser_timezone":                      " Europe/Paris ",
		"browser_geolocation_latitude":          48.8566,
		"browser_geolocation_longitude":         2.3522,
		"browser_geolocation_accuracy":          12.5,
		"browser_http_credentials_username":     "browser-user",
		"browser_http_credentials_password":     "browser-pass",
		"browser_http_credentials_origin":       "https://secure.example",
		"browser_http_credentials_send":         "always",
		"browser_proxy_server":                  "http://proxy.example:3128",
		"browser_proxy_bypass":                  ".internal,localhost",
		"browser_proxy_username":                "proxy-user",
		"browser_proxy_password":                "proxy-pass",
		"browser_client_certificates": []map[string]any{{
			"origin":     "https://mtls.example",
			"cert_path":  certPath,
			"key_path":   keyPath,
			"passphrase": "cert-pass",
		}},
		"browser_touch_mode":          true,
		"browser_orientation":         " portrait ",
		"browser_client_metrics":      true,
		"browser_experience_canary":   true,
		"browser_har_capture_policy":  "on_sla_breach",
		"browser_har_sla_breach_ms":   float64(1750),
		"browser_capture_screenshots": "per_action",
		"browser_viewport_width":      800,
		"browser_viewport_height":     600,
		"browser_device_scale_factor": float64(2.5),
		"browser_cpu_throttle_rate":   float64(3),
		"browser_device_platform":     "desktop",
		"browser_device_tier":         "mid",
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
	if b.locale != "fr-FR" || b.timezone != "Europe/Paris" || b.geolocation == nil || b.geolocation.Latitude != 48.8566 || b.geolocation.Longitude != 2.3522 || b.geolocation.Accuracy == nil || *b.geolocation.Accuracy != 12.5 {
		t.Fatalf("locale/timezone/geolocation = %q/%q/%+v", b.locale, b.timezone, b.geolocation)
	}
	if b.httpCredentials == nil || b.httpCredentials.Username != "browser-user" || b.httpCredentials.Password != "browser-pass" || b.httpCredentials.Origin == nil || *b.httpCredentials.Origin != "https://secure.example" || b.httpCredentials.Send == nil || string(*b.httpCredentials.Send) != "always" {
		t.Fatalf("http credentials = %+v", b.httpCredentials)
	}
	if b.proxy == nil || b.proxy.Server != "http://proxy.example:3128" || b.proxy.Bypass == nil || *b.proxy.Bypass != ".internal,localhost" || b.proxy.Username == nil || *b.proxy.Username != "proxy-user" || b.proxy.Password == nil || *b.proxy.Password != "proxy-pass" {
		t.Fatalf("proxy = %+v", b.proxy)
	}
	if len(b.clientCertificates) != 1 || b.clientCertificates[0].Origin != "https://mtls.example" || b.clientCertificates[0].CertPath != nil || string(b.clientCertificates[0].Cert) != "test certificate" || b.clientCertificates[0].KeyPath != nil || string(b.clientCertificates[0].Key) != "test key" || b.clientCertificates[0].Passphrase == nil || *b.clientCertificates[0].Passphrase != "cert-pass" {
		t.Fatalf("client certificates = %+v", b.clientCertificates)
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

func TestBrowserClientCertificatePathsAreGovernedAndBounded(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "client.pfx")
	if err := os.WriteFile(outside, []byte("pfx"), 0o600); err != nil {
		t.Fatal(err)
	}
	var browser Browser
	err := browser.applyConfig(map[string]any{"browser_client_certificates": []map[string]any{{
		"origin": "https://mtls.example", "pfx_path": outside,
	}}})
	if err == nil || !strings.Contains(err.Error(), "outside allowed roots") {
		t.Fatalf("outside-root browser certificate error = %v", err)
	}

	dir := t.TempDir()
	if err := fileasset.RegisterRoot(dir); err != nil {
		t.Fatal(err)
	}
	oversized := filepath.Join(dir, "oversized.pfx")
	if err := os.WriteFile(oversized, []byte(strings.Repeat("x", int(fileasset.MaxCredentialBytes)+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	err = browser.applyConfig(map[string]any{"browser_client_certificates": []map[string]any{{
		"origin": "https://mtls.example", "pfx_path": oversized,
	}}})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized browser certificate error = %v", err)
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
	if b.orientation != "portrait" || b.cpuThrottleRate != 1.75 {
		t.Fatalf("profile orientation/cpu = %q/%v, want portrait/1.75", b.orientation, b.cpuThrottleRate)
	}
	if b.networkMode != "preset" || b.networkPreset != "10mbps" || b.networkLatencyMS != 5 || b.networkBandwidthKB != 10240 {
		t.Fatalf("profile network = mode:%q preset:%q latency:%d bandwidth:%d, want 10mbps preset", b.networkMode, b.networkPreset, b.networkLatencyMS, b.networkBandwidthKB)
	}
	if !b.mobile || !b.touchMode || b.userAgent == "" {
		t.Fatalf("profile mobile/touch/ua = %v/%v/%q", b.mobile, b.touchMode, b.userAgent)
	}
}

func TestBrowserApplyConfigDeviceProfilePreservesExplicitOrientationAndCPU(t *testing.T) {
	var b Browser
	if err := b.applyConfig(map[string]any{
		"browser_device_profile":    "pixel_8",
		"browser_orientation":       "landscape",
		"browser_cpu_throttle_rate": float64(3),
	}); err != nil {
		t.Fatalf("applyConfig returned error: %v", err)
	}
	if b.orientation != "landscape" {
		t.Fatalf("orientation = %q, want explicit landscape", b.orientation)
	}
	if b.cpuThrottleRate != 3 {
		t.Fatalf("cpuThrottleRate = %v, want explicit 3", b.cpuThrottleRate)
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
	accuracy := 10.0
	b := Browser{
		viewportW:         900,
		viewportH:         600,
		orientation:       "portrait",
		deviceScaleFactor: 2.5,
		touchMode:         true,
		mobile:            true,
		locale:            "en-GB",
		timezone:          "Europe/London",
		geolocation:       &playwright.Geolocation{Latitude: 51.5, Longitude: -0.12, Accuracy: &accuracy},
		httpCredentials: &playwright.HttpCredentials{
			Username: "browser-user",
			Password: "browser-pass",
		},
		proxy: &playwright.Proxy{
			Server: "http://proxy.example:3128",
		},
		clientCertificates: []playwright.ClientCertificate{{
			Origin:   "https://mtls.example",
			CertPath: playwright.String("/certs/client.pem"),
			KeyPath:  playwright.String("/certs/client-key.pem"),
		}},
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
	if options.Locale == nil || *options.Locale != "en-GB" || options.TimezoneId == nil || *options.TimezoneId != "Europe/London" {
		t.Fatalf("locale/timezone options = %v/%v", options.Locale, options.TimezoneId)
	}
	if options.Geolocation == nil || options.Geolocation.Latitude != 51.5 || options.Geolocation.Longitude != -0.12 {
		t.Fatalf("geolocation options = %+v", options.Geolocation)
	}
	if len(options.Permissions) != 1 || options.Permissions[0] != "geolocation" {
		t.Fatalf("permissions = %v, want [geolocation]", options.Permissions)
	}
	if options.HttpCredentials == nil || options.HttpCredentials.Username != "browser-user" || options.HttpCredentials.Password != "browser-pass" {
		t.Fatalf("http credentials option = %+v", options.HttpCredentials)
	}
	if options.Proxy == nil || options.Proxy.Server != "http://proxy.example:3128" {
		t.Fatalf("proxy option = %+v", options.Proxy)
	}
	if len(options.ClientCertificates) != 1 || options.ClientCertificates[0].Origin != "https://mtls.example" {
		t.Fatalf("client certificates option = %+v", options.ClientCertificates)
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
		{name: "timezone", config: map[string]any{"browser_timezone": "Mars/Olympus"}, wantErr: "unsupported browser timezone"},
		{name: "incomplete geolocation", config: map[string]any{"browser_geolocation_latitude": 0}, wantErr: "browser geolocation requires latitude and longitude"},
		{name: "latitude", config: map[string]any{"browser_geolocation_latitude": 91, "browser_geolocation_longitude": 0}, wantErr: "latitude must be between -90 and 90"},
		{name: "longitude", config: map[string]any{"browser_geolocation_latitude": 0, "browser_geolocation_longitude": -181}, wantErr: "longitude must be between -180 and 180"},
		{name: "accuracy", config: map[string]any{"browser_geolocation_latitude": 0, "browser_geolocation_longitude": 0, "browser_geolocation_accuracy": -1}, wantErr: "accuracy must be zero or greater"},
		{name: "http credentials username", config: map[string]any{"browser_http_credentials_password": "pass"}, wantErr: "browser HTTP credentials require username"},
		{name: "http credentials send", config: map[string]any{"browser_http_credentials_username": "user", "browser_http_credentials_password": "pass", "browser_http_credentials_send": "eager"}, wantErr: "browser HTTP credentials send must be unauthorized or always"},
		{name: "proxy server", config: map[string]any{"browser_proxy_username": "proxy-user"}, wantErr: "browser proxy requires server"},
		{name: "proxy password", config: map[string]any{"browser_proxy_server": "proxy.example:3128", "browser_proxy_password": "proxy-pass"}, wantErr: "browser proxy password requires username"},
		{name: "client certificate origin", config: map[string]any{"browser_client_certificates": []map[string]any{{"origin": "http://mtls.example", "pfx_path": "/certs/client.p12"}}}, wantErr: "origin must be an HTTPS origin"},
		{name: "client certificate material", config: map[string]any{"browser_client_certificates": []map[string]any{{"origin": "https://mtls.example", "cert_path": "/certs/client.pem"}}}, wantErr: "requires cert_path and key_path, or pfx_path"},
		{name: "viewport dimension", config: map[string]any{"browser_viewport_width": MaxConfiguredViewportDimension + 1}, wantErr: "viewport dimensions"},
		{name: "viewport area", config: map[string]any{"browser_viewport_width": MaxConfiguredViewportDimension, "browser_viewport_height": MaxConfiguredViewportDimension}, wantErr: "viewport exceeds"},
		{name: "device scale", config: map[string]any{"browser_device_scale_factor": float64(maxBrowserDeviceScaleFactor + 1)}, wantErr: "device scale factor"},
		{name: "scaled viewport", config: map[string]any{"browser_viewport_width": 4096, "browser_viewport_height": 4096, "browser_device_scale_factor": float64(2)}, wantErr: "scaled viewport"},
		{name: "cpu throttle", config: map[string]any{"browser_cpu_throttle_rate": float64(maxBrowserCPUThrottleRate + 1)}, wantErr: "CPU throttle"},
		{name: "slow motion delay", config: map[string]any{"browser_slow_mo_ms": int(MaxConfiguredBrowserDelay/time.Millisecond) + 1}, wantErr: "delays must not exceed"},
		{name: "network latency", config: map[string]any{"web_network_simulation_latency_ms": int(MaxConfiguredBrowserDelay/time.Millisecond) + 1}, wantErr: "network latency"},
		{name: "network bandwidth", config: map[string]any{"web_network_simulation_bandwidth_kbps": MaxConfiguredNetworkKbps + 1}, wantErr: "network bandwidth"},
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

func TestBrowserApplyDefaultConfigClearsSecurityAndRetentionState(t *testing.T) {
	b := &Browser{
		traceparentEnabled:    true,
		harDir:                "/tmp/previous",
		resourceValidatorKeys: []string{"https://previous.example"},
	}
	b.applyDefaultConfig()
	if b.traceparentEnabled || b.harDir != "" || len(b.resourceValidatorKeys) != 0 {
		t.Fatalf("stale config survived reset: trace=%v harDir=%q validatorKeys=%v", b.traceparentEnabled, b.harDir, b.resourceValidatorKeys)
	}
}
