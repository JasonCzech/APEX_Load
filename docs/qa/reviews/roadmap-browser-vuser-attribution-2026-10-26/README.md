# Roadmap QA Review — Per-VUser Browser RSS Attribution and v3 Benchmark Contract (`BE-C1`)

- **Date:** 2026-10-26
- **Requirement / Track:** `BE-4` / `BE-C` (`BE-C1`)
- **Worktree / Branch:** `be-c1-vuser-attribution`

## Scope and Problem Statement

Before this slice:

1. `TestBrowserBenchmark` (`pkg/protocol/browser/browser_benchmark_test.go`) and `BenchmarkBrowserRSSPerChromiumDeviceProfile` (`pkg/protocol/browser/mobile_benchmark_test.go`) sampled the combined Playwright driver + Chromium process tree and declared `RSSAttribution: "shared_playwright_driver_descendants_or_driver"`, which mixed shared browser/GPU/utility/driver overhead into per-VUser RSS and did not isolate individual `BrowserContext` renderer processes.
2. `TestBrowserBenchmark` recorded `CaptureOverheadMs` (navigation with document evidence capture vs. plain navigation) rather than action or touch-over-click-baseline overhead (`ActionOverheadTargetMs: 10`).
3. Legacy ambiguous unit fields remained in the contract (`RSSPerVUserTargetMB` on `BrowserBenchmarkAcceptance`, `memory_mib` in `benchmarkHost()`, and `rss_mib/op` in `BenchmarkBrowserRSSPerChromiumDeviceProfile`), capacity planning (`browserCapacityProfileForTier`) used raw decimal MB values (`180`, `220`, `260`, `280`) directly against binary MiB `Generator.MemoryMB` (`8192 = 8 * 1024 MiB`), and `observerMaxVUsers` (`10`) in `pkg/api/goal_controller.go` exceeded `MaximumClaimedVUsersPerGenerator: 5`.

## Implemented Changes

1. **CDP `SystemInfo.getProcessInfo` Per-VUser Renderer Attribution (`pkg/protocol/browser/vuser_attribution.go`):**
   - Added `parseCDPProcessInfo`, `queryBrowserCDPProcessInfo`, `snapshotBaselineRendererPIDs`, `attributeVUserRendererPIDs`, `parseLinuxSmapsPSSBytes`, `readLinuxProcessPSSBytes`, and `computeAttributedVUserMemory`.
   - On Chromium (`desktop_1080p`, `pixel_8`), opens a browser-level CDP session (`Browser.NewBrowserCDPSession()`) and diffs `SystemInfo.getProcessInfo` `renderer` PIDs before and after `BrowserContext` creation/navigation to attribute `per_vuser_rss_bytes` (plus Linux `/proc/<pid>/smaps_rollup` `Pss` in `per_vuser_pss_bytes`) separately from `fixed_overhead_bytes` (Playwright driver + `browser` + `gpu` + `utility` + `zygote` PIDs) with `rss_attribution: "cdp_renderer_per_vuser"`.
   - On WebKit (`desktop_1920x1080`, `iphone_15_pro`), where `NewBrowserCDPSession` and `SystemInfo.getProcessInfo` are unsupported, records `rss_attribution: "unavailable"` and leaves `per_vuser_rss_bytes`, `fixed_overhead_bytes`, and `peak_rss_bytes` `null`.
2. **Action Overhead and `apexload.browser_benchmark.v3` Schema (`pkg/protocol/browser/browser_benchmark_test.go`, `pkg/protocol/browser/mobile_benchmark_test.go`, `pkg/protocol/browser/vuser_footprint_benchmark_test.go`):**
   - Added `ActionOverheadMs` and `ActionOverheadKind` (`click_evidence_capture_overhead` on desktop, `touch_over_click_baseline` on mobile profiles) to `BrowserBenchmarkResult` and `action_overhead_ms/op` to `BenchmarkBrowserTouchOrClickActionLatency`.
   - Bumped `writeBrowserBenchmark` to emit `"schema": "apexload.browser_benchmark.v3"`, removed `info["memory_mib"]` from `benchmarkHost()`, removed `rss_mib/op` from `BenchmarkBrowserRSSPerChromiumDeviceProfile` (reporting `rss_bytes/op`, `per_vuser_rss_bytes/op`, and `fixed_overhead_bytes/op`), and kept `parseBrowserBenchmarkArtifact` compatible with both `v2` and `v3`.
   - Updated `TestBrowserVUserMarginalRSS` (`N = 1..5` concurrent contexts) to attribute renderer `VmRSS` (and Linux `Pss`) plus `FixedOverheadBytes` via `SystemInfo.getProcessInfo` and compute `computeMarginalRSSLinearRegression` in bytes.
