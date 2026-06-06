package browser

import (
	"fmt"
	"strings"
	"time"
)

func (b *Browser) applyConfig(config map[string]any) error {
	b.applyDefaultConfig()
	b.applyConfigOverrides(config)
	if err := b.validateConfig(); err != nil {
		return err
	}
	if err := b.applyConfiguredDeviceProfile(); err != nil {
		return err
	}
	if b.downloadResources == "recorded" && !b.recordedResourcesAvailable {
		return fmt.Errorf("recorded resource metadata not available")
	}
	return nil
}

func (b *Browser) applyDefaultConfig() {
	b.engine = "chromium"
	b.baseURL = ""
	b.currentURL = ""
	b.deviceProfile = ""
	b.devicePlatform = ""
	b.deviceTier = ""
	b.headless = true
	b.cacheEnabled = true
	b.cacheContentURLs = true
	b.checkNewerVersions = false
	b.recordedResourcesAvailable = false
	b.recordedResources = nil
	b.downloadFilterMode = ""
	b.downloadFilterHosts = nil
	b.networkMode = ""
	b.networkPreset = ""
	b.networkBandwidthKB = 0
	b.networkLatencyMS = 0
	b.downloadResources = "all"
	b.slowMo = 0
	b.userAgent = ""
	b.captureScreenshots = ""
	b.clientMetricsEnabled = false
	b.mobile = false
	b.touchMode = false
	b.orientation = "landscape"
	b.deviceScaleFactor = 1
	b.cpuThrottleRate = 1
	b.viewportW = 1440
	b.viewportH = 900
	b.resourceValidators = make(map[string]string)
}

func (b *Browser) applyConfigOverrides(config map[string]any) {
	if baseURL, ok := config["base_url"].(string); ok {
		b.baseURL = strings.TrimSpace(baseURL)
	}
	if engine, ok := config["browser_engine"].(string); ok && strings.TrimSpace(engine) != "" {
		b.engine = strings.ToLower(strings.TrimSpace(engine))
	}
	if headless, ok := config["browser_headless"].(bool); ok {
		b.headless = headless
	}
	if cacheEnabled, ok := config["browser_cache_enabled"].(bool); ok {
		b.cacheEnabled = cacheEnabled
	}
	if cacheContentURLs, ok := config["browser_cache_content_urls"].(bool); ok {
		b.cacheContentURLs = cacheContentURLs
	}
	if checkNewerVersions, ok := config["browser_check_newer_versions"].(bool); ok {
		b.checkNewerVersions = checkNewerVersions
	}
	if recordedResourcesAvailable, ok := config["browser_recorded_resources_available"].(bool); ok {
		b.recordedResourcesAvailable = recordedResourcesAvailable
	}
	if recordedResources, ok := config["browser_recorded_resources"].([]string); ok {
		b.recordedResources = normalizeRecordedResources(recordedResources)
	}
	if downloadFilterMode, ok := config["web_download_filters_mode"].(string); ok {
		b.downloadFilterMode = strings.ToLower(strings.TrimSpace(downloadFilterMode))
	}
	if downloadFilterHosts, ok := config["web_download_filters_hosts"].([]string); ok {
		b.downloadFilterHosts = normalizeDownloadFilterHosts(downloadFilterHosts)
	}
	if networkMode, ok := config["web_network_simulation_mode"].(string); ok {
		b.networkMode = strings.ToLower(strings.TrimSpace(networkMode))
	}
	if networkPreset, ok := config["web_network_simulation_preset"].(string); ok {
		b.networkPreset = strings.ToLower(strings.TrimSpace(networkPreset))
	}
	if networkBandwidthKB := configInt(config, "web_network_simulation_bandwidth_kbps"); networkBandwidthKB > 0 {
		b.networkBandwidthKB = networkBandwidthKB
	}
	if networkLatencyMS := configInt(config, "web_network_simulation_latency_ms"); networkLatencyMS > 0 {
		b.networkLatencyMS = networkLatencyMS
	}
	if downloadResources, ok := config["browser_download_resources"].(string); ok && strings.TrimSpace(downloadResources) != "" {
		b.downloadResources = strings.ToLower(strings.TrimSpace(downloadResources))
	}
	if slowMoMs := configInt(config, "browser_slow_mo_ms"); slowMoMs > 0 {
		b.slowMo = time.Duration(slowMoMs) * time.Millisecond
	}
	if userAgent, ok := config["browser_user_agent"].(string); ok {
		b.userAgent = userAgent
	}
	if touchMode, ok := config["browser_touch_mode"].(bool); ok {
		b.touchMode = touchMode
	}
	if orientation, ok := config["browser_orientation"].(string); ok && strings.TrimSpace(orientation) != "" {
		b.orientation = normalizeOrientation(orientation)
	}
	if clientMetrics, ok := config["browser_client_metrics"].(bool); ok {
		b.clientMetricsEnabled = clientMetrics
	}
	if captureScreenshots, ok := config["browser_capture_screenshots"].(string); ok {
		b.captureScreenshots = captureScreenshots
	}
	if viewportW := configInt(config, "browser_viewport_width"); viewportW > 0 {
		b.viewportW = viewportW
	}
	if viewportH := configInt(config, "browser_viewport_height"); viewportH > 0 {
		b.viewportH = viewportH
	}
	if deviceScaleFactor, ok := config["browser_device_scale_factor"].(float64); ok && deviceScaleFactor > 0 {
		b.deviceScaleFactor = deviceScaleFactor
	}
	if cpuThrottleRate, ok := config["browser_cpu_throttle_rate"].(float64); ok && cpuThrottleRate > 0 {
		b.cpuThrottleRate = cpuThrottleRate
	}
	if deviceProfile, ok := config["browser_device_profile"].(string); ok {
		b.deviceProfile = deviceProfile
	}
	if devicePlatform, ok := config["browser_device_platform"].(string); ok {
		b.devicePlatform = devicePlatform
	}
	if deviceTier, ok := config["browser_device_tier"].(string); ok {
		b.deviceTier = deviceTier
	}
}

