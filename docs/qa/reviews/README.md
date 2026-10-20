# QA Review Records

- [Roadmap HAR finalization budget (2026-10-19)](roadmap-har-finalization-budget-2026-10-19/README.md) — shared publication deadline, explicit exhaustion and retained physical ownership.

- [Roadmap browser baseline qualification guards (2026-10-19)](roadmap-browser-baseline-qualification-2026-10-19/README.md) — effect floors and known document-loss refusal.

Status: Historical index (point-in-time UX/QA reviews and feature evaluations; not current product documentation). This is the single review index. Live status for the test program is in [../STATUS.md](../STATUS.md) and [../DEFECTS.md](../DEFECTS.md).

This directory holds dated reviews that still carry open items or are still cited, together with their captured evidence (screenshots, logs, JSON) in the sibling folders. Completed reviews are collected in the themed reports below and [September audits](../../archive/qa-reviews/AUDITS_2026-09.md); see [../../archive/README.md](../../archive/README.md) for everything else that was archived or removed (including how to restore deleted files from git). The whole-application review of 2026-08-02 was removed on 2026-09-24; its 11 tracked defects are in [../DEFECTS.md](../DEFECTS.md).

The themed reports preserve the original narrative of each consolidated review, including its date, scope and qualification limits.

## Previously tracked open reviews

| File | Date | Status | Purpose |
| --- | --- | --- | --- |
| [peer-live-verification-2026-10-18/README.md](peer-live-verification-2026-10-18/README.md) | 2026-10-18 | Peer findings fixed; independent reviews and targeted verification completed; full suites not run | Import performance and canonical response compatibility, runtime footprints, archive incarnation isolation, telemetry publication and dated execution evidence; full-suite limits explicitly recorded. |
| [full-codebase-2026-10-17/FIXES.md](full-codebase-2026-10-17/FIXES.md) | 2026-10-17 | Eleven correctness findings fixed; broader refactoring recommendations retained | Capacity ownership, recording and device workflows, scenario history, CLI lifecycle/reporting, and exact protocol extraction; regression tests and repeated independent review. |
| [windows-native-har-2026-10-16/README.md](windows-native-har-2026-10-16/README.md) | 2026-10-16 | Waves 1-4 delivered; qualified on Windows by three green CI runs in a row | Native HAR capture on Windows: the platform gate, the Phase 0 spike findings (identity, sharing, durability, ACLs, locks, process identity), `pkg/internal/winfs`, Windows ownership, the Windows tests and CI step, and each Windows run |
| [feature-completion-wave1-2026-10-06/README.md](feature-completion-wave1-2026-10-06/README.md) | 2026-10-06 | Wave 1 implemented and reviewed; requirements remain open | Scenario authoring view, conditioned writers and idempotent creates; summary and histogram imports; source epochs; resource association and evidence budget; evaluation jobs, policies and acceptance records |
| [aura-record-replay-2026-10-03/implementation.md](aura-record-replay-2026-10-03/implementation.md) | 2026-10-03 | Implementation slices in progress | Aura outcomes, React recording/replay, correlation, device identity and HTTP/debugger fidelity; each slice is tested and independently reviewed before commit. |
| [web-record-replay-roadmap-2026-10-03/roadmap.md](web-record-replay-roadmap-2026-10-03/roadmap.md) | 2026-10-03 | Roadmap delivered; live device/session qualification open | Evidence-backed VuGen/browser priorities for React, Salesforce Aura and Chrome on a physical iPad. |
| [java-dependency-import-2026-10-01/import-review.md](java-dependency-import-2026-10-01/import-review.md) | 2026-10-01 to 2026-10-02 | Import/save/reopen complete; replay gated | Java VuGen settings, supplemental dependency provenance, stored JAR binding fidelity, independent code review and durable review navigation. |
| [clm-import-2026-10-01/import-review.md](clm-import-2026-10-01/import-review.md) | 2026-10-01 | Import/save/reopen complete; original replay prerequisites remain | ZIP/USZ collection fidelity, governed Java/Kafka settings, independent code review, and durable native-script navigation. |
| [truclient-import-2026-10-01/import-review.md](truclient-import-2026-10-01/import-review.md) | 2026-10-01 | Import/save/reopen complete; replay gated | Source settings, browser-profile policy, parameter/data and transaction accounting, with saved authoring-review evidence. |
| [authoring-analytics-cloud-2026-10-01/REVIEW.md](authoring-analytics-cloud-2026-10-01/REVIEW.md) | 2026-10-01 | Implemented slices validated; broader roadmap scope open | Authoring handoffs, native panel creation, cloud collector previews, bounded properties and messaging lag rules. |
| [import-reviews-2026-09.md](import-reviews-2026-09.md) | 2026-09-12 to 2026-09-24 | Open | Import fixture matrix and broker runbook, LoadRunner missing-producer investigation, importer consistency audit, LoadRunner issue-detail and panel-design reviews, JMeter import fidelity and source-plan batch reviews. Evidence folders: `import-fixture-matrix-2026-09-12/`, `importer-consistency-2026-09-23/`, `jmeter-import-fidelity-2026-09-23/`, `loadrunner-issue-detail-2026-09-23/`, `loadrunner-panel-design-2026-09-23/` |
| [ux-reviews-2026-09.md](ux-reviews-2026-09.md) | 2026-09-09, 2026-09-14 | Open | Command Center usability review (remaining original recommendations) and load impact heatmap review (no implementation yet). Evidence folders: `command-center-usability-2026-09-09/`, `heatmap-audit-2026-09-14/` |
| [monitoring-audit-2026-09-12/README.md](monitoring-audit-2026-09-12/README.md) | 2026-09-12 | Implemented; one verification limit open | Monitoring and fleet views audit and remediation; remote Prometheus/APM ingestion never verified against a real datasource |
| [evaluations-2026.md](evaluations-2026.md) | 2026-07-30, 2026-09-22 | Data Pools roadmap (Tier B/C) open; LoadRunner runtime-settings evaluations complete | Data Pools API and UX evaluation, LoadRunner runtime-settings import evaluations for `ACME_RCI` and `ACME_IAO`. Current importer behavior: [../../IMPORT_FIDELITY_AND_REPAIRS.md](../../IMPORT_FIDELITY_AND_REPAIRS.md) |

