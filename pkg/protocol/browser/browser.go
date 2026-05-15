package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/performance"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	playwright "github.com/playwright-community/playwright-go"
	"github.com/jasonczech/stormrunner/pkg/protocol"
)

func init() {
	protocol.Register("browser", func() protocol.Protocol {
		return &Browser{}
	})
}

type Browser struct {
	engine                     string
	baseURL                    string
	currentURL                 string
	deviceProfile              string
	deviceTier                 string
	headless                   bool
	cacheEnabled               bool
	cacheContentURLs           bool
	checkNewerVersions         bool
	recordedResourcesAvailable bool
	recordedResources          map[string]struct{}
	downloadFilterMode         string
	downloadFilterHosts        []string
	networkMode                string
	networkPreset              string
	networkBandwidthKB         int
	networkLatencyMS           int
	downloadResources          string
	slowMo                     time.Duration
	userAgent                  string
	captureScreenshots         string
	clientMetricsEnabled       bool
	mobile                     bool
	touchMode                  bool
	orientation                string
	deviceScaleFactor          float64
	cpuThrottleRate            float64
	viewportW                  int
	viewportH                  int

	allocCtx    context.Context
	browserCtx  context.Context
	allocCancel context.CancelFunc
	browserStop context.CancelFunc
	pw          *playwright.Playwright
	pwBrowser   playwright.Browser
	pwContext   playwright.BrowserContext
	pwPage      playwright.Page

	mu             sync.Mutex
	consoleEntries []protocol.ConsoleEntry
	networkEntries []protocol.NetworkEntry
	pendingNetwork map[network.RequestID]networkRequest
}

type networkRequest struct {
	url          string
	method       string
	resourceType string
	statusCode   int
	startedAt    time.Time
}

type domState struct {
	ReadyState string   `json:"readyState"`
	Visible    []string `json:"visible"`
}

func (b *Browser) Name() string { return "browser" }

