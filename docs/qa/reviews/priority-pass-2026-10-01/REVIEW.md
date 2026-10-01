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

## Items 4–5

Pending implementation and individual review.
