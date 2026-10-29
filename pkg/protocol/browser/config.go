package browser

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/browsercontract"
	"github.com/jasonczech/apexload/pkg/selector"
	playwright "github.com/mxschmitt/playwright-go"
)

const (
	MaxConfiguredViewportDimension = browsercontract.MaxConfiguredViewportDimension
	MaxConfiguredViewportPixels    = browsercontract.MaxConfiguredViewportPixels
	MaxConfiguredBrowserDelay      = browsercontract.MaxConfiguredBrowserDelay
	MaxConfiguredNetworkKbps       = browsercontract.MaxConfiguredNetworkKbps
	maxBrowserDeviceScaleFactor    = 4
	maxBrowserDevicePixels         = 32 << 20
	maxBrowserCPUThrottleRate      = 100
)

func (b *Browser) applyConfig(config map[string]any) error {
	b.replayHAR = protocol.ReplayHARCaptureFromConfig(config)
	b.mu.Lock()
	b.unredactedDebug, _ = config[protocol.ReplayUnredactedDebugConfigKey].(bool)
	b.wsProjectionRevision++
	b.mu.Unlock()
	for _, key := range []string{"browser_capture_document_metrics", "browser_capture_interactions"} {
		if raw, present := config[key]; present {
			if _, valid := raw.(bool); !valid {
				return fmt.Errorf("%s must be boolean", key)
			}
		}
	}
	if raw, present := config["browser_runtime_new_user_policy"]; present {
		policy, typed := raw.(string)
		if !typed || policy != "persistent_context" && policy != "context_each_iteration" && policy != "cookies_each_iteration" && policy != "cookies_and_cache_each_iteration" {
			return fmt.Errorf("browser_runtime_new_user_policy is unsupported")
		}
	}
	if raw, present := config["browser_interaction_evidence_version"]; present {
		value := protocol.ConfigFloat(config, "browser_interaction_evidence_version")
		switch raw.(type) {
		case int, float64:
		default:
			return fmt.Errorf("browser interaction evidence version must be numeric")
		}
		if value != 1 && value != 2 && value != 3 && value != 4 {
			return fmt.Errorf("unsupported browser interaction evidence version")
		}
	}
	if raw, present := config["browser_authored_page_version"]; present {
		value := protocol.ConfigFloat(config, "browser_authored_page_version")
		switch raw.(type) {
		case int, float64:
		default:
			return fmt.Errorf("browser authored page version must be numeric")
		}
		if value != 1 && value != 2 && value != 3 {
			return fmt.Errorf("unsupported browser authored page version")
		}
	}
	b.applyDefaultConfig()
	b.applyConfigOverrides(config)
	if err := b.configureStorageState(config); err != nil {
		return err
	}
	if err := b.configureSalesforceJWTSession(config); err != nil {
		return err
	}
	b.eptCalibration = config["browser_ept_calibration"] == true
	b.systemLanguage = protocol.ConfigString(config, "browser_system_language")
	b.availableScreenWidth = protocol.ConfigInt(config, "browser_available_screen_width")
	b.availableScreenHeight = protocol.ConfigInt(config, "browser_available_screen_height")
	if len(b.systemLanguage) > 64 || strings.ContainsAny(b.systemLanguage, "\r\n\x00") {
		return fmt.Errorf("invalid browser system_language")
	}
	for _, key := range []string{"browser_available_screen_width", "browser_available_screen_height"} {
		if n := protocol.ConfigFloat(config, key); math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n || n < 0 || n > 8192 {
			return fmt.Errorf("%s must be between 0 and 8192", key)
		}
	}

	if err := b.validateConfig(); err != nil {
		return err
	}
	if err := b.applyConfiguredDeviceProfile(); err != nil {
		return err
	}
	b.applyResolvedNetworkProfile()
	if err := b.validateResourceConfig(); err != nil {
		return err
	}
	if b.downloadResources == "recorded" && !b.recordedResourcesAvailable {
		return fmt.Errorf("recorded resource metadata not available")
	}
	return nil
}

