# UX consistency implementation

The initial implementation below is supplemented by [completion of the remaining original work](REMAINING_WORK.md), including shared libraries, page structure, form feedback, the Command Center default, and the full theme matrix.

Implemented against the [audit](AUDIT.md) and [interaction contract](UX_CONSISTENCY_SPEC.md). The existing APEX palette, brand assets, authorization rules, and workflow-specific editors remain the foundation.

## Completed work

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

## Intentional variations

- Code documents keep closable document tabs; wizards retain numbered steps and their guarded Back/Next flow. These serve different tasks from page navigation.
- The full-screen report composer already renders outside app containers and retains document/canvas geometry. Small anchored informational popovers are not full modal dialogs.
- Settings keeps inline creation when it is part of domain/project configuration; global selectors use compact creation dialogs. They share field styling while retaining each workflow's pinned scope and validation.
- Access uses server-side pagination with its existing 50-row default; ordinary asset libraries retain 10-row pages. The control order remains consistent.
- Panel visibility is temporary UI state. Existing explicit editor/global layout preferences retain their persistence semantics; hiding a panel does not reset its data.
- Overview and Analysis remain available as operator views. This migration removes visible implementation-version naming without removing supported controls.
- Secondary table columns remain available through expansion, the explicit column control, or horizontal scrolling. Mobile phone use and exhaustive screen-reader certification are not claimed.

## Review fixes

The implementation review fixed duplicate tab activation, a repeated-delete race after asynchronous confirmation, hidden-opener focus restoration, navigation collapsing after Keep editing, compressed narrow script names, Monitoring tabs clipped beside advisories, fixed-width Repository filters, and false empty-library messaging after a failed load. The queued-run recovery test now waits for its enabled action instead of assuming that a debounce has finished.

## Evidence

The `after-*.jpg` captures in this directory record rendered verification. Matching reference/implementation images were opened together for Notes, project creation, dashboard, script library/editor, Monitoring, schedules, Repository, API Guide, Command Center and Settings. See the application-wide section in the root `design-qa.md` for comparison findings and verification limits.
