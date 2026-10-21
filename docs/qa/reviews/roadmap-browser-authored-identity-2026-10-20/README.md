# Browser authored identity foundation — 2026-10-20

The engine binds browser actions to their exact serialized script and canonical structural DSL location before per-VUser interpolation. Duplicate display names remain distinct. Normal load, initialization/teardown and replay use the same versioned identity; loop iterations, retries and replay ordering do not change it. A changed authored script changes the digest and every dependent step identity. Open-workload iteration copies and observer-identity interpolation retain the original authored script binding; structurally changed copies remain unavailable.

## Contract and scope

BrowserAuthoredStep version 1 contains the script SHA-256, structural path and a domain-separated SHA-256 step ID. Paths contain only lifecycle/control-flow tokens and numeric positions, never names, URLs, selectors or source labels. The protocol action's binding is engine-owned and excluded from JSON, so imported action JSON cannot supply it. Unknown/detached locations, aliased action trees, unsupported serialization and invalid structural bounds remain unavailable; construction cannot publish a partial index.

The immutable per-VUser index uses the existing DSL walker. The walker now checks roots and queued branches against the shared definition budget before allocating their pending paths, including queued siblings. This retains traversal order and the existing definition/depth limits.

This is the engine foundation only. Existing interaction/document records, archive formats, feature negotiation, Help and dashboard assets are unchanged. Authored page identity, retained step identity, actual dispatch timing, document/lifecycle joins and compatible baseline/policy workflows still require their subsequent slices. Historical records receive no invented identity. Local tests do not qualify external hosts, actual devices or source-export fidelity.

## Validation and review

The final full `go test -timeout=20m ./...` run passed with the private HAR import overlay and required native browser tests. Targeted race tests passed. Documentation checks passed for 297 active Markdown files; all 31 Help unit tests passed, and four embedded Help sources matched byte for byte. Browser Help workflows passed in both profiles: 13 dev-admin tests (one auth-specific skip) and 14 local-auth tests.

The supplied HAR retains 803 requests, 476 converted actions, 327 filtered assets, 479 assertions, 37 extractors, 43 correlations, four warnings and zero DSL validation issues. All stable compatibility fields match the previous slice. No private target traffic was replayed or private capture content committed.

Self-review fixed pre-allocation walker bounds, original-script binding across runtime copies and the executable-field coverage guard. A final review found no further issues. The earlier failed full run is retained alongside the corrected final run; it is not counted as passing evidence. See the validation summary (`evidence/validation-summary.json`), review rounds (`evidence/review-rounds.json`), tested source hashes (`evidence/tested-source-hashes-final.json`) and HAR comparison (`evidence/private-har-comparison-final.json`). Qualification remains limited to the local macOS/arm64 environment and the scope above.
