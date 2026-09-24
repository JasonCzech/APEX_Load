# UX consistency review (2026-09-08)

Status: Complete. Audit, spec, implementation, the completion of the original remaining scope, and validation are all recorded below; no open items.

Merged from the original per-topic files of this review. Each section below was one file; the screenshots and evidence next to this README are unchanged.

## APEX Load — application-wide UX consistency audit

Reviewed 2026-09-08. The application has a recognizable visual identity, but interaction conventions vary by feature. A shared behavior system is the next investment: dialogs, contextual panels, navigation, forms and tables should follow common contracts. A visual reskin alone will leave the learning burden and layout defects in place.

### Scope and evidence

Fresh browser inspection covered every primary navigation area: Dashboard, New Test, Command Center, Results, Scripts, Scenarios, Repository, Monitoring, API Guide, Access and Settings. Twenty-one accepted screenshots cover representative pages, editor states and dialogs. Captures 1–20 use the current 923 × 987 viewport; capture 21 uses 1440 × 1000. All use the current dark theme and development-admin session, against the existing controller. Earlier reporting-audit screenshots were not reused.

This is a cross-application consistency audit, not an exhaustive functional certification of every nested setting, protocol editor, role or integration. Loading captures were rejected; the temporary wide-viewport capture was replaced after rendering stabilized. No tests were launched, no saved resources or permissions were changed, and no destructive actions were submitted. The audit-created calendar prefill was discarded; the original dashboard and normal viewport were restored.

### Prioritized findings

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

### Shared-component evidence

Source searches found 41 component files declaring dialog/alertdialog roles and 36 importing `useModalDialog`. These are separate search counts, not proof that exactly five dialogs lack the hook. The shared hook already provides useful focus trapping and Escape behavior; it should be retained and hardened rather than replaced blindly.

At least three visual shells coexist: `Modal.css` uses a 480px default, 28px padding, 20px title and overlay z-index 200; `parityPanels.css` uses a 520px cap, 16px title, separate padded header and z-index 1000; `ProjectModal.css` uses its own shell, 17px title and z-index 80. Notes adds another size/layout layer. Absolute z-index differences are not by themselves defects, but the observed containment failure demonstrates that the shared layering contract is missing.

`App.css` applies a transform to `.app-layout`; `TestNotesDialog.tsx` renders its overlay in its current component tree. This is a likely contributor to the observed positioning/stacking failure and should be checked when migrating to a portal. The reporting composer already uses a body portal and offers an implementation precedent.

Native `confirm()` calls remain in ScriptsPage, ResultsPage, ApplicationRepositoryPage, TestHub, LibraryFederationPanel and other flows. This source finding was not exercised by starting real deletion operations. Standardize those confirmations without weakening existing authorization or consequences.

Verified source anchors: `Sidebar.tsx:721`, `DashboardPage.tsx:513`, `ScriptEditor.tsx:6097`, `AccessPage.tsx:1704`, `SettingsPage.tsx:3215`, `useModalDialog.ts`, `Modal.css`, `ProjectModal.css`, `parityPanels.css:164`, `TestNotesDialog.tsx:177`, `App.css:7`, `AccessPage.tsx:339`, `RunReportPage.tsx:446`.

### Recommended delivery order

1. Establish shared Dialog and Panel primitives. Fix Test Notes clipping and modal return focus first; migrate creation, editing, confirmation and workbench variants without changing business behavior.
2. Establish Tabs, PageHeader, ActionBar and DataTable patterns. Align keyboard behavior, overflow, row actions, effective scope and the placement of primary actions across libraries.
3. Migrate forms and feedback. Standardize field labels/help/errors, save state, pending operation handling, inline creation and unsaved-change semantics.
4. Apply density and token cleanup after behavior is stable. Reduce nested cards, gratuitous pill shapes, repeated metadata and inconsistent title/spacing scales. Preserve brand assets and semantic colors.
5. Validate the full operator journey in normal and narrow desktop layouts, including nested dialogs, keyboard-only use, unsaved changes, slow/failing requests, read-only roles and all supported themes.

