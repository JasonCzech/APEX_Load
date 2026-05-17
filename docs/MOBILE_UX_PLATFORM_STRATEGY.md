# Strategic Plan: Mobile UX Performance Platform

## Vision
Position Stormrunner as a **synthetic digital experience platform** — capable of emulating any modern mobile device (iOS, iPadOS, Android) with high fidelity and measuring the complete client-side user experience with the depth and precision of tools like Quantum Metric, but from a controlled, repeatable performance testing context.

This plan is the strategic umbrella that aligns the existing `MOBILE_BROWSER_EMULATION_PLAN.md` and `BROWSER_CLIENT_METRICS_PLAN.md` under one long-term trajectory. Both plans remain valid as tactical implementation guides — this document ensures they converge toward the same product goal rather than diverging into disconnected features.

## Status Snapshot (May 2026)

### Implemented
- Layer 1 foundation is in place: named device profiles, browser-family compatibility rules, orientation/touch configuration, mobile gesture actions, and script-editor authoring support are all shipped.
- Real WebKit execution is now available through a Playwright-backed runtime path for iOS/iPadOS-style profiles, alongside the existing Chromium path for Android and desktop profiles.
- Layer 2 collection is in place for the current browser metric contract: per-action `ClientMetrics`, optional `browser.client_metrics`, live `browser` and `browser_by_device` summaries, archive persistence, canonical archive `browser_by_engine` / `browser_by_platform` / `browser_by_device` rollups, and dashboard drilldown are implemented.
- Validation and replay now surface actionable WebKit prerequisite warnings before execution, and saved scripts can be validated directly from the editor.

### Remaining
- Stage 2 is only partially complete: device profiles now carry `CPUThrottleRate` and `NetworkPreset`, and the runtime applies them, but calibration against real devices, benchmark evidence, and any memory-pressure model are still open.
- Stage 4 still has UX gaps: dedicated device-comparison views, per-resource waterfall drilldown, and broader manual browser validation on real applications are not finished. Archive event inspection now includes a richer navigation/resource waterfall summary from persisted client metrics, including DOM/load phase gaps and blocking-resource pressure.
- Stage 5 TrueClient MobileWeb import is partially shipped for the current browser step envelope, including runtime-setting extraction and common decomposed JavaScript fill/click, scroll, and navigation patterns; broader fixture-backed hardening remains open.
- Stage 6 UX intelligence work has not started.

## Why This Matters
Traditional performance testing measures server response time. Modern web applications spend 40-70% of perceived latency in **client-side rendering, JavaScript execution, and layout computation**. A user on an iPhone SE over 4G experiences a fundamentally different application than a user on a desktop with fiber — even when the server response is identical.

Quantum Metric and similar RUM platforms capture this reality from production traffic. Stormrunner's opportunity is to capture it **before production**, under controlled load, across every target device and network profile.

## Architectural North Star

### The Three Layers
Every browser VUser produces three distinct categories of measurement. The architecture must treat them as composable layers, not a monolith:

```
┌─────────────────────────────────────────────────────────┐
│  Layer 3: UX Intelligence                               │
│  Frustration signals, interaction heatmaps,             │
│  business-impact scoring, anomaly detection             │
│  (Future — data science on top of collected metrics)    │
├─────────────────────────────────────────────────────────┤
│  Layer 2: Client-Side Performance Telemetry             │
│  Core Web Vitals (LCP, FCP, CLS, INP)                  │
│  Navigation timing, resource waterfall                  │
│  JS heap, layout/paint counts, long tasks              │
│  Network sub-resource timing, render blocking          │
│  → BROWSER_CLIENT_METRICS_PLAN.md                       │
├─────────────────────────────────────────────────────────┤
│  Layer 1: Device Emulation & Interaction                │
│  Device profiles, viewport, UA, touch events            │
│  CPU throttling, network conditioning                   │
│  Orientation, device scale factor                       │
│  → MOBILE_BROWSER_EMULATION_PLAN.md                     │
└─────────────────────────────────────────────────────────┘
```

**Key constraint:** Every architectural decision must keep these layers separable. A user should be able to run Layer 1 without Layer 2, or Layer 2 without Layer 3. Coupling them prematurely will limit adoption and complicate the codebase.

## What "High Fidelity" Actually Means

Device emulation fidelity has four dimensions. The current mobile emulation plan covers the first two. This strategic plan adds the third and fourth:

### Dimension 1: Visual Fidelity (Covered — Mobile Emulation Plan Phase 1)
- Viewport dimensions, device scale factor, pixel ratio
- Mobile user agent string → correct CSS breakpoints and responsive layouts
- Orientation (portrait/landscape)

### Dimension 2: Interaction Fidelity (Covered — Mobile Emulation Plan Phase 2)
- Touch events instead of mouse clicks
- Swipe, long-press, pinch-zoom gestures
- Touch-specific JavaScript event handlers fire correctly

### Dimension 3: Performance Fidelity (New — This Plan)
- **CPU throttling** via `Emulation.setCPUThrottlingRate` — simulate slower mobile processors
- **Network conditioning** per device class — not just generic presets but device-aware defaults (e.g., iPhone on LTE vs. Android on WiFi)
- **Memory pressure** simulation — constrained heap budgets that mirror real devices
- These controls ensure that client-side timing measurements reflect what a real device would produce, not what a 32-core build server produces

### Dimension 4: Measurement Fidelity (New — This Plan)
- **Core Web Vitals** collected from the browser's own Performance API
- **Navigation timing** waterfall (DNS, TCP, TLS, TTFB, DOM parse, render)
- **Resource loading** analysis (blocking scripts, deferred images, font loading)
- **JavaScript execution** profiling (long tasks, main thread blocking)
- **Layout and paint** counts from CDP `Performance.getMetrics`

## Design Decisions That Protect The Long-Term Goal

### 1. Device Profiles Must Include Performance Characteristics
The `DeviceProfile` struct in `MOBILE_BROWSER_EMULATION_PLAN.md` currently bundles viewport + UA + scale + touch. To support performance fidelity, extend it with hardware simulation parameters:

```go
type DeviceProfile struct {
    // Visual (already planned)
    Name              string
    Width             int
    Height            int
    DeviceScaleFactor float64
    Mobile            bool
    HasTouch          bool
    DefaultOrientation string
    UserAgent         string

    // Performance (new)
    CPUThrottleRate   float64  // e.g., 4.0 for 4x slowdown (iPhone SE), 2.0 (iPhone 15 Pro)
    NetworkPreset     string   // "4g_lte", "5g", "wifi", "3g" — maps to existing throttle presets
    JSHeapLimitMB     int      // advisory — for reporting, not hard enforcement
    DeviceTier        string   // "low", "mid", "high" — for dashboard grouping
}
```

This is the single most important decision. If device profiles only carry visual parameters, performance measurement will always be disconnected from device simulation.

### 2. Client Metrics Must Be Per-Action, Not Per-Page
Quantum Metric measures at session granularity. Stormrunner measures at action granularity (each `browser_navigate`, `browser_click`, etc.). The client metrics architecture must attach timing data to each action result, not just to full page loads.

Extend `protocol.Result` with an optional client metrics struct:

```go
type ClientMetrics struct {
    // Core Web Vitals
    LCPMs            float64 `json:"lcp_ms,omitempty"`
    FCPMs            float64 `json:"fcp_ms,omitempty"`
    CLSScore         float64 `json:"cls_score,omitempty"`
    INPMs            float64 `json:"inp_ms,omitempty"`

    // Navigation timing
    DOMContentLoadedMs float64 `json:"dom_content_loaded_ms,omitempty"`
    DOMCompleteMs      float64 `json:"dom_complete_ms,omitempty"`
    LoadEventMs        float64 `json:"load_event_ms,omitempty"`

    // Resource timing
    BlockingResourceCount int     `json:"blocking_resource_count,omitempty"`
    TotalResourceCount    int     `json:"total_resource_count,omitempty"`
    ResourceTransferKB    float64 `json:"resource_transfer_kb,omitempty"`

    // Runtime performance
    JSHeapUsedMB     float64 `json:"js_heap_used_mb,omitempty"`
    JSHeapTotalMB    float64 `json:"js_heap_total_mb,omitempty"`
    LayoutCount      int     `json:"layout_count,omitempty"`
    RecalcStyleCount int     `json:"recalc_style_count,omitempty"`
    ScriptDurationMs float64 `json:"script_duration_ms,omitempty"`
    LayoutDurationMs float64 `json:"layout_duration_ms,omitempty"`
    TaskDurationMs   float64 `json:"task_duration_ms,omitempty"`
    LongTaskCount    int     `json:"long_task_count,omitempty"`
}
```

Add `ClientMetrics *ClientMetrics` to `protocol.Result`. Nil when client metrics are disabled — zero cost for HTTP/JMS protocols.

