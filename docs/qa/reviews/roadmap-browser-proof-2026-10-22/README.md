# Browser durable receipt and qualification work

This work implements the remaining receipt transport, conservative full-window mapping and independent source-closure contracts in [BI-A](../../../ROADMAP_IMPLEMENTATION_SPEC.md#bi-a--qualified-baseline-selection-and-findings). This record describes completed slices only; qualification remains unavailable until the transport, expected-source inventory, native lifecycle closure and trusted-domain window checks are all connected.

## Shared receipt contract

The native acquisition DTOs now have a passive owner in `pkg/protocol`; aliases preserve the existing local browser inspection API. The bounded v1 decoder rejects unknown versions, missing/duplicate fields, unknown fields, non-finite browser offsets, foreign identities, inconsistent host elapsed/wall-drift values and conflicting document/lifecycle identity tuples. Empty and partial snapshots remain readable and never imply completeness. The raw `browser_documents_v2` envelope and measurement values are unchanged.

Independent review found that individually valid receipts could disagree on a lifecycle's document, frame or browser time origin. The fix enforces one immutable tuple per lifecycle and per document, including restored lifecycles. Regression tests cover direct cloning and wire decoding while preserving partial snapshots.

Targeted contract/native round-trip tests passed. Both full affected package suites passed (`pkg/protocol`: 0.713 seconds; native-required `pkg/protocol/browser`: 98.923 seconds). Independent re-review found no remaining findings. Evidence is retained outside the repository at `/private/tmp/apex-browser-durable-backend/browser-receipt-contract-*`.

## Remaining integration

- Negotiate and persist separate bounded receipt/closure artifacts; include them in transfer, integrity, recovery and RCA input digests.
- Establish expected execution slots before initialization and retain every terminal outcome and publication obligation, including resets, failed initialization and missing generators.
- Acquire native document expectations independently of JS receipts. Unsupported native discovery remains unavailable.
- Map complete lifetimes with conservative causal bounds in a trusted controller domain. Bounds crossing a pause, transition, restart or unknown interval cannot qualify.
- Run positive and refusal fixtures, the private HAR import regression, retained Salesforce replay, independent slice reviews and final cross-cutting reviews before PR publication.

## Durable receipt transport

Receipt snapshots now have a separate bounded JSONL sidecar. Local and generator archives retain its count and SHA-256, include it in artifact transfer/integrity and RCA input identity, and recover it through the durable source collection cache. Cache filenames follow the manifest, preserving old three-file and document-only four-file caches alongside receipt-only and combined layouts. Receipts do not create action or document measurement samples.

Remote transport requires a fresh advertised capability, traffic-free staging, runtime provider/wiring validation and an exact version acknowledgement. Legacy assignments explicitly suppress the new sidecar. Group, dynamic, parallel and reinitialization configuration carry that choice. Receipt publication is serialized and deduplicated. A failed read, missing sink, failed write or wiring panic leaves sticky receipt-loss state, including a separate manifest flag when an archive sink exists; it never changes the raw v2 measurement population.

Independent review identified missing durable loss disclosure and missing staged-runtime validation. A further review rejected a panic fallback that treated unavailable wiring as version 1. All were fixed, with tests for no publication on unsupported/panicking wiring, retry/deduplication, retirement, local/generator finalization, cache restart with and without receipt payloads, corruption/missing remote artifacts, and RCA digest invalidation. Both independent reviewers then reported zero remaining findings. This slice transports acquisition evidence only; it does not qualify clocks or source closure.

Transport validation passed: targeted engine, generator RPC, API/archive and collection-cache checks (0.884 / 0.754 / 17.191 seconds), and engine receipt race checks (2.276 seconds). Logs: `/private/tmp/apex-browser-durable-backend/browser-receipt-transport-final2.log` and `/private/tmp/apex-browser-durable-backend/browser-receipt-transport-race.log`. Cross-package full-suite validation remains scheduled after the remaining proof slices.

## Causal lifetime and mapping contract

Native receipt v2 adds an optional context lifetime: its source monotonic origin is established before `NewContext`, and its upper bound freezes only after owned native close and worker joins succeed. Concurrent close attempts share context-aware ownership; a cancelled waiter cannot publish closure. Version 2 is negotiated independently, and the explicit v1 projection omits lifetime fields so strict older peers remain readable. Artifact inventory describes the versioned receipt container accurately.

The pure mapper binds an exact final metric identity/revision, capture receipt, navigation or restored-lifecycle start, and raw window end. It returns the entire authority dispatch-before / terminal-acknowledgement-after envelope. No one-way delivery latency, sparse wall-clock sample or interpolation narrows that envelope. A qualifying steady span must contain the whole envelope in the exact same run/incarnation/monotonic domain; adjacent spans cannot be joined across pauses or unknown periods. This contract still needs the independent source ledger and trusted dispatcher acquisition/consumer wiring described above.

Independent reviews fixed strict-v1 compatibility, a missing raw-window-start check, premature closure under concurrent native closes, cancellation while waiting for close ownership, and inaccurate artifact metadata. Both reviewers then returned zero findings. Native Chromium and WebKit tests verify bound acquisition, retention and repeated-close immutability; deterministic fixtures cover pauses, foreign/restarted domains, delayed receipts, missing terminals and altered raw windows. Targeted protocol/browser/engine/generator/API checks passed (0.125 / 11.217 / 0.446 / 0.537 / 2.916 seconds), including HAR ownership regressions. Log: `/private/tmp/apex-browser-durable-backend/browser-causal-final2.log`.

## Independent source journal and native closure

Receipt v3 adds a durable execution-owner journal: slot expectation precedes adapter `Init`, context expectation precedes native context creation, context terminal follows physical close, and slot terminal follows adapter teardown. Each write syncs the sidecar; its initial publication also syncs the run directory and parent. Failed writes and publisher panics prevent admission or invalidate closure. Older negotiated versions receive explicit projections without the new fields.

Chromium closure reconciles an independent native loader census with immutable document identities. This first supported census is limited to a single main-frame page: child frames, additional pages, ambiguous loaders, discovery overflow, lost acquisition, and WebKit remain unavailable. A joined native close can account for a departing document whose JavaScript end receipt was lost; it does not manufacture a metric. Raw document evidence remains unchanged.

The reader requires an independently supplied expected-owner inventory, contiguous journal sequences, successful terminal obligations, matching document/lifecycle identities, consistent same-process monotonic lifetime bounds, and reconciliation of all extra snapshots. Unknown generators or unbound snapshots cannot disappear from the population. The mapper binds the exact final measurement revision and raw window to conservative native creation/receipt-delivery bounds. It never estimates a clock offset from a URL, browser wall time, or one-way delivery.

Independent reviews found and fixed silent child-frame/page-overflow gaps, directory publication durability, impossible lifetime bounds, document identity conflicts, forgotten unbound snapshots, and noncanonical control records. Added tests cover these refusals, v1/v2/v3 projection, publisher failure/panic, engine admission ordering, and native Chromium mapping with silent-child/WebKit refusals. Targeted protocol/browser/engine/generator/API checks passed; protocol and engine journal race checks passed. Logs are under `/private/tmp/apex-browser-durable-backend/browser-source-*`. Engine phase hooks and archive/RCA qualification remain the next integration slice.