The implementation specification in `UX_CONSISTENCY_SPEC.md` defines the proposed contracts and acceptance checks. No production UI code was modified by this audit.

### Strengths to retain

The persistent workspace selectors, APEX branding, explicit lifecycle badges, existing modal focus helper, searchable settings taxonomy, contextual guidance and revised results hierarchy provide a solid foundation. Different content types may retain appropriate layouts: code editing, documentation, calendars and short forms should not be forced into identical geometry.

### Accessibility and verification limits

Observed behavior: New Project initially focused Name; Escape dismissed it but left page-level focus. Test Notes initially focused its editor and returned focus to its opener on Escape. Run Results arrow navigation changed selected tab and focus; Access Users did not change on ArrowRight. The latter is supported by source inspection showing tab roles without the same arrow-key handler. No full screen-reader journey, measured contrast audit, 200% zoom matrix or WCAG conformance assessment was performed.

The development controller and the frontend may have different build times. The editor's unavailable secret status and current queued/reserved records are environment/data observations, not established backend regressions. Settings and integrations were reviewed representatively; secret-bearing dialogs, new credentials, remote connections and destructive confirmations were not submitted. Light/alternate themes, mobile layouts, live-run manipulation and every nested settings form remain validation work for the implementation phase.

### Captured review steps

#### 1. Dashboard — expanded snapshot — Needs change

The scope shell and status text are recognizable. At 923px the snapshot moves ahead of Recent Activity and occupies the first screen. Its Collapse button differs from the global edge chevron. Keep the task list first and make secondary context follow a common responsive-panel rule.

![1. Dashboard — expanded snapshot](01-dashboard.jpg)

#### 2. Dashboard — collapsed snapshot — Mixed

Collapsing exposes Recent Activity and provides a labeled Open strip. This is discoverable, but the operator must learn a different reopen pattern from the editor and Access. The history table still requires horizontal travel to reach actions.

![2. Dashboard — collapsed snapshot](02-dashboard-collapsed.jpg)

#### 3. Settings — controller and runtime — Mixed

Scope and restart guidance are explicit. Browse settings becomes a full-width pill; the disabled No changes action resembles a primary CTA. Dense nested field cards and several save models need a shared form hierarchy. No configuration was changed.

![3. Settings — controller and runtime](03-settings.jpg)

#### 4. Script Library — review queue — Needs change

Existing review issues are visible, but two large repair forms occupy the opening viewport before library search or rows. The library unexpectedly becomes a repair workbench. Use a compact review summary with an explicit open-review action. Technical blocker keys should be translated into operator wording.

![4. Script Library — review queue](04-script-library.jpg)

#### 5. Script Editor — expanded outline — Needs change

Save state and credential uncertainty are explained. Tool controls, navigation tabs, close icons and collapse icons coexist at several levels. At this width panel stacking produces a dense opening screen. Keep the main editing surface primary and move the inspector to an explicit overlay when space is insufficient.

![5. Script Editor — expanded outline](05-script-editor.jpg)

#### 6. Script Editor — collapsed outline — Needs change

The collapsed outline becomes a full-width bar containing only a chevron. It has an accessible Expand outline side panel label, but the visible label no longer tells the operator which panel will return. This differs from Dashboard Open and Access Show Details.

![6. Script Editor — collapsed outline](06-editor-collapsed.jpg)

#### 7. Access — user list and inspector — Needs change

Users have readable lifecycle badges and filters. Nested tabs, a horizontally scrolling table, inline User Access and a separate User Details panel duplicate context. Hide Details uses a different position and vocabulary from other inspectors. ArrowRight on Users remained on Users; results tabs do switch.

![7. Access — user list and inspector](07-access.jpg)

#### 8. New Project dialog — Mixed

Centered compact form, visible focus and an X close control work well. It uses its own title scale and spacing. Escape closed the dialog, but focus returned to the page rather than a live opener because the scope-menu trigger disappeared. Create Project would describe the action better than Save.

![8. New Project dialog](08-project-dialog.jpg)

#### 9. Monitoring — overview — Mixed

