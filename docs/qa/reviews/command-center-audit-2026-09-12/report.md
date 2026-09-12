# Command Center usability and functional completeness audit

Evaluated September 12, 2026. **Verdict: the selected-run workspace has substantial operational functionality, but the empty and multiple-run entry experiences are incomplete.** The page is currently better at inspecting one known run than helping an operator discover, prioritize, and move between concurrent runs.

## Scope and evidence

Reviewed the running local application (build `v0.1.0-dev.20260912+6d53cf5`), fresh browser captures, semantic browser state, and relevant current source. Real data supplied the empty BOBPM workspace and two queued ACC runs. A temporary, read-only localhost proxy supplied six synthetic active runs (four running, one paused, one pending) and a catalog HTTP 503 response. It served the existing application and forwarded only GET requests to the local controller; all mutation methods were blocked. No load tests were started, paused, stopped, or aborted. The temporary server was stopped after capture.

The synthetic example establishes rendering and navigation behavior, not live execution correctness. WebSocket transport was intentionally unavailable in that example; its Disconnected label, absent histories/evidence, and synthetic metric relationships are not findings about the production telemetry pipeline. Existing working-tree changes were left intact. No application source was changed and no implementation test cycle was run.

Captured desktop and compact layouts. Accepted screenshots below were saved and reopened during this audit. Some desktop capture attempts were replaced with complete captures. Responsive coverage is limited; this is not a full accessibility or device certification.

## Highest-impact findings

| Priority | Finding | User impact | Recommended behavior | Evidence |
|---|---|---|---|---|
| P1 | Active-run overflow opens Scenario Library | With six active runs, clicking `+4 runs` leaves execution monitoring and displays saved scenarios. The label promises access to active runs but takes the operator elsewhere. | Open an all-active-runs panel or Command Center overview, retaining workspace scope. | Steps 3 and 6; `App.tsx:3210` |
| P1 | Discovery failure masquerades as an empty workspace | A failed catalog request produces `No active run selected` and start-test advice, without a local error or Retry. An operator cannot distinguish no runs from unavailable data. | Separate loading, empty, error, and stale states; retain the last successful list and show last update plus Retry. | Step 7; `CommandCenterPrototype.tsx:735` |
| P2 | No multi-run triage information | The chooser displays name, reference, creation timestamp, and lifecycle status only. An error-heavy running test is visually equivalent to healthy running tests. There is no search, health sort, elapsed/remaining time, current/target load, TPS, error rate, or warning summary. | Add a compact active-run table with lifecycle and health separated, key metrics, search, status filters, and an attention-first sort. Show upcoming/waiting runs separately with their reason or scheduled time when available. | Step 3 |
| P2 | Selection is transient and the chooser is hard to recover | Clicking a run card opens its controls without changing the URL. Clicking Command Center again keeps that selection; reloading returns to the chooser. The inspected run can be absent from both topbar chips. | Route card selection to the run ID; provide a persistent run switcher and `All active runs`; mark the current run visibly and semantically. Preserve each run's drafts independently. | Steps 3–4; `CommandCenterPrototype.tsx:1316` |
| P2 | Empty-state advice lacks its primary action | The text says to start a test, but the only in-page button is Open Results. The title says no run is selected rather than confirming whether the current scope has no active runs. | Say `No active runs in [scope]`; provide primary New Test and secondary Browse scenarios / Results, with permission-aware availability. Resolve missing workspace selections in place. | Step 1; `CommandCenterPrototype.tsx:1543` |
| P2 | Empty analysis controls offer no useful outcome | Open analysis adds OPS VIEW and GRAPH WALL while leaving the same empty message. | Hide or explain unavailable run-specific view controls until a run is selected. Use one consistent operations/graphs/analysis hierarchy once selected. | Step 1; `CommandCenterPrototype.tsx:1296` |

P1 means a functional defect that can misdirect or misinform an operator. P2 means a meaningful workflow or usability gap. There is no P0 finding from this audit.

## Functional coverage

| Operator task | Assessment |
|---|---|
| Understand that a queued run has not produced samples | Good: explicit Planned load, Not started, and No samples; authored load profile remains available. |
| Open one of two known runs | Available: full-card buttons, references and timestamps distinguish runs. |
| Discover the run needing intervention | Incomplete: lifecycle badges alone do not establish health or urgency. |
| Move between more than two runs | Defective overflow destination; no persistent all-run switcher. |
| Share/bookmark a run selected from the chooser | Incomplete: chooser selection does not update the route. Topbar chips do have run-specific links. |
| Start the next test from an empty workspace | Available in global navigation, missing as a contextual primary action. |
| Inspect and adjust a selected run | Broad visible coverage: load targets, immediate/gradual transitions, duration, pause, stop, abort, notes, advanced details, scenario/error/infrastructure tables, OPS and Graph Wall. |
| Confirm an operational action | Good observed behavior: pause dialog names the exact run and explains action-boundary behavior; Escape dismisses it. Submission was deliberately not exercised. |
| Recover from discovery failure | Missing local error and explicit Retry. |
| Reach historical results from a scope lacking a release | Navigation works, but lands on `No release selected` with no in-page selector/action. |
| Determine data freshness | No discovery timestamp or stale-list treatment. Live transport resilience was not assessed with the synthetic fixture. |

## Captured walkthrough

### 1. Empty workspace, analysis, and Results handoff — needs improvement

