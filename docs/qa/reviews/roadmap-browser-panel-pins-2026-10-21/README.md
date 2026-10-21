# Frozen browser comparison panels — 2026-10-21

Explicit query v4 retains evaluated run/publication, tuple and canonical query identities. Read and save validate actual authorized archive bytes. Changed, deleted or inaccessible inputs remain unavailable; opening from another run never retargets the saved comparison. Existing queries and metadata-only historical edits preserve their semantics. Baseline-only cohorts retain inspectable buckets and CSV rows with explicit missing current values/counts. This does not establish baseline eligibility or cross-publication transactionality.

## Validation and review

- Full Go suite: **35,909 passing tests/subtests**, 38 explicit skips, zero failures. The private HAR import stable fields match the prior baseline.
- Focused race checks: **249 passing tests/subtests** over three repetitions, including publication integrity and saved query contracts.
- Full dashboard cycle, both authentication profiles, generated contracts, documentation links and exact embedded Help passed. Two controlled native browser runs exercise explicit older policy revision selection, comparison/save, cross-run reopen, pinned CSV, and deleted-input refusal. Local-auth also verifies scoped viewer inspection and separate write/HAR-download denial. Final typecheck and lint were repeated after the screenshot adjustment. The corrected native workflow was also rerun in both profiles after adding an explicit viewport assertion for the deleted-input screenshot. Final screenshots were inspected; the earlier full-cycle dev-admin capture did not show that offscreen state and is not used as visual proof.
- Final review found no remaining findings. Local evidence includes `evidence/validation-summary.json`, source/asset hashes and `evidence/SHA256SUMS`.

Raw evidence remains local; only this Markdown record is committed. This preserves exact inputs; it does not establish baseline anomaly eligibility or qualify actual devices/external hosts.

## Findings addressed

- **P2 — Baseline-only cohorts:** retain their actual table and CSV buckets, including real zero values, while current values/counts remain explicitly missing.
- **P2 — Combined comparison limits:** enforce the selected series limit (at most 20) and 20,000-row budget over the union of current and baseline inputs, including different runtime cohorts. Both query v3/v4 previews and frozen saves reject an excessive union.
- **P3 — Visual proof:** scroll the deleted-input panel into view and assert its viewport visibility before capturing the screenshot. The corrected workflow passed in both authentication profiles and its captures were inspected.
- **P3 — Future-version fixture:** change the unknown-version test from v4 (now reserved for frozen browser inputs) to v5. Production rejection was correct; the old fixture expected the generic future-version error. The first full run had 35,908 passes, 38 skips and this one failing test; its complete logs and hashes remain local as `before-version-test-fix-*`.
- **P3 — Validation attribution:** capture the committed base alongside source/asset hashes and reject evidence packaging or commit if that base changes during validation.

## Requested public-site recording

An actual APEX recording visited Salesforce home and clicked **What is CRM?**, **What is Salesforce?**, and **Customer Success**. The capture contains three clicks, six steps and 1,207 HAR requests. Its 103,706,431-byte HAR has SHA-256 `b1fd2641e8a3ca3c0c68d404a25bdd32863145bc35ea2312eaf1037f45fe68c3`. Multipart import returned HTTP 200, preserved the capture hash and produced zero DSL validation issues. Capture warnings include bounded response-body omission, background request/console failures, WebSockets and readiness hints.

One sequential replay was attempted and refused by Salesforce with HTTP 403 on initial navigation. **Live Salesforce replay is not qualified.** The successful recording/import does not replace the controlled browser comparisons above. Raw captures, converted DSL, import summary and failed replay report remain local under `output/playwright/salesforce-20261021/`; they are not committed. The original supplied private HAR ZIP has a separate compatibility check.
