# Browser baseline bucket timestamps — 2026-10-21

## Scope

Browser baseline comparison now requires unique, nonzero retained bucket timestamps
on each series' declared positive cadence. Duplicate publications cannot manufacture
the thirty-bucket minimum, and off-grid timestamps cannot silently receive equal
bucket weight. Both current and baseline series must qualify; invalid grids produce
an unscored `bucket_grid_invalid` reason with no z-score or expected range.

Whole missing buckets remain missing. Unordered input is sorted without changing
caller-owned evidence, and separate runs can retain independent time-grid origins.
The check rejects duration-overflow timestamp ranges rather than treating a saturated
`time.Duration` as evidence of a regular cadence.

The estimator remains `browser_baseline.v1`: median of current bucket p75 values
against baseline buckets at the same load. Engine identity advances to v5 so that
new requests cannot reuse an older assessment under the new qualification rules;
completed historical assessments remain readable and unchanged.

This is a retained-series qualification guard. It does not establish synchronization
of producer clocks, source-window completeness, compatibility of load windows, or
real browser-cohort qualification. The archive adapter still constructs its grid
from retained interval summaries. Latest-compatible selection and the broader BI-A
clock/source-window acceptance gates remain open.

## Validation and review

- `go test ./pkg/rca -timeout=5m` passed in 0.347 seconds.
- `go test -race ./pkg/rca -timeout=5m` passed in 1.713 seconds.
- The full `go test -overlay=<private HAR overlay> -json -timeout=20m ./...`
  passed: 168 packages passed, 10 skipped, zero failures. The API package passed
  in 877.533 seconds, including historical-assessment compatibility. Latest main
  was fetched and already integrated before the run; packages ran serially.
- The attached private HAR imported successfully in 39.660 seconds, with 803 requests,
  476 actions and zero DSL validation issues. The private archive is not committed
  and was not replayed.
- Documentation checks passed for 331 active Markdown files; whitespace checks passed.
- Two independent agents reviewed code, tests and the qualification boundary with
  no findings. The final precommit review remained clean.

Raw logs and the private import summary are under
`/private/tmp/apex-browser-durable-backend/`: `bucket-grid-targeted.log`,
`bucket-grid-race.log`, `bucket-grid-full.jsonl` and `bucket-grid-har-summary.json`.
No UI behavior or browser protocol changed; these checks do not claim new live
browser or infrastructure qualification.
