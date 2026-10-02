# Priority implementation pass — 2026-10-01

Five separate implementation/review commits; no live cloud or 10 GB scale acceptance claimed.

## Item 1 — live analytics foundations

Implemented opt-in completion-cohort buckets from real mergeable distributions, bounded action/property joint summaries, per-source publication deduplication, collection coverage and shared aligned lag correlation. Existing source mode and summary-only history remain compatible.

Review fixes: repair the retained error-count JSON tag and assert error persistence; include assigned peers before first publication; reject malformed/partial digests; preserve cardinality bounds without truncating joint identities; include property/digest retention in memory accounting; avoid re-counting resident distributed peers; preserve missing expected-source evidence and distinguish collection coverage from paired correlation coverage.

Validation: final focused engine/storage/generator/API tests and six dashboard panel tests passed; typecheck, lint, build, generated contracts, documentation and whitespace checks passed. All six affected browser workflows passed in each auth profile. The full affected Go packages and targeted race tests passed. The earlier attempt raced with dashboard asset replacement and was restarted after the build. Follow-up code review found no further actionable issues.

## Item 2 — dimensions, annotations and investigation

Archive grouping/filtering supports network profile, hierarchy and lifecycle phase. Live network-profile marginals reuse existing retained telemetry; live hierarchy/phase and independent dimension intersections remain explicitly unavailable. A shared bounded inspector follows selected series/buckets into raw observations and displays retained phase durations and elapsed milestones without invented server timing.

Timestamped run annotations use project authorization, revision compare-and-swap and schema release 25. Deleted notes retain audit history; run deletion cascades storage cleanup. Notes remain separate from immutable evidence.

Review fixes: preserve exact grouped identity through the inspector; return explicit annotation quota errors; maintain TypeScript target compatibility and separate UI/model exports for lint; expose annotations on the completed-run report route; preserve sub-millisecond execution-commit timestamps for elapsed-zero annotations.

Validation: focused API/storage and schema tests, real PostgreSQL workflow integration, dashboard typecheck/lint/build, inspector/annotation unit tests and all 34 report component tests passed. All seven affected browser workflows passed in both dev-admin and local-auth profiles. Targeted API/storage race tests, regenerated QA artifacts, documentation and whitespace checks passed. The follow-up review found no remaining actionable issues.

## Item 3 — messaging diagnostics

Implemented bounded source/partition/subscription intersections, independent age/offset/acknowledgement moments, typed raw telemetry and versioned generator aggregate transport. Action populations remain available even when business-transaction summaries omit request details. Live snapshots and JSONL/Parquet interval artifacts preserve authoritative rollups; reports describe retained raw observations. Real adapters measure successful client publish/settlement calls; mock and no-ack operations remain unavailable.

Review fixes: prevent raw-sample double counting; preserve zero observations and independent denominators; reject invalid/overflowing moments and counts; bound action maps; deep-clone optional acknowledgements and summaries; avoid classifying gRPC/WebSocket messages as broker evidence; mark mixed legacy archive coverage incomplete; keep missing dimensions renderable; qualify partition-skew goals by source, coverage and capability; label legacy scalar skew unqualified.

Validation: focused protocol/engine/generator/API/storage tests and real Kafka/Event Hubs/Service Bus build-profile tests passed. Dashboard typecheck/lint/build and 76 affected component tests passed. All three diagnostic browser workflows passed in each auth profile. Protocol, engine, generator and API messaging race regressions passed, including legacy archive coverage and capability admission. The final review also fixed the archive fixture timestamp to remove a second-boundary flake. Follow-up review found no remaining actionable issues. Live provider acceptance is not claimed.

## Item 4 — shared scenario assembly and authoring parity

Scenario save, run creation and revisions now use one execution payload builder with independent nested snapshots. The authoring form uses the complete canonical data-pool binding array. Legacy browser checkpoints are upgraded at their read boundary before the first-binding adapter is removed. Other adapters remain until their compatibility fixtures prove they can be removed.

Review fixes: preserve every binding through save/reopen and append new attachments; retain legacy-only checkpoint attachments; make an empty first-row field genuinely optional rather than submitting an invalid empty object; test durable reopen after a full reload and compare storage bindings by identity because storage sorts aliases.

Validation: focused API validation/parsing/runtime/snapshot/data-pool and messaging archive regressions passed. Dashboard typecheck, lint, build and all 4,693 unit tests passed. The complete dev-admin suite passed (182 tests, four expected skips). All five affected authoring/composition/data browser workflows passed in local-auth, including durable three-pool save/reopen. The broader local-auth suite finished with five unrelated infrastructure timeouts; all five unchanged cases passed against a fresh controller, as recorded below. Documentation and whitespace checks passed. The current follow-up review found no further actionable implementation issues.

## Item 5 — preparation recovery

Implemented durable staged allocations and a distinct, capability-gated traffic-free readiness RPC. Bounded peer checks verify exact group counts, unstarted executions, controller generation, live supervision/assignment leases and the immutable peak-capacity claim. Healthy checks refresh the existing stage deadline; missing or semantically expired stages cannot be resurrected. Start and renewal revalidate; revision, reservation and owner loss invalidate preparation and cancel through existing cleanup. The callback is released when the gate exits.

Review fixes: publish placement before the ready gate; prevent the default two-minute generator stage timer from contradicting thirty-minute preparation readiness; enforce deadline timestamps even if timer callbacks are delayed; reject normal/committed/drained/mismatched stages; preserve live deadman and capacity checks; keep RPCs outside the runner lock; recheck local state after network calls; prioritize expiry over readiness refresh. Acceptance fixtures now dial fresh owned clients, use valid protocol/build-profile configuration and shut down before durable-store disposal.

