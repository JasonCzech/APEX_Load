package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/jasonczech/stormrunner/pkg/protocol"
)

func init() {
	protocol.Register("browser", func() protocol.Protocol {
		return &Browser{}
	})
}

type Browser struct {
	baseURL            string
	currentURL         string
	headless           bool
	cacheEnabled       bool
	checkNewerVersions bool
	networkMode        string
	networkPreset      string
	networkBandwidthKB int
	networkLatencyMS   int
	downloadResources  string
	slowMo             time.Duration
	userAgent          string
	captureScreenshots string
	viewportW          int
	viewportH          int

	allocCtx    context.Context
	browserCtx  context.Context
	allocCancel context.CancelFunc
	browserStop context.CancelFunc

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
	b.headless = true
	b.cacheEnabled = true
	b.checkNewerVersions = false
	b.networkMode = ""
	b.networkPreset = ""
	b.networkBandwidthKB = 0
	b.networkLatencyMS = 0
	b.downloadResources = "all"
	b.viewportW = 1440
	b.viewportH = 900
	b.pendingNetwork = make(map[network.RequestID]networkRequest)
	b.currentURL = ""

	if baseURL, ok := config["base_url"].(string); ok {
		b.baseURL = baseURL
	}
	if headless, ok := config["browser_headless"].(bool); ok {
		b.headless = headless
	}
	if cacheEnabled, ok := config["browser_cache_enabled"].(bool); ok {
		b.cacheEnabled = cacheEnabled
	}
	if checkNewerVersions, ok := config["browser_check_newer_versions"].(bool); ok {
		b.checkNewerVersions = checkNewerVersions
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
	if captureScreenshots, ok := config["browser_capture_screenshots"].(string); ok {
		b.captureScreenshots = captureScreenshots
	}
	if viewportW := configInt(config, "browser_viewport_width"); viewportW > 0 {
		b.viewportW = viewportW
	}
	if viewportH := configInt(config, "browser_viewport_height"); viewportH > 0 {
		b.viewportH = viewportH
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
		chromedp.EmulateViewport(int64(b.viewportW), int64(b.viewportH)),
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
	if throttleAction := b.networkThrottleAction(); throttleAction != nil {
		startupActions = append(startupActions, throttleAction)
	}
	if err := chromedp.Run(b.browserCtx, startupActions...); err != nil {
		b.close()
		return err
	}

	return nil
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
	if b.browserCtx == nil {
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
		actionErr = chromedp.Run(actionCtx,
			chromedp.Navigate(b.resolveURL(action.Target)),
			chromedp.WaitReady("body", chromedp.ByQuery),
		)
	case "browser_wait":
		actionErr = b.runWait(actionCtx, action)
	case "browser_click":
		selector := b.actionSelector(action)
		if selector == "" {
			result.ErrorMessage = "browser_click requires selector or target"
			result.Duration = time.Since(result.StartTime)
			return result, nil
		}
		actionErr = chromedp.Run(actionCtx,
			chromedp.WaitVisible(selector, chromedp.ByQuery),
			chromedp.Click(selector, chromedp.ByQuery),
		)
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
		actionErr = chromedp.Run(actionCtx,
			chromedp.WaitVisible(selector, chromedp.ByQuery),
			chromedp.SetValue(selector, action.Value, chromedp.ByQuery),
		)
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
			return chromedp.Run(waitCtx, chromedp.WaitVisible(waitValue, chromedp.ByQuery))
		case "hidden":
			if waitValue == "" {
				return fmt.Errorf("browser_wait hidden requires selector or wait_for value")
			}
			return chromedp.Run(waitCtx, chromedp.WaitNotPresent(waitValue, chromedp.ByQuery))
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
		return chromedp.Run(waitCtx, chromedp.WaitVisible(selector, chromedp.ByQuery))
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

func (b *Browser) readDOMSummary(ctx context.Context) (string, string, *protocol.DOMSummary, error) {
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
	var html string
	err := chromedp.Run(ctx, chromedp.OuterHTML("html", &html, chromedp.ByQuery))
	return html, err
}

func (b *Browser) captureScreenshot(ctx context.Context, action *protocol.Action) (string, error) {
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
		if err := chromedp.Run(ctx, chromedp.Evaluate(expression, &ready)); err == nil && ready {
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

func (b *Browser) close() {
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
