# Browser Roadmap

> Status: Roadmap/detail. For current browser and mobile support guidance, start
> with [BROWSER_AND_MOBILE_SUPPORT.md](BROWSER_AND_MOBILE_SUPPORT.md).

Consolidated browser/mobile roadmap: mobile device emulation, client-side metrics,
the UX-platform strategy that ties them together, and browser replay HAR capture.
Merged from the former MOBILE_BROWSER_EMULATION_PLAN,
MOBILE_UX_PLATFORM_STRATEGY, BROWSER_CLIENT_METRICS_PLAN, and
BROWSER_REPLAY_HAR_CAPTURE_PLAN documents (recover the long historical design
sketches from git history). Benchmark procedure lives in
[PERFORMANCE_BENCHMARKS.md](PERFORMANCE_BENCHMARKS.md#browser-benchmark-runbook).

Contents:

1. [Vision and architecture](#vision-and-architecture)
2. [Status snapshot](#status-snapshot-rechecked-2026-09-24)
3. [Device emulation](#device-emulation)
4. [Capacity and density](#capacity-and-density)
5. [Client metrics](#client-metrics)
6. [TrueClient MobileWeb import](#trueclient-mobileweb-import)
7. [Dashboard and UX intelligence](#dashboard-and-ux-intelligence)
8. [Browser replay HAR capture](#browser-replay-har-capture)
9. [Stage map, open items, and risks](#stage-map-open-items-and-risks)

## Vision and architecture

Position APEX Load as a synthetic digital-experience platform: emulate modern
mobile devices (iOS, iPadOS, Android) with high fidelity and measure client-side
user experience under controlled load, before production. This is deliberately
not a full LoadRunner TrueClient clone and not native desktop or native mobile app
automation.

Decisions that stay in force:

- **One protocol.** The DSL `protocol` remains `browser`; no `browser_mobile`,
  `browser_desktop`, or `browser_metrics`. Device behaviour is configuration read
  in `Init()` of `pkg/protocol/browser`; there is no `pkg/protocol/mobileweb`.
- **Explicit DSL fields.** `BrowserConfig` carries `device_profile`, `touch_mode`,
  `orientation`, `client_metrics`, and engine/headless settings as first-class
  fields, not metadata maps.
- **Opt-in touch semantics.** With touch mode active `browser_click` maps to touch
  events; desktop scripts are unaffected.
- **Three separable layers.** Layer 1 device emulation and interaction; Layer 2
  client-side telemetry (Core Web Vitals, navigation/resource timing, JS heap,
  layout/paint, long tasks); Layer 3 UX intelligence. Each must be usable without
  the layer above it.
- **Four fidelity dimensions.** Visual (viewport, UA, scale, orientation),
  interaction (tap/swipe/long-press), performance (CPU throttle, network preset,
  advisory JS heap), and measurement (per-action client metrics).
- **Device profiles carry performance characteristics** (`CPUThrottleRate`,
  `NetworkPreset`, `DeviceTier`), so simulation and measurement stay coupled.
  CPU throttling is applied once at `Init()` and persists for the VUser lifetime.
- **Metrics are per action, not per page**, attached to `protocol.Result` as an
  optional `ClientMetrics` (nil for HTTP/JMS and when disabled, so zero cost) and
  tagged with device profile/tier so results can be segmented by device.
- **Two collection sources**: CDP `Performance.getMetrics` (heap, layout/style
  counts, script/layout/task duration) plus injected PerformanceObserver/Performance
  API scripts (LCP, FCP, CLS, INP, long tasks, navigation/resource timing). Network
  events come from the existing capture. The shipped runtime runs everything through
  Playwright (`playwright-go`) with CDP sessions for Chromium-only telemetry;
  `chromedp` is not a dependency.
- **Benchmark before scale claims.** Browser VUsers cost roughly 150-300 MB each;
  they are diagnostic canaries paired with high-density protocol load (HTTP, JMS,
  WebSocket, gRPC), not a bulk-load engine.
- **Out of scope**: native app testing, sensors, multi-touch beyond pinch-zoom,
  offline mode (use network throttling), visual regression, video recording.

Seams: `pkg/dsl/types.go`, `pkg/protocol/protocol.go`, `pkg/protocol/browser/`
(`browser.go`, `devices.go`), `pkg/engine/config.go` and `metrics.go`,
`pkg/api/runner.go` and `archive.go`, `dashboard/src/types.ts`.

## Status snapshot (rechecked 2026-09-24)

### Implemented

- Named device profiles in `pkg/protocol/browser/devices.go` with browser-family
  metadata, orientation defaults, CPU throttle, network preset, and tier.
- DSL, engine wiring, and validation for `device_profile`, `touch_mode`,
  `orientation`, and `client_metrics`, including profile/engine compatibility checks.
- Touch actions `browser_tap`, `browser_long_press`, `browser_swipe`,
  `browser_set_orientation`, plus click-to-touch mapping in mobile mode.
- Chromium and WebKit both execute through Playwright; WebKit prerequisite
  warnings surface in validation, replay APIs, and the editor.
- Script editor, scenario authoring, scenario library, queued previews, and archive
  comparisons preserve and display browser runtime defaults (profile, orientation,
  touch, headed/headless).
- Per-action `ClientMetrics` (FCP, LCP, CLS, INP, DOM timing, blocking-resource
  counts, long tasks, JS heap, layout/style/task duration, Chromium process
  CPU/RSS), live `browser` and `browser_by_device` summaries, archive persistence,
  canonical `browser_by_engine` / `browser_by_platform` / `browser_by_device`
  rollups, charted LCP/INP/CLS comparison, per-action drilldown, and a capped
  browser resource waterfall. Device cards use a single `Platform · Engine · Tier` label.
- TruClient MobileWeb import MVP (see below).
- Explainable UX intelligence slice: health score, weakest-device callout, and
  threshold-backed issue cards (LCP, INP, CLS, long tasks, blocking resources,
  browser CPU/RSS).
- Opt-in benchmark harness and runbook (`APEXLOAD_BROWSER_BENCHMARKS=1`).

### Still open

- Measured benchmark publication from a controlled hardware matrix; nothing
  measured is checked in, so capacity guidance is conservative and code-backed, not
  empirically calibrated.
- Evidence that CPU/network profile differences produce meaningful timing
  separation on representative applications; decide whether to add memory-pressure
  simulation and publish how approximate simulated profile timings are.
- WebKit process CPU/RSS attribution (the Playwright WebKit path exposes no stable
  process IDs); WebKit stays scoped to page/runtime metrics.
- Broader manual validation on real applications, especially WebKit hosts.
- Representative TrueClient MobileWeb archives for importer hardening.
- Per-resource waterfall rows beyond the capped archived network entries.
- UX intelligence beyond thresholds: frustration/rage-tap detection, heatmaps,
  business-impact scoring, anomaly detection against baselines.

## Device emulation

### Supported browser and device profile envelope

Defined in `pkg/protocol/browser/devices.go` and enforced by DSL and replay validation.

| Profile | Engine | Platform | Tier | Viewport | Scale | Default orientation |
|:---|:---|:---|:---|:---|---:|:---|
| `desktop_1080p` | `chromium` | desktop | high | 1920x1080 | 1.0 | landscape |
| `galaxy_s24` | `chromium` | android | high | 360x780 | 3.0 | portrait |
| `pixel_8` | `chromium` | android | high | 412x915 | 2.625 | portrait |
| `ipad_air` | `webkit` | ipados | mid | 820x1180 | 2.0 | portrait |
| `iphone_15_pro` | `webkit` | ios | high | 393x852 | 3.0 | portrait |
| `iphone_se` | `webkit` | ios | low | 375x667 | 2.0 | portrait |

Enforced rules:

- `iphone_*` and `ipad_*` require `webkit`; `desktop_1080p`, `galaxy_s24`, and
  `pixel_8` require `chromium`.
- `touch_mode` and `orientation` overrides are valid on top of a profile, but the
  profile remains the source of truth for platform, tier, and default viewport.
- Headed and headless are supported for both engines; treat headed as a diagnostic
  workflow, not a scaling target.

### DSL shape

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
      "client_metrics": true,
      "capture_screenshots": "on_error"
    }
  }
}
```

`buildProtoConfig` in `pkg/engine/config.go` forwards `browser_device_profile`,
`browser_touch_mode`, and `browser_orientation`. Init applies profile viewport,
scale, mobile flag, and UA (unless overridden) and enables touch emulation.

### Existing browser protocol baseline

Playwright execution for both engines; headless/headed; custom UA and viewport;
network throttling presets (56K, DSL, T1, 10Mbps) and custom bandwidth/latency;
download allow/block lists; cache control; new-user-per-iteration reset; actions
`browser_navigate`, `browser_click`, `browser_fill`, `browser_select`,
`browser_wait`, `browser_screenshot`, `browser_tap`, `browser_long_press`,
`browser_swipe`; full network event and console capture, DOM summaries, and error
snapshots (screenshot, HTML, console, network).

### Tests and benchmarks

Unit coverage: device lookup, touch dispatch, click-to-tap, orientation override,
config wiring (`pkg/engine/config_test.go`), DSL parsing, TrueClient converter.
Benchmarks (`pkg/protocol/browser/mobile_benchmark_test.go`, opt-in): launch with
device profile p50 < 3 s, touch overhead < 10 ms, Chromium RSS < 250 MB per VUser.
Procedure: [PERFORMANCE_BENCHMARKS.md](PERFORMANCE_BENCHMARKS.md#browser-benchmark-runbook).

## Capacity and density

APEX Load capacity estimates classify scenarios as `http`, `browser`, or `mixed`.
The per-tier figures below are **internal engineering ceilings**
(`browserCapacityProfileForTier` in `pkg/api/capacity_planning.go`), not published
density. The **published density** is the diagnostic canary maximum of **5 browser
VUsers per generator** (`publishedBrowserDensityPerGenerator`;
`pkg/api/browser_benchmark_evidence.go`), with a recommended start of 1-2 and a
maximum of 5. See [PERFORMANCE_BENCHMARKS.md](PERFORMANCE_BENCHMARKS.md#browser-benchmark-artifacts)
and [BROWSER_AND_MOBILE_SUPPORT.md](BROWSER_AND_MOBILE_SUPPORT.md) for the published envelope.

| Device tier | Internal ceiling (VUsers / generator; not published) | Memory / VUser | CPU / VUser |
|:---|---:|---:|---:|
| low | 40 | 180 MB | 0.055 cores |
| mid | 32 | 220 MB | 0.070 cores |
| high | 25 | 260 MB | 0.085 cores |
| unknown | 20 | 280 MB | 0.090 cores |

Runs above the published cap flip the release gate to
`benchmark_evidence_and_override_required` and require
`APEXLOAD_BROWSER_DENSITY_OVERRIDE=true` plus attached benchmark evidence.

Superseded for published guidance: the original per-profile starting ranges
(desktop Chromium 1-15, mobile Chromium 1-10, mobile WebKit 1-5) are engineering
starting points for local, non-published investigation only.

Scaling guidance: raise concurrency only after confirming stable host CPU, memory,
and browser-process telemetry; enable `browser.client_metrics` for benchmark runs;
treat cross-engine comparisons as diagnostic, not as transferable density; re-run
the matrix tests and a local sample-app benchmark after Playwright/WebKit
upgrades; change envelopes only per the benchmark runbook. Recommended operating
model is the hybrid: high-scale HTTP/JMS load plus a few browser canaries with
client metrics; device comparison uses VUser groups (about 5 browser VUsers per
profile) with per-group `device_profile` overrides.

## Client metrics

Goal: the practical equivalent of TrueClient's browser-side timing and artifact
capture for web apps, extending the existing `browser` protocol rather than adding
a separate runtime (execution stays in the Go generator; replay, snapshot, and
metrics surfaces are reused). Alternatives considered: application RUM/OpenTelemetry
instrumentation (scales better but needs app changes and is not self-contained) and
a separate browser worker/sidecar (more capability, second execution model);
neither is pursued now.

Phases:

| Phase | Scope | Status |
|:---|:---|:---|
| 0 | Product boundaries: browser web apps only, v1 metric contract, non-goals | Complete |
| 1 | DSL `browser.client_metrics` and optional `protocol.Result.ClientMetrics` | Complete |
| 2 | Runtime collection: Performance API + CDP, screenshots/HTML/console/network | Mostly complete; WebKit process attribution open |
| 3 | Live and archive aggregation, per-action archived `client_metrics`, bounded payloads | Complete for the current metric contract |
| 4 | Dashboard cards, replay drilldown, comparison charts, authoring/validation | Mostly complete; per-resource waterfall rows open |
| 5 | Benchmark and positioning as a low-concurrency diagnostic mode | Implementation complete, evidence pending |

Tradeoffs: browser concurrency is far below HTTP density; process CPU/RSS
attribution is noisy and OS-specific (Chromium via CDP process info plus OS RSS
lookup); rich artifacts can bloat live WebSocket traffic, so live streams carry
summaries while heavy artifacts stay in replay/archive drilldown. Avoid the
"TrueClient equivalent" naming to avoid native-desktop parity expectations.

Risks: CPU throttling distorts timing non-linearly (calibrate, document as an
approximation); PerformanceObserver gaps in headless (fall back to Navigation
Timing); collection overhead (opt-in, benchmark with and without); Layer 3 scope
creep (defer until Stages 1-4 produce real data).

## TrueClient MobileWeb import

Code-owned scope: `pkg/convert/trueclient/support.go`. Importer:
`pkg/convert/enterprisearchive/zip.go` checks the parsed `.usr` type first.

| `.usr` type | Status | Notes |
|:---|:---|:---|
| `TruClient - Mobile Web` | MVP implemented | Only browser-import target in the MVP; `default.xml` steps convert to browser DSL actions, unsupported constructs stay visible as warnings |
| `TruClient 2.0 - Web` | Planned separately | Tracked in [COMPETITIVE_COMPARISON.md](COMPETITIVE_COMPARISON.md#web-20-browser-profile) as the `web2_browser_profile` scope |
| `TruClient` (legacy desktop) | Out of scope | Returns an explicit out-of-scope error |

| Step | Status | APEX Load shape |
|:---|:---|:---|
| Navigate | Supported | `browser_navigate` |
| Click | Supported | `browser_click` with the captured selector |
| Set / Type | Supported | `browser_fill` |
| Select | Supported | `browser_select` (value, text, or label) |
| Wait | Supported | `browser_wait` (duration, text, visible selector) |
| Verify | Supported | action assertions |
| Start/End Transaction | Supported | transaction metadata; preservation prefixes converted action names |
| Scroll | Supported | `browser_swipe`; direct Scroll and `window.scrollTo`/`scrollBy` |
| Evaluate JavaScript | Review only | Decomposed when simple: jQuery `.val`/`.attr('value', ...)`/`.click()`/`.trigger('click')`, `document.querySelector`, first-match `querySelectorAll`/`getElementById`/`getElementsByName`/`ByClassName`/`ByTagName` (`[0]` or `item(0)`), `document.forms[].elements[]` fill/click, window scroll, `location` navigation. Other shapes stay in guided review because there is no public browser-eval action |

Import responses include `review_sections` (warnings, unsupported steps, preserved
metadata, assumptions) and deterministic `repair_suggestions`; the dashboard offers
draft browser replay validation of converted imports before save. Device profile is
extracted from `.usr` and `default.cfg` runtime settings.

Open: sample-backed hardening for less common object-identification shapes and
non-decomposable JavaScript. Needs 3-5 representative TrueClient MobileWeb archives
as fixtures; the step format is proprietary and undocumented.

## Dashboard and UX intelligence

- Scenario authoring: device preset, orientation, touch, and headed/headless
  selectors; saved defaults flow into queued-run payloads, capacity estimates,
  queued previews, archived comparisons, and scenario cards (browser badge,
  profile, orientation, touch, mode).
- Script editor: device profile selector, orientation toggle, viewport preview at
  the right aspect ratio, mobile/tablet/desktop indicator, touch actions in the
  palette, replay/config validation warnings.
- Archive: device/engine/platform rollups, LCP/INP/CLS comparison, per-action
  drilldown, browser resource waterfall, and explainable UX health scoring with
  weakest-device context.
- Long-term goal: answer "will this release degrade checkout on iPhone SE over 4G?"
  before deploying, with anomaly detection over collected metrics (Layer 3, a
  product evolution to start only once Stages 1-4 produce real data).

## Browser replay HAR capture

Browser replay stays a diagnostic canary workflow; HAR is evidence for debugging,
migration review, and browser-to-HTTP conversion, not a density claim.

**V1: synthetic HAR from replay evidence** (no new required backend endpoint)

- Backend HAR 1.2 writer converts replay `network_entries` across events into
  `log.entries`; `replay.har` is included in the replay ZIP export whenever network
  evidence exists; the Browser Replay cockpit gains `Download HAR`; optional
  artifact metadata (availability, entry count, fidelity label).
- Chromium CDP enrichment: track request ID, URL, method, resource type, headers,
  optional post body, status/status text, MIME type, sizes, timing, failure text;
  correlate `RequestWillBeSent` / `ResponseReceived` / `LoadingFinished` /
  `LoadingFailed`; keep redirects as distinct entries; tolerate partial streams.
- Heavy HAR stays in artifact/export flows, never on the live WebSocket hot path;
  response bodies may be omitted unless already available.

**V2: native Playwright HAR recording** where Playwright owns the context
lifecycle (`RecordHarPath`, `RecordHarMode`, `RecordHarContent`,
`RecordHarOmitContent`, URL filters), saved on context close and bundled in replay
exports, with fidelity labelled `native` vs `synthetic`, size guardrails (content
mode defaults, static-asset filtering, UI warnings). Chromium already runs on
Playwright, so the earlier engine-choice question is resolved; adopting native
recording for both engines remains a V2 decision.

Compatibility: only optional fields are added to Go/TypeScript replay entry types
and artifact manifests; existing replay start/pause/resume/stop/read endpoints and
ZIP exports are unchanged; V2 may add optional start-payload fields with defaults
preserving current behavior.

Tests: Go unit tests for CDP enrichment (success, failure, redirects, missing
response/finish, headers, timing, sizes), HAR 1.2 structure/order/mapping, empty
evidence, manifest metadata, and (V2) context options and export inclusion;
dashboard tests that `Download HAR` shows only with network evidence, ZIP includes
`replay.har`, empty sessions show an unavailable state, and fidelity labels render.
Validation: focused Go tests for `pkg/protocol/browser`, HAR builder and API export;
dashboard tests for `BrowserReplayDebugger` and `ScriptEditor`; `go test ./...`;
`npm run build` in `dashboard`; in-app smoke of Script Editor Debug replay, HAR
availability, ZIP export, and responsive layout.

## Stage map, open items, and risks

| Stage | Scope | Status |
|:---|:---|:---|
| 1 Foundation | Device profile registry, viewport/touch emulation, touch actions, orientation | Complete (gate: mobile CSS and touch events verified before adding performance measurement) |
| 2 Performance simulation | `CPUThrottleRate` and `NetworkPreset` per profile applied in `Init()`; opt-in launch/action/RSS benchmark harness and capacity advice | Implementation complete, evidence pending (calibration, memory-pressure decision, approximation guidance) |
| 3 Client metrics collection | `ClientMetrics` on `protocol.Result`, `browser.client_metrics` flag | Complete for the current metric contract |
| 4 Aggregation and dashboard | Device-segmented aggregation, live/archived metrics, comparison and waterfall views | Complete for the current metric contract |
| 5 TrueClient import | `pkg/convert/trueclient`, device profile extraction, replay validation | Partially complete; sample-backed hardening open |
| 6 UX intelligence | Explainable scoring slice shipped | First slice complete; frustration, heatmap, business-impact, anomaly work is future |

| Risk | Mitigation |
|:---|:---|
| Touch events do not trigger every mobile JS handler | Test against real responsive apps; document limits |
| Device presets go stale | Data-driven, extensible profiles |
| Playwright/CDP behaviour changes | Pin the driver (`PLAYWRIGHT_DRIVER_VERSION` in the Dockerfile); test stable Chromium |
| Memory per browser VUser exceeds expectations | Keep the canary positioning and hybrid HTTP + browser model |
| Device comparison needs many browser VUsers | Canary mode, about 5 VUsers per profile, paired with HTTP load |

External requirements: Chromium/Chrome on generators, Playwright WebKit assets on
hosts running WebKit profiles, controlled benchmark hardware for publishing
density claims, and TrueClient sample archives for importer fixtures. Related:
[PROTOCOL_EXTENSION_PLAN.md](archive/plans/PROTOCOL_EXTENSION_PLAN.md) (SOAP and Java are
separate protocols) and the
[enterprise archive runtime settings reference](COMPETITIVE_COMPARISON.md#enterprise-archive-runtime-settings-reference).
