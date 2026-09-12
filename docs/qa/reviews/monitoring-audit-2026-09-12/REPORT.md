# Monitoring usability and functional-completeness audit

Reviewed September 12, 2026, against the running local APEX Load dashboard.

**Verdict: useful operational coverage, with incomplete paths from status to diagnosis and resolution.** The five tabs cover the right areas, but status wording, hidden calendar events, collector onboarding, and keyboard behavior need attention before this can be treated as a dependable enterprise monitoring workspace.

## Scope and evidence

User goal: determine whether the platform can run tests, identify what needs attention, inspect monitoring coverage, and understand upcoming work and capacity.

Inspected the running application at `127.0.0.1:8080`, using the Codex in-app browser, in the existing ACC project / Legacy application / Imported Legacy release context. The runtime identifies itself as local development with authentication bypass, SQLite, in-memory cache, no distributed generator inventory, no registered collectors, and no configured APM profiles. Existing queued and recurring runs populated the calendar. Desktop and narrow layouts were inspected; the narrow browser reported 704 CSS pixels of content width.

All screenshots were captured and inspected during this audit. Source inspection corroborates behavior and identifies implementation limitations; it is explicitly distinguished from live verification below. Existing uncommitted work was present. No product source was edited, settings saved, collectors registered, tokens issued, tests executed, bookings reserved, or services restarted.

## Flow and health

| Step | Task and captured states | Health |
|---|---|---|
| 1 | Overview: initial narrow layout and desktop health/readiness | Usable; weak prioritization and ambiguous monitoring coverage |
| 2 | Fleet: capacity, empty inventory, alerts, audit | Partially complete; good setup links, weak explanations and drill-down |
| 3 | Telemetry: catalog, expanded APM/pack details, collector registration | Needs improvement; setup instructions and form layout interrupt onboarding |
| 4 | Alerts: empty state, keyboard navigation, policy settings, return to Alerts | Needs improvement; warning visibility and coverage wording undermine confidence |
| 5 | Schedules: month/week/day, run detail, booking form, unsaved draft, narrow layout | Broad functionality; hidden-event access and context preservation are incomplete |

## What works

- Five recognizable tabs keep the scope manageable. Selected tabs have both visible styling and tab semantics.
- Overview distinguishes controller connectivity from production readiness. Optional Kubernetes is explicitly “Not applicable” in local mode; the auth-bypass warning explains the actual degraded check.
- Fleet explicitly separates local execution from generator inventory and offers relevant configuration links.
- Telemetry differentiates native support from integration contracts and says that support does not prove live data reception.
- Month, week, and day views respond. Day view makes full run names easier to scan, and selecting a run exposes workload, test reference, status, and an Open Test action.
- Booking has separate availability and reservation actions. Leaving a modified draft triggered an unsaved-changes dialog; discarding only the audit’s temporary value worked.
- Alert policy settings opened at the correct section. “Return to originating workflow” restored the Alerts tab and project/application/release context.
- Narrow schedules reflow into an agenda without document-level horizontal overflow in the inspected state.

## Prioritized findings

### 1. High — “Clear” does not establish that SLA monitoring is configured or has sufficient evidence

**Evidence: steps 1 and 4, captures 02, 07, 11; source corroboration.** The SLA card reports “CLEAR” and “No global SLA threshold breaches on running tests.” Following its policy link showed the global error-rate, P99, server-processing, and transfer thresholds unconfigured. The card does not surface that coverage state. This does not mean every possible test-specific quality gate is disabled; it means this card’s claim does not explain the coverage of the global policies it describes.

The source derives “clear” from an empty active-alert array, without a policy-configuration or observation-window check: [MonitoringPage.tsx](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/dashboard/src/components/MonitoringPage.tsx:572).

**Recommendation:** distinguish “Not configured,” “Waiting for samples,” “No breaches observed,” and “Active breaches.” Show the number of enabled policies and last evaluated sample, with a direct Configure policies action. Keep alert-feed freshness separate from ordinary page refresh.

**Acceptance:** a fresh session with no enabled global thresholds cannot present the same state as a configured, recently evaluated policy with no breaches.

### 2. High — The calendar hides runs behind non-interactive “+N more” text

**Evidence: step 5, capture 08; source corroboration.** Four continuing queued runs occupy each early-month cell, hiding additional cron executions behind “+1 more.” That text is a span rather than an action. Users must discover a filter or change view to reach the hidden run. Long-lived queued runs also dominate many days without clearly explaining whether they are executing or merely waiting.

