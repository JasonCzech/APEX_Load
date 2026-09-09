# Command Center usability review

The Command Center has useful operational depth, but its visual hierarchy makes a healthy run look busier and more concerning than it is. Preserve the controls and diagnostics; simplify their presentation and fix the places where content becomes obscured.

Evaluated September 9, 2026 against the current local development dashboard, including existing uncommitted changes. Used a real three-group HTTP run with one VUser per group and one-second pacing against the local controller health endpoint. No application code changed. The audit run was stopped after capture; its records remain available for inspection.

## Implementation follow-up

The two requested follow-up changes are implemented:

- Details is now Advanced run details and opens the Overview tab of the advanced execution workspace. Back to Command Center explicitly targets the same run, including after a reload or run completion. During an ordinary details visit, the cockpit stays mounted so staged load values, table filters, and graph selections survive; keyboard focus returns to its entry button. Draft state is in memory and does not survive a full page reload.
- Highest Error Rate now shows neutral No errors with no group attribution when sampled groups have zero errors. No samples remains separate. Actual small error rates display <0.01% instead of rounding misleadingly to zero, positive ties name the tied groups, and unavailable group attribution is distinguished from zero errors. Overall zero-error styling is neutral as well.
- The summary and action row have separate space so the longer navigation label does not squeeze No errors into an ellipsis. Full metric values are also available in their titles.

Validation: type checking, lint, and the embedded build passed; 368 unit-test files passed (4,025 tests). The dev-admin suite passed 79 checks initially; the new details check failed on a case-sensitive test selector, then passed on its isolated rerun after correction. All 10 local-auth checks passed. The full cycle command therefore recorded a nonzero initial browser result, with its only failing check subsequently resolved and reverified. No unresolved failures remain from these checks.

The remaining original audit recommendations are unchanged. This implementation does not remove the advanced workspace or any of its controls.

Browser checks confirmed staged load and filter retention, Graph Wall retention, and focus restoration. Regression coverage includes the same-run navigation path and reload return, zero errors, no samples, positive errors, ties, small nonzero rates, and the return action for completed runs.

![Implemented Graph Wall return](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-usability-2026-09-09/12-implemented-graph-return.png)

![Implemented advanced return action](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-usability-2026-09-09/13-implemented-details.png)

## Priority findings