func (b *Browser) Init(ctx context.Context, config map[string]any) error {
	b.close()
	b.engine = "chromium"
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
	b.orientation = "landscape"
	b.deviceScaleFactor = 1
	b.cpuThrottleRate = 1
	b.viewportW = 1440
	b.viewportH = 900
	b.pendingNetwork = make(map[network.RequestID]networkRequest)
	b.currentURL = ""
	b.clientMetricsEnabled = false
	b.mobile = false
	b.touchMode = false
	b.deviceProfile = ""
	b.deviceTier = ""
	engineConfigured := false

	if baseURL, ok := config["base_url"].(string); ok {
		b.baseURL = baseURL
	}
	if engine, ok := config["browser_engine"].(string); ok && strings.TrimSpace(engine) != "" {
		b.engine = strings.TrimSpace(engine)
		engineConfigured = true
	}
	if deviceProfile, ok := config["browser_device_profile"].(string); ok && strings.TrimSpace(deviceProfile) != "" {
		profile, found := LookupDevice(strings.TrimSpace(deviceProfile))
		if !found {
			return fmt.Errorf("unknown browser device profile: %s", deviceProfile)
		}
		if profile.BrowserFamily != "" {
			if engineConfigured && b.engine != profile.BrowserFamily {
				return fmt.Errorf("browser engine %s is incompatible with device profile %s (requires %s)", b.engine, profile.Name, profile.BrowserFamily)
			}
			b.engine = profile.BrowserFamily
		}
		b.applyDeviceProfile(profile)
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
		b.downloadFilterMode = strings.TrimSpace(downloadFilterMode)
	}
	if downloadFilterHosts, ok := config["web_download_filters_hosts"].([]string); ok {
		b.downloadFilterHosts = normalizeDownloadFilterHosts(downloadFilterHosts)
	}
	if networkMode, ok := config["web_network_simulation_mode"].(string); ok {
		b.networkMode = strings.TrimSpace(networkMode)
	}
	if networkPreset, ok := config["web_network_simulation_preset"].(string); ok {
		b.networkPreset = strings.TrimSpace(networkPreset)
	}
	if networkBandwidthKB := configInt(config, "web_network_simulation_bandwidth_kbps"); networkBandwidthKB > 0 {
		b.networkBandwidthKB = networkBandwidthKB
	}
	if networkLatencyMS := configInt(config, "web_network_simulation_latency_ms"); networkLatencyMS > 0 {
		b.networkLatencyMS = networkLatencyMS
	}
	if downloadResources, ok := config["browser_download_resources"].(string); ok && strings.TrimSpace(downloadResources) != "" {
		b.downloadResources = strings.TrimSpace(downloadResources)
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
	if b.orientation == "" {
		return fmt.Errorf("unsupported browser orientation")
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
	if b.networkMode == "" && b.networkPreset == "" {
		if profile, ok := LookupDevice(b.deviceProfile); ok && profile.NetworkPreset != "" {
			b.networkMode = "preset"
			b.networkPreset = profile.NetworkPreset
		}
	}
	if b.downloadResources == "recorded" && !b.recordedResourcesAvailable {
		return fmt.Errorf("browser download_resources=recorded requires recorded resource metadata")
	}
	if b.engine != "chromium" && b.engine != "webkit" {
		return fmt.Errorf("unsupported browser engine: %s", b.engine)
	}
	if b.engine == "webkit" {
		return b.initPlaywright(ctx)
	}

	allocatorOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", b.headless),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.NoSandbox,
	)
	if b.userAgent != "" {
		allocatorOpts = append(allocatorOpts, chromedp.UserAgent(b.userAgent))
	}

	b.allocCtx, b.allocCancel = chromedp.NewExecAllocator(ctx, allocatorOpts...)
	b.browserCtx, b.browserStop = chromedp.NewContext(b.allocCtx)
	chromedp.ListenTarget(b.browserCtx, b.handleBrowserEvent)

	startupActions := []chromedp.Action{
		network.Enable(),
		network.SetCacheDisabled(!b.cacheEnabled),
		runtime.Enable(),
		emulation.SetDeviceMetricsOverride(int64(b.viewportW), int64(b.viewportH), b.deviceScaleFactor, b.mobile).
			WithScreenOrientation(&emulation.ScreenOrientation{
				Type:  screenOrientationType(b.orientation),
				Angle: screenOrientationAngle(b.orientation),
			}),
	}
	if b.clientMetricsEnabled {
		startupActions = append(startupActions,
			chromedp.ActionFunc(func(ctx context.Context) error {
				_, err := page.AddScriptToEvaluateOnNewDocument(browserMetricsBootstrapScript).Do(ctx)
				return err
			}),
			performance.Enable(),
		)
	}
	if b.touchMode {
		startupActions = append(startupActions, emulation.SetTouchEmulationEnabled(true).WithMaxTouchPoints(5))
	}
	if b.cpuThrottleRate > 1 {
		startupActions = append(startupActions, emulation.SetCPUThrottlingRate(b.cpuThrottleRate))
	}
	if b.downloadResources == "none" {
		startupActions = append(startupActions, network.SetBlockedURLS(blockedStaticAssetPatterns()))
	}
	if b.checkNewerVersions {
		startupActions = append(startupActions, network.SetExtraHTTPHeaders(network.Headers(map[string]any{
			"Cache-Control": "no-cache",
			"Pragma":        "no-cache",
		})))
	}
	if patterns := b.fetchRequestPatterns(); len(patterns) > 0 {
		startupActions = append(startupActions, fetch.Enable().WithPatterns(patterns))
	}
	if throttleAction := b.networkThrottleAction(); throttleAction != nil {
		startupActions = append(startupActions, throttleAction)
	}
	if err := chromedp.Run(b.browserCtx, startupActions...); err != nil {
		b.close()
		return err
	}

	return nil
}

func (b *Browser) applyDeviceProfile(profile DeviceProfile) {
	b.deviceProfile = profile.Name
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

func screenOrientationType(orientation string) emulation.OrientationType {
	if normalizeOrientation(orientation) == "portrait" {
		return emulation.OrientationTypePortraitPrimary
	}
	return emulation.OrientationTypeLandscapePrimary
}

func screenOrientationAngle(orientation string) int64 {
	if normalizeOrientation(orientation) == "portrait" {
		return 0
	}
	return 90
}

func blockedStaticAssetPatterns() []string {
	return []string{
		"*.png",
		"*.jpg",
		"*.jpeg",
		"*.gif",
		"*.webp",
		"*.svg",
		"*.ico",
		"*.css",
		"*.js",
		"*.woff",
		"*.woff2",
		"*.ttf",
		"*.eot",
		"*.mp3",
		"*.mp4",
		"*.webm",
		"*.avi",
		"*.mov",
	}
}

func assetRequestPatterns() []*fetch.RequestPattern {
	requestStage := fetch.RequestStageRequest
	return []*fetch.RequestPattern{
		{ResourceType: network.ResourceTypeImage, RequestStage: requestStage},
		{ResourceType: network.ResourceTypeStylesheet, RequestStage: requestStage},
		{ResourceType: network.ResourceTypeScript, RequestStage: requestStage},
		{ResourceType: network.ResourceTypeFont, RequestStage: requestStage},
		{ResourceType: network.ResourceTypeMedia, RequestStage: requestStage},
		{ResourceType: network.ResourceTypeOther, RequestStage: requestStage},
	}
}

func contentRequestPatterns() []*fetch.RequestPattern {
	requestStage := fetch.RequestStageRequest
	return []*fetch.RequestPattern{
		{ResourceType: network.ResourceTypeDocument, RequestStage: requestStage},
		{ResourceType: network.ResourceTypeFetch, RequestStage: requestStage},
		{ResourceType: network.ResourceTypeXHR, RequestStage: requestStage},
	}
}

func (b *Browser) fetchRequestPatterns() []*fetch.RequestPattern {
	if !b.hasDownloadFilters() && b.cacheContentURLs && b.downloadResources != "recorded" {
		return nil
	}
	patterns := make([]*fetch.RequestPattern, 0, 9)
	seen := make(map[network.ResourceType]struct{})
	appendUnique := func(items []*fetch.RequestPattern) {
		for _, item := range items {
			if item == nil {
				continue
			}
			if _, ok := seen[item.ResourceType]; ok {
				continue
			}
			seen[item.ResourceType] = struct{}{}
			patterns = append(patterns, item)
		}
	}
	if b.hasDownloadFilters() {
		appendUnique(assetRequestPatterns())
	}
	if b.downloadResources == "recorded" {
		appendUnique(assetRequestPatterns())
	}
	if !b.cacheContentURLs {
		appendUnique(contentRequestPatterns())
	}
	return patterns
}

func normalizeRecordedResources(urls []string) map[string]struct{} {
	normalized := make(map[string]struct{})
	for _, rawURL := range urls {
		for _, key := range recordedResourceKeys(rawURL) {
			normalized[key] = struct{}{}
		}
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func recordedResourceKeys(rawURL string) []string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	if parsed.RawQuery != "" {
		path += "?" + parsed.RawQuery
	}
	if parsed.Host == "" {
		return []string{path}
	}
	return []string{strings.ToLower(parsed.Host) + path, path}
}

func normalizeDownloadFilterHosts(hosts []string) []string {
	normalized := make([]string, 0, len(hosts))
	seen := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		candidate := strings.ToLower(strings.TrimSpace(host))
		candidate = strings.TrimPrefix(candidate, "http://")
		candidate = strings.TrimPrefix(candidate, "https://")
		candidate = strings.TrimRight(candidate, "/")
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		normalized = append(normalized, candidate)
	}
	return normalized
}

func (b *Browser) hasDownloadFilters() bool {
	return (b.downloadFilterMode == "allow_list" || b.downloadFilterMode == "block_list") && len(b.downloadFilterHosts) > 0
}

func (b *Browser) shouldBlockFilteredRequest(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	fullHost := strings.ToLower(parsed.Host)
	hostName := strings.ToLower(parsed.Hostname())
	matched := false
	for _, allowedHost := range b.downloadFilterHosts {
		if allowedHost == fullHost || allowedHost == hostName {
			matched = true
			break
		}
	}
	switch b.downloadFilterMode {
	case "allow_list":
		return !matched
	case "block_list":
		return matched
	default:
		return false
	}
}

func (b *Browser) shouldBlockUnrecordedRequest(rawURL string) bool {
	if b.downloadResources != "recorded" || !b.recordedResourcesAvailable {
		return false
	}
	if len(b.recordedResources) == 0 {
		return true
	}
	for _, key := range recordedResourceKeys(rawURL) {
		if _, ok := b.recordedResources[key]; ok {
			return false
		}
	}
	return true
}

func shouldRevalidateContentRequest(resourceType network.ResourceType) bool {
	switch resourceType {
	case network.ResourceTypeDocument, network.ResourceTypeFetch, network.ResourceTypeXHR:
		return true
	default:
		return false
	}
}

func continuedRequestHeaders(headers network.Headers) []*fetch.HeaderEntry {
	keys := make([]string, 0, len(headers)+2)
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]*fetch.HeaderEntry, 0, len(keys)+2)
	seen := make(map[string]struct{}, len(keys)+2)
	for _, key := range keys {
		lowerKey := strings.ToLower(strings.TrimSpace(key))
		if lowerKey == "cache-control" || lowerKey == "pragma" {
			continue
		}
		entries = append(entries, &fetch.HeaderEntry{Name: key, Value: fmt.Sprint(headers[key])})
		seen[lowerKey] = struct{}{}
	}
	if _, ok := seen["cache-control"]; !ok {
		entries = append(entries, &fetch.HeaderEntry{Name: "Cache-Control", Value: "no-cache"})
	}
	if _, ok := seen["pragma"]; !ok {
		entries = append(entries, &fetch.HeaderEntry{Name: "Pragma", Value: "no-cache"})
	}
	return entries
}

