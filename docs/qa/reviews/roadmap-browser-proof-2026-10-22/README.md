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
