# APEX Load — end-to-end UX review

9 September 2026

The shared visual foundation is stronger than the remaining workflow behavior. The highest-impact gaps are unreliable narrow-width selection, ambiguous metric states, weak evidence qualification and failed-run recovery. Fix those before another broad visual refresh.

## Scope and method

Reviewed live in the in-app browser on 9 September 2026: 36 captured states across authoring, scenario validation, run configuration, monitoring, results, reporting, administration and personal preferences. Used existing data and an unsaved draft; validated the draft load model and discarded it. Examined dark mode at 923×987 and selected reproductions at 1440×1000. Admin and User development previews were exercised. Original viewport, Admin preview and Personal preferences were restored.

## What works

- Personal Settings matches the admin visual language and clearly describes immediate browser-local preferences (29).
- The unsaved-change dialog offers explicit Save/Discard/Keep editing and initially focuses Keep editing (13).
- Scenario validation separates capacity, data, placement, demand and runtime checks (12).
- Results distinguishes lifecycle from acceptance and retains usable primary actions at narrow width (18).
- Reports already distinguish unassessed acceptance and unknown termination from successful execution; export retains some caveats (19,22).
- Monitoring discloses deployment-wide scope; Admin Settings provides grouped navigation, search, scope and restart-required indicators (24,27).

## Prioritized work items

18 items: **5 P1**, **11 P2**, **2 P3**. P1 means address first because it can obstruct a core task or mislead interpretation; it does not denote a security incident. P2 covers workflow clarity, consistency and accessibility. P3 improves discoverability and scope understanding.

### UX-01 · P1 · Keep the scenario script picker inside its panel

