# Documentation Index

Status: Current

This is the canonical map for APEX Load documentation. Generated references are
the source of truth for API and DSL contracts; roadmap and historical files are
kept for planning or audit context only.

## Current Guides

| Document | Status | Purpose |
| --- | --- | --- |
| [HELP_API_OVERVIEW.md](HELP_API_OVERVIEW.md) | Current | In-app help: api overview |
| [HELP_CAPABILITIES.md](HELP_CAPABILITIES.md) | Current | In-app help: capabilities |
| [HELP_CHANGES.md](HELP_CHANGES.md) | Current | In-app help: changes |
| [HELP_DSL_OPERATOR_GUIDE.md](HELP_DSL_OPERATOR_GUIDE.md) | Current | DSL execution, examples, and migration guidance for LoadRunner and JMeter operators |
| [HELP_GETTING_STARTED.md](HELP_GETTING_STARTED.md) | Current | In-app help: getting started |
| [HELP_SUPPORT.md](HELP_SUPPORT.md) | Current | In-app help: support |
| [HELP_TROUBLESHOOTING.md](HELP_TROUBLESHOOTING.md) | Current | In-app help: troubleshooting |
| [README.md](../README.md) | Current | Project overview, quick start, and primary links |
| [ARCHITECTURE.md](../ARCHITECTURE.md) | Current | System architecture and runtime model |
| [DEVELOPMENT.md](../DEVELOPMENT.md) | Current | Developer setup, package guide, testing policy |
| [TROUBLESHOOTING.md](../TROUBLESHOOTING.md) | Current | Operational troubleshooting |
| [APPLICATION_USER_GUIDE.md](APPLICATION_USER_GUIDE.md) | Current | End-user guide to the dashboard application (rendered in the in-app Help area: User Guide) |
| [ADMIN_USER_GUIDE.md](ADMIN_USER_GUIDE.md) | Current | Administrator guide: users, RBAC, fleet, settings, governance (rendered in the in-app Help area: Administration, for administrators) |
| [API_USER_GUIDE.md](API_USER_GUIDE.md) | Current | API workflows for operators and automation |
| [SCRIPT_LIFECYCLE.md](SCRIPT_LIFECYCLE.md) | Current | Script source-to-run lifecycle |
| [BROWSER_AND_MOBILE_SUPPORT.md](BROWSER_AND_MOBILE_SUPPORT.md) | Current | Browser canaries, mobile emulation, client metrics, benchmark evidence |
| [MESSAGING_SUPPORT.md](MESSAGING_SUPPORT.md) | Current | JMS, Kafka, AsyncAPI, and Event Hubs support summary, JMS Solace sidecar bridge, and JMeter Kafka/Event Hubs import fidelity |
| [AUTH_RBAC_OPERATIONS_RUNBOOK.md](AUTH_RBAC_OPERATIONS_RUNBOOK.md) | Current | Auth/RBAC operations and open work / backlog |
| [GITHUB_ACTIONS_RELEASE_AND_DEPLOYMENT.md](GITHUB_ACTIONS_RELEASE_AND_DEPLOYMENT.md) | Current | Signed releases, protected deployments, rollback operations, GitHub Enterprise Server compatibility/mirroring/qualification, and the release evidence manifest |
| [ENTERPRISE_DATA_PLATFORM_OPERATIONS_RUNBOOK.md](ENTERPRISE_DATA_PLATFORM_OPERATIONS_RUNBOOK.md) | Current | Enterprise storage/cache/archive operations |
| [PROMETHEUS_METRICS.md](PROMETHEUS_METRICS.md) | Current | Prometheus scrape metrics |
| [MONITORING_INTEGRATIONS.md](MONITORING_INTEGRATIONS.md) | Current | Dynatrace SaaS monitoring, Dynatrace lifecycle webhooks, and Elastic (ELK) in-flight monitoring |
| [POWERBI_INTEGRATION.md](POWERBI_INTEGRATION.md) | Current | Publishing run results to a Power BI push dataset: setup, schema, dedupe, report layout |
| [OPENTELEMETRY.md](OPENTELEMETRY.md) | Current | Opt-in OTLP tracing, propagation, sampling, privacy, and deployment configuration |
| [PERFORMANCE_BENCHMARKS.md](PERFORMANCE_BENCHMARKS.md) | Current | Harness-overhead benchmark, versioned local performance evidence harness and target-scope caveats, browser benchmark artifact manifest, and the mobile browser benchmark runbook |
| [recording-agent.md](recording-agent.md) | Current | Local browser Recording Agent |
| [deployments/README.md](../deployments/README.md) | Current | Deployment chooser |

## Specialized Current References

These focused references are also current. They are kept separate from the primary-guide list because they describe a single runtime, importer, migration, or evidence surface.

| Document | Purpose |
| --- | --- |
| [IMPORT_FIDELITY_AND_REPAIRS.md](IMPORT_FIDELITY_AND_REPAIRS.md) | JMeter and LoadRunner import fidelity, finite iteration execution, native CSV exports, source-plan execution, protected guided repair, and regression coverage (status: current) |
| [NATIVE_ARCHIVE_COMPATIBILITY.md](NATIVE_ARCHIVE_COMPATIBILITY.md) | Native archive runtime-setting mappings, PAC proxy mode, and re-import behavior (includes review notes) |
| [LIBRARY_ASSIGNMENTS.md](LIBRARY_ASSIGNMENTS.md) | Assigning scripts and scenarios to applications and releases from the libraries |
| [JAVA_VUSER_RUNTIME.md](JAVA_VUSER_RUNTIME.md) | Governed Java Vuser runner contract and operations |
| [ACCESS_SURFACES.md](ACCESS_SURFACES.md) | Supported hosted, desktop, and CLI access surfaces |
| [AI_ASSISTED_WORKFLOWS.md](AI_ASSISTED_WORKFLOWS.md) | Governed assisted-authoring workflow and API contract |
| [CICD_INTEGRATION.md](CICD_INTEGRATION.md) | CI/CD gate integration patterns and examples |
| [CLUSTER_CONNECTIONS.md](CLUSTER_CONNECTIONS.md) | Registering external Kubernetes/AKS/EKS control planes and per-pool host resource allocation |
| [MULTI_CLOUD_PAAS.md](MULTI_CLOUD_PAAS.md) | Azure Container Apps/Jobs and AWS ECS Fargate compute registration, lifecycle, reverse connectivity, and production-evidence boundaries |
| [COMPETITIVE_COMPARISON.md](COMPETITIVE_COMPARISON.md) | Competitive positioning, evaluator summary/trust pack, REST API comparison, and migration reference |
| [ENTERPRISE_CONTRACTS.md](ENTERPRISE_CONTRACTS.md) | Enterprise authoring lifecycle, workload-shape/load-model, and project-first governance contracts |
| [ENTERPRISE_FLEET_AND_CAPACITY.md](ENTERPRISE_FLEET_AND_CAPACITY.md) | Generator fleet inventory, HPA/KEDA autoscaling signals, project and peak-based pod capacity reservations, and fail-closed run supervision |
| [ENTERPRISE_PLATFORM_ARCHITECTURE_RUNBOOK.md](ENTERPRISE_PLATFORM_ARCHITECTURE_RUNBOOK.md) | Production platform reference architecture |
| [GITHUB_INTEGRATION.md](GITHUB_INTEGRATION.md) | GitHub Enterprise authentication and script workflow |
| [KUBERNETES_RESTART_CONTROLS.md](KUBERNETES_RESTART_CONTROLS.md) | Governed controller and generator restart controls |
| [WEB_API_EXECUTION_SUPPORT_MATRIX.md](WEB_API_EXECUTION_SUPPORT_MATRIX.md) | Executable, bounded, and review-only Web/API support dispositions |
| [SQLITE_TO_POSTGRES_MIGRATION_GUIDE.md](SQLITE_TO_POSTGRES_MIGRATION_GUIDE.md) | SQLite-to-PostgreSQL and HA cutover procedure |
| [SAPGUI_RUNNER_SPEC.md](SAPGUI_RUNNER_SPEC.md) | SAP GUI external-runner contract |
| [OPTIN_TEST_SUITES.md](OPTIN_TEST_SUITES.md) | Machine-enforced inventory of build-tag and environment-gated test suites and how to run them |
| [PROJECT_SECRETS.md](PROJECT_SECRETS.md) | Project secret providers, API behavior, and operational guidance |
| [RUNTIME_SETTINGS_PARITY.md](RUNTIME_SETTINGS_PARITY.md) | Runtime-settings parity evidence and API contract |
| [RUNTIME_CAPTURE_DIAGNOSTICS.md](RUNTIME_CAPTURE_DIAGNOSTICS.md) | Imported setting reasons and bounded HTTP/resource capture |
| [SCRIPT_DEBUGGER_GUIDE.md](SCRIPT_DEBUGGER_GUIDE.md) | Script replay debugger workflow and routes |
| [LOADRUNNER_WEB_FUNCTION_COVERAGE.md](LOADRUNNER_WEB_FUNCTION_COVERAGE.md) | Generated LoadRunner Web conversion coverage |
| [LOADRUNNER_JAVA_LR_FUNCTION_COVERAGE.md](LOADRUNNER_JAVA_LR_FUNCTION_COVERAGE.md) | Generated Java LR function coverage |
| [LOADRUNNER_JAVA_VUSER_IMPORT_COVERAGE.md](LOADRUNNER_JAVA_VUSER_IMPORT_COVERAGE.md) | Generated Java Vuser import coverage |
| [LOADRUNNER_TRUCLIENT_STEP_COVERAGE.md](LOADRUNNER_TRUCLIENT_STEP_COVERAGE.md) | Generated TruClient step coverage |

Every Markdown file directly under `docs/`, other than this index itself, appears in one of the tables on this page — current guide, specialized reference, generated artifact, or roadmap/planning material. That is machine-enforced: `check_index_membership` in `scripts/check-docs.py` fails the CI documentation job if a `docs/*.md` file is not linked from a table row here, so adding a document means adding a row — a mention in prose does not satisfy the check. Listing is not itself a claim that a file is a current product contract; the Status column says that. Point-in-time material under `docs/reviews/` and `docs/archive/` is excluded from both this index and the check.

## Generated References

| Document | Status | Source |
| --- | --- | --- |
| [swagger.yaml](swagger.yaml) | Generated | `go generate ./pkg/api` |
| [api-endpoint-catalog.json](api-endpoint-catalog.json) | Generated | `go generate ./pkg/api` |
| [dsl.schema.json](dsl.schema.json) | Generated | `go generate ./pkg/dsl` |
| [DSL_ACTION_REFERENCE.md](DSL_ACTION_REFERENCE.md) | Generated | `go generate ./pkg/dsl` |

## Roadmap And Planning

These files contain tactical plans, future work, or parity analysis. They are
not the source of truth for current API or DSL contracts.

| Document | Status |
| --- | --- |
| [BROWSER_ROADMAP.md](BROWSER_ROADMAP.md) | Roadmap/detail |
| [MESSAGING_PLAN.md](MESSAGING_PLAN.md) | Roadmap/detail: messaging open work (IBM MQ, Solace SMF, Event Hubs deployment), landed-work record, and Kafka/AsyncAPI design record |
| [ROADMAP.md](ROADMAP.md) | Roadmap: runtime telemetry SLA, protocol telemetry parity, script authoring consolidation, proposed PT Analysis Parquet import, CloudTest program status |
| [CLOUDTEST_PARITY_PROGRAM.md](CLOUDTEST_PARITY_PROGRAM.md) | CloudTest outcome-parity evidence ledger, implementation sequencing, and migration gates |
| [qa/TEST_PROGRAM.md](qa/TEST_PROGRAM.md) | Nine-month progressive test and infrastructure-readiness program; scope of record, live risk register and release test checklist |
| [qa/STATUS.md](qa/STATUS.md) | Live milestone state and headline metrics for the test program (tracker-enforced) |
| [qa/E2E_SUITES.md](qa/E2E_SUITES.md) | Browser E2E suites: the twelve-step Tier-0 journey and assertion matrix, workflow regression suite, and recording/replay suite |
| [qa/DEFECTS.md](qa/DEFECTS.md) | Rolling defect log, including the eleven defects left open by the 2026-08-02 whole-application review |
| [qa/FLAKE_LOG.md](qa/FLAKE_LOG.md) | Quarantined non-deterministic tests, root causes, and un-quarantine dates |

## Historical

Historical material is preserved for audit context, is excluded from current-document freshness checks, and should not be treated as current product documentation.

| Location | Contents |
| --- | --- |
| [reviews/README.md](reviews/README.md) | Point-in-time reviews still referenced (status table with live trackers) |
| [archive/reviews/README.md](archive/reviews/README.md) | Whole-application review 2026-08-02 (older June/July reviews were removed; see README) |
| [archive/README.md](archive/README.md) | Archived implemented plans (`archive/plans/`), QA reviews (`archive/qa-reviews/`) and the retired DSL guide |
| [qa/reviews/README.md](qa/reviews/README.md) | Index of dated UX/QA review reports still carrying open items, with captured evidence (completed reviews are in `archive/qa-reviews/`) |
