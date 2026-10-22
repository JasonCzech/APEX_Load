# Atomic browser evidence promotion — 2026-10-20

A browser-intelligence pass now pins accepted BE-4 evidence for the same binary, commit, engine, profile, platform, runtime/capability digests, metric and measurement algorithm. Dependency verification and insertion share a writer-fenced database transaction. Current support is withdrawn if its dependency is missing, superseded, pending, failed or revoked; historical records remain unchanged. The record digest binds artifacts, actor, sequence and cohort. Separate metric/analysis cohorts remain separate acceptance subjects, and broader withdrawals cover narrower cohorts.

The API requires a backend that advertises the atomic promotion contract. CLI qualification files are bounded JSON objects with duplicate keys preserved for strict server validation. Exact cohort status queries validate only matching subjects. The optional schema columns preserve legacy acceptance history; legacy records cannot qualify new BI promotions. Schema release 72 records the change.

No production acceptance records were written. Tests use temporary SQLite databases and an owned disposable loopback PostgreSQL 16 fixture; this does not certify an external browser cohort, deployed database topology or actual device.

## Validation and review

- Full Go suite: **35,717 passing tests/subtests**, 38 explicit skips; native browser tests required. The supplied private HAR import retains every stable compatibility result.
- Targeted SQLite/PostgreSQL/API/CLI race coverage: **144 passing tests/subtests** over three repetitions, including a concurrent BE withdrawal while promotion waits for the database writer fence.
- Dashboard typecheck, lint, 31 Help unit tests, generated API/TypeScript contracts and build passed. Help workflows passed in both profiles: dev-admin 15 pass/one auth-specific skip; local-auth 16 pass. The narrow Help screenshot was reviewed, and the embedded article matches its source exactly. Documentation checks passed for 302 active Markdown files.
- Review fixed atomic-backend capability, cohort scope, digest precision and status-query findings; the final round found no issues. Local records: `evidence/review-rounds.json`, `evidence/validation-summary.json`, tested source/asset hashes and `evidence/SHA256SUMS`.

Raw evidence remains beside this Markdown review locally; only this record is committed. No private HAR source bytes, production acceptance records or external qualification claims are included.
