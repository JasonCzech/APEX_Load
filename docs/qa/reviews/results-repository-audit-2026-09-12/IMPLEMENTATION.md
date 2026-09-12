# Results and Repository implementation review

Implemented the findings from the September 12 audit without changing stored runs or repository assets.

| Finding | Implementation |
| --- | --- |
| R1 — hidden-row bulk selection | Repository owns pagination and selection together. The header selects the current page; a separate action selects all matching items. Selection counts distinguish this page from the total selection. Results disables the unrelated DOM pagination layer and explicitly names its all-matching selection. |
| R2 — incomplete asset navigation | Every catalog asset can be inspected. Scripts and scenarios expose related assets and run history within the application scope. Projects/releases open Structure. Data pools open their owning project and exact pool, with a scoped return link; unavailable pool links never substitute another pool. |
| R3 — inconsistent duration | Results and report headers share controller lifecycle timing. Missing timing is “Not recorded”; configured duration and creation timestamps never substitute for elapsed time. Archived evidence elapsed is a separate, explicitly labeled value and requires archived timestamps. Imported timing is labeled source-reported. |
| R4 — baseline discovery | Eligible baseline discovery follows all catalog pages. Loading, failure, and empty states are distinct. Empty guidance explains the scope and links to Results; entering a known archived run ID remains available. Empty comparison metrics stay hidden until a baseline is selected. |
| R5 — lost Repository context | Session state preserves the selected tab, filters, sort, page, page size, and main scroll position separately by domain/project/application. Returning from an asset restores its original workspace. |
| R6 — scope/count ambiguity | Catalog copy distinguishes application-filtered assets from project-wide assets. Structure explicitly names its full-domain scope. Filtered tabs show matching/total counts. |
| R7 — readiness ambiguity | Scripts identify their status as authoring health, missing health is “Not assessed,” and scenarios say “Saved.” The catalog makes clear that validation, debug, and approval evidence must be checked before execution. |
| R8 — filters/accessibility/density | Results adds inclusive calendar-date filters, active-filter descriptions and reset. Repository identifiers are expandable. Timeline zoom uses named native keyboard sliders with bounds and time descriptions. Archived action, group, SOAP-operation and page-size selectors have accessible names. |

## Browser review

Reviewed the updated frontend against the existing local controller on a narrow viewport. Verified:

- Repository displays ten rows and selects exactly ten; moving to the next page preserves the ten-item selection while the page checkbox is unchecked.
- A CSI data pool opens in CSI and returns to ACC with the original Repository query restored.
- Script inspection receives keyboard focus and its related scenarios/runs remain within the application catalog scope.
- Results date entry immediately filters seven runs to the one July archived run.
- The legacy archived run displays lifecycle “Not recorded” consistently in Results and its report, alongside archived evidence elapsed of 4m 37s.
- Empty baseline guidance links to the scoped Results page.
- Arrow-key timeline zoom changes the selected time, updates the other slider’s bounds, and synchronizes the charts.
- Diagnostics exposes named selectors.

The browser review also caught and resolved scope switching out of Structure, unscoped return URLs, delayed date-input updates, slider spacing, and related assets from other applications.

Screenshots: [Repository return context](after-repository.png), [filtered Results](after-results.png), [asset inspection](after-inspection.png), [report](after-report.png).

## Validation

Validation completed:

- TypeScript and ESLint passed.
- Complete Vitest suite: **381 files, 4,128 tests passed** with `VITEST_MAX_WORKERS=2`.
- One additional archive-loading timing regression passed after the full unit run; its component and test also passed focused ESLint.
- Fresh embedded dashboard build passed.
- Dev-admin browser profile: **98 passed, 1 role-specific test skipped**.
- Local-auth browser profile: **19 passed**, including the role-specific test.
- The shared browser-test login helper passed focused ESLint after adding login pacing.

The full `npm run test:cycle` reached local-auth after passing type checking, lint, unit tests, build, and dev-admin. Its first local-auth pass hit the existing ten-logins-per-minute protection because the expanded suite signs in repeatedly from the same source IP. The test helper now paces real UI sign-ins; the complete local-auth profile then passed. Authentication policy and failed-login behavior were not weakened. Earlier highly concurrent unit runs also hit two existing import-test timeouts; the complete reduced-worker run passed without changing those tests.

Evidence: [full cycle log](validation-cycle.log), [successful local-auth rerun](validation-local-auth.log), [additional timing regression](validation-timing.log).
