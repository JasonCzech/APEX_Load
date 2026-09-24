# UX reviews — settings and execution results (September 2026, complete)

Status: Complete (historical). Small single-file UX reviews grouped by theme.

Grouped from three standalone review files. The `user-settings-2026-09-09/` folder next to this file holds the screenshots.

## Personal Settings review

Date: 2026-09-09. Status: Complete

### Delivered behavior

Settings remains visible in the sidebar and keyboard navigation for non-admin users, including the development User preview. The personal page uses the existing administrative Settings grid, section navigation, panel toggle, form fields, colors, and responsive layout.

- Appearance: the existing six theme choices (including System), plus device-aware reduced motion.
- Tables & editor: a remembered 10/25-row default for standard tables and the existing synchronized editor word-wrap preference. Per-table overrides remain available; specialized/server-paged tables retain their controls.
- Notifications: the existing test-outcome notification preference in the native desktop app; browser users see an accurate availability explanation.
- Account: read-only name, email, and sign-in provider, with organizational profile-management guidance.

Preferences apply immediately and are browser/device-local, rather than account-synchronized. Administrative Preferences uses the same content component. The administrative Settings component mounts only after effective access establishes system/domain/project management permission; ordinary users and User preview receive the personal component, including for direct administrative destination URLs.

### Review

Reviewed the personal page at 1440×1000 and 923×987 in Dark and Light themes, including navigation expansion, collapse, section changes, readable labels/help, and shared administrative Preferences. Screenshots are alongside this report. Restored Dark theme, Admin preview, and the original viewport after inspection.

Fixed a missing administrative Preferences region label found by regression tests. Added explicit focus handling when selecting a section closes narrow navigation. The page performs no administrative settings requests or identity mutations.

### Validation

Completed `npm run test:cycle` and reran the local-auth profile after correcting its new account assertion from “User” to the seeded “Local Viewer” display name.

- Type checking, lint, and embedded dashboard build: passed.
- Unit suite: 3,962 tests in 362 files passed; the final personal-page review changes also passed their focused rerun.
- Dev-admin browser profile: all 76 workflows passed, including existing accessibility and five-theme/two-width checks.
- Local-auth browser profile: all 9 workflows passed after the fixture assertion correction. The new viewer test verifies theme, reduced motion, table-row default and word-wrap persistence; read-only account identity; direct administrative destination fallback; no system-settings fetch; and HTTP 403 for an administrative settings write.
- `git diff --check`: passed.

Browser workflows used disposable controllers and databases. No live account or test-run records were modified.

## Settings and Monitoring operator workflow review

Date: 2026-09. Status: Complete; the follow-up implementation finished the remaining workflow work and final verification passed

Settings now uses searchable, addressable task destinations. Infrastructure editors live in Settings; Monitoring exposes runtime information and scoped configuration links. Access & Security owns authentication, feature/import/redaction/secret policies and governance audit. Repository owns shared test data. Existing storage and authorization boundaries remain in place.

Implemented operator changes:

- Distinct provider pages for Prometheus, OpenSearch and Grafana, with saved-connection verification and dashboard handoffs. Tests verify API access, not metric arrival. GitHub shows project/global defaults and collapses App automation credentials. The CloudTest delivery reference is in the API Guide's administration documentation.
- Standing allocations remain separate from scheduled bookings. Authorized project, scenario, generator-pool and metric-pack selectors replace free-form identifiers. Draft allocation counts are labeled. Missing pool prerequisites disable provisioning without blocking smaller static-pool requests that have runtime members.
- Explicit deployment/project/browser scope, pending save summary, platform-only reset, browser-only appearance preferences, and retained unsaved-change guards. Workspace setup includes release creation on its management page.
- Notification All/Selected event selection, sample payload preview, explicit saved-destination test and bounded delivery history. History is controller-local, holds the last 50 completed attempts and clears on restart; it is not a durable delivery ledger.
- Governance audit project/date/action/actor/status filters and pagination; capped CSV exports state the cap. Federation uses local hierarchy catalogs and full remote workspace discovery, including empty workspaces.
- Runtime redaction validation/sample preview and provisioning guidance for read-only secret providers.
- Correct tracked-test labeling, capability/configuration separation, no health-looking navigation fractions, no “healthy” claim for absent telemetry samples, readable readiness objects, responsive calendar controls, opaque dialogs, corrected stacking, labeled controls and keyboard tab navigation.

Review fixes include stale diagnostic previews, legacy settings revision handling, omitted notification indices, cross-parent mapping selection, hidden editor network requests, incorrect application labels, CSS token correctness and modal stacking beneath the toolbar.

Follow-up implementation completes the remaining workflow work:

- Settings, Access activity and Fleet share audit event rendering, resource/scope presentation and accessible details. Existing query permissions and export controls remain with their owning APIs.
- An infrastructure setup guide covers Kubernetes, cloud targets and static generators. Inventory counts explicitly describe configuration rather than runtime health.
- Saved standing allocations and scheduled bookings appear together with project/pool scope, windows and approval status. They retain separate admission semantics and are not summed.
- Configuration links preserve originating run/dashboard/schedule routes. Pool and imported-dashboard links select the referenced resource and report missing resources without silently substituting another one.
- Deployment, domain and project drafts show changed field names with independent Save/Cancel controls. Credential values are excluded from summaries. Numeric validation messages are associated with their controls.
- Remote workspace discovery queries full authorized project/application/release catalogs through the existing guarded transport, including empty workspaces. Local discovery requires system administration. Responses contain only IDs and names; parent changes invalidate child options and edited connection details invalidate discovery results. Older remotes and permission failures are reported explicitly.
- Repeated notification and provider controls have purpose-specific labels. Keyboard workflows cover the setup guide and independent cancellation; automated accessibility checks cover dark/light themes.
- The generator origin-classification test explicitly opts into script-sourced redaction, consistent with the current default. The distributed merge test uses paced requests to bound archive volume; a captured timeout stack showed Parquet compression of unpaced traffic was exhausting its completion budget. Its duration and completion deadline are unchanged.

Final verification passed: `go test ./...` (API package 376.982 seconds), dashboard type checking/lint/build, 3,919 unit tests across 353 files, 65 dev-admin workflows and 8 local-auth workflows. Additional focused tests verify setup-path persistence and the pending-save cancellation guard. Documentation checks and generated API contract checks passed. Visual inspection used a real headless browser; keyboard and automated accessibility checks passed. A live VoiceOver session was not performed while the Mac was locked.

The controller on `127.0.0.1:8080` was rebuilt and gracefully restarted with its existing launch environment, working directory and SQLite database. Served version: `0.1.0-dev.20260908+8e6a13b`, build time `2026-09-08T20:33:31Z`. All 122 tracked test records remained available after restart. No active runs were interrupted.

Review fixes covered stale remote discovery responses, remote parent-path validation and response projection, scoped resource navigation, pending-save cancellation, default-field noise in change summaries, and capacity-overview spacing/empty states. An initial verification run exhausted local disk space; clearing the disposable Go build cache restored sufficient space, and the final complete suite passed.

Additional code review fixed allocation drafts being lost on pool changes/navigation, policy fields accepting edits during pending saves, stale quota information during cluster changes, and Fleet/Scheduling links dropping application/release context. Allocation saves now participate in the shared navigation guard, preserve failed drafts, and expose an explicit cancel action. Regression tests cover keep/discard, failed save before navigation, pending-save locking, stale capacity, and workspace links; 142 focused tests passed.

The full browser run exposed an additional fast-debugger completion race: a finalized start response retired the session without publishing completion activity, leaving “Replay requested” visible. The response now publishes the final timestamp and outcome itself; regression tests cover both success and failure without a WebSocket completion event.

Final follow-up verification: type checking, lint, embedded dashboard build, and all 3,927 unit tests across 353 files passed. Browser workflows passed: 65 dev-admin and 8 local-auth, including the formerly failing fast-debugger case. Validation used an isolated checkout containing the Settings/Monitoring changes plus the debugger race fix, excluding concurrent report/results work. Dedicated ports 19181/19182 avoided another task’s test controller; an earlier default-port run was invalidated by that collision. Documentation checks and `go test ./docs/qa` also passed. No remaining actionable findings were identified in this reviewed scope.

## Execution Results design QA

Date: 2026-09 (moved from the repository root 2026-09-24). Status: Complete; no open design questions

Status: Historical QA log (moved from the repository root 2026-09-24).

final result: passed

### Findings

No actionable P0/P1/P2 visual differences remain. Completed, failed, and aborted runs use the existing Results shell and its five sections. Missing archives are an evidence state within that shell. Historical execution links resolve to Results while live runs retain their execution controls.

### Visual truth and comparison evidence

Source: the existing APEX Load Results page, dark theme, `web wiki` run, Summary selected. This is the approved product visual reference, rather than a separate mockup.

Source visual truth path:
`/Users/jasonczech/.codex/visualizations/2026/09/10/01a08b79-4115-75d2-9cec-f1d6a0113bd9/execution-implementation/results-reference.png`

Implementation screenshot paths in the same directory:

- `completed-summary.png`: same run, theme, viewport and Summary state as the reference.
- `completed-execution.png`: new Execution section, including visible keyboard focus and captured load history.
- `failed-execution.png`: actual `jms-rel-run`, including its recorded controller restart cause and missing archive notice.
- `failed-summary.png`: the failed run's Summary and investigation actions.

The desktop source and implementation screenshots are 1422 × 800 pixels at the same browser content size. They were compared at matching image dimensions without density scaling. Source and implementation were opened together in a single comparison input; the matched Summary views establish shell fidelity. Execution and failed states intentionally have different content and vertical proportions, so those comparisons assess consistency rather than pixel identity.

Responsive evidence: `dashboard/test-results/dev-admin/workflow-execution-results-f9b60-vigation-without-an-archive/execution-narrow.png`, a 760 × 900 screenshot from the aborted-run workflow. The cards wrap and the five tabs remain reachable with no document overflow. This is a separate responsive check, not a density-normalized comparison against the desktop reference.

Full-view screenshots made the typography, controls, borders and spacing legible; a separate magnified crop was unnecessary. Group configuration, raw manifest disclosure, historical diagnostic timestamps and tab focus were additionally inspected in the browser.

### Required fidelity surfaces

- **Typography:** existing Results font family, title hierarchy, label weights and secondary-text treatment are retained. Long notices wrap without truncation. Dates and metrics use readable labels and explicit missing-value text.
- **Spacing/layout:** shared toolbar, panel padding, border radii and tab alignment match the source. Execution uses a compact facts row, one load timeline, one group table and an event section. Tables and raw records scroll locally; responsive cards wrap.
- **Colors/tokens:** existing surface, border, text and accent tokens are reused. Failure uses a restrained red heading and edge; missing evidence remains neutral. Zero transaction errors cannot produce a successful execution badge.
- **Image/assets:** existing APEX logo and navigation assets remain unchanged and sharp. No generated or replacement visual assets were introduced. The load chart renders recorded data with the existing Recharts library.
- **Copy/content:** configured load and duration are distinguished from observed elapsed time and peak load. The original plan is called immutable only when provenance explicitly supports that claim. Missing timestamps display “Not recorded”; retained samples are not presented as whole-run KPIs or current system health.

### Comparison and correction history

1. The audit identified three disconnected destinations and missing failure context. The implementation moved terminal execution into Results and surfaced the recorded reason above its tabs.
2. Initial browser review exposed an irrelevant metric-population selector in Execution. It is now limited to analysis sections; `completed-execution.png` shows the corrected hierarchy.
3. Browser interaction checks exposed focus loss when Results section changes remounted the route. Report section/lens changes now preserve the page instance. The final screenshot shows the Execution focus outline, and browser tests cover End-key navigation.
4. Actual failed-run inspection exposed retained events stuck loading under StrictMode. Request invalidation now resets the obsolete in-flight reference. The browser shows the retained create/start events, with a dedicated regression test.

### Verification and limits

The required dashboard cycle was run: type checking, lint, unit tests, a fresh embedded build, and both workflow profiles. All 4,051 unit tests passed. Browser expectations for old archive destinations were updated to open the Results Evidence disclosure before downloading; the final dev-admin rerun passed all 85 tests. Local-auth passed all 10 tests. Verification logs: `/tmp/execution-cycle-verified.log` and `/tmp/execution-workflows-final.log`.

Manual checks covered completed and failed legacy URLs, retained evidence, expandable configuration, diagnostic timestamps, common navigation, and keyboard focus. Automated workflows cover archive-backed analysis and COMPLETE/FAILED/ABORTED states without archives. No Go source changed.

The existing records do not establish original ramp schedules or complete telemetry for every run. The UI explicitly preserves those limitations. It does not reconstruct missing measurements or infer a successful workload from retained counters.

### Implementation checklist

- [x] Use one Results shell for terminal run states.
- [x] Surface recorded failure reasons and evidence availability.
- [x] Separate configuration, observed measurements and acceptance.
- [x] Preserve shared styling and keyboard navigation.
- [x] Inspect desktop, narrow and missing-evidence states.

No blocking open design questions or follow-up polish findings remain.

### Code review follow-up

The implementation review corrected three P2 issues:

- Missing fields in an immutable configuration no longer fall back to mutable test values. Header and facts display missing values explicitly.
- Manifest request failures no longer also claim that no lifecycle history was retained. Retry recovery is covered by a component regression test.
- The execution chart uses a numeric time axis, sorts retained samples, and rejects invalid dates and negative load values. Irregular timestamps retain their actual elapsed spacing, and an unavailable configured reference is omitted.

The corrected chart was opened in the browser at the original desktop viewport. Screenshot: `/Users/jasonczech/.codex/visualizations/2026/09/10/01a08b79-4115-75d2-9cec-f1d6a0113bd9/execution-implementation/review-completed-execution.png`. No layout regression was observed. Regression coverage includes incomplete immutable records, failed event retrieval and retry, irregular sampling, out-of-range timestamps, and input preservation.

Post-review validation: `npm run test:cycle` passed in full: type checking, lint, 4,054 unit tests, embedded build, 85 dev-admin workflows and 10 local-auth workflows. Log: `/tmp/execution-review-cycle.log`. `git diff --check` passed.
