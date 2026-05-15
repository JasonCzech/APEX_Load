# Implementation Plan: Mobile Browser Emulation

## Objective
Add mobile device emulation to Stormrunner's existing browser protocol so that the same engine used for desktop browser tests can run mobile web tests with accurate device simulation.

This is positioned as a configuration extension of the existing `pkg/protocol/browser` implementation, not a new protocol. The current implementation now uses a browser-family-aware runtime split: Chromium-backed execution continues through `chromedp`/CDP, while iOS/iPadOS-style WebKit execution uses Playwright. The public DSL surface remains the singular `browser` protocol.

## Relationship To Other Plans
- **Browser Client Metrics** (`docs/BROWSER_CLIENT_METRICS_PLAN.md`): That plan focuses on collecting user-perceived performance telemetry from browser sessions. Mobile emulation adds device simulation beneath it. Both features compose naturally — mobile emulation controls the browser environment, client metrics collect the telemetry.
- **Protocol Extension** (`docs/PROTOCOL_EXTENSION_PLAN.md`): SOAP and Java-over-HTTP are separate protocol implementations. Mobile emulation stays within the existing browser protocol boundary.

## Status Snapshot (May 2026)

### Implemented
- Named device profiles are compiled into `pkg/protocol/browser/devices.go`, including browser-family metadata, orientation defaults, CPU throttle defaults, network presets, and device tiers.
- DSL, engine wiring, and validation support `config.browser.device_profile`, `touch_mode`, `orientation`, and `client_metrics`, including compatibility checks between profiles and browser engines.
- Mobile interaction support is shipped: `browser_tap`, `browser_long_press`, `browser_swipe`, plus automatic click-to-touch mapping in mobile mode.
- WebKit-backed execution is shipped for WebKit device profiles, with actionable install guidance surfaced through validation and replay APIs and the dashboard editor.
- Script-editor authoring and replay flows support browser engine selection, device profiles, touch mode, orientation, and saved-script validation.

### Remaining
- TrueClient MobileWeb import is still unimplemented.
- Published mobile-browser benchmark guidance and hardening work are still open.
- Broader scenario-level/dashboard mobile UX polish is still incomplete compared with the script-editor/browser-replay surfaces.

## Current Baseline

### What The Browser Protocol Already Supports
The existing `pkg/protocol/browser` implementation provides:

- Chromium execution via `chromedp` and WebKit execution via Playwright, both under the same `browser` protocol surface
- Headless and headed modes
- Custom user agent via `config["browser_user_agent"]`
- Custom viewport dimensions via `config["browser_viewport_width"]` and `config["browser_viewport_height"]`
- Network throttling with presets (56K, DSL, T1, 10Mbps) and custom bandwidth/latency
- Download filtering (allow list / block list)
- Cache control, recorded resource filtering, and new-user-per-iteration browser reset
- Actions: `browser_navigate`, `browser_click`, `browser_fill`, `browser_wait`, `browser_screenshot`, `browser_tap`, `browser_long_press`, `browser_swipe`
- Full network event capture, console log capture, DOM summary extraction
- Error snapshots with screenshots, HTML, console, and network evidence
- Per-action browser client metrics when `browser.client_metrics` is enabled

### What The DSL Already Supports
`pkg/dsl/types.go` includes `BrowserConfig` with viewport, user agent, headless, cache, engine, device profile, orientation, touch mode, and client metrics settings. `WebRuntimeConfig` covers network simulation, download filters, and content checks. `WaitCondition` and `BrowserCaptureOptions` are available on actions.

### What Is Missing
- No TrueClient script format import or conversion
- No published mobile-browser benchmark envelope or hardening guidance
- No complete scenario/dashboard device UX across every authoring surface
- No broad real-application validation matrix yet for WebKit- and Chromium-backed mobile profiles

## Design Principles

### 1. Configuration, Not Protocol
Mobile web testing is a browser configuration variant, not a separate protocol. The DSL `protocol` field remains `"browser"`. The device profile configures the browser environment.

### 2. Extend, Don't Fork
All mobile emulation work builds on top of `pkg/protocol/browser/browser.go`. There is no `pkg/protocol/mobileweb` package. Mobile-specific behavior is enabled by configuration flags read during `Init()`.

### 3. Explicit DSL Fields
Device profiles, touch mode, and orientation are modeled as explicit fields in `BrowserConfig`, not hidden in metadata maps. This keeps import/export, validation, and dashboard rendering deterministic.