| Priority | Evidence | Finding | Recommended change |
| --- | --- | --- | --- |
| High | 9–11 | Details unexpectedly opens a second execution workspace with repeated live controls and a generic Back button. The destination and return path are unclear. | Rename to Advanced run details, add a prominent Back to Command Center link bound to this run, and preserve the originating mode, filters, and staged edits. Consider an in-context details drawer while retaining the complete advanced workspace. |
| High | 5 | The graph configuration drawer is transparent enough that underlying labels, cards, and buttons overlap its contents. Reopening confirmed this was a settled state. | Use an opaque surface and a separate backdrop. Preserve every graph toggle and Reset action. |
| High | 7–8 | The fixed page header covers the upper portion of the run shelf after scrolling, partially obscuring run identity, summary, and controls. | Give the run shelf a correct sticky offset, or combine the two headers into one persistent region. Verify keyboard focus is never covered. |
| High | 1 | Narrow group cards lose visible metric headings. Numbers such as 1 and 1.0 appear without nearby Current VU and TPS labels. | Give each responsive metric a visible label and unit; use a compact labeled grid. |
| High | 4 | Graph titles and legends truncate; the first chart's controls crowd out its title. Meanwhile substantial card height is unused. | Protect a dedicated title row, move secondary toggles into chart options, size plots to available height, and wrap or expand legends. Use two columns when three cannot fit readable headings; retain the user's dense layout option. |
| Medium | 1–2, 4, 9 | Highest Error Rate incorrectly attributes a healthy zero-error tie to Browse; this is misleading group attribution as well as excessive red emphasis. | When valid samples have zero errors, show No errors with no group name and neutral styling. Name a group only when it has actual errors. Keep No samples distinct from zero errors, handle positive ties explicitly, and retain all group metrics. |
| Medium | 2 | Current/target, planned/actual/delta, and allocated/projected VUsers occupy several prominent panels. Empty error detail and explanatory copy add more weight. | Establish one primary run summary, compress capacity to a status row, and make the load model expandable while retaining a small trend preview. Expand exceptions when relevant. |
| Medium | 2 | The Scenario Run table has 17 columns; even on the wide capture, latency and errors require horizontal scrolling. | Default to Name, Group, Current/Target VU, TPS, Error %, and p95. Preserve every column in a column picker and a full-detail preset; freeze identity columns. |
| Medium | 3 | Runtime Settings and Parameters are reachable through the group name, but that name lacks a clear menu indicator. | Add a visible chevron or a labeled group-actions button. Keep the same menu destinations. |
| Medium | 2, 4–5 | Graph configuration is split between Configure Graphs, the sidebar, and per-chart actions. TPS and Change duration disappear from the top shelf in Graph Wall. | Keep a stable run summary and intervention shelf across both modes. Group chart selection and display options in one organized drawer; retain per-chart actions. |
| Medium | 7 | With no filter entered and no errors, the table says no patterns match the current filter and shows a second empty evidence panel. | Distinguish No errors recorded from No matching errors. Show the evidence pane only after selection; keep diagnostics one click away. |
| Medium | 8 | Infrastructure combines capacity and route rows; WARNING badges have no immediate explanatory reason in the visible columns. | Show the reason beside each warning and distinguish unknown telemetry from confirmed pressure. Preserve all route and capacity metrics. The underlying warnings were not independently validated. |

## Functionality-preserving organization

| Always visible | Available on demand |
| --- | --- |
| Run name, state, current/target load, remaining time, TPS, error rate, connection freshness | Full run metadata and details |
| Pause/Resume, Stop, distinct Abort, duration control, Notes | Existing confirmation and editing dialogs |
| Operations / Graphs switch in one stable location | Existing graph categories and configuration |
| Compact group health rows and explicit target editing | Slider, immediate/gradual ramp controls, staged values and Apply, runtime settings, parameters |
| Small load-profile preview and capacity status | Full profile, planned/actual/delta values, allocation/projection and generator details |
| Scenario, Errors, Infrastructure tabs | All existing columns, percentiles, signatures and transaction evidence |

Retain the purple accent, group colors, and established components. Reduce decorative gradients, repeated card borders, unnecessary status prose, and fixed empty space. Keep errors discoverable with a count/status indicator even when the detailed empty panel is collapsed. Any disclosure change should preserve staged inputs when switching views.

## Capture walkthrough

### 1. Open a running test in the narrow layout — needs improvement

Live status, current/target load, remaining time, and intervention buttons are available. Group metrics lose visible labels, the highest-error group truncates, and stacked panels push diagnostics below the initial viewport. Small supporting text is an accessibility risk.

![Narrow live run](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-usability-2026-09-09/01-live-narrow.png)

### 2. Inspect Operations on a wide layout — usable but crowded

Group colors and current/target controls help orientation. Repeated VUser values, red zero-error tiles, a large empty error panel, and a horizontally scrolling detail table compete for attention. The group button shows a clear focus outline after dismissing its menu.

![Wide operations view](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-usability-2026-09-09/02-live-desktop.png)

### 3. Open group actions — functional, weak discoverability

Runtime Settings and Parameters are available in a compact menu. Escape dismisses the menu and returns focus to the group button. The closed trigger needs a visible affordance. Editing these settings was not exercised.

![Group actions menu](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-usability-2026-09-09/03-group-menu.png)

### 4. Switch to Graph Wall — needs improvement

