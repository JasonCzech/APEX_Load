# Browser baseline original-source qualification — 2026-10-21

## Behavior

RCA browser baseline qualification now checks each shared scenario group's
retained `BrowserAuthoredSources` descriptor alongside its executable script
identity. Both descriptors must pass the existing version/digest validation and
match in original script and structure digests. Equal executable forms alone
cannot establish equal authored journeys. Equal originals cannot override an
existing executable mismatch or missing runtime identity.

Missing or malformed original provenance leaves the affected group's browser
comparison unscored through the existing `cohort_changed` result. An unrelated
group with matching valid original and executable identities remains comparable.
No original identity is synthesized for historical archives. This guard is
consumed only by browser detection; general RCA workload qualification is unchanged.

The archive adapter advances to `archive.7`, preventing new requests from reusing
assessments under the earlier qualification behavior. Stored historical results
remain readable. The estimator remains `browser_baseline.v1`; policy floors,
statistical parameters, archive formats and public API fields are unchanged.

## Validation and review

Latest `origin/main` was fetched and already integrated before validation.

- `go test -race ./pkg/api ./pkg/rca -run
  'Test(RCA|BrowserBaseline|BrowserPolicyMetricFloors|EngineVersion)' -timeout=5m`
  passed: API 133.710 seconds, RCA 1.283 seconds. Tests cover matching source and
  execution, changed source content/structure, missing source on either or both
  sides, malformed descriptors, runtime mismatches, nil/empty inputs, unrelated
  groups, independent result maps and the archive-to-RCA evidence path.
- Documentation checks passed for 329 active Markdown files. Whitespace checks
  passed. No route/API schema changes require generated artifact updates.
- The review traced original-source provenance through archive digest validation,
  baseline materialization and browser-only detection. It checked that original
  identity cannot override executable mismatches, that historical missing data
  stays unscored and that the adapter bump prevents stale assessment reuse. The
  final review round found no issues.

Raw logs are `/private/tmp/apex-browser-durable-backend/authored-baseline-race.log`
and `/private/tmp/apex-browser-durable-backend/authored-baseline-docs.log`.
The full Go suite remains the CI merge gate. There were no UI/import changes;
the preceding slice's private HAR import evidence and earlier browser verification
evidence are preserved, not represented as new runs here.

## Remaining work

This is an additional prerequisite for latest-compatible selection, not its
activation. Clock/source binding, complete source-window qualification, frozen
selection receipts, browser-findings integration and real two-run qualification
remain open. No browser UI, HAR importer or recording behavior changes here.
