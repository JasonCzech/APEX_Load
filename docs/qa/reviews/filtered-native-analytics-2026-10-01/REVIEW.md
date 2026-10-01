# Filtered native analytics — 2026-10-01

Status: implemented and reviewed; no remaining actionable findings in the delivered scope. Builds on published commit `10e9f08e` in the existing implementation worktree.

## Delivered scope

- Optional saved archive queries select action/business population, exact action identity, approved retained property categories, elapsed windows and bucket width. Existing run-total queries retain their behavior.
- A viewer-authorized, bounded archive query streams canonical JSONL observations and computes counts, error percentages and raw nearest-rank latency percentiles. Missing evidence remains missing. Limits: first 24 elapsed hours, 1,000 buckets, 100,000 selected observations, existing scan byte/record budgets, 30-second deadline and shared analytics admission.
- Saved comparisons use the same selection and elapsed window for two project-authorized runs, expose added/removed identities and warn when workload/version compatibility or evidence completeness is not established.
- Native panels show saved selection context and support CSV/SVG as applicable. Chart/table bucket selection opens a paginated inspector using the existing archive transaction API and redacted snapshot/header/body components.
- Updated backlog and capability ledger; broad analytics parity remains partial. General dimension grouping, filtered live timelines and monitor/imported overlays remain open.

## Review and fixes

- Assigned the read-only POST query `results:read`, replacing the inferred `tests:run` service-account permission. Added route-body limits and explicit authorization characterization.
- Added exact action identity and exclusive-end filter options so adjacent bucket inspections reconcile without case collisions or boundary double counting; existing inclusive archive filters remain compatible.
- Reject unsupported dimensions, overlays and multiple-series requests explicitly. Queries exceeding raw limits return errors, not truncated results.
- Added retry controls for busy analytics admission and cancellation guards for stale frontend responses.
- Visual review found insufficient dark-theme contrast and table spacing; added scoped theme-aware panel/form styles and visible saved selection context.

## Validation

Full `go test -ldflags=-w -timeout 20m -p 1 ./...` passed, including generated contracts. Targeted `go test -race -ldflags=-w ./pkg/api ./pkg/storage` for native archive queries and route characterization passed. Documentation checks and `git diff --check` passed. Tests cover raw percentiles, population separation, missing/legacy evidence, query limits and cancellation, saved query round trips, project authorization, second-viewer access, comparison identity changes, and browser save/reopen/inspection in both auth profiles.

An early targeted race build overlapped replacement of embedded dashboard assets and failed to build; it is not counted as a passing test. Subsequent targeted Go builds were stopped to serialize overlapping API compilations; the final serialized race run passed. The first full Go run exposed two stale route characterization expectations (sample path and route count); both were corrected before the final run.

Dashboard validation:

- Typecheck, lint and production build passed; all 444 unit-test files / 4,676 tests passed.
- Broad `npm run test:cycle`: dev-admin 176 passed, 4 skipped, 2 timed out (execution teardown and JMeter import); local-auth 98 passed, 1 skipped. The broad command returned failure and is not recorded as an all-green run.
- Focused final browser reruns: dev-admin 15 passed, local-auth 10 passed, including both previously timed-out workflows. Screenshot-only final reruns passed in both profiles.
- Visual review: [dev-admin](filtered-panel-dev-admin.png) and [local-auth](filtered-panel-local-auth.png).

## Limits

This is archive-first. No live filtered history, arbitrary SQL, cloud acceptance, 10 GB benchmark, automatic regression verdict or new runtime SLA action is claimed. Browser fixtures exercise UI behavior over real saved definitions with controlled archive responses; Go tests exercise actual JSONL aggregation and API authorization.
