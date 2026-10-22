# Browser baseline inventory and qualification — 2026-10-21

## Candidate inventory foundation

The optional storage interface returns bounded canonical run identities for one
project/application/scenario, excluding the current run and non-complete runs.
An indexed ID cursor avoids timestamp-text ordering: legacy SQLite rows may
retain offsets, so completion ranking must compare decoded instants in Go.
At most 128 identities are returned with an explicit overflow flag. A latest
selector must refuse a partial inventory, enforce the current-run start fence,
and validate archived browser compatibility before selecting anything.

Schema release 75 adds the matching inventory index without changing earlier
migration fingerprints. Independent review found no issues after replacing the
initial timestamp-order query. The full storage suite passed in 90.416 seconds;
the earlier run caught a missing schema-release entry, which was fixed before
this passing run. Tests cover scope, self exclusion, status, pagination,
overflow, cancellation and invalid limits. The same exercise subsequently passed
on PostgreSQL 16 in an isolated schema (2.100 seconds), including schema migration
and the expression index; the test schema was removed by its cleanup hook.
The log is `/private/tmp/apex-browser-durable-backend/baseline-candidates-storage.log`.

This is an inventory foundation, not completed latest-compatible admission.
Automatic selection and positive browser clock/source proof acquisition remain
open. Existing source archives and unrelated work are preserved.

## Clock and source-coverage safety gate

Browser document RCA projections now carry separate clock and source-coverage
qualification states. Existing archives have neither browser-to-host calibration
covering each measurement window nor an expected-source/terminal capture ledger;
the adapter therefore reports both states as unavailable. Generator-to-controller
clock observations, local execution, successful collection and no reported drops
cannot establish those proofs.

Both browser baseline comparison and generic in-run browser load scoring refuse
unqualified inputs. Baseline results retain current/baseline bucket and sample
counts without a z-score, expected range or anomaly verdict; section limitations
identify missing clock and source-coverage proof separately. Generic detection
excludes these browser series from load scoring with the same limitations.
Non-browser detection and native/raw browser observations remain unchanged.

Engine v7 prevents reuse of older qualification decisions for new requests while
retaining completed historical assessments. The estimator is still
`browser_baseline.v1`. No new sidecars are consumed by this gate, and no clock
samples are promoted into browser evidence. Future positive qualification must
include every newly consumed receipt in frozen evidence digests and freshness
checks, bind source generations to their actual host/controller clock domains,
and establish complete expected-source populations. It must not extrapolate
clock bounds across unmeasured windows without a justified contract.

Two independent reviewers found no remaining issues after fixing historical
engine-version coverage and using valid generator-clock/collection metadata in
the archive regression. The targeted RCA/API checks passed (API 4.994 seconds),
and the RCA race check passed in 1.577 seconds. Regression coverage includes
unknown qualification on both baseline sides, generic in-run scoring, valid
generator-only calibration, collected sources, local absence, preserved raw
points/counts, non-browser behavior and historical-assessment readability.
Documentation checks passed for 333 active Markdown files.

The full API suite passed in 957.971 seconds. The attached private HAR retained
its original archive digest and imported 803 requests into 476 actions, with
479 assertions, 37 extractors, 43 correlations and zero DSL validation issues.
It was not replayed or copied into the repository. The full Go run passed: 168 packages passed, 10 skipped, zero failures. Latest main was
fetched and already integrated before validation. The previous HTTP/3 fixture
timeout did not recur; that package passed in 2.207 seconds. Logs and the full
summary remain under `/private/tmp/apex-browser-durable-backend/` with the
`browser-qualification-` prefix.
Positive native proof acquisition, frozen latest-compatible selection/rejection
receipts and the controlled real-browser acceptance gate remain unfinished.


The retained Salesforce home-and-three-link recording was attempted once through
the real replay manager, with its 11 converted actions and stop-on-error behavior.
Salesforce refused the initial navigation with HTTP 403 (`Access Denied`). The
manager closed its owned browser session; no bypass or load run was attempted.
This external workflow is not qualified. Its local report and summary are
`qualification-salesforce-report.json` and `qualification-salesforce-summary.json`
under `/private/tmp/apex-browser-durable-backend/`; the raw recording stays local.

Private Go build/test caches and abandoned session build directories were cleaned
after validation, reclaiming approximately 6.3 GiB. Logs, private source fixtures,
dependencies and deliverables remain. Shared Go/Docker caches were left intact
for parallel work; no containers, images or volumes were removed. No frontend
build cache was created in this slice.
