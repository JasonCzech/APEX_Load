# Documentation Archive

Status: Historical

Material here is preserved for audit and design history. It is not current product documentation, an API reference, or a release claim. For current documentation start with [docs/INDEX.md](../INDEX.md).

## Implemented plans (`plans/`)

| File | Status | Current reference |
| --- | --- | --- |
| [plans/DYNATRACE_SAAS_MONITORING_PLAN.md](plans/DYNATRACE_SAAS_MONITORING_PLAN.md) | Implemented (archived 2026-09-24) | [MONITORING_INTEGRATIONS.md](../MONITORING_INTEGRATIONS.md) |
| [plans/ENTERPRISE_DATA_PLATFORM_UPGRADE_PLAN.md](plans/ENTERPRISE_DATA_PLATFORM_UPGRADE_PLAN.md) | Implemented, all phases (archived 2026-09-24) | [ENTERPRISE_DATA_PLATFORM_OPERATIONS_RUNBOOK.md](../ENTERPRISE_DATA_PLATFORM_OPERATIONS_RUNBOOK.md) |
| [plans/AUTH_RBAC_FOLLOW_UP_TICKETS.md](plans/AUTH_RBAC_FOLLOW_UP_TICKETS.md) | Implemented, all tickets (archived 2026-09-24) | [AUTH_RBAC_OPERATIONS_RUNBOOK.md](../AUTH_RBAC_OPERATIONS_RUNBOOK.md) |
| [plans/RUN_REPORT_IMPLEMENTATION_PLAN.md](plans/RUN_REPORT_IMPLEMENTATION_PLAN.md) | Implemented (RunReportPage and related components exist) | [APPLICATION_USER_GUIDE.md](../APPLICATION_USER_GUIDE.md) |
| [plans/PROTOCOL_EXTENSION_PLAN.md](plans/PROTOCOL_EXTENSION_PLAN.md) | Implemented (SOAP/java_http exist; archived 2026-09-24) | [MESSAGING_SUPPORT.md](../MESSAGING_SUPPORT.md), [WEB_API_EXECUTION_SUPPORT_MATRIX.md](../WEB_API_EXECUTION_SUPPORT_MATRIX.md) |
| [plans/SCRIPT_EDITOR_INTERACTION_MODEL.md](plans/SCRIPT_EDITOR_INTERACTION_MODEL.md) | Implemented (archived 2026-09-24) | [APPLICATION_USER_GUIDE.md](../APPLICATION_USER_GUIDE.md#authoring-a-script) |

## Retired reference (`APEX_Load_DSL_Reference_Guide_v2.1.docx`)

| File | Status |
| --- | --- |
| [APEX_Load_DSL_Reference_Guide_v2.1.docx](APEX_Load_DSL_Reference_Guide_v2.1.docx) | Retired 2026-09-24. Hand-maintained snapshot (pinned to commit f757e96) that contradicted the code on loop caps, `pop` exhaustion, unknown action types and `fn.*()` calls. Use the generated [DSL_ACTION_REFERENCE.md](../DSL_ACTION_REFERENCE.md) and [HELP_DSL_OPERATOR_GUIDE.md](../HELP_DSL_OPERATOR_GUIDE.md) instead. |

## QA UX reviews (`qa-reviews/`)

| File | Status |
| --- | --- |
| [qa-reviews/DASHBOARD_UX_REVIEWS_2026-09.md](qa-reviews/DASHBOARD_UX_REVIEWS_2026-09.md) | Historical. Merged 2026-09-24 from the September 2026 frontend user-workflow review, usability remediation and dashboard analysis review formerly in `docs/qa/`. Current coverage: [qa/E2E_SUITES.md](../qa/E2E_SUITES.md) |
| [qa-reviews/import-reviews-complete-2026-09.md](qa-reviews/import-reviews-complete-2026-09.md) | Historical. Bruno, import design consistency and early LoadRunner review records (2026-09-19), consolidated 2026-09-24 from `docs/qa/reviews/`; captures in `qa-reviews/import-consistency-2026-09-19/` |
| [qa-reviews/ux-reviews-2026-09.md](qa-reviews/ux-reviews-2026-09.md) | Historical. Personal settings review, settings/monitoring workflow review and Execution Results design QA (2026-09), consolidated 2026-09-24 from `docs/qa/reviews/` |
| qa-reviews/`<folder>`/README.md | Historical. Completed September 2026 audits, each merged from its audit, implementation and validation files with its screenshots alongside: [ux-consistency-2026-09-08](qa-reviews/ux-consistency-2026-09-08/README.md), [post-test-reporting-2026-09-08](qa-reviews/post-test-reporting-2026-09-08/README.md), [end-to-end-ux-2026-09-09](qa-reviews/end-to-end-ux-2026-09-09/README.md), [access-audit-2026-09-12](qa-reviews/access-audit-2026-09-12/README.md), [command-center-audit-2026-09-12](qa-reviews/command-center-audit-2026-09-12/README.md), [dashboard-audit-2026-09-12](qa-reviews/dashboard-audit-2026-09-12/README.md), [import-source-audit-2026-09-12](qa-reviews/import-source-audit-2026-09-12/README.md), [new-test-audit-2026-09-12](qa-reviews/new-test-audit-2026-09-12/README.md), [results-repository-audit-2026-09-12](qa-reviews/results-repository-audit-2026-09-12/README.md), [scenario-builder-evaluation-2026-09-12](qa-reviews/scenario-builder-evaluation-2026-09-12/README.md), [script-scenario-audit-2026-09-12](qa-reviews/script-scenario-audit-2026-09-12/README.md). Reviews with open items stay in [../qa/reviews/README.md](../qa/reviews/README.md) |

## Reviews (`reviews/`)

Only the whole-application review is kept; the rest of the June and July 2026 reviews and the session handoff were deleted on 2026-09-24 and are recoverable from git. See [reviews/README.md](reviews/README.md).
