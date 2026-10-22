# Browser baseline full measurement windows — 2026-10-21

## Scope

RCA now retains the earliest start and latest end of every final document
measurement contributing to a bucket's p75. Progress revisions and unfinished
measurements remain excluded. Raw archive evidence, native analytics panels,
bucket values and retained document counts are preserved.

Both current and baseline populations require recorded, valid full measurement
windows inside each bucket's admitting steady-load span. A final publication that
lands in a steady bucket does not prove its entire measurement occurred at that
load. Missing or malformed windows, ramp crossings and windows bridging a pause
between equal-load spans leave the comparison unscored, with
`source_window_invalid` or `baseline_source_window_invalid` and no z-score or
expected range. This conservatively refuses the affected comparison rather than
recalculating its distribution after dropping individual documents. Buckets outside
the compared spans remain excluded and cannot poison an otherwise valid comparison.

Window envelopes use stable UTC bucket timestamps, independent of point ordering.
Equal start/end timestamps remain valid; source-window containment has no rounding
tolerance. Malformed contributors cannot be hidden by other valid contributors.
Only emitted populated buckets retain envelopes in the bounded RCA projection.

The `browser_baseline.v1` estimator remains unchanged. Engine v6 prevents new
requests from reusing an older assessment; completed historical results remain
readable. These checks qualify recorded timestamp containment, not synchronization
of producer clocks or complete source coverage. Latest-compatible selection,
browser-findings integration and real controlled two-run qualification remain open.

## Validation and review

Documentation checks passed for 332 active Markdown files; whitespace checks passed.

- Targeted browser-baseline RCA and API checks passed (RCA 0.144 seconds,
  API 4.201 seconds).
- `go test -race ./pkg/rca -timeout=5m` passed in 1.591 seconds.
- The attached private HAR imported successfully in 36.710 seconds: 803 requests,
  476 actions, 479 assertions, 37 extractors, 43 correlations and zero DSL
  validation issues. Its archive digest is unchanged; it was not replayed.
- The archive-to-RCA envelope regression passed explicitly in 1.652 seconds.
  A local review caught its original `_windows_test.go` filename, which Go treated
  as Windows-only. Renaming it to `_window_test.go` made it eligible on this host;
  the explicit run supplements the full suite started before that rename.
- Two independent agents reviewed the implementation and regressions without
  findings. Independent re-review after the test-discovery fix was also clean.
- The full API suite passed in 889.304 seconds, including historical-assessment
  compatibility. Latest main was fetched and already integrated before the full
  Go run; packages ran serially with a private cache.
- Full Go run: 167 packages passed, 10 skipped and 1 failed.
  It encountered one unchanged HTTP/3 fixture timeout:
  `pkg/internal/h3fixture/TestCloseReleasesGoroutines` timed out on its initial
  loopback QUIC GET before reaching the leak assertion. The complete fixture
  package then passed ten consecutive runs in 19.939 seconds. Independent review
  found no dependency on the modified code and no justified fixture change.
  The original failed run is retained; this is not described as a clean full run.

Logs are retained under `/private/tmp/apex-browser-durable-backend/`:
`source-window-targeted.log`, `source-window-race.log`,
`source-window-full.jsonl`, `source-window-envelope.log` and
`source-window-har-summary.json` and `source-window-h3-recheck.log`.
No UI or browser protocol behavior changed; these checks do not claim live
browser or infrastructure qualification.
