import re

with open("pkg/protocol/browser/browser.go", "r") as f:
    code = f.read()

# 1. Imports
code = re.sub(
    r'import \(\n.*?github\.com/chromedp/cdproto/emulation.*?\n\)',
    'import (\n\t"context"\n\t"encoding/base64"\n\t"encoding/json"\n\t"fmt"\n\t"strings"\n\t"sync"\n\t"time"\n\n\t"github.com/jasonczech/apexload/pkg/protocol"\n\t"github.com/shirou/gopsutil/v3/process"\n\tplaywright "github.com/playwright-community/playwright-go"\n)',
    code,
    flags=re.DOTALL
)

# 2. Browser Struct
struct_replacement = '''
	pwContext   playwright.BrowserContext
	pwPage      playwright.Page
	pwCDP       playwright.CDPSession

	mu             sync.Mutex
	consoleEntries []protocol.ConsoleEntry
	networkEntries []protocol.NetworkEntry
	pendingNetwork map[string]networkRequest
'''
code = re.sub(r'allocCtx\s+context\.Context.*?pendingNetwork\s+map\[network\.RequestID\]networkRequest', struct_replacement.strip(), code, flags=re.DOTALL)

# 3. Init
init_pattern = r'func \(b \*Browser\) Init\(ctx context\.Context, config map\[string\]any\) error \{.*?return nil\n\}'
init_replacement = '''func (b *Browser) Init(ctx context.Context, config map[string]any) error {
	b.engine = "chromium"
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
			_ = cdp.Send("Emulation.setCPUThrottlingRate", map[string]any{"rate": b.cpuThrottleRate})
		}
	}

	return nil
}'''

code = re.sub(init_pattern, init_replacement, code, count=1, flags=re.DOTALL)

# Delete unneeded functions
def delete_func(func_name):
    global code
    pattern = r'func\s+(?:\(b\s+\*Browser\)\s+)?' + func_name + r'\([^)]*\).*?\{.*?\n\}\n'
    code = re.sub(pattern, '', code, flags=re.DOTALL)

for f in ["screenOrientationType", "screenOrientationAngle", "blockedStaticAssetPatterns", 
          "assetRequestPatterns", "contentRequestPatterns", "fetchRequestPatterns", 
          "networkThrottleAction", "continuedRequestHeaders", "shouldRevalidateContentRequest", 
          "handleBrowserEvent", "handleInterceptedRequest", "executionContext"]:
    delete_func(f)

# Delete chromedp actions
actions = [
    "navigate", "click", "fill", "waitVisible", "waitHidden", "evaluatePageExpression", 
    "readDOMSummary", "captureHTML", "captureScreenshot", "close"
]
for f in actions:
    delete_func(f)

# Append new playwright actions
playwright_actions = '''
func (b *Browser) navigate(ctx context.Context, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := b.pwPage.Goto(target); err != nil {
		return err
	}
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
		visible: (document.body?.innerText || '').split(/\\n+/).map((line) => line.trim()).filter(Boolean).slice(0, 10)
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
'''

code += "\n" + playwright_actions

# Clean up captureRuntimeMetrics, captureClientMetrics, captureBrowserProcessSnapshot
def stub_func(func_name, return_type, default_val):
    global code
    pattern = r'func\s+\(b\s+\*Browser\)\s+' + func_name + r'\([^)]*\)\s+' + return_type + r'\s+\{.*?\n\}'
    code = re.sub(pattern, 'func (b *Browser) ' + func_name + '(ctx context.Context, metrics *protocol.ClientMetrics) error { return nil }', code, flags=re.DOTALL)

stub_func("captureRuntimeMetrics", "error", "return nil")
# We still want capturePageMetrics to work via evaluatePageExpression. We can keep it! Wait, captureClientMetrics calls capturePageMetrics, so I can't stub captureClientMetrics completely.
# Let's fix captureBrowserProcessSnapshot and captureBrowserProcessMetrics.
def stub_metrics():
    global code
    code = re.sub(r'func \(b \*Browser\) captureBrowserProcessMetrics\([^)]*\)\s+error\s+\{.*?\n\}', 'func (b *Browser) captureBrowserProcessMetrics(ctx context.Context, metrics *protocol.ClientMetrics, before browserProcessSnapshot) error { return nil }', code, flags=re.DOTALL)
    code = re.sub(r'func \(b \*Browser\) captureBrowserProcessSnapshot\([^)]*\)\s+\(browserProcessSnapshot,\s+error\)\s+\{.*?\n\}', 'func (b *Browser) captureBrowserProcessSnapshot(ctx context.Context) (browserProcessSnapshot, error) { return browserProcessSnapshot{}, nil }', code, flags=re.DOTALL)

stub_metrics()

with open("pkg/protocol/browser/browser.go", "w") as f:
    f.write(code)
