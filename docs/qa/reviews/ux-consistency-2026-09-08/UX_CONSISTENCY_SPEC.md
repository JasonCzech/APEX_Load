# Proposed APEX Load UX consistency contract

Status: implemented; see [implementation mapping and intentional variations](IMPLEMENTATION.md). Preserve existing brand assets, theme support, authorization, data integrity and workflow capabilities. The names below describe proposed shared components, not claims that those components already exist.

## 1. Application hierarchy

Use a stable page header: page title and effective scope at the left, one contextual primary action at the right. Keep deployment/build identity secondary. Do not allow status chips to cover navigation or actions. Use a compact overflow menu for secondary actions.

Define four distinct navigation types and reuse them consistently:

| Type | Use | Contract |
|---|---|---|
| Global navigation | Move between product areas | Stable sidebar, current-page state, consistent named collapse control |
| Page navigation | Change subpages such as monitoring categories | One shared component; clear selected state; explicit overflow; preserve URL and back behavior |
| Content tabs | Change views within an object | Shared tab semantics and arrow/Home/End navigation; retain object identity; associated tab panels |
| Wizard steps | Progress through authoring | Numbered steps, validation state, stable Back/Next footer; draft saving is visibly secondary |

Document-editor tabs may additionally support closing documents; their close action must be visually and semantically distinct from closing a panel or modal. Retire Original/v2 choices from routine operator navigation once the supported command-center experience is selected.

## 2. Dialog system

Create a shared `Dialog` shell with compact, form and workbench sizes, plus a confirmation variant. Suggested starting widths are 420, 640 and 1040px, always capped by available viewport width with safe gutters. Calibrate against current theme typography rather than enlarging every form.

All variants share a portal outside transformed/clipped app containers, one layering scale, backdrop token, header/title/description spacing, a labeled top-right close control where dismissal is allowed, scrollable body and stable footer. Long workbenches may become full-screen at narrow widths. Do not allow a dialog to sit beneath the sidebar or sticky page header.

Use action-specific verbs such as Create project, Save changes and Delete user. Put the primary confirmation last; retain an explicit safe dismissal. Unsaved-change prompts may have Keep editing / Discard changes / Save changes, but should follow the same button-height and wrapping rules. Do not add browser-native confirm prompts inside a custom dialog.

Behavior: deliberate initial focus, contained Tab/Shift+Tab, Escape for the topmost dismissible dialog, guarded dismissal for dirty forms, and return focus to a surviving opener or a named fallback such as the scope selector. Do not re-enable a mutating action while its request is in flight. Failed saves retain edits and show the failure in context. Preserve existing special handling for uncertain outcomes and long saves.

## 3. Context panels

Create a shared `ContextPanel`/`PanelToggle` contract for inspectors, outlines and snapshots. Put a directional collapse icon in the panel header next to its title. Use accessible labels such as Collapse user details and Expand script outline, with `aria-expanded` and `aria-controls`.

When collapsed, preserve a visible panel name in a compact rail or named trigger. Never replace it with an unexplained full-width chevron. Keep the trigger in a predictable location and move focus to it when the focused region is removed. Preserve selected object, filters and unsaved data when merely hiding a panel.

At narrow widths, context follows the task or opens as an overlay; it must not push the primary task entirely below the first screen. Global navigation and contextual panels can use different geometry, but the icon language, labels, focus and reopen behavior must agree. Decide which panel preferences persist per workspace and apply that rule consistently.

## 4. Libraries and tables

Use one `LibraryToolbar` and table family for Scripts, Scenarios, Results and Repository. Put search first, essential filters next, optional filters in a disclosure, then primary creation/action and pagination in consistent locations. Clearly label queries that deliberately span beyond the global project/release scope.

Keep item name/reference, lifecycle/health and the main row action visible at narrow widths. Place transfer, duplicate, archive and delete in a consistent More menu. Selection exposes a contextual bulk-action bar. Horizontal scrolling is acceptable for secondary metrics, not as the only way to discover the main action.

Use consistent pagination defaults and control order unless dataset size justifies an explicit exception. Distinguish loading, no items, no filter matches, unavailable data and load failure. A loading request must not flash a convincing empty-library message.

For calendars, offer an agenda presentation when seven readable day columns do not fit. Keep run selection and scheduling targets keyboard reachable and distinguish nested actions without nested interactive buttons.

## 5. Forms and editing

Use shared `FormField`, help, validation and footer patterns. Labels stay above their controls; units, scope and restart requirements are concise helper text. Errors appear beside the field and in a linked summary for long forms. Preserve entered values after failure.

Make the save model explicit: immediate personal preference, staged settings, or resource editor. Do not make operators infer that from the page. A non-actionable Saved state should look like status, not the primary action. Use the same creation form and validation across global pickers, settings and inline detours; choose inline versus dialog presentation deliberately based on complexity.

Distinguish navigation prefills from user edits when deciding whether to prompt about unsaved changes. Keep effective scope visible before a create, save or launch. Save draft must remain distinguishable from Next, approval and execution.

## 6. Feedback, wording and visual tokens

Use one status vocabulary: queued, reserved, running, paused, completed, failed and aborted describe execution; acceptance and evidence completeness are separate. Do not call queued/reserved runs in progress without qualification. Unavailable is not zero; not assessed is not passed.

Use a restrained spacing scale (proposed 4/8/12/16/24/32px), shared field/button heights, a small title/body/caption type scale and existing semantic colors. Prefer a single surface around related fields over nested cards for every row. Reserve pill shapes for compact states and view switches rather than every link, control and heading. Keep disabled states visually distinct from available primary actions.

Keep status messages close to the affected task, include a useful recovery action, and use live-region announcements for asynchronous results. Avoid persistent generic banners competing with the primary content. Translate internal keys and implementation-generation names into operator language.

## Migration sequence and verification

| Batch | Scope | Acceptance gate |
|---|---|---|
| 1 | Dialog shell, Test Notes, focus handling, confirmations | No clipping beneath app chrome at 923px and wide desktop; title/close/footer reachable; nested Escape and focus return tested; existing save guards preserved |
| 2 | Panel controls and responsive inspectors | Consistent collapse/reopen labels and placement; visible collapsed name; focus preserved; editor/task content remains reachable |
| 3 | Tabs, headers, library actions and scope | Arrow/Home/End parity; selected tab scrolled into view; browser back works; names and primary actions remain visible; broad scope explicit |
| 4 | Forms, save state and feedback | Dirty, clean, loading, failed and uncertain save states tested; common validation; disabled/read-only roles preserved |
| 5 | Density, typography and theme polish | Compare every primary area at narrow/wide widths and supported themes; no unexplained nested surfaces or truncated controls |

Add behavior tests to shared primitives and representative migration tests to each affected workflow. Test project creation, unsaved navigation, Test Notes, script editing, scenario creation, run setup, results retrieval, access editing and settings navigation together. For implementation, follow repository policy: the full dashboard `npm run test:cycle`, meaningful browser verification, and `go test ./...` if backend code changes. A CSS-only source check cannot establish that a popup or collapsed panel works.

Do not migrate business logic into the visual primitives. Preserve API contracts, role checks, concurrency guards and data semantics. Document exceptions for code editors, report documents and large calendars so consistency remains purposeful.