### 3. Collection Must Use CDP + PerformanceObserver, Not Just One
Two complementary data sources:

| Source | What It Provides | Collection Method |
|:---|:---|:---|
| **CDP `Performance.getMetrics`** | JS heap, layout/paint counts, script duration, task duration | Go call via chromedp after each action |
| **CDP `PerformanceObserver` (injected JS)** | LCP, FCP, CLS, INP, long tasks, resource timing | JavaScript injected at page load, results extracted via `Runtime.evaluate` |
| **CDP `Network` domain** | Resource waterfall, blocking detection | Already captured in browser protocol |
| **CDP `Performance.enable` timeline** | Detailed trace events | Optional — heavy, for diagnostic drills only |

The browser protocol already captures network events. The plan adds the other two as opt-in collection modes.

### 4. Metrics Aggregation Must Support Device-Segmented Analysis
The dashboard must be able to answer: "What is the P90 LCP for iPhone SE on 4G vs. Galaxy S24 on WiFi?" This requires the metrics pipeline to carry device profile as a dimension:

```go
// In MetricsCollector, browser client metrics are tagged with device context
type BrowserMetricSample struct {
    ActionName    string
    DeviceProfile string
    DeviceTier    string
    Timestamp     time.Time
    ClientMetrics ClientMetrics
}
```

### 5. CPU Throttling Is Applied At Init, Not Per-Action
`Emulation.setCPUThrottlingRate` is a global browser setting. It's set once during `Init()` based on the device profile and persists for the VUser's lifetime. This mirrors how a real device works — the CPU doesn't change between page loads.

```go
// In browser.go Init(), after device metric override
if b.cpuThrottleRate > 1.0 {
    startupActions = append(startupActions,
        emulation.SetCPUThrottlingRate(b.cpuThrottleRate),
    )
}
```

### 6. The `browser` Protocol Stays Singular
No `browser_mobile`, no `browser_desktop`, no `browser_metrics`. One protocol, multiple configuration profiles. This was already decided in the mobile emulation plan — this strategic plan reinforces it as a long-term commitment.

## Implementation Sequence

The existing plans define tactical phases. This section maps them to the strategic trajectory:

### Stage 1: Foundation (Mobile Emulation Plan Phases 1-2)
- Device profile registry with visual parameters
- CDP device metric override, touch emulation
- Touch action types

**Strategic gate:** After this stage, validate that mobile-configured browsers serve mobile CSS, fire touch events, and render responsively. If visual fidelity is poor, stop and fix before adding performance measurement.

### Stage 2: Performance Simulation (Partially Complete)
- Extend device profiles with `CPUThrottleRate` and `NetworkPreset`
- Apply `Emulation.setCPUThrottlingRate` during `Init()`
- Wire device-specific network presets to existing throttling infrastructure
- Benchmark: confirm that CPU-throttled Chromium produces measurably different timing than unthrottled

**Key deliverables:**
- `browser/devices.go` updated with performance characteristics per device
- `browser.go Init()` applies CPU throttling from profile
- Unit tests confirming profile defaults are wired; benchmark calibration and timing validation are still open

**Remaining work:** benchmark calibration, decide whether to add memory-pressure simulation, and publish operational guidance for how approximate the simulated profile timings are. Scenario capacity planning now uses conservative tier-based browser VUser density caps so users get safer generator estimates while benchmark calibration is still in progress.

### Stage 3: Client Metrics Collection (Browser Client Metrics Plan Phases 1-2)
- `ClientMetrics` struct on `protocol.Result`
- `performance.Enable()` + `performance.GetMetrics()` after each action
- PerformanceObserver injection for Core Web Vitals (LCP, FCP, CLS, INP)
- Resource timing extraction
- Long task detection

**Key deliverables:**
- `browser/metrics.go` — CDP metric collection helpers
- `browser/vitals.js` — injected PerformanceObserver script
- `protocol.Result.ClientMetrics` populated per action
- Config flag: `browser.client_metrics: true` enables collection

**Effort:** 5-7 days

### Stage 4: Aggregation + Dashboard (Browser Client Metrics Plan Phases 3-4 + Mobile Plan Phase 3)
- Device-segmented metric aggregation in `engine/metrics.go`
- Live WebSocket streaming of client metric summaries
- Archived per-action client metric persistence
- Dashboard: device profile selector, Core Web Vitals charts, resource waterfall view
- Dashboard: device comparison mode (side-by-side LCP/FCP across profiles)

**Effort:** 5-7 days