Health, scope and advisory limitations are stated. At this width the category row clips the Schedules label beside an advisory chip. Controls need predictable overflow treatment rather than competition with status metadata. Settings links name destinations that may differ from the current settings taxonomy.

![9. Monitoring — overview](09-monitoring.jpg)

#### 10. Monitoring — schedules — Needs change

Timezone/calendar modes and booking entry are available. Calendar arrow buttons, segmented views, filter chips and primary buttons have different shapes and emphasis. Seven narrow day columns truncate continuing-run labels; an agenda view is better for this width. Queued reservations are presented differently from active-run counts elsewhere.

![10. Monitoring — schedules](10-schedules.jpg)

#### 11. New Test — release and scheduling — Mixed

The numbered setup and run-specific override explanation are useful. New Release expands an inline create form, whereas New Project opens a modal. A calendar-prefilled date triggered the unsaved-change guard when leaving even though no fields were manually edited. Preserve that distinction in guard copy or allow unchanged prefill to leave cleanly.

![11. New Test — release and scheduling](11-run-setup.jpg)

#### 12. Unsaved Changes dialog — Mixed

It focuses Keep editing and distinguishes discard from save. Compared with New Project it has no close icon, a larger undivided heading region, different spacing and wrapped action labels. These can be intentional confirmation variants, but should share dimensions, keyboard contracts and footer rules.

![12. Unsaved Changes dialog](12-unsaved-dialog.jpg)

#### 13. Scenario Library — empty state — Mixed

The empty state gives an actionable next step. Create scenario spans the available width while equivalent actions elsewhere are compact or hidden behind mode switches. Library filters, source scope and action placement should be standardized.

![13. Scenario Library — empty state](13-scenario-library.jpg)

#### 14. Scenario Builder — model selection — Mixed

Six named stages make progression understandable. The first screen has three status badges and two navigation levels; Next and Save Scenario coexist before a model is chosen. Clarify draft-save versus progression, keep the progression action in a stable footer, and reduce diagnostic badge noise.

![14. Scenario Builder — model selection](14-scenario-builder.jpg)

#### 15. Application Repository — Needs change

Search and asset categories are useful. A second Domain/Project/Release filter system overrides the impression of the global Unassigned/Legacy scope without making the broader query prominent. The filter row overflows and tab styles differ from the libraries. Distinguish current workspace scope from explicitly broader browsing.

![15. Application Repository](15-repository.jpg)

#### 16. API Guide — Mixed

Content is grouped and the base endpoint is clear. A long horizontal category strip clips later destinations at 923px. The document needs a consistent section navigator with visible overflow affordance; documentation may retain a different reading layout from operational tables.

![16. API Guide](16-api-guide.jpg)

#### 17. Command Center — run picker — Needs change

The picker clearly labels each queued/reserved item. The headline says active runs are in progress although the listed states are queued/reserved. Original and Command Center v2 expose implementation generations as competing product modes. Consolidate the default and use precise waiting/running terminology.

![17. Command Center — run picker](17-command-center.jpg)

#### 18. Command Center — Test Notes dialog — Major issue

At 923px, the global sidebar obscures the dialog title and left notes region while the top bar remains above it. The same is not true of New Project. Close is a text pill instead of an X, and the dialog has a substantially different shell. Escape did restore focus to Test Notes. Fix overlay ownership and sizing before cosmetic changes.

![18. Command Center — Test Notes dialog](18-notes-dialog.jpg)

#### 19. Results Library — Mostly sound

Run identity, status and View results remain visible at this width; secondary row operations are under More. This is a useful baseline for other libraries. Filters and management still consume considerable height, and horizontal scrolling remains for secondary columns.

![19. Results Library](19-results-library.jpg)

#### 20. Run Results — summary — Mostly sound

Four clear sections, explicit metric populations and honest Not assessed/Unknown states establish a useful pattern. Underline tabs differ from page-level segmented navigation; this should become a deliberate hierarchy rule. ArrowRight moved Summary to Performance and moved focus with it.

![20. Run Results — summary](20-run-results.jpg)

#### 21. Settings — wide desktop — Mixed

