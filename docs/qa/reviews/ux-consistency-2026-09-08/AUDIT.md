# APEX Load — application-wide UX consistency audit

Reviewed 2026-09-08. The application has a recognizable visual identity, but interaction conventions vary by feature. A shared behavior system is the next investment: dialogs, contextual panels, navigation, forms and tables should follow common contracts. A visual reskin alone will leave the learning burden and layout defects in place.

## Scope and evidence

Fresh browser inspection covered every primary navigation area: Dashboard, New Test, Command Center, Results, Scripts, Scenarios, Repository, Monitoring, API Guide, Access and Settings. Twenty-one accepted screenshots cover representative pages, editor states and dialogs. Captures 1–20 use the current 923 × 987 viewport; capture 21 uses 1440 × 1000. All use the current dark theme and development-admin session, against the existing controller. Earlier reporting-audit screenshots were not reused.

This is a cross-application consistency audit, not an exhaustive functional certification of every nested setting, protocol editor, role or integration. Loading captures were rejected; the temporary wide-viewport capture was replaced after rendering stabilized. No tests were launched, no saved resources or permissions were changed, and no destructive actions were submitted. The audit-created calendar prefill was discarded; the original dashboard and normal viewport were restored.

## Prioritized findings

| Priority | Finding | Evidence | Required change |
|---|---|---|---|
| P1 | Dialog containment is unreliable | 8 vs 18 | Render all modal variants through one overlay root above the application shell. Keep title, close, body and footer within the viewport. |
| P1 | Keyboard/focus behavior varies | 7, 8, 18, 20 | One tab keyboard implementation; one dialog focus trap and return-focus policy with a surviving fallback trigger. |
| P1 | Narrow layouts hide navigation or essential context | 5, 9, 10, 15, 16, 18 | Apply explicit responsive rules to tabs, filters, inspectors and calendars. Never hide essential controls beneath another surface. |
| P2 | Collapse/reopen controls require relearning | 1–3, 5–7, 21 | One panel-header control with consistent icon, location, tooltip, expanded state and visible collapsed-panel name. |
| P2 | Dialog visual families diverge | 8, 12, 18; source inventory | One shell with small/form/workbench and confirmation variants; shared title scale, padding, footer alignment, backdrop and motion. |
| P2 | Page action hierarchy and editing contracts differ | 3, 4, 7, 11, 13, 14, 19 | A stable page toolbar, one primary action per task, explicit draft/save states, and consistent destructive/secondary actions. |
| P2 | Navigation hierarchy is visually ambiguous | 4, 5, 7, 9, 13–17, 20 | Define page navigation, in-page tabs, view switches and wizard steps as distinct reusable patterns. Remove implementation-version choices from the normal journey. |
| P2 | Scope and state terminology conflict | 1, 3, 11, 15, 17, 20 | Make effective scope explicit and distinguish queued/reserved/running, unavailable/zero, and execution/acceptance. |
| P2 | Dense cards and repetitive status content bury the task | 1, 4, 7, 10, 14 | Put task content first, use compact summaries, and disclose repair/governance detail on demand. |

## Shared-component evidence

Source searches found 41 component files declaring dialog/alertdialog roles and 36 importing `useModalDialog`. These are separate search counts, not proof that exactly five dialogs lack the hook. The shared hook already provides useful focus trapping and Escape behavior; it should be retained and hardened rather than replaced blindly.

At least three visual shells coexist: `Modal.css` uses a 480px default, 28px padding, 20px title and overlay z-index 200; `parityPanels.css` uses a 520px cap, 16px title, separate padded header and z-index 1000; `ProjectModal.css` uses its own shell, 17px title and z-index 80. Notes adds another size/layout layer. Absolute z-index differences are not by themselves defects, but the observed containment failure demonstrates that the shared layering contract is missing.

`App.css` applies a transform to `.app-layout`; `TestNotesDialog.tsx` renders its overlay in its current component tree. This is a likely contributor to the observed positioning/stacking failure and should be checked when migrating to a portal. The reporting composer already uses a body portal and offers an implementation precedent.

Native `confirm()` calls remain in ScriptsPage, ResultsPage, ApplicationRepositoryPage, TestHub, LibraryFederationPanel and other flows. This source finding was not exercised by starting real deletion operations. Standardize those confirmations without weakening existing authorization or consequences.

Verified source anchors: `Sidebar.tsx:721`, `DashboardPage.tsx:513`, `ScriptEditor.tsx:6097`, `AccessPage.tsx:1704`, `SettingsPage.tsx:3215`, `useModalDialog.ts`, `Modal.css`, `ProjectModal.css`, `parityPanels.css:164`, `TestNotesDialog.tsx:177`, `App.css:7`, `AccessPage.tsx:339`, `RunReportPage.tsx:446`.

## Recommended delivery order