### Stage 5: TrueClient Import (Mobile Emulation Plan Phase 4)
- `pkg/convert/trueclient` converter
- MVP implemented for `TruClientMobileWeb` LoadRunner ZIP archives
- Step-to-action mapping for navigate, click, fill, select, wait, conservative verify assertions, transaction-preserved names, and simple decomposed JavaScript fill/click statements
- Device profile extraction from LoadRunner runtime settings

**Remaining effort:** 3-5 days for non-decomposable JavaScript hardening, import review UX refinements, and broader replay-validation coverage. Simple TruClient JavaScript fill/click, scroll, and location-navigation statements now map to browser DSL actions, reducing previous review-only repair paths.

### Stage 6: UX Intelligence (Future)
- Frustration detection: rage taps, repeated navigation, abandonment patterns
- Interaction heatmap generation from touch/click coordinates
- Business impact scoring: correlate client metric degradation with conversion funnel position
- Anomaly detection: flag actions where client metrics regress vs. baseline

**Effort:** This is a product evolution, not a single implementation phase. Begin only after Stages 1-4 are stable and producing real data.

## Metric Collection Implementation Detail

### Core Web Vitals via Injected JavaScript
After each navigation action, inject a PerformanceObserver and extract results:

```go
func (b *Browser) collectWebVitals(ctx context.Context) (*ClientMetrics, error) {
    var vitalsJSON string
    err := chromedp.Run(ctx, chromedp.Evaluate(webVitalsScript, &vitalsJSON))
    if err != nil {
        return nil, err
    }
    var metrics ClientMetrics
    json.Unmarshal([]byte(vitalsJSON), &metrics)
    return &metrics, nil
}

const webVitalsScript = `(() => {
    const entries = performance.getEntriesByType('paint');
    const fcp = entries.find(e => e.name === 'first-contentful-paint');
    const nav = performance.getEntriesByType('navigation')[0] || {};
    const resources = performance.getEntriesByType('resource');
    const blocking = resources.filter(r => r.renderBlockingStatus === 'blocking');

    // LCP from PerformanceObserver (buffered)
    let lcp = 0;
    try {
        const lcpEntries = performance.getEntriesByType('largest-contentful-paint');
        if (lcpEntries.length) lcp = lcpEntries[lcpEntries.length - 1].startTime;
    } catch(e) {}

    // CLS from layout-shift entries
    let cls = 0;
    try {
        const shifts = performance.getEntriesByType('layout-shift');
        cls = shifts.reduce((sum, e) => sum + (e.hadRecentInput ? 0 : e.value), 0);
    } catch(e) {}

    return JSON.stringify({
        fcp_ms: fcp ? fcp.startTime : 0,
        lcp_ms: lcp,
        cls_score: cls,
        dom_content_loaded_ms: nav.domContentLoadedEventEnd || 0,
        dom_complete_ms: nav.domComplete || 0,
        load_event_ms: nav.loadEventEnd || 0,
        blocking_resource_count: blocking.length,
        total_resource_count: resources.length,
        resource_transfer_kb: resources.reduce((s, r) => s + (r.transferSize || 0), 0) / 1024
    });
})();`
```

### CDP Runtime Metrics
After each action, sample CDP performance counters:

```go
func (b *Browser) collectCDPMetrics(ctx context.Context) (*ClientMetrics, error) {
    var metrics []*performance.Metric
    err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
        var err error
        metrics, err = performance.GetMetrics().Do(ctx)
        return err
    }))
    // Map CDP metric names to ClientMetrics fields
    cm := &ClientMetrics{}
    for _, m := range metrics {
        switch m.Name {
        case "JSHeapUsedSize":
            cm.JSHeapUsedMB = m.Value / (1024 * 1024)
        case "JSHeapTotalSize":
            cm.JSHeapTotalMB = m.Value / (1024 * 1024)
        case "LayoutCount":
            cm.LayoutCount = int(m.Value)
        case "RecalcStyleCount":
            cm.RecalcStyleCount = int(m.Value)
        case "ScriptDuration":
            cm.ScriptDurationMs = m.Value * 1000
        case "LayoutDuration":
            cm.LayoutDurationMs = m.Value * 1000
        case "TaskDuration":
            cm.TaskDurationMs = m.Value * 1000
        }
    }
    return cm, nil
}
```

### Merging Both Sources
After each action, merge web vitals (from JS) with CDP metrics (from Go):

