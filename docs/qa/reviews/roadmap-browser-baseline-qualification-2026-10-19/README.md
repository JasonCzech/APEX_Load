# Roadmap implementation: browser baseline qualification guards

Date: 2026-10-19. Base: `86a9f5cdb`, with upstream `609bf1b1a` integrated. Scope: a bounded [BI-A](../../../ROADMAP_IMPLEMENTATION_SPEC.md#bi-a--qualified-baseline-selection-and-findings) correctness slice.

## Contract

A configured per-metric effect floor cannot lower the effective timing floor (the larger of the absolute floor and the baseline-relative floor) or CLS floor. Raised policy floors remain effective. Explicit nonpositive/non-finite per-metric floors and overflowing calculations cannot qualify a comparison; other units require a positive catalog/configured floor.

A known loss of document observations in either cohort leaves the comparison unscored with `source_loss` or `baseline_source_loss`, and the section partial. Current/baseline bucket and sample counts, current evidence and baseline identity remain available; an expected range, z-score or anomaly direction is not emitted for that comparison. Both loss caveats remain when both cohorts lost observations. This prevents a small observed delta from claiming a healthy comparison just as it prevents a large delta from claiming a qualified regression.

The `browser_baseline.v1` median-of-bucket-p75 estimator, equal bucket weighting, default 30-bucket minimum, three-sigma threshold, scope/load matching, advisory-only behavior and stored historical results are unchanged. This does not implement per-metric policy editing, `latest_compatible` selection, stable authored identity, missing cadence qualifications or the real two-run browser gate.

## Validation and review

Draft overlays reproduce the lower-floor and known-loss scoring defects against the original source. The complete RCA package passes with the draft. The applied-source full RCA package passed (0.238 s), focused race tests passed (1.147 s), API baseline/policy/evidence compatibility checks passed (1.918 s), and the generated acceptance inventory passed (1.234 s). The 258-file documentation check, help ownership/fragment check and whitespace check passed. The final full Go suite passed: 168 tested packages and 10 skipped/no-test packages, with no failures. The final scoped self-review found no further issues. The [full trace](evidence/baseline-full-go.jsonl.gz), [package outcomes](evidence/baseline-full-go-summary.json), exact commands, source hashes and focused logs are retained; SHA256SUMS covers the review evidence. Raw failures are retained; local deterministic series are not real-browser qualification.