At 1440px, the searchable settings navigation works as a dedicated column. There is no equivalent visible collapse control in this desktop navigation, while other page panels provide one. Project creation is inline here and modal in the global scope picker. Use the same resource editor/validation contract in both entry points.

![21. Settings — wide desktop](21-settings-wide.jpg)

## Proposed APEX Load UX consistency contract

Status: implemented; see [implementation mapping and intentional variations](#ux-consistency-implementation). Preserve existing brand assets, theme support, authorization, data integrity and workflow capabilities. The names below describe proposed shared components, not claims that those components already exist.

### 1. Application hierarchy

Use a stable page header: page title and effective scope at the left, one contextual primary action at the right. Keep deployment/build identity secondary. Do not allow status chips to cover navigation or actions. Use a compact overflow menu for secondary actions.

Define four distinct navigation types and reuse them consistently:

| Type | Use | Contract |
|---|---|---|
| Global navigation | Move between product areas | Stable sidebar, current-page state, consistent named collapse control |
| Page navigation | Change subpages such as monitoring categories | One shared component; clear selected state; explicit overflow; preserve URL and back behavior |
| Content tabs | Change views within an object | Shared tab semantics and arrow/Home/End navigation; retain object identity; associated tab panels |
| Wizard steps | Progress through authoring | Numbered steps, validation state, stable Back/Next footer; draft saving is visibly secondary |

Document-editor tabs may additionally support closing documents; their close action must be visually and semantically distinct from closing a panel or modal. Retire Original/v2 choices from routine operator navigation once the supported command-center experience is selected.

### 2. Dialog system

Create a shared `Dialog` shell with compact, form and workbench sizes, plus a confirmation variant. Suggested starting widths are 420, 640 and 1040px, always capped by available viewport width with safe gutters. Calibrate against current theme typography rather than enlarging every form.

All variants share a portal outside transformed/clipped app containers, one layering scale, backdrop token, header/title/description spacing, a labeled top-right close control where dismissal is allowed, scrollable body and stable footer. Long workbenches may become full-screen at narrow widths. Do not allow a dialog to sit beneath the sidebar or sticky page header.

Use action-specific verbs such as Create project, Save changes and Delete user. Put the primary confirmation last; retain an explicit safe dismissal. Unsaved-change prompts may have Keep editing / Discard changes / Save changes, but should follow the same button-height and wrapping rules. Do not add browser-native confirm prompts inside a custom dialog.

Behavior: deliberate initial focus, contained Tab/Shift+Tab, Escape for the topmost dismissible dialog, guarded dismissal for dirty forms, and return focus to a surviving opener or a named fallback such as the scope selector. Do not re-enable a mutating action while its request is in flight. Failed saves retain edits and show the failure in context. Preserve existing special handling for uncertain outcomes and long saves.

### 3. Context panels

Create a shared `ContextPanel`/`PanelToggle` contract for inspectors, outlines and snapshots. Put a directional collapse icon in the panel header next to its title. Use accessible labels such as Collapse user details and Expand script outline, with `aria-expanded` and `aria-controls`.

When collapsed, preserve a visible panel name in a compact rail or named trigger. Never replace it with an unexplained full-width chevron. Keep the trigger in a predictable location and move focus to it when the focused region is removed. Preserve selected object, filters and unsaved data when merely hiding a panel.

At narrow widths, context follows the task or opens as an overlay; it must not push the primary task entirely below the first screen. Global navigation and contextual panels can use different geometry, but the icon language, labels, focus and reopen behavior must agree. Decide which panel preferences persist per workspace and apply that rule consistently.

### 4. Libraries and tables

Use one `LibraryToolbar` and table family for Scripts, Scenarios, Results and Repository. Put search first, essential filters next, optional filters in a disclosure, then primary creation/action and pagination in consistent locations. Clearly label queries that deliberately span beyond the global project/release scope.

Keep item name/reference, lifecycle/health and the main row action visible at narrow widths. Place transfer, duplicate, archive and delete in a consistent More menu. Selection exposes a contextual bulk-action bar. Horizontal scrolling is acceptable for secondary metrics, not as the only way to discover the main action.

Use consistent pagination defaults and control order unless dataset size justifies an explicit exception. Distinguish loading, no items, no filter matches, unavailable data and load failure. A loading request must not flash a convincing empty-library message.

For calendars, offer an agenda presentation when seven readable day columns do not fit. Keep run selection and scheduling targets keyboard reachable and distinguish nested actions without nested interactive buttons.

### 5. Forms and editing

Use shared `FormField`, help, validation and footer patterns. Labels stay above their controls; units, scope and restart requirements are concise helper text. Errors appear beside the field and in a linked summary for long forms. Preserve entered values after failure.

Make the save model explicit: immediate personal preference, staged settings, or resource editor. Do not make operators infer that from the page. A non-actionable Saved state should look like status, not the primary action. Use the same creation form and validation across global pickers, settings and inline detours; choose inline versus dialog presentation deliberately based on complexity.

Distinguish navigation prefills from user edits when deciding whether to prompt about unsaved changes. Keep effective scope visible before a create, save or launch. Save draft must remain distinguishable from Next, approval and execution.

### 6. Feedback, wording and visual tokens

Use one status vocabulary: queued, reserved, running, paused, completed, failed and aborted describe execution; acceptance and evidence completeness are separate. Do not call queued/reserved runs in progress without qualification. Unavailable is not zero; not assessed is not passed.

Use a restrained spacing scale (proposed 4/8/12/16/24/32px), shared field/button heights, a small title/body/caption type scale and existing semantic colors. Prefer a single surface around related fields over nested cards for every row. Reserve pill shapes for compact states and view switches rather than every link, control and heading. Keep disabled states visually distinct from available primary actions.

Keep status messages close to the affected task, include a useful recovery action, and use live-region announcements for asynchronous results. Avoid persistent generic banners competing with the primary content. Translate internal keys and implementation-generation names into operator language.

### Migration sequence and verification

| Batch | Scope | Acceptance gate |
|---|---|---|
| 1 | Dialog shell, Test Notes, focus handling, confirmations | No clipping beneath app chrome at 923px and wide desktop; title/close/footer reachable; nested Escape and focus return tested; existing save guards preserved |
| 2 | Panel controls and responsive inspectors | Consistent collapse/reopen labels and placement; visible collapsed name; focus preserved; editor/task content remains reachable |
| 3 | Tabs, headers, library actions and scope | Arrow/Home/End parity; selected tab scrolled into view; browser back works; names and primary actions remain visible; broad scope explicit |
| 4 | Forms, save state and feedback | Dirty, clean, loading, failed and uncertain save states tested; common validation; disabled/read-only roles preserved |
| 5 | Density, typography and theme polish | Compare every primary area at narrow/wide widths and supported themes; no unexplained nested surfaces or truncated controls |

Add behavior tests to shared primitives and representative migration tests to each affected workflow. Test project creation, unsaved navigation, Test Notes, script editing, scenario creation, run setup, results retrieval, access editing and settings navigation together. For implementation, follow repository policy: the full dashboard `npm run test:cycle`, meaningful browser verification, and `go test ./...` if backend code changes. A CSS-only source check cannot establish that a popup or collapsed panel works.

Do not migrate business logic into the visual primitives. Preserve API contracts, role checks, concurrency guards and data semantics. Document exceptions for code editors, report documents and large calendars so consistency remains purposeful.

## UX consistency implementation

The initial implementation below is supplemented by [completion of the remaining original work](#completion-of-the-original-ux-consistency-scope), including shared libraries, page structure, form feedback, the Command Center default, and the full theme matrix.

Implemented against the [audit](#apex-load--application-wide-ux-consistency-audit) and [interaction contract](#proposed-apex-load-ux-consistency-contract). The existing APEX palette, brand assets, authorization rules, and workflow-specific editors remain the foundation.

### Completed work

| Audit concern | Implementation | Verification |
|---|---|---|
| Modal containment and visual families | `DialogLayer` moves 60 overlay instances in 39 component files outside transformed/clipped containers. Shared tokens govern backdrop, viewport gutters, title size, corners, control height and wrapping footers. Compact forms and larger workbenches retain appropriate widths. | Real portal/Strict Mode unit coverage; Notes and project screenshots at 923px and 1440px; browser journeys through imports, run controls, editors and settings. |
| Keyboard and focus parity | Shared dialog helper isolates background branches with `inert`, locks background scrolling, traps keyboard/programmatic focus, restores prior state, and returns focus to a surviving scope trigger. Tab groups use common Arrow/Home/End handling and roving tab stops; existing specialized handlers are respected. | Nested Notes discard browser workflow; scope-picker fallback checked manually; Access arrow navigation checked manually; hook and tab tests. |
| Native confirmations | Shared asynchronous confirmations replace browser-native prompts in component workflows. Cancel receives initial focus. Concurrent prompts are rejected; navigation cancels stale requests. Caller save, permissions and mutation guards remain in place. | Cancellation, acceptance, navigation and duplicate-request tests; nested discard browser workflow; existing deletion and access tests. Browser before-unload protection remains browser-owned. |
| Collapse/reopen inconsistency | Shared `PanelToggle` for dashboard snapshot, script outline/details, Access inspectors, calendar run details and Settings navigation. Every trigger exposes a name, expanded state and controlled region; collapsed panels retain visible names. | Panel tests plus existing component tests; browser focus checks and narrow/wide screenshots. Settings keeps navigation open when a dirty navigation is canceled. |
| Narrow navigation and filters | Page tabs wrap within their available space; Monitoring advisories occupy their own row. Repository filter columns wrap and its effective domain/catalog scope is explicit. | Matched Monitoring, API Guide and Repository before/after captures. |
| Calendar readability | Narrow month/week calendars become a vertical date list with full weekday/date labels. Scheduling is a dedicated button, separate from event selection. Previous/Next and day actions use theme controls. | Calendar component tests; responsive screenshot; workflow scheduling tests. |
| Libraries and density | Authoring review is a compact disclosure. Script rows keep Edit visible and move secondary actions into More. Narrow script tables prioritize identity/health/actions with an explicit control to reveal usage and dates. Repository primary row actions remain visible. Dashboard activity precedes supporting snapshot context at narrow widths. | Script table disclosure test; matched library/dashboard screenshots; library workflow coverage. |
| Loading and recovery | Script loading and failed loading have distinct messages, with no false empty-library creation prompt. Explicit library Refresh refreshes both the federation catalog and the visible local model. | Failure/retry component test and federation refresh test. |
| Forms and save model | Shared name-field layout in scope dialogs, Settings inline creation and the New Test release detour. Creation dialogs use Create project/application/release; clean Settings displays a status instead of a disabled primary action. Calendar date prefills do not count as edits; resetting the draft resets its date baseline. | Project failure/in-flight/Strict Mode tests; prefill navigation regression test; saved/dirty/error workflow coverage. |
| Visual hierarchy and wording | Shared rectangular button geometry, restrained tab corners, theme-aware disabled states, fewer nested field surfaces. Command Center presents Overview/Analysis instead of implementation versions, and accurately describes runs as running or waiting. | Dark/light checks, matched Command Center and Settings captures; automated accessibility checks in both themes. |

### Intentional variations

- Code documents keep closable document tabs; wizards retain numbered steps and their guarded Back/Next flow. These serve different tasks from page navigation.
- The full-screen report composer already renders outside app containers and retains document/canvas geometry. Small anchored informational popovers are not full modal dialogs.
- Settings keeps inline creation when it is part of domain/project configuration; global selectors use compact creation dialogs. They share field styling while retaining each workflow's pinned scope and validation.
- Access uses server-side pagination with its existing 50-row default; ordinary asset libraries retain 10-row pages. The control order remains consistent.
- Panel visibility is temporary UI state. Existing explicit editor/global layout preferences retain their persistence semantics; hiding a panel does not reset its data.
- Overview and Analysis remain available as operator views. This migration removes visible implementation-version naming without removing supported controls.
- Secondary table columns remain available through expansion, the explicit column control, or horizontal scrolling. Mobile phone use and exhaustive screen-reader certification are not claimed.

### Review fixes

The implementation review fixed duplicate tab activation, a repeated-delete race after asynchronous confirmation, hidden-opener focus restoration, navigation collapsing after Keep editing, compressed narrow script names, Monitoring tabs clipped beside advisories, fixed-width Repository filters, and false empty-library messaging after a failed load. The queued-run recovery test now waits for its enabled action instead of assuming that a debounce has finished.

### Evidence

The `after-*.jpg` captures in this directory record rendered verification. Matching reference/implementation images were opened together for Notes, project creation, dashboard, script library/editor, Monitoring, schedules, Repository, API Guide, Command Center and Settings. See the application-wide section in [`design-qa.md`](../ux-reviews-2026-09.md#execution-results-design-qa) for comparison findings and verification limits.

## Completion of the original UX consistency scope

This record supersedes the earlier broad completion claim. The first implementation addressed major containment, focus, responsive and visual defects; the following structural items were still partial and are completed in this follow-up.

| Original remaining item | Implementation | Review evidence |
|---|---|---|
| Library controls and action placement | `LibraryToolbar` provides search-first placement, explicit Source, collapsible optional filters with an active-state indicator, and compact creation/refresh actions. Scripts, Scenarios, Results and Repository use it. `BulkActions` handles selected script/scenario/result actions; `RowActions` shares More behavior in scripts, scenarios and results. | Narrow before/after script and scenario captures; disclosure/state tests; theme/viewport browser matrix. |
| Scenario progression | The persistent footer separates Save draft from Next and places progression last. The final review step removes the disabled Next action. Save copy states that saving does not launch a run. Readiness details are disclosed instead of three competing badges. | Matched builder screenshots; existing author/save/run workflows use the renamed operation. |
| Form and validation consistency | `FormField` connects labels, hints and errors to controls while preserving existing descriptions. User profile/onboarding email and display-name fields and scenario review naming join the creation forms. `FormErrorSummary` supports actionable issue lists, taking scenario operators to the relevant step, and shared run-setup failure presentation. | Error association/removal and issue-navigation unit tests, Access tests and scenario/run workflows. |
| Shared navigation and headers | `PageHeader` owns the application header for every route. Existing route contributions retain their actions and effective context. `TabList` consolidates content/page tab semantics and keyboard behavior across existing div-based tab groups; specialized document tabs, view switches and wizard steps retain their distinct roles. | Shared structure tests; existing keyboard and route workflows; page-title checks across all themes and widths. |
| Command Center default | Operations is the single default entry. Open analysis is an explicit tool, with Back to operations returning to the selected run and operational view. All run controls and graph-wall capabilities remain available. | Default/analysis/return component regression test and narrow screenshot comparison. |
| Full theme/width coverage | A new browser matrix visits all 11 primary areas at 923px and 1440px in Dark, Light, Solarized Dark, Solarized Light and Monokai Dimmed. It verifies the shared heading, applied theme and library disclosure controls and saves screenshots for visual review. | Passed: all 10 matrix cases, 110 final screenshots. |

### Purposeful domain differences

- The existing shared table pagination manager already gives ordinary libraries the same 10-row default and Rows/Previous/Next order. Access retains server-side pagination; Repository's bounded virtualized large-catalog path retains virtualization rather than creating a second paging layer.
- Results uses the shared selection container with its existing comparison/management actions and shows guidance before selection. Repository retains asset-specific actions; no bulk mutation API is invented where the product does not support one.
- Creation is offered for scripts and scenarios. Results are produced by running/importing tests, and Repository aggregates existing domain assets.
- Saving a scenario still enforces existing required-data and integrity rules. The label change does not introduce incomplete server-side records or bypass validation.
- Analysis is a tool within Command Center, not a replacement load-control implementation. This preserves supported functionality while removing the competing default/version selection.

### Review/fix cycle

1. Corrected an undefined border token caught by the theme-token unit test.
2. Compared initial and revised library screenshots: reduced excess toolbar spacing and removed disabled empty-selection bulk actions, bringing table content back into view.
3. Added a visible Active marker for applied filters when the filter disclosure is closed.
4. Preserved keyboard event ownership so specialized handlers cannot activate a tab twice.
5. Fixed a false onboarding dirty state after workspace catalog hydration by comparing with the actual initial preset, updating that baseline only on reset/successful save. A hydration regression test and manual untouched-form navigation verify the fix.
6. Removed empty-selection Results actions after the cross-theme review, matching script/scenario behavior while retaining comparison guidance.
7. Tightened screenshot readiness to assert route-specific headings, resolved loading surfaces and Command Center content, and disabled screenshot animations. One early Light Command Center capture was taken during transition and is replaced by the final capture.

### Final validation

Passed `cd dashboard && npm run test:cycle` after all production changes and review fixes:

- Type checking, zero-warning lint, and fresh embedded build passed.
- **3,953 unit tests across 360 files passed.**
- **76 dev-admin browser workflows passed**, including all 10 theme/width matrix cases.
- **8 local-auth browser workflows passed.**
- `git diff --check` passed.

All 110 final matrix screenshots are retained in `theme-matrix/`, with review sheets for each theme/width. The populated-workspace before/after comparisons and final Results capture are recorded in [`design-qa.md`](../ux-reviews-2026-09.md#execution-results-design-qa). The final Light Command Center screenshot was reopened to confirm the transition-state capture was replaced; final Solarized Light and Monokai Results captures were inspected at full size after the selection-bar correction.

The preview is restored to Dashboard in Dark theme at normal viewport size. Temporary onboarding input and result selection were cleared; no existing users or real runs were changed. The existing controller was not restarted. No original-scope work items remain open in this completion record; domain-specific exceptions are documented above.

## UX consistency validation

Completed September 8, 2026, after all implementation and review fixes.

### Required test cycle

Command: `cd dashboard && npm run test:cycle`

- TypeScript type checking: passed.
- ESLint with zero warnings allowed: passed.
- Unit tests: **3,946 passed across 359 files**.
- Fresh embedded dashboard build: passed.
- Dev-admin Playwright workflows: **66 passed**.
- Local-auth Playwright workflows: **8 passed**.
- `git diff --check`: passed.

The final cycle exited successfully. Browser workflows used disposable controllers and throwaway databases. This UX implementation did not change Go sources; existing unrelated Go changes were preserved.

### Behavior and visual review

Compared original and updated screenshots together at 923 × 987, with representative wide views at 1440 × 1000. Reviewed dark and light themes, modal placement above application chrome, nested cancel/discard, focus containment and return, panel controls, keyboard tabs, narrow libraries, monitoring schedules, settings navigation, and creation forms. Screenshots and the completion map are linked in [implementation section](#ux-consistency-implementation); detailed comparisons are recorded in the repository's `design-qa.md`.

Review fixes include duplicate keyboard activation, repeated-delete protection, canceled settings navigation preserving the current layout, monitoring panel expansion, clipped navigation/filter layouts, and library refresh retrying failed local loads without presenting them as empty libraries. Regression coverage passed in the final cycle.

The browser preview was returned to Dashboard with the original dark theme and normal viewport. No real run was started or altered, and the temporary review note was discarded without saving. The existing controller was not restarted.

### Limits

This review does not certify exhaustive screen-reader coverage, every alternate theme, or phone-sized workflows. Intentional differences for code tabs, wizard steps, report composition, and anchored informational popovers are documented in the implementation record.


### Follow-up: remaining original scope completed

The final follow-up cycle passed after shared library/header/tab/form migration, scenario progression, Command Center default consolidation, and the review fixes:

- 3,953 unit tests in 360 files.
- 76 dev-admin workflows (including 10 theme/width matrix cases).
- 8 local-auth workflows.
- Type checking, zero-warning lint, fresh embedded build and `git diff --check`.

The matrix covers 11 primary routes, all five concrete supported themes, and 923px/1440px widths; 110 screenshots are retained in `theme-matrix/`. See [the completion record](#completion-of-the-original-ux-consistency-scope) for the exact scope, review fixes and purposeful variations. The earlier representative-theme limitation is superseded for these primary-route states; exhaustive screen-reader and phone-width certification remain outside this review.
