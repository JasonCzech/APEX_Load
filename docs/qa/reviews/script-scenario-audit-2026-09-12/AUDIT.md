# Script and Scenario Library usability review

Reviewed September 12, 2026 in the local APEX Load dashboard, using the in-app browser and the ACC project. All screenshots below were captured, saved and visually inspected during this audit. The screenshots show the narrow in-app browser surface; the final DOM check reported a 703 × 1094 CSS-pixel viewport. Horizontal table scrolling is part of the observed experience.

The confirmed selection, labeling, filtering and narrow-table defects were fixed. Existing authoring health and validation safeguards remain intact. This review covers library discovery, selection and history; the full authoring wizard, remote transfers and destructive deletion were not manually exercised against the working project.

## 1. Script catalog and discovery

Fair → improved. Script assets already expose protocol, versions, copyable reference keys, authoring warnings and explicit Edit actions. The generic Library Sources title obscured the primary task, while project-wide content could be mistaken for the selected application/release. The updated title and scope statement clarify this. Local health counts now follow search/release/folder filters, and health buttons expose their pressed state.

![Step 1: Script catalog and discovery](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/script-scenario-audit-2026-09-12/01-script-library.png)

## 2. Script bulk selection

Critical mismatch → fixed. The table showed ten of 29 scripts, but its header selected all 29. The library now owns pagination, selects only addressable local scripts on the page, and offers a separately labeled action to select all matching scripts. Remote assets share pagination but are never included in local deletion.

![Step 2: Script bulk selection](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/script-scenario-audit-2026-09-12/02-script-selection.png)

## 3. Script history

Fair → improved. Expanded rows provide health inputs and linked scenario runs. Duration represented configured duration, not measured elapsed time. It is now labeled Configured duration. Completed run links now use the same report routing as Results and Repository. Nested history pagination remains independent of catalog pagination.

![Step 3: Script history](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/script-scenario-audit-2026-09-12/03-script-details.png)

## 4. Scenario catalog and workload

Poor at the inspected narrow width → improved. Sticky Actions covered workload text. Actions now remain in their own column, workload gets a minimum readable width, and the horizontal catalog region is keyboard focusable. Saved scenarios and the project-wide scope statement make the asset type and scope explicit. Existing Validate before creating a run / Script review required messages correctly avoid implying execution readiness.

![Step 4: Scenario catalog and workload](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/script-scenario-audit-2026-09-12/04-scenario-library.png)

## 5. Scenario bulk selection

Critical mismatch → fixed. Ten displayed scenarios produced a twelve-item selection. The header now selects only the current page; selecting all matches requires the explicit separate action. Filtering prunes hidden selections and moves the pager to the first applicable page.

![Step 5: Scenario bulk selection](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/script-scenario-audit-2026-09-12/05-scenario-selection.png)

## 6. Scenario run history

Fair → improved. View Runs, Open and Run Dashboard provide explicit actions. The saved duration is now labeled Configured duration, consistently with Script Library and the configured-versus-observed distinction in Results. Horizontal scrolling is still required for all history columns at narrow widths.

![Step 6: Scenario run history](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/script-scenario-audit-2026-09-12/06-scenario-history.png)

## 7. Navigation and filter recovery

The return trip from a script report initially lost the library query. Both libraries now preserve search and associated-release filters per domain/project for the browser session; Script Library also preserves folder and health filters. Selection is deliberately transient. Reset filters is visible whenever search, source or filters are active. Storage failures fall back to normal in-memory filtering. Page and sort positions are not persisted by this change.

### Scenario page selection: ten selected, explicit all-matching action

![Scenario page selection: ten selected, explicit all-matching action](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/script-scenario-audit-2026-09-12/07-scenario-selection-fixed.png)

### Scenario history: readable workload and configured duration

![Scenario history: readable workload and configured duration](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/script-scenario-audit-2026-09-12/08-scenario-history-fixed.png)

### Script page selection: ten selected from 29

![Script page selection: ten selected from 29](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/script-scenario-audit-2026-09-12/09-script-selection-fixed.png)

### Script history and local health counts

![Script history and local health counts](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/script-scenario-audit-2026-09-12/10-script-history-fixed.png)

### Completed script run opens Run Results

![Completed script run opens Run Results](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/script-scenario-audit-2026-09-12/11-script-run-report.png)

### Script query restored after visiting Scenario Library

![Script query restored after visiting Scenario Library](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/script-scenario-audit-2026-09-12/12-script-filter-restored.png)

## Review and validation

- Regression coverage includes mixed local/remote pagination, page-only selection, explicit selection of all matches, page reset after filtering, independent nested history pagination, filter reset, filter restoration, project isolation, malformed saved state and unavailable session storage.
- Browser checks verified ten selected scripts out of 29 and ten selected scenarios out of twelve, an unselected second scenario page, pruning selections after filtering, local health counts following search, independent history pagination, completed-script-run report routing, separate restored script/scenario queries, resetting the scenario catalog to twelve rows, and keyboard focus/ArrowRight scrolling of the scenario catalog.
- Final full-cycle results are recorded in [VALIDATION.md](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/script-scenario-audit-2026-09-12/VALIDATION.md).

## Accessibility and evidence limits

Health toggle state is programmatically exposed with aria-pressed; pagination controls have descriptive labels and disabled boundaries; the scenario scroll region has a keyboard focus target. Existing name/history/action buttons remain available to keyboard users. Screenshots and DOM checks do not establish full accessibility conformance. No complete screen-reader, zoom or contrast audit was performed for these libraries. The final browser workflow suite includes existing theme/narrow-layout checks but is not a substitute for a full accessibility audit. Remote pagination has automated fixture coverage; live remote transfers were not exercised.
