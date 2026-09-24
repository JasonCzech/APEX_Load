# Importer review consistency audit

Date: 2026-09-23

## Scope

This audit compares the current importer catalog and the non-LoadRunner importer families against the LoadRunner review pattern now in the dashboard. Evidence was captured from the live dashboard on port 4012.

The catalog exposes 16 importers outside LoadRunner. They resolve to five review architectures:

| Architecture | Importers |
| --- | --- |
| Shared protocol asset review | OpenAPI, Postman, WSDL, AsyncAPI, RAML, DevWeb, k6, Akamai CloudTest, Event Hubs Capture, Broker Config, HAR, Recorder Export |
| Shared sample review | Request / Response Sample |
| JMeter review | JMeter |
| Bruno review | Bruno |
| Review-only report | Oracle E-Business / Siebel |

## User goal and accessibility target

An operator should be able to move among importers without relearning where issues, credentials, data, runtime settings, generated DSL, and source evidence live. The common shell should remain keyboard navigable and expose clear labels, statuses, and recovery actions. Importer-specific fidelity controls must stay available where they are needed.

## Overall verdict

The importers already share a strong six-tab foundation and consistent source-to-review workflow. The largest gap is inside the Review tab: LoadRunner now uses a persistent issue list with specific evidence and in-context repair, while the other families still expose findings through summary cards and detached dialogs. The UI looks related, but the operator workflow is not yet consistent.

## Strengths

- The catalog clearly groups all 17 sources by job and provides searchable, descriptive cards.
- OpenAPI, JMeter, Sample, and most protocol importers reuse the same tab names and order.
- Tab lists expose tab/tabpanel semantics, selected state, and readable count text.
- Severity is expressed in text as well as color in issue dialogs.
- Importer-specific capabilities remain visible: JMeter multi-script/scenario output, Bruno native-versus-canonical execution, HAR comparison and scrubbing, and Oracle review-only posture.
- Source and conversion controls explain that changes require a new review, which protects the operator from silently reviewing stale output.

## UX risks

### 1. Issue resolution is inconsistent and loses context

JMeter, OpenAPI, and Oracle route the operator from a summary into `IssueDetailsDialog`. The dialog has only a Close action. It does not show the selected issue beside source evidence, offer a repair, or route directly to the relevant Credentials, Data, Runtime, DSL, or Source tab.

Recommendation: use the LoadRunner vertical issue rail and detail pane in every executable importer. Group repeated findings by rule, show occurrence counts and affected assets, and place the most relevant fix or handoff at the top of the detail pane.

### 2. Repeated diagnostics are not grouped into operator decisions

The JMeter blocker dialog shows three cards with the same generic title, `Blocking Import Diagnostics`, and a diagnostic code. Each says three diagnostics require review, but the operator cannot tell what setting to change or whether one change resolves all three.

Recommendation: title the issue by the actual decision, for example `Set a finite JMeter termination rule`. List all affected thread groups under one issue and provide an inline duration/iteration editor that applies to selected or all groups.

### 3. Navigation and issue summaries duplicate the same work

Shared reviews show tabs, a `Resolve import requirements` button row, readiness cards, Conversion reference, and Import issue summary. Bruno repeats the same destinations through readiness buttons and tabs. This creates several ways to reach the same place without clarifying which one is primary.

Recommendation: keep the tabs as the stable navigation. Replace the extra button rows with one compact readiness strip that summarizes unresolved counts and moves focus to the corresponding tab. Keep raw conversion diagnostics inside a collapsed Conversion reference rather than repeating them as primary issues.

### 4. The action hierarchy changes by importer

OpenAPI places editor and save actions at the top of the review. JMeter does the same, but its generated asset inventory immediately becomes the dominant content. Bruno places change-source, readiness actions, API selection, tabs, content, and save guidance in separate blocks. Oracle leaves the entire source form expanded above its review.

Recommendation: standardize the review header as: title/status, primary Edit/Open action, Save action, compact readiness summary, then tabs. Collapse source options after a successful review and expose one `Change source or options` action in the header.

### 5. Tab labels and counts are not fully consistent

Bruno uses `Runtime & source settings` where other importers use `Runtime settings`. It also shows `Review 0`, which reads as an empty review even though the review contains important readiness guidance. Oracle exposes six tabs although Credentials, DSL Preview, and Source Mapping contain only review-only explanations.

Recommendation: keep the six canonical labels and order. Counts should represent unresolved actionable items and zero badges should be hidden. Review-only importers may disable or omit inapplicable tabs, as long as the remaining tabs retain the same names and positions.

### 6. Project scope fails late in HAR

HAR accepts a source file and enables `Inspect & review`, then returns `project_id query parameter or leading multipart field is required for multipart imports` in the unassigned workspace. The operator cannot resolve this from the review surface.

Recommendation: treat Unassigned as a valid project scope if supported, or disable Inspect with an inline explanation and a direct workspace selection action before the upload is submitted.

### 7. The review shell becomes visually unstable at narrower widths

At the captured width, Bruno and Oracle tabs wrap into two rows. The selected tab and related panel content shift vertically between importers, and long labels make scanning harder.

Recommendation: use a single-row horizontally scrollable tab list with visible overflow affordance, or switch to a compact select/list pattern below the breakpoint. Preserve arrow-key behavior and keep the active tab visible.

## Accessibility risks