**Evidence:** [Step 09](#09-script-picker)

At 923px, opening Choose script focuses a search field in an overflowing table and shifts content behind the sidebar. DOM inspection measured the table at 1180px wide with its left edge at −291px. The operator loses the surrounding form.

**Change:** Contain horizontal scrolling within the picker, keep selection and search visible, and place focus without scrolling the outer workspace sideways.

**Acceptance:** At 923px and 1440px, open the picker with mouse and keyboard, filter and choose a script, and return to the group. No labels disappear behind navigation; focus remains visible.

### UX-02 · P1 · Separate planned, allocated and measured load

**Evidence:** [Step 17](#17-reserved-run), [Step 36](#36-failed-diagnostics)

A Reserved run offers Start Test while displaying Current/Target 1/1 VU and Actual VU 1 alongside zero latency and errors. A historical failed snapshot labels telemetry Healthy and metrics Live Summary without clear historical context.

**Change:** Make metric presentation depend on lifecycle and sample availability. Label allocation separately; show Not started or No samples instead of successful-looking measurement zeros. Label historical samples with full timestamps.

**Acceptance:** Reserved, queued, running, disconnected and terminal fixtures distinguish allocation from observed load. No-sample latency/error values are unavailable, and historical health cannot imply current live health.

### UX-03 · P1 · Carry evidence limitations into conclusions and exports

**Evidence:** [Step 19](#19-report-summary), [Step 21](#21-evidence), [Step 23](#23-export-risks)

The report has nine request records, unknown elapsed time and termination, and 1 peak VUser against 22 planned. Evidence separately reports critical telemetry gaps, yet Executive Top Risks says no significant error patterns were recorded. An 85% coverage score does not communicate the critical limitation.

**Change:** Promote insufficient-evidence and critical telemetry findings to Summary and exported Top Risks. Separate measured errors from confidence in the test. Avoid inferring customer impact from generic latency/error prose.

**Acceptance:** For this run, Summary and export both identify sparse workload evidence and critical telemetry gaps. No-errors text is qualified. Unknown values read naturally, including replacing Unknown/s elapsed-run average.

### UX-04 · P1 · Qualify baseline comparisons before showing regression claims

**Evidence:** [Step 20](#20-baseline-compare)

The selected baseline produces raw deltas including +1980ms p95 and −44 VUsers, while the current run has nine request records and 1 peak VUser. The view does not explain whether differing workload and sample populations support comparison.

**Change:** Show comparability checks for script/scenario revision, workload, duration, sample count and metric definition. Keep descriptive deltas available but mark an unmatched or unknown baseline as unsuitable for a confident regression conclusion.

**Acceptance:** A mismatched-load baseline displays a prominent qualification next to deltas; unknown metadata remains unknown. A matched baseline receives a clear comparison summary using the same metric populations.

### UX-05 · P1 · Make missing-archive recovery useful

**Evidence:** [Step 18](#18-results), [Step 34](#34-missing-archive), [Step 35](#35-failed-snapshot), [Step 36](#36-failed-diagnostics)

An older Failed run appears with archive Pending. Its result page loses name/reference/status and says this run is complete. View execution snapshot initially lands on an empty Archive Report panel. Diagnostics later shows a delayed-publication banner and live-style telemetry, but no failure cause on that panel.

**Change:** Preserve run identity through errors; distinguish publishing, delayed, unavailable and failed archive states when supported by backend evidence. Open a useful overview or failure-diagnostics fallback, with a concrete explanation when failure details cannot be recovered.

**Acceptance:** Open the observed jms-rel-run from Results: identity and Failed status remain visible, recovery never lands on an empty archive panel, and unavailable failure details have explicit next actions. Do not imply publication is still progressing without evidence.

### UX-06 · P2 · Count scripts, not expanded detail rows

**Evidence:** [Step 05](#05-expanded-script), [Step 33](#33-expanded-pagination-wide)

Expanding one script changes pagination from 27 to 28 while the All filter remains 27. The desktop reproduction shows All 27 and Showing 1–10 of 28 together.

**Change:** Paginate logical records and render expanded details within their parent record.

**Acceptance:** Expanding or collapsing any number of rows leaves item totals, page boundaries and the set of scripts on a page unchanged.

### UX-07 · P2 · Normalize the import form and prioritize the source

**Evidence:** [Step 04](#04-import-form), [Step 32](#32-import-wide)

New script folder name is a native gray input at 1440px (360×20px) and stretches into a 128.5px-high block at 923px. Destination setup precedes file selection, and an Agentforce option appears in the generic OpenAPI flow.

**Change:** Use the shared themed input with constrained dimensions. Lead with source selection and detected format; reveal destination and format-specific options when relevant.

**Acceptance:** Both tested widths show the same input treatment and usable label/field spacing. The source step is obvious; specialized options do not compete with generic OpenAPI import.

### UX-08 · P2 · Make Settings search find actual settings

**Evidence:** [Step 28](#28-settings-search), [Step 29](#29-user-settings)

Find settings returns No settings match for theme although Dashboard Theme exists in Personal preferences.

**Change:** Index field names and common synonyms as well as section titles, and send results to the matching control.

**Acceptance:** Theme, dark, word wrap, rows and motion resolve to their relevant controls when available to the current role; searching never exposes inaccessible admin settings.

### UX-09 · P2 · Make cross-links respect the current role

**Evidence:** [Step 30](#30-user-admin-link)

In User preview, Monitoring → Controller settings leads to personal Appearance with no explanation or visible return-to-monitoring control. This is misleading navigation, not evidence of an authorization bypass.

**Change:** Hide irrelevant administrative links or offer a clear read-only explanation of who can manage the setting. Preserve the return path when a destination cannot be opened.

**Acceptance:** A non-admin following the Monitoring link gets an honest destination or explanatory state and can return to Monitoring; an admin reaches Controller settings.

### UX-10 · P2 · Reduce page chrome and preserve essential actions

**Evidence:** [Step 01](#01-dashboard), [Step 02](#02-script-library), [Step 03](#03-create-entry), [Step 06](#06-script-editor), [Step 26](#26-access)

The script library consumes roughly 600px before records at the narrow viewport. Create-script choices stack above the form; Dashboard dates/actions and editor action controls require horizontal scrolling. Access layers tabs, links, filters, a table and details.

**Change:** Use a compact shared page header, one primary action, collapsible secondary filters and responsive essential columns. Reuse the Results list’s successful narrow-width treatment. Keep row actions and identifying context visible.

**Acceptance:** At 923px, users can identify and act on a recent run or script without horizontal page scrolling. A completed dashboard run has a clear results action. Core editing controls are visible while ancillary metadata remains available.

### UX-11 · P2 · Define one readiness vocabulary across authoring

**Evidence:** [Step 02](#02-script-library), [Step 06](#06-script-editor), [Step 09](#09-script-picker), [Step 12](#12-scenario-validation)

Checkout HTTP is Ready · 100 in the library, but the editor says validation evidence not yet, Debugged missing and approval readiness needs evidence. These may represent different checks, but their scopes are not obvious. A workload-source warning appears on Scripts before the source step.

**Change:** Name each readiness dimension consistently: static validity, debug evidence, approval and run-model validation. Explain what each enables and defer future-step requirements until relevant.

**Acceptance:** The same script has compatible status labels in library and editor; no unqualified Ready implies missing execution evidence exists. Each warning has an actionable current-step destination.

### UX-12 · P2 · Reset wizard scroll and focus on step changes

**Evidence:** [Step 09](#09-script-picker), [Step 10](#10-run-settings), [Step 11](#11-data)

After advancing to Data, the viewport remains down near the model preview and disabled fields instead of the new step heading. Run Settings appears complete on initial arrival using defaults.

**Change:** Move focus to the new step heading and reset the intended content scroll container. Distinguish valid defaults from a step the user has reviewed.

**Acceptance:** Next and Back consistently announce and reveal the step at both widths and via keyboard, without losing entered values. Step status wording does not imply explicit review of untouched defaults.

### UX-13 · P2 · Align creation entry points with their promised task

**Evidence:** [Step 03](#03-create-entry), [Step 07](#07-scenario-library), [Step 08](#08-scenario-start), [Step 14](#14-new-test)

Create script opens Use existing. An empty scenario library offers Copy existing first. New Test has a useful builder link, but shows lengthy downstream setup before a scenario exists. Model and copy terminology obscures whether an original is changed.

**Change:** Open the requested creation mode, use a clear first-test path, describe copy versus edit explicitly, and defer downstream configuration until prerequisites exist.

**Acceptance:** A first-time operator can create a script, build a scenario and configure a run without choosing unavailable copy sources. Save, Save copy and configure-run actions describe their effects accurately.

### UX-14 · P2 · Disambiguate runs before opening them

**Evidence:** [Step 16](#16-command-entry)

The run selector contains duplicate names with identical Reserved or Queued statuses and no reference or creation time.

**Change:** Include stable reference, relevant time and workspace context, with grouping by lifecycle where helpful.

**Acceptance:** Each duplicate-named run is distinguishable in the selector and its identity matches the workspace opened afterward.

### UX-15 · P2 · Use one explicit scheduling time convention

**Evidence:** [Step 14](#14-new-test), [Step 15](#15-schedules)

One-off scheduling defaults to America/New_York while recurring schedules default to UTC. Both expose time zones, but switching modes changes the assumption.

**Change:** Use the same documented user or workspace default in both flows. Show the next occurrences with their time zone and handle daylight-saving transitions explicitly.

**Acceptance:** Switching one-off/recurring preserves the intended time zone. Previewed occurrences agree with stored schedules, including a daylight-saving boundary.

### UX-16 · P2 · Correct Access action semantics and simplify inspection

**Evidence:** [Step 26](#26-access)

The inspected user-row action cell is labelled Service account actions in the DOM. The dense table/detail arrangement also duplicates inspection/edit entry points.

**Change:** Correct entity-specific accessible names and consolidate the inspect/edit path without hiding identity or role context.

**Acceptance:** Keyboard and screen-reader checks identify the selected user and action correctly. Focus enters and returns from the details panel predictably. User and service-account rows are not mislabelled.

### UX-17 · P3 · Make operator help easy to find

**Evidence:** [Step 31](#31-help-entry)

User Guide and Quick Start live under API Guide, whose default page foregrounds REST response codes and development headers.

**Change:** Use Help & documentation as the entry, prioritize operator tasks, and retain API reference as a clear destination.

**Acceptance:** An operator can find first-test, failed-run and report-interpretation help without recognizing API terminology.

### UX-18 · P3 · Make repository scope an explicit mode

**Evidence:** [Step 25](#25-repository)

Repository explains that its domain/project/release filters are independent of the sidebar workspace. This is honest but requires operators to track two scope systems.

**Change:** Offer an obvious Current workspace / All accessible workspaces mode, with visible scope near results and explicit destination when opening an asset.

**Acceptance:** A user can predict which assets will appear and whether opening one changes workspace; global browsing remains possible. Do not infer a permissions problem from different catalog totals.

## Recommended delivery sequence

1. Repair overflow, lifecycle metric states and missing-archive recovery (UX-01, UX-02, UX-05).
2. Unify evidence-quality conclusions and qualify comparisons across report views and exports (UX-03, UX-04).
3. Fix deterministic defects: pagination, import control, search, role-aware links and accessible labels (UX-06–09, UX-16).
4. Apply shared workflow patterns to density, readiness, wizard progression, creation, run identity and scheduling (UX-10–15).
5. Improve help and cross-workspace browsing (UX-17–18).

## Accessibility observations

The picker overflow (09), step-change focus/scroll (11), horizontally displaced actions (01,06), and incorrect entity label (26) merit keyboard, zoom and screen-reader regression checks. The unsaved dialog’s safe initial focus is a positive observation, not a full focus-trap certification. Small secondary text and low-emphasis metadata need measured contrast checks before any conformance claim.

## Ordered walkthrough

Health describes the captured step, not the whole module. Screenshots are unchanged captured evidence; URLs preserve the inspected context.

<a id="01-dashboard"></a>
### 01. Dashboard — Needs work

Dashboard entry: inspect result discoverability, recent activity and the operations snapshot. Default narrow viewport.

Work items: UX-10

![Step 01: Dashboard](01-dashboard.png)

<a id="02-script-library"></a>
### 02. Script library — Needs work

Script discovery: source selector, filters, health counts, row actions and authoring review sessions.

Work items: UX-10, UX-11

![Step 02: Script library](02-script-library.png)

<a id="03-create-entry"></a>
### 03. Create-script entry — Needs work

Create script opens Use existing rather than Create. The next action is called Model; copy language says original unchanged until save, which may imply overwrite.

Work items: UX-10, UX-13

![Step 03: Create-script entry](03-create-entry.png)

<a id="04-import-form"></a>
### 04. OpenAPI import — Needs work

Import presents destination setup before the source file, plus an Agentforce-specific checkbox on generic OpenAPI flow. Validate and Convert disabled until a file is supplied; no conversion performed.

Work items: UX-07

![Step 04: OpenAPI import](04-import-form.png)

<a id="05-expanded-script"></a>
### 05. Expanded script — Needs work

Expanding Checkout HTTP changes pagination total from 27 to 28 although health-filter total stays 27. Detail rows appear to count as library items.

Work items: UX-06

![Step 05: Expanded script](05-expanded-script.png)

<a id="06-script-editor"></a>
### 06. Script editor — Needs work

Library marks Checkout HTTP Ready 100; editor shows Validation evidence not yet, Debugged missing and Approval readiness needs evidence. Distinguish static validity from execution/approval readiness.

Work items: UX-10, UX-11

![Step 06: Script editor](06-script-editor.png)

<a id="07-scenario-library"></a>
### 07. Empty scenario library — Needs work

Scenario library shares table controls but has a wide set of default columns. Assess ability to find and act on a scenario at narrow width.

Work items: UX-13

![Step 07: Empty scenario library](07-scenario-library.png)

<a id="08-scenario-start"></a>
### 08. Scenario start — Needs work

Empty library creation offers Copy existing first despite no scenarios; five technical model choices precede script selection. Save draft is enabled on an untouched start.

Work items: UX-13

![Step 08: Scenario start](08-scenario-start.png)

<a id="09-script-picker"></a>
### 09. Scenario script picker — High priority

Opening the picker pushes its table and surrounding content behind the sidebar at 923px. Focused search is visible far to the right; table geometry corroborates overflow. A future source-step warning appears early.

Work items: UX-01, UX-11, UX-12

![Step 09: Scenario script picker](09-script-picker.png)

<a id="10-run-settings"></a>
### 10. Run settings — Mostly sound

Guided VUser worksheet explains totals and phases clearly. Review compact layout and defaults; Run Settings is marked complete on first arrival.

Work items: UX-12

![Step 10: Run settings](10-run-settings.png)

<a id="11-data"></a>
### 11. Optional data — Needs work

Advancing to Data preserves a lower scroll position instead of revealing the new heading. Snapshot semantics are clear; disabled fields take space before a pool is selected.

Work items: UX-12

![Step 11: Optional data](11-data.png)

<a id="12-scenario-validation"></a>
### 12. Model validation — Mostly sound

Model validation gives explicit capacity, data, placement, demand and runtime results. Save & Create Test Run coexists with text saying saving does not launch; clarify next step.

Work items: UX-11

![Step 12: Model validation](12-scenario-validation.png)

<a id="13-unsaved-dialog"></a>
### 13. Unsaved-change dialog — Sound

Unsaved-change guard provides Keep editing, Discard and Save; initial focus lands on safe Keep editing. Draft is audit-only and will be discarded.

Work items: Preserve the strengths noted above.

![Step 13: Unsaved-change dialog](13-unsaved-dialog.png)

<a id="14-new-test"></a>
### 14. New Test prerequisites — Needs work

No scenarios available. Clear builder detour exists, but downstream run setup, scheduling and lengthy empty summary remain visible. No run was launched.

Work items: UX-13, UX-15

![Step 14: New Test prerequisites](14-new-test.png)

<a id="15-schedules"></a>
### 15. Recurring schedules — Needs work

Recurring schedule defaults to UTC while one-off Scheduled Start uses America/New_York. Timezone is visible but defaults differ; use one explicit scheduling convention and show next occurrences.

Work items: UX-15

![Step 15: Recurring schedules](15-schedules.png)

<a id="16-command-entry"></a>
### 16. Run selector — Needs work

Active-run selector includes duplicate names with identical statuses, with no date or reference to disambiguate them. Queued and Reserved are mixed under active.

Work items: UX-14

![Step 16: Run selector](16-command-entry.png)

<a id="17-reserved-run"></a>
### 17. Reserved run — High priority

Reserved run has Start Test, yet Current/Actual Load says 1 VU and reports zero errors/latency as values. Separate planned allocation from measured active load before execution.

Work items: UX-02

![Step 17: Reserved run](17-reserved-run.png)

<a id="18-results"></a>
### 18. Results library — Mostly sound

Completed results mix Complete/Failed lifecycle, assessment labels, and archive Pending; long-failed runs still say Pending without visible expiry/recovery explanation.

Work items: UX-05

![Step 18: Results library](18-results.png)

<a id="19-report-summary"></a>
### 19. Report summary — High priority

Summary correctly separates execution, acceptance and evidence. However nine samples, unknown termination/elapsed time and 1 peak VUser versus 22 planned merit a prominent insufficient-evidence conclusion above numeric KPIs.

Work items: UX-03

![Step 19: Report summary](19-report-summary.png)

<a id="20-baseline-compare"></a>
### 20. Baseline comparison — High priority

The selected baseline shows large latency/throughput/VUser deltas without an explicit comparability qualification for the current sparse run.

Work items: UX-04

![Step 20: Baseline comparison](20-baseline-compare.png)

<a id="21-evidence"></a>
### 21. Evidence details — Mostly sound

Evidence exposes critical telemetry breaches and missing tiers, but summary only gives 85% coverage. Make critical quality warnings and recommended recovery prominent in Summary.

Work items: UX-03

![Step 21: Evidence details](21-evidence.png)

<a id="22-export-preview"></a>
### 22. Export preview — Mostly sound

The light export document is intentional print presentation, not a dark-theme defect. It retains outcome/acceptance caveats and distinguishes transaction and request populations. No export was saved.

Work items: Preserve the strengths noted above.

![Step 22: Export preview](22-export-preview.png)

<a id="23-export-risks"></a>
### 23. Export risk narrative — High priority

Export Top Risks says no significant error patterns despite critical telemetry gaps and sparse samples; Business Impact is generic metric prose. Findings heading is empty.

Work items: UX-03

![Step 23: Export risk narrative](23-export-risks.png)

<a id="24-monitoring"></a>
### 24. Monitoring — Mostly sound

Monitoring states deployment-wide health explicitly and distinguishes optional Kubernetes from blockers. Local auth bypass appears as an advisory.

Work items: Preserve the strengths noted above.

![Step 24: Monitoring](24-monitoring.png)

<a id="25-repository"></a>
### 25. Repository — Needs work

Repository explicitly says its domain/project/release filters are independent of sidebar workspace. This is useful disclosure but adds a second scope model.

Work items: UX-18

![Step 25: Repository](25-repository.png)

<a id="26-access"></a>
### 26. Access — Needs work

Access combines global tabs, related-admin links, user subtabs, filters, wide table and a details panel. User row action cell is named Service account actions in DOM; verify screen-reader semantics.

Work items: UX-10, UX-16

![Step 26: Access](26-access.png)

<a id="27-admin-settings"></a>
### 27. Admin Settings — Mostly sound

Admin Settings provides search, grouped destinations, shared panel collapse, explicit deployment scope and restart-required labels. Narrow navigation adds a separate scroll region.

Work items: Preserve the strengths noted above.

![Step 27: Admin Settings](27-admin-settings.png)

<a id="28-settings-search"></a>
### 28. Settings search — Needs work

Typing theme into Find settings yields No settings match even though Personal preferences contains Dashboard Theme. Search needs field names and synonyms, not destination titles alone.

Work items: UX-08

![Step 28: Settings search](28-settings-search.png)

<a id="29-user-settings"></a>
### 29. User Settings — Sound

Non-admin Settings exposes appearance with browser-local persistence and no administrative form; matches shared visual language. Other sections require expanding navigation at narrow width.

Work items: UX-08

![Step 29: User Settings](29-user-settings.png)

<a id="30-user-admin-link"></a>
### 30. User-to-admin link — Needs work

In User preview, Monitoring → Controller settings opens personal Appearance with no explanation and no visible return-to-monitoring control. Access is restricted, but the link promises the wrong destination.

Work items: UX-09

![Step 30: User-to-admin link](30-user-admin-link.png)

<a id="31-help-entry"></a>
### 31. Help entry — Needs work

Operator User Guide and Quick Start are nested under API Guide, whose default page leads with REST responses and dev headers. Rename/reorganize as Help & documentation with task-led entry.

Work items: UX-17

![Step 31: Help entry](31-help-entry.png)

<a id="32-import-wide"></a>
### 32. Import at desktop width — Needs work

At 1440px, New script folder name uses a native gray 360×20px input while adjacent fields use theme styling. At 923px the same control stretches to a tall block. Keyboard Tab proceeds to Back.

Work items: UX-07

![Step 32: Import at desktop width](32-import-wide.png)

<a id="33-expanded-pagination-wide"></a>
### 33. Expanded pagination at desktop width — Needs work

Desktop reproduction: health filter All 27 but pagination Showing 1–10 of 28 after expanding one script. Expanded detail row consumes an item slot.

Work items: UX-06

![Step 33: Expanded pagination at desktop width](33-expanded-pagination-wide.png)

<a id="34-missing-archive"></a>
### 34. Missing archive — High priority

Failed jms-rel-run results open No archive available, with generic this run is complete and no run name/status/reference. Execution snapshot is the sole recovery action. Preserve identity/failure status and distinguish unavailable from pending archives.

Work items: UX-05

![Step 34: Missing archive](34-missing-archive.png)

<a id="35-failed-snapshot"></a>
### 35. Execution snapshot fallback — High priority

The snapshot recovery action initially opens Archive, whose visible panel contains only Archive Report. A publication-delay banner appears in the subsequent diagnostics view, so recovery is incomplete rather than wholly absent.

Work items: UX-05

![Step 35: Execution snapshot fallback](35-failed-snapshot.png)

<a id="36-failed-diagnostics"></a>
### 36. Failed-run diagnostics — High priority

Historical failed-run diagnostics retain Live Summary and Healthy telemetry labels; timestamp context is unclear and no failure cause is surfaced here. A delayed-publication banner appears, but the initial snapshot fallback landed on an empty Archive panel.

Work items: UX-02, UX-05

![Step 36: Failed-run diagnostics](36-failed-diagnostics.png)

## Evidence limits

This is a browser UX audit, not a complete functional, security or WCAG certification. No new load was launched, existing run stopped, script/import saved, schedule created, account changed or report published. Actual login/SSO, enforced non-admin RBAC, every protocol, light theme, screen-reader output, contrast measurements and full keyboard coverage were not tested. Historical imported records may lack metadata; recommendations concern honest presentation of those states, not proof of an ingestion defect. The visible controller build identifies 8 September while the dashboard is served live from the current checkout; verify recovery behavior on the intended deployment build during implementation. Existing source changes were preserved; no application code changed in this audit.
