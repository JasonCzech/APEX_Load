# Documentation Index

Status: Current

This is the canonical map for APEX Load documentation. Generated references are
the source of truth for API and DSL contracts; roadmap and historical files are
kept for planning or audit context only.

## User and operator guides

| Document | Status | Purpose |
| --- | --- | --- |
| [HELP_CENTER.md](HELP_CENTER.md#build-and-run-your-first-test) | Current | In-app help: first test, capabilities, support, changes, and troubleshooting |
| [README.md](../README.md) | Current | Project overview, quick start, and primary links |
| [APPLICATION_USER_GUIDE.md](APPLICATION_USER_GUIDE.md) | Current | End-user guide to the dashboard application |
| [BASELINE_COMPARISON.md](BASELINE_COMPARISON.md) | Current | Pinned baseline selection, matched-load evidence, advisory policies and saved comparison exports |
| [ADMIN_USER_GUIDE.md](ADMIN_USER_GUIDE.md) | Current | Administrator tasks, library assignments, and project secrets |
| [API_USER_GUIDE.md](API_USER_GUIDE.md) | Current | API orientation, authentication, workflows, and automation |
| [HELP_DSL_OPERATOR_GUIDE.md](HELP_DSL_OPERATOR_GUIDE.md) | Current | DSL execution, examples, and migration guidance for LoadRunner and JMeter operators |
| [recording-agent.md](recording-agent.md) | Current | Local browser Recording Agent |
| [deployments/README.md](../deployments/README.md) | Current | Deployment chooser |

## Operations and deployment

| Document | Purpose |
| --- | --- |
| [AUTH_RBAC_OPERATIONS_RUNBOOK.md](AUTH_RBAC_OPERATIONS_RUNBOOK.md) | Auth/RBAC operations, break-glass recovery, service accounts, and audit evidence |
| [CICD_INTEGRATION.md](CICD_INTEGRATION.md) | CI/CD performance gates, GitHub Enterprise integration, and pipeline examples |
| [GITHUB_ACTIONS_RELEASE_AND_DEPLOYMENT.md](GITHUB_ACTIONS_RELEASE_AND_DEPLOYMENT.md) | Signed releases, protected deployments, rollback operations, and release evidence |
| [CLUSTERS_AND_CLOUD.md](CLUSTERS_AND_CLOUD.md) | Kubernetes connections and restart controls, plus Azure/AWS PaaS compute |
| [ENTERPRISE_PLATFORM_RUNBOOK.md](ENTERPRISE_PLATFORM_RUNBOOK.md) | Enterprise platform architecture, data platform operations, and database migration |
| [OBSERVABILITY_INTEGRATIONS.md](OBSERVABILITY_INTEGRATIONS.md) | Prometheus, OpenTelemetry, Power BI, Dynatrace, and Elastic integrations |
| [ENTERPRISE_FLEET_AND_CAPACITY.md](ENTERPRISE_FLEET_AND_CAPACITY.md) | Generator fleet inventory, autoscaling, capacity reservations, and run supervision |

## Engineering and product references

| Document | Purpose |
| --- | --- |
| [ARCHITECTURE.md](../ARCHITECTURE.md) | System architecture and runtime model |
| [DEVELOPMENT.md](../DEVELOPMENT.md) | Developer setup, package guide, and testing policy |
| [TROUBLESHOOTING.md](../TROUBLESHOOTING.md) | Developer and operational troubleshooting |
| [SCRIPT_LIFECYCLE.md](SCRIPT_LIFECYCLE.md) | Script source-to-run lifecycle |
| [SCRIPT_DEBUGGER_GUIDE.md](SCRIPT_DEBUGGER_GUIDE.md) | Script replay debugger workflow, routes, and imported runtime-setting/capture diagnostics |
| [PROTOCOL_SUPPORT.md](PROTOCOL_SUPPORT.md) | Web/API, browser, messaging, Java Vuser, and SAP GUI support boundaries |
| [PROTOCOL_DIAGNOSTICS.md](PROTOCOL_DIAGNOSTICS.md) | gRPC measurement semantics, availability, distributed summaries, and archive compatibility |
| [IMPORT_FIDELITY_AND_REPAIRS.md](IMPORT_FIDELITY_AND_REPAIRS.md) | JMeter and LoadRunner import fidelity, source-plan execution, guided repair, native archive compatibility, and regression coverage |
| [RUNTIME_SETTINGS_PARITY.md](RUNTIME_SETTINGS_PARITY.md) | Runtime-settings parity evidence and API contract |
| [PERFORMANCE_BENCHMARKS.md](PERFORMANCE_BENCHMARKS.md) | Harness-overhead benchmark, local performance evidence, and browser benchmark procedures |
| [JAVA_VUSER_POOLING_IMPLEMENTATION.md](JAVA_VUSER_POOLING_IMPLEMENTATION.md) | Experimental Java evidence tooling, operator AppCDS, slot foundations, and remaining pooling/AKS gates |
| [JAVA_BOUNDARY_ATTESTOR_CONTRACT.md](JAVA_BOUNDARY_ATTESTOR_CONTRACT.md) | Authenticated external runtime and infrastructure-fencing proof contract for dedicated Java cleanup |
| [JAVA_BROKER_SERVICE.md](JAVA_BROKER_SERVICE.md) | Experimental dedicated Java broker command, operator profiles, host credentials and authenticated preparation API |
| [ACCESS_SURFACES.md](ACCESS_SURFACES.md) | Supported hosted, desktop, and CLI access surfaces |
| [AI_ASSISTED_WORKFLOWS.md](AI_ASSISTED_WORKFLOWS.md) | Governed assisted-authoring workflow and API contract |
| [COMPETITIVE_COMPARISON.md](COMPETITIVE_COMPARISON.md) | Competitive positioning, evaluator trust pack, REST API comparison, and migration reference |
| [ENTERPRISE_CONTRACTS.md](ENTERPRISE_CONTRACTS.md) | Enterprise authoring lifecycle, workload shape/load model, and project-first governance contracts |
| [HA_RUN_CONTINUITY_PLAN.md](HA_RUN_CONTINUITY_PLAN.md) | Plan and P0 evidence for keeping in-flight distributed runs alive across HA controller failover |

Every Markdown file directly under `docs/`, other than this index itself, appears in one of the audience or lifecycle tables on this page. That is machine-enforced: `check_index_membership` in `scripts/check-docs.py` fails the CI documentation job if a `docs/*.md` file is not linked from a table row here. Point-in-time material under `docs/qa/reviews/` and `docs/archive/` is excluded from this index and the check.

## Generated References

| Document | Status | Source |
| --- | --- | --- |
| [swagger.yaml](swagger.yaml) | Generated | `go generate ./pkg/api` |
| [api-endpoint-catalog.json](api-endpoint-catalog.json) | Generated | `go generate ./pkg/api` |
| [dsl.schema.json](dsl.schema.json) | Generated | `go generate ./pkg/dsl` |
| [DSL_ACTION_REFERENCE.md](DSL_ACTION_REFERENCE.md) | Generated | `go generate ./pkg/dsl` |
| [LOADRUNNER_COVERAGE.md](LOADRUNNER_COVERAGE.md) | Generated | `go generate ./pkg/convert/enterprisearchive` |

## Roadmap and QA program

These files contain tactical plans, future work, or parity analysis. They are
not the source of truth for current API or DSL contracts.

| Document | Status |
| --- | --- |
| [NODE_FAILURE_READINESS_PLAN.md](NODE_FAILURE_READINESS_PLAN.md) | Proposed implementation and qualification plan for worker recovery, KEDA capacity, liveness, storage, and archive issues found in September 2026 fault tests |
| [NODE_STORAGE_RECOVERY.md](NODE_STORAGE_RECOVERY.md) | Worker state inventory, fencing, storage recovery, and qualification limits |
| [ARCHIVE_SOURCE_RETENTION.md](ARCHIVE_SOURCE_RETENTION.md) | Opt-in confirmed-source retention, publication fences and recoverability limits |
| [ANALYSIS_WORKFLOWS.md](ANALYSIS_WORKFLOWS.md) | Project-gated run monitoring, traffic-free preparation, native Parquet imports, saved mappings and correlation; validation limits |
| [ROADMAP.md](ROADMAP.md) | Roadmap: runtime telemetry SLA, protocol telemetry parity, script authoring consolidation, PT Analysis Parquet import, messaging open work and design record, browser/mobile roadmap, CloudTest outcome-parity ledger and phases |
| [qa/TEST_PROGRAM.md](qa/TEST_PROGRAM.md) | Nine-month progressive test and infrastructure-readiness program; scope of record, live risk register, and release test checklist |
| [qa/DASHBOARD.md](qa/DASHBOARD.md) | Generated current QA status, headline metrics, Tier-0/API coverage, and gate tiers |
| [qa/STATUS.md](qa/STATUS.md) | Milestone state and current program narrative (tracker-enforced) |
| [qa/E2E_SUITES.md](qa/E2E_SUITES.md) | Browser E2E suites: Tier-0 journey and assertion matrix, workflow regression suite, and recording/replay suite |
| [qa/DEFECTS.md](qa/DEFECTS.md) | Rolling defect log and resolution evidence |
| [qa/FLAKE_LOG.md](qa/FLAKE_LOG.md) | Quarantined non-deterministic tests, root causes, and un-quarantine dates |

## Historical

Historical material is preserved for audit context, is excluded from current-document freshness checks, and should not be treated as current product documentation.

| Location | Contents |
| --- | --- |
| [archive/README.md](archive/README.md) | Archive map: consolidated September 2026 QA/UX audits (`archive/qa-reviews/AUDITS_2026-09.md`), the retired DSL guide, and the list of removed plans and reviews with `git show` restore commands |
| [qa/reviews/README.md](qa/reviews/README.md) | Single index of point-in-time reviews and evaluations (open vs complete), including the Data Pools and LoadRunner runtime-settings evaluations, with captured evidence; points to `archive/README.md` for completed reviews |
