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
| [JMETER_IMPORT_FIDELITY.md](JMETER_IMPORT_FIDELITY.md) | Current | JMeter review repairs, finite iteration execution, native CSV exports, and regression coverage |
| [HELP_GETTING_STARTED.md](HELP_GETTING_STARTED.md) | Current | In-app help: getting started |
| [HELP_SUPPORT.md](HELP_SUPPORT.md) | Current | In-app help: support |
| [HELP_TROUBLESHOOTING.md](HELP_TROUBLESHOOTING.md) | Current | In-app help: troubleshooting |
| [README.md](../README.md) | Current | Project overview, quick start, and primary links |
| [ARCHITECTURE.md](../ARCHITECTURE.md) | Current | System architecture and runtime model |
| [DEVELOPMENT.md](../DEVELOPMENT.md) | Current | Developer setup, package guide, testing policy |
| [QUICK_REFERENCE.md](../QUICK_REFERENCE.md) | Current | Commands, DSL examples, common fixes |
| [TROUBLESHOOTING.md](../TROUBLESHOOTING.md) | Current | Operational troubleshooting |
| [APPLICATION_USER_GUIDE.md](APPLICATION_USER_GUIDE.md) | Current | End-user guide to the dashboard application (rendered in-app as API Guide → User Guide) |
| [ADMIN_USER_GUIDE.md](ADMIN_USER_GUIDE.md) | Current | Administrator guide: users, RBAC, fleet, settings, governance (rendered in-app as API Guide → Admin Guide) |
| [API_USER_GUIDE.md](API_USER_GUIDE.md) | Current | API workflows for operators and automation |
| [SCRIPT_LIFECYCLE.md](SCRIPT_LIFECYCLE.md) | Current | Script source-to-run lifecycle |
| [BROWSER_AND_MOBILE_SUPPORT.md](BROWSER_AND_MOBILE_SUPPORT.md) | Current | Browser canaries, mobile emulation, client metrics, benchmark evidence |
| [MESSAGING_SUPPORT.md](MESSAGING_SUPPORT.md) | Current | JMS, Kafka, AsyncAPI, and Event Hubs support summary |
| [AUTH_RBAC_OPERATIONS_RUNBOOK.md](AUTH_RBAC_OPERATIONS_RUNBOOK.md) | Current | Auth/RBAC operations |
| [GITHUB_ACTIONS_RELEASE_AND_DEPLOYMENT.md](GITHUB_ACTIONS_RELEASE_AND_DEPLOYMENT.md) | Current | Signed releases, protected deployments, and rollback operations |
| [GHES_GITHUB_ACTIONS_COMPATIBILITY.md](GHES_GITHUB_ACTIONS_COMPATIBILITY.md) | Current | GHES capability matrix, mirroring, trust, and qualification |
| [GITHUB_ACTIONS_CUTOVER.md](GITHUB_ACTIONS_CUTOVER.md) | Current | GitHub Actions enforcement and Jenkins retirement sequence |
| [ENTERPRISE_DATA_PLATFORM_OPERATIONS_RUNBOOK.md](ENTERPRISE_DATA_PLATFORM_OPERATIONS_RUNBOOK.md) | Current | Enterprise storage/cache/archive operations |
| [PROMETHEUS_METRICS.md](PROMETHEUS_METRICS.md) | Current | Prometheus scrape metrics |
| [DYNATRACE_WEBHOOKS.md](DYNATRACE_WEBHOOKS.md) | Current | Dynatrace event webhook setup |
| [POWERBI_INTEGRATION.md](POWERBI_INTEGRATION.md) | Current | Publishing run results to a Power BI push dataset: setup, schema, dedupe, report layout |
| [OPENTELEMETRY.md](OPENTELEMETRY.md) | Current | Opt-in OTLP tracing, propagation, sampling, privacy, and deployment configuration |
| [PERFORMANCE_BENCHMARK_EVIDENCE.md](PERFORMANCE_BENCHMARK_EVIDENCE.md) | Current | Versioned local performance evidence harness, target-scope caveats, and publication workflow |
| [recording-agent.md](recording-agent.md) | Current | Local browser Recording Agent |
| [deployments/README.md](../deployments/README.md) | Current | Deployment chooser |

## Specialized Current References

These focused references are also current. They are kept separate from the primary-guide list because they describe a single runtime, importer, migration, or evidence surface.

