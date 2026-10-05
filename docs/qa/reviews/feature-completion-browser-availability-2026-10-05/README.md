# Browser legacy-summary availability correction — 2026-10-05

Status: corrective implementation, independent review and final composed repository qualification verified. Full browser evidence and intelligence completion remains open.

This slice implements the missing-data correction required by BE-2 and BI-1 without claiming document-finality or standards-qualified Core Web Vitals. The active frontend model is `legacy-summary-availability-v2`, applicable only to existing action-summary averages. Its seven equally weighted legacy components require finite nonnegative values and a positive safe-integer `Samples` count. Explicitly present zero is an observation; absent, malformed, negative and nonfinite values are unavailable. Missing required components prevent an overall estimate. Observed components, fixed contributions, field availability and exclusion reasons remain visible. Field availability is not document or time coverage.

Only complete device summaries are ranked. The former arithmetic is available solely through an explicit `legacy-canary-v1` historical reproduction helper. Existing API browser scalars cannot establish qualified zero or independent per-metric eligibility. Root-cause reporting therefore leaves browser qualification unscored, preserves explicit SLA/critical/warning priority, and emits only clearly labeled legacy advisories for valid observed positive exceedances.

## Implementation and review

The shared browser breakdown model now supplies live, archived, comparison-chart and comparison-card admission. Missing fallback members cannot become zero or disappear from a weighted cohort. Malformed null, primitive, array and nested identity values retain explicit gap rows and cannot crash React labels. Internal cohort identities use raw identities and unavailable-source tuples rather than friendly display labels; case aliases and labels resembling unavailable markers remain separate. CSV exports preserve exact cohort identities and empty unavailable cells, and charts do not connect across missing measurements.

Review/fix/review corrected stable weighted-mean rounding, unsafe runtime cohort dereferences, object-valued display labels, friendly-label cohort collisions, contradictory duplicate comparison-card averaging, and clipped screenshot geometry. Regressions cover boundary values, malformed archives, sample-count admission, permutation/collision behavior, mixed cohorts, live/archive rendering and real CSV downloads. Independent final source and visual reviews found no additional actionable issue.

The registered `workflow-browser-resource-evidence.spec.ts` retains resource filtering and initiating-action inspection and adds legacy availability, complete-device ranking and mixed/malformed comparison workflows. Both authentication profiles exercise all four cases. Controlled API fixtures and authorized queued runs supply the evidence; these checks do not measure browser document metrics or qualify native HAR publication. Narrow-view whole-box, scroll-owner, overflow and center-hit assertions are retained. Final element captures are [dev-admin live](evidence/browser-full-final-dev-admin-live.png), [dev-admin archive](evidence/browser-full-final-dev-admin-archive.png), [local-auth live](evidence/browser-full-final-local-auth-live.png) and [local-auth archive](evidence/browser-full-final-local-auth-archive.png). [Capture provenance](evidence/browser-full-final-captures.json) records exact sources, bytes, hashes and the full-cycle receipt.

## Final verification

Pinned Node 22.22 and Go 1.26.5 use the recorded constrained profile, shared session Go cache and ports 18471/18472. Every recorded check has a sibling raw log and JSON execution receipt containing command, working directory, UTC interval, duration, exit and raw-log hash.

| Check | Actual result | Receipt |
| --- | --- | --- |
| Final affected frontend units | 97/97 passed across six files | [receipt](evidence/browser-units-composed-final.json) |
| Final focused dev-admin workflow | 4/4 passed; Playwright 9.9 seconds | [receipt](evidence/browser-dev-affected-composed.json) |
| Final focused local-auth workflow | 4/4 passed; Playwright 37.3 seconds | [receipt](evidence/browser-auth-affected-composed.json) |
| Full `npm run test:cycle` | Exit 0; typecheck, lint, build; 509 unit files / 5,596 tests; dev-admin 236 passed / 5 skipped; local-auth 167 passed / 1 skipped; 2,370.810 seconds | [receipt](evidence/browser-full-cycle-final.json) |
| Affected Go root-cause normal | Passed; package 3.170 seconds, command 90.451 seconds | [receipt](evidence/browser-root-cause-normal.json) |
| Affected Go root-cause race | Passed; package 3.122 seconds, command 133.414 seconds | [receipt](evidence/browser-root-cause-race.json) |
| Final composed `go test ./...` | Passed; 1,133.940 seconds, including the frozen final embedded dashboard | [receipt](evidence/browser-composed-full-go-repeat1.json) |

The full dashboard raw log is retained as `evidence/browser-full-cycle-final.log`, 87,435 bytes, SHA256 `e53dd254002d90e490dedc37f186c6cee519ea4afe95f701c659e3dcc1a53534`. All affected browser cases passed in both profiles. Four dev-admin skips are viewer/non-administrator profile cases; the fifth dev-admin skip and the one local-auth skip require the unavailable private LoadRunner acceptance archive. None is a skipped browser-availability case.

The [final embedded inventory](evidence/browser-final-embedded-files.json) records the completed full-cycle build: 247 files, 8,424,956 bytes, inventory SHA256 `135f7fc7882ad6c8809c6bb600bab6ba1b0bf770e1ad213e3955a646143d6efc`. Its timestamp precedes the remaining full-cycle browser phases; those phases subsequently passed. The bundle remains frozen. Earlier full Go execution used an earlier build inventory and failed the newly added opt-in Kafka suite documentation/runner registration checks. That failed `browser-composed-full-go.log/.json` is retained as failed. Registration was corrected in a separate reviewed commit. The final composed repeat passed against the frozen final bundle; its raw log SHA256 is `3ed271eba780f376f237b2cb025cb963230cf34a5147e39c5713f0c1093e77e6`. All embedded file hashes were rechecked after that gate. The [final source inventory](evidence/browser-final-source-inventory.json) captures the reviewed composed source after the completed gate, rather than claiming a historical start-time capture.

## Retained failures and limits

All failed executions remain in `evidence/` with their receipts. Initial typecheck exposed an over-narrow fixture type; initial units exposed weighted-mean roundoff; two comparison-card attempts exposed missing application/release fixture context; the first composed build exposed an implicit-any fixture; one build attempt ran from the wrong directory and failed with ENOENT. Corrected affected repeats and the final complete dashboard cycle passed. Historical focused runs before the final comparison-card/source corrections are not used to qualify the final source.

Open completion requirements include document/navigation/request identity, clock qualification and lifecycle finality; independently eligible final-document distributions; standards CLS/INP/LCP qualification; native/synthetic HAR receipt, protected finalization, quotas and crash/deletion/recovery; Chromium/WebKit acceptance and measured benchmark publication; versioned persisted qualified findings, baseline matching, interaction/journey models and full browser intelligence workflows. This correction does not close BE-1–BE-4 or BI-1–BI-4. Native 1 GiB reference-load, five-platform, process-restart/HA and remote-backend acceptance also remain open elsewhere in the program. Only the 10 GB analytics benchmark is waived at user direction.