Live charts, group selection, expansion controls, graph categories, and display options are present. Three-column cards truncate titles and legends. The first chart's options dominate its heading. TPS and duration adjustment disappear from the shared run shelf. Accessible chart measurement tables and named expansion/reorder controls are exposed in the accessibility tree, a useful foundation to retain.

![Graph Wall](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-usability-2026-09-09/04-graph-wall.png)

### 5. Open graph configuration — readability defect

Graph selection and Reset are discoverable, and focus enters on Close. The drawer's background allows underlying content to remain visible through labels and options. This is a concrete readability problem, not a preference about visual density. Graph settings were inspected without changing them.

![Graph configuration drawer](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-usability-2026-09-09/05-graph-configuration.png)

### 6. Inspect Pause confirmation — generally clear

The dialog names the run and explains the action boundary and resumability. Cancel initially receives focus. Preserve that specificity. Button contrast deserves measurement. The dialog was cancelled; pause/resume execution was not tested.

![Pause confirmation](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-usability-2026-09-09/06-pause-confirmation.png)

### 7. Open Error Details and scroll — needs improvement

Error patterns and evidence share one tab, which is useful. An unfiltered healthy run receives filter-related empty copy and two large blank regions. The page header overlaps the sticky run shelf. Populated signatures, evidence retrieval, and failure recovery were not tested.

![Error Details and header overlap](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-usability-2026-09-09/07-error-details.png)

### 8. Open Infrastructure — useful detail, unclear warning context

Capacity and route information are available with status text as well as color. Warning reasons are not apparent in the visible columns, and horizontal scrolling separates identity from later details. The same header overlap persists.

![Infrastructure](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-usability-2026-09-09/08-infrastructure.png)

## Follow-up: Details navigation and zero-error attribution

A second local HTTP run with two one-VUser groups was used for this follow-up. It was stopped after capture. Only review documentation and screenshots changed.

### 9. Locate Details and inspect Highest Error Rate — misleading label and attribution

Details is a small generic action beside Notes and duration controls. It does not explain that it leaves Command Center for a different full execution workspace. The accessibility tree again reports Highest Error Rate as 0.00% · Browse while both groups have zero errors; the screenshot also shows how severely the compact card truncates this content.

Source inspection confirms that `highestGroupErrorRate` chooses the first group even when every rate is zero. That is a ranking artifact, not an unhealthy group. Require actual recorded errors before attributing a group. Do not simply test the formatted percentage: a small nonzero rate can round to 0.00%, so show adequate precision or an error count in that case. For positive ties, say that multiple groups are tied or make the tie discoverable rather than implying a unique worst group.

![Details entry](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-usability-2026-09-09/09-details-entry.png)

### 10. Follow Details to the execution workspace — confusing context switch

The route changes from the run's Command Center to its `/test/` workspace. In this session the Graphs tab was selected, with expanded VUser scaling and duration controls filling the initial viewport. The destination repeats operational controls, including Pause VUsers and Pause, and uses a different information hierarchy. It feels like another runtime dashboard rather than a focused details view.

A Back button does exist at the upper left of the action row. Its generic text, low prominence, and placement among operational actions do not clearly promise a return to this run's Command Center. This is a discoverability and orientation issue; the captured build does not lack a return control entirely. Keep the full advanced capabilities, but label the destination and give navigation its own clearly named return link. A focused details drawer could serve common inspection needs, with Advanced execution workspace available explicitly.

![Details destination](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-usability-2026-09-09/10-details-destination.png)

### 11. Use Back — return works in the inspected path

Clicking the page's Back button returned to the same run's Command Center with its application/release scope. This verifies the direct return path in this local build. It does not establish retention of Graph Wall mode, filters, scroll position, or staged load edits, nor behavior after deep links, reloads, or intermediate navigation. Those must be regression cases.

![Return from Details](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-usability-2026-09-09/11-details-back.png)

