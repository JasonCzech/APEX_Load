# New Test audit remediation — September 12, 2026

The eight findings in [the audit](README.md) are addressed in the working tree. Existing launch, reservation, recurring-schedule CRUD, approval, and occurrence workflows remain in place.

| Audit finding | Implemented behavior |
| --- | --- |
| Saving during navigation could start a test | Save changes and Save draft store a tab-local draft. Neither calls test creation, start, or schedule creation. Both tabs retain edits. Saved drafts can be restored after navigation/reload. |
| Selected scenario could be outside the visible list | A persistent selection panel shows workload name, unique reference, VUsers, duration, and release. The launch controls repeat the identity. Show selected scenario reveals its exact row regardless of filtering/pagination. |
| Dense tables and hard-to-discover group editing | Scenario details move into the expanded row; all sort choices remain available. Groups use three readable columns, expandable runtime details, and explicit Edit settings buttons. The wider editor only shows browser settings for browser groups. |
| Capacity and host availability appeared contradictory | Capacity is labeled an estimate, proposed windows are distinguished from reservations, live-host inventory has a check time and refresh controls, and empty inventory explains the distinction. |
| Duplicate recurring scenario names and no cross-scenario overview | Searchable, sorted scenario choices include unique references. A project-wide catalog shows upcoming schedules, status, required actions, and access to management. Partial load failures remain visible. |
| Hidden policies and ambiguous override scope | The form displays effective approval/missed-run policies and their source before saving. It explicitly identifies the stored workload and states that run-only overrides do not apply. Unavailable system defaults block new trigger submission with a retry action. |
| Invalid cron could be submitted; preview could be stale | Save remains disabled while the authoritative preview is pending or invalid. Errors are associated with the raw cron field. A change to scenario, timezone, cron, or scheduling windows invalidates the previous preview immediately. |
| Insufficient readiness context | A review panel exposes safe target origins, nested assertion counts, group response-time SLAs, data-pool requirements, and links to owning workspaces. Missing data and unverified monitoring coverage remain explicit. |

## Review corrections

- A saved, unchanged draft no longer prompts again on navigation; subsequent edits still do.
- A missing saved capacity reservation stays attached and blocks launch until the user reviews it. Restoring a draft never silently removes the reservation.
- New recurring drafts use current system policies when restored; existing schedule revisions retain their saved policy and concurrency contract.
- Drafts are scoped to project/application and checked against the scenario revision. Malformed storage is rejected and failed writes are reported.
- Target presentation strips credentials, paths, query strings, and fragments. Dynamic/unsupported targets remain unknown rather than appearing verified.
- Readiness refresh hides stale results. Schedule catalog timestamps use the timezone named beside them.
- Scenario metadata and sorting remain accessible after reducing the visible table columns.
- The scheduling form uses aligned controls and collapses to a single column on narrower screens. Wide schedule tables scroll within their container rather than splitting action labels.
- Full-suite validation exposed an unrelated shared-checkout Bruno import regression: the review dialog referenced parent-only variables. Its guide now uses the review stage, and the main guide preserves the credential-review card's required placement. Existing Bruno tests cover both behaviors.

## Validation

- New Test unit coverage: 78 tests across New Test, draft persistence, and schedule catalog tests, included in the passing full suite.
- Browser journeys cover navigation-guard saving without execution, both-tab restoration, duplicate scenario identity, invalid cron, explicit group editing, effective policies, paused schedule persistence, and narrow-screen alignment.
- Full unit suite: **4,176 tests passed in 386 files**. Type checking, ESLint, and a fresh embedded dashboard build passed.
- Dev-admin browser profile: **104 passed, 1 intentional profile-specific skip**.
- Local-auth browser profile: **25 passed**, including both New Test journeys and the full authenticated create/launch/stop/results journey.
- The required `npm run test:cycle` was run with `VITEST_MAX_WORKERS=2` and isolated browser-test ports/output directories. It passed the unit/build/dev-admin stages, then exited during local-auth bootstrap before any tests began. Rerunning the complete local-auth profile on port 19247 passed. Logs: [dashboard cycle](dashboard-cycle.log), [local-auth retry](local-auth.log).
- Earlier full-suite runs surfaced the corrected import regression and large-import test timeouts during concurrent validation load. Worker concurrency was reduced for the passing full-suite run; no assertions or timeout limits were weakened.
- Final source whitespace validation: `git diff --check` passed. No Go source was changed for this remediation.

Browser captures: [selected workload](after-run-setup.png), [group editor](after-group-editor.png), [scheduling form](after-schedules.png), [narrow scheduling form](after-schedules-narrow.png), [readiness review](after-readiness.png).

Draft storage is local to the current browser tab. Readiness is an advisory inventory, not a live target-health or monitoring-coverage certification. The audit's baseline captures remain unchanged.
