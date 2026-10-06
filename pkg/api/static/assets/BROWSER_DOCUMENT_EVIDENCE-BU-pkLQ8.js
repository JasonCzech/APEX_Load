var e=`# Browser document evidence

Status: Guide — optional resource/document evidence v2 and bounded live/archive inspection. This delivered slice is part of the [feature completion program](../FEATURE_COMPLETION_SPEC.md#6-browser-evidence-completion); it does not certify the full browser evidence, intelligence, HAR or performance program.

## Capture and admission

Enable document capture explicitly on the browser script:

\`\`\`json
{
  "config": {
    "browser": {
      "capture_document_metrics": true
    }
  }
}
\`\`\`

Capture is off by default. Explicit capture requires the generator's \`browser_document_evidence_v2\` capability; preparation/Start rejects an unsupported peer. Preparation stages the implementation without target-page observers or user execution. Capture attaches only during execution. Disabling document capture leaves ordinary passive network resource evidence available.

This configuration fragment extends a valid browser script; it is not a standalone executable script. Runtime limits and browser density admission remain in [protocol support](../PROTOCOL_SUPPORT.md#browser-execution-and-mobile-emulation) and the [performance guide](../PERFORMANCE_BENCHMARKS.md#browser-benchmark-artifacts).

## Resource identities and clocks

The v2 contract separates opaque request, frame, document/navigation, redirect and action identities. The current native producer captures request/frame/navigation/redirect lineage and both initiating and completion-observation action IDs. Same-URL concurrent frame requests stay distinct; URL similarity is not a dependency or identity key. Resource document ID and document-relative request offset remain unavailable when the native hooks cannot establish them. A frame or page URL observed at completion does not establish the initiating document.

The native request start epoch is distinct from document-relative offsets and the parent action capture timestamp. A native \`-1\` timing sentinel stays unavailable; a genuinely observed zero remains zero. The producer retains exposed relative phase endpoints, separate request/response byte kinds and observed service-worker true/false. Cache and initiator provenance remain unavailable in this producer. These identities are run lineage, not stable cross-run baseline matching keys. Cross-host clock synchronization is not certified by a UTC timestamp alone.

In-flight requests, metadata rows and evidence buffers have fixed bounds. Overflow increments retained drops/incompleteness before omitting a row. Old-generation callbacks cannot relabel a new context's evidence. Action/document sidecar capture preserves callback ownership and does not add health-probe traffic.

The document summary's \`dropped_observations\` counts only document observations reported lost by the source. Request-identity losses remain a separate raw sidecar counter. Either positive loss marks coverage incomplete, even when a peer omits its incomplete flag. Reaching a provider or measurement-identity ledger limit also marks coverage incomplete; the number of distinct omitted observations remains unknown, so retries do not manufacture a loss count. Incomplete coverage with a zero observation-drop count does not establish complete capture. Coverage changes publish during idle collection and shutdown without requiring an action result; an unchanged unavailable provider does not repeat that publication.

## Document lifecycle population

Document metrics carry metric availability, pinned algorithm/version, source generation, document/lifecycle/measurement identity, increasing revision and observation window. The runtime pins the original \`web-vitals\` 5.1.0 asset and hash in its dependency receipt. Qualified final measurements are aggregated in bounded mergeable histograms; their quantiles are histogram estimates, not exact raw quantiles.

- FCP/TTFB and other exposed page metrics retain their own availability. Browser action latency and legacy action \`Samples\` are different populations.
- Session-window CLS and ranked document INP use the pinned implementation. Hidden checkpoints can be provisional. LCP and terminal checkpoints use their lifecycle boundary rather than arbitrary action completion.
- No-interaction INP is unavailable. Cancellation, crash and source overflow censor unfinished observations. BFCache lifecycle identities remain separate; soft-navigation metrics are unsupported.
- Qualification records the actual browser/version/platform/viewport and applicable device/runtime settings. Unapplied or unknown settings exclude a measurement from qualified final distributions. Routing makes the effective cache disabled; requesting cache does not override that native behavior. A configured emulation new-user flag that did not create a new context is explicitly unapplied.
- Page metric support is separate from runtime process metrics. WebKit CPU/RSS has no fabricated value. Missing, unsupported or censored metrics do not become zero, healthy measurements or a score.

Each summary tuple is limited to one compatible metric/algorithm/cohort/group/region/frame/lifecycle population. Final distribution count equals eligible final observations. Excluded-reason tallies can cover only part of the excluded population; they do not justify a coverage percentage.

Live publications retain **first-seen provisional history**. A measurement can later finalize, so provisional counts overlap later terminal history and are never added to its final population. Archive analysis retains the **latest measurement revision**; its provisional count means pending measurements. These two counts intentionally have different semantics.

## Live, archive and CLI inspection

The existing live run status and completed-run analysis display a document lifecycle panel. Inspect metric tuples for version/hash, cohort, availability and final distributions. The archive panel also exposes at most 256 retained concrete observations with their source IDs, windows and finality; a truncated display is labeled explicitly. This bounded display is not a resource pagination API or whole-page export.

CLI reads the same authorized run resources:

\`\`\`bash
apexctl analysis browser-documents --id RUN_ID --mode live
apexctl analysis browser-documents --id RUN_ID --mode archive
\`\`\`

Use \`--api\`, \`--api-key\` or \`--bearer-token\` when required. Help never prints configured credential values. This command accepts run ID and live/archive mode; metric/time/project filters are rejected rather than silently ignored. Its output labels the population and distinct live/archive provisional semantics, preserves absent legacy evidence and explicit incompleteness, and makes no SLA or health verdict. Required wire fields distinguish omitted counts from observed zero; malformed evidence is refused. The CLI checks live path identities and the actual archive \`manifest.test_id\`; conflicting or malformed identities are refused. A RefKey archive request uses at most one authenticated, bounded run-summary lookup to match its canonical run and any supplied project identity. Canonical matching IDs need no extra read. Output reports \`identity_state\`; older omitted or empty identities retain the requested value as \`historical-requested-unverified\`, which does not attest archive identity or manufacture evidence.

Document evidence is retained independently in immutable \`browser_documents.jsonl\` and \`.parquet\` sidecars referenced by the existing manifest. The controller verifies canonical names, bounded record counts and SHA-256 proofs on the exact opened artifacts. Generator transfer, materialization, retries, report reconstruction and artifact cleanup include these sidecars. Invalid optional records do not create zero-duration transactions or poison unrelated valid action evidence. Explicitly requested missing peer capture remains incomplete after restart or terminal group trimming.

## Qualification and remaining work

The retained [review and acceptance record](../qa/reviews/feature-completion-browser-document-2026-10-05/README.md) separates genuine native acquisition from bounded transport/UI fixtures. Local Chromium and WebKit correctness fixtures cover same-URL frames/reload, no-interaction metrics, action-spanning requests and cancellation/blocked drain. Chromium also exercises genuine layout-shift session ground truth and 50 trusted interaction rank/outlier ground truth. These tests are not performance or whole-page coverage benchmarks.

Real BFCache/hidden-resume cross-engine qualification, broader cache/service-worker/redirect/cross-origin/flood/secret/crash cases, complete resource drill-down, native/synthetic HAR delivery, browser intelligence/baseline/heatmap/journey workflows, source overhead and platform/hardware acceptance remain open. No additional browser-density or real-device fidelity claim is made.
`;export{e as default};