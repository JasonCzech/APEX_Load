# Documentation Archive

Status: Historical

Material here is preserved for audit and design history. It is not current product documentation, an API reference, or a release claim. For current documentation start with [docs/INDEX.md](../INDEX.md). Open reviews and evaluations are indexed in [../qa/reviews/README.md](../qa/reviews/README.md).

## Contents

| Path | Status |
| --- | --- |
| [qa-reviews/AUDITS_2026-09.md](qa-reviews/AUDITS_2026-09.md) | Historical. One file with a section per completed September 2026 UX/QA audit (UX consistency, post-test reporting, end-to-end UX, the 2026-09-12 access, Command Center, landing dashboard, Import From Source, New Test, Results and Repository, Scenario Builder and Script/Scenario Library audits, Dashboard UX reviews, settings and execution-results reviews, Bruno/import-consistency/LoadRunner import reviews). Consolidated 2026-09-24 from 14 markdown files. Current browser coverage: [qa/E2E_SUITES.md](../qa/E2E_SUITES.md) |
| `qa-reviews/<audit-folder>/` | Evidence only (screenshots, logs, JSON, DOM inventories) referenced from `AUDITS_2026-09.md`: `ux-consistency-2026-09-08`, `post-test-reporting-2026-09-08`, `end-to-end-ux-2026-09-09`, `access-audit-2026-09-12`, `command-center-audit-2026-09-12`, `dashboard-audit-2026-09-12`, `import-source-audit-2026-09-12`, `new-test-audit-2026-09-12`, `results-repository-audit-2026-09-12`, `scenario-builder-evaluation-2026-09-12`, `script-scenario-audit-2026-09-12`, `import-consistency-2026-09-19`, `user-settings-2026-09-09` |
| [APEX_Load_DSL_Reference_Guide_v2.1.docx](APEX_Load_DSL_Reference_Guide_v2.1.docx) | Retired 2026-09-24. Hand-maintained snapshot (pinned to commit f757e96) that contradicted the code on loop caps, `pop` exhaustion, unknown action types and `fn.*()` calls. Use the generated [DSL_ACTION_REFERENCE.md](../DSL_ACTION_REFERENCE.md) and [HELP_DSL_OPERATOR_GUIDE.md](../HELP_DSL_OPERATOR_GUIDE.md) instead |

## Consolidated current documentation (2026-09-24)

The following focused guides were folded into their catalog parents to reduce
navigation duplication while preserving their in-app article sections. Recover
the pre-merge source from the branch parent `7234a9b1` with
`git show 7234a9b1:docs/<path>`:

- `HELP_GETTING_STARTED.md`, `HELP_CAPABILITIES.md`, `HELP_SUPPORT.md`, `HELP_CHANGES.md`, and `HELP_TROUBLESHOOTING.md` → `HELP_CENTER.md`; `HELP_API_OVERVIEW.md` → `API_USER_GUIDE.md`.
- `LIBRARY_ASSIGNMENTS.md` and `PROJECT_SECRETS.md` → `ADMIN_USER_GUIDE.md`; `RUNTIME_CAPTURE_DIAGNOSTICS.md` → `SCRIPT_DEBUGGER_GUIDE.md`.
- `WEB_API_EXECUTION_SUPPORT_MATRIX.md`, `BROWSER_AND_MOBILE_SUPPORT.md`, `MESSAGING_SUPPORT.md`, `JAVA_VUSER_RUNTIME.md`, and `SAPGUI_RUNNER_SPEC.md` → `PROTOCOL_SUPPORT.md`; `NATIVE_ARCHIVE_COMPATIBILITY.md` → `IMPORT_FIDELITY_AND_REPAIRS.md`.
- `PROMETHEUS_METRICS.md`, `OPENTELEMETRY.md`, `POWERBI_INTEGRATION.md`, and `MONITORING_INTEGRATIONS.md` → `OBSERVABILITY_INTEGRATIONS.md`.
- `CLUSTER_CONNECTIONS.md`, `KUBERNETES_RESTART_CONTROLS.md`, and `MULTI_CLOUD_PAAS.md` → `CLUSTERS_AND_CLOUD.md`.
- `ENTERPRISE_DATA_PLATFORM_OPERATIONS_RUNBOOK.md`, `ENTERPRISE_PLATFORM_ARCHITECTURE_RUNBOOK.md`, and `SQLITE_TO_POSTGRES_MIGRATION_GUIDE.md` → `ENTERPRISE_PLATFORM_RUNBOOK.md`; `GITHUB_INTEGRATION.md` → `CICD_INTEGRATION.md`; `OPTIN_TEST_SUITES.md` → `qa/TEST_PROGRAM.md`.

## Removed 2026-09-24 (recoverable from git)

These documents were fully implemented, remediated or superseded and were deleted. Restore any of them with `git show <hash>:<path>`.

### Implemented plans (`plans/`)

Restore with `git show 587d279b8:docs/archive/plans/<file>`.

| File | Note | Current reference |
| --- | --- | --- |
| AUTH_RBAC_FOLLOW_UP_TICKETS.md | Workstream 4 auth/RBAC follow-up decisions; all tickets implemented | [AUTH_RBAC_OPERATIONS_RUNBOOK.md](../AUTH_RBAC_OPERATIONS_RUNBOOK.md) |
| DYNATRACE_SAAS_MONITORING_PLAN.md | Optional Dynatrace SaaS in-flight monitoring; implemented | [OBSERVABILITY_INTEGRATIONS.md](../OBSERVABILITY_INTEGRATIONS.md) |
| ENTERPRISE_DATA_PLATFORM_UPGRADE_PLAN.md | PostgreSQL, Prometheus, object storage and Redis upgrade; all phases shipped | [ENTERPRISE_PLATFORM_RUNBOOK.md](../ENTERPRISE_PLATFORM_RUNBOOK.md) |
| PROTOCOL_EXTENSION_PLAN.md | SOAP and Java-over-HTTP protocol support; implemented | [PROTOCOL_SUPPORT.md](../PROTOCOL_SUPPORT.md), [PROTOCOL_SUPPORT.md](../PROTOCOL_SUPPORT.md) |
| RUN_REPORT_IMPLEMENTATION_PLAN.md | Answer-first Run Report page for completed runs; `RunReportPage` and related components exist | [APPLICATION_USER_GUIDE.md](../APPLICATION_USER_GUIDE.md) |
| SCRIPT_EDITOR_INTERACTION_MODEL.md | Script editor decision memo (Author outline, Action editor, Source tab); shipped | [APPLICATION_USER_GUIDE.md](../APPLICATION_USER_GUIDE.md#authoring-a-script) |

### Per-audit READMEs and theme files (`qa-reviews/`)

Merged into [qa-reviews/AUDITS_2026-09.md](qa-reviews/AUDITS_2026-09.md); the originals are recoverable with `git show 587d279b8:docs/archive/qa-reviews/<file>`: `DASHBOARD_UX_REVIEWS_2026-09.md`, `import-reviews-complete-2026-09.md`, `ux-reviews-2026-09.md`, and `<folder>/README.md` for `access-audit-2026-09-12`, `command-center-audit-2026-09-12`, `dashboard-audit-2026-09-12`, `end-to-end-ux-2026-09-09`, `import-source-audit-2026-09-12`, `new-test-audit-2026-09-12`, `post-test-reporting-2026-09-08`, `results-repository-audit-2026-09-12`, `scenario-builder-evaluation-2026-09-12`, `script-scenario-audit-2026-09-12`, `ux-consistency-2026-09-08`.

### Reviews (`reviews/`)

| File | Note |
| --- | --- |
| WHOLE_APP_REVIEW_2026-08-02.md | Whole-application review (312 of 323 defects fixed at the time). Its 11 residual defects are tracked, with re-validation results, in [../qa/DEFECTS.md](../qa/DEFECTS.md). Restore with `git show 587d279b8:docs/archive/reviews/WHOLE_APP_REVIEW_2026-08-02.md` |
| README.md | Index of this folder (removed with it) |

The June and July 2026 reviews below were fully remediated or superseded and were deleted earlier the same day. Restore any of them with `git show 780f032b4:docs/archive/reviews/<file>` (handoff: `docs/archive/handoffs/<file>`).

| File | Note |
| --- | --- |
| CODE_REVIEW_REPORT.md | Full codebase review, 2026-06-15 |
| REFACTORING_OPPORTUNITIES_2026-06-27.md | Refactoring opportunities |
| TWO_WEEK_REVIEW_2026-06-17_to_2026-06-29.md, TWO_WEEK_REVIEW_REVERIFY_2026-07-02.md | Two-week review and re-verification |
| FLEET_MANAGEMENT_REVIEW.md | Fleet management review |
| WSDL_SOAP_IMPORT_REPLAY_REVIEW.md | WSDL/SOAP import and replay |
| RECORDING_IMPORT_REPLAY_REVIEW_2026-07-02.md, apex_load_recording_import_replay_review.md | Recording/import/replay reviews and memory |
| PERF_REVIEW_2026-07-06.md, PERF_BASELINES_2026-07-06.md | Performance review and baselines |
| WEEK_REVIEW_2026-07-02_to_2026-07-07.md | Week review |
| BRUNO_YAML_IMPORT_REVIEW_2026-07-16.md (+ _ROUND2) | Bruno YAML import review |
| GITHUB_ACTIONS_CI_SECOND_OPINION_2026-07-16.md | CI second opinion |
| UNCOMMITTED_REVIEW_2026-07-22.md | Uncommitted-changes review (all 48 findings fixed same day) |
| ../handoffs/SESSION_FEATURE_HANDOFF_APPLIED_2026-07-24.md | One-off session report |

### Reviews moved out of `docs/reviews/`

`docs/reviews/` was merged into [../qa/reviews/](../qa/reviews/README.md) on 2026-09-24: the Data Pools and LoadRunner runtime-settings evaluations are now in [../qa/reviews/evaluations-2026.md](../qa/reviews/evaluations-2026.md). Originals: `git show 587d279b8:docs/reviews/<file>`.

## Completed live-table plans

Archived 2026-10-19 after their October 12 completion statements. These preserve design rationale and exact qualification limits, including the restricted final integration checks; they are not new delivery gates or current operator instructions.

| Historical plan | Current guidance |
| --- | --- |
| [Run-scoped live-table plan](plans/LIVE_DATA_TABLES_REVISED_PLAN.md) | [Runtime](../LIVE_DATA_TABLES_RUNTIME.md), [VTS import boundary](../LIVE_DATA_TABLES_VTS_COMPATIBILITY.md) |
| [Native shared-data extension plan](plans/LIVE_DATA_TABLES_PROJECT_PLAN.md) | [Project tables](../PROJECT_LIVE_TABLES.md), [native services](../NATIVE_SCRIPT_DATA_SERVICES.md), [rendezvous](../DISTRIBUTED_RENDEZVOUS.md) |

## October 2026 consolidation

Nineteen completed scoped review narratives were merged without dropping their failed checks, residuals or acceptance limits. Their evidence folders remain under `docs/qa/reviews/` at the original paths; links in the reports resolve back to those files. Parent-feature qualification remains open wherever the records say so.

| Historical record | Contents |
| --- | --- |
| [Messaging reviews](qa-reviews/MESSAGING_2026-10.md) | Eight provider and processor slices |
| [Analytics reviews](qa-reviews/ANALYTICS_2026-10.md) | Four contract, partition and panel slices |
| [Authoring reviews](qa-reviews/AUTHORING_2026-10.md) | Goals persistence and revision safety |
| [Browser reviews](qa-reviews/BROWSER_2026-10.md) | Availability, document evidence and HAR compatibility |
| [Integration reviews](qa-reviews/INTEGRATION_2026-10.md) | Publication and composed qualification |
| [Roadmap snapshot](ROADMAP_2026-10-19.md) | Complete pre-cleanup delivery narrative and design history; the [current roadmap](../ROADMAP.md) carries remaining gates |

## Java validation history

| Historical record | Current references |
| --- | --- |
| [September Java verification](JAVA_VALIDATION_2026-09.md) | Dated integration checks, foundation verification and local Linux evidence; current [scope and release gates](../JAVA_VUSER_POOLING_IMPLEMENTATION.md), [pilot operation](../JAVA_PILOT_OPERATIONS.md), [pooled contracts](../JAVA_POOLED_RUNTIME_CONTRACTS.md) and [dedicated contracts](../JAVA_DEDICATED_EXECUTION_CONTRACTS.md) |

The October 19 analytics/Java pass moved technical sections intact. Review-derived Java constraints remain in the current pooled contract reference; only dated validation summaries were archived. Evidence artifacts remain at their original paths. Imported measurement identities are now part of [native analytics](../NATIVE_ANALYTICS.md#imported-measurement-identities).

## Delivery and continuity records (2026-10-19)

- [Feature completion delivery snapshot](plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md): full dated requirements, delivery narratives, deviations and evidence; maintained requirements remain in [the specification](../FEATURE_COMPLETION_SPEC.md).
- [HA continuity design and results](plans/HA_RUN_CONTINUITY_2026-10-19.md): original decisions, contracts and P0–P4 and later review evidence; current operations are in [the enterprise runbook](../ENTERPRISE_PLATFORM_RUNBOOK.md#ha-controller-behavior). Worker-loss qualification remains open.
