# Frozen browser baseline selection — 2026-10-21

Latest-compatible RCA admission now consumes the bounded inventory from the
[qualification foundation](../roadmap-browser-qualification-2026-10-21/README.md).
It refuses overflow, missing current identity, incomplete projections and
unqualified current evidence. It verifies candidate project/application/scenario,
canonical identity, run incarnation, execution generation, terminal state,
archive identity and pinned browser policy before comparing evidence.

Admission inspects the complete bounded inventory under a ten-second cooperative
deadline, using existing integrity-checked archive readers and materialization quotas.
It reads summary and browser-document evidence, not transaction populations.
Database timestamps only determine inspection order. Selection ranks compatible
candidates by the immutable manifest completion instant, with canonical ID as
the tie-breaker, and requires completion strictly before the current run starts.
This preserves nanosecond fences even when PostgreSQL stores microseconds.
Database identity and publication reads include connection-pool waits in that
deadline. Unknown publication state and outstanding jobs (including failed jobs
that can retry) abort admission. The current run is checked before and after
inspection as well as each candidate. Transient errors and concurrent
finalization abort admission rather than silently selecting an older candidate.
Truncated document projections report incomplete evidence; a matching prefix cannot qualify omitted later populations.

The versioned receipt records the policy, inventory count, outcome, rejected
candidates and reasons, and selected canonical ID/evidence digest when available.
Its hash participates in deduplication. The queue freezes the receipt and selected
archive; workers verify those inputs without repeating selection. Assessments
and exported bundles retain the receipt. Explicit caller baselines still take
precedence. Archive adapter version 9 prevents reuse under earlier admission
semantics, while completed historical assessments remain readable.

Current native archives still lack positive browser-to-controller clock and
complete expected-source proof. Their automatic selection therefore records a
refusal; raw observations remain available. Synthetic qualified inputs exercise
the positive selector mechanics, including zero-variance compatible baselines.
This slice does not qualify native production runs or complete BI-A acceptance.

Schema release 76 adds the corrected default-project expression index without
rewriting release 75. Query and index share the same default-project constant.
Legacy NULL/empty project IDs belong to `unassigned`; a separate project named
`default` is excluded.

## Validation

Focused RCA and API tests passed after the initial independent review fixes.
Coverage includes exact offset/nanosecond ordering, stored-precision collisions,
start-boundary refusal, inventory overflow, missing proof, population coverage,
transient failure/cancellation, frozen policy edits, explicit precedence,
deduplication, receipt tampering and exported receipts.
Two independent reviewers reported no remaining findings after fixes for point
truncation, timestamp precision, exported-receipt coverage and legacy project
normalization, publication-state uncertainty and database deadlines. Final targeted
API checks passed (4.005 seconds). SQLite candidate/schema and connection-pool deadline checks passed (0.815 seconds); the same
candidate regression passed on PostgreSQL in an isolated schema (final rerun:
1.345 seconds).
RCA race checks passed (1.639 seconds). Documentation/link checks passed for 334
active Markdown files. The final full Go gate passed: 168 packages passed,
10 skipped, zero failures; 36,243 tests passed and 46 skipped. The API suite
passed in 919.830 seconds. The private HAR retained its original archive digest
and imported 803 requests into 476 actions, with 479 assertions, 37 extractors,
43 correlations and zero DSL validation issues. It was not replayed. Latest main
was fetched and already integrated before the full gate. Session logs stay
outside the repository under `/private/tmp/apex-browser-durable-backend/` with
the `browser-selection-` prefix. The private HAR and Salesforce recording remain
local and are not committed. The final test summary is
`browser-selection-final-summary.json` in the same session directory. The retained
Salesforce recording was replayed through the real manager with stop-on-error; the site refused the initial
navigation with HTTP 403 (Access Denied), so its three clicks did not run. The
manager closed its owned browser session. This is not a successful external
qualification.

Private Go build/test caches were cleared after all validation, reclaiming
approximately 7.6 GiB. Evidence logs, private fixtures, dependencies and deliverables
remain. Shared Go/Docker cache cleanup is deferred while parallel work is active;
no images, containers, volumes or application data were removed. This slice
created no frontend build cache. Native clock and complete expected-source proof
acquisition remains the next implementation slice.
