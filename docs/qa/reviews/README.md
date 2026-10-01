# QA Review Records

Status: Historical index (point-in-time UX/QA reviews and feature evaluations; not current product documentation). This is the single review index. Live status for the test program is in [../STATUS.md](../STATUS.md) and [../DEFECTS.md](../DEFECTS.md).

This directory holds dated reviews that still carry open items or are still cited, together with their captured evidence (screenshots, logs, JSON) in the sibling folders. Completed reviews live in [../../archive/qa-reviews/AUDITS_2026-09.md](../../archive/qa-reviews/AUDITS_2026-09.md); see [../../archive/README.md](../../archive/README.md) for everything else that was archived or removed (including how to restore deleted files from git). The whole-application review of 2026-08-02 was removed on 2026-09-24; its 11 tracked defects are in [../DEFECTS.md](../DEFECTS.md).

Each report merges what used to be separate audit, implementation, code review, validation and remaining-work files into one file; each section states its date and status.

## Open reviews

| File | Date | Status | Purpose |
| --- | --- | --- | --- |
| [authoring-analytics-cloud-2026-10-01/REVIEW.md](authoring-analytics-cloud-2026-10-01/REVIEW.md) | 2026-10-01 | Implemented slices validated; broader roadmap scope open | Authoring handoffs, native panel creation, cloud collector previews, bounded properties and messaging lag rules. |
| [import-reviews-2026-09.md](import-reviews-2026-09.md) | 2026-09-12 to 2026-09-24 | Open | Import fixture matrix and broker runbook, LoadRunner missing-producer investigation, importer consistency audit, LoadRunner issue-detail and panel-design reviews, JMeter import fidelity and source-plan batch reviews. Evidence folders: `import-fixture-matrix-2026-09-12/`, `importer-consistency-2026-09-23/`, `jmeter-import-fidelity-2026-09-23/`, `loadrunner-issue-detail-2026-09-23/`, `loadrunner-panel-design-2026-09-23/` |
| [ux-reviews-2026-09.md](ux-reviews-2026-09.md) | 2026-09-09, 2026-09-14 | Open | Command Center usability review (remaining original recommendations) and load impact heatmap review (no implementation yet). Evidence folders: `command-center-usability-2026-09-09/`, `heatmap-audit-2026-09-14/` |
| [monitoring-audit-2026-09-12/README.md](monitoring-audit-2026-09-12/README.md) | 2026-09-12 | Implemented; one verification limit open | Monitoring and fleet views audit and remediation; remote Prometheus/APM ingestion never verified against a real datasource |
| [evaluations-2026.md](evaluations-2026.md) | 2026-07-30, 2026-09-22 | Data Pools roadmap (Tier B/C) open; LoadRunner runtime-settings evaluations complete | Data Pools API and UX evaluation, LoadRunner runtime-settings import evaluations for `ACME_RCI` and `ACME_IAO`. Current importer behavior: [../../IMPORT_FIDELITY_AND_REPAIRS.md](../../IMPORT_FIDELITY_AND_REPAIRS.md) |

## Completed reviews

| File | Date | Purpose |
| --- | --- | --- |
| [AUDITS_2026-09.md](../../archive/qa-reviews/AUDITS_2026-09.md) | 2026-09-06 to 2026-09-20 | Single archive file with one section per completed review: UX consistency, post-test reporting, end-to-end UX, the 2026-09-12 audits (access, Command Center, landing dashboard, Import From Source, New Test, Results and Repository, Scenario Builder, Script and Scenario Library), Dashboard UX reviews, personal/admin settings and execution results design QA, and the Bruno, import consistency and early LoadRunner review records. Captures stay in the sibling folders under `docs/archive/qa-reviews/` |

## Frontend review captures, 2026-09-06

Four screenshots taken for the first-pass frontend user-workflow review (status: historical; findings F1 to F5 and the proposed E2E cases are written up in the [Dashboard UX reviews section](../../archive/qa-reviews/AUDITS_2026-09.md#dashboard-ux-reviews-2026-09-06-to-2026-09-20)). They are kept in `../frontend-review-2026-09-06/`: [dashboard](../frontend-review-2026-09-06/01-dashboard.png), [scenario start](../frontend-review-2026-09-06/02-scenario-start.png), [validation](../frontend-review-2026-09-06/03-validation.png), [empty script picker](../frontend-review-2026-09-06/04-empty-script-picker.png).
