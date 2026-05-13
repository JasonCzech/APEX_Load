# Browser Client Metrics Plan

## Objective
Add a browser-client metrics capability that serves as Stormrunner's practical equivalent to LoadRunner TrueClient for web applications. The first release should focus on real browser sessions for web apps and capture user-perceived action latency, browser process CPU and memory, and screenshots or related artifacts.

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

## Implementation Phases

### Phase 0: Lock Product Boundaries
- Confirm this feature targets browser web apps first.
- Define the metric contract for v1.
- Set non-goals explicitly so the work does not expand into desktop automation.

### Phase 1: DSL And Result Model
- Add browser metric capture settings to the DSL.
- Extend protocol results with optional browser client metric fields.
- Preserve backward compatibility for existing browser and non-browser scripts.

### Phase 2: Runtime Collection
- Reuse the existing browser protocol runtime.
- Capture post-action timing using browser performance APIs and CDP.
- Sample browser process CPU and memory conservatively.
- Attach screenshots and other artifacts using the current snapshot model.

### Phase 3: Aggregation And APIs
- Add summarized browser KPIs to live metrics.
- Extend archive transaction records for per-action browser client detail.
- Keep payload size controlled so live streaming remains practical.

### Phase 4: Dashboard And Authoring
- Add browser-specific execution cards and charts.
- Reuse browser replay drilldown for screenshots, console, network, and DOM detail.
- Expose capture settings in authoring and validation flows.

### Phase 5: Benchmark And Positioning
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