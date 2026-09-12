# Access usability and functionality audit

Reviewed September 12, 2026. **Verdict: substantial functionality is implemented, but the experience is not yet complete for dependable day-to-day access administration.** Fix the wrong feature-policy destination and incomplete audit-history/export behavior first; then improve review remediation, accessibility and navigation.

## Scope and evidence

Live APEX Load at http://127.0.0.1:8080, Admin development view, Dev Bypass authentication, Default Domain / ACC. Browser build identified itself as v0.0.0-20260912012333-6d53cf50a74c+dirty, built September 11. Used the Codex in-app browser, its initial narrow viewport and a temporary 1440 × 1000 desktop viewport. Browser sizing was restored afterward. All screenshots were captured, saved and inspected during this run; no historical screenshots were used. Some screenshots show only the viewport because the app uses internal scrolling; additional form content is captured separately.

Source was reviewed in the current working tree, which already contains unrelated changes and can differ from the running embedded build. Runtime findings and source-only limits are identified separately. No application source was changed. No users, credentials, permissions, secrets or policies were created, rotated, deleted or saved.

User goal: identify a principal, understand their effective access, grant or revoke intended access, provision automation, and investigate or resolve access issues without losing scope or identity context. Accessibility review covered observable semantics, selection state, keyboard tab navigation and visible layout; this is not a WCAG compliance certification.

## What already works

- Separate human and automation principals, with useful role/scope/status filters and detailed metadata.
- User onboarding supports initial global, domain, project and repository roles, rather than requiring a second access-assignment step.
- Service-account purpose presets, ownership fields, expiry/rotation settings, scoped roles, edit and lifecycle controls exist.
- Audit events, usage attribution, access posture and a read-only role-permission simulation are implemented.
- Secrets accurately explains a read-only provider; redaction explains the built-in baseline even without custom rules.
- Live invalid-email validation and keyboard arrow navigation worked.

Two targeted suites passed: **27 tests across AccessPage.test.tsx and FeatureAccessPanel.test.tsx** using `npm test -- --run src/components/AccessPage.test.tsx src/components/FeatureAccessPanel.test.tsx`. These component tests corroborate implemented contracts; they do not certify the running server or fix the observed integration/layout gaps. Full frontend cycle and Go suites were not run because this was an audit with no application-code changes.

## Prioritized findings

P1 = task-blocking or materially misleading output. P2 = significant usability/accessibility or workflow incompleteness. P3 = clarity and efficiency.

### F1 · P1 · Feature policy displays the wrong settings body

**Evidence:** Steps 11, 16.

Selecting Domain feature policy updates the URL/title but leaves Authentication or Governance audit controls visible. An administrator cannot reliably reach the intended policy and can misinterpret deployment controls as domain controls.

**Acceptance criteria:** Make the destination determine both heading and rendered section. Show loading or denied state while permissions resolve. Verify direct links, reloads, and navigation from every Access destination.

**Implementation reference:** SettingsPage.tsx:971 resets feature-access to general when canManageSystem is false, including during permission loading; the destination effect at :1164 only depends on destination. This is a plausible source mechanism, not a proven runtime trace.

### F2 · P1 · Activity cannot browse or export complete matching history

**Evidence:** Step 6.

Both list and export use buildActivityFilter(), which hard-codes limit: 50 and supplies no offset. The generic table paginates only those fetched rows. “50 audit events” looks like a total, and Export CSV does not disclose truncation.

**Acceptance criteria:** Implement server pagination or cursor-based history; distinguish loaded count from total/has-more. Export the full matching set or state an explicit cap and provide a complete export workflow. Test with more than 50 matching events.

**Implementation reference:** accessUtils.ts:208–218; AccessPage.tsx:639–662 and :1354–1364; handlers_auth.go:1947–2024.

### F3 · P2 · Usage and review export the wrong kind of data

**Evidence:** Steps 7, 8.

Export CSV remains visible across all Activity subtabs but always calls the user-activity event export. It cannot export the displayed usage buckets or access-review findings. Those subtabs also retain the audit-event count.

**Acceptance criteria:** Provide a subtab-specific title, count, scope summary and export. If an export is unsupported, hide it or label it Export audit events explicitly.

**Implementation reference:** AccessPage.tsx:2553–2583; handleExportUserActivity in handlers_auth.go writes event columns only.

### F4 · P2 · Access Review identifies issues but does not support resolution

**Evidence:** Steps 4, 8.

Flagged account/user names are not links. No direct Inspect, Rotate, Edit access, or review-completion flow connects the report to the existing lifecycle controls. Disabled and never-used principals are counted alongside actionable overdue rotation.

**Acceptance criteria:** Link each principal to its selected detail view; show reason, severity, owner, relevant action and last-reviewed state where supported. Align the attention summary with the actual flags.

**Implementation reference:** AccessPage.tsx:1973–2025.

### F5 · P2 · Permission controls do not expose their meaning accessibly

**Evidence:** Steps 5, 17.

Global-role and token-scope selections are indicated visually by CSS alone. Scope role dropdowns expose values such as Editor without an accessible label identifying the domain/project/repository. Keyboard users and screen-reader users cannot reliably determine selection and context.

**Acceptance criteria:** Use checkboxes or aria-pressed buttons for multiple selection; label dropdowns, e.g. Role for ACC. Verify selected state and labels through the accessibility tree and a screen-reader pass.

**Implementation reference:** AccessPage.tsx:1746–1796 and :1803–1841; live AX inspection and aria-pressed read corroborate this.

### F6 · P2 · Creation forms retain another principal’s inspector

**Evidence:** Steps 3, 5, 17.

Onboard Users keeps Codex QA User details; Generate Key keeps Verify SA details and lifecycle buttons while the new form contains CI Runner. This competing identity context makes the target of actions harder to understand.

**Acceptance criteria:** Hide existing-principal details during creation or replace them with a clearly labeled draft access summary. Keep lifecycle actions only in existing-account views.

**Implementation reference:** AccessPage.tsx:2322 and :2548 render the inspectors independent of the creation subtab.

### F7 · P2 · Access-review and simulator tables miss the established table styling

**Evidence:** Steps 8, 9.

Headers and cells run together and flags read as concatenated plain text. Other Access tables have borders, padding and status pills.

**Acceptance criteria:** Use the shared access-data-table styling and responsive wrapper; add clear spacing and readable flag badges. Verify long names, several flags and narrow layouts.

**Implementation reference:** Review/simulator use access-table and access-flag-badge classes. No corresponding selectors were found in dashboard CSS; Pages.css:2402 onward styles access-data-table.

### F8 · P2 · The simulator is not a complete API-access answer

**Evidence:** Step 9.

The UI correctly warns that it evaluates roles only, but Allowed operations can imply a service-account key can perform operations its token scopes exclude. Verify SA has results:read while the result includes broader role-derived operations. Its scope is a UUID rather than ACC.

**Acceptance criteria:** Rename it Role permissions preview unless it intersects RBAC and token scopes. For a full checker, accept action/resource/scope and explain allow/deny reasons. Show human-readable scope names.

**Implementation reference:** AccessPage.tsx:2028–2087; simulatorPrincipalOptions at :347 is also limited to the currently loaded user list, a scaling gap requiring a larger fixture to verify.

### F9 · P2 · Access navigation changes structure between destinations

**Evidence:** Steps 10–16.

Related administration replaces the three Access tabs with the complete Settings tree. Users must scan unrelated Platform/Connections/Infrastructure sections; the return control is far down the navigation. Main Access subtabs do not update the URL.

**Acceptance criteria:** Keep an Access-specific shell with persistent identity, policy and audit navigation, a visible return path and deep-linkable subtabs. Reuse the existing components and styles.

**Implementation reference:** settingsNavigation.ts assigns Access & Security to access; AccessPage.tsx:204–207 stores subtab selection locally.

### F10 · P2 · Usage lacks a clear reporting scope and time contract

**Evidence:** Step 7.

The visible report has no date range or scope summary; its actor/project context comes from controls on Events. Backend usage calculation applies project and actor but not the audit date/status/action filters, so users can assume a relationship that is not implemented.

**Acceptance criteria:** Give usage independent, visible filters and define its time period and VUser metrics. Explain unattributed usage and link to relevant runs. Test date, actor and project isolation.

**Implementation reference:** handlers_auth.go:1969–1973 computes usage separately from filtered events; AccessPage.tsx:2659–2698.

### F11 · P3 · Wide tables and redundant panels reduce scanning efficiency

**Evidence:** Steps 1, 2, 4, 7.

Tables need horizontal scrolling even at desktop size. Identity and actions are separated; Usage and Review retain empty space intended for an inspector.

**Acceptance criteria:** Use full width when no inspector is rendered, allow column selection, pin identity/actions, and consider opening details on demand. Keep essential actions visible at narrow widths.

**Implementation reference:** Pages.css:2252 fixes a two-column workspace; AccessPage users and service-account tables have ten columns.

### F12 · P3 · Task labels and onboarding guidance need a final pass

**Evidence:** Steps 3, 5, 10, 13, 14.

Generate Key actually creates an account; Rotation Interval omits days; Onboard Users requires an upstream subject without explaining whether an invitation is sent or how the user will sign in. Generic save terminology and raw provider identifiers add friction.

**Acceptance criteria:** Use Create service account, Rotation interval (days), and mode-specific onboarding instructions. Name save buttons for the current resource and link read-only provider guidance to the relevant setup instructions.

**Implementation reference:** Observed copy and controls; the issued-key modal additionally hard-codes localhost examples in source (AccessIssuedKeyModal.tsx:16–17), which should be deployment-aware. The modal was not generated live.

## Capability completeness and verification limits

| Area | Present | Remaining gap / not verified |
|---|---|---|
| User lifecycle | Onboard, inspect, edit profile/access, disable/restore; role-scoped controls in source/tests | No live mutation, invitation/delivery, IdP identity matching, or real sign-in verification |
| Scoped authorization | Global/domain/project/repository role models and UI | No full production RBAC enforcement audit; simulator is role-only and user picker is list-page-dependent |
| Service accounts | Presets, scopes, ownership, expiry, rotate, enable/disable/delete, one-time-key modal in source/tests | No real issuance, credential copy, runner integration, rotation recovery or overlap test |
| Events | Filter, sort, inspect, CSV | Fixed 50-row request with no history traversal; export semantics need repair |
| Usage | User attribution and usage measures | Time/scope contract and matching export missing; no run drill-down verified |
| Access review | Posture flags and role simulation | No direct remediation navigation, review disposition or review export |
| Authentication | Current mode, saved/active information, restart guidance | OIDC/trusted-header/local-auth paths and restart behavior not tested |
| Domain feature policy | Dedicated component exists and focused tests pass | Live route does not display it reliably |
| Import/redaction | Policy control, custom rules, preview affordance, save controls | Saved-state persistence and preview execution not tested |
| Secrets | Provider capability explanation and management architecture | Environment is read-only; writable-provider actions not testable in this run |
| Governance audit | Scope/date/action filters, server pagination and bounded export | Nested pagination creates ambiguity; export file not downloaded during audit |

Other evidence limits: one stored user and two service accounts; no large-directory, empty-database, denied-role, backend-failure, multi-browser, screen-reader, contrast-measurement or 200% zoom matrix. Low-contrast secondary copy is a visual risk, not a measured ratio finding. Screenshot-only observations cannot prove backend authorization or mutation persistence.

## Captured walkthrough

Each step below pairs its inspected screenshot with health and notes. Retakes improved readability while preserving the same reviewed state.

### Step 1 · Users, narrow layout — Needs improvement

The sidebar collapses and filters reflow, but the wide table hides later columns and actions. The details panel moves below the list. Preserve the user identity and essential actions when horizontally scrolling.

![Step 1: Users, narrow layout](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/access-audit-2026-09-12/01-users.png)

### Step 2 · Users, desktop layout — Usable with friction

Search, role/status/scope filters, pagination, selected-row styling, role bindings, and lifecycle controls are present. The always-open inspector duplicates identity and role data and squeezes the table even at desktop width. Right-arrow keyboard navigation from Users to Service Accounts worked with visible focus.

![Step 2: Users, desktop layout](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/access-audit-2026-09-12/02-users-desktop.png)

### Step 3 · Onboard Users — Partially complete workflow

The form explains the identity-provider subject and provides initial global, domain, project, and repository assignments. It still displays an unrelated existing user in the right panel. No invitation, login handoff, or provisioning-mode explanation appears on this form. The subject field and other fields also have noticeably different sizing and label treatments.

![Step 3: Onboard Users](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/access-audit-2026-09-12/03-onboard-users.png)

### Step 4 · Existing service accounts — Broad capability, crowded layout

Names, status, roles, scope, last-use and rotation metadata are available, with edit, rotate, disable and delete controls. Attention messaging and badges help triage. The table overflows; the summary says rotation/expiry/broad scope even when one flagged account is only never-used.

![Step 4: Existing service accounts](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/access-audit-2026-09-12/04-service-accounts.png)

### Step 5 · Generate Key form — Needs clearer task framing

Purpose presets are useful and permissions can be tailored. The tab creates a new service account, while its name suggests issuing a key for an existing account. The right panel still shows Verify SA with active lifecycle actions, unrelated to the CI Runner being drafted. Rotation interval lacks a visible unit.

![Step 5: Generate Key form](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/access-audit-2026-09-12/05-generate-key.png)

### Step 6 · Activity / Events — Incomplete history navigation

Filtering, sorting, event details and table pagination are present. The visible 50-event count represents a fixed fetched slice. UI pagination only pages that slice, with no API offset advancement. The event CSV uses that same limit.

![Step 6: Activity / Events](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/access-audit-2026-09-12/06-events.png)

### Step 7 · Activity / Usage By User — Incomplete reporting context

