# Results and Repository audit (2026-09-12)

Status: Complete. Findings implemented and reviewed (see Implementation).

Merged from the original per-topic files of this review. Each section below was one file; the screenshots and evidence next to this README are unchanged.

## Results and Repository — usability and functional completeness audit

Evaluated September 12, 2026 in the running local APEX Load app at `127.0.0.1:8080`, using the in-app browser, current-run screenshots, accessibility/DOM observations, and targeted implementation reads. No application code or stored product data was changed.

**Verdict:** Results has a substantial analysis workflow and useful evidence safeguards, but needs more consistent semantics and better comparison discovery. Repository is useful for inventory and script access, but is incomplete as a workspace for finding, inspecting, and reusing assets. Its bulk-selection behavior should be corrected before expanding its management functionality.

The session used Development preview / Admin, Default Domain, project ACC, application Legacy, release Imported Legacy. Results contained seven completed local runs, only one with an available archive. Repository showed 27 catalog items across accessible projects. Findings describe this dataset and build; they are not a production certification.

### Task coverage

| Step | User task | Health | Captures |
|---|---|---|---|
| 1 | Find results, inspect filters, recover from an empty search | Usable; discovery and terminology need improvement | 01, 02, 16 |
| 2 | Open a run and decide whether its evidence supports acceptance | Strong detail view; inconsistent duration semantics | 03, 08 |
| 3 | Explore performance, isolate errors, inspect retained evidence | Strong utility; accessibility risks remain | 04, 05, 06 |
| 4 | Choose a baseline and inspect export choices | Broad options; comparison blocked by available data and poorly explained | 07 |
| 5 | Browse Repository and reach shared data | Useful inventory; incomplete asset handoffs | 09, 10 |
| 6 | Select a page of repository items for bulk work | High-priority selection defect | 11 |
| 7 | Inspect project/release structure | Useful administration; scope differs from the catalog | 12 |
| 8 | Search for a script, open it, and return | Editor handoff works; search context is lost | 13, 14, 15 |

### Prioritized findings

#### R1 — High: “Select all visible” selects assets outside the displayed page

**Observed, step 6 / capture 11.** With All selected and the table displaying “Showing 1–10 of 27,” checking “Select all visible repository items” produced “27 items selected,” “Archive selected (4),” and “Delete selected (27).” Selection was cleared; no archive or deletion was attempted.

The UI's promise and the operation's scope disagree. This is particularly consequential in a catalog combining projects, releases, scripts, scenarios, runs, and data pools from multiple projects. A later confirmation showing a count does not repair the initial selection ambiguity.

**Recommendation:** Select only the current page by default. Offer a separate, explicit “Select all 27 matching items” action. Before a bulk operation, identify affected types/projects and exclusions. Preserve the distinction through pagination, filtering, and sorting.

**Acceptance:** With 27 matches and page size 10, the header checkbox selects 10. Selecting all matches requires a separate action. Bulk operations use exactly that selection; changes of page or filter cannot silently broaden it.