### 4. Opt-In Touch Semantics
Touch event dispatch is gated behind configuration. Existing desktop browser scripts are unaffected. When touch mode is active, `browser_click` automatically dispatches touch events instead of mouse events unless the action explicitly requests mouse behavior.

### 5. Benchmark Before Scale Claims
Browser VUsers are inherently expensive (~150–300 MB each). Mobile emulation does not change this constraint. Any documentation must position mobile browser testing as a low-concurrency diagnostic mode, consistent with `docs/BROWSER_CLIENT_METRICS_PLAN.md`.

## Scope

### In Scope
- Mobile device profile presets with named device bundles
- CDP device metric emulation (mobile flag, device scale factor, screen orientation)
- Touch event dispatch for tap, swipe, and long-press actions
- Automatic click-to-tap mapping in mobile mode
- DSL extensions for device profile, orientation, and touch mode
- Engine config wiring to pass new settings to the browser protocol
- TrueClient script import for basic step-to-action conversion
- Dashboard device profile selector in scenario creation
- Tests and benchmarks for all new behavior

### Out of Scope For The First Cut
- Native mobile app testing (Android/iOS simulators)
- Camera, GPS, accelerometer, or other sensor emulation
- Multi-touch gestures beyond pinch-zoom
- Offline mode simulation (use existing network throttling instead)
- Visual regression or screenshot comparison tooling
- Video recording of browser sessions

## 1. Device Profile Registry

### Design
A device profile bundles viewport, user agent, device scale factor, touch capability, and default orientation into a named preset. Profiles are compiled into the binary as a lookup table.

### Proposed Profiles

| Profile Name | Width | Height | Scale | Touch | User Agent Fragment |
|:---|:---|:---|:---|:---|:---|
| `iphone_15_pro` | 393 | 852 | 3.0 | yes | iPhone; CPU iPhone OS 17 |
| `iphone_14` | 390 | 844 | 3.0 | yes | iPhone; CPU iPhone OS 16 |
| `iphone_se` | 375 | 667 | 2.0 | yes | iPhone; CPU iPhone OS 15 |
| `galaxy_s24` | 360 | 780 | 3.0 | yes | Linux; Android 14; SM-S921B |
| `galaxy_s23` | 360 | 780 | 3.0 | yes | Linux; Android 13; SM-S911B |
| `pixel_8` | 412 | 915 | 2.625 | yes | Linux; Android 14; Pixel 8 |
| `pixel_7` | 412 | 915 | 2.625 | yes | Linux; Android 13; Pixel 7 |
| `ipad_pro_12` | 1024 | 1366 | 2.0 | yes | iPad; CPU OS 17 |
| `ipad_air` | 820 | 1180 | 2.0 | yes | iPad; CPU OS 17 |
| `galaxy_tab_s9` | 800 | 1280 | 2.0 | yes | Linux; Android 14; SM-X710 |
| `desktop_1080p` | 1920 | 1080 | 1.0 | no | (default Chrome UA) |
| `desktop_1440p` | 2560 | 1440 | 1.0 | no | (default Chrome UA) |

### Implementation Location
New file: `pkg/protocol/browser/devices.go`

```go
type DeviceProfile struct {
    Name              string
    Width             int
    Height            int
    DeviceScaleFactor float64
    Mobile            bool
    HasTouch          bool
    DefaultOrientation string  // "portrait" or "landscape"
    UserAgent         string
}

var deviceProfiles = map[string]DeviceProfile{
    "iphone_15_pro": { ... },
    // ...
}

func LookupDevice(name string) (DeviceProfile, bool) { ... }
func ListDevices() []DeviceProfile { ... }
```

## 2. DSL Extensions

### BrowserConfig Additions
In `pkg/dsl/types.go`, extend `BrowserConfig`:

```go
type BrowserConfig struct {
    // ... existing fields ...
    DeviceProfile string `json:"device_profile,omitempty"` // named device preset
    TouchMode     *bool  `json:"touch_mode,omitempty"`     // enable touch event dispatch
    Orientation   string `json:"orientation,omitempty"`    // "portrait" or "landscape"
}
```

### DSL Script Example