func (b *Browser) networkThrottleAction() chromedp.Action {
	latencyMS, bandwidthKB, connectionType := resolveNetworkProfile(b.networkMode, b.networkPreset, b.networkLatencyMS, b.networkBandwidthKB)
	if latencyMS <= 0 && bandwidthKB <= 0 {
		return nil
	}

	downloadThroughput := -1.0
	uploadThroughput := -1.0
	if bandwidthKB > 0 {
		bytesPerSecond := float64(bandwidthKB*1024) / 8
		downloadThroughput = bytesPerSecond
		uploadThroughput = bytesPerSecond
	}

	action := network.EmulateNetworkConditions(false, float64(latencyMS), downloadThroughput, uploadThroughput)
	if connectionType != "" {
		action = action.WithConnectionType(connectionType)
	}
	return action
}

func resolveNetworkProfile(mode, preset string, latencyMS, bandwidthKB int) (int, int, network.ConnectionType) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "preset":
		switch strings.ToLower(strings.TrimSpace(preset)) {
		case "56k":
			return 120, 56, network.ConnectionTypeCellular2g
		case "dsl":
			return 50, 1536, network.ConnectionTypeCellular3g
		case "t1":
			return 20, 1544, network.ConnectionTypeEthernet
		case "10mbps":
			return 5, 10240, network.ConnectionTypeWifi
		}
	case "custom":
		return latencyMS, bandwidthKB, ""
	}
	return 0, 0, ""
}