func (b *Browser) applyDefaultConfig() {
	b.interactionVersion = protocol.BrowserInteractionVersion
	b.authoredPageVersion = 0
	b.activePageBindings = make(map[playwright.Page]*protocol.BrowserAuthoredPageBinding)
	b.lifecycleRouteSeq = make(map[string]uint64)
	b.storageState = nil
	b.documentProofVersion = protocol.BrowserDocumentReceiptClosureVersion
	b.documentMetricsEnabled = false
	b.newUserPolicy = "persistent_context"
	b.engine = browsercontract.DefaultEngine
	b.baseURL = ""
	b.currentURL = ""
	b.deviceProfile = ""
	b.devicePlatform = ""
	b.deviceTier = ""
	b.headless = browsercontract.DefaultHeadless
	b.cacheEnabled = browsercontract.DefaultCacheEnabled
	b.cacheContentURLs = browsercontract.DefaultCacheContentURLs
	b.cacheContentURLsConfigured = false
	b.checkNewerVersions = false
	b.recordedResourcesAvailable = false
	b.recordedResources = nil
	b.downloadFilterMode = ""
	b.downloadFilterHosts = nil
	b.networkMode = ""
	b.networkPreset = ""
	b.networkBandwidthKB = 0
	b.networkLatencyMS = 0
	b.downloadResources = browsercontract.DefaultDownloadResources
	b.slowMo = 0
	b.userAgent = ""
	b.locale = ""
	b.systemLanguage = ""
	b.availableScreenWidth = 0
	b.availableScreenHeight = 0
	b.timezone = ""
	b.geolocation = nil
	b.geolocationIncomplete = false
	b.httpCredentials = nil
	b.proxy = nil
	b.clientCertificates = nil
	b.captureScreenshots = ""
	b.clientMetricsEnabled = false
	b.autoActivatePopups = false
	b.experienceCanary = false
	b.traceparentEnabled = false
	b.selfHealMode = browserSelfHealReplayOnly
	b.runMode = browserRunModeReplay
	b.mobile = false
	b.touchMode = false
	b.orientation = browsercontract.DefaultOrientation
	b.orientationExplicit = false
	b.deviceScaleFactor = browsercontract.DefaultDeviceScaleFactor
	b.cpuThrottleRate = browsercontract.DefaultCPUThrottleRate
	b.cpuThrottleExplicit = false
	b.viewportW = browsercontract.DefaultViewportWidth
	b.viewportH = browsercontract.DefaultViewportHeight
	b.harCapturePolicy = HARCaptureOff
	b.harDir = ""
	b.harSLABreachThreshold = 0
	b.resourceValidators = make(map[string]string)
	b.resourceValidatorKeys = nil
	b.pendingDialog = nil
	b.dialogErr = nil
	b.selfHealCache = make(map[string]selector.Candidate)
}