[MonitoringPage.tsx](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/dashboard/src/components/MonitoringPage.tsx:2420) renders the overflow indicator without an interaction. Week/day switching works, so this is a broken direct discovery path rather than total loss of access.

**Recommendation:** make “+N more” open the selected date’s complete agenda, retain the selected date, and offer a way to collapse continuing queued work. Use “Queued since…” to distinguish waiting from execution.

**Acceptance:** every hidden run can be reached from its calendar cell using mouse and keyboard; four continuing runs cannot permanently obscure a new execution.

### 3. Medium — The advisory warning cannot be investigated from Alerts

**Evidence: steps 1 and 4, captures 02 and 07.** “1 advisory warning” remains above an Alerts tab containing Clear, No diagnostic samples, and No inventory. The actual warning is the auth-mode readiness issue visible in Overview. The chip exposes only a title tooltip (“Readiness: 1 degraded”) and is not a navigable action.

**Recommendation:** include readiness/controller advisories in the Alerts workspace, or make the summary open a list with a link to the responsible card. Preserve the distinction between deployment advisories and test alerts.

**Acceptance:** every warning counted in the top summary has an accessible explanation and next action reachable from that summary.

### 4. Medium — Collector registration has a concrete layout defect

**Evidence: step 3, capture 06 and loaded accessibility snapshot.** Metric-pack checkboxes appear in the middle of wide rows while their labels start at the left on another line. Status and signal text run together, for example “Native supportcpu_process_pct.” This makes the relationship between a checkbox, a pack, and its signals difficult to scan.

Source corroboration: [MetricPackPicker.tsx](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/dashboard/src/components/MetricPackPicker.tsx:18) lacks a structured row treatment, while [parityPanels.css](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/dashboard/src/components/parityPanels.css:224) applies full-width field styles to all inputs, including checkboxes.

**Recommendation:** give each pack a compact checkbox row, a clearly separated support badge, and a second line for signals. Scope text-input styles to the relevant input types. Use a shorter accessible label with supplementary descriptions.

**Acceptance:** each checkbox is visually adjacent to its pack name, text has proper separation, and the picker works at desktop and narrow widths.

### 5. Medium — Telemetry presents capabilities but does not complete setup-to-verification workflows

**Evidence: step 3, captures 04–06.** Expanding OpenTelemetry provides environment-variable instructions and says to check datasource/collector freshness, but supplies no contextual setup guide, configuration action, or connection-verification result. JVM’s integration contract is similarly a technical instruction to publish archive metrics. Four narrow columns and nested scroll areas make these details harder to use while much of the page remains empty. Amber styling for supported capabilities further resembles an operational fault.

**Recommendation:** separate “Available capabilities” from “Connected and receiving data.” Use wider detail panels with explicit Configure, Setup guide, Verify connection, and View latest data actions appropriate to each integration. Reserve warning styling for actual degraded or incomplete required configuration, rather than all optional catalog entries.

**Acceptance:** an operator can move from choosing an integration to confirming its data is arriving, with unsupported automation clearly identified and a usable manual setup path.

### 6. Medium — Collector data can look current while it is a one-time snapshot

**Evidence: step 3, capture 04 anchors the collector surface; this is a source-confirmed limitation, not a live heartbeat test.** The page refreshes available monitoring data every five seconds, but `MonitoringCollectorsCard` loads its list on mount/scope change and after its own mutations. The Metrics action makes a single request and opens a dialog titled “Live metrics”; there is no polling or Refresh action in that dialog. The page’s updated timestamp can therefore be newer than collector information.

References: [collector load](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/dashboard/src/components/MonitoringCollectorsCard.tsx:160), [metrics fetch](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/dashboard/src/components/MonitoringCollectorsCard.tsx:248), [parent refresh](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/dashboard/src/components/MonitoringPage.tsx:789).

**Recommendation:** integrate collectors into the refresh lifecycle and show per-source last-success/age. Either refresh the metric dialog or name it “Latest captured metrics” and provide Refresh. Add a retry action for collector-list failures.

**Acceptance:** a collector heartbeat received while the tab is open updates its visible status within the documented interval, and a snapshot cannot be mistaken for a live stream.

### 7. Medium — Keyboard tab navigation loses focus; calendar selection state is visual only