1. Establish shared Dialog and Panel primitives. Fix Test Notes clipping and modal return focus first; migrate creation, editing, confirmation and workbench variants without changing business behavior.
2. Establish Tabs, PageHeader, ActionBar and DataTable patterns. Align keyboard behavior, overflow, row actions, effective scope and the placement of primary actions across libraries.
3. Migrate forms and feedback. Standardize field labels/help/errors, save state, pending operation handling, inline creation and unsaved-change semantics.
4. Apply density and token cleanup after behavior is stable. Reduce nested cards, gratuitous pill shapes, repeated metadata and inconsistent title/spacing scales. Preserve brand assets and semantic colors.
5. Validate the full operator journey in normal and narrow desktop layouts, including nested dialogs, keyboard-only use, unsaved changes, slow/failing requests, read-only roles and all supported themes.

The implementation specification in `UX_CONSISTENCY_SPEC.md` defines the proposed contracts and acceptance checks. No production UI code was modified by this audit.

## Strengths to retain

The persistent workspace selectors, APEX branding, explicit lifecycle badges, existing modal focus helper, searchable settings taxonomy, contextual guidance and revised results hierarchy provide a solid foundation. Different content types may retain appropriate layouts: code editing, documentation, calendars and short forms should not be forced into identical geometry.

## Accessibility and verification limits

Observed behavior: New Project initially focused Name; Escape dismissed it but left page-level focus. Test Notes initially focused its editor and returned focus to its opener on Escape. Run Results arrow navigation changed selected tab and focus; Access Users did not change on ArrowRight. The latter is supported by source inspection showing tab roles without the same arrow-key handler. No full screen-reader journey, measured contrast audit, 200% zoom matrix or WCAG conformance assessment was performed.

The development controller and the frontend may have different build times. The editor's unavailable secret status and current queued/reserved records are environment/data observations, not established backend regressions. Settings and integrations were reviewed representatively; secret-bearing dialogs, new credentials, remote connections and destructive confirmations were not submitted. Light/alternate themes, mobile layouts, live-run manipulation and every nested settings form remain validation work for the implementation phase.

## Captured review steps

### 1. Dashboard — expanded snapshot — Needs change

The scope shell and status text are recognizable. At 923px the snapshot moves ahead of Recent Activity and occupies the first screen. Its Collapse button differs from the global edge chevron. Keep the task list first and make secondary context follow a common responsive-panel rule.

![1. Dashboard — expanded snapshot](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/ux-consistency-2026-09-08/01-dashboard.jpg)

### 2. Dashboard — collapsed snapshot — Mixed

Collapsing exposes Recent Activity and provides a labeled Open strip. This is discoverable, but the operator must learn a different reopen pattern from the editor and Access. The history table still requires horizontal travel to reach actions.

![2. Dashboard — collapsed snapshot](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/ux-consistency-2026-09-08/02-dashboard-collapsed.jpg)

### 3. Settings — controller and runtime — Mixed

Scope and restart guidance are explicit. Browse settings becomes a full-width pill; the disabled No changes action resembles a primary CTA. Dense nested field cards and several save models need a shared form hierarchy. No configuration was changed.

![3. Settings — controller and runtime](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/ux-consistency-2026-09-08/03-settings.jpg)

### 4. Script Library — review queue — Needs change

Existing review issues are visible, but two large repair forms occupy the opening viewport before library search or rows. The library unexpectedly becomes a repair workbench. Use a compact review summary with an explicit open-review action. Technical blocker keys should be translated into operator wording.

![4. Script Library — review queue](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/ux-consistency-2026-09-08/04-script-library.jpg)

### 5. Script Editor — expanded outline — Needs change

Save state and credential uncertainty are explained. Tool controls, navigation tabs, close icons and collapse icons coexist at several levels. At this width panel stacking produces a dense opening screen. Keep the main editing surface primary and move the inspector to an explicit overlay when space is insufficient.

![5. Script Editor — expanded outline](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/ux-consistency-2026-09-08/05-script-editor.jpg)

### 6. Script Editor — collapsed outline — Needs change

The collapsed outline becomes a full-width bar containing only a chevron. It has an accessible Expand outline side panel label, but the visible label no longer tells the operator which panel will return. This differs from Dashboard Open and Access Show Details.

![6. Script Editor — collapsed outline](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/ux-consistency-2026-09-08/06-editor-collapsed.jpg)

### 7. Access — user list and inspector — Needs change

Users have readable lifecycle badges and filters. Nested tabs, a horizontally scrolling table, inline User Access and a separate User Details panel duplicate context. Hide Details uses a different position and vocabulary from other inspectors. ArrowRight on Users remained on Users; results tabs do switch.

![7. Access — user list and inspector](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/ux-consistency-2026-09-08/07-access.jpg)

### 8. New Project dialog — Mixed

Centered compact form, visible focus and an X close control work well. It uses its own title scale and spacing. Escape closed the dialog, but focus returned to the page rather than a live opener because the scope-menu trigger disappeared. Create Project would describe the action better than Save.

![8. New Project dialog](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/ux-consistency-2026-09-08/08-project-dialog.jpg)