## Completed implementation reviews

[Browser workflow isolation and roadmap integration — 2026-10-20](roadmap-e2e-isolation-2026-10-20/README.md) records the combined precision-consent/main regression, private import-store isolation, measured bundle inventory and retained failed attempts.

[Native decimal precision consent — 2026-10-20](roadmap-native-precision-2026-10-20/README.md) records source/revision-bound conversion consent, worker revalidation, persistence fences and the original slice evidence; its broad browser follow-up is recorded above.

Completion here applies only to the reviewed slice. Outstanding provider, platform, scale and parent-feature gates are retained inside each report and linked from the [roadmap](../../ROADMAP.md). Consolidation does not mark those gates passed. Evidence directories and non-Markdown artifacts remain unchanged.

[Coordinated starts and published clocks — 2026-10-19](roadmap-coordinated-published-clock-2026-10-19/README.md) retains the full-suite failure, clock-domain correction and generator execution regressions.

[Local-wall timestamp qualification — 2026-10-19](roadmap-analytics-local-wall-times-2026-10-19/README.md) records unique-instant refusal, non-hour transitions and retained row provenance.

[Native timestamp source offsets — 2026-10-19](roadmap-analytics-timestamp-offsets-2026-10-19/README.md) records explicit source-offset validation, per-row DST fixtures, rejection provenance and source non-mutation.

[Java HTTP body conversion — 2026-10-19](roadmap-java-body-conversion-2026-10-19/README.md) records single conversion, independent endpoint bytes, compression and replay-provenance regression evidence.

[Monotonic browser timing — 2026-10-19](roadmap-monotonic-browser-timing-2026-10-19/README.md) records local receipt identity, monotonic callback timing and visibility/session regression evidence; visible-presentation qualification remains open.

[Durable controller cursors — 2026-10-19](roadmap-durable-cursors-2026-10-19/README.md) records atomic cursor/effects persistence, bounded source-window recovery, fencing, migration and replay evidence.

[Source collection windows — 2026-10-19](roadmap-source-windows-2026-10-19/README.md) records the next bounded TEL-A prerequisite: source drain-window provenance on the wire and in generator archives; controller recovery follows in the slice above.

[Replay-safe telemetry coverage — 2026-10-19](roadmap-telemetry-coverage-2026-10-19/README.md) records the first bounded TEL-A implementation slice, regression evidence and remaining gates.

| Themed report | Scope |
| --- | --- |
| [Messaging](../../archive/qa-reviews/MESSAGING_2026-10.md) | Eight Event Hubs, Kafka, Service Bus and Solace implementation reviews |
| [Analytics](../../archive/qa-reviews/ANALYTICS_2026-10.md) | Four catalog, partition and live/archive panel reviews |
| [Authoring](../../archive/qa-reviews/AUTHORING_2026-10.md) | Goals persistence and script revision foundations |
| [Browser](../../archive/qa-reviews/BROWSER_2026-10.md) | Availability correction, document evidence and HAR compatibility |
| [Integration](../../archive/qa-reviews/INTEGRATION_2026-10.md) | Publication and composed-source qualification |

### Earlier completed reviews

| File | Date | Purpose |
| --- | --- | --- |
| [AUDITS_2026-09.md](../../archive/qa-reviews/AUDITS_2026-09.md) | 2026-09-06 to 2026-09-20 | Single archive file with one section per completed review: UX consistency, post-test reporting, end-to-end UX, the 2026-09-12 audits (access, Command Center, landing dashboard, Import From Source, New Test, Results and Repository, Scenario Builder, Script and Scenario Library), Dashboard UX reviews, personal/admin settings and execution results design QA, and the Bruno, import consistency and early LoadRunner review records. Captures stay in the sibling folders under `docs/archive/qa-reviews/` |

## Frontend review captures, 2026-09-06