**Evidence: steps 4 and 5; `keyboard-focus-evidence.txt`, captures 08–09.** ArrowLeft from Schedules selected Alerts, but focus moved to the main region. A subsequent Left key did not continue through the tabs. The route-focus effect corroborates the focus move: [App.tsx](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/dashboard/src/App.tsx:3061).

Month/Week/Day and All/Cron/Ad-hoc/Plans/Current render as ordinary buttons with an `active` class, without an exposed pressed/selected state: [MonitoringPage.tsx](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/dashboard/src/components/MonitoringPage.tsx:2320).

**Recommendation:** preserve focus on the newly selected tab when navigation originates in the tab strip. Expose mutually exclusive view/filter selection through appropriate button or tab semantics. Verify focus order and visible focus across the complete flow.

**Acceptance:** repeated arrow keys navigate all five tabs without requiring the user to find the strip again; assistive technology can identify the active calendar view and filter.

### 8. Medium — SLA alert acknowledgement is session-local, with no durable triage history here

**Evidence: step 4, capture 07 anchors the surface; source-confirmed, not reproduced using live breaches.** Threshold alerts and their acknowledgement are stored in React state; the client bounds the list at 50. Fleet acknowledgement uses a backend API. These are materially different behaviors under similar alert workflows. An operator cannot assume SLA acknowledgement is shared with another operator or survives reload, and this page has no searchable persisted SLA incident history.

References: [App.tsx](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/dashboard/src/App.tsx:400), [acknowledgement callback](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/dashboard/src/App.tsx:755), [thresholdAlerts.ts](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/dashboard/src/thresholdAlerts.ts:33).

**Recommendation:** document the present session boundary in the UI; for enterprise triage, persist alert lifecycle and acknowledgement with actor/time, and add severity/status/time filters and history. Do not describe acknowledgement as resolution while a breach remains active.

### 9. Medium — Fleet capacity and audit summaries need stronger explanations

**Evidence: step 2, capture 03; source corroboration for audit sampling.** Effective Available is zero and Project Pending Slots is 32, but the card does not explain what is waiting or offer a drill-down to affected work. Project demand is partly labeled with a UUID. The empty audit card suggests adjusting filters/timeframe, but the controls are elsewhere.

The Fleet audit request reads the latest 20 general audit events, then filters locally and displays up to eight. A “quiet” result can therefore omit fleet activity outside that sample: [MonitoringPage.tsx](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/dashboard/src/components/MonitoringPage.tsx:766). This is not a claim that omitted events existed during this run.

**Recommendation:** explain capacity units and pending-demand scope, use project names, and link to affected queued runs. Label audit sampling explicitly or request fleet-filtered history server-side. Present one recommended fleet setup action for local/unconfigured deployments.

### 10. Lower — Overview hierarchy and schedule context add avoidable friction

**Evidence: steps 1 and 5, captures 01–02, 08–10, 12.** Version strings occupy substantial Overview space while the advisory’s cause sits lower in the readiness card. Calendar detail displays a cross-date queued interval as “May 9, 9:47 PM – 8:58 AM,” omitting the end date. Day view offers no equivalent to the date-cell Schedule a run button. Returning to Schedules after visiting another tab reset the inspected Day view to Month. The calendar’s visible counts combine source categories and overlapping state counts without explaining their relationship.

**Recommendation:** lead Overview with actionable health/readiness and move build detail into a disclosure. Show both dates for multi-day intervals, expose the display timezone, preserve calendar view/date/filter in navigation, and offer a persistent Schedule run action distinct from New booking. Explain that Current and Projected overlap the source totals.

## Functionality coverage and remaining gaps

| Area | Available now | Completeness assessment |
|---|---|---|
| Platform health | API/WebSocket, runtime/build identity, readiness checks, services | Useful current status; warning drill-down and coverage wording need work |
| Fleet | Capacity, inventory entry, alerts, audit link, configuration handoffs | Broad surface; zero-inventory state verified, populated operations not verified |
| APM and packs | Capability catalog, setup text, native/contract distinction | Partial onboarding; no observed end-to-end connection/ingestion verification path |
| Collectors | Registration form; source includes metrics, editing, token rotation, deletion | Management surface exists; registration layout and freshness gaps remain; no credentials issued during audit |
| Alerts | SLA, telemetry, fleet cards; policy and notification links; source includes Open/Ack | Operational feed, not yet a complete shared incident workflow |
| Scheduling | Calendar views/filters, queued and projected events, detail, Open Test, booking form | Broadest functionality; hidden-event access, creation consistency, and state retention need improvement |
| Historical monitoring | Current/sampled health and metrics; links to other product areas | No unified time-range investigation or historical telemetry charts observed in these tabs; decide whether to provide this here or clearly route to Results/APM |