Follow-up acceptance criteria: Details announces its destination; a visible Back to Command Center action always targets the same run; keyboard focus returns to the Details trigger; the original view and draft inputs survive; zero-error runs never name a highest-error group; no-sample and real low-rate-error states remain distinct. Cover both Operations and Graph Wall entry, deep links, reloads, positive ties, and run completion while viewing details.

## Evidence limits and acceptance criteria

This is a design inspection of a low-load, healthy HTTP run in the dark theme, not an operator study or a complete functional/accessibility certification. Captures cover narrow and wide rendered layouts. A temporary viewport override produced incorrectly scaled captures; those were rejected and replaced after reset. No findings rely on that capture artifact.

Not covered: large group counts, real error evidence, missing/disconnected streams, other themes, full keyboard traversal, screen-reader announcements, measured contrast, backend correctness of infrastructure warnings, and successful changes to duration/load/pause/abort. The accessibility tree also exposes prototype-oriented names such as Grafana wall mockup and Final graph configuration; replace them with user-facing names.

For a follow-up implementation, preserve all existing capabilities and verify: no overlapping sticky headers; readable titles, labels and key values at both widths; opaque drawers; all prior metrics reachable; stable run controls across modes; staged edits survive navigation; neutral healthy states and explanatory warnings; keyboard focus remains visible. Add meaningful regression tests and run the repository's full dashboard test cycle, including both browser workflow profiles.

Recommended next step: fix the obscured controls, transparent drawer, and missing/truncated labels before reorganizing the broader layout.

## Implementation: interface cleanup

Implemented the remaining recommendations in the existing Command Center:

- The sticky run shelf measures the wrapping application header. TPS and duration controls remain available in both views.
- Graph selection, layout, time display, and reset share one opaque configuration drawer. Chart titles wrap on their own row; secondary chart toggles use a disclosure. The dense grid adapts to available width and card heights follow their content.
- Responsive group values have visible labels and the group actions trigger has a chevron.
- Scenario Run starts with seven essential columns. Individual column selection and a full 17-column preset retain every metric. Name and Group remain pinned while scrolling.
- Capacity is a compact disclosure, expanded for pressure. Missing generator counts display as unavailable instead of invented defaults. The load trend stays visible with its numerical comparison on demand.
- Healthy error tables say “No errors recorded”; filtered results say “No matching errors.” Evidence appears only for a visible selected signature. Warning reasons sit beside infrastructure status badges.
- Filters, sorting, column selection, and graph category/display mode survive Operations/Graph Wall switches within the same run. Filter text is independent per table. Presentation state resets for another run.

Code review corrected the inherited wide table minimum, pinned-header stacking, run-specific presentation state, and unavailable capacity values. Existing run controls, group settings, graph actions, metrics, and diagnostics remain available.

### Review and validation

The final diff review additionally corrected VUser projections for TPS-based runs, delayed error-signature messaging, and keyboard focus spacing including the scroll container's padding. The observer also handles the run shelf appearing after asynchronous loading. No unresolved findings remain from this review.

`npm run test:cycle` was run, covering type checking, lint, 4,027 unit tests across 368 files, the embedded production build, 81 dev-admin workflows, and 10 local-auth workflows. An initial complete cycle passed. The strengthened narrow-screen focus assertion in the subsequent cycle exposed a small overlap; that implementation was corrected and both Command Center browser tests passed on a fresh build. All other browser workflows passed. The error-signature edge case also passed the focused 68-test Command Center suite. Desktop, narrow, and drawer screenshots were visually inspected.

Latest evidence is saved below. These are low-load HTTP smoke runs; they validate the interface and navigation rather than production-scale performance.

![Operations with essential columns](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-usability-2026-09-09/14-clean-operations.png)

![Narrow group metrics and visible keyboard focus](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-usability-2026-09-09/15-clean-narrow.png)

![Opaque unified graph configuration](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-usability-2026-09-09/16-clean-graph-configuration.png)
