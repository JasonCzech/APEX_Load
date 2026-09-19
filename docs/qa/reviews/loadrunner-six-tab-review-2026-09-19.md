# LoadRunner six-tab import review implementation

## Scope

Implemented the accepted Product Design review across Review, Credentials, Data & parameters, Runtime & source settings, DSL Preview, and Source Mapping. Existing protected candidate, secret-binding, save, and execution-readiness contracts remain authoritative.

| Surface | Change |
| --- | --- |
| Shared shell / Review | Compact archive context, shared grouped readiness, actionable tab counts, save/editor explanation disclosed on demand, source-to-issue navigation. |
| Credentials | Distinct group/requirement/location language; setup, preserved, and configured filters; readable provider names; optional-value guidance without implying replay readiness. |
| Data & parameters | Imported parameter inventory, source row counts, allocation rules, retained capacity results, run assumptions, per-VUser row ranges and unused rows. Optional environment substitutions and advanced repeated values have a redacted preview. Missing source variables stay in the guided repair queue. |
| Runtime & source settings | Grouped mapped, decision-required, preserved-only, and non-applicable behavior; source evidence disclosures; relevant settings-file and editor links; mapping is explicitly not replay proof. |
| DSL Preview | Section selection, action search/jump, wrap and focus controls; bounded on-screen preview distinguished from the complete protected review download. Explicit credential paths remain redacted in individual-action previews. |
| Source Mapping | Previous/next review mapping, contextual repair handoff, retained selection across tabs, code pane measurement on visibility/resize, reset stale full-document viewport when rendering mapped excerpts. |

## Review findings corrected

- Repeated candidate inspections under React StrictMode could compete for the protected candidate lease and falsely report an unavailable candidate. One inspection promise is shared for the same candidate/project scope.
- Allocation results were cleared on tab changes. They now survive tab changes and invalidate when inputs, project, or candidate change.
- Runtime source links previously landed on an unrelated selected file. They now focus the referenced file, including files without request mappings.
- Source comparison selection was lost on tab switches. The visited workspace remains mounted but hidden, including its shortcut visibility guards.
- DSL action preview needed credential-path rebasing, and its search/section controls needed consistent selected-action state.
- Expired/disabled credential review must also disable the new state filter.
- Removed duplicate credential guidance inside a reuse button and distinguished credential requirements from affected locations.
- Missing source variables were incorrectly grouped with environment overrides. Guided review routes them through the issue queue instead of optional substitutions.
- Restored save/editor guidance and existing editor navigation used by other enterprise import types.

## Local archive check

Validated `acc_wls_reg_1.zip` in an isolated development tab, without saving scripts, supplying credentials, or launching target traffic. The existing operator tab was left unchanged.

The archive retained 492 primary requests, 25 transactions, 940 LR markers, and 58 extractors. All six tabs were inspected. The protected inspection completed successfully after the StrictMode fix. The current server findings showed 19 grouped replay blockers and one review issue; the UI did not clear any blocker by acknowledgement. The credential view distinguished 12 groups, 967 requirements, 11 preserved groups, and one group needing setup.

The two-VUser duration preview reported, for example, 3,995 usable email rows from start row 6, 1,997 rows per VUser, and one unused row. The first user's range was 6–2,002. The Once username parameter reserved one row per VUser. Results persisted through other tabs. Runtime source navigation selected `default.cfg`; next-review navigation selected the legacy TLS source and rendered the corresponding destination code. Its repair link selected the TLS issue in Review.

Desktop screenshots and a 768×1024 Source Mapping check are local-only under the Codex artifact directory `import-tabs-implemented`; the tablet page had no horizontal document overflow. Private archive content and screenshots are not committed.

## Validation

- `go test ./...`: passed.
- Final dashboard typecheck, lint, and embedded build: passed.
- Full unit suite: 400 files / 4,316 tests passed. The additional final credential-filter test passed in the 63-test credential suite.
- Final dev-admin browser profile: 112 passed, 8 failed, 4 skipped. All seven LoadRunner workflows passed, as did the transaction-label persistence test and Source Mapping layout/review workflows.
- Final authenticated browser profile: 38 passed, 1 failed, 1 skipped. All seven LoadRunner workflows passed. The failure is the previously documented `workflow-local-auth.spec.ts` workspace journey.
- `npm run test:cycle` completed both profiles and remains non-green because of those nine failures outside import review.
- Final additional-test typecheck and `git diff --check`: passed.

The eight dev-admin failures are outside the modified import surfaces: Command Center details, development-role navigation, live-run identity, pause/resume/scale/stop, abort, workspace creation, authoring workspace/history, and guided workspace setup. The private archive automation remains opt-in; the original archive was checked manually in the browser during this pass.

The broader navigation/run-control failures were already documented in [the preceding review validation](loadrunner-review-redesign-2026-09-19.md#validation). Their screens and navigation implementations are unchanged by this work.

Target replay and comparison with a LoadRunner baseline remain separate from import/UI validation.

## Running preview

The updated development preview is available at `http://127.0.0.1:4019`. The embedded dashboard was rebuilt. The existing Docker controller on port 8080 was not restarted, and the operator’s original import tab was not reloaded.