func (b *Browser) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	if action == nil {
		return &protocol.Result{
			StartTime:    time.Now(),
			ErrorMessage: "browser action is required",
		}, nil
	}
	result := &protocol.Result{
		ActionName:  action.Name,
		StartTime:   time.Now(),
		Extractions: map[string]string{},
	}
	if !b.initialized() {
		result.ErrorMessage = "browser protocol is not initialized"
		return result, nil
	}

	consoleStart, networkStart := b.eventOffsets()
	actionCtx, cancel := b.executionContext(ctx)
	defer cancel()

	var actionErr error
	switch action.Type {
	case "browser_navigate":
		if action.Target == "" {
			result.ErrorMessage = "browser_navigate requires target"
			result.Duration = time.Since(result.StartTime)
			return result, nil
		}
		actionErr = b.navigate(actionCtx, b.resolveURL(action.Target))
	case "browser_wait":
		actionErr = b.runWait(actionCtx, action)
	case "browser_click":
		selector := b.actionSelector(action)
		if selector == "" {
			result.ErrorMessage = "browser_click requires selector or target"
			result.Duration = time.Since(result.StartTime)
			return result, nil
		}
		if b.touchMode {
			actionErr = b.dispatchTap(actionCtx, selector)
		} else {
			actionErr = b.click(actionCtx, selector)
		}
	case "browser_tap":
		selector := b.actionSelector(action)
		if selector == "" {
			result.ErrorMessage = "browser_tap requires selector or target"
			result.Duration = time.Since(result.StartTime)
			return result, nil
		}
		actionErr = b.dispatchTap(actionCtx, selector)
	case "browser_long_press":
		selector := b.actionSelector(action)
		if selector == "" {
			result.ErrorMessage = "browser_long_press requires selector or target"
			result.Duration = time.Since(result.StartTime)
			return result, nil
		}
		durationMS := actionParamInt(action.Params, "duration_ms", 500)
		if durationMS <= 0 {
			durationMS = 500
		}
		actionErr = b.dispatchLongPress(actionCtx, selector, time.Duration(durationMS)*time.Millisecond)
	case "browser_swipe":
		selector := b.actionSelector(action)
		if selector == "" {
			result.ErrorMessage = "browser_swipe requires selector or target"
			result.Duration = time.Since(result.StartTime)
			return result, nil
		}
		direction := actionParamString(action.Params, "direction", "left")
		distance := actionParamInt(action.Params, "distance", 300)
		if distance <= 0 {
			distance = 300
		}
		actionErr = b.dispatchSwipe(actionCtx, selector, direction, distance)
	case "browser_fill":
		selector := b.actionSelector(action)
		if selector == "" {
			result.ErrorMessage = "browser_fill requires selector or target"
			result.Duration = time.Since(result.StartTime)
			return result, nil
		}
		if action.Value == "" {
			result.ErrorMessage = "browser_fill requires value"
			result.Duration = time.Since(result.StartTime)
			return result, nil
		}
		actionErr = b.fill(actionCtx, selector, action.Value)
	case "browser_screenshot":
		// Capture is handled with the rest of the page state below.
	default:
		result.ErrorMessage = fmt.Sprintf("unsupported browser action type: %s", action.Type)
		result.Duration = time.Since(result.StartTime)
		return result, nil
	}

	if actionErr == nil {
		actionErr = b.afterActionDelay(actionCtx)
	}

	pageURL, pageTitle, domSummary, html, screenshot, captureErr := b.capturePageState(actionCtx, action, actionErr != nil)
	if pageURL != "" {
		b.currentURL = pageURL
		result.PageURL = pageURL
	}
	result.PageTitle = pageTitle
	result.DOMSummary = domSummary
	result.HTML = html
	result.ScreenshotBase64 = screenshot
	result.ConsoleEntries = b.consoleEntriesSince(consoleStart)
	result.NetworkEntries = b.networkEntriesSince(networkStart)
	result.Duration = time.Since(result.StartTime)
	result.ClientMetrics = b.captureClientMetrics(actionCtx, result.Duration)

	if actionErr != nil {
		result.ErrorMessage = actionErr.Error()
		result.Snapshot = &protocol.ErrorSnapshot{
			PageURL:          result.PageURL,
			PageTitle:        result.PageTitle,
			HTML:             result.HTML,
			ScreenshotBase64: result.ScreenshotBase64,
			ConsoleEntries:   cloneConsoleEntries(result.ConsoleEntries),
			NetworkEntries:   cloneNetworkEntries(result.NetworkEntries),
			DOMSummary:       cloneDOMSummary(result.DOMSummary),
		}
		return result, nil
	}
	if captureErr != nil {
		result.ErrorMessage = captureErr.Error()
		return result, nil
	}

	result.Success = true
	return result, nil
}

func (b *Browser) Teardown(_ context.Context) error {
	b.close()
	return nil
}

func (b *Browser) HealthCheck(_ context.Context) error {
	return nil
}

func (b *Browser) resolveURL(target string) string {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return target
	}
	if b.baseURL == "" {
		return target
	}
	return strings.TrimRight(b.baseURL, "/") + "/" + strings.TrimLeft(target, "/")
}

func (b *Browser) runWait(ctx context.Context, action *protocol.Action) error {
	if action.WaitFor != nil {
		waitValue := action.WaitFor.Value
		if waitValue == "" {
			waitValue = b.actionSelector(action)
		}
		waitCtx, cancel := context.WithTimeout(ctx, b.waitTimeout(action.WaitFor.TimeoutMs))
		defer cancel()

		switch strings.ToLower(action.WaitFor.Type) {
		case "", "selector", "visible", "ready":
			if waitValue == "" {
				return fmt.Errorf("browser_wait requires selector, target, or wait_for value")
			}
			return b.waitVisible(waitCtx, waitValue)
		case "hidden":
			if waitValue == "" {
				return fmt.Errorf("browser_wait hidden requires selector or wait_for value")
			}
			return b.waitHidden(waitCtx, waitValue)
		case "text":
			if waitValue == "" {
				return fmt.Errorf("browser_wait text requires wait_for value")
			}
			return b.waitForJS(waitCtx, fmt.Sprintf(`document.body && document.body.innerText.includes(%q)`, waitValue))
		case "url_contains":
			if waitValue == "" {
				return fmt.Errorf("browser_wait url_contains requires wait_for value")
			}
			return b.waitForJS(waitCtx, fmt.Sprintf(`window.location.href.includes(%q)`, waitValue))
		case "duration", "sleep":
			delay, err := parseWaitDelay(waitValue)
			if err != nil {
				return err
			}
			return b.sleep(waitCtx, delay)
		default:
			if waitValue == "" {
				return fmt.Errorf("browser_wait requires selector, target, or wait_for value")
			}
			return chromedp.Run(waitCtx, chromedp.WaitVisible(waitValue, chromedp.ByQuery))
		}
	}

	selector := b.actionSelector(action)
	if selector != "" {
		waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return b.waitVisible(waitCtx, selector)
	}
	if action.Target == "" {
		return fmt.Errorf("browser_wait requires wait_for, selector, or target")
	}
	delay, err := parseWaitDelay(action.Target)
	if err != nil {
		return chromedp.Run(ctx, chromedp.WaitVisible(action.Target, chromedp.ByQuery))
	}
	return b.sleep(ctx, delay)
}

func (b *Browser) actionSelector(action *protocol.Action) string {
	if action.Selector != "" {
		return action.Selector
	}
	return action.Target
}

func (b *Browser) dispatchTap(ctx context.Context, selector string) error {
	return b.runTouchScript(ctx, selector, `
		dispatchPointer('pointerdown', x, y);
		dispatchTouch('touchstart', x, y, true);
		dispatchTouch('touchend', x, y, false);
		dispatchPointer('pointerup', x, y);
		dispatchClick(x, y);
	`, "browser tap")
}

