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
| [Browser intelligence workflows](guides/BROWSER_INTELLIGENCE_WORKFLOWS.md) | Current | Report workflows, finding/score contracts, journey accounting, gesture patterns, heatmaps, CLI and MCP |
| [Browser document evidence](guides/BROWSER_DOCUMENT_EVIDENCE.md) | Current | Opt-in capture, lifecycle populations, bounded live/archive inspection and qualification limits |
| [HTTP/3 and QUIC](guides/HTTP3_AND_QUIC.md) | Preview | Version policies, QUIC settings, import mapping, results, and operator enablement, egress and buffers |
| [Retained resource inspection](guides/RETAINED_RESOURCE_INSPECTION.md) | Current | Authorized bounded archive resource pages, exact filters, native availability and CLI inspection |
| [Native HAR working-file ownership](guides/BROWSER_HAR_WORKING_OWNERSHIP.md) | Current | Private staging, joined native close, protected retirement and remaining transfer/platform limits |
| [Browser interaction evidence](guides/BROWSER_INTERACTION_EVIDENCE.md) | Current | Opt-in gesture evidence: dispatched points, keyboard targets, layout revision, transport and bounds |
| [Telemetry budget policy receipts](guides/TELEMETRY_POLICY_RECEIPTS.md) | Current | Immutable run budgets, digest verification, live/archive inspection and diagnostic limits |
| [Scenario and queued-run authoring fidelity](guides/AUTHORING_SCENARIO_FIDELITY.md) | Current | Whole-section settings, library snapshot compatibility and revised queued copies |
| [BASELINE_COMPARISON.md](BASELINE_COMPARISON.md) | Current | Pinned baseline selection, matched-load evidence, advisory policies and saved comparison exports |
| [ADMIN_USER_GUIDE.md](ADMIN_USER_GUIDE.md) | Current | Administrator tasks, library assignments, and project secrets |
| [API_USER_GUIDE.md](API_USER_GUIDE.md) | Current | API orientation, authentication, workflows, and automation |
| [Live data tables](LIVE_DATA_TABLES_RUNTIME.md) | Current | Explicit per-run live bindings, action semantics, recovery limits, and protected output |
| [Project live tables](PROJECT_LIVE_TABLES.md) | Current | Shared project lifetime, operator edits, revision conflicts, lifecycle and permissions |
| [Native script data services](NATIVE_SCRIPT_DATA_SERVICES.md) | Current | Java SDK and isolated JavaScript actions, native permissions and runner support |
| [Distributed rendezvous](DISTRIBUTED_RENDEZVOUS.md) | Current | Opt-in group barriers, fixed membership, durable outcomes and transport bounds |
| [VTS compatibility](LIVE_DATA_TABLES_VTS_COMPATIBILITY.md) | Current | Supported authoring primitives and blocked vendor imports without unproven parity |
| [OBJECT_ACTIVITY.md](OBJECT_ACTIVITY.md) | Current | Project object history, disclosure-safe activity and archive workflows |
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
| [PROMETHEUS_MONITORING.md](PROMETHEUS_MONITORING.md) | Project target-system Prometheus connections, typed queries, collection, evidence, archives, acceptance and operating guidance |
| [PROMETHEUS_MONITORING_SPEC.md](PROMETHEUS_MONITORING_SPEC.md) | Target-system monitoring baseline audit, contracts, implementation sequence, limits and qualification matrix |
| [DYNATRACE.md](DYNATRACE.md) | Dynatrace SaaS Classic/Grail setup, sources, run evidence, scan budgets, and troubleshooting |
| [ENTERPRISE_FLEET_AND_CAPACITY.md](ENTERPRISE_FLEET_AND_CAPACITY.md) | Generator fleet inventory, autoscaling, capacity reservations, and run supervision |

## Engineering and product references

