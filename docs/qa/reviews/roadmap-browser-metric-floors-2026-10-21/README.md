# Configured browser metric floors — 2026-10-21

Browser policies can set separate LCP, FCP and INP baseline effect floors. The API and policy editor accept optional values from 50 to 600,000 ms. The existing browser_baseline.v1 estimator applies the largest of the metric floor, overall absolute floor and relative floor. These values are independent of threshold-score metric inclusion and cannot lower existing thresholds.

Runs resolve floors through their immutable policy revision pins; editing a policy later does not rewrite historical inputs. Missing or legacy per-metric configuration keeps existing defaults. Invalid internal floors leave the affected metric unscored. Compatible-run selection and real two-run qualification remain separate work.

## Validation and review

- Full Go suite: **35,729 passing tests/subtests**, 38 explicit skips, zero failures; native browser tests required. The supplied private HAR import retains every stable compatibility result.
- Focused policy/baseline race coverage: **168 passing tests/subtests** over three repetitions, including old pinned revisions, global/relative floor clamps, invalid values and admission policy pins.
- Dashboard typecheck, lint, 37 policy-editor/Help unit tests, generated contracts and build passed. Browser intelligence/editor/Help workflows passed in both profiles: dev-admin 18 pass/one auth-specific skip; local-auth 19 pass. Narrow editor and Help screenshots were reviewed. The embedded article matches the repository source, and documentation links passed.
- Review corrected the roadmap placement/status finding before application; the final round found no issues. Local records: `evidence/review-rounds.json`, `evidence/validation-summary.json`, tested source/asset hashes and `evidence/SHA256SUMS`.

The initial race filter unnecessarily included unrelated generated-code tests and was interrupted; those diagnostics are retained separately. The focused race run and required full Go suite are the acceptance evidence. The first full attempt exposed the independently reviewed [CDP teardown race](../roadmap-browser-cdp-close-2026-10-21/README.md); the successful full run includes that fix. Raw evidence remains local; only this Markdown record is committed. No private HAR source bytes or external qualification claims are included.
