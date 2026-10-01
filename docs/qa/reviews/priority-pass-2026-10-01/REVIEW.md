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

## Items 3–5

Pending implementation and individual review.
