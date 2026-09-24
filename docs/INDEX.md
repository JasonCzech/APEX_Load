# Documentation Index

Status: Current

This is the canonical map for APEX Load documentation. Generated references are
the source of truth for API and DSL contracts; roadmap and historical files are
kept for planning or audit context only.

## Current Guides

| Document | Status | Purpose |
| --- | --- | --- |
| [HELP_CENTER.md](HELP_CENTER.md#build-and-run-your-first-test) | Current | In-app help: first test, capabilities, support, changes, and troubleshooting |
| [HELP_DSL_OPERATOR_GUIDE.md](HELP_DSL_OPERATOR_GUIDE.md) | Current | DSL execution, examples, and migration guidance for LoadRunner and JMeter operators |
| [README.md](../README.md) | Current | Project overview, quick start, and primary links |
| [ARCHITECTURE.md](../ARCHITECTURE.md) | Current | System architecture and runtime model |
| [DEVELOPMENT.md](../DEVELOPMENT.md) | Current | Developer setup, package guide, testing policy |
| [TROUBLESHOOTING.md](../TROUBLESHOOTING.md) | Current | Developer and operational troubleshooting |
| [APPLICATION_USER_GUIDE.md](APPLICATION_USER_GUIDE.md) | Current | End-user guide to the dashboard application |
| [ADMIN_USER_GUIDE.md](ADMIN_USER_GUIDE.md) | Current | Administrator tasks, library assignments, and project secrets |
| [API_USER_GUIDE.md](API_USER_GUIDE.md) | Current | API orientation, authentication, workflows, and automation |
| [SCRIPT_LIFECYCLE.md](SCRIPT_LIFECYCLE.md) | Current | Script source-to-run lifecycle |
| [PROTOCOL_SUPPORT.md](PROTOCOL_SUPPORT.md) | Current | Web/API, browser, messaging, Java Vuser, and SAP GUI support boundaries |
| [AUTH_RBAC_OPERATIONS_RUNBOOK.md](AUTH_RBAC_OPERATIONS_RUNBOOK.md) | Current | Auth/RBAC operations and open work / backlog |
| [GITHUB_ACTIONS_RELEASE_AND_DEPLOYMENT.md](GITHUB_ACTIONS_RELEASE_AND_DEPLOYMENT.md) | Current | Signed releases, protected deployments, rollback operations, and release evidence |
| [ENTERPRISE_PLATFORM_RUNBOOK.md](ENTERPRISE_PLATFORM_RUNBOOK.md) | Current | Enterprise platform architecture, data platform operations, and database migration |
| [OBSERVABILITY_INTEGRATIONS.md](OBSERVABILITY_INTEGRATIONS.md) | Current | Prometheus, OpenTelemetry, Power BI, Dynatrace, and Elastic observability integrations |
| [CLUSTERS_AND_CLOUD.md](CLUSTERS_AND_CLOUD.md) | Current | Kubernetes connections and restart controls, plus Azure/AWS PaaS compute |
| [PERFORMANCE_BENCHMARKS.md](PERFORMANCE_BENCHMARKS.md) | Current | Harness-overhead benchmark, local performance evidence, and browser benchmark procedures |
| [recording-agent.md](recording-agent.md) | Current | Local browser Recording Agent |
| [deployments/README.md](../deployments/README.md) | Current | Deployment chooser |

## Specialized Current References

These focused references describe a single runtime, importer, migration, or evidence surface.

| Document | Purpose |
| --- | --- |
| [IMPORT_FIDELITY_AND_REPAIRS.md](IMPORT_FIDELITY_AND_REPAIRS.md) | JMeter and LoadRunner import fidelity, source-plan execution, guided repair, native archive compatibility, and regression coverage |
| [SCRIPT_DEBUGGER_GUIDE.md](SCRIPT_DEBUGGER_GUIDE.md) | Script replay debugger workflow, routes, and imported runtime-setting/capture diagnostics |
| [ACCESS_SURFACES.md](ACCESS_SURFACES.md) | Supported hosted, desktop, and CLI access surfaces |
| [AI_ASSISTED_WORKFLOWS.md](AI_ASSISTED_WORKFLOWS.md) | Governed assisted-authoring workflow and API contract |
| [CICD_INTEGRATION.md](CICD_INTEGRATION.md) | CI/CD performance gates, GitHub Enterprise integration, and pipeline examples |
| [COMPETITIVE_COMPARISON.md](COMPETITIVE_COMPARISON.md) | Competitive positioning, evaluator trust pack, REST API comparison, and migration reference |
| [ENTERPRISE_CONTRACTS.md](ENTERPRISE_CONTRACTS.md) | Enterprise authoring lifecycle, workload-shape/load-model, and project-first governance contracts |
| [ENTERPRISE_FLEET_AND_CAPACITY.md](ENTERPRISE_FLEET_AND_CAPACITY.md) | Generator fleet inventory, autoscaling, capacity reservations, and run supervision |
| [qa/TEST_PROGRAM.md](qa/TEST_PROGRAM.md) | Progressive test and infrastructure-readiness program, including the machine-checked opt-in suite inventory |
| [RUNTIME_SETTINGS_PARITY.md](RUNTIME_SETTINGS_PARITY.md) | Runtime-settings parity evidence and API contract |
| [LOADRUNNER_COVERAGE.md](LOADRUNNER_COVERAGE.md) | Generated LoadRunner coverage: Web functions, Java `lr` functions, TruClient steps, and Java Vuser import surfaces |

Every Markdown file directly under `docs/`, other than this index itself, appears in one of the tables on this page — current guide, specialized reference, generated artifact, or roadmap/planning material. That is machine-enforced: `check_index_membership` in `scripts/check-docs.py` fails the CI documentation job if a `docs/*.md` file is not linked from a table row here. Point-in-time material under `docs/qa/reviews/` and `docs/archive/` is excluded from this index and the check.

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
| [ROADMAP.md](ROADMAP.md) | Roadmap: runtime telemetry SLA, protocol telemetry parity, script authoring consolidation, proposed PT Analysis Parquet import, messaging open work and design record, browser/mobile roadmap, CloudTest outcome-parity ledger and phases |
| [qa/TEST_PROGRAM.md](qa/TEST_PROGRAM.md) | Nine-month progressive test and infrastructure-readiness program; scope of record, live risk register and release test checklist |
| [qa/STATUS.md](qa/STATUS.md) | Live milestone state and headline metrics for the test program (tracker-enforced) |
| [qa/E2E_SUITES.md](qa/E2E_SUITES.md) | Browser E2E suites: the twelve-step Tier-0 journey and assertion matrix, workflow regression suite, and recording/replay suite |
| [qa/DEFECTS.md](qa/DEFECTS.md) | Rolling defect log, including the eleven defects left open by the 2026-08-02 whole-application review |
| [qa/FLAKE_LOG.md](qa/FLAKE_LOG.md) | Quarantined non-deterministic tests, root causes, and un-quarantine dates |

## Historical

Historical material is preserved for audit context, is excluded from current-document freshness checks, and should not be treated as current product documentation.

| Location | Contents |
| --- | --- |
| [archive/README.md](archive/README.md) | Archive map: consolidated September 2026 QA/UX audits (`archive/qa-reviews/AUDITS_2026-09.md`), the retired DSL guide, and the list of removed plans and reviews with `git show` restore commands |
| [qa/reviews/README.md](qa/reviews/README.md) | Single index of point-in-time reviews and evaluations (open vs complete), including the Data Pools and LoadRunner runtime-settings evaluations, with captured evidence; points to `archive/README.md` for completed reviews |