Run counts, VUsers, duration and VUser seconds are shown and sortable. The header still says 50 audit events; Export CSV exports events, not usage. Project/actor filters are hidden on this subtab, and there is no visible reporting period or attribution explanation. Empty inspector space remains reserved.

![Step 7: Activity / Usage By User](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/access-audit-2026-09-12/07-usage.png)

### Step 8 · Activity / Access Review — Incomplete remediation workflow

Flagged users and service accounts are listed, but names are plain text and there are no inspect/remediate/reviewed actions. Column content visually runs together, flags lack the styling seen in other tabs, and the header/export still describe events.

![Step 8: Activity / Access Review](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/access-audit-2026-09-12/08-access-review.png)

### Step 9 · Effective-Access Simulator — Functional but limited

A real read-only simulation for Verify SA returned resource permissions. Scope is expressed using a raw project UUID. Results are explicitly role-based and exclude token-scope intersection, despite the broad Effective-Access Simulator title and Allowed operations column. Table spacing is poor.

![Step 9: Effective-Access Simulator](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/access-audit-2026-09-12/09-simulator.png)

### Step 10 · Authentication — Usable in the observed mode

Shows active/saved Dev Bypass, scope, restart requirement and explanation. Opening it replaces Access tabs with the complete Settings navigation. The return to users is below many unrelated settings. Other authentication modes and restart behavior were not exercised.

![Step 10: Authentication](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/access-audit-2026-09-12/10-authentication.png)

### Step 11 · Domain feature policy, from Authentication — Blocked by wrong content

The title and URL change to Domain feature policy but the body remains Authentication. This state persisted on a subsequent observation. The screenshot captures the actual defect, not a loading state.

![Step 11: Domain feature policy, from Authentication](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/access-audit-2026-09-12/11-feature-policy.png)

### Step 12 · Import credential policy — Usable, save clarity can improve

Clearly explains source credential migration, default-off posture and create-only behavior. The control is present, but the shared save instructions describe multiple save mechanisms. No policy changes were submitted.

![Step 12: Import credential policy](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/access-audit-2026-09-12/12-import-policy.png)

### Step 13 · Project redaction rules — Useful baseline

Built-in protection is explained even with zero custom rules. Add Rule, validation preview and Save Project Governance are available. Save wording should name the current task; preview and persistence were not exercised.

![Step 13: Project redaction rules](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/access-audit-2026-09-12/13-redaction.png)

### Step 14 · Project secrets — Correctly constrained by provider

The page explains that the environment provider is read-only and values cannot be created or rotated here. This is an environment capability limit, not evidence that secret management is unimplemented. The next step is prose rather than a direct configuration/help link.

![Step 14: Project secrets](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/access-audit-2026-09-12/14-project-secrets.png)

### Step 15 · Governance audit — Useful but confusing pagination

Provides scope/date/action/actor/status filters, event details and a stated export maximum. Two independent Previous/Next systems are visible: API pages and inner table pages. Some date controls appear native and visually inconsistent.

![Step 15: Governance audit](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/access-audit-2026-09-12/15-governance-audit.png)

### Step 16 · Feature policy after Governance audit and reload — Defect reproduced

Feature policy retains the previously selected Governance audit body; reloading preserves the mismatch. This confirms the incorrect content depends on previous settings state and is not specific to Authentication.

![Step 16: Feature policy after Governance audit and reload](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/access-audit-2026-09-12/16-feature-policy-reload.png)

### Step 17 · Remaining key scope controls — Broad coverage, accessibility gaps

Project, domain and repository checkboxes and role dropdowns are present, followed by Create Service Account. Role dropdowns have no accessible names. Selected global-role/token-scope chips are ordinary buttons without aria-pressed; the live Tests run button returned null for that attribute.

![Step 17: Remaining key scope controls](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/access-audit-2026-09-12/17-key-scope-controls.png)

### Step 18 · Onboarding email validation — Healthy basic validation

Typing invalid-email displayed an inline message and kept Create User disabled. The draft was cleared afterward. Missing-subject prevention is present; live creation and duplicate-subject handling were not exercised.

![Step 18: Onboarding email validation](/Users/jasonczech/Documents/GitHub/Project_Stormrunner/docs/qa/reviews/access-audit-2026-09-12/18-onboard-validation.png)

## Suggested implementation order

1. Repair feature-policy routing and make headings/body share one authoritative destination; add browser coverage through permission-loading transitions.
2. Correct event pagination and all export contracts with fixtures above 50 records.
3. Make review findings actionable; clarify simulator scope and limitations.
4. Fix selection semantics, named role inputs, table styling and creation identity context.
5. Consolidate Access navigation, reporting scope, table density and task copy.

The recommended next step is a focused correctness pass on feature-policy routing and Activity history/export before broader visual redesign.

