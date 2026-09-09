# Settings and Monitoring operator workflow review

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
