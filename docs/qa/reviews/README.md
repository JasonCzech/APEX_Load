# QA Review Records

Status: Historical index (point-in-time UX/QA reviews; not current product documentation)

This directory keeps the dated September 2026 audits that still carry open items, together with their captured evidence (screenshots, logs, JSON). Reviews whose items are all implemented and verified were moved to [../../archive/qa-reviews/](../../archive/README.md#qa-ux-reviews-qa-reviews). Live status for the test program is in [../STATUS.md](../STATUS.md) and [../DEFECTS.md](../DEFECTS.md). The whole-application code review lives in [../../archive/reviews/](../../archive/reviews/README.md).

Each report merges what used to be separate audit, implementation, code review, validation and remaining-work files into one file; each section states its date and status.

## Open reviews (this directory)

| File | Date | Status | Purpose |
| --- | --- | --- | --- |
| [import-reviews-2026-09.md](import-reviews-2026-09.md) | 2026-09-12 to 2026-09-24 | Open | Import fixture matrix and broker runbook, LoadRunner missing-producer investigation, importer consistency audit, LoadRunner issue-detail and panel-design reviews, JMeter import fidelity and source-plan batch reviews. Evidence folders: `import-fixture-matrix-2026-09-12/`, `importer-consistency-2026-09-23/`, `jmeter-import-fidelity-2026-09-23/`, `loadrunner-issue-detail-2026-09-23/`, `loadrunner-panel-design-2026-09-23/` |
| [ux-reviews-2026-09.md](ux-reviews-2026-09.md) | 2026-09-09, 2026-09-14 | Open | Command Center usability review (remaining original recommendations) and load impact heatmap review (no implementation yet). Evidence folders: `command-center-usability-2026-09-09/`, `heatmap-audit-2026-09-14/` |
| [monitoring-audit-2026-09-12/README.md](monitoring-audit-2026-09-12/README.md) | 2026-09-12 | Implemented; one verification limit open | Monitoring and fleet views audit and remediation; remote Prometheus/APM ingestion never verified against a real datasource |

## Completed reviews (moved to `docs/archive/qa-reviews/`)

| File | Date | Purpose |
| --- | --- | --- |
| [ux-consistency-2026-09-08/README.md](../../archive/qa-reviews/ux-consistency-2026-09-08/README.md) | 2026-09-08 | Application-wide UX consistency audit, spec, implementation, theme matrix and validation |
| [post-test-reporting-2026-09-08/README.md](../../archive/qa-reviews/post-test-reporting-2026-09-08/README.md) | 2026-09-08 | Results library, archive and report-selection audit and implementation |
| [end-to-end-ux-2026-09-09/README.md](../../archive/qa-reviews/end-to-end-ux-2026-09-09/README.md) | 2026-09-09 | End-to-end workflow UX audit, implementation and code reviews |
| [ux-reviews-2026-09.md](../../archive/qa-reviews/ux-reviews-2026-09.md) | 2026-09 | Personal/admin settings review, settings and monitoring workflow review, Execution Results design QA |
| [access-audit-2026-09-12/README.md](../../archive/qa-reviews/access-audit-2026-09-12/README.md) | 2026-09-12 | Access administration audit and implementation |
| [command-center-audit-2026-09-12/README.md](../../archive/qa-reviews/command-center-audit-2026-09-12/README.md) | 2026-09-12 | Command Center audit and implementation review |
| [dashboard-audit-2026-09-12/README.md](../../archive/qa-reviews/dashboard-audit-2026-09-12/README.md) | 2026-09-12 | Landing dashboard audit and implementation |
| [import-source-audit-2026-09-12/README.md](../../archive/qa-reviews/import-source-audit-2026-09-12/README.md) | 2026-09-12 | Import From Source catalog audit and validation |
| [new-test-audit-2026-09-12/README.md](../../archive/qa-reviews/new-test-audit-2026-09-12/README.md) | 2026-09-12 | New Test / run setup audit and remediation |
| [results-repository-audit-2026-09-12/README.md](../../archive/qa-reviews/results-repository-audit-2026-09-12/README.md) | 2026-09-12 | Results repository audit and implementation |
| [scenario-builder-evaluation-2026-09-12/README.md](../../archive/qa-reviews/scenario-builder-evaluation-2026-09-12/README.md) | 2026-09-12 | Scenario Builder evaluation and implementation |
| [script-scenario-audit-2026-09-12/README.md](../../archive/qa-reviews/script-scenario-audit-2026-09-12/README.md) | 2026-09-12 | Script and scenario library audit and verification |
| [import-reviews-complete-2026-09.md](../../archive/qa-reviews/import-reviews-complete-2026-09.md) | 2026-09-19 | Bruno import, import design consistency and early LoadRunner review records (captures in `import-consistency-2026-09-19/`) |