### 9. Monitoring — overview — Mixed

Health, scope and advisory limitations are stated. At this width the category row clips the Schedules label beside an advisory chip. Controls need predictable overflow treatment rather than competition with status metadata. Settings links name destinations that may differ from the current settings taxonomy.

![9. Monitoring — overview](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/ux-consistency-2026-09-08/09-monitoring.jpg)

### 10. Monitoring — schedules — Needs change

Timezone/calendar modes and booking entry are available. Calendar arrow buttons, segmented views, filter chips and primary buttons have different shapes and emphasis. Seven narrow day columns truncate continuing-run labels; an agenda view is better for this width. Queued reservations are presented differently from active-run counts elsewhere.

![10. Monitoring — schedules](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/ux-consistency-2026-09-08/10-schedules.jpg)

### 11. New Test — release and scheduling — Mixed

The numbered setup and run-specific override explanation are useful. New Release expands an inline create form, whereas New Project opens a modal. A calendar-prefilled date triggered the unsaved-change guard when leaving even though no fields were manually edited. Preserve that distinction in guard copy or allow unchanged prefill to leave cleanly.

![11. New Test — release and scheduling](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/ux-consistency-2026-09-08/11-run-setup.jpg)

### 12. Unsaved Changes dialog — Mixed

It focuses Keep editing and distinguishes discard from save. Compared with New Project it has no close icon, a larger undivided heading region, different spacing and wrapped action labels. These can be intentional confirmation variants, but should share dimensions, keyboard contracts and footer rules.

![12. Unsaved Changes dialog](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/ux-consistency-2026-09-08/12-unsaved-dialog.jpg)

### 13. Scenario Library — empty state — Mixed

The empty state gives an actionable next step. Create scenario spans the available width while equivalent actions elsewhere are compact or hidden behind mode switches. Library filters, source scope and action placement should be standardized.

![13. Scenario Library — empty state](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/ux-consistency-2026-09-08/13-scenario-library.jpg)

### 14. Scenario Builder — model selection — Mixed

Six named stages make progression understandable. The first screen has three status badges and two navigation levels; Next and Save Scenario coexist before a model is chosen. Clarify draft-save versus progression, keep the progression action in a stable footer, and reduce diagnostic badge noise.

![14. Scenario Builder — model selection](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/ux-consistency-2026-09-08/14-scenario-builder.jpg)

### 15. Application Repository — Needs change

Search and asset categories are useful. A second Domain/Project/Release filter system overrides the impression of the global Unassigned/Legacy scope without making the broader query prominent. The filter row overflows and tab styles differ from the libraries. Distinguish current workspace scope from explicitly broader browsing.

![15. Application Repository](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/ux-consistency-2026-09-08/15-repository.jpg)

### 16. API Guide — Mixed

Content is grouped and the base endpoint is clear. A long horizontal category strip clips later destinations at 923px. The document needs a consistent section navigator with visible overflow affordance; documentation may retain a different reading layout from operational tables.

![16. API Guide](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/ux-consistency-2026-09-08/16-api-guide.jpg)

### 17. Command Center — run picker — Needs change

The picker clearly labels each queued/reserved item. The headline says active runs are in progress although the listed states are queued/reserved. Original and Command Center v2 expose implementation generations as competing product modes. Consolidate the default and use precise waiting/running terminology.

![17. Command Center — run picker](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/ux-consistency-2026-09-08/17-command-center.jpg)

### 18. Command Center — Test Notes dialog — Major issue

At 923px, the global sidebar obscures the dialog title and left notes region while the top bar remains above it. The same is not true of New Project. Close is a text pill instead of an X, and the dialog has a substantially different shell. Escape did restore focus to Test Notes. Fix overlay ownership and sizing before cosmetic changes.

![18. Command Center — Test Notes dialog](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/ux-consistency-2026-09-08/18-notes-dialog.jpg)

### 19. Results Library — Mostly sound

Run identity, status and View results remain visible at this width; secondary row operations are under More. This is a useful baseline for other libraries. Filters and management still consume considerable height, and horizontal scrolling remains for secondary columns.

![19. Results Library](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/ux-consistency-2026-09-08/19-results-library.jpg)

### 20. Run Results — summary — Mostly sound

Four clear sections, explicit metric populations and honest Not assessed/Unknown states establish a useful pattern. Underline tabs differ from page-level segmented navigation; this should become a deliberate hierarchy rule. ArrowRight moved Summary to Performance and moved focus with it.

![20. Run Results — summary](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/ux-consistency-2026-09-08/20-run-results.jpg)

### 21. Settings — wide desktop — Mixed

At 1440px, the searchable settings navigation works as a dedicated column. There is no equivalent visible collapse control in this desktop navigation, while other page panels provide one. Project creation is inline here and modal in the global scope picker. Use the same resource editor/validation contract in both entry points.

![21. Settings — wide desktop](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/ux-consistency-2026-09-08/21-settings-wide.jpg)

