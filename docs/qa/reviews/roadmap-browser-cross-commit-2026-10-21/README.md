# Browser stack cross-commit review — 2026-10-21

Scope: the completed browser stack in draft PRs #261–#263, reviewed on source commit `6f826fc7e` against main `d639a646f`, including interactions with the earlier browser/HAR foundations already on main. This review does not close unfinished roadmap features or external qualification gates.

## Findings and repair

The review found an unbounded keyboard-target aggregation table. Returning only the top 64 targets bounded the response, but did not bound the table accumulated while reading retained archive records. A failing regression reproduced 1,034 retained keys where the new limit is 1,024.

Each of at most 64 heatmaps now retains at most 1,024 distinct keyboard targets. Further gestures for existing retained targets continue to count exactly. Gestures for new targets beyond the limit are counted under `keyboard_targets_truncated`; map notes disclose that the displayed top 64 are selected from the retained targets. Pointer cells and other map cohorts remain independent. Raw archive evidence, interaction wire versions, pattern sampling and capture-loss accounting are unchanged.

The regression covers the aggregation boundary before output truncation, exact repeat counts, independent map cohorts, pointer cells, coverage reconciliation and visible scope notes. Existing dashboard rendering exposes map notes and generic exclusion reasons; report views expose exclusion counts.

## Review rounds

1. Reviewed original-source and authored-step identity, negotiated dispatch/outcome versions, cohort and lifecycle joins, acceptance dependencies, policy selection and immutable pins, tuple panel limits, replay receipts and ownership, shutdown joins, SDK navigation/timing synchronization, HAR omission retention, and consumer disclosure. Earlier panel repairs remain preserved. Found the keyboard aggregation bound issue described above.
2. Reviewed the repair and its callers, including exact retained counts versus excluded gestures, old-reader compatibility, report/dashboard disclosure, policy/create flows and clone boundaries. No additional findings.

3. Final review of the scoped diff, regression, unchanged consumer rendering and validation record found no further issues, including low-severity findings.

## Validation

- Focused heatmap, cohort, qualified-interaction and report race tests: 87 passing test/subtest events across three repetitions.
- Full API and browser package suites: 13465 passing test/subtest events and 24 conditional skips, with both packages passing. Skips do not establish native/provider qualification.
- Cross-package browser/replay/policy race checks across protocol, engine, storage, RCA, generator RPC and record bridge: 550 passing test/subtest events.
- Targeted API shutdown/replay-ownership race checks: 10 passing test/subtest events.
- Private HAR import: all stable summary fields match the retained baseline; randomized script identity is excluded. The ZIP is not committed and its requests were not replayed.
- Documentation checks and whitespace checks pass; all 311 previously tested embedded asset hashes match. No frontend source or generated asset change is included in this repair.

Main was fetched and confirmed integrated before the full API suite. This incremental API-only repair uses the earlier full-repository and browser workflow qualification records as historical evidence; it does not claim another full `./...` or UI cycle.

## Evidence and limits

Raw local logs are under `/private/tmp/apex-browser-cross-commit-review/`: `heatmap-before.log` preserves the expected failing regression; `heatmap-after.jsonl`, `final-tests.jsonl`, `final-race.jsonl`, `recovery-race.jsonl` and `docs.log` preserve validation. Raw evidence is not committed.

The first broader race invocation was interrupted and is not claimed as a completed gate. Fresh completed runs supersede it. Previously tested embedded dashboard assets are unchanged (311 checked hashes); this API-only repair does not claim fresh native-browser or UI qualification. The private HAR is imported only, never replayed.

Windows native-worker CI investigation and exact PR-head CI remain separate release gates. No ownership or cleanup fence was weakened to bypass an unconfirmed CI failure. Separate-host performance, native release lanes, real TruClient exports and deployed recovery qualification remain pending as recorded in the [roadmap](../../../ROADMAP.md).