```go
func (b *Browser) captureClientMetrics(ctx context.Context, action *protocol.Action) *protocol.ClientMetrics {
    if !b.clientMetricsEnabled {
        return nil
    }
    vitals, _ := b.collectWebVitals(ctx)
    cdp, _ := b.collectCDPMetrics(ctx)
    return mergeClientMetrics(vitals, cdp)
}
```

Result is attached to `protocol.Result.ClientMetrics` and flows through the existing metrics pipeline.

## DSL Configuration

```json
{
    "protocol": "browser",
    "config": {
        "base_url": "https://m.example.com",
        "browser": {
            "device_profile": "iphone_se",
            "orientation": "portrait",
            "touch_mode": true,
            "headless": true,
            "client_metrics": true,
            "capture_screenshots": "on_error"
        }
    }
}
```

When `device_profile` is set and `client_metrics` is true:
1. Viewport, UA, scale factor, touch mode from profile (visual fidelity)
2. CPU throttle rate from profile (performance fidelity)
3. Network preset from profile (network fidelity)
4. PerformanceObserver + CDP metrics collected per action (measurement fidelity)

All four dimensions active, all from a single config flag + device name.

## Dashboard: Device Comparison View

The strategic differentiator is the ability to **compare client-side performance across device profiles** in a single test run using VUser groups:

```json
{
    "groups": [
        { "name": "iPhone SE 4G",     "vusers": 5,  "script": "checkout.json", "overrides": { "browser": { "device_profile": "iphone_se" } } },
        { "name": "iPhone 15 Pro 5G", "vusers": 5,  "script": "checkout.json", "overrides": { "browser": { "device_profile": "iphone_15_pro" } } },
        { "name": "Galaxy S24 WiFi",  "vusers": 5,  "script": "checkout.json", "overrides": { "browser": { "device_profile": "galaxy_s24" } } },
        { "name": "iPad Air WiFi",    "vusers": 5,  "script": "checkout.json", "overrides": { "browser": { "device_profile": "ipad_air" } } }
    ]
}
```

Dashboard renders side-by-side Core Web Vitals per device group — immediate visibility into which device/network combinations produce poor UX.

## Risks

| Risk | Impact | Mitigation |
|:---|:---|:---|
| CPU throttling distorts timing non-linearly | Metrics don't match real devices | Calibrate throttle rates against real device measurements; document as "approximation" |
| PerformanceObserver not available in headless mode | Missing Core Web Vitals | Test headless support early; fall back to Navigation Timing API |
| Client metrics collection adds overhead | Slower action execution | Make collection opt-in; benchmark with/without; keep JS injection minimal |
| Device comparison requires many browser VUsers | High resource cost | Position as canary mode (5-10 VUsers per profile); pair with HTTP bulk load |
| Layer 3 UX Intelligence scope creep | Delays Layers 1-2 | Defer Layer 3 until Stages 1-4 produce real data; treat as separate product phase |

## Completion Bar

### Implemented Today
- A browser script can run against named device profiles with browser-family-aware routing (`chromium` or `webkit`), touch semantics, and per-action client metrics.
- `ClientMetrics` are attached per browser action and remain nil for non-browser protocols.
- Live and archived results expose browser summaries plus engine/platform/device browser segmentation, and the dashboard can drill into archived client metrics with normalized `Platform · Engine · Tier` labels.

### Still Required To Close Stage 2 And Stage 4
- Publish evidence that the configured CPU/network profile differences produce meaningful timing separation on representative applications.
- Add richer dashboard comparison UX beyond the current overall/platform/engine/device rollups, plus per-resource waterfall diagnostics beyond the event-level archive waterfall summary.
- Expand manual browser validation coverage, especially on WebKit hosts with Playwright assets installed.

### Long-Term
- Teams should eventually be able to answer: "Will this release degrade the checkout experience on iPhone SE over 4G?" before deploying to production, with anomaly detection layered on top of the collected metrics.

## Summary
This plan ensures that the mobile emulation work (device profiles, touch events) and the client metrics work (Core Web Vitals, CDP performance) converge toward a unified mobile UX performance platform. The key architectural decision is extending `DeviceProfile` with performance characteristics (CPU throttle, network preset) so that device simulation and measurement are always coupled — you can't measure mobile performance without simulating mobile constraints.

The three existing plans remain the tactical implementation guides:
- `MOBILE_BROWSER_EMULATION_PLAN.md` → Stages 1, 5
- `BROWSER_CLIENT_METRICS_PLAN.md` → Stages 3, 4
- This document → Stage 2 (performance simulation), Stage 6 (UX intelligence), and the strategic alignment between all three