- The common tab semantics and textual severity labels are strong foundations.
- Detached issue dialogs interrupt spatial context. A persistent issue workspace would reduce focus transitions and make source/fix relationships easier to understand.
- Wrapped tabs can create a visual order that differs from the expected left/right keyboard sequence.
- Raw diagnostic codes and underscored posture values are exposed as primary copy in several places. Human-readable labels should be primary, with codes available as secondary technical detail.
- The HAR error is technically announced as an alert, but it appears only after a preventable submission and does not identify a usable recovery control.
- Screenshot evidence cannot confirm focus return, screen-reader announcements after repairs, contrast ratios, zoom reflow, or full keyboard completion. Those need browser-based accessibility tests.

## Importer-specific guidance

### Shared protocol and sample importers

Adopt the LoadRunner issue workspace inside `ScriptImportReview`. Preserve protocol-specific panels such as auth schemes, selector candidates, Agentforce/Salesforce review, SOAP faults, messaging topology, and support coverage. Convert their diagnostics into specific issue models with a target tab, source location, evidence, and optional repair action.

### JMeter

Keep multi-plan selection, generated scripts, scenario draft, messaging preview, CSV sources, extractors, and termination semantics. Move blocker/warning/unsupported findings into the vertical issue workspace. Add inline fixes for finite duration/iteration rules, timestamp policy, unsupported sampler disposition, and per-script parameterization. Group repeated issues across thread groups.

### Bruno

Keep native-versus-canonical mode, API selection, destination requirements, encrypted source custody, sandbox profile, environments, and source manifest. Normalize the tab label to `Runtime settings`; show source execution profile inside it. Replace the readiness button grid with a compact summary above the tabs, and use the Review tab for grouped acknowledgements/conversion issues. Do not show a zero Review badge.

### Oracle E-Business / Siebel

Keep the review-only posture and package detection. Collapse the source form after review. Use the vertical issue workspace for package posture and correlation candidates. Since the page cannot apply changes, each issue should provide a precise handoff such as `Open LoadRunner importer`, `Open HAR importer`, or `Copy correlation recipe`, with the source evidence beside it. Omit tabs that cannot contain executable content.

### HAR and Recorder

Keep comparison capture, scrubbing, static-asset filtering, Salesforce/Agentforce detection, selectors, and replay evidence. Validate project scope before conversion. Correlation findings should use the issue workspace and offer inline extractor/parameter creation where the draft supports it.

## Prioritized recommendations

1. Replace `IssueDetailsDialog` as the primary review path with a shared issue workspace in `ScriptImportReview`.
2. Define a common issue contract: specific title, severity, affected assets, evidence, source location, target tab, repair action, and occurrence grouping.
3. Standardize review header actions, tab labels/order, count rules, and source-panel collapse behavior.
4. Remove duplicate readiness navigation and keep raw diagnostics in Conversion reference.
5. Add importer-specific repair adapters for JMeter, Bruno, HAR/Recorder, protocol assets, and Oracle review handoffs.
6. Fix HAR project-scope validation before Inspect is enabled.
7. Add keyboard, focus, scroll-retention, and narrow-width regression coverage across all five review architectures.

## Evidence limits

The live run captured the importer catalog plus representative JMeter, OpenAPI, Bruno, HAR, and Oracle flows. OpenAPI represents the shared protocol-asset review architecture; source inspection confirms that Postman, WSDL, AsyncAPI, RAML, DevWeb, k6, CloudTest, Event Hubs Capture, Broker Config, HAR, and Recorder share that component. HAR review generation was blocked by the current unassigned-project multipart validation error, which is recorded as a finding. No source data was saved and no replay was started.

## Implementation status

Implemented in the dashboard after this audit:

- Shared protocol, sample, and JMeter reviews now use a persistent vertical issue queue with grouped occurrences, evidence, filters, keyboard navigation, and direct links to the relevant review tab.
- JMeter runtime settings can apply a finite duration or per-VUser iteration rule to every imported thread group, clearing the matching unbounded-run diagnostic from the active review.
- Duplicate review-navigation buttons were replaced with a compact readiness strip. Informational converter output now lives under `Conversion reference`.
- Review tabs remain on one horizontally scrollable row, hide zero badges, and can omit inapplicable tabs. Bruno now uses the canonical `Runtime settings` label.
- Oracle review results use the shared issue workspace, collapse source inputs after a successful review, and expose only Review, Data & parameters, Runtime settings, and Source Mapping.
- HAR blocks inspection before submission when the workspace has no project scope and explains how to recover.
- Large issue groups page their evidence at 100 occurrences so large imports remain bounded.

Verification included the full 4,469-test dashboard unit suite, production build, live inspection on port 4012, and the affected Playwright workflows under both dev-admin and local-auth profiles.

## Evidence

1. `00-import-catalog.png` — importer discovery catalog.
2. `01-jmeter-review.png` — JMeter shared review shell and generated assets.
3. `02-jmeter-blockers.png` — repeated generic blocker dialog.
4. `03-openapi-review.png` — shared protocol review shell.
5. `04-bruno-review.png` — importer-specific readiness and tab layout.
6. `05-har-project-error.png` — late project-scope failure after source selection.
7. `06-oracle-review.png` — review-only flow with source form retained above tabs.
8. `07-oracle-details.png` — specific findings shown in a detached dialog without repair actions.