| Document | Purpose |
| --- | --- |
| [JAVA_VUSER_RUNTIME.md](JAVA_VUSER_RUNTIME.md) | Governed Java Vuser runner contract and operations |
| [JMS_SIDECAR.md](JMS_SIDECAR.md) | JMS Solace external sidecar bridge: operator environment, transports, and containment |
| [ACCESS_SURFACES.md](ACCESS_SURFACES.md) | Supported hosted, desktop, and CLI access surfaces |
| [AI_ASSISTED_WORKFLOWS.md](AI_ASSISTED_WORKFLOWS.md) | Governed assisted-authoring workflow and API contract |
| [AKS_INTEGRATION_READINESS.md](AKS_INTEGRATION_READINESS.md) | Current AKS capability and production-readiness boundaries |
| [EKS_INTEGRATION_READINESS.md](EKS_INTEGRATION_READINESS.md) | Current Amazon EKS capability and production-readiness boundaries |
| [APEX_LOAD_REST_API_COMPARISON.md](APEX_LOAD_REST_API_COMPARISON.md) | API-layer comparison with LoadRunner Enterprise |
| [BENCHMARK_HARNESS_OVERHEAD.md](BENCHMARK_HARNESS_OVERHEAD.md) | Reproducible metrics hot-path benchmark artifact |
| [BROWSER_BENCHMARK_ARTIFACTS.md](BROWSER_BENCHMARK_ARTIFACTS.md) | Browser benchmark artifact manifest and evidence contract |
| [CICD_INTEGRATION.md](CICD_INTEGRATION.md) | CI/CD gate integration patterns and examples |
| [CLUSTER_CONNECTIONS.md](CLUSTER_CONNECTIONS.md) | Registering external Kubernetes/AKS/EKS control planes and per-pool host resource allocation |
| [POD_CAPACITY_RESERVATIONS.md](POD_CAPACITY_RESERVATIONS.md) | Peak-based shared and dedicated pod reservations, rollout, and recovery |
| [MULTI_CLOUD_PAAS.md](MULTI_CLOUD_PAAS.md) | Azure Container Apps/Jobs and AWS ECS Fargate compute registration, lifecycle, reverse connectivity, and production-evidence boundaries |
| [COMPETITIVE_COMPARISON.md](COMPETITIVE_COMPARISON.md) | Competitive positioning and migration reference |
| [ENTERPRISE_AUTHORING_CONTRACT.md](ENTERPRISE_AUTHORING_CONTRACT.md) | Shared enterprise authoring lifecycle contract |
| [ENTERPRISE_AUTOSCALING_SIGNALS.md](ENTERPRISE_AUTOSCALING_SIGNALS.md) | HPA/KEDA signal contract for generator scaling |
| [ENTERPRISE_GENERATOR_FLEET_MODEL.md](ENTERPRISE_GENERATOR_FLEET_MODEL.md) | Generator fleet inventory and allocation contract |
| [GENERATOR_RUN_SUPERVISION.md](GENERATOR_RUN_SUPERVISION.md) | Fail-closed generator ownership leases, rollout order, observability, and recovery drills |
| [ENTERPRISE_GOVERNANCE_MODEL.md](ENTERPRISE_GOVERNANCE_MODEL.md) | Project-first ownership and governance model |
| [ENTERPRISE_LOAD_MODEL_CONTRACT.md](ENTERPRISE_LOAD_MODEL_CONTRACT.md) | Workload-shape support and authoring contract |
| [ENTERPRISE_PLATFORM_ARCHITECTURE_RUNBOOK.md](ENTERPRISE_PLATFORM_ARCHITECTURE_RUNBOOK.md) | Production platform reference architecture |
| [EVALUATOR_COMPETITIVE_ONE_PAGER.md](EVALUATOR_COMPETITIVE_ONE_PAGER.md) | Concise evaluator positioning and supported-scope summary |
| [EVALUATOR_TRUST_PACK.md](EVALUATOR_TRUST_PACK.md) | Reproducible evaluator evidence workflow |
| [GITHUB_INTEGRATION.md](GITHUB_INTEGRATION.md) | GitHub Enterprise authentication and script workflow |
| [KUBERNETES_RESTART_CONTROLS.md](KUBERNETES_RESTART_CONTROLS.md) | Governed controller and generator restart controls |
| [WEB_API_EXECUTION_SUPPORT_MATRIX.md](WEB_API_EXECUTION_SUPPORT_MATRIX.md) | Executable, bounded, and review-only Web/API support dispositions |
| [SQLITE_TO_POSTGRES_MIGRATION_GUIDE.md](SQLITE_TO_POSTGRES_MIGRATION_GUIDE.md) | SQLite-to-PostgreSQL and HA cutover procedure |
| [SAPGUI_RUNNER_SPEC.md](SAPGUI_RUNNER_SPEC.md) | SAP GUI external-runner contract |
| [MOBILE_BROWSER_BENCHMARK_RUNBOOK.md](MOBILE_BROWSER_BENCHMARK_RUNBOOK.md) | Repeatable mobile browser evidence collection |
| [OPTIN_TEST_SUITES.md](OPTIN_TEST_SUITES.md) | Machine-enforced inventory of build-tag and environment-gated test suites and how to run them |
| [PROJECT_SECRETS.md](PROJECT_SECRETS.md) | Project secret providers, API behavior, and operational guidance |
| [RELEASE_EVIDENCE_MANIFEST.md](RELEASE_EVIDENCE_MANIFEST.md) | Required evidence behind release and parity claims |
| [RUNTIME_SETTINGS_PARITY.md](RUNTIME_SETTINGS_PARITY.md) | Runtime-settings parity evidence and API contract |
| [RUNTIME_CAPTURE_DIAGNOSTICS.md](RUNTIME_CAPTURE_DIAGNOSTICS.md) | Imported setting reasons and bounded HTTP/resource capture |
| [SCHEDULING_TIMEZONE_BEHAVIOR_CHANGE.md](SCHEDULING_TIMEZONE_BEHAVIOR_CHANGE.md) | Cron timezone and DST behavior migration note |
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
| [API_AND_DSL_REFERENCE.md](../API_AND_DSL_REFERENCE.md) | Current | Compatibility landing page for generated references |

