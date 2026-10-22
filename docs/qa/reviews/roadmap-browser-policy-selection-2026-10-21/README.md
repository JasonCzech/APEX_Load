# Immutable browser/journey policy selection — 2026-10-21

New run and revised queued copies can choose exact immutable browser/journey policy revisions. Drafts, copies, held/delayed runs and durable reloads retain selections. Omission preserves defaults; an explicit empty selection clears a copied selection. The server validates project, kind, revision and deletion visibility, computes the digest and freezes pins through the existing admission identity and ownership fences. Failed admission does not expose a candidate receipt; retries do not drift to a newer revision. Already admitted historical pins remain unchanged.

Schema release 73 adds an optional test configuration column. The selected revision and latest tombstone use one SQL snapshot on SQLite and PostgreSQL. Later policy edits do not rewrite a selection; deletion visible at resolution refuses admission. Deletion after that read has the same resolution boundary as existing defaults. Recurring schedule authoring still uses project defaults.

## Validation and review

- Full Go suite: **35,765 passing tests/subtests**, 38 explicit skips, zero failures, with native browser tests required. Private HAR import stable results match the prior baseline.
- Focused race checks: **162 passing tests/subtests** over three repetitions; disposable PostgreSQL race checks: **3** passing tests/subtests.
- The full dashboard cycle ran 725 unit files / 8,086 unit tests and both complete authentication profiles. Its only browser failure was an existing Help heading sampled before ResizeObserver realignment. The separate test-only repair (`6972519be`) passed 20 repetitions in each profile; every other workflow passed in the complete cycle. Generated contracts, exact embedded Help and documentation links passed. Native managed browser chains retain archive/report/HAR evidence; real HTTP run fixtures verify policy pins and admission denial without claiming browser measurements.
- Final review found no remaining issues. Local evidence includes `evidence/validation-summary.json`, source/asset hashes, browser screenshots, the full dashboard-cycle log and `evidence/SHA256SUMS`.

Raw evidence remains local; only this Markdown record is committed. This slice does not qualify native devices, external hosts, TruClient exports or baseline inference.
