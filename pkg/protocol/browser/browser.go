package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/playwright-community/playwright-go"
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
	devicePlatform             string
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

	pwContext   playwright.BrowserContext
	pwPage      playwright.Page
	pwCDP       playwright.CDPSession

	mu             sync.Mutex
	consoleEntries []protocol.ConsoleEntry
	networkEntries []protocol.NetworkEntry
	pendingNetwork map[string]networkRequest

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

type browserProcessSnapshot struct {
	CPUTimeSeconds float64
	MemoryMB       float64
	CapturedAt      time.Time
}

func (b *Browser) Name() string { return "browser" }

func (b *Browser) Init(ctx context.Context, config map[string]any) error {
	b.engine = "chromium"
	if baseURL, ok := config["base_url"].(string); ok {
		b.baseURL = strings.TrimSpace(baseURL)
	}
	if engine, ok := config["browser_engine"].(string); ok && strings.TrimSpace(engine) != "" {
		b.engine = strings.ToLower(strings.TrimSpace(engine))
	}
	if headless, ok := config["browser_headless"].(bool); ok {
		b.headless = headless
	} else {
		b.headless = true
	}
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
	b.pendingNetwork = make(map[string]networkRequest)
	b.currentURL = ""
	b.clientMetricsEnabled = false
	b.mobile = false
	b.deviceProfile = ""
	b.devicePlatform = ""
	b.deviceTier = ""
	b.userAgent = ""
	b.touchMode = false
	b.captureScreenshots = ""
	
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

	if b.engine != "chromium" && b.engine != "webkit" {
		return fmt.Errorf("unsupported browser engine: %s", b.engine)
	}

	if b.deviceProfile != "" {
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
	}
	
	if b.downloadResources == "recorded" && !b.recordedResourcesAvailable {
		return fmt.Errorf("recorded resource metadata not available")
	}

	pwBrowser, err := getBrowserInstance(b.engine, b.headless)
	if err != nil {
		return err
	}

	contextOptions := playwright.BrowserNewContextOptions{
		HasTouch: playwright.Bool(b.touchMode),
		IsMobile: playwright.Bool(b.mobile),
		Viewport: &playwright.Size{Width: b.viewportW, Height: b.viewportH},
	}
	if b.userAgent != "" {
		contextOptions.UserAgent = playwright.String(b.userAgent)
	}

	browserContext, err := pwBrowser.NewContext(contextOptions)
	if err != nil {
		return err
	}
	if b.clientMetricsEnabled {
		if err := browserContext.AddInitScript(playwright.Script{Content: playwright.String(browserMetricsBootstrapScript)}); err != nil {
			_ = browserContext.Close()
			return err
		}
	}

	browserContext.OnConsole(func(message playwright.ConsoleMessage) {
		source := ""
		if location := message.Location(); location != nil {
			source = location.URL
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		b.consoleEntries = append(b.consoleEntries, protocol.ConsoleEntry{
			Level:     message.Type(),
			Text:      strings.TrimSpace(message.Text()),
			Source:    source,
			Timestamp: time.Now(),
		})
	})
	browserContext.OnDialog(func(dialog playwright.Dialog) {
		_ = dialog.Dismiss()
	})
	
	recordRequest := func(request playwright.Request, finished bool) {
		statusCode := 0
		if response, err := request.Response(); err == nil && response != nil {
			statusCode = response.Status()
		}
		var bytesReceived int64
		if sizes, err := request.Sizes(); err == nil && sizes != nil {
			bytesReceived = int64(sizes.ResponseHeadersSize + sizes.ResponseBodySize)
		}
		durationMs := 0.0
		if timing := request.Timing(); timing != nil {
			switch {
			case timing.ResponseEnd >= 0:
				durationMs = timing.ResponseEnd
			case timing.ResponseStart >= 0:
				durationMs = timing.ResponseStart
			}
		}
		if !finished && durationMs < 0 {
			durationMs = 0
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		b.networkEntries = append(b.networkEntries, protocol.NetworkEntry{
			URL:           request.URL(),
			Method:        request.Method(),
			StatusCode:    statusCode,
			ResourceType:  request.ResourceType(),
			DurationMs:    durationMs,
			BytesReceived: bytesReceived,
		})
	}
	browserContext.OnRequestFinished(func(request playwright.Request) {
		recordRequest(request, true)
	})
	browserContext.OnRequestFailed(func(request playwright.Request) {
		recordRequest(request, false)
	})

	page, err := browserContext.NewPage()
	if err != nil {
		_ = browserContext.Close()
		return err
	}

	b.pwContext = browserContext
	b.pwPage = page

	if b.engine == "chromium" && b.cpuThrottleRate > 1.0 {
		cdp, err := browserContext.NewCDPSession(page)
		if err == nil {
			b.pwCDP = cdp
			_, _ = cdp.Send("Emulation.setCPUThrottlingRate", map[string]any{"rate": b.cpuThrottleRate})
		}
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
	if b.pwPage == nil {
		result.ErrorMessage = "browser protocol is not initialized"
		return result, nil
	}

	consoleStart, networkStart := b.eventOffsets()
	actionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	processBefore, _ := b.captureBrowserProcessSnapshot(actionCtx)

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
	case "browser_select":
		selector := b.actionSelector(action)
		if selector == "" {
			result.ErrorMessage = "browser_select requires selector or target"
			result.Duration = time.Since(result.StartTime)
			return result, nil
		}
		if action.Value == "" {
			result.ErrorMessage = "browser_select requires value"
			result.Duration = time.Since(result.StartTime)
			return result, nil
		}
		actionErr = b.selectOption(actionCtx, selector, action.Value)
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
	result.ClientMetrics = b.captureClientMetrics(actionCtx, result.Duration, processBefore)

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
	if requiresBrowserAssertionHTML(action.Assertions) && result.HTML == "" {
		html, err := b.captureHTML(actionCtx)
		if err != nil {
			result.ErrorMessage = err.Error()
			return result, nil
		}
		result.HTML = html
	}
	if assertionErr := evaluateBrowserAssertions(action.Assertions, result); assertionErr != nil {
		result.ErrorMessage = assertionErr.Error()
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

	result.Success = true
	return result, nil
}

func requiresBrowserAssertionHTML(assertions []protocol.Assertion) bool {
	for _, assertion := range assertions {
		switch assertion.Type {
		case "body_contains", "body_not_contains":
			return true
		}
	}
	return false
}

func evaluateBrowserAssertions(assertions []protocol.Assertion, result *protocol.Result) error {
	for _, assertion := range assertions {
		switch assertion.Type {
		case "response_time_ms":
			if assertion.Max > 0 && result.Duration.Milliseconds() > int64(assertion.Max) {
				return fmt.Errorf("browser response time %dms exceeded max %dms", result.Duration.Milliseconds(), assertion.Max)
			}
		case "body_contains":
			if !strings.Contains(result.HTML, assertion.Value) {
				return fmt.Errorf("browser page HTML does not contain %q", assertion.Value)
			}
		case "body_not_contains":
			if strings.Contains(result.HTML, assertion.Value) {
				return fmt.Errorf("browser page HTML contains %q (unexpected)", assertion.Value)
			}
		case "page_title":
			expected := strings.TrimSpace(assertion.Value)
			if expected == "" && assertion.Expected != nil {
				expected = strings.TrimSpace(fmt.Sprint(assertion.Expected))
			}
			if expected == "" {
				return fmt.Errorf("page_title assertion requires value or expected")
			}
			if result.PageTitle != expected {
				return fmt.Errorf("browser page title = %q, want %q", result.PageTitle, expected)
			}
		case "url_contains":
			if assertion.Value == "" {
				return fmt.Errorf("url_contains assertion requires value")
			}
			if !strings.Contains(result.PageURL, assertion.Value) {
				return fmt.Errorf("browser page URL %q does not contain %q", result.PageURL, assertion.Value)
			}
		default:
			return fmt.Errorf("unsupported browser assertion type: %s", assertion.Type)
		}
	}
	return nil
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
			return b.waitVisible(waitCtx, waitValue)
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
		return b.waitVisible(ctx, action.Target)
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

func (b *Browser) captureClientMetrics(ctx context.Context, duration time.Duration, processBefore browserProcessSnapshot) *protocol.ClientMetrics {
	if !b.clientMetricsEnabled {
		return nil
	}

	metrics := &protocol.ClientMetrics{
		DeviceProfile:    b.deviceProfile,
		DevicePlatform:   b.devicePlatform,
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
	if err := b.captureBrowserProcessMetrics(ctx, metrics, processBefore); err != nil {
		// Keep browser-process attribution non-fatal because CDP support varies by runtime.
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
		const observerMetrics = window.__apexloadMetrics || {};
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
	if (window.__apexloadMetrics) {
		return;
	}
	window.__apexloadMetrics = {
		lcp: 0,
		cls: 0,
		inp: 0,
		longTaskCount: 0,
	};
	try {
		new PerformanceObserver((list) => {
			for (const entry of list.getEntries()) {
				window.__apexloadMetrics.lcp = entry.startTime || window.__apexloadMetrics.lcp;
			}
		}).observe({ type: 'largest-contentful-paint', buffered: true });
	} catch (e) {}
	try {
		new PerformanceObserver((list) => {
			for (const entry of list.getEntries()) {
				if (!entry.hadRecentInput) {
					window.__apexloadMetrics.cls += entry.value || 0;
				}
			}
		}).observe({ type: 'layout-shift', buffered: true });
	} catch (e) {}
	try {
		new PerformanceObserver((list) => {
			window.__apexloadMetrics.longTaskCount += list.getEntries().length;
		}).observe({ type: 'longtask', buffered: true });
	} catch (e) {}
	try {
		new PerformanceObserver((list) => {
			for (const entry of list.getEntries()) {
				const duration = entry.duration || entry.processingEnd || 0;
				if (duration > window.__apexloadMetrics.inp) {
					window.__apexloadMetrics.inp = duration;
				}
			}
		}).observe({ type: 'event', buffered: true, durationThreshold: 16 });
	} catch (e) {}
})();`

func (b *Browser) captureRuntimeMetrics(ctx context.Context, metrics *protocol.ClientMetrics) error { return nil }

func (b *Browser) captureBrowserProcessMetrics(ctx context.Context, metrics *protocol.ClientMetrics, before browserProcessSnapshot) error { return nil }

func (b *Browser) captureBrowserProcessSnapshot(ctx context.Context) (browserProcessSnapshot, error) { return browserProcessSnapshot{}, nil }

func browserProcessResourceMetrics(before, after browserProcessSnapshot) (float64, float64) {
	memoryMB := after.MemoryMB
	if before.CapturedAt.IsZero() || after.CapturedAt.IsZero() || !after.CapturedAt.After(before.CapturedAt) {
		return 0, memoryMB
	}
	cpuDelta := after.CPUTimeSeconds - before.CPUTimeSeconds
	if cpuDelta <= 0 {
		return 0, memoryMB
	}
	elapsedSeconds := after.CapturedAt.Sub(before.CapturedAt).Seconds()
	if elapsedSeconds <= 0 {
		return 0, memoryMB
	}
	return cpuDelta / elapsedSeconds * 100, memoryMB
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



func (b *Browser) screenshotSelector(action *protocol.Action) string {
	if action == nil {
		return ""
	}
	if action.Selector != "" {
		return action.Selector
	}
	switch action.Type {
	case "browser_click", "browser_fill", "browser_select", "browser_wait", "browser_screenshot":
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


func (b *Browser) navigate(ctx context.Context, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	resolvedTarget := target
	if b.baseURL != "" && strings.HasPrefix(target, "/") {
		resolvedTarget = strings.TrimRight(b.baseURL, "/") + target
	} else if !strings.HasPrefix(target, "http") {
		resolvedTarget = "http://" + target
	}

	if _, err := b.pwPage.Goto(resolvedTarget); err != nil {
		return err
	}
	b.currentURL = resolvedTarget
	return b.waitVisible(ctx, "body")
}

func (b *Browser) click(ctx context.Context, selector string) error {
	if err := b.waitVisible(ctx, selector); err != nil {
		return err
	}
	return b.pwPage.Click(selector)
}

func (b *Browser) fill(ctx context.Context, selector, value string) error {
	if err := b.waitVisible(ctx, selector); err != nil {
		return err
	}
	return b.pwPage.Fill(selector, value)
}

func (b *Browser) waitVisible(ctx context.Context, selector string) error {
	_, err := b.pwPage.WaitForSelector(selector)
	return err
}

func (b *Browser) waitHidden(ctx context.Context, selector string) error {
	_, err := b.pwPage.WaitForSelector(selector, playwright.PageWaitForSelectorOptions{State: playwright.WaitForSelectorStateHidden})
	return err
}

func (b *Browser) evaluatePageExpression(ctx context.Context, expression string, result any) error {
	res, err := b.pwPage.Evaluate(expression)
	if err != nil {
		return err
	}
	if result == nil {
		return nil
	}
    data, _ := json.Marshal(res)
	return json.Unmarshal(data, result)
}

func (b *Browser) captureHTML(ctx context.Context) (string, error) {
	return b.pwPage.Content()
}

func (b *Browser) captureScreenshot(ctx context.Context, action *protocol.Action) (string, error) {
	opts := playwright.PageScreenshotOptions{}
	selector := b.screenshotSelector(action)
	if selector != "" {
		el, err := b.pwPage.QuerySelector(selector)
		if err != nil {
			return "", err
		}
		if el != nil {
			buf, err := el.Screenshot()
			return base64.StdEncoding.EncodeToString(buf), err
		}
	}
	if action != nil && action.Capture != nil && action.Capture.FullPage {
		opts.FullPage = playwright.Bool(true)
	}
	buf, err := b.pwPage.Screenshot(opts)
	return base64.StdEncoding.EncodeToString(buf), err
}

func (b *Browser) readDOMSummary(ctx context.Context) (string, string, *protocol.DOMSummary, error) {
	res, err := b.pwPage.Evaluate(`(() => ({
		url: window.location.href,
		title: document.title,
		readyState: document.readyState,
		visible: (document.body?.innerText || '').split(/\n+/).map((line) => line.trim()).filter(Boolean).slice(0, 10)
	}))()`)
	if err != nil {
		return "", "", nil, err
	}
	data, ok := res.(map[string]any)
	if !ok {
		return "", "", nil, fmt.Errorf("invalid dom summary")
	}
	title, _ := data["title"].(string)
	url, _ := data["url"].(string)
	readyState, _ := data["readyState"].(string)
	
	visibleList, _ := data["visible"].([]any)
	visibleText := []string{}
	for _, v := range visibleList {
		if s, ok := v.(string); ok {
			visibleText = append(visibleText, s)
		}
	}
	
	return url, title, &protocol.DOMSummary{
		Title:       title,
		ReadyState:  readyState,
		VisibleText: visibleText,
	}, nil
}

func (b *Browser) close() {
	if b.pwCDP != nil {
		_ = b.pwCDP.Detach()
		b.pwCDP = nil
	}
	if b.pwPage != nil {
		_ = b.pwPage.Close()
		b.pwPage = nil
	}
	if b.pwContext != nil {
		_ = b.pwContext.Close()
		b.pwContext = nil
	}
	b.currentURL = ""
	b.slowMo = 0
	b.userAgent = ""
	b.captureScreenshots = ""
	b.pendingNetwork = nil
	b.consoleEntries = nil
	b.networkEntries = nil
}


func (b *Browser) selectOption(ctx context.Context, selector, value string) error {
	if err := b.waitVisible(ctx, selector); err != nil {
		return err
	}
	_, err := b.pwPage.SelectOption(selector, playwright.SelectOptionValues{
		Values: playwright.StringSlice(value),
	})
	return err
}
