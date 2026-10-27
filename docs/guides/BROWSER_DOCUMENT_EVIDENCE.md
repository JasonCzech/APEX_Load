# Browser document evidence

Status: Guide — optional document evidence v2, request evidence v3 with request-to-document association, and bounded live/archive inspection. This delivered slice does not certify the full browser evidence, intelligence, HAR or performance program.

## Evidence guide map

| Task | Reference |
| --- | --- |
| Capture document timings and interpret lifecycle populations | [Capture and admission](#capture-and-admission) and [document lifecycle](#document-lifecycle-population) |
| Capture gestures and their provenance | [Interaction capture](BROWSER_INTERACTION_EVIDENCE.md) |
| Inspect bounded archived request evidence | [Retained resource inspection](RETAINED_RESOURCE_INSPECTION.md) |
| Understand HAR staging, recovery, transfer and exports | [HAR ownership contract](BROWSER_HAR_WORKING_OWNERSHIP.md) |
| Interpret scores, journey risk and gesture patterns | [Browser intelligence workflows](BROWSER_INTELLIGENCE_WORKFLOWS.md) |

## Capture and admission

Enable document capture explicitly on the browser script: in the dashboard, turn on **Capture page timings and Web Vitals** in the script's Runtime Settings › Browser, or **Page timings and Web Vitals** in a scenario group's runtime settings (a group's or the scenario's browser section replaces the script's whole browser section). The JSON is:

```json
{
  "config": {
    "browser": {
      "capture_document_metrics": true
    }
  }
}
```

Capture is off by default. Explicit capture requires the generator's `browser_document_evidence_v2` capability; preparation/Start rejects an unsupported peer. Preparation stages the implementation without target-page observers or user execution. Capture attaches only during execution. Disabling document capture leaves ordinary passive network resource evidence available.

This configuration fragment extends a valid browser script; it is not a standalone executable script. Runtime limits and browser density admission remain in [protocol support](../PROTOCOL_SUPPORT.md#browser-execution-and-mobile-emulation) and the [performance guide](../PERFORMANCE_BENCHMARKS.md#browser-benchmark-artifacts).

## Resource identities and clocks

The v2 contract separates opaque request, frame, document/navigation, redirect and action identities. The current native producer captures request/frame/navigation/redirect lineage and both initiating and completion-observation action IDs. Same-URL concurrent frame requests stay distinct; URL similarity is not a dependency or identity key. Resource document ID and document-relative request offset remain unavailable when the native hooks cannot establish them. A frame or page URL observed at completion does not establish the initiating document.

The native request start epoch is distinct from document-relative offsets and the parent action capture timestamp. A native `-1` timing sentinel stays unavailable; a genuinely observed zero remains zero. The producer retains exposed relative phase endpoints, separate request/response byte kinds and observed service-worker true/false.

With document capture enabled, request evidence version 3 adds the issuing document. On Chromium a dedicated CDP session per page joins each request to its CDP record (same page, method, URL and ordinal, checked against Playwright's start time) and takes disk/memory/prefetch cache, service-worker (`fromServiceWorker`), and initiator type/origin provenance from it (`cdp_loader_join`). The document a loader produced is the one whose bootstrap reported its token to that session from an execution context created after the loader's commit; commits, contexts and reports arrive in order on one session, and no clock is compared across browser processes. A context created before the session finished installing its binding proves nothing, so a popup's or new window's first document stays unassociated when it commits before the install completes (later documents are associated), and so may a document committed while the install waits behind another blocking browser call such as a dialog. The session enables the Runtime domain, so console calls and exceptions are reported to it as well: pages that log heavily pay that inspector cost twice while document capture is on, and each such event also crosses the driver connection and is decoded by the generator. On WebKit a non-navigation request joins the one document of its frame holding exactly one Resource Timing entry for its URL (`page_resource_timing_join`); its document offset is that entry's `startTime`, and cache and initiator stay `not_exposed`. Anything ambiguous stays unavailable with its reason (`cdp_unobserved`, `ambiguous_cdp_join`, `document_not_observed`, `ambiguous_document_commit`, `resource_timing_unobserved`, `ambiguous_resource_timing`, `request_start_unavailable`). Records that no association pass examined keep version 2, so older readers still accept them; readers accept versions 2 and 3. These identities are run lineage, not stable cross-run baseline matching keys. Cross-host clock synchronization is not certified by a UTC timestamp alone.

In-flight requests, metadata rows and evidence buffers have fixed bounds. Overflow increments retained drops/incompleteness before omitting a row. Old-generation callbacks cannot relabel a new context's evidence. Action/document sidecar capture preserves callback ownership and does not add health-probe traffic.

The document summary's `dropped_observations` counts only document observations reported lost by the source. Request-identity losses remain a separate raw sidecar counter. Either positive loss marks coverage incomplete, even when a peer omits its incomplete flag. Reaching a provider or measurement-identity ledger limit also marks coverage incomplete; the number of distinct omitted observations remains unknown, so retries do not manufacture a loss count. Incomplete coverage with a zero observation-drop count does not establish complete capture. Coverage changes publish during idle collection and shutdown without requiring an action result; an unchanged unavailable provider does not repeat that publication.

## Document lifecycle population

Document metrics carry metric availability, pinned algorithm/version, source generation, document/lifecycle/measurement identity, increasing revision and observation window. The runtime pins the original `web-vitals` 5.1.0 asset and hash in its dependency receipt. Qualified final measurements are aggregated in bounded mergeable histograms; their quantiles are histogram estimates, not exact raw quantiles.

- FCP/TTFB and other exposed page metrics retain their own availability. Browser action latency and legacy action `Samples` are different populations.
- Session-window CLS and ranked document INP use the pinned implementation. Hidden checkpoints can be provisional. LCP and terminal checkpoints use their lifecycle boundary rather than arbitrary action completion: LCP is final at the first trusted input or when the page is hidden, including when its value did not change there (the library reports only changed values, so the bootstrap drains the library's pending work and then finalizes the unchanged value).
- No-interaction INP is unavailable. Cancellation, crash and source overflow censor unfinished observations. BFCache lifecycle identities remain separate. Soft (same-document) navigations are not measured as navigations, and no separate soft-navigation metrics exist: API views state `browser_soft_navigation: "unsupported"` beside every archive document summary and document comparison (the generator wire format is unchanged, so mixed-version fleets still decode).
- Qualification records the actual browser/version/platform/viewport and applicable device/runtime settings. Unapplied or unknown settings exclude a measurement from qualified final distributions. Routing makes the effective cache disabled; requesting cache does not override that native behavior. A configured emulation new-user flag that did not create a new context is explicitly unapplied.
- Page metric support is separate from runtime process metrics. WebKit CPU/RSS has no fabricated value: its availability is `unsupported`, and even with `APEXLOAD_BROWSER_PROCESS_METRICS` enabled the driver's process tree never stands in for it. Missing, unsupported or censored metrics do not become zero, healthy measurements or a score.

Each summary tuple is limited to one compatible metric/algorithm/cohort/group/region/frame/lifecycle population. Final distribution count equals eligible final observations. Excluded-reason tallies can cover only part of the excluded population; they do not justify a coverage percentage.

Live publications retain **first-seen provisional history**. A measurement can later finalize, so provisional counts overlap later terminal history and are never added to its final population. Archive analysis retains the **latest measurement revision**; its provisional count means pending measurements. These two counts intentionally have different semantics.

## Live, archive and CLI inspection

The existing live run status and completed-run analysis display a document lifecycle panel. Inspect metric tuples for version/hash, cohort, availability and final distributions. The archive panel also exposes at most 256 retained concrete observations with their source IDs, windows and finality; a truncated display is labeled explicitly. This bounded display is not a resource pagination API or whole-page export.

CLI reads the same authorized run resources:

```bash
apexctl analysis browser-documents --id RUN_ID --mode live
apexctl analysis browser-documents --id RUN_ID --mode archive
```

Use `--api`, `--api-key` or `--bearer-token` when required. Help never prints configured credential values. This command accepts run ID and live/archive mode; metric/time/project filters are rejected rather than silently ignored. Its output labels the population and distinct live/archive provisional semantics, preserves absent legacy evidence and explicit incompleteness, and makes no SLA or health verdict. Required wire fields distinguish omitted counts from observed zero; malformed evidence is refused. The CLI checks live path identities and the actual archive `manifest.test_id`; conflicting or malformed identities are refused. A RefKey archive request uses at most one authenticated, bounded run-summary lookup to match its canonical run and any supplied project identity. Canonical matching IDs need no extra read. Output reports `identity_state`; older omitted or empty identities retain the requested value as `historical-requested-unverified`, which does not attest archive identity or manufacture evidence.

Document evidence is retained independently in immutable `browser_documents.jsonl` and `.parquet` sidecars referenced by the existing manifest. The controller verifies canonical names, bounded record counts and SHA-256 proofs on the exact opened artifacts. Generator transfer, materialization, retries, report reconstruction and artifact cleanup include these sidecars. Invalid optional records do not create zero-duration transactions or poison unrelated valid action evidence. Explicitly requested missing peer capture remains incomplete after restart or terminal group trimming.

## Comparison and native analysis

Comparisons, native panels and root-cause analysis read final document measurements only: each document lifecycle metric counts once, at its latest retained state, however many progress revisions it recorded. Provisional and censored measurements never enter a value, and values of different measurement tuples are never merged. A tuple is one metric with one algorithm (and its SHA-256), unit, lifecycle phase, frame scope, engine, device profile and actual runtime cohort, within one group and region.

- **Baseline comparison.** Each saved comparison snapshot lists `browser_documents`: for every baseline and tuple, the eligible final counts and, when the tuple matches exactly, the current and baseline p75 and their difference. Otherwise the row is not compared and names the first difference: `algorithm_mismatch`, `unit_mismatch`, `lifecycle_phase_mismatch`, `frame_scope_mismatch`, `engine_mismatch`, `device_profile_mismatch` or `cohort_mismatch`; a tuple only one run has is `baseline_unmatched` or `current_unmatched`, and a matched tuple without final values is `insufficient_final_documents`. The rows are advisory and are not part of the regression verdict. Groups pair through the comparison policy's group mappings, as transactions do, and each row names its runtime `cohort` digest (in the tuple label and CSV key too). Rows cover each whole run, not the selected measurement windows or load range, and the snapshot says so. The Baseline Comparison workspace and the HTML, PDF and CSV reports (`baseline_browser_document` rows) show them. p75 is the distribution's bucket estimate, at most 20% above the true value.
- **Native archive panels.** Population `browser_document` with `document_fcp`, `document_lcp`, `document_cls` (unit `score`) or `document_inp`, aggregations `p50`, `p75`, `p90`, `p95` and `p99`, and filters `engine`, `device_profile`, `frame_scope`, `lifecycle_phase`, `group`, `region` and `cohort` (the 16-hex cohort digest shown in the series identity). A bucket holds the final measurements whose window closed in it; its `count` is how many. A selection that still spans more than one tuple is refused with its first difference; filter it down to one. `excluded` counts the selection's measurements that are not final eligible values (`unqualified_final`, `unavailable_final`, `provisional`, `censored`). Two runs compare only when they measured the same tuple; otherwise the response says which field differs and baseline derivations are `baseline_not_comparable`. These are archive panels (`POST /api/v1/tests/{id}/archive/native-series`): evidence queries (`POST /api/v1/tests/{id}/native-series`) refuse the population. A saved panel shows the metric's own unit (ms, or score for CLS), states that percentiles are bucket estimates, and offers no transaction drill-down for its buckets. The dashboard's panel editor cannot author these panels yet; they are saved through the dashboard definitions API.
- **Root-cause analysis.** Each tuple of FCP, LCP, CLS and INP becomes a `browser_document` detection series of its final measurements' p75 on the run's interval grid; a bucket without a final document is a gap, never zero. Its measurement identity includes the tuple, so baseline-run judgments pair only the same tuple; in baseline mode the baseline run's own document tuples are read for that pairing. Layout shift is judged on a robust scale never below 0.01, so changes well below it build no onset.
- **Legacy action summaries** stay where they were, labelled legacy: the archive and report Browser Metrics sections say so, report CSV rows carry `metric_basis: legacy_action_summary`, and the run comparison CSV adds `browser_metric_basis: legacy_action_summary`. They are averages over browser actions and never stand in for document measurements.

## Qualification and remaining work

Qualification separates genuine native acquisition from bounded transport/UI fixtures. Local Chromium and WebKit correctness fixtures cover same-URL frames/reload, no-interaction metrics, action-spanning requests and cancellation/blocked drain. Chromium also exercises genuine layout-shift session ground truth and 50 trusted interaction rank/outlier ground truth. These tests are not performance or whole-page coverage benchmarks.

Chromium and WebKit fixtures also cover five hidden/resume cycles (LCP final at `hidden`, progress revisions counted once) and a back/forward-cache entry and restore (the navigation lifecycle final at `pagehide`, then a separate `bfcache_restore` lifecycle in the same document). They dispatch the visibility and page-transition events themselves, because under Playwright a back navigation reloads the page in both engines rather than restoring it from the cache and a headless page is never hidden; the library, bootstrap and evidence path run in the real engine. A WebKit fixture checks that process CPU/RSS stay unavailable with process attribution enabled.

Real (browser-initiated) BFCache/hidden-resume qualification, broader cache/service-worker/redirect/cross-origin/flood/secret/crash cases, complete resource drill-down, baseline/heatmap workflows, source overhead and platform/hardware acceptance remain open. HAR download and export, synthetic HARs ([ownership guide](BROWSER_HAR_WORKING_OWNERSHIP.md#downloads-and-exports)), browser findings and journey accounting were implemented in feature-completion Wave 3; they are covered by fixtures, not qualified on real runs. No additional browser-density or real-device fidelity claim is made.