## Roadmap And Planning

These files contain tactical plans, future work, or parity analysis. They are
not the source of truth for current API or DSL contracts.

| Document | Status |
| --- | --- |
| [BROWSER_CLIENT_METRICS_PLAN.md](BROWSER_CLIENT_METRICS_PLAN.md) | Roadmap/detail |
| [MOBILE_BROWSER_EMULATION_PLAN.md](MOBILE_BROWSER_EMULATION_PLAN.md) | Roadmap/detail |
| [MOBILE_UX_PLATFORM_STRATEGY.md](MOBILE_UX_PLATFORM_STRATEGY.md) | Roadmap/detail |
| [BROWSER_REPLAY_HAR_CAPTURE_PLAN.md](BROWSER_REPLAY_HAR_CAPTURE_PLAN.md) | Roadmap/detail |
| [KAFKA_MESSAGING_AND_ASYNCAPI_PLAN.md](KAFKA_MESSAGING_AND_ASYNCAPI_PLAN.md) | Roadmap/detail |
| [MESSAGING_COVERAGE_AND_PLAN.md](MESSAGING_COVERAGE_AND_PLAN.md) | Roadmap/detail |
| [PROTOCOL_EXTENSION_PLAN.md](PROTOCOL_EXTENSION_PLAN.md) | Roadmap/detail |
| [PROTOCOL_TELEMETRY_PARITY_PLAN.md](PROTOCOL_TELEMETRY_PARITY_PLAN.md) | Roadmap/detail |
| [AUTH_RBAC_BACKLOG.md](AUTH_RBAC_BACKLOG.md) | Roadmap/detail |
| [AUTH_RBAC_FOLLOW_UP_TICKETS.md](AUTH_RBAC_FOLLOW_UP_TICKETS.md) | Roadmap/detail |
| [ENTERPRISE_DATA_PLATFORM_UPGRADE_PLAN.md](ENTERPRISE_DATA_PLATFORM_UPGRADE_PLAN.md) | Roadmap/detail |
| [SCRIPT_AUTHORING_CONSOLIDATION_ROADMAP.md](SCRIPT_AUTHORING_CONSOLIDATION_ROADMAP.md) | Roadmap/detail |
| [RUNTIME_TELEMETRY_SLA_PLAN.md](RUNTIME_TELEMETRY_SLA_PLAN.md) | Roadmap/detail |
| [RUN_REPORT_IMPLEMENTATION_PLAN.md](RUN_REPORT_IMPLEMENTATION_PLAN.md) | Proposed implementation plan |
| [SCRIPT_EDITOR_INTERACTION_MODEL.md](SCRIPT_EDITOR_INTERACTION_MODEL.md) | Approved design direction and phased plan |
| [CLOUDTEST_PARITY_PROGRAM.md](CLOUDTEST_PARITY_PROGRAM.md) | CloudTest outcome-parity evidence ledger, implementation sequencing, and migration gates |
| [qa/TEST_PROGRAM.md](qa/TEST_PROGRAM.md) | Nine-month progressive test and infrastructure-readiness program; scope of record |
| [qa/STATUS.md](qa/STATUS.md) | Live milestone state and headline metrics for the test program (tracker-enforced) |
| [qa/TIER0_JOURNEY.md](qa/TIER0_JOURNEY.md) | The twelve-step end-to-end user journey the M1 Playwright suite implements |
| [qa/DEFECTS.md](qa/DEFECTS.md) | Rolling defect log, including the eleven defects left open by the 2026-08-02 whole-application review |
| [qa/RISK_REGISTER.md](qa/RISK_REGISTER.md) | Test-program risks, re-scored at each milestone close |
| [qa/FLAKE_LOG.md](qa/FLAKE_LOG.md) | Quarantined non-deterministic tests, root causes, and un-quarantine dates |

## Historical

Point-in-time reviews live under `docs/reviews/`; older reviews are archived under
[archive/reviews](archive/reviews/README.md). Both locations are preserved for
audit context and are excluded from current-document freshness checks. They
should not be treated as current product documentation.