func (b *Browser) dispatchLongPress(ctx context.Context, selector string, duration time.Duration) error {
	if err := b.runTouchScript(ctx, selector, `
		dispatchPointer('pointerdown', x, y);
		dispatchTouch('touchstart', x, y, true);
	`, "browser long press start"); err != nil {
		return err
	}
	if err := b.sleep(ctx, duration); err != nil {
		return err
	}
	return b.runTouchScript(ctx, selector, `
		dispatchTouch('touchend', x, y, false);
		dispatchPointer('pointerup', x, y);
		dispatchClick(x, y);
	`, "browser long press end")
}

func (b *Browser) dispatchSwipe(ctx context.Context, selector, direction string, distance int) error {
	return b.runTouchScript(ctx, selector, fmt.Sprintf(`
		const startX = x;
		const startY = y;
		let endX = startX;
		let endY = startY;
		switch (%q) {
		case 'right':
			endX = startX + %d;
			break;
		case 'up':
			endY = startY - %d;
			break;
		case 'down':
			endY = startY + %d;
			break;
		default:
			endX = startX - %d;
			break;
		}
		dispatchPointer('pointerdown', startX, startY);
		dispatchTouch('touchstart', startX, startY, true);
		const steps = 5;
		for (let i = 1; i <= steps; i++) {
			const nextX = startX + ((endX - startX) * i / steps);
			const nextY = startY + ((endY - startY) * i / steps);
			dispatchPointer('pointermove', nextX, nextY);
			dispatchTouch('touchmove', nextX, nextY, true);
		}
		dispatchTouch('touchend', endX, endY, false);
		dispatchPointer('pointerup', endX, endY);
	`, strings.ToLower(strings.TrimSpace(direction)), distance, distance, distance, distance), "browser swipe")
}

func (b *Browser) runTouchScript(ctx context.Context, selector, operation, label string) error {
	if err := b.waitVisible(ctx, selector); err != nil {
		return err
	}

	var evalErr string
	expression := fmt.Sprintf(`(() => {
		const el = document.querySelector(%q);
		if (!el) {
			return 'selector not found';
		}
		el.scrollIntoView({ block: 'center', inline: 'center' });
		const rect = el.getBoundingClientRect();
		const x = rect.left + (rect.width / 2);
		const y = rect.top + (rect.height / 2);
		const makeTouchList = (clientX, clientY, active) => {
			const touchLike = {
				identifier: Date.now(),
				target: el,
				clientX,
				clientY,
				pageX: clientX + window.scrollX,
				pageY: clientY + window.scrollY,
				screenX: clientX,
				screenY: clientY,
				radiusX: 2,
				radiusY: 2,
				rotationAngle: 0,
				force: 1,
			};
			if (typeof Touch === 'function') {
				const touch = new Touch(touchLike);
				return { changed: [touch], active: active ? [touch] : [] };
			}
			return { changed: [touchLike], active: active ? [touchLike] : [] };
		};
		const dispatchTouch = (type, clientX, clientY, active) => {
			const list = makeTouchList(clientX, clientY, active);
			let event;
			if (typeof TouchEvent === 'function') {
				event = new TouchEvent(type, {
					bubbles: true,
					cancelable: true,
					composed: true,
					touches: list.active,
					targetTouches: list.active,
					changedTouches: list.changed,
				});
			} else {
				event = new Event(type, { bubbles: true, cancelable: true, composed: true });
				Object.defineProperty(event, 'touches', { value: list.active });
				Object.defineProperty(event, 'targetTouches', { value: list.active });
				Object.defineProperty(event, 'changedTouches', { value: list.changed });
			}
			el.dispatchEvent(event);
		};
		const dispatchPointer = (type, clientX, clientY) => {
			if (typeof PointerEvent !== 'function') {
				return;
			}
			el.dispatchEvent(new PointerEvent(type, {
				bubbles: true,
				cancelable: true,
				composed: true,
				pointerType: 'touch',
				isPrimary: true,
				clientX,
				clientY,
			}));
		};
		const dispatchClick = (clientX, clientY) => {
			el.dispatchEvent(new MouseEvent('click', {
			bubbles: true,
			cancelable: true,
			composed: true,
			clientX,
			clientY,
		}));
		};
		%s
		return '';
	})()`, selector, operation)
	if err := b.evaluatePageExpression(ctx, expression, &evalErr); err != nil {
		return err
	}
	if evalErr != "" {
		return fmt.Errorf("%s failed: %s", label, evalErr)
	}
	return nil
}