## Accessibility risks and limits

Confirmed issues are the keyboard focus interruption, missing programmatic calendar selection, and collector label/checkbox layout described above. Statuses generally include text alongside color, and dialogs have names and usable cancellation/return behavior. Muted text, small labels, and small calendar controls warrant contrast and target-size measurements. No screen-reader session, automated accessibility scan, formal contrast audit, full zoom matrix, or compliance determination was performed.

The local environment had no distributed fleet, collectors, active threshold-breach samples, or connected APM data. Successful collector deployment, token lifecycle, metric ingestion, alert delivery/acknowledgement across users, restart/drain operations, reservation conflict handling, and production RBAC remain unverified. The booking form was inspected but availability was not submitted: the browser automation did not retain entered datetime values. This is an audit-tool limitation, not evidence that the product’s date fields are broken. The only temporary form value was discarded. No load run was started to manufacture evidence.

Existing frontend changes and the running backend’s build identity mean source findings should be validated against the final built application before implementation sign-off. This was an evaluation; no code changes or regression suite were required or run.

## Recommended order

1. Fix trustworthy states and access: SLA configuration/observation state, actionable advisory summary, and calendar overflow drill-down.
2. Repair collector form layout, refresh behavior, and keyboard continuity.
3. Complete integration setup/verification paths and durable alert triage, then improve Fleet explanations and calendar context retention.

## Current-run screenshot walkthrough

### Step 1 — Overview: usable, needs clearer prioritization

Initial narrow entry shows a large control-plane/build card ahead of readiness. The desktop view makes all three cards visible and clearly distinguishes optional Kubernetes from degraded auth mode.

![01 — Initial Overview in the narrow app panel](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/monitoring-audit-2026-09-12/01-overview.png)

![02 — Desktop Overview and readiness](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/monitoring-audit-2026-09-12/02-overview-desktop.png)

### Step 2 — Fleet: partial, with useful setup handoffs

Local mode and missing inventory are explicit. Pending demand lacks an affected-work drill-down; the audit sample needs a clearer scope statement.

![03 — Fleet capacity and empty inventory](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/monitoring-audit-2026-09-12/03-fleet.png)

### Step 3 — Telemetry and collectors: onboarding needs repair

The catalog differentiates support levels, but expanded instructions are constrained by narrow, internally scrolling cards. Registration’s checkbox layout is visibly broken.

![04 — Telemetry catalog and collector empty state](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/monitoring-audit-2026-09-12/04-telemetry.png)

![05 — Expanded OpenTelemetry and JVM instructions](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/monitoring-audit-2026-09-12/05-telemetry-expanded.png)

![06 — Collector registration and pack-picker alignment defect](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/monitoring-audit-2026-09-12/06-register-collector.png)

### Step 4 — Alerts and policy handoff: trust gaps, working return path

Alerts does not surface the readiness advisory. The policy handoff works and shows unconfigured global thresholds; returning restores Alerts and its scope. Keyboard evidence is saved separately in `keyboard-focus-evidence.txt`.

![07 — Alerts with an unexplained advisory and Clear SLA state](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/monitoring-audit-2026-09-12/07-alerts.png)

![11 — Alert policy handoff with unconfigured thresholds and return link](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/monitoring-audit-2026-09-12/11-alert-policy-handoff.png)

### Step 5 — Schedules: useful depth, incomplete overflow and context handling

Month view hides executions behind non-interactive overflow text. Week and Day switches work. Run detail exposes useful workload/context; cross-day dates and waiting status need clarity. Booking provides separate checking/reserving controls and an unsaved-change guard. Narrow layout reflows to an agenda.

![08 — Calendar with continuing queued runs and hidden events](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/monitoring-audit-2026-09-12/08-schedules.png)

![09 — Day agenda and queued-run details](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/monitoring-audit-2026-09-12/09-schedule-details.png)

![10 — New scheduled booking form, no reservation submitted](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/monitoring-audit-2026-09-12/10-new-booking.png)

![12 — Narrow schedule layout reflows into an agenda](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/monitoring-audit-2026-09-12/12-schedules-narrow.png)