| Document | Purpose |
| --- | --- |
| [ARCHITECTURE.md](../ARCHITECTURE.md) | System architecture and runtime model |
| [DEVELOPMENT.md](../DEVELOPMENT.md) | Developer setup, package guide, and testing policy |
| [TROUBLESHOOTING.md](../TROUBLESHOOTING.md) | Developer and operational troubleshooting |
| [SCRIPT_LIFECYCLE.md](SCRIPT_LIFECYCLE.md) | Script source-to-run lifecycle |
| [OBJECT_ACTIVITY.md](OBJECT_ACTIVITY.md) | Object history, dependency evidence, archive admission, and restoration |
| [RETIRED_INTEGRATIONS.md](RETIRED_INTEGRATIONS.md) | Retired AppDynamics monitoring and Bruno Runner, retained history and restoration guidance |
| [DYNATRACE_SPEC.md](DYNATRACE_SPEC.md) | Dynatrace SaaS native monitoring contracts, migration, lifecycle, limits, and fixture qualification |
| [ELASTIC.md](ELASTIC.md) | Elasticsearch, ES\|QL and OpenSearch connections, collectors, schemas, budgets and coverage |
| [ELASTIC_SPEC.md](ELASTIC_SPEC.md) | Elastic implementation contracts, lifecycle and qualification gates |
| [ELASTIC_QUALIFICATION.md](ELASTIC_QUALIFICATION.md) | Exact-version Elasticsearch/OpenSearch vendor trials, evidence and deployment limits |
| [SCRIPT_DEBUGGER_GUIDE.md](SCRIPT_DEBUGGER_GUIDE.md) | Script replay debugger workflow, routes, and imported runtime-setting/capture diagnostics |
| [EVENT_HUBS_HOWTO.md](EVENT_HUBS_HOWTO.md) | Native Event Hubs deployment defaults, observer isolation and terminal lag gates |
| [PROTOCOL_SUPPORT.md](PROTOCOL_SUPPORT.md) | Web/API, browser, messaging, Java Vuser, and SAP GUI support boundaries |
| [PROTOCOL_DIAGNOSTICS.md](PROTOCOL_DIAGNOSTICS.md) | gRPC measurement semantics, availability, distributed summaries, and archive compatibility |
| [IMPORT_FIDELITY_AND_REPAIRS.md](IMPORT_FIDELITY_AND_REPAIRS.md) | JMeter and LoadRunner import fidelity, source-plan execution, guided repair, native archive compatibility, and regression coverage |
| [RUNTIME_SETTINGS_PARITY.md](RUNTIME_SETTINGS_PARITY.md) | Runtime-settings parity evidence and API contract |
| [RECORDING_PROFILES.md](RECORDING_PROFILES.md) | Project recording profiles and how VuGen's recording options map to them |
| [PERFORMANCE_BENCHMARKS.md](PERFORMANCE_BENCHMARKS.md) | Harness-overhead benchmark, local performance evidence, and browser benchmark procedures |
| [JAVA_SHARED_JVMS.md](JAVA_SHARED_JVMS.md) | Operator-enabled shared JVMs, LoadRunner thread mode, distributed capacity and scoped benchmark evidence |
| [JAVA_VUSER_POOLING_IMPLEMENTATION.md](JAVA_VUSER_POOLING_IMPLEMENTATION.md) | Java shared-runtime and pilot scope, component map, reference ownership and open release gates |
| [JAVA_PILOT_OPERATIONS.md](JAVA_PILOT_OPERATIONS.md) | Fixture construction, baseline/pilot evidence, comparison gates and operator AppCDS |
| [JAVA_POOLED_RUNTIME_CONTRACTS.md](JAVA_POOLED_RUNTIME_CONTRACTS.md) | IPC, containment, shared admission, ownership, lifecycle and review-derived constraints |
| [JAVA_DEDICATED_EXECUTION_CONTRACTS.md](JAVA_DEDICATED_EXECUTION_CONTRACTS.md) | Durable broker authority, execution-pod supervision, preparation and distributed contracts |
| [JAVA_BOUNDARY_ATTESTOR_CONTRACT.md](JAVA_BOUNDARY_ATTESTOR_CONTRACT.md) | Authenticated external runtime and infrastructure-fencing proof contract for dedicated Java cleanup |
| [JAVA_BROKER_SERVICE.md](JAVA_BROKER_SERVICE.md) | Experimental dedicated Java broker command, operator profiles, host credentials and authenticated preparation API |
| [ACCESS_SURFACES.md](ACCESS_SURFACES.md) | Supported hosted, desktop, and CLI access surfaces |
| [AI_ASSISTED_WORKFLOWS.md](AI_ASSISTED_WORKFLOWS.md) | Governed assisted-authoring workflow and API contract |
| [COMPETITIVE_COMPARISON.md](COMPETITIVE_COMPARISON.md) | Competitive positioning, evaluator trust pack, REST API comparison, and stable links to maintained import contracts |
| [ENTERPRISE_CONTRACTS.md](ENTERPRISE_CONTRACTS.md) | Enterprise authoring lifecycle, workload shape/load model, and project-first governance contracts |
| [HA_RUN_CONTINUITY_PLAN.md](HA_RUN_CONTINUITY_PLAN.md) | Historical design and P0–P4/later review evidence; current continuity operations are in the enterprise runbook |
| [NODE_STORAGE_RECOVERY.md](NODE_STORAGE_RECOVERY.md) | Worker state inventory, fencing, storage recovery, and qualification limits |
| [ARCHIVE_SOURCE_RETENTION.md](ARCHIVE_SOURCE_RETENTION.md) | Opt-in confirmed-source retention, publication fences and recoverability limits |
| [ANALYSIS_WORKFLOWS.md](ANALYSIS_WORKFLOWS.md) | Project/native-build enablement and navigation to monitoring, native analytics and RCA contracts |
| [MONITORING_AND_PREPARATION.md](MONITORING_AND_PREPARATION.md) | Collector trust, source binding, traffic-free preparation, capacity and messaging management |
| [ROOT_CAUSE_ANALYSIS.md](ROOT_CAUSE_ANALYSIS.md) | Validity, budgets, onset, browser anomalies, graph/hypothesis and service contracts |
| [NATIVE_ANALYTICS.md](NATIVE_ANALYTICS.md) | Parquet imports, measurement identities, queries, panels and resource/acceptance limits |