Four screenshots taken for the first-pass frontend user-workflow review (status: historical; findings F1 to F5 and the proposed E2E cases are written up in the [Dashboard UX reviews section](../../archive/qa-reviews/AUDITS_2026-09.md#dashboard-ux-reviews-2026-09-06-to-2026-09-20)). They are kept in `../frontend-review-2026-09-06/`: [dashboard](../frontend-review-2026-09-06/01-dashboard.png), [scenario start](../frontend-review-2026-09-06/02-scenario-start.png), [validation](../frontend-review-2026-09-06/03-validation.png), [empty script picker](../frontend-review-2026-09-06/04-empty-script-picker.png).

## Complete retained-record inventory

This inventory includes records not individually classified by the older open-review table. Inclusion does not assert that a review is open or complete: read its dated status, residuals and later composed qualification. Supporting evidence notes remain with their artifacts.

| Record | Location |
| --- | --- |
| [Analysis workflows implementation review](analysis-workflows-2026-10-01/REVIEW.md) | `analysis-workflows-2026-10-01` |
| [Recording/replay implementation slices — 2026-10-03](aura-record-replay-2026-10-03/implementation.md) | `aura-record-replay-2026-10-03` |
| [Authoring, native panels, cloud collectors and messaging — 2026-10-01](authoring-analytics-cloud-2026-10-01/REVIEW.md) | `authoring-analytics-cloud-2026-10-01` |
| [Browser workflow first-failure triage](clm-import-2026-10-01/browser-failure-triage.md) | `clm-import-2026-10-01` |
| [Independent import-fidelity code review — 2026-10-01](clm-import-2026-10-01/code-review.md) | `clm-import-2026-10-01` |
| [CLM import response performance — 2026-10-01](clm-import-2026-10-01/import-response-performance.md) | `clm-import-2026-10-01` |
| [CLM VuGen collection import and review — 2026-10-01](clm-import-2026-10-01/import-review.md) | `clm-import-2026-10-01` |
| [CLM Java Kafka import fidelity — 2026-10-01](clm-import-2026-10-01/kafka-fidelity.md) | `clm-import-2026-10-01` |
| [Web archive import fidelity — 2026-10-01](clm-import-2026-10-01/web-fidelity.md) | `clm-import-2026-10-01` |
| [Desktop save descriptor ownership review](desktop-file-ownership-2026-09-28.md) | `.` |
| [Diagnostics, collectors and preparation review — 2026-10-01](diagnostics-collectors-capacity-2026-10-01/REVIEW.md) | `diagnostics-collectors-capacity-2026-10-01` |
| [Feature and Import Evaluations, July and September 2026](evaluations-2026.md) | `.` |
| [Evidence expansion — 2026-10-02](evidence-expansion-2026-10-02/REVIEW.md) | `evidence-expansion-2026-10-02` |
| [Authoring fidelity repair — 2026-10-05](feature-completion-authoring-fidelity-2026-10-05/README.md) | `feature-completion-authoring-fidelity-2026-10-05` |
| [Instructions](feature-completion-authoring-fidelity-2026-10-05/evidence/browser-v6-dev-admin-failed/dev-admin/workflow-new-test-queued-c-334fb-urce-delay-and-root-runtime/error-context.md) | `feature-completion-authoring-fidelity-2026-10-05/evidence/browser-v6-dev-admin-failed/dev-admin/workflow-new-test-queued-c-334fb-urce-delay-and-root-runtime` |
| [Instructions](feature-completion-authoring-fidelity-2026-10-05/evidence/browser-v6-dev-admin-failed/dev-admin/workflow-new-test-queued-c-334fb-urce-delay-and-root-runtime-repeat1/error-context.md) | `feature-completion-authoring-fidelity-2026-10-05/evidence/browser-v6-dev-admin-failed/dev-admin/workflow-new-test-queued-c-334fb-urce-delay-and-root-runtime-repeat1` |
| [Instructions](feature-completion-authoring-fidelity-2026-10-05/evidence/browser-v6-dev-admin-failed/dev-admin/workflow-new-test-queued-c-f0a67-ears-the-last-authored-goal/error-context.md) | `feature-completion-authoring-fidelity-2026-10-05/evidence/browser-v6-dev-admin-failed/dev-admin/workflow-new-test-queued-c-f0a67-ears-the-last-authored-goal` |
| [Instructions](feature-completion-authoring-fidelity-2026-10-05/evidence/browser-v6-dev-admin-failed/dev-admin/workflow-new-test-queued-c-f0a67-ears-the-last-authored-goal-repeat1/error-context.md) | `feature-completion-authoring-fidelity-2026-10-05/evidence/browser-v6-dev-admin-failed/dev-admin/workflow-new-test-queued-c-f0a67-ears-the-last-authored-goal-repeat1` |
| [Instructions](feature-completion-authoring-fidelity-2026-10-05/evidence/browser-v6-dev-admin-failed/dev-admin/workflow-new-test-queued-g-18b8b-ket-zero-and-explicit-clear/error-context.md) | `feature-completion-authoring-fidelity-2026-10-05/evidence/browser-v6-dev-admin-failed/dev-admin/workflow-new-test-queued-g-18b8b-ket-zero-and-explicit-clear` |
| [Instructions](feature-completion-authoring-fidelity-2026-10-05/evidence/browser-v6-dev-admin-failed/dev-admin/workflow-new-test-queued-g-18b8b-ket-zero-and-explicit-clear-repeat1/error-context.md) | `feature-completion-authoring-fidelity-2026-10-05/evidence/browser-v6-dev-admin-failed/dev-admin/workflow-new-test-queued-g-18b8b-ket-zero-and-explicit-clear-repeat1` |
| [Instructions](feature-completion-authoring-fidelity-2026-10-05/evidence/browser-v6-dev-admin-failed/dev-admin/workflow-scenario-composit-1f0ac-authored-root-SLA-and-rules/error-context.md) | `feature-completion-authoring-fidelity-2026-10-05/evidence/browser-v6-dev-admin-failed/dev-admin/workflow-scenario-composit-1f0ac-authored-root-SLA-and-rules` |
| [Instructions](feature-completion-authoring-fidelity-2026-10-05/evidence/browser-v6-dev-admin-failed/dev-admin/workflow-scenario-composit-1f0ac-authored-root-SLA-and-rules-repeat1/error-context.md) | `feature-completion-authoring-fidelity-2026-10-05/evidence/browser-v6-dev-admin-failed/dev-admin/workflow-scenario-composit-1f0ac-authored-root-SLA-and-rules-repeat1` |
| [Instructions](feature-completion-authoring-revision-2026-10-05/evidence/browser-dev-admin-first-artifacts/workflow-script-editor-aut-7060d-r-save-and-canonical-reopen/error-context.md) | `feature-completion-authoring-revision-2026-10-05/evidence/browser-dev-admin-first-artifacts/workflow-script-editor-aut-7060d-r-save-and-canonical-reopen` |
| [Instructions](feature-completion-authoring-revision-2026-10-05/evidence/browser-dev-admin-first-artifacts/workflow-script-editor-lib-d68ba-eview-after-a-real-conflict/error-context.md) | `feature-completion-authoring-revision-2026-10-05/evidence/browser-dev-admin-first-artifacts/workflow-script-editor-lib-d68ba-eview-after-a-real-conflict` |
| [Instructions](feature-completion-authoring-revision-2026-10-05/evidence/browser-dev-admin-first-artifacts/workflow-script-editor-pla-1c7e1-reserves-the-current-folder/error-context.md) | `feature-completion-authoring-revision-2026-10-05/evidence/browser-dev-admin-first-artifacts/workflow-script-editor-pla-1c7e1-reserves-the-current-folder` |
| [Instructions](feature-completion-authoring-revision-2026-10-05/evidence/browser-dev-admin-first-artifacts/workflow-script-editor-two-d2399-ision-review-before-reapply/error-context.md) | `feature-completion-authoring-revision-2026-10-05/evidence/browser-dev-admin-first-artifacts/workflow-script-editor-two-d2399-ision-review-before-reapply` |
| [Instructions](feature-completion-authoring-revision-2026-10-05/evidence/browser-dev-admin-first-artifacts/workflow-script-editor-unc-413bd-ority-until-explicit-review/error-context.md) | `feature-completion-authoring-revision-2026-10-05/evidence/browser-dev-admin-first-artifacts/workflow-script-editor-unc-413bd-ority-until-explicit-review` |
| [Instructions](feature-completion-authoring-revision-2026-10-05/evidence/browser-dev-admin-fixtures-repeat-artifacts/workflow-script-editor-lib-d68ba-eview-after-a-real-conflict/error-context.md) | `feature-completion-authoring-revision-2026-10-05/evidence/browser-dev-admin-fixtures-repeat-artifacts/workflow-script-editor-lib-d68ba-eview-after-a-real-conflict` |
| [Instructions](feature-completion-authoring-revision-2026-10-05/evidence/browser-dev-admin-fixtures-repeat-artifacts/workflow-script-editor-unc-413bd-ority-until-explicit-review/error-context.md) | `feature-completion-authoring-revision-2026-10-05/evidence/browser-dev-admin-fixtures-repeat-artifacts/workflow-script-editor-unc-413bd-ority-until-explicit-review` |
| [Instructions](feature-completion-authoring-revision-2026-10-05/evidence/browser-dev-admin-library-repeat-artifacts/workflow-script-editor-lib-d68ba-eview-after-a-real-conflict/error-context.md) | `feature-completion-authoring-revision-2026-10-05/evidence/browser-dev-admin-library-repeat-artifacts/workflow-script-editor-lib-d68ba-eview-after-a-real-conflict` |
| [Instructions](feature-completion-authoring-revision-2026-10-05/evidence/browser-dev-admin-null-consumers-artifacts/workflow-script-editor-aut-7060d-r-save-and-canonical-reopen/error-context.md) | `feature-completion-authoring-revision-2026-10-05/evidence/browser-dev-admin-null-consumers-artifacts/workflow-script-editor-aut-7060d-r-save-and-canonical-reopen` |
| [Instructions](feature-completion-authoring-revision-2026-10-05/evidence/browser-full-cycle-read-context-final-artifacts/authoring-goals-final/dev-admin/workflow-script-goals-auth-63f1f-r-save-and-canonical-reopen/error-context.md) | `feature-completion-authoring-revision-2026-10-05/evidence/browser-full-cycle-read-context-final-artifacts/authoring-goals-final/dev-admin/workflow-script-goals-auth-63f1f-r-save-and-canonical-reopen` |
| [Instructions](feature-completion-authoring-revision-2026-10-05/evidence/browser-full-cycle-read-context-final-artifacts/authoring-goals-repeat/dev-admin/workflow-script-goals-auth-63f1f-r-save-and-canonical-reopen/error-context.md) | `feature-completion-authoring-revision-2026-10-05/evidence/browser-full-cycle-read-context-final-artifacts/authoring-goals-repeat/dev-admin/workflow-script-goals-auth-63f1f-r-save-and-canonical-reopen` |
| [Instructions](feature-completion-authoring-revision-2026-10-05/evidence/browser-full-cycle-read-context-final-artifacts/dev-admin/workflow-authoring--U07-U1-85862-ot-overwrite-stored-content/error-context.md) | `feature-completion-authoring-revision-2026-10-05/evidence/browser-full-cycle-read-context-final-artifacts/dev-admin/workflow-authoring--U07-U1-85862-ot-overwrite-stored-content` |
| [Instructions](feature-completion-authoring-revision-2026-10-05/evidence/browser-local-auth-own-keys-first-artifacts/workflow-authoring--U03-U0-b2f09-nvalid-saves-do-not-persist/error-context.md) | `feature-completion-authoring-revision-2026-10-05/evidence/browser-local-auth-own-keys-first-artifacts/workflow-authoring--U03-U0-b2f09-nvalid-saves-do-not-persist` |
| [Instructions](feature-completion-authoring-revision-2026-10-05/evidence/browser-local-auth-own-keys-first-artifacts/workflow-authoring--U04-pr-24cc3-te-a-similarly-named-script/error-context.md) | `feature-completion-authoring-revision-2026-10-05/evidence/browser-local-auth-own-keys-first-artifacts/workflow-authoring--U04-pr-24cc3-te-a-similarly-named-script` |
| [Instructions](feature-completion-authoring-revision-2026-10-05/evidence/browser-local-auth-own-keys-first-artifacts/workflow-authoring--U06-ma-6dd7d-ut-creating-a-library-entry/error-context.md) | `feature-completion-authoring-revision-2026-10-05/evidence/browser-local-auth-own-keys-first-artifacts/workflow-authoring--U06-ma-6dd7d-ut-creating-a-library-entry` |
| [Instructions](feature-completion-authoring-revision-2026-10-05/evidence/browser-local-auth-own-keys-first-artifacts/workflow-authoring--U07-U1-777a4-ot-overwrite-stored-content/error-context.md) | `feature-completion-authoring-revision-2026-10-05/evidence/browser-local-auth-own-keys-first-artifacts/workflow-authoring--U07-U1-777a4-ot-overwrite-stored-content` |
| [Instructions](feature-completion-authoring-revision-2026-10-05/evidence/browser-local-auth-own-keys-first-artifacts/workflow-authoring--U07-U1-85862-ot-overwrite-stored-content/error-context.md) | `feature-completion-authoring-revision-2026-10-05/evidence/browser-local-auth-own-keys-first-artifacts/workflow-authoring--U07-U1-85862-ot-overwrite-stored-content` |
| [Instructions](feature-completion-authoring-revision-2026-10-05/evidence/browser-local-auth-own-keys-first-artifacts/workflow-authoring--U07-U1-de155-ot-overwrite-stored-content/error-context.md) | `feature-completion-authoring-revision-2026-10-05/evidence/browser-local-auth-own-keys-first-artifacts/workflow-authoring--U07-U1-de155-ot-overwrite-stored-content` |
| [Instructions](feature-completion-authoring-revision-2026-10-05/evidence/browser-local-auth-own-keys-first-artifacts/workflow-authoring--U07-sc-50c06-exact-persistence-semantics/error-context.md) | `feature-completion-authoring-revision-2026-10-05/evidence/browser-local-auth-own-keys-first-artifacts/workflow-authoring--U07-sc-50c06-exact-persistence-semantics` |
| [Instructions](feature-completion-authoring-revision-2026-10-05/evidence/browser-local-auth-own-keys-first-artifacts/workflow-authoring--U07-sc-6893f-exact-persistence-semantics/error-context.md) | `feature-completion-authoring-revision-2026-10-05/evidence/browser-local-auth-own-keys-first-artifacts/workflow-authoring--U07-sc-6893f-exact-persistence-semantics` |
| [Instructions](feature-completion-authoring-revision-2026-10-05/evidence/browser-local-auth-own-keys-first-artifacts/workflow-authoring--U07-sc-a8ee2-exact-persistence-semantics/error-context.md) | `feature-completion-authoring-revision-2026-10-05/evidence/browser-local-auth-own-keys-first-artifacts/workflow-authoring--U07-sc-a8ee2-exact-persistence-semantics` |
| [Instructions](feature-completion-authoring-revision-2026-10-05/evidence/browser-superseded-cycle-timestamp-review-artifacts/command-center-details-run-b0569-ettings-in-an-opaque-drawer/error-context.md) | `feature-completion-authoring-revision-2026-10-05/evidence/browser-superseded-cycle-timestamp-review-artifacts/command-center-details-run-b0569-ettings-in-an-opaque-drawer` |
| [Final authoring frontend verification](feature-completion-authoring-revision-2026-10-05/evidence/frontend-final-verification.md) | `feature-completion-authoring-revision-2026-10-05/evidence` |
| [Browser document frontend qualification](feature-completion-browser-document-2026-10-05/FRONTEND_VERIFICATION.md) | `feature-completion-browser-document-2026-10-05` |
| [Instructions](feature-completion-browser-document-2026-10-05/evidence/browser-author-focus-before-fix/dev-admin/workflow-author-experience-50289-aves-without-losing-context/error-context.md) | `feature-completion-browser-document-2026-10-05/evidence/browser-author-focus-before-fix/dev-admin/workflow-author-experience-50289-aves-without-losing-context` |
| [Instructions](feature-completion-browser-document-2026-10-05/evidence/browser-focused-layout-repeat-1/dev-admin/workflow-browser-resource--6abc2--independently-from-actions/error-context.md) | `feature-completion-browser-document-2026-10-05/evidence/browser-focused-layout-repeat-1/dev-admin/workflow-browser-resource--6abc2--independently-from-actions` |
| [Instructions](feature-completion-browser-document-2026-10-05/evidence/browser-focused-layout-repeat-1/dev-admin/workflow-browser-resource--814c4-s-and-missing-peer-coverage/error-context.md) | `feature-completion-browser-document-2026-10-05/evidence/browser-focused-layout-repeat-1/dev-admin/workflow-browser-resource--814c4-s-and-missing-peer-coverage` |
| [Instructions](feature-completion-browser-document-2026-10-05/evidence/browser-focused-layout-repeat-2/local-auth/workflow-browser-resource--7036a-ranks-only-complete-devices/error-context.md) | `feature-completion-browser-document-2026-10-05/evidence/browser-focused-layout-repeat-2/local-auth/workflow-browser-resource--7036a-ranks-only-complete-devices` |
| [Instructions](feature-completion-browser-document-2026-10-05/evidence/browser-focused-preliminary-1/dev-admin/workflow-browser-resource--6abc2--independently-from-actions/error-context.md) | `feature-completion-browser-document-2026-10-05/evidence/browser-focused-preliminary-1/dev-admin/workflow-browser-resource--6abc2--independently-from-actions` |
| [Instructions](feature-completion-browser-document-2026-10-05/evidence/browser-focused-preliminary-1/dev-admin/workflow-browser-resource--814c4-s-and-missing-peer-coverage/error-context.md) | `feature-completion-browser-document-2026-10-05/evidence/browser-focused-preliminary-1/dev-admin/workflow-browser-resource--814c4-s-and-missing-peer-coverage` |
| [Instructions](feature-completion-browser-document-2026-10-05/evidence/browser-full-cycle-final-1/local-auth/workflow-author-experience-50289-aves-without-losing-context/error-context.md) | `feature-completion-browser-document-2026-10-05/evidence/browser-full-cycle-final-1/local-auth/workflow-author-experience-50289-aves-without-losing-context` |
| [Instructions](feature-completion-browser-document-2026-10-05/evidence/browser-full-cycle-focus-fixed-final-1/local-auth/workflow-analysis-preparat-0fe44-tors-across-save-and-reopen/error-context.md) | `feature-completion-browser-document-2026-10-05/evidence/browser-full-cycle-focus-fixed-final-1/local-auth/workflow-analysis-preparat-0fe44-tors-across-save-and-reopen` |
| [Browser document finality qualification — 2026-10-05](feature-completion-browser-document-finality-2026-10-05/README.md) | `feature-completion-browser-document-finality-2026-10-05` |
| [Browser document finalization isolation](feature-completion-browser-frame-isolation-2026-10-05/README.md) | `feature-completion-browser-frame-isolation-2026-10-05` |
| [Retained resource-reader backend qualification](feature-completion-browser-resources-2026-10-05/BACKEND_QUALIFICATION.md) | `feature-completion-browser-resources-2026-10-05` |
| [Retained browser resource inspection — scoped qualification](feature-completion-browser-resources-2026-10-05/README.md) | `feature-completion-browser-resources-2026-10-05` |
| [Instructions](feature-completion-browser-resources-2026-10-05/playwright/first-ui-1/dev-admin/workflow-retained-resource-ad392-alse-and-independent-clocks/error-context.md) | `feature-completion-browser-resources-2026-10-05/playwright/first-ui-1/dev-admin/workflow-retained-resource-ad392-alse-and-independent-clocks` |
| [Instructions](feature-completion-browser-resources-2026-10-05/playwright/local-ui-2-failed/local-auth/workflow-retained-resource-d21e8-navailable-evidence-as-zero/error-context.md) | `feature-completion-browser-resources-2026-10-05/playwright/local-ui-2-failed/local-auth/workflow-retained-resource-d21e8-navailable-evidence-as-zero` |
| [Lazy archive inspector and access stylesheet qualification](feature-completion-composed-review-2026-10-05/dashboard-lazy-access-styles/README.md) | `feature-completion-composed-review-2026-10-05/dashboard-lazy-access-styles` |
| [Committed frontend bundle baseline](feature-completion-composed-review-2026-10-05/evidence/bundle-baseline/README.md) | `feature-completion-composed-review-2026-10-05/evidence/bundle-baseline` |
| [Engine snapshot synchronization review — 2026-10-03](feature-completion-eh-capacity-2026-10-03/engine-snapshot-review.md) | `feature-completion-eh-capacity-2026-10-03` |
| [Engine lifecycle progress review and verification](feature-completion-eh-progress-2026-10-03/engine-review.md) | `feature-completion-eh-progress-2026-10-03` |
| [Current-checkout HAR reuse audit](feature-completion-har-import-compatibility-2026-10-05/evidence/current-checkout-har-reuse-audit.md) | `feature-completion-har-import-compatibility-2026-10-05/evidence` |
| [Import publication primitive qualification](feature-completion-import-publication-2026-10-05/README.md) | `feature-completion-import-publication-2026-10-05` |
| [Main integration — 2026-10-05 / resumed 2026-10-06](feature-completion-main-integration-2026-10-05/README.md) | `feature-completion-main-integration-2026-10-05` |
| [Instructions](feature-completion-main-integration-2026-10-05/playwright/full-cycle-1/dev-admin/command-center-details-run-b0569-ettings-in-an-opaque-drawer/error-context.md) | `feature-completion-main-integration-2026-10-05/playwright/full-cycle-1/dev-admin/command-center-details-run-b0569-ettings-in-an-opaque-drawer` |
| [Instructions](feature-completion-main-integration-2026-10-05/playwright/targeted-cycle-2/dev-admin/command-center-details-adv-a5691-cockpit-edits-or-graph-mode/error-context.md) | `feature-completion-main-integration-2026-10-05/playwright/targeted-cycle-2/dev-admin/command-center-details-adv-a5691-cockpit-edits-or-graph-mode` |
| [Messaging management foundation review and verification](feature-completion-management-2026-10-03/README.md) | `feature-completion-management-2026-10-03` |
| [Instructions](feature-completion-management-2026-10-03/local-auth-disk-reserve-error-context.md) | `feature-completion-management-2026-10-03` |
| [Shared messaging evidence review — 2026-10-03](feature-completion-messaging-2026-10-03/README.md) | `feature-completion-messaging-2026-10-03` |
| [Native HAR ownership candidate review](feature-completion-native-har-ownership-2026-10-05/README-v5-static-history.md) | `feature-completion-native-har-ownership-2026-10-05` |
| [Native HAR ownership qualification](feature-completion-native-har-ownership-2026-10-05/README.md) | `feature-completion-native-har-ownership-2026-10-05` |
| [Proposed pinned owned-HAR close acknowledgment](feature-completion-native-har-ownership-2026-10-05/evidence/apexload-native-har-owned-sdk-patch-design-20261005.md) | `feature-completion-native-har-ownership-2026-10-05/evidence` |
| [Proposed pinned owned-HAR close acknowledgment](feature-completion-native-har-ownership-2026-10-05/evidence/apexload-native-har-owned-sdk-patch-design-v2-20261005.md) | `feature-completion-native-har-ownership-2026-10-05/evidence` |
| [Native HAR working-file ownership: read-only design review](feature-completion-native-har-ownership-2026-10-05/evidence/apexload-native-har-working-ownership-design-20261005.md) | `feature-completion-native-har-ownership-2026-10-05/evidence` |
| [Imported metric identities and closed analytical requests](feature-completion-native-identity-2026-10-04/README.md) | `feature-completion-native-identity-2026-10-04` |
| [Native partition-pruning slice contract](feature-completion-native-partitions-2026-10-05/CONTRACT.md) | `feature-completion-native-partitions-2026-10-05` |
| [Canonical native query window decision — 2026-10-05](feature-completion-native-partitions-2026-10-05/PRECISION_DECISION.md) | `feature-completion-native-partitions-2026-10-05` |
| [Instructions](feature-completion-native-partitions-2026-10-05/evidence/browser-analysis-focused/local-auth/workflow-analysis-preparat-0fe44-tors-across-save-and-reopen/error-context.md) | `feature-completion-native-partitions-2026-10-05/evidence/browser-analysis-focused/local-auth/workflow-analysis-preparat-0fe44-tors-across-save-and-reopen` |
| [Instructions](feature-completion-native-partitions-2026-10-05/evidence/browser-analysis-horizontal-diagnostic-v2/local-auth/native-horizontal-diagnost-04874-tors-across-save-and-reopen/error-context.md) | `feature-completion-native-partitions-2026-10-05/evidence/browser-analysis-horizontal-diagnostic-v2/local-auth/native-horizontal-diagnost-04874-tors-across-save-and-reopen` |
| [Instructions](feature-completion-native-partitions-2026-10-05/evidence/browser-native-full-cycle/dev-admin/workflow-script-editor-aut-7060d-r-save-and-canonical-reopen/error-context.md) | `feature-completion-native-partitions-2026-10-05/evidence/browser-native-full-cycle/dev-admin/workflow-script-editor-aut-7060d-r-save-and-canonical-reopen` |
| [Portable measurement series identity — scoped qualification](feature-completion-native-rca-identity-2026-10-05/README.md) | `feature-completion-native-rca-identity-2026-10-05` |
| [Native analytics adapter plan — confirmed RCA engine](feature-completion-native-rca-identity-2026-10-05/evidence/rca-adapter-plan.md) | `feature-completion-native-rca-identity-2026-10-05/evidence` |
| [Durable uploads and complete dataset deletion](feature-completion-native-upload-2026-10-04/README.md) | `feature-completion-native-upload-2026-10-04` |
| [Native worker and durable publication foundation](feature-completion-native-worker-2026-10-04/README.md) | `feature-completion-native-worker-2026-10-04` |
| [Feature completion R0 — prerequisite verification](feature-completion-r0-2026-10-02/README.md) | `feature-completion-r0-2026-10-02` |
| [Recovery shutdown review](feature-completion-recovery-shutdown-2026-10-03/README.md) | `feature-completion-recovery-shutdown-2026-10-03` |
| [Scenario revision foundation and capability refusal — 2026-10-05](feature-completion-scenario-revision-2026-10-05/README.md) | `feature-completion-scenario-revision-2026-10-05` |
| [Private scenario source admission qualification](feature-completion-scenario-source-admission-2026-10-05/README.md) | `feature-completion-scenario-source-admission-2026-10-05` |
| [Second HAR: offline evaluation](feature-completion-second-har-evaluation-2026-10-05/README.md) | `feature-completion-second-har-evaluation-2026-10-05` |
| [Immutable telemetry policy receipt — 2026-10-05](feature-completion-telemetry-policy-2026-10-05/README.md) | `feature-completion-telemetry-policy-2026-10-05` |
| [Feature completion Wave 1 — 2026-10-06](feature-completion-wave1-2026-10-06/README.md) | `feature-completion-wave1-2026-10-06` |
| [Feature completion Wave 2 — 2026-10-07](feature-completion-wave2-2026-10-07/README.md) | `feature-completion-wave2-2026-10-07` |
| [Feature completion Wave 3 — 2026-10-08](feature-completion-wave3-2026-10-08/README.md) | `feature-completion-wave3-2026-10-08` |
| [Feature completion Wave 4 — 2026-10-09](feature-completion-wave4-2026-10-09/README.md) | `feature-completion-wave4-2026-10-09` |
| [Feature completion Wave 5 — 2026-10-09](feature-completion-wave5-2026-10-09/README.md) | `feature-completion-wave5-2026-10-09` |
| [Additional HAR import evidence — 2026-10-02](har-import-evidence-2026-10-02/additional-review.md) | `har-import-evidence-2026-10-02` |
| [HAR fidelity code review — 2026-10-02](har-import-evidence-2026-10-02/code-review.md) | `har-import-evidence-2026-10-02` |
| [HAR import fidelity evidence — 2026-10-02](har-import-evidence-2026-10-02/import-review.md) | `har-import-evidence-2026-10-02` |
| [Import reviews with open items (September 2026)](import-reviews-2026-09.md) | `.` |
| [Java VuGen import and review — 2026-10-01 to 2026-10-02](java-dependency-import-2026-10-01/import-review.md) | `java-dependency-import-2026-10-01` |
| [Java dependency import fidelity — 2026-10-01](java-dependency-import-2026-10-01/java-dependency-fidelity.md) | `java-dependency-import-2026-10-01` |
| [Monitoring and fleet audit (2026-09-12)](monitoring-audit-2026-09-12/README.md) | `monitoring-audit-2026-09-12` |
| [Next priority pass — 2026-10-01](next-priority-pass-2026-10-01/REVIEW.md) | `next-priority-pass-2026-10-01` |
| [Node-failure readiness: first implementation slice](node-failure-readiness-2026-09-27.md) | `.` |
| [Object activity and archive review — 2026-10-02](object-activity-2026-10-02/REVIEW.md) | `object-activity-2026-10-02` |
| [Priority implementation pass — 2026-10-01](priority-pass-2026-10-01/REVIEW.md) | `priority-pass-2026-10-01` |
| [gRPC diagnostic capture review](protocol-diagnostics-2026-09-30/REVIEW.md) | `protocol-diagnostics-2026-09-30` |
| [TruClient import and review — 2026-10-01](truclient-import-2026-10-01/import-review.md) | `truclient-import-2026-10-01` |
| [TruClient import fidelity — 2026-10-01](truclient-import-2026-10-01/truclient-fidelity.md) | `truclient-import-2026-10-01` |
| [UX reviews with open items (September 2026)](ux-reviews-2026-09.md) | `.` |
| [Browser and HTTP recording/replay roadmap](web-record-replay-roadmap-2026-10-03/roadmap.md) | `web-record-replay-roadmap-2026-10-03` |
| [Recording and replay usability audit](web-record-replay-roadmap-2026-10-03/usability-audit.md) | `web-record-replay-roadmap-2026-10-03` |