func actionParamInt(params map[string]any, key string, defaultValue int) int {
	if params == nil {
		return defaultValue
	}
	value, ok := params[key]
	if !ok {
		return defaultValue
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case float32:
		return int(typed)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return defaultValue
}

func actionParamString(params map[string]any, key, defaultValue string) string {
	if params == nil {
		return defaultValue
	}
	value, ok := params[key]
	if !ok {
		return defaultValue
	}
	if typed, ok := value.(string); ok && strings.TrimSpace(typed) != "" {
		return strings.TrimSpace(typed)
	}
	return defaultValue
}

func (b *Browser) captureRequestedArtifacts(ctx context.Context, action *protocol.Action) (string, string, error) {
	captureEveryAction := b.captureScreenshots == "per_action"
	if action.Type != "browser_screenshot" && action.Capture == nil && !captureEveryAction {
		return "", "", nil
	}

	var html string
	var screenshot string
	var err error
	if captureEveryAction || action.Type == "browser_screenshot" || (action.Capture != nil && action.Capture.Screenshot) {
		screenshot, err = b.captureScreenshot(ctx, action)
		if err != nil {
			return "", "", err
		}
	}
	if action.Capture != nil && action.Capture.HTML {
		html, err = b.captureHTML(ctx)
		if err != nil {
			return "", "", err
		}
	}
	return html, screenshot, nil
}

func (b *Browser) capturePageState(ctx context.Context, action *protocol.Action, forceArtifacts bool) (string, string, *protocol.DOMSummary, string, string, error) {
	pageURL, pageTitle, domSummary, err := b.readDOMSummary(ctx)
	if err != nil {
		return "", "", nil, "", "", err
	}

	html, screenshot, err := b.captureRequestedArtifacts(ctx, action)
	if err != nil {
		return pageURL, pageTitle, domSummary, html, screenshot, err
	}
	if forceArtifacts {
		if html == "" {
			html, _ = b.captureHTML(ctx)
		}
		if screenshot == "" {
			screenshot, _ = b.captureScreenshot(ctx, action)
		}
	}
	return pageURL, pageTitle, domSummary, html, screenshot, nil
}

type browserPageMetrics struct {
	FCPMs                 float64 `json:"fcp_ms"`
	LCPMs                 float64 `json:"lcp_ms"`
	CLSScore              float64 `json:"cls_score"`
	INPMs                 float64 `json:"inp_ms"`
	DOMContentLoadedMs    float64 `json:"dom_content_loaded_ms"`
	DOMCompleteMs         float64 `json:"dom_complete_ms"`
	LoadEventMs           float64 `json:"load_event_ms"`
	BlockingResourceCount int     `json:"blocking_resource_count"`
	TotalResourceCount    int     `json:"total_resource_count"`
	ResourceTransferKB    float64 `json:"resource_transfer_kb"`
	LongTaskCount         int     `json:"long_task_count"`
}

func (b *Browser) captureClientMetrics(ctx context.Context, duration time.Duration) *protocol.ClientMetrics {
	if !b.clientMetricsEnabled {
		return nil
	}

	metrics := &protocol.ClientMetrics{
		DeviceProfile:    b.deviceProfile,
		DeviceTier:       b.deviceTier,
		BrowserEngine:    b.engine,
		ActionDurationMs: float64(duration) / float64(time.Millisecond),
	}

	if err := b.capturePageMetrics(ctx, metrics); err != nil {
		// Keep metrics collection non-fatal for the browser action.
	}
	if err := b.captureRuntimeMetrics(ctx, metrics); err != nil {
		// Keep metrics collection non-fatal for the browser action.
	}

	if *metrics == (protocol.ClientMetrics{ActionDurationMs: metrics.ActionDurationMs}) {
		return metrics
	}
	return metrics
}

func (b *Browser) capturePageMetrics(ctx context.Context, metrics *protocol.ClientMetrics) error {
	var snapshot browserPageMetrics
	expression := `(() => {
		const paints = performance.getEntriesByType('paint');
		const fcp = paints.find((entry) => entry.name === 'first-contentful-paint');
		const nav = performance.getEntriesByType('navigation')[0] || {};
		const resources = performance.getEntriesByType('resource');
		const lcpEntries = performance.getEntriesByType('largest-contentful-paint');
		const lcp = lcpEntries.length ? lcpEntries[lcpEntries.length - 1].startTime : 0;
		const shifts = performance.getEntriesByType('layout-shift');
		const cls = shifts.reduce((sum, entry) => sum + (entry.hadRecentInput ? 0 : (entry.value || 0)), 0);
		const eventEntries = performance.getEntriesByType('event');
		const inp = eventEntries.reduce((max, entry) => Math.max(max, entry.duration || entry.processingEnd || 0), 0);
		const longTasks = performance.getEntriesByType('longtask');
		const observerMetrics = window.__stormrunnerMetrics || {};
		const blocking = resources.filter((entry) => entry.renderBlockingStatus === 'blocking');
		return {
			fcp_ms: fcp ? fcp.startTime : 0,
			lcp_ms: observerMetrics.lcp || lcp,
			cls_score: observerMetrics.cls || cls,
			inp_ms: observerMetrics.inp || inp,
			dom_content_loaded_ms: nav.domContentLoadedEventEnd || 0,
			dom_complete_ms: nav.domComplete || 0,
			load_event_ms: nav.loadEventEnd || 0,
			blocking_resource_count: blocking.length,
			total_resource_count: resources.length,
			resource_transfer_kb: resources.reduce((sum, entry) => sum + ((entry.transferSize || 0) / 1024), 0),
			long_task_count: observerMetrics.longTaskCount || longTasks.length,
		};
	})()`
	if err := b.evaluatePageExpression(ctx, expression, &snapshot); err != nil {
		return err
	}
	metrics.FCPMs = snapshot.FCPMs
	metrics.LCPMs = snapshot.LCPMs
	metrics.CLSScore = snapshot.CLSScore
	metrics.INPMs = snapshot.INPMs
	metrics.DOMContentLoadedMs = snapshot.DOMContentLoadedMs
	metrics.DOMCompleteMs = snapshot.DOMCompleteMs
	metrics.LoadEventMs = snapshot.LoadEventMs
	metrics.BlockingResourceCount = snapshot.BlockingResourceCount
	metrics.TotalResourceCount = snapshot.TotalResourceCount
	metrics.ResourceTransferKB = snapshot.ResourceTransferKB
	metrics.LongTaskCount = snapshot.LongTaskCount
	return nil
}

const browserMetricsBootstrapScript = `(() => {
	if (window.__stormrunnerMetrics) {
		return;
	}
	window.__stormrunnerMetrics = {
		lcp: 0,
		cls: 0,
		inp: 0,
		longTaskCount: 0,
	};
	try {
		new PerformanceObserver((list) => {
			for (const entry of list.getEntries()) {
				window.__stormrunnerMetrics.lcp = entry.startTime || window.__stormrunnerMetrics.lcp;
			}
		}).observe({ type: 'largest-contentful-paint', buffered: true });
	} catch (e) {}
	try {
		new PerformanceObserver((list) => {
			for (const entry of list.getEntries()) {
				if (!entry.hadRecentInput) {
					window.__stormrunnerMetrics.cls += entry.value || 0;
				}
			}
		}).observe({ type: 'layout-shift', buffered: true });
	} catch (e) {}
	try {
		new PerformanceObserver((list) => {
			window.__stormrunnerMetrics.longTaskCount += list.getEntries().length;
		}).observe({ type: 'longtask', buffered: true });
	} catch (e) {}
	try {
		new PerformanceObserver((list) => {
			for (const entry of list.getEntries()) {
				const duration = entry.duration || entry.processingEnd || 0;
				if (duration > window.__stormrunnerMetrics.inp) {
					window.__stormrunnerMetrics.inp = duration;
				}
			}
		}).observe({ type: 'event', buffered: true, durationThreshold: 16 });
	} catch (e) {}
})();`

func (b *Browser) captureRuntimeMetrics(ctx context.Context, metrics *protocol.ClientMetrics) error {
	if b.engine != "chromium" {
		return nil
	}
	var perfMetrics []*performance.Metric
	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		perfMetrics, err = performance.GetMetrics().Do(ctx)
		return err
	})); err != nil {
		return err
	}
	for _, metric := range perfMetrics {
		switch metric.Name {
		case "JSHeapUsedSize":
			metrics.JSHeapUsedMB = metric.Value / (1024 * 1024)
		case "JSHeapTotalSize":
			metrics.JSHeapTotalMB = metric.Value / (1024 * 1024)
		case "LayoutCount":
			metrics.LayoutCount = int(metric.Value)
		case "RecalcStyleCount":
			metrics.RecalcStyleCount = int(metric.Value)
		case "ScriptDuration":
			metrics.ScriptDurationMs = metric.Value * 1000
		case "LayoutDuration":
			metrics.LayoutDurationMs = metric.Value * 1000
		case "TaskDuration":
			metrics.TaskDurationMs = metric.Value * 1000
		}
	}
	return nil
}

