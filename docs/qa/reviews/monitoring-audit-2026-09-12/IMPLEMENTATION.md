# Monitoring audit remediation

The Monitoring workspace now distinguishes configuration, observed samples, and active breaches. SLA incident episodes and acknowledgement actor/time are stored per project and survive reloads. Search, severity, status, and time filters run before pagination. Acknowledgement leaves active severity intact; a recurrence creates a new episode. Existing session alerts are not backfilled into durable history.

The journal uses a bounded worker outside metrics publication and drains during graceful shutdown. Persistence failures or backlog overflow appear as incomplete history. Abrupt process termination can lose samples still queued for storage. History follows test/project deletion. Completed runs cease contributing active incidents.

Overview prioritizes readiness, with build metadata under a disclosure. System advisories open Alerts and expose individual causes and next actions. Calendar overflow opens a day view; queued runs can be hidden, view/filter/date persist per project within the browser session, selected controls expose pressed state, and time labels include the local timezone and dates. Scheduling is available in every view.

Telemetry uses wider panels and neutral styling for optional capabilities; an enabled APM integration reporting unavailable retains a danger badge. Datasource evidence queries real Prometheus samples for 15-minute, 1-hour, or 24-hour windows, with charts, latest sample time, and refresh. Catalog configuration does not claim successful remote APM ingestion; setup guidance explains how to verify a run in the destination. Collector health refreshes every five seconds while visible. Metric dialogs explicitly show captured snapshots and offer refresh.

Fleet capacity exposes project names and pending-run links. Fleet audit filtering happens in storage before pagination, so unrelated recent access events cannot hide fleet activity.

## Validation and review

Validation ran against an isolated source copy at `/tmp/apexload-monitoring-review-20260912` because other tasks were editing and rebuilding the shared checkout. Their changes were preserved. The existing controller on port 8080 was not restarted.

- Dashboard type checking, lint, and production build passed.
- The required `npm run test:cycle` was started. Its 4,120-test unit run exposed two stale assertions in concurrent workspace changes. After updating those expectations, all 50 tests in the affected files passed. Subsequent Monitoring review added an unavailable-APM regression; the complete 54-test MonitoringPage suite passed. The remaining cycle stages were run separately after the fixes.
- The full browser profiles exercised 99 dev-admin cases and 17 local-auth cases. Stale Results search selectors and Monitoring fixture setup failures were corrected, and every failed case passed on rerun. One Help role-specific case intentionally skips in dev-admin. Final Monitoring browser checks were repeated against the final build in both profiles, including keyboard navigation, retained calendar context, live collector heartbeats, snapshot refresh, and the unavailable-APM badge. The badge failure state uses an intercepted profile response; the heartbeat uses the real collector API.
- `go test ./...` completed all other packages but exceeded the API package's default ten-minute timeout. All 4,160 API tests/examples/fuzz seed entrypoints then passed in 17 bounded batches. Storage tests passed, including schema migration, PostgreSQL SQL compatibility, incident recurrence, acknowledgement persistence, project isolation, and fleet filtering before pagination. Two Java tests that failed during the concurrent full run passed when their packages were rerun separately. Generated API contracts and QA inventory checks passed after regeneration.
- Manual browser inspection covered desktop and narrow Telemetry layouts, 18-pixel collector checkboxes and adjacent labels, calendar overflow-to-day navigation, retained filters, and repeated arrow-key focus. `git diff --check` passed.

Review also corrected the configured-profile count, stale Results test selectors, a report empty-data guard, generated route/schema expectations, and the distinction between optional APM setup and an enabled integration failure.

## Remaining environment limits

No external Prometheus or vendor APM datasource was configured in the local environment, so remote ingestion was not verified. Prometheus request windows, empty/configuration/error states, and chart rendering are covered by component tests. Durable incident behavior is verified through storage and API tests; existing historical session alerts cannot be reconstructed. The original running controller requires a normal rebuild/restart to serve the new incident endpoints.

## Captured implementation evidence

- [Telemetry layout](implemented-telemetry.png)
- [Collector metric-pack picker](implemented-collector-picker.png)
- [Narrow collector picker](implemented-picker-narrow.png)
- [Calendar day view](implemented-calendar.png)