3. **API Acceptance Contract, Capacity Unit Reconciliation, and Observer Cap (`pkg/api/browser_benchmark_evidence.go`, `pkg/api/capacity_planning.go`, `pkg/api/goal_controller.go`, `dashboard/src/types/system.ts`):**
   - Bumped `BrowserBenchmarkAcceptance.MeasurementVersion` to `"apexload.browser_benchmark.v3"`, set `RSSAttribution: "cdp_renderer_per_vuser"` (`RSSPerVUserTargetBytes: 250_000_000`) on Chromium profiles and `"unavailable"` (`RSSPerVUserTargetBytes: 0`) on WebKit, removed `RSSPerVUserTargetMB`, and regenerated OpenAPI/TypeScript contracts via `go generate ./pkg/api`.
   - Added `browserDecimalMBToMiB` in `pkg/api/capacity_planning.go` so the decimal MB tier budgets (`180`, `220`, `260`, `280` around `browserTargetRSSBytes = 250_000_000`) are converted into binary MiB (`1 << 20` bytes) before comparing against `Generator.MemoryMB` (`8192` MiB).
   - Lowered `observerMaxVUsers` from `10` to `5` in `pkg/api/goal_controller.go` so observer-group scaling cannot exceed `MaximumClaimedVUsersPerGenerator: 5`.

## Verification and Mutation Check

- **Unit and Contract Tests (`-race -count=2 -shuffle=on`):**
  - `go test -race -count=2 -shuffle=on ./pkg/protocol/browser -run 'TestBrowserBenchmark|TestParseCDPProcessInfo|TestParseLinuxSmapsPSSBytes|TestComputeMarginalRSSLinearRegression|TestMobileBrowserBenchmarkProfilesResolve'`
  - `go test -race -count=2 -shuffle=on ./pkg/api -run 'TestBrowserBenchmarkEvidenceUsesDecimalByteTargets|TestObserverGroup|TestEstimateCapacity|TestBrowserCapacityProfileReconcilesDecimalMBTargetToGeneratorMiB|TestOpenAPISpec'`
  - `go test ./docs/qa -run TestBrowserAcceptanceInventory`
- **Opt-In Local Real-Browser Verification (`darwin/arm64`, Chromium `149.0.7827.55`, WebKit `26.5`, Playwright `1.61.1`):**
  - Command:
    `APEXLOAD_BROWSER_BENCHMARKS=1 APEXLOAD_BROWSER_BENCHMARK_OUT=<scratch>/be-c1-bench APEXLOAD_COMMIT_SHA=$(git rev-parse HEAD) go test ./pkg/protocol/browser -run '^(TestBrowserBenchmark|TestBrowserVUserMarginalRSS)$' -v -timeout 10m`
  - Local `TestBrowserBenchmark` output (`<scratch>/be-c1-bench/browser-benchmark.json`, SHA-256 `69a0a5a8e8f2d7b49b1f6fa6d60972c6922cad5dbdb5fce9be3fe4265fab38b8`):
    - `chromium/desktop_1080p`: `per_vuser_rss_bytes = 99,680,256`, `fixed_overhead_bytes = 396,902,400`, `peak_rss_bytes = 493,256,704`, `rss_attribution = "cdp_renderer_per_vuser"`, `action_overhead_ms = -0.763` (`click_evidence_capture_overhead`), `leftover_processes = 0`.
    - `chromium/pixel_8`: `per_vuser_rss_bytes = 97,402,880`, `fixed_overhead_bytes = 394,608,640`, `peak_rss_bytes = 490,553,344`, `rss_attribution = "cdp_renderer_per_vuser"`, `action_overhead_ms = -1.332` (`touch_over_click_baseline`), `leftover_processes = 0`.
    - `webkit/desktop_1920x1080` and `webkit/iphone_15_pro`: `per_vuser_rss_bytes = null`, `fixed_overhead_bytes = null`, `peak_rss_bytes = null`, `rss_attribution = "unavailable"`, `rss_unavailable_samples = 15/15`, `leftover_processes = 0`.
  - Local `TestBrowserVUserMarginalRSS` (`N = 1..5` concurrent Chromium `desktop_1080p` contexts):
    - `N = 1`: renderer `VmRSS = 91,914,240` B, diffed VUser `91,914,240` B, fixed overhead `392,970,240` B.
    - `N = 5`: renderer `VmRSS = 460,439,552` B, diffed VUser `92,061,696` B, fixed overhead `442,843,136` B.
    - Linear regression (`N = 1..5`): marginal slope `92,135,424` bytes/VUser (`~92.14 MB` decimal), renderer intercept `-237,568` bytes.
- **Overlay Mutation Verification (`go test -overlay`):**
  - Mutant in `pkg/protocol/browser/vuser_attribution.go` (including shared `browser` PIDs inside `RendererPIDs` instead of `SharedBrowserPIDs`) killed by `TestParseCDPProcessInfoAndAttributeVUserRendererPIDs`.
  - Mutant in `pkg/api/goal_controller.go` (`observerMaxVUsers = 10` instead of `5`) killed by `TestObserverGroupMaxVUsersMatchesCanaryEnvelope`.

## Remaining Dependencies (`BE-C2`)

- Local `darwin/arm64` results verify the `apexload.browser_benchmark.v3` attribution and unit contract only; they do **not** certify the **4 vCPU / 8 GB** reference generator cohort (`generator-canary-4vcpu-8gb`).
- Full reference-host qualification (`-count 5`, `TestBrowserBenchmark`, `TestBrowserVUserMarginalRSS`, and acceptance-record promotion) remains open under `BE-C2`.
