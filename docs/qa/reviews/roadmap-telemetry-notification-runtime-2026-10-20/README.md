# Atomic telemetry acceptance notifications — 2026-10-20

This [TEL-B slice](../../../ROADMAP_IMPLEMENTATION_SPEC.md#tel-b--policy-evolution-and-complete-consumers) connects the notification outbox to the exact verified archive publication. The existing catalog completion transaction also commits either the immutable event or a fixed bounded decision. Local completion and recovery from an uploaded archive whose local source is lost use the same boundary.

The execution leader uses bounded leases and the existing notification dispatcher. A Server-owned limit covers preparation and storage completion as well as HTTP dispatch. Recipients are opaque keyed fingerprints of the complete authorized configuration; retry reuses the original payload and recipients, while changed or removed configurations revoke their delivery. Temporary settings failures remain retryable. Final lease/incarnation admission rejects stale or deleted work before dispatch. Already admitted external requests cannot be recalled, and ambiguous receiver completion remains at least once with a stable idempotency key.

The retained run's redaction rules and shared credential scrubber protect the bounded envelope. The viewer-authorized status operation exposes only fixed publication decisions, manifest/event identity, suppression count and delivery totals, with service-account scope checks. No destination, payload or credential is returned. Capacity and rejected projection decisions do not block archive completion indefinitely. Custom stores without the complete atomic capability retain the documented legacy claim-before-dispatch behavior.

Schema release 69 appends to release 68, preserving previous fingerprints. New publication state follows test/project deletion. Historical archives and legacy claims retain their semantics. The [owning guide](../../../guides/TELEMETRY_POLICY_RECEIPTS.md#durable-acceptance-notifications) defines bounds and operational interpretation.

## Validation and review

- Applied API/route/freshness checks: 23 top-level tests and 10 subtests passed.
- Expanded applied route/RBAC/service-account/generated-contract checks: 118 top-level tests and 304 subtests passed.
- Applied runtime race suite: 14 top-level tests and 10 subtests passed, including retry, authorization, deletion, shutdown and bounded preparation observers.
- Applied SQLite/PostgreSQL and schema checks: 17 top-level tests and 90 subtests passed. Schema release 69 preserves earlier release fingerprints and explicitly tests both publication foreign keys.
- Full Go suite: 168 packages, 17,671 top-level tests and 17,350 subtests passed. The test-only private-HAR overlay ran within this gate; all production source came from the installed checkout.
- Actual private ZIP import: HTTP 200, 803 requests, 476 actions, 327 filtered assets, 479 assertions, 37 extractors, 43 correlations, four warnings and zero DSL issues; matching aggregate fields and unchanged original ZIP hash.
- API/TypeScript generation, telemetry inventory, dashboard typecheck/lint/34 Help units/build, both Help auth profiles, documentation checks and evidence checksums passed. Exact logs, input/asset/controller hashes and original failed draft fixtures are retained in the evidence bundle (`SHA256SUMS`).

The private customer ZIP was imported through the real multipart API without replay or saved converted content. Its safe aggregate result matches the preceding compatibility cohort. Random generated script/attestation hashes are excluded from equality claims; the original ZIP hash is unchanged.

Review corrected archive-store authority, capability fallback, final incarnation admission, observer bounds, and fixture shutdown/incarnation setup. Three superseded full-suite attempts exposed stale API/auth inventories and legacy-only failure adapters; their failed logs remain in the evidence bundle, with the first two marked incomplete. Corrected deletion-fence and lease-loss tests passed five race-enabled repetitions across legacy and atomic completion before the broader archive and final integrated gates. Final applied-source and staged review found no remaining actionable findings after the recorded storage, lifecycle, authorization and fixture corrections.

The fixtures use local SQLite, a disposable local PostgreSQL container and local HTTP receivers only. They do not certify external receivers, multi-host failover or reference-host performance. In-run policy segments, the full telemetry consumer/sanitization matrix and other TEL-B requirements remain open.