func (b *Browser) validateConfig() error {
	if b.orientation == "" {
		return fmt.Errorf("unsupported browser orientation")
	}
	if !supportedBrowserEngine(b.engine) {
		return fmt.Errorf("unsupported browser engine: %s", b.engine)
	}
	return nil
}

func supportedBrowserEngine(engine string) bool {
	switch engine {
	case "chromium", "webkit", "firefox", "chrome":
		return true
	default:
		return false
	}
}

func (b *Browser) applyConfiguredDeviceProfile() error {
	if b.deviceProfile == "" {
		return nil
	}
	profile, ok := LookupDevice(b.deviceProfile)
	if !ok {
		return fmt.Errorf("unknown browser device profile: %s", b.deviceProfile)
	}
	if profile.BrowserFamily != "" && profile.BrowserFamily != b.engine {
		return fmt.Errorf("incompatible device profile %s for engine %s", b.deviceProfile, b.engine)
	}
	b.devicePlatform = profile.Platform
	b.deviceTier = profile.DeviceTier
	if b.userAgent == "" && profile.UserAgent != "" {
		b.userAgent = profile.UserAgent
	}
	if b.viewportW == 1440 && profile.Width > 0 {
		b.viewportW = profile.Width
	}
	if b.viewportH == 900 && profile.Height > 0 {
		b.viewportH = profile.Height
	}
	if b.deviceScaleFactor == 1 && profile.DeviceScaleFactor > 0 {
		b.deviceScaleFactor = profile.DeviceScaleFactor
	}
	if !b.touchMode && profile.HasTouch {
		b.touchMode = true
	}
	if !b.mobile && profile.Mobile {
		b.mobile = true
	}
	return nil
}

func (b *Browser) applyDeviceProfile(profile DeviceProfile) {
	b.deviceProfile = profile.Name
	b.devicePlatform = profile.Platform
	b.deviceTier = profile.DeviceTier
	b.viewportW = profile.Width
	b.viewportH = profile.Height
	b.deviceScaleFactor = profile.DeviceScaleFactor
	b.mobile = profile.Mobile
	b.touchMode = profile.HasTouch
	b.orientation = normalizeOrientation(profile.DefaultOrientation)
	b.cpuThrottleRate = profile.CPUThrottleRate
	if b.userAgent == "" {
		b.userAgent = profile.UserAgent
	}
}

func normalizeOrientation(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "landscape":
		return "landscape"
	case "portrait":
		return "portrait"
	default:
		return ""
	}
}