Every Markdown file directly under `docs/`, other than this index itself, appears in one of the audience or lifecycle tables on this page. That is machine-enforced: `check_index_membership` in `scripts/check-docs.py` fails the CI documentation job if a `docs/*.md` file is not linked from a table row here. Point-in-time material under `docs/qa/reviews/` and `docs/archive/` is excluded from this index and the check.

## Generated References

| Document | Status | Source |
| --- | --- | --- |
| [swagger.yaml](swagger.yaml) | Generated | `go generate ./pkg/api` |
| [api-endpoint-catalog.json](api-endpoint-catalog.json) | Generated | `go generate ./pkg/api` |
| [dsl.schema.json](dsl.schema.json) | Generated | `go generate ./pkg/dsl` |
| [DSL_ACTION_REFERENCE.md](DSL_ACTION_REFERENCE.md) | Generated | `go generate ./pkg/dsl` |
| [LOADRUNNER_COVERAGE.md](LOADRUNNER_COVERAGE.md) | Generated | `go generate ./pkg/convert/enterprisearchive` |
| [DATA_MODEL.md](DATA_MODEL.md) | Generated | `go generate ./pkg/storage` |

## Roadmap and QA program

These files contain tactical plans, future work, or parity analysis. They are
not the source of truth for current API or DSL contracts.