**Implementation evidence:** [selection model](../../../../dashboard/src/components/ApplicationRepositoryPage.tsx#L541), [selection handler](../../../../dashboard/src/components/ApplicationRepositoryPage.tsx#L771), and [global pagination hiding rows](../../../../dashboard/src/components/TablePaginationManager.tsx#L80). The repository model treats all filtered rows as visible while the pagination manager hides some rows afterward.

#### R2 — Medium: Repository discovery does not consistently lead to asset inspection

**Observed, steps 5 and 7 / captures 09, 10, 12.** Data-pool rows expose “Catalog” as noninteractive text. Project and release rows similarly expose noninteractive “Structure” text. Scenario rows offer New Test, but no direct inspect/edit-scenario action. Script rows do have working Edit buttons.

“Shared test data” opens a Settings-style workspace within the Repository route. It correctly identifies ACC as its project, but shows zero pools while the preceding cross-project catalog shows three pools in CSI, OPUS, and Unassigned. The generic link cannot carry a selected pool's identity or project because the catalog has no pool-level Open action.

**Recommendation:** Give each asset a primary Open/Inspect action that carries its owning project and asset ID. Use secondary actions for Edit, New Test, Open structure, Archive, and Delete. Make scenario/script dependency counts and run-history summaries navigable. Keep the shared-data editor focused on data management and provide a clear return to the catalog.

**Acceptance:** A pool found in CSI opens that pool in CSI; a release opens its structure/detail; a scenario can be inspected before creating a test. Returning restores the originating catalog context.

**Implementation evidence:** [row actions](../../../../dashboard/src/components/ApplicationRepositoryPage.tsx#L1033). This is a workflow gap on Repository, not a claim that data editing or scenario editing is absent elsewhere in the product.

#### R3 — Medium: Results uses different duration meanings without sufficient labeling

**Observed, steps 1 and 2 / captures 01, 03, 08.** The web wiki list row says Duration 5m 7s; its report says Observed elapsed 4m 37s and Configured 20m. Gradual VUser UI Check says Duration 2m in the list, while its report identifies 2m as configured duration and says observed elapsed was not recorded.

These can be different legitimate measurements, but the library's single Duration column hides that distinction. Users cannot reliably compare rows or reconcile the list with the report. The fallback case is especially misleading because a planned value occupies the position used for measured duration.

**Recommendation:** Define separate planned duration, lifecycle elapsed, and captured-evidence window values where needed. Use the same named value consistently between list and detail. Show “Not recorded” when observed elapsed is missing; never silently substitute configured duration under an observed label.

**Acceptance:** Both reviewed runs display mutually consistent, explicitly named durations. Sorting by observed duration handles missing observations explicitly.

**Implementation evidence:** [duration calculation and fallback](../../../../dashboard/src/components/ResultsPage.tsx#L120), [row construction](../../../../dashboard/src/components/ResultsPage.tsx#L746), and [execution facts](../../../../dashboard/src/components/runReport/executionPresentation.ts#L18).

#### R4 — Medium: Comparison discovery needs a useful unavailable state and full history access

**Observed, step 4 / capture 07.** The completed-baseline picker contained only “Select a baseline.” The page displayed five “Not available” deltas and an Enter a run ID escape hatch, without explaining that no eligible alternative archive was available in this scope. Only one of the seven local runs had an archive. The library's selection feedback was better: selecting that run said “Select one more archived run to compare.”

**Recommendation:** Explain the eligibility rule and current scope beside the picker. Show “No other archived runs in this release,” provide a path to eligible history, and distinguish no candidates from loading and request failure. Keep manual IDs as an advanced option. A completed comparison should identify its baseline, population, load differences, evidence limitations, and selected time basis.

**Additional code-supported completeness risk:** The detail view requests `limit: 100` once and filters eligible baselines afterward. Older qualifying runs can be omitted; this was not reproduced in the seven-run dataset. [Baseline loading](../../../../dashboard/src/components/RunReportPage.tsx#L139).

**Acceptance:** Empty eligibility is explained; an eligible older run remains discoverable beyond the first response page; comparison cannot present unsupported populations as comparable.

#### R5 — Medium: Returning from an asset loses the search that found it

**Observed, step 8 / captures 13–15.** Searching Scripts for WikipediaRandomLinks narrowed the catalog to one row. Edit opened the correct script. Browser Back preserved the Scripts tab but cleared the search and restored all five script rows.

**Recommendation:** Preserve search, filters, sort, page, and scroll position in route or per-workspace navigation state. Add a clear return-to-repository action where the entry originated in Repository.

**Acceptance:** Find a script on a filtered page, open it, then return: the same query, scope, page, and row remain visible. [Current local filter/sort state](../../../../dashboard/src/components/ApplicationRepositoryPage.tsx#L280).

#### R6 — Medium: Repository scope and counts are difficult to reconcile

**Observed, steps 5, 7, and 8 / captures 09, 12, 13.** The catalog says “All accessible projects,” while the selected application still filters some asset types. The Releases tab shows 1, but Structure shows ACC with 3 releases and other projects with additional releases. After a search returns one script, the Scripts badge still reads 5 and All reads 27.

Counts may intentionally represent different populations. The problem is that those populations are not explained where the user compares them. There is also a duplicated domain selector in the sidebar and page body.

**Recommendation:** State the complete catalog scope, including application restrictions, and use “1 matching / 5 total” when a filter changes the visible count. Either align Structure with the catalog scope or clearly label it as the full domain structure. Present one coherent scope control.

**Implementation evidence:** [application filtering for release rows](../../../../dashboard/src/components/repositoryCatalogUtils.ts#L216) and [project-only scope toggles](../../../../dashboard/src/components/ApplicationRepositoryPage.tsx#L1098).

#### R7 — Medium: Readiness labels need a precise meaning

**Observed, steps 2 and 8 / captures 03, 13, 14.** Results usefully separates execution status, workload completion, acceptance, and evidence coverage. Repository labels the reviewed script READY, while its editor reports validation evidence “not yet,” debug evidence “missing,” and approval readiness “needs evidence.” This does not prove execution is broken; it shows that “Ready” is underspecified across surfaces.

**Recommendation:** Name readiness states by purpose: authoring validity, debug evidence, approval readiness, and runtime prerequisites. Carry the most decision-relevant qualifier into the repository row. Likewise retain Results' distinction between completed execution and accepted performance.

#### R8 — Lower: Search/filter presentation and dense metadata slow routine work

**Observed, steps 1, 5, and 8 / captures 01, 02, 09, 13, 16.** Results leads with “Library Sources” and “result sources,” even though the common task is finding runs. Its expanded filters cover release, status, and archive availability; there is no date-range control on the library. Repository supports activity ranges but repeats raw IDs, category labels, and generic status information in a dense mixed-asset table. Search result counts remain tied to total inventory. The Results empty-search message explains recovery, but has no explicit combined reset action.

**Recommendation:** Lead Results with run outcome, date, scenario, and evidence availability. Add a date range and named filter chips with reset. Give Repository type-specific columns and secondary disclosure for raw identifiers. Preserve IDs for copying and precise lookup. Consider saved views only after navigation state and scope are reliable.

### Functional completeness assessment

| Capability | Current assessment |
|---|---|
| Results inventory/search/sort | Present; empty search and restoration verified. Release/status/archive controls inspected. Date filtering and persistent views remain opportunities. |
| Outcome interpretation | Strong detail implementation: execution, planned workload, acceptance and evidence coverage are distinct. List duration semantics need correction. |
| Performance analysis | Synchronized load, throughput, response-time and error-rate charts; action summaries; named-transaction SLA handling present. Reviewed run had no named-transaction samples. |
| Diagnostics | Errors-only filtering reduced 444 action records to 2 error records. Inspect exposed TLS timing and captured request/response details. Useful and verified. |
| Evidence/provenance | Coverage score, telemetry health, compatibility, evidence tiers, provenance and raw-download choices inspected. Missing archive provides retained-record investigation paths. |
| Compare/reporting | Baseline selection, pinning affordance, audience lenses and HTML/PDF/CSV/DOCX choices present. No eligible second archive; comparison calculations, exports and Power BI publication were not executed. Source also contains multi-run library comparison. |
| Repository inventory | Projects, releases, scripts, scenarios, test runs and data pools appear; search, type tabs and sorting affordances present. Scope/counter explanations need work. |
| Asset reuse | Script editing handoff verified. Scenario New Test affordance present. Direct pool/release/project inspection and navigable dependency/history links are incomplete on this page. |
| Structure administration | Category/project/release creation forms and active-project controls present. No writes executed. |
| Bulk lifecycle management | Archive/delete controls and permission-dependent source logic present; page-selection contract is defective. Only releases and pools are archiveable in the reviewed implementation. |
| Scale, roles, integrations | Not fully established by this review: large datasets, local-auth roles, remote federation, concurrent edits and publication failures require dedicated checks. |

### Accessibility observations

**Verified strengths:** Main landmarks and headings, named navigation buttons, labeled search inputs, tab roles/selected states, named row-selection controls, and status text are exposed. ArrowRight moved focus and selection from Scripts to Scenarios in Repository. The script-search input showed a visible focus outline.

**Risks tied to captured steps:** The performance timeline accessibility tree exposed two sliders without useful names and with undefined minimum/maximum descriptions (step 3 / capture 04). Diagnostics action/group/page-size selectors lacked descriptive accessible names in the inspected tree (step 3 / capture 05). Dense muted text, repeated action labels and chart-only temporal patterns deserve contrast, target-size and screen-reader testing. These are scoped observations, not a WCAG compliance verdict.

**Suggested verification:** Name the timeline range endpoints and expose meaningful time bounds/values; label the diagnostics selectors; verify keyboard-only range changes, focus order, chart data alternatives, zoom/reflow, and actual contrast ratios. The bulk-selection naming issue in R1 also affects assistive-technology users.

### Evidence limits and recommended sequence

Screenshots were captured and reopened during this audit. Browser panel sizing and zoom affected image framing; captures show some viewport edges and black margins. Do not interpret those capture boundaries as proven application overflow defects. A narrow performance layout was observed, but this was not a complete responsive-device or assistive-technology audit.

No deletion, archiving, creation, script saving, test execution, baseline pinning or external publishing was performed. No download file contents were validated. The reviewed data was legacy-heavy, so missing evidence is not proof that new runs fail to archive. Current source was consulted for specific behavior; the running app identifies itself as a local dirty build, and unrelated working-tree changes were already present. No automated suite was run because this was an evaluation with no application-code change.

Implement in this order: **R1 selection correctness; R3 duration semantics; R2/R5 asset handoffs and return state; R4 comparison discovery; R6/R7 scope and readiness terminology; R8 presentation refinements.** Test the first fixes together with the real pagination manager and both required workflow browser profiles. Use separate suitable archived runs to validate comparison and export behavior.

### Screenshot walkthrough

#### Step 1 — Results discovery and recovery · Usable, needs clearer filters

Capture 01: scoped run list, headline metrics, reference keys and archive states.

![01 Results library](01-results-library.png)

Capture 02: expanded release/status/archive filters. The selected release is counted as an active filter.

![02 Results filters](02-results-filters.png)

Capture 16: empty-search recovery text. Clearing the search restored seven rows.

![16 Results empty search](16-results-empty-search.png)

#### Step 2 — Outcome and missing archives · Strong explanation, inconsistent durations

Capture 03: the archived report separates acceptance, workload and evidence coverage. Compare its duration with capture 01.

![03 Result summary](03-result-summary.png)

Capture 08: unavailable archive with retained-record investigation paths; observed elapsed is explicitly not recorded.

![08 Unavailable archive](08-unavailable-archive.png)

#### Step 3 — Performance and diagnosis · Strong utility, accessibility gaps

Capture 04: synchronized timeline at the observed narrow panel size. Range handles need stronger accessible naming.

![04 Result performance](04-result-performance.png)

Capture 05: Errors only correctly yields two retained failures for the selected action.

![05 Result diagnostics](05-result-diagnostics.png)

Capture 06: Inspect exposes network timing and request/response evidence, with honest missing-body states.

![06 Error inspection](06-error-inspection.png)

#### Step 4 — Compare and export · Broad functionality, unhelpful empty picker

Capture 07: audience-specific export choices and the baseline picker with no eligible choices. Export generation and comparison calculations remain unverified.

![07 Comparison and export](07-comparison-export.png)

#### Step 5 — Repository and data handoff · Useful inventory, incomplete navigation

Capture 09: all-project catalog with mixed asset types and data pools owned by other projects.

![09 Repository catalog](09-repository-catalog.png)

Capture 10: generic Shared test data link opens ACC's pool manager, with no selected pool and no pools in ACC.

![10 Data handoff](10-repository-data-handoff.png)

#### Step 6 — Bulk selection · High-priority defect

Capture 11: 27 selected while only 1–10 of 27 are on the page. Selection was cleared immediately after review.

![11 Repository bulk selection](11-repository-bulk-selection.png)

#### Step 7 — Structure · Functional controls, inconsistent scope

Capture 12: full structure exposes three ACC releases although the catalog's Releases badge reads one. Creation controls were inspected without submitting.

![12 Repository structure](12-repository-structure.png)

#### Step 8 — Search, edit, return · Working handoff, lost context

Capture 13: one matching script; total badges remain unchanged.

![13 Repository script search](13-repository-script-search.png)

Capture 14: Edit opens the correct script. Readiness evidence is more qualified than the repository's READY label.

![14 Script handoff](14-script-handoff.png)

Capture 15: Browser Back returns to Scripts, but clears the query and shows all five scripts.

![15 Repository return state](15-repository-return-state.png)

## Results and Repository implementation review

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

### Browser review

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

### Validation

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
