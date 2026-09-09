# Code review and backlog closeout

Reviewed the UX implementation against the original 18 work items, focusing on report evidence, navigation and focus, lifecycle states, entity mutations, accessibility and regression coverage.

## Findings fixed

| Priority | Finding | Resolution |
| --- | --- | --- |
| P1 | Run outcome rendering still dereferenced a missing legacy manifest; null rates could reach formatting code. | Optional manifest access and finite-rate checks; regression covers missing metadata and null rate. |
| P1 | Comparison copy treated missing baseline context as matching, omitted release scope, counted all archive records instead of the selected population, and ignored available revisions. | A pure qualification model checks available context, normalizes duration, compares recorded revisions, and counts the selected population. Unknown metadata remains explicit. |
| P2 | Search results stopped at a Settings section; section remounting discarded pending control focus. | The app carries a focus request across guarded route changes and editor remounts, including Access-owned Settings destinations, then focuses/reveals the matching control. |
| P2 | Failed domain deletion dismissed the confirmation and provided no in-dialog retry; pending requests lacked a duplicate-submission guard. | Pending state and request lock, retained target/error, retry-ready confirmation, and guarded dismissal. |
| P2 | Archive retry banners asserted delayed publication and completion without supporting lifecycle evidence. | Snapshot banners identify unavailable archives and unknown publication progress, while retaining Check again. |
| P2 | Running tests with zero or missing request samples could show reassuring zero performance values. | Explicit no-request-sample presentation and unavailable throughput/error values, without conflating allocated VUsers with executed requests. |
| P2 | Selecting a user did not move keyboard focus into its details; collapsing details had no predictable return focus. | Focus the identified user's heading, retain the shared panel-toggle focus return on collapse. Corrected identity-cell labels for both users and service accounts. |

## Backlog reconciliation

The original audit remains in AUDIT.md. The prior implementation covered all 18 topics, but UX-02, UX-04, UX-05, UX-08 and UX-16 had acceptance gaps; this pass completes those gaps. The implementation also verifies scheduling labels across the New York daylight-saving boundary. No remaining implementation items were identified in the audited backlog. Runtime activation is separate: the existing development controller must be restarted to serve its newly embedded UI/backend changes.

## Verification

Final validation passed against the shared checkout:

- `VITEST_MAX_WORKERS=4 APEXLOAD_E2E_PORT=18181 APEXLOAD_E2E_AUTH_PORT=18182 APEXLOAD_E2E_OUTPUT_ROOT=/tmp/apex-review-final-browser npm --prefix dashboard run test:cycle`: exit 0. Type checking, lint, 4,001 unit tests across 367 files, fresh embedded build, 78 dev-admin workflows and 9 local-auth workflows passed.
- `go test -p 1 -timeout 20m ./...`: exit 0 across the full Go suite, including the concurrent archive-outcome integration changes. The API package passed in 676.597 seconds.
- `git diff --check`: passed.

Browser inspection verified focused, visible Settings search targets from Projects and from Access-owned Authentication, plus user-detail heading focus and the shared panel-toggle return focus. Screenshots are in `verification/`. Regression coverage includes missing report metadata, population-specific comparisons and revisions, deletion failure/retry, zero/missing request samples, cross-page Settings search and daylight-saving labels.

Concurrent validation initially caused resource contention, an API package timeout at the default ten-minute limit, and a browser artifact collision. The final runs bounded unit workers, serialized Go packages with a twenty-minute package timeout, and used separate controller ports and artifact directories. Playwright now accepts `APEXLOAD_E2E_OUTPUT_ROOT` while retaining its existing default. A new search workflow also exposed a page-loading race in its setup; explicit destination-heading waits fixed the test. No product assertions were weakened. All final suites completed successfully.
