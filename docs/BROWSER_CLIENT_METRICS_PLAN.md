# Browser Client Metrics Plan

## Objective
Add a browser-client metrics capability that serves as Stormrunner's practical equivalent to LoadRunner TrueClient for web applications. The current implementation already covers the browser-side timing and artifact portion of that goal; the remaining gap is mainly browser-process sampling, benchmark publication, and some comparison UX.

This is intentionally not a full TrueClient clone. The current architecture is well suited for browser-side synthetic UX telemetry, but not for native desktop UI automation parity.

## Recommended Direction
Extend the existing browser protocol instead of creating a separate runtime from scratch.

Reasons:

- Execution stays inside the Go load generator.
- Existing browser replay, snapshot, and metrics surfaces can be reused.
- The implementation remains aligned with the current protocol extension model.
- Scope stays bounded enough to be delivered and measured.

## Scope For The First Release

Included:

- Browser action response time
- Navigation and page readiness timing
- Browser process CPU and memory sampling
- Screenshots and HTML snapshots for diagnostics
- Optional console and network evidence
- Live dashboard summaries plus archived per-action detail

Explicitly excluded:

- Native Windows desktop automation
- Full LoadRunner TrueClient parity
- Video capture
- Visual diffing
- High-scale browser load claims without benchmark evidence

## Architectural Stance
The preferred implementation path is to extend the existing browser protocol and keep richer browser metric capture optional.

Primary seams:

- `pkg/dsl/types.go` for explicit browser metric capture settings
- `pkg/protocol/protocol.go` for optional result fields
- `pkg/protocol/browser/browser.go` for runtime collection via chromedp/CDP
- `pkg/engine/metrics.go` for summarized browser KPI aggregation
- `pkg/api/runner.go` for live metric transport
- `pkg/api/archive.go` for archived per-action browser metric persistence
- `dashboard/src/types.ts` and browser replay UI surfaces for presentation

## Status Snapshot (May 2026)

### Implemented
- Product scope is locked to browser web applications running through the existing `browser` protocol.
- The DSL and result model are in place: `config.browser.client_metrics` exists, browser scripts can opt in explicitly, and `protocol.Result` carries optional `ClientMetrics`.
- Runtime collection is in place for browser timing and page-performance telemetry: FCP, LCP, CLS, INP, DOM timing, blocking-resource counts, long-task counts, JS heap, layout/style/task duration, screenshots, HTML, console, and network evidence.
- Aggregation and APIs are in place: live `browser` and `browser_by_device` summaries stream through the API, archive reports expose canonical `browser_by_engine`, `browser_by_platform`, and `browser_by_device` rollups, and archived transaction events persist per-action `client_metrics`.
- Core dashboard and authoring support are in place: script-editor controls, replay/debug drilldown, archive views, run comparison breakdowns, CSV export, and warning flows for WebKit/browser prerequisites.
- Browser metadata presentation has been normalized in the live and archive UI: device cards now show a single canonical `Platform · Engine · Tier` label instead of duplicating partial browser metadata.

### Remaining
- Chromium browser-process CPU and OS-memory sampling are now captured through CDP process information plus OS RSS lookup when `browser.client_metrics` is enabled. WebKit process attribution remains open because the current Playwright WebKit runtime does not expose stable browser process IDs through the public integration path.
- Dedicated comparison charts beyond the current overall/platform/engine/device card breakdowns are still open. Archive event drilldown now includes a compact navigation/resource waterfall summary from persisted client metrics, while richer per-resource waterfall UX remains open.
- Benchmark publication and final positioning guidance for density/overhead are still open.

## Implementation Phases

### Phase 0: Lock Product Boundaries
Status: complete.

- Confirm this feature targets browser web apps first.
- Define the metric contract for v1.
- Set non-goals explicitly so the work does not expand into desktop automation.

### Phase 1: DSL And Result Model
Status: complete.

- Add browser metric capture settings to the DSL.
- Extend protocol results with optional browser client metric fields.
- Preserve backward compatibility for existing browser and non-browser scripts.

### Phase 2: Runtime Collection
Status: mostly complete.

- Reuse the existing browser protocol runtime.
- Capture post-action timing using browser performance APIs and CDP.
- Sample browser process CPU and memory conservatively.
- Attach screenshots and other artifacts using the current snapshot model.

Remaining in this phase:
- extend process attribution to WebKit if a stable Playwright process discovery path is added, or keep WebKit explicitly scoped to page/runtime metrics plus JS heap where available.

### Phase 3: Aggregation And APIs
Status: complete for the current metric contract.

- Add summarized browser KPIs to live metrics.
- Extend archive transaction records for per-action browser client detail.
- Keep payload size controlled so live streaming remains practical.

### Phase 4: Dashboard And Authoring
Status: partially complete.

- Add browser-specific execution cards and charts.
- Reuse browser replay drilldown for screenshots, console, network, and DOM detail.
- Expose capture settings in authoring and validation flows.

Remaining in this phase:
- add charted comparison views and richer per-resource waterfall diagnostics where the current archive/debug surfaces still emphasize summary cards and compact event drilldown.

### Phase 5: Benchmark And Positioning
Status: not complete.

- Benchmark browser density and overhead with client metrics disabled and enabled.
- Document this as a low-concurrency diagnostic mode, not a replacement for Stormrunner's high-scale HTTP engine.

## Tradeoffs And Challenges

### Main Tradeoffs
- Extending the current browser protocol is the fastest path, but browser concurrency will be far lower than HTTP virtual-user density.
- Browser CPU and memory metrics are useful, but Chromium process attribution is noisy and OS-specific.
- Rich artifacts improve debuggability, but screenshots and detailed browser payloads can bloat live WebSocket traffic.

### Main Challenges
- Naming: calling this a TrueClient equivalent will invite expectations around native desktop parity.
- Scale: real browsers are expensive, so the feature should be positioned as synthetic UX telemetry, not bulk load.
- Payload discipline: summarized metrics should stay in live streams, while heavy diagnostic artifacts may need replay-style drilldown paths.

## Alternatives

### Hybrid Model
Run Stormrunner HTTP or JMS load at scale and a small number of browser canaries with client metrics in parallel.

This is the recommended operational model for most teams because it combines backend pressure with user-experience visibility.

### Application Instrumentation
Add browser-side telemetry through OpenTelemetry or RUM-style instrumentation and correlate it with Stormrunner test runs.

This scales better, but it depends on application changes and is not a self-contained synthetic browser capability.

### Dedicated Browser Worker Or Sidecar
Move richer browser automation into a separate worker or sidecar runtime.

This can increase capability, but it introduces a second execution model and more orchestration complexity.

## Recommendation
Pursue a browser-client metrics mode first, using the existing browser protocol as the implementation seam.

This keeps the work aligned with Stormrunner's current architecture, reuses existing browser replay and telemetry surfaces, and creates a practical browser-side client metrics feature without promising unsupported desktop automation scope.
