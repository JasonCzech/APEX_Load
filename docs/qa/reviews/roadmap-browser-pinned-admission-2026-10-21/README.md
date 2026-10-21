# Browser pinned baseline admission — 2026-10-21

## Scope

BI-A now resolves an omitted RCA baseline from the current run's immutable
`browser_policy` revision when its mode is `pinned_run`. Admission persists the
resolved ID in the existing analysis request and its archive evidence digest in
the existing frozen inputs. Execution never consults a newer policy revision to
select a replacement. Explicit caller baselines retain their existing behavior;
the browser detector still enforces the run's policy independently of general RCA.

Same-project authorization, self-baseline rejection and archive availability
checks apply to policy-selected runs. Both explicit and policy-selected baselines
wait while their archives are finalizing. A changed baseline after admission
remains unavailable evidence instead of being silently replaced. Missing or
invalid policy pins retain the existing disclosed policy-unavailable behavior;
a policy-store failure refuses admission so it can be retried.

The archive adapter identity advances from `archive.5` to `archive.6`; saved
historical assessments remain readable, and queued assessments under an older
engine identity require a new request. The `browser_baseline.v1` estimator,
thresholds, stored schema and API field shapes are unchanged.

## Validation and review

Latest `origin/main` was fetched and already integrated before validation.

- RCA/browser baseline race checks passed: `pkg/api` 161.130 seconds and
  `pkg/rca` 1.092 seconds. The selection covered `TestRCA*`, browser baseline,
  metric-floor, engine-version and embedded API documentation tests.
- The attached private HAR import regression passed without race instrumentation
  in 53.839 seconds: HTTP 200, 803 requests, 476 actions, 327 filtered assets,
  479 assertions, 37 extractors, 43 correlations, four warnings, 264 protected
  requirements and zero DSL validation issues. Its original ZIP hash is unchanged.
  All summary fields match the preceding compatibility run except the existing
  timestamp-sensitive script hash. No captured target was replayed.
- Documentation validation passed for 328 active Markdown files; whitespace
  checks passed. No dashboard, route or API schema changes require regeneration.
  Browser UI validation from earlier slices is not claimed as a fresh run here.
- The full Go suite remains the CI merge gate; this slice used targeted validation.

Review covered selection precedence, immutable policy revisions, queue reuse,
project authorization, finalization, changed baseline evidence, automatic queue
callers and engine-version compatibility. The review/fix cycle added the baseline
finalization guard, corrected stale cadence documentation and corrected test
fixtures to create a separate foreign-project run rather than attempting an
unsupported project move. The final review round found no further issues.

A combined race run with the large private HAR fixture exhausted its five-minute
budget inside import. That attempt is retained and is not a passing race-import
qualification. Separate RCA race checks and the established non-race import
regression passed afterward.

Raw logs are local under `/private/tmp/apex-browser-durable-backend/`:
`pinned-baseline-final-rca-race.log`, `pinned-baseline-private-har.log`,
`pinned-baseline-private-har-summary.json`, `pinned-baseline-docs.log`;
`pinned-baseline-final-race.log` retains the timed-out combined attempt.

## Remaining qualification

This slice does not activate `latest_compatible` search or browser-findings job
selection. Full authored identity, clock/source coverage and comparable-window
qualification remain required before automatic compatible selection. The real
two-run seeded-regression gate and missing external environments remain pending.
No dashboard code, recorder/import code or private HAR fixture was changed.