Validation: controller-to-real-gRPC-generator readiness survives a deliberately shorter stage TTL with independent HTTP/TCP counters at zero; Start then executes init/load, while peer drain invalidates without traffic. Local cancellation, disabled-flag cancellation, revision changes, reservation loss, peer loss and expiry passed. Generator guards cover HTTP init, gRPC reflection, WebSocket, PostgreSQL connection, TCP, browser navigation and Kafka staging; the native Kafka/Event Hubs/Service Bus build profile passed. Required-monitor readiness creates no collection jobs. SQLite and real PostgreSQL tests prove stale/successor ownership cannot consume unrecovered reservations and audit recovery is idempotent. Deterministic compute/AWS/Azure/Kubernetes controls passed. Preparation browser acceptance passed in both profiles. Generated API/TypeScript and documentation checks passed. The final full `GOMAXPROCS=4 go test -p 2 ./...` suite passed. Targeted API/storage/generator race tests passed across preparation, annotations, messaging, native analytics overlays, authoring parity and capacity cleanup. The full-suite review caught a stale generated API contract matrix and a foreign-key inventory missing the two annotation references; both were corrected and the gates rerun successfully. The final follow-up review found no further actionable issues. Live provider acceptance remains open.

## Broad dashboard gate

The full cycle passed typecheck, lint, build and 4,693 unit tests; dev-admin completed with 182 passes and four expected skips. Local-auth completed with 97 passes, one expected skip and five unrelated LoadRunner browser setup/login/seeding/reload timeouts. All five unchanged cases passed on a fresh controller (7–9 seconds each), alongside preparation acceptance; the first failures are retained in the flake log. All affected authoring, diagnostics, analysis and preparation workflows passed in each profile. No tests were disabled and no assertions were weakened.

## Final pass gate

All five implementation/review cycles are complete. Generated protobuf/API/TypeScript contracts, documentation inventory and whitespace checks passed. The final full Go suite and targeted race gate passed after the freshness/foreign-key review fixes above. The dashboard results and fresh-controller retries are recorded without claiming an uninterrupted full-cycle pass. No live cloud readiness or 10 GB import acceptance is inferred from local tests.

## Whole-pass review follow-up

A fresh review of all five commits found and corrected:

- A local auto-transaction messaging snapshot wrote to a nil map. Action populations now own the live messaging map without synthetic transaction duplication; a new test reproduced the original panic.
- Raw archive action reports counted mirrored business-transaction messaging evidence twice. Messaging counts now exclude transaction wrappers while preserving existing observation totals.
- Kafka request/reply attributed the reply partition to the request source and discarded publish acknowledgement metadata. Combined direction identities now remain unavailable/incomplete while independent measurements survive.
- Bare messaging source names could pool unrelated protocols or execution groups and hide skew. Protocol/group scope now participates in bounded identities, goal pooling, reports and dashboard rows. Explicit goals require the scoped capability; older unscoped evidence stays readable and unscored.
- Event Hubs sampled batches weighted message volume by action count. Counts now retain actual batch size, with unpinned partition attribution unavailable.
- JMS measured-zero message age lost its availability flag. Valid timestamps now preserve zero with an independent observation count.
- Legacy browser checkpoints lost first-binding replacements/removals when an older canonical array remained. Migration now preserves the old overlay semantics and subsequent bindings; current canonical-only drafts remain unchanged.
- Annotation histories at the original encoded-content quota could no longer be deleted. Immutable content keeps its original quota with bounded additional tombstone space, and actor snapshots are safely bounded.
- Supervised preparations rejected valid STAGED claims by requiring ACTIVE. Readiness now uses the read-only pre-commit validator without activating execution or renewing supervision.
- Distributed messaging archive windows could claim complete coverage when a known peer missed the alignment window, had no intervals, or failed archive collection entirely. Accepted observations remain intact, while known gaps now mark the messaging summaries incomplete and unscored.

Validation: the final `GOMAXPROCS=4 go test -p 2 ./...` passed after the archive coverage fix. Targeted race tests passed across API, storage, generator, engine and messaging protocols, covering preparation, annotations, messaging, native analytics, authoring parity and capacity cleanup. The `kafka_real eventhubs_real jms_servicebus_real` build-profile regressions passed. API/DSL freshness tests, the explicit generated TypeScript check, documentation checks for all 83 active Markdown files and whitespace checks passed.

Dashboard typecheck, lint and build passed, along with all 4,694 unit tests in 448 files. All 13 affected analysis/preparation, diagnostics and scenario-data browser workflows passed per auth profile across the reruns: 12 passed together in each profile, and the corrected legacy migration workflow passed separately in both. Initial browser failures came from outdated scoped-messaging fixtures and an incomplete manually seeded legacy form. The legacy fixture now includes a valid legacy source selection and the old builder's `start_offset` default, waits for save/checkpoint completion and `Editing · Saved`, then uses the supported library navigation before a full reload and reopen. Durable binding assertions remain intact. This follow-up does not claim an uninterrupted full browser-suite or full-cycle pass.

The auto-transaction snapshot regression reproduced the original nil-map panic before correction. New archive coverage tests likewise failed before correction for skewed peer windows, empty known peer archives and uncollected sources; their aligned and fully collected best-effort controls passed. All regressions pass after the fixes. Independent final reviews of analytics/preparation, messaging/archive coverage and authoring/annotations/UI found no additional actionable issues.

Follow-up fixes remain uncommitted. Live cloud acceptance and the waived 10 GB benchmark remain unchanged.
