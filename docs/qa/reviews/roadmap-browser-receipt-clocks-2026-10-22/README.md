# Native document receipt-clock acquisition

This slice adds bounded, process-local diagnostic acquisition to the browser producer. It does **not** qualify a browser baseline or complete the clock/source proof work in [BI-A](../../../ROADMAP_IMPLEMENTATION_SPEC.md#bi-a--qualified-baseline-selection-and-findings).

## Contract and implementation

`BrowserDocumentReceiptEvidence()` takes an owned snapshot without accessing the browser. Each accepted lifecycle begin/end and immutable terminal measurement has a receipt binding its native document, lifecycle and frame IDs; metric receipts also bind the exact measurement ID and revision. Receipts retain the browser-supplied `timeOrigin`/offset separately from native host receipt time and elapsed monotonic nanoseconds from the source generation's origin. The existing v2 raw document timestamps and serialization contract remain unchanged.

A generation has at most 357 receipts: 51 admitted lifecycles, each with one begin, five metric terminals and one end. Overflow and forced lifecycle censorship retain incomplete status. Duplicate finals cannot revise receipts; source reset creates a new monotonic origin and empties the ledger. Reads own their returned slice. No target probes, extra native calls, timers or workers are introduced.

A sampled absolute difference greater than 100 ms between host wall-clock progression and monotonic elapsed time latches `wall_step_observed`. A false flag proves neither continuity between samples nor a maximum drift. Browser timestamps are target-supplied, and one-way delivery gives no maximum transport delay or synchronized mapping. Initial documents can disappear during navigation before delivering their terminal callback; native tests preserve that missingness instead of assuming completion.

## Scope still open

These snapshots are local diagnostic acquisition only. They are not yet retained in run archives, negotiated between mixed-version peers, exported by the API or consumed by RCA. The existing comparison gate remains unavailable. Remaining work includes versioned durable transport, independently established expected-source and terminal closure ledgers, authenticated browser/host/controller mapping with uncertainty over the full measurement window, evidence-digest binding, and real two-run qualification. Receipt counts, collection success and absence of observed clock steps cannot substitute for those proofs.

## Validation and review

- Full affected browser package: 841 passing test cases, three benchmark/platform skips, no failures (102.581 seconds). Native browser execution was required; Chromium 149.0.7827.55 and WebKit 26.5 ran locally on macOS.
- Targeted receipt tests passed, including immutable measurement joins, generation isolation, bounded storage, snapshot ownership, sampled forward/backward wall steps and native missing-terminal disclosure. The same receipt unit/native cases passed under the race detector (5.695 seconds).
- Private HAR API import passed (64.990 seconds): 803 source requests, 476 actions, 479 assertions, 37 extractors, 43 correlations, four warnings, 264 protected requirements and zero DSL validation issues. Source archive hash and every stable summary field match the preceding slice. The historical script hash retains fixture identity variability and is excluded from the summary comparison; no byte-for-byte generated-script equality is claimed. The private capture was neither replayed nor committed.
- The retained 11-action Salesforce home/link-click recording ran with document capture enabled through the product replay manager. Initial navigation returned HTTP 403 Access Denied, so link clicks did not execute. This is a blocked external replay, not positive qualification. The replay manager closed its owned session; native package tests shut down their browser pool.
- Documentation checks passed for 335 active Markdown files. No dashboard, in-app help registry, route or generated artifact changed. The full repository suite remains a CI merge gate; this pass ran the full affected package and the targeted API import regression.
- Two independent agents each completed a clean review and a clean re-review. The local review additionally tightened censorship disclosure after native testing exposed a missing initial-document end receipt.

Raw logs and summary evidence are outside the repository under `/private/tmp/apex-browser-durable-backend/browser-receipts-*`; only this Markdown review record is committed. The unrelated existing promotion evidence directory is preserved.