func (b *Browser) readDOMSummary(ctx context.Context) (string, string, *protocol.DOMSummary, error) {
	if b.engine == "webkit" {
		return b.readPlaywrightDOMSummary(ctx)
	}
	var pageURL string
	var title string
	var state domState
	if err := chromedp.Run(ctx,
		chromedp.Location(&pageURL),
		chromedp.Title(&title),
		chromedp.Evaluate(`(() => ({ readyState: document.readyState, visible: (document.body?.innerText || '').split(/\n+/).map((line) => line.trim()).filter(Boolean).slice(0, 10) }))()`, &state),
	); err != nil {
		return "", "", nil, err
	}
	return pageURL, title, &protocol.DOMSummary{
		Title:       title,
		ReadyState:  state.ReadyState,
		VisibleText: state.Visible,
	}, nil
}

func configInt(config map[string]any, key string) int {
	switch value := config[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case float32:
		return int(value)
	default:
		return 0
	}
}

func (b *Browser) captureHTML(ctx context.Context) (string, error) {
	if b.engine == "webkit" {
		return b.capturePlaywrightHTML(ctx)
	}
	var html string
	err := chromedp.Run(ctx, chromedp.OuterHTML("html", &html, chromedp.ByQuery))
	return html, err
}

func (b *Browser) captureScreenshot(ctx context.Context, action *protocol.Action) (string, error) {
	if b.engine == "webkit" {
		return b.capturePlaywrightScreenshot(ctx, action)
	}
	var buf []byte
	selector := b.screenshotSelector(action)
	if selector != "" {
		if err := chromedp.Run(ctx, chromedp.Screenshot(selector, &buf, chromedp.ByQuery)); err != nil {
			return "", err
		}
	} else if action.Capture != nil && action.Capture.FullPage {
		if err := chromedp.Run(ctx, chromedp.FullScreenshot(&buf, 90)); err != nil {
			return "", err
		}
	} else {
		if err := chromedp.Run(ctx, chromedp.CaptureScreenshot(&buf)); err != nil {
			return "", err
		}
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

func (b *Browser) screenshotSelector(action *protocol.Action) string {
	if action == nil {
		return ""
	}
	if action.Selector != "" {
		return action.Selector
	}
	switch action.Type {
	case "browser_click", "browser_fill", "browser_wait", "browser_screenshot":
		return action.Target
	default:
		return ""
	}
}

func (b *Browser) waitForJS(ctx context.Context, expression string) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		var ready bool
		if err := b.evaluatePageExpression(ctx, expression, &ready); err == nil && ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (b *Browser) afterActionDelay(ctx context.Context) error {
	if b.slowMo <= 0 {
		return nil
	}
	return b.sleep(ctx, b.slowMo)
}

func (b *Browser) sleep(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (b *Browser) executionContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if b.engine == "webkit" {
		return context.WithCancel(ctx)
	}
	actionCtx, cancel := context.WithCancel(b.browserCtx)
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-actionCtx.Done():
		}
	}()
	return actionCtx, cancel
}

func (b *Browser) eventOffsets() (int, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.consoleEntries), len(b.networkEntries)
}

func (b *Browser) consoleEntriesSince(offset int) []protocol.ConsoleEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	if offset <= 0 {
		return cloneConsoleEntries(b.consoleEntries)
	}
	if offset >= len(b.consoleEntries) {
		return nil
	}
	return cloneConsoleEntries(b.consoleEntries[offset:])
}

func (b *Browser) networkEntriesSince(offset int) []protocol.NetworkEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	if offset <= 0 {
		return cloneNetworkEntries(b.networkEntries)
	}
	if offset >= len(b.networkEntries) {
		return nil
	}
	return cloneNetworkEntries(b.networkEntries[offset:])
}

