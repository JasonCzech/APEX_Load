# Live/grouped analytics and overlays — 2026-10-01

Status: implemented and reviewed; no remaining actionable findings in this slice. Builds on published `3cb32972`. Broad-run limitations and successful reruns are recorded below.

## Delivered scope

- Versioned bounded action/transaction/property/region/dependency publication summaries survive history persistence and hydration; additive generator RPC fields distinguish supported evidence from old/mixed peers. Detailed evidence stays off the metrics WebSocket feed.
- Saved live selections refresh without overlapping requests. Unsupported independent-dimension intersections fail. Grouped archives compute percentiles from raw observations; 20-series, 1,000-bucket and 100,000-observation caps fail explicitly.
- Monitor and associated imported-dataset overlays reuse project authorization, durable artifacts, resource admission and analytical queries. Explicit unit axes preserve distinct measurements; archive lag correlation exposes coverage and unscored evidence.
- Existing saved archive comparisons and run-total panels retain their semantics. IBM MQ is explicitly very low priority.

## Review fixes

- Fixed a distributed aggregate early return that bypassed evidence retention.
- Added fail-closed evidence versions for mixed peers and source-plan runtimes without side-channel summaries.
- Kept group-filtered responses from disclosing unrelated native summaries and compacted terminal resident evidence.
- Validated dataset association before analytical reads; rejected unsupported dataset dimension filters instead of ignoring them.
- Preserved project identity and missing-value quality in shared correlation inputs.
- Rendered each chart line from its own timestamps, so asynchronous overlay timestamps do not introduce artificial breaks in other series.
- Reset incompatible hidden group selections when changing population and corrected live-population labels.
- Added visible chart gaps for uncovered live publication intervals.
- Registered schema release 24 for the additive native interval evidence column, preserving all prior migration fingerprints.
- Added the missing local-auth login to the new browser test before API workspace creation; unauthenticated setup correctly returned 401.

## Validation

- Full Go suite executed. Its only failures were generated QA dashboard freshness and the missing schema release fingerprint. Both were fixed; the complete storage suite and QA freshness tests passed on rerun. API, generator, engine, protocol, runner and script packages passed in the broad run. The broad command itself was not rerun after these metadata fixes.
- Targeted API/storage/generator race tests passed. Native DuckDB import and overlay acceptance passed against real normalized Parquet, including association denial and ten aligned correlation pairs.
- SQLite persistence, clone, prior-schema upgrade, historical-record preservation and reopen checks passed. PostgreSQL integration passed again after schema release 24; the disposable database was removed.
- Dashboard typecheck, lint, build and five new panel tests passed. Two broad unit runs hit separate five-second timeouts in existing SecretBindingReview stress and recordingProxyCommands lock tests. Each complete affected file passed isolated reruns; neither broad unit command is recorded as all-green.
- Dev-admin browser suite: 179 passed, four profile/fixture skips. Local-auth broad suite: 96 passed, one fixture skip, three failures. Its new-test setup failure was fixed by adding login; the existing authoring and debugger timeouts, plus a later loaded-host analytics timeout, passed without changing assertions/timeouts when the full affected files were rerun (seven analytics/authoring tests and three debugger tests passed).
- Generated API contracts, QA dashboard freshness, documentation checks and whitespace checks passed. Broad commands with initial failures are not reported as clean single-pass runs.

Rendered evidence was inspected in both auth profiles: [dev-admin overlay](dev-admin-overlay.png), [local-auth overlay](local-auth-overlay.png).

## Scope limits

Live queries use original publication intervals and fixed elapsed windows; live percentile re-bucketing, independent-dimension intersections and live lag correlation remain unavailable. Source-plan runtimes lacking summaries and older peers return explicit unavailable evidence. Browser transport fixtures exercise saved-definition/UI behavior; real Go fixtures exercise aggregation, persistence, RPC merging and authorization. The 10 GB benchmark remains waived. No live cloud or cross-platform release acceptance is claimed.