```json
{
  "protocol": "browser",
  "config": {
    "base_url": "https://m.example.com",
    "browser": {
      "device_profile": "iphone_15_pro",
      "orientation": "portrait",
      "touch_mode": true,
      "headless": true,
      "capture_screenshots": "per_action"
    }
  },
  "actions": [
    {
      "name": "open_homepage",
      "type": "browser_navigate",
      "target": "/"
    },
    {
      "name": "tap_menu",
      "type": "browser_tap",
      "selector": "#mobile-menu-toggle"
    },
    {
      "name": "swipe_carousel",
      "type": "browser_swipe",
      "selector": ".product-carousel",
      "params": { "direction": "left", "distance": 300 }
    }
  ]
}
```

## 3. Engine Config Wiring

In `pkg/engine/config.go`, extend `buildProtoConfig` to pass new fields:

```go
if browserCfg.DeviceProfile != "" {
    cfg["browser_device_profile"] = browserCfg.DeviceProfile
}
if browserCfg.TouchMode != nil {
    cfg["browser_touch_mode"] = *browserCfg.TouchMode
}
if browserCfg.Orientation != "" {
    cfg["browser_orientation"] = browserCfg.Orientation
}
```

## 4. Browser Protocol Extensions

### Init — Device Metric Override
In `pkg/protocol/browser/browser.go`, during `Init()`, after existing viewport emulation:

```go
// Apply device profile if configured
if profileName, ok := config["browser_device_profile"].(string); ok {
    if profile, found := LookupDevice(profileName); found {
        b.viewportW = profile.Width
        b.viewportH = profile.Height
        b.deviceScaleFactor = profile.DeviceScaleFactor
        b.mobile = profile.Mobile
        b.touchMode = profile.HasTouch
        if b.userAgent == "" {
            b.userAgent = profile.UserAgent
        }
        if b.orientation == "" {
            b.orientation = profile.DefaultOrientation
        }
    }
}

// CDP device metric override (replaces simple EmulateViewport)
startupActions = append(startupActions,
    emulation.SetDeviceMetricsOverride(
        int64(b.viewportW),
        int64(b.viewportH),
        b.deviceScaleFactor,
        b.mobile,
    ).WithScreenOrientation(&emulation.ScreenOrientation{
        Type:  orientationType(b.orientation),
        Angle: orientationAngle(b.orientation),
    }),
)

// Enable touch emulation if configured
if b.touchMode {
    startupActions = append(startupActions,
        emulation.SetTouchEmulationEnabled(true).WithMaxTouchPoints(5),
    )
}
```

### New Action Types

#### `browser_tap`
Touch-based click replacement for mobile interactions:

```go
case "browser_tap":
    selector := b.actionSelector(action)
    if selector == "" {
        result.ErrorMessage = "browser_tap requires selector or target"
        result.Duration = time.Since(result.StartTime)
        return result, nil
    }
    actionErr = b.dispatchTap(actionCtx, selector)
```

Implementation dispatches `Input.dispatchTouchEvent` with `touchStart` → `touchEnd` sequence at the element's center coordinates.

#### `browser_swipe`
Directional swipe gesture:

```go
case "browser_swipe":
    selector := b.actionSelector(action)
    direction := "left"
    distance := 300
    if d, ok := action.Params["direction"].(string); ok {
        direction = d
    }
    if dist, ok := action.Params["distance"].(float64); ok {
        distance = int(dist)
    }
    actionErr = b.dispatchSwipe(actionCtx, selector, direction, distance)
```

Implementation dispatches `touchStart` → series of `touchMove` → `touchEnd` across the element.

#### `browser_long_press`
Long-press (touch and hold):

```go
case "browser_long_press":
    selector := b.actionSelector(action)
    durationMs := 500
    if d, ok := action.Params["duration_ms"].(float64); ok {
        durationMs = int(d)
    }
    actionErr = b.dispatchLongPress(actionCtx, selector, time.Duration(durationMs)*time.Millisecond)
```

#### Auto Click-to-Tap
When `touchMode` is enabled, `browser_click` automatically dispatches touch events:

```go
case "browser_click":
    selector := b.actionSelector(action)
    if b.touchMode {
        actionErr = b.dispatchTap(actionCtx, selector)
    } else {
        actionErr = chromedp.Run(actionCtx,
            chromedp.WaitVisible(selector, chromedp.ByQuery),
            chromedp.Click(selector, chromedp.ByQuery),
        )
    }
```

### Orientation Toggle
Support runtime orientation changes via a dedicated action:

```go
case "browser_set_orientation":
    orientation := action.Value
    if orientation == "" {
        orientation = action.Target
    }
    actionErr = chromedp.Run(actionCtx,
        emulation.SetDeviceMetricsOverride(
            int64(b.viewportW), int64(b.viewportH),
            b.deviceScaleFactor, b.mobile,
        ).WithScreenOrientation(&emulation.ScreenOrientation{
            Type:  orientationType(orientation),
            Angle: orientationAngle(orientation),
        }),
    )
```

## 5. TrueClient Script Import

### Strategy
TrueClient scripts use a proprietary step-based format stored in the script directory alongside the `.usr` file. The import strategy is:

1. Detect the protocol from the `.usr` file (`Type=TruClient` or `Type=TruClient - Mobile Web`)
2. Parse the TrueClient step data (XML/JSON format within the script folder)
3. Map TrueClient steps to Stormrunner browser DSL actions
4. Extract the device profile from `default.cfg` runtime settings
5. Emit a Stormrunner DSL script with `protocol: browser` and the appropriate `device_profile`

### Step Mapping

| TrueClient Step | Stormrunner Action | Notes |
|:---|:---|:---|
| Navigate | `browser_navigate` | URL from step arguments |
| Click | `browser_tap` (mobile) / `browser_click` (desktop) | Selector from object ID |
| Set / Type | `browser_fill` | Selector + value from arguments |
| Wait | `browser_wait` | End event maps to `wait_for` condition |
| Verify | assertion | Text/element verification |
| Evaluate JavaScript | `browser_eval` (new) | Custom JS execution |
| Scroll | `browser_swipe` | Map scroll to vertical swipe |
| Select | `browser_click` + `browser_fill` | Dropdown interaction |

### Converter Extension
In `pkg/convert/loadrunner/converter.go`, add TrueClient detection:

```go
func detectImportProtocol(functions []ParsedFunction) (string, ...) {
    // Existing HTTP/SOAP detection...

    // TrueClient detection happens at the .usr file level,
    // before function parsing. This is handled in the handler layer.
}
```

New file: `pkg/convert/trueclient/converter.go`

```go
package trueclient

type ConvertResult struct {
    Script   *dsl.Script  `json:"script"`
    Warnings []string     `json:"warnings,omitempty"`
    Stats    ConvertStats `json:"stats"`
}

// ConvertTrueClientScript converts a parsed TrueClient script directory
// into a Stormrunner browser DSL script.
func ConvertTrueClientScript(dir TrueClientDir, opts ConvertOptions) (*ConvertResult, error) {
    // 1. Read .usr file for protocol type and device profile
    // 2. Parse step files for recorded actions
    // 3. Map steps to DSL actions
    // 4. Extract runtime config from default.cfg
    // 5. Build script with browser config and device profile
}
```

### Import Risks
- TrueClient step format is proprietary and not publicly documented
- Object identification methods may not map cleanly to CSS selectors
- End events may require custom `wait_for` logic
- JavaScript evaluation steps need a new `browser_eval` action type

Mitigation: Require 3–5 sample TrueClient MobileWeb scripts as test fixtures before starting this phase. Accept partial import coverage with clear warnings for unmapped steps.

## 6. Dashboard Support

### Create Scenario Modal
When the user selects `protocol: browser`, show a device profile dropdown:

- Device category tabs: Phone / Tablet / Desktop
- Device preset list with preview (viewport dimensions, scale factor)
- Orientation toggle (portrait/landscape)
- Custom device option with manual viewport/scale/UA entry
- Touch mode toggle

### Scenario Cards
Show device profile badge on browser scenario cards:
- Device icon (phone/tablet/desktop)
- Profile name
- Viewport dimensions

### Script Editor
When editing a browser script with a device profile:
- Show viewport preview frame at the correct aspect ratio
- Display mobile/tablet/desktop indicator
- Surface touch-specific action types in the action palette

## 7. Testing Requirements

### Unit Tests

| Test | File | Coverage |
|:---|:---|:---|
| Device profile lookup | `browser/devices_test.go` | All presets resolve, unknown names return false |
| Touch dispatch | `browser/browser_test.go` | Tap, swipe, long-press execute without error |
| Click-to-tap mapping | `browser/browser_test.go` | Click dispatches touch when touchMode is true |
| Orientation override | `browser/browser_test.go` | Portrait/landscape emulation applies correctly |
| Config wiring | `engine/config_test.go` | Device profile, touch mode, orientation propagate |
| DSL parsing | `dsl/types_test.go` | New BrowserConfig fields parse and serialize |
| TrueClient import | `convert/trueclient/converter_test.go` | Step mapping, device detection, warning generation |