func (b *Browser) handleBrowserEvent(ev any) {
	if event, ok := ev.(*fetch.EventRequestPaused); ok {
		go b.handleInterceptedRequest(event)
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	switch event := ev.(type) {
	case *runtime.EventConsoleAPICalled:
		parts := make([]string, 0, len(event.Args))
		for _, arg := range event.Args {
			parts = append(parts, remoteObjectText(arg))
		}
		b.consoleEntries = append(b.consoleEntries, protocol.ConsoleEntry{
			Level:     string(event.Type),
			Text:      strings.TrimSpace(strings.Join(parts, " ")),
			Timestamp: time.Now(),
		})
	case *runtime.EventExceptionThrown:
		b.consoleEntries = append(b.consoleEntries, protocol.ConsoleEntry{
			Level:     "error",
			Text:      event.ExceptionDetails.Text,
			Source:    event.ExceptionDetails.URL,
			Timestamp: time.Now(),
		})
	case *network.EventRequestWillBeSent:
		b.pendingNetwork[event.RequestID] = networkRequest{
			url:          event.Request.URL,
			method:       event.Request.Method,
			resourceType: string(event.Type),
			startedAt:    time.Now(),
		}
	case *network.EventResponseReceived:
		pending := b.pendingNetwork[event.RequestID]
		pending.statusCode = int(event.Response.Status)
		if pending.resourceType == "" {
			pending.resourceType = string(event.Type)
		}
		b.pendingNetwork[event.RequestID] = pending
	case *network.EventLoadingFinished:
		pending, ok := b.pendingNetwork[event.RequestID]
		if !ok {
			return
		}
		b.networkEntries = append(b.networkEntries, protocol.NetworkEntry{
			URL:           pending.url,
			Method:        pending.method,
			StatusCode:    pending.statusCode,
			ResourceType:  pending.resourceType,
			DurationMs:    float64(time.Since(pending.startedAt).Microseconds()) / 1000,
			BytesReceived: int64(event.EncodedDataLength),
		})
		delete(b.pendingNetwork, event.RequestID)
	case *network.EventLoadingFailed:
		pending, ok := b.pendingNetwork[event.RequestID]
		if !ok {
			return
		}
		b.networkEntries = append(b.networkEntries, protocol.NetworkEntry{
			URL:          pending.url,
			Method:       pending.method,
			StatusCode:   pending.statusCode,
			ResourceType: pending.resourceType,
			DurationMs:   float64(time.Since(pending.startedAt).Microseconds()) / 1000,
		})
		delete(b.pendingNetwork, event.RequestID)
	}
}

func (b *Browser) handleInterceptedRequest(event *fetch.EventRequestPaused) {
	if event == nil || event.Request == nil || b.browserCtx == nil {
		return
	}
	if b.shouldBlockUnrecordedRequest(event.Request.URL) {
		_ = fetch.FailRequest(event.RequestID, network.ErrorReasonBlockedByClient).Do(b.browserCtx)
		return
	}
	if b.hasDownloadFilters() && b.shouldBlockFilteredRequest(event.Request.URL) {
		_ = fetch.FailRequest(event.RequestID, network.ErrorReasonBlockedByClient).Do(b.browserCtx)
		return
	}
	continueRequest := fetch.ContinueRequest(event.RequestID)
	if !b.cacheContentURLs && shouldRevalidateContentRequest(event.ResourceType) {
		continueRequest = continueRequest.WithHeaders(continuedRequestHeaders(event.Request.Headers))
	}
	_ = continueRequest.Do(b.browserCtx)
}

func (b *Browser) close() {
	b.closePlaywright()
	if b.browserStop != nil {
		b.browserStop()
		b.browserStop = nil
	}
	if b.allocCancel != nil {
		b.allocCancel()
		b.allocCancel = nil
	}
	b.currentURL = ""
	b.browserCtx = nil
	b.allocCtx = nil
	b.slowMo = 0
	b.userAgent = ""
	b.captureScreenshots = ""
	b.pendingNetwork = nil
	b.consoleEntries = nil
	b.networkEntries = nil
}

func (b *Browser) waitTimeout(timeoutMs int) time.Duration {
	if timeoutMs <= 0 {
		return 10 * time.Second
	}
	return time.Duration(timeoutMs) * time.Millisecond
}

func parseWaitDelay(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, fmt.Errorf("wait duration is required")
	}
	if strings.IndexFunc(raw, func(r rune) bool { return r < '0' || r > '9' }) == -1 {
		return time.ParseDuration(raw + "ms")
	}
	return time.ParseDuration(raw)
}

func remoteObjectText(obj *runtime.RemoteObject) string {
	if obj == nil {
		return ""
	}
	if obj.Description != "" {
		return obj.Description
	}
	if obj.UnserializableValue != "" {
		return string(obj.UnserializableValue)
	}
	if len(obj.Value) == 0 {
		return ""
	}
	var decoded any
	if err := json.Unmarshal(obj.Value, &decoded); err == nil {
		return fmt.Sprint(decoded)
	}
	return string(obj.Value)
}

func cloneConsoleEntries(entries []protocol.ConsoleEntry) []protocol.ConsoleEntry {
	if len(entries) == 0 {
		return nil
	}
	cloned := make([]protocol.ConsoleEntry, len(entries))
	copy(cloned, entries)
	return cloned
}

func cloneNetworkEntries(entries []protocol.NetworkEntry) []protocol.NetworkEntry {
	if len(entries) == 0 {
		return nil
	}
	cloned := make([]protocol.NetworkEntry, len(entries))
	copy(cloned, entries)
	return cloned
}

func cloneDOMSummary(summary *protocol.DOMSummary) *protocol.DOMSummary {
	if summary == nil {
		return nil
	}
	cloned := *summary
	if len(summary.VisibleText) > 0 {
		cloned.VisibleText = append([]string(nil), summary.VisibleText...)
	}
	return &cloned
}