| Document | Status |
| --- | --- |
| [NODE_FAILURE_READINESS_PLAN.md](NODE_FAILURE_READINESS_PLAN.md) | Open implementation and qualification ledger for worker recovery, KEDA capacity, liveness, storage, and archive issues found in September 2026 fault tests |
| [HTTP_AUTHORING_UNIFICATION_PLAN.md](HTTP_AUTHORING_UNIFICATION_PLAN.md) | Proposed HAR/proxy/browser HTTP equivalence, LoadRunner request conformance, shared policy and workflow composition, and adjacent importer consolidation |
| [HTTP3_IMPLEMENTATION.md](HTTP3_IMPLEMENTATION.md) | HTTP/3 delivery status, quic-go contract, test fixture, and transport decision records (timing, retries, socket ownership, defaults) |
| [ROADMAP.md](ROADMAP.md) | Remaining engineering work and qualification gates; dated delivery history is archived |
| [ROADMAP_IMPLEMENTATION_SPEC.md](ROADMAP_IMPLEMENTATION_SPEC.md) | Proposed implementation delta against current main for all remaining roadmap tracks except Messaging and CloudTest outcome parity; dependencies, compatibility and acceptance gates |
| [FEATURE_COMPLETION_SPEC.md](FEATURE_COMPLETION_SPEC.md) | Approved implementation and acceptance specification for Event Hubs/messaging, browser evidence/intelligence, telemetry reporting, native analytics and authoring cleanup |
| [qa/TEST_PROGRAM.md](qa/TEST_PROGRAM.md) | Nine-month progressive test and infrastructure-readiness program; scope of record, live risk register, and release test checklist |
| [qa/DASHBOARD.md](qa/DASHBOARD.md) | Generated current QA status, headline metrics, Tier-0/API coverage, and gate tiers |
| [qa/STATUS.md](qa/STATUS.md) | Milestone state and current program narrative (tracker-enforced) |
| [qa/E2E_SUITES.md](qa/E2E_SUITES.md) | Browser E2E suites: Tier-0 journey and assertion matrix, workflow regression suite, and recording/replay suite |
| [qa/DEFECTS.md](qa/DEFECTS.md) | Rolling defect log and resolution evidence |
| [qa/FLAKE_LOG.md](qa/FLAKE_LOG.md) | Quarantined non-deterministic tests, root causes, and un-quarantine dates |

### Acceptance references

The inventories below retain qualification gates. Nested guides are listed in the user-guide table; the existing top-level checker does not enforce their membership.

| Acceptance reference | Purpose |
| --- | --- |
| [Analytics acceptance](qa/ANALYTICS_ACCEPTANCE.md) | Native platform and reference-workload gates |
| [Browser acceptance](qa/BROWSER_ACCEPTANCE.md) | Browser evidence inventory and qualification |
| [Browser intelligence acceptance](qa/BROWSER_INTELLIGENCE_ACCEPTANCE.md) | Findings, baselines and journey workflows |
| [Protocol telemetry](qa/PROTOCOL_TELEMETRY_MATRIX.md) | Non-Messaging local measurement fixtures, propagation evidence and pending qualification |
| [Telemetry acceptance](qa/TELEMETRY_ACCEPTANCE.md) | Fault matrix and reference-workload limits |
| [Lifecycle parity](qa/LIFECYCLE_PARITY_MATRIX.md) | Authoring lifecycle evidence |
| [Adapter retirement](qa/ADAPTER_RETIREMENT.md) | Remaining adapters and retirement evidence |

## Historical

Historical material is preserved for audit context, is excluded from current-document freshness checks, and should not be treated as current product documentation.

| Location | Contents |
| --- | --- |
| [archive/README.md](archive/README.md) | Archive map: September audits, October themed reviews, completed live-table plans, roadmap history and restore commands |
| [qa/reviews/README.md](qa/reviews/README.md) | Review navigation: open records, all retained Markdown records, completed themed reports and unchanged evidence folders |
