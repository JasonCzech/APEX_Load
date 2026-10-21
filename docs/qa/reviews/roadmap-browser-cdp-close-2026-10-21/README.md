# HAR publication after a concurrent CDP detach — 2026-10-21

The full browser metric-floor validation exposed a native Chromium managed-run failure: context close completed while diagnostic CDP detachment returned `No session with given id`. Treating that completed diagnostic call as an unjoined writer prevented native HAR publication.

CDP detach remains joined before retirement. Its returned error does not invalidate the independent successful `CloseHAROwned` receipt. A blocked, panicking or abandoned runtime join still retains ownership; a failed native close still prevents publication and preserves the raw generation. No unknown writer is reclaimed and the shared finalization deadline is unchanged.

## Validation and review

- Focused race validation: **140 passing tests/subtests** over ten repetitions, including four real desktop/mobile-emulation managed replay/run cohorts per repetition.
- Deterministic regressions verify completed detach errors with a joined writer, failed writer-close retention, blocked detach deadlines and abandoned runtime joins.
- Full Go suite with native browser tests required: **35,729 pass**, 38 explicit skips, zero failures. The supplied private HAR import compatibility check passed in that suite.
- Final self-review found no remaining issues. Local evidence includes `evidence/review-rounds.json`, `evidence/validation-summary.json`, tested source hashes and `evidence/SHA256SUMS`.

The first failed full run was stopped after its native workflow failure was diagnosed; it is not presented as a complete suite. The first failed suite is retained locally as `evidence/failed-full-go.jsonl.gz`; it is diagnostic evidence, not an accepted run. Only this Markdown record is committed.