func (b *Browser) applyConfigOverrides(config map[string]any) {
	b.documentMetricsEnabled, _ = config["browser_capture_document_metrics"].(bool)
	if _, present := config["browser_document_proof_version"]; present {
		b.documentProofVersion = protocol.ConfigInt(config, "browser_document_proof_version")
	}
	b.interactionsEnabled, _ = config["browser_capture_interactions"].(bool)
	if _, present := config["browser_interaction_evidence_version"]; present {
		b.interactionVersion = protocol.ConfigInt(config, "browser_interaction_evidence_version")
	}
	if _, present := config["browser_authored_page_version"]; present {
		b.authoredPageVersion = protocol.ConfigInt(config, "browser_authored_page_version")
	}
	if policy, present := config["browser_runtime_new_user_policy"].(string); present {
		b.newUserPolicy = policy
	}
	b.allowedTargetHosts = protocol.TargetPolicyHosts(config)
	if baseURL, ok := config["base_url"].(string); ok {
		b.baseURL = strings.TrimSpace(baseURL)
	}
	if engine, ok := config["browser_engine"].(string); ok && strings.TrimSpace(engine) != "" {
		b.engine = strings.ToLower(strings.TrimSpace(engine))
	}
	recorded, _ := config["browser_recorded_version"].(string)
	if recorded = strings.TrimSpace(recorded); !ValidBrowserVersion(recorded) {
		recorded = ""
	}
	identityIndex := protocol.ConfigInt(config, "browser_identity_index")
	identityUsername := strings.TrimSpace(protocol.ConfigString(config, "browser_identity_username"))
	b.mu.Lock()
	b.recordedBrowserVersion = recorded
	b.browserVersionChecked, b.browserVersionSkews = false, nil
	b.identityIndex, b.identityUsername, b.identityReported = identityIndex, identityUsername, false
	b.mu.Unlock()
	if headless, ok := config["browser_headless"].(bool); ok {
		b.headless = headless
	}
	// The script's "HTTP errors as warnings" runtime setting also lets a page
	// load answered with an error status proceed.
	b.httpErrorsAsWarnings, _ = config["http_errors_as_warnings"].(bool)
	b.quic = browserQUICAuto
	if quic, ok := config["browser_quic"].(string); ok && strings.TrimSpace(quic) != "" {
		b.quic = strings.ToLower(strings.TrimSpace(quic))
	}
	if autoActivatePopups, ok := config["browser_auto_activate_popups"].(bool); ok {
		b.autoActivatePopups = autoActivatePopups
	}
	if cacheEnabled, ok := config["browser_cache_enabled"].(bool); ok {
		b.cacheEnabled = cacheEnabled
	}
	if cacheContentURLs, ok := config["browser_cache_content_urls"].(bool); ok {
		b.cacheContentURLs = cacheContentURLs
		b.cacheContentURLsConfigured = true
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
	if networkBandwidthKB := protocol.ConfigInt(config, "web_network_simulation_bandwidth_kbps"); networkBandwidthKB > 0 {
		b.networkBandwidthKB = networkBandwidthKB
	}
	if networkLatencyMS := protocol.ConfigInt(config, "web_network_simulation_latency_ms"); networkLatencyMS > 0 {
		b.networkLatencyMS = networkLatencyMS
	}
	if downloadResources, ok := config["browser_download_resources"].(string); ok && strings.TrimSpace(downloadResources) != "" {
		b.downloadResources = strings.ToLower(strings.TrimSpace(downloadResources))
	}
	if slowMoMs := protocol.ConfigInt(config, "browser_slow_mo_ms"); slowMoMs > 0 {
		b.slowMo = configuredBrowserDuration(slowMoMs)
	}
	if userAgent, ok := config["browser_user_agent"].(string); ok {
		b.userAgent = userAgent
	}
	if locale, ok := config["browser_locale"].(string); ok {
		b.locale = strings.TrimSpace(locale)
	}
	if timezone, ok := config["browser_timezone"].(string); ok {
		b.timezone = strings.TrimSpace(timezone)
	}
	if timezone, ok := config["browser_timezone_id"].(string); ok && strings.TrimSpace(timezone) != "" {
		b.timezone = strings.TrimSpace(timezone)
	}
	if geo, ok, incomplete := geolocationFromConfig(config); ok {
		b.geolocation = geo
		b.geolocationIncomplete = incomplete
	}
	b.httpCredentials = browserHTTPCredentialsFromConfig(config)
	b.proxy = browserProxyFromConfig(config)
	b.clientCertificates = browserClientCertificatesFromConfig(config)
	if touchMode, ok := config["browser_touch_mode"].(bool); ok {
		b.touchMode = touchMode
	}
	if orientation, ok := config["browser_orientation"].(string); ok && strings.TrimSpace(orientation) != "" {
		b.orientation = normalizeOrientation(orientation)
		b.orientationExplicit = true
	}
	if clientMetrics, ok := config["browser_client_metrics"].(bool); ok {
		b.clientMetricsEnabled = clientMetrics
	}
	if experienceCanary, ok := config["browser_experience_canary"].(bool); ok {
		b.experienceCanary = experienceCanary
		if experienceCanary {
			b.clientMetricsEnabled = true
			b.traceparentEnabled = true
		}
	}
	if traceparent, ok := config["browser_traceparent"].(bool); ok {
		b.traceparentEnabled = traceparent
	}
	if selfHeal, ok := config["browser_self_heal"].(string); ok {
		b.selfHealMode = normalizeBrowserSelfHealMode(selfHeal)
	}
	if selfHeal, ok := config["self_heal"].(string); ok {
		b.selfHealMode = normalizeBrowserSelfHealMode(selfHeal)
	}
	for _, key := range []string{"apexload_run_mode", "browser_run_mode", "run_mode"} {
		if runMode, ok := config[key].(string); ok && strings.TrimSpace(runMode) != "" {
			if normalized := normalizeBrowserRunMode(runMode); normalized != "" {
				b.runMode = normalized
			}
		}
	}
	if harCapture, ok := config["browser_har_capture"].(string); ok {
		b.harCapturePolicy = normalizeHARCapturePolicy(harCapture)
	}
	if harCapture, ok := config["browser_har_capture_policy"].(string); ok {
		b.harCapturePolicy = normalizeHARCapturePolicy(harCapture)
	}
	if slaMs := protocol.ConfigInt(config, "browser_har_sla_breach_ms"); slaMs > 0 {
		b.harSLABreachThreshold = configuredBrowserDuration(slaMs)
	}
	if harDir, ok := config["browser_har_dir"].(string); ok && strings.TrimSpace(harDir) != "" {
		b.harDir = strings.TrimSpace(harDir)
	}
	if captureScreenshots, ok := config["browser_capture_screenshots"].(string); ok {
		b.captureScreenshots = captureScreenshots
	}
	if viewportW := protocol.ConfigInt(config, "browser_viewport_width"); viewportW > 0 {
		b.viewportW = viewportW
	}
	if viewportH := protocol.ConfigInt(config, "browser_viewport_height"); viewportH > 0 {
		b.viewportH = viewportH
	}
	if deviceScaleFactor, ok := config["browser_device_scale_factor"].(float64); ok && deviceScaleFactor > 0 {
		b.deviceScaleFactor = deviceScaleFactor
	}
	if cpuThrottleRate, ok := config["browser_cpu_throttle_rate"].(float64); ok && cpuThrottleRate > 0 {
		b.cpuThrottleRate = cpuThrottleRate
		b.cpuThrottleExplicit = true
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
	if b.selfHealMode == "" {
		return fmt.Errorf("unsupported browser self-heal mode")
	}
	if b.timezone != "" {
		if _, err := time.LoadLocation(b.timezone); err != nil {
			return fmt.Errorf("unsupported browser timezone: %s", b.timezone)
		}
	}
	if b.geolocationIncomplete {
		return fmt.Errorf("browser geolocation requires latitude and longitude")
	}
	if err := validatePlaywrightGeolocation(b.geolocation); err != nil {
		return err
	}
	if err := validateBrowserHTTPCredentials(b.httpCredentials); err != nil {
		return err
	}
	if err := validateBrowserProxy(b.proxy); err != nil {
		return err
	}
	switch b.quic {
	case browserQUICAuto:
	case browserQUICOff:
		if b.engine == "webkit" {
			return fmt.Errorf("browser QUIC off is not supported by WebKit")
		}
	default:
		return fmt.Errorf("browser QUIC mode must be auto or off")
	}
	if b.proxy != nil {
		if err := protocol.ValidateTargetAddress(b.proxy.Server, b.allowedTargetHosts); err != nil {
			return err
		}
	}
	if err := validateBrowserClientCertificates(b.clientCertificates); err != nil {
		return err
	}
	if err := materializeBrowserClientCertificates(b.clientCertificates); err != nil {
		return err
	}
	if err := b.validateResourceConfig(); err != nil {
		return err
	}
	return nil
}

func configuredBrowserDuration(milliseconds int) time.Duration {
	if milliseconds > int(MaxConfiguredBrowserDelay/time.Millisecond) {
		// Preserve an invalid sentinel for validateResourceConfig instead of
		// overflowing time.Duration and accidentally turning the delay off.
		return MaxConfiguredBrowserDelay + time.Millisecond
	}
	return time.Duration(milliseconds) * time.Millisecond
}

func (b *Browser) validateResourceConfig() error {
	if b.viewportW <= 0 || b.viewportH <= 0 || b.viewportW > MaxConfiguredViewportDimension || b.viewportH > MaxConfiguredViewportDimension {
		return fmt.Errorf("browser viewport dimensions must be between 1 and %d", MaxConfiguredViewportDimension)
	}
	viewportPixels := float64(b.viewportW) * float64(b.viewportH)
	if viewportPixels > MaxConfiguredViewportPixels {
		return fmt.Errorf("browser viewport exceeds %d-pixel limit", MaxConfiguredViewportPixels)
	}
	if math.IsNaN(b.deviceScaleFactor) || math.IsInf(b.deviceScaleFactor, 0) || b.deviceScaleFactor <= 0 || b.deviceScaleFactor > maxBrowserDeviceScaleFactor {
		return fmt.Errorf("browser device scale factor must be greater than zero and at most %g", float64(maxBrowserDeviceScaleFactor))
	}
	if viewportPixels*b.deviceScaleFactor*b.deviceScaleFactor > maxBrowserDevicePixels {
		return fmt.Errorf("browser scaled viewport exceeds %d-pixel limit", maxBrowserDevicePixels)
	}
	if math.IsNaN(b.cpuThrottleRate) || math.IsInf(b.cpuThrottleRate, 0) || b.cpuThrottleRate <= 0 || b.cpuThrottleRate > maxBrowserCPUThrottleRate {
		return fmt.Errorf("browser CPU throttle rate must be greater than zero and at most %g", float64(maxBrowserCPUThrottleRate))
	}
	if b.slowMo > MaxConfiguredBrowserDelay || b.harSLABreachThreshold > MaxConfiguredBrowserDelay {
		return fmt.Errorf("browser delays must not exceed %s", MaxConfiguredBrowserDelay)
	}
	if b.networkLatencyMS < 0 || b.networkLatencyMS > int(MaxConfiguredBrowserDelay/time.Millisecond) {
		return fmt.Errorf("browser network latency must be between 0 and %d milliseconds", MaxConfiguredBrowserDelay/time.Millisecond)
	}
	if b.networkBandwidthKB < 0 || b.networkBandwidthKB > MaxConfiguredNetworkKbps {
		return fmt.Errorf("browser network bandwidth must be between 0 and %d Kbps", MaxConfiguredNetworkKbps)
	}
	return nil
}

func geolocationFromConfig(config map[string]any) (*playwright.Geolocation, bool, bool) {
	latitude, hasLatitude := configFloatValue(config, "browser_geolocation_latitude")
	longitude, hasLongitude := configFloatValue(config, "browser_geolocation_longitude")
	if !hasLatitude && !hasLongitude {
		return nil, false, false
	}
	geo := &playwright.Geolocation{Latitude: latitude, Longitude: longitude}
	if accuracy, ok := configFloatValue(config, "browser_geolocation_accuracy"); ok {
		geo.Accuracy = playwright.Float(accuracy)
	}
	return geo, true, !hasLatitude || !hasLongitude
}

func configFloatValue(config map[string]any, key string) (float64, bool) {
	if config == nil {
		return 0, false
	}
	value, ok := config[key]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	default:
		return 0, false
	}
}

func validatePlaywrightGeolocation(geo *playwright.Geolocation) error {
	if geo == nil {
		return nil
	}
	if geo.Latitude < -90 || geo.Latitude > 90 {
		return fmt.Errorf("browser geolocation latitude must be between -90 and 90")
	}
	if geo.Longitude < -180 || geo.Longitude > 180 {
		return fmt.Errorf("browser geolocation longitude must be between -180 and 180")
	}
	if geo.Accuracy != nil && *geo.Accuracy < 0 {
		return fmt.Errorf("browser geolocation accuracy must be zero or greater")
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
	if !b.orientationExplicit && strings.TrimSpace(profile.DefaultOrientation) != "" {
		b.orientation = normalizeOrientation(profile.DefaultOrientation)
	}
	if !b.cpuThrottleExplicit && profile.CPUThrottleRate > 0 {
		b.cpuThrottleRate = profile.CPUThrottleRate
	}
	if b.networkMode == "" && profile.NetworkPreset != "" {
		b.networkMode = "preset"
		b.networkPreset = profile.NetworkPreset
	}
	return nil
}

func (b *Browser) applyResolvedNetworkProfile() {
	if b.networkMode != "preset" {
		return
	}
	latencyMS, bandwidthKbps := resolveBrowserNetworkProfile(b.networkPreset, b.networkLatencyMS, b.networkBandwidthKB)
	if b.networkLatencyMS <= 0 {
		b.networkLatencyMS = latencyMS
	}
	if b.networkBandwidthKB <= 0 {
		b.networkBandwidthKB = bandwidthKbps
	}
}

func resolveBrowserNetworkProfile(preset string, latencyMS, bandwidthKbps int) (int, int) {
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "56k":
		return 120, 56
	case "dsl":
		return 50, 1536
	case "t1":
		return 20, 1544
	case "4g", "10mbps":
		return 5, 10240
	}
	return latencyMS, bandwidthKbps
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
