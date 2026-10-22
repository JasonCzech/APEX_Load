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