Real BOBPM scope has no active runs. The empty state is calm and readable, but gives no direct New Test action, no explicit scope in its message, and no distinction between no selection and no active runs. Open analysis changes only the toolbar. Open Results successfully navigates, then stops at the missing-release prerequisite. That extra prerequisite should be explained or resolved before handoff.

![Empty Command Center](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-audit-2026-09-12/04-empty.png)

![Analysis controls with no run](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-audit-2026-09-12/05-empty-analysis.png)

![Results handoff without a selected release](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-audit-2026-09-12/06-empty-results-destination.png)

### 2. Two queued runs and a selected planned run — mostly sound

The two real queued runs are distinguishable by name and reference. Selecting one reveals planned load and clear no-sample states. Start Test, Abort, notes, and advanced details are present. The picker does not explain whether a queued item is awaiting a manual start, a schedule, or capacity; it surfaces creation dates rather than a wait reason. The compact planned-load value truncates visually, although the accessible text and explanatory panel preserve the meaning.

![Two queued runs](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-audit-2026-09-12/02-multiple-queued-desktop.png)

![Selected queued run](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-audit-2026-09-12/03-queued-run.png)

### 3. Six active runs — inadequate for triage

Controlled example with four RUNNING, one PAUSED, and one PENDING item. The entire row is clickable and all states have text labels. The list lacks health and scale information even though the selected-run screen can show those metrics. Similar names are severely shortened in the compact topbar. The two chips show recent running entries rather than the error-heavy test. Lifecycle RUNNING should remain distinct from a health assessment.

![Six active runs](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-audit-2026-09-12/10-multiple-runs-compact.png)

### 4. Select an active run and return/reload — controls strong, navigation weak

The peak-traffic run opens via click and Enter. Its controls identify the name/reference and surface load, remaining time, TPS, and error rate. The URL remains the generic Command Center URL. Sidebar Command Center does not return to the chooser; reload does. The selected peak-traffic run does not appear in either of the two topbar chips. No All active runs control is available here.

![Selected active run](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-audit-2026-09-12/09-selected-active-run.png)

### 5. Pause confirmation — good safeguard

The dialog names Checkout API — peak traffic and AUDIT-RUN-2, explains that VUsers pause at their next action boundary, and initially focuses Cancel. Escape dismisses the dialog. No pause request was submitted. This is a pattern worth preserving as run switching improves.

![Pause confirmation](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-audit-2026-09-12/11-pause-confirmation.png)

### 6. Active-run overflow — broken destination

Clicking +4 runs navigates to Scenario Library, confirmed by the heading, URL, and saved-scenario table. This is a route mapping defect, not simply an unclear label. The overflow link should reveal the other active runs without making the operator hunt through saved scenarios.

![Overflow opens Scenario Library](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-audit-2026-09-12/08-overflow-scenario-library.png)

### 7. Active-run catalog fails — misleading empty state

Returning HTTP 503 from the active-run catalog yields the same No active run selected message and start-test guidance as a legitimate empty result. No catalog error or retry action appears in the Command Center. Source confirms the catch handler clears discovered runs. The synthetic fixture's disconnected transport indicator is not the evidence for this finding; the missing catalog error is.

![Catalog failure appears as empty state](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/command-center-audit-2026-09-12/12-discovery-failure.png)

## Accessibility observations and limits

Positive observations: run cards are native buttons and activate with Enter; badges include text; the page has a main heading and named regions; pause confirmation is a named dialog, focuses Cancel, and dismisses with Escape.

Risks: the multi-run chooser's accessible region is still named `No active command center run`; topbar lifecycle status is carried by a colored dot and title tooltip rather than persistent visible text; run links have no current-selection semantics; compressed names and small muted metadata make rapid identification harder. Add clear current-run indication and ensure status and full identifiers remain available without hover. The replacement of a card list with a run workspace also needs a deliberate focus/announcement policy.

Full Tab order, screen-reader output, focus restoration for every dialog, contrast ratios, 200% zoom, and all responsive breakpoints were not tested. Do not infer WCAG compliance or failure from screenshots alone.

## Source anchors

- [Overflow routing](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/dashboard/src/App.tsx:3210)
- [Discovery error handling](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/dashboard/src/components/CommandCenterPrototype.tsx:735)
- [Transient selection wiring](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/dashboard/src/components/CommandCenterPrototype.tsx:1316)
- [Empty-state content/actions](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/dashboard/src/components/CommandCenterPrototype.tsx:1541)
- [Unconditional analysis controls](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/dashboard/src/components/CommandCenterPrototype.tsx:1296)
- [Discovery scope](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/dashboard/src/components/commandCenterRunDiscovery.ts:24)

Additional source-only concern: discovery filters project and application but has no release parameter, while a release is displayed in the sidebar and Results requires release scope. Define and communicate whether Command Center is application-wide or release-specific; cross-release behavior was not directly exercised in this audit.

## Recommended implementation sequence

1. Fix overflow routing and distinguish discovery error/stale/empty states.
2. Route every selection to a run ID; add a persistent, searchable run switcher with All active runs and current selection.
3. Add a multi-run overview that shows status, health, load, throughput, errors, time remaining, freshness, and waiting reasons where available.
4. Complete the empty state with explicit scope and a permission-aware New Test action; hide irrelevant analysis switches and resolve missing release prerequisites.

Acceptance should cover zero/one/many runs; running/paused/pending combinations; a failing catalog after a successful load; long duplicate names; a selected run outside the first two; chooser selection/back/reload; lifecycle transitions; cross-release scope; and keyboard navigation. Implementation should follow the repository's unit and browser workflow test policy.