### Benchmark Tests

| Benchmark | Measurement | Acceptance |
|:---|:---|:---|
| Browser init with device profile | Time to launch Chromium with full device emulation | < 3s |
| Touch dispatch latency | Overhead of touch events vs. mouse clicks | < 10ms additional |
| Memory per mobile VUser | RSS of headless Chromium with mobile viewport | < 250 MB |

## 8. Implementation Phases

| Phase | Status | Milestone | Deliverables | Exit Criteria |
|:---|:---|:---|:---|:---|
| **Phase 1** | **Complete** | **Device Profiles + Runtime Emulation** | Device profile registry, orientation support, DSL extensions, engine config wiring, browser-family-aware runtime selection, unit tests | Named device profiles launch with the expected viewport, scale, mobile flag, user agent, and browser engine |
| **Phase 2** | **Complete** | **Touch Event Simulation** | `browser_tap`, `browser_swipe`, `browser_long_press` action types, auto click-to-tap mapping, touch emulation, unit tests | Touch actions execute on mobile-configured browser; `browser_click` dispatches touch interactions in mobile mode |
| **Phase 3** | **Partially Complete** | **Dashboard UX** | Script-editor device profile selector, orientation toggle, replay/config validation warnings, mobile/browser settings authoring | Core mobile browser scenarios can be authored and replayed from the dashboard, but broader scenario-level UX is still incomplete |
| **Phase 4** | **Not Started** | **TrueClient Import** | `pkg/convert/trueclient` package, step-to-action mapping, device profile extraction from `.usr` + `default.cfg`, API endpoint, unit tests | A TrueClient MobileWeb `.zip` import produces a runnable Stormrunner browser script with correct device profile and mapped actions |
| **Phase 5** | **Not Started** | **Hardening + Benchmarks** | Memory benchmarks, touch latency benchmarks, documented scale guidance, edge case coverage | Published memory/latency measurements; documented operational guidance for mobile browser VUser density |

## 9. Risks And Mitigations

| Risk | Impact | Mitigation |
|:---|:---|:---|
| TrueClient step format is undocumented | Import coverage may be partial | Collect sample scripts first; accept partial import with clear warnings |
| Touch events don't trigger all mobile JS handlers | Some mobile interactions may not replay correctly | Test against real responsive web apps; provide fallback to direct JS execution |
| Memory per mobile VUser exceeds expectations | Lower concurrency than planned | Document mobile browser VUsers as diagnostic, not bulk-load; recommend hybrid HTTP + browser canary model |
| Device profile presets become stale | User agents and viewport sizes change with new device releases | Make profiles data-driven and easily extensible; support custom profile override |
| CDP emulation API changes | Breaking changes in chromedp dependency | Pin chromedp version; test against stable Chrome releases |

## 10. Dependencies

### Go Packages
- `github.com/chromedp/cdproto/emulation` — device metric override, touch emulation
- `github.com/chromedp/cdproto/input` — touch event dispatch
- Existing: `github.com/chromedp/chromedp`, `github.com/chromedp/cdproto/network`, `github.com/chromedp/cdproto/fetch`, `github.com/chromedp/cdproto/runtime`

### External Requirements
- Chromium/Chrome installed on generator pods (already required for existing browser protocol)
- Playwright WebKit assets installed on the controller host for WebKit-backed mobile/browser runs
- 3–5 sample TrueClient MobileWeb scripts for import test fixtures (Phase 4 blocker)

## Summary
Mobile browser emulation now extends Stormrunner's existing browser protocol with device profiles, touch events, orientation support, browser-family-aware runtime execution, and editor/replay validation guidance. The architecture still stays within the current protocol boundary — no new protocol registration, and no split public DSL surface.

The remaining work is now concentrated in three areas: broader dashboard/scenario UX polish, TrueClient import, and published benchmark guidance. Net-new mobile web tests are already available; legacy script migration and operational scale guidance still need dedicated follow-through.

Related documentation:
- Browser client metrics: `docs/BROWSER_CLIENT_METRICS_PLAN.md`
- Protocol extensions (SOAP, Java): `docs/PROTOCOL_EXTENSION_PLAN.md`
- LoadRunner RTS reference: `docs/LOADRUNNER_RTS_REFERENCE.md`
