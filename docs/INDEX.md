# Documentation Index

Status: Current

This is the canonical map for APEX Load documentation. Generated references are
the source of truth for API and DSL contracts; roadmap and historical files are
kept for planning or audit context only.

## User and operator guides

| Document | Status | Purpose |
| --- | --- | --- |
| [HELP_CENTER.md](HELP_CENTER.md#build-and-run-your-first-test) | Current | In-app help: first test, capabilities, support, changes, and troubleshooting |
| [README.md](../README.md) | Current | Project overview, quick start, 21 `apexctl` commands, and canonical runtime environment variables |
| [APPLICATION_USER_GUIDE.md](APPLICATION_USER_GUIDE.md) | Current | End-user guide to the dashboard application |
| [Browser intelligence workflows](guides/BROWSER_INTELLIGENCE_WORKFLOWS.md) | Current | Consolidated browser document/resource/interaction evidence, HAR downloads/exports, findings, scores, journeys, heatmaps, CLI and MCP |
| [HTTP/3 and QUIC](guides/HTTP3_AND_QUIC.md) | Preview | Version policies, QUIC settings, import mapping, results, operator enablement, egress and UDP buffers |
| [Telemetry budget policy receipts](guides/TELEMETRY_POLICY_RECEIPTS.md) | Current | Immutable run budgets, multi-segment transitions, digest verification, live/archive inspection and diagnostic limits |
| [Scenario and queued-run authoring fidelity](guides/AUTHORING_SCENARIO_FIDELITY.md) | Current | Whole-section settings, `ETag`/`If-Match` CAS, Contract v2 receipts, `ScenarioValidationContext`, and revised queued copies |
| [ADMIN_USER_GUIDE.md](ADMIN_USER_GUIDE.md) | Current | Administrator tasks, library assignments, and project secrets |
| [API_USER_GUIDE.md](API_USER_GUIDE.md) | Current | API orientation, authentication, workflows, and automation |
| [Live data tables](LIVE_DATA_TABLES_RUNTIME.md) | Current | Explicit per-run live bindings, action semantics, VTS migration summary, recovery limits, and protected output |
| [Project live tables](PROJECT_LIVE_TABLES.md) | Current | Shared project lifetime, operator edits, revision conflicts, lifecycle and permissions |
| [Native script data services](NATIVE_SCRIPT_DATA_SERVICES.md) | Current | Java SDK and isolated JavaScript actions, native permissions and runner support |
| [Distributed rendezvous](DISTRIBUTED_RENDEZVOUS.md) | Current | Opt-in group barriers, fixed membership, durable outcomes and transport bounds |
| [VTS compatibility](LIVE_DATA_TABLES_VTS_COMPATIBILITY.md) | Current | Supported authoring primitives and blocked vendor imports without unproven parity |
| [OBJECT_ACTIVITY.md](OBJECT_ACTIVITY.md) | Current | Project object history, dependency evidence, disclosure-safe activity, and archive restoration |
| [HELP_DSL_OPERATOR_GUIDE.md](HELP_DSL_OPERATOR_GUIDE.md) | Current | Canonical DSL templating, execution, examples, and migration guidance for LoadRunner and JMeter operators |
| [RECORDING_PROFILES.md](RECORDING_PROFILES.md) | Current | Project recording profiles and VuGen recording-option mappings |
| [AI_ASSISTED_WORKFLOWS.md](AI_ASSISTED_WORKFLOWS.md) | Current | Governed deterministic assisted-authoring workflows, MCP server integration, and API contract |
| [recording-agent.md](recording-agent.md) | Current | Local browser Recording Agent |

## Operations and deployment

| Document | Purpose |
| --- | --- |
| [deployments/README.md](../deployments/README.md) | Deployment chooser and complete Kubernetes overlay map |
| [deployments/helm/apexload/README.md](../deployments/helm/apexload/README.md) | Enterprise Kubernetes/AKS Helm chart values, RBAC, and upgrade procedures |
| [deployments/k8s/local/README.md](../deployments/k8s/local/README.md) | Local and kind Kubernetes overlays |
| [deployments/k8s/onprem/README.md](../deployments/k8s/onprem/README.md) | Production on-premises Kubernetes overlay, mTLS, and manifest rendering |
| [deployments/k8s/aks/README.md](../deployments/k8s/aks/README.md) | Production Azure AKS overlay and Azure Terraform (`AKS`, `ACI`, `Event Hubs`, `PostgreSQL`, `Redis`, `Blob`, `Key Vault`) |
| [deployments/k8s/eks/README.md](../deployments/k8s/eks/README.md) | Production AWS EKS overlay and AWS Terraform (`EKS`, `ECS Fargate`, `RDS`, `ElastiCache`, `S3`, `EFS`, `KMS`) |
| [deployments/azure/appservice/README.md](../deployments/azure/appservice/README.md) | Azure App Service `launcher.js` supervisor, single-port constraints, and static/reverse generator topology |
| [deployments/docker/README.md](../deployments/docker/README.md) | Local multi-service Docker Compose stack (`controller`, `generator`, `prometheus`, optional `java-vuser`) |
| [AUTH_RBAC_OPERATIONS_RUNBOOK.md](AUTH_RBAC_OPERATIONS_RUNBOOK.md) | Auth/RBAC operations, production break-glass recovery, service accounts, and audit evidence |
| [CICD_INTEGRATION.md](CICD_INTEGRATION.md) | CI/CD performance gates, GitHub Enterprise integration, and pipeline examples (see also [.github/actions/apexload-run/README.md](../.github/actions/apexload-run/README.md)) |
| [GITHUB_ACTIONS_RELEASE_AND_DEPLOYMENT.md](GITHUB_ACTIONS_RELEASE_AND_DEPLOYMENT.md) | 12-workflow CI/release inventory, 6-lane image promotion, protected deployments, and rollback operations |
| [CLUSTERS_AND_CLOUD.md](CLUSTERS_AND_CLOUD.md) | Registered Kubernetes connections, 6-rule restart controls, and Azure/AWS PaaS compute |
| [ENTERPRISE_PLATFORM_RUNBOOK.md](ENTERPRISE_PLATFORM_RUNBOOK.md) | Enterprise platform architecture, 10-probe readiness gate, HA run continuity & evacuation, data platform operations, and database migration |
| [ARCHIVE_SOURCE_RETENTION.md](ARCHIVE_SOURCE_RETENTION.md) | Opt-in confirmed-source retention, publication fences, collection budgets, and node storage recovery |
| [ENTERPRISE_FLEET_AND_CAPACITY.md](ENTERPRISE_FLEET_AND_CAPACITY.md) | Generator fleet inventory, protocol density envelopes, autoscaling, capacity reservations, and run supervision |
| [OBSERVABILITY_INTEGRATIONS.md](OBSERVABILITY_INTEGRATIONS.md) | Prometheus `/metrics`, OpenTelemetry tracing, Power BI/Confluence publishing, and governed provider registry |
| [PROMETHEUS_MONITORING.md](PROMETHEUS_MONITORING.md) | Project target-system Prometheus connections, typed PromQL queries, collection, archives, and acceptance rules |
| [DYNATRACE.md](DYNATRACE.md) | Dynatrace SaaS Classic/Grail setup, sources, run evidence, scan budgets, CLI subcommands, and webhooks |
| [ELASTIC.md](ELASTIC.md) | Elasticsearch, ES\|QL, and OpenSearch connections, collectors, schemas, budgets, and coverage |
| [MONITORING_AND_PREPARATION.md](MONITORING_AND_PREPARATION.md) | Collector agent reference, source binding, traffic-free preparation, capacity, and messaging management |

## Engineering and product references

| Document | Purpose |
| --- | --- |
| [AGENTS.md](../AGENTS.md) | Canonical `cmd/` and `pkg/` repository inventory, testing/commit policies, and build/cleanup rules |
| [ARCHITECTURE.md](../ARCHITECTURE.md) | System architecture, fail-closed gRPC security posture, and runtime model |
| [architecture.html](architecture.html) | Interactive single-file system architecture and data-flow explorer |
| [DEVELOPMENT.md](../DEVELOPMENT.md) | Developer setup, codegen targets, package guide, and contributor workflow |
| [TROUBLESHOOTING.md](../TROUBLESHOOTING.md) | Verified symptom-to-fix developer and operational troubleshooting runbook |
| [SCRIPT_LIFECYCLE.md](SCRIPT_LIFECYCLE.md) | Script source-to-run lifecycle, template resolution rules, and conditional authoring contracts |
| [SCRIPT_DEBUGGER_GUIDE.md](SCRIPT_DEBUGGER_GUIDE.md) | Script replay debugger workflow, routes, and imported runtime-setting/capture diagnostics |
| [PROTOCOL_SUPPORT.md](PROTOCOL_SUPPORT.md) | All 15 bundled runtime protocols (`http` 1.1/2/3, `soap`, `java_http`, `java_vuser`, `websocket`, `grpc`, JMS, `kafka`, `eventhubs`, `database`, `network`, `browser`, `appium`) plus authoring-only `sap_gui` |
| [PROTOCOL_DIAGNOSTICS.md](PROTOCOL_DIAGNOSTICS.md) | gRPC/Java-HTTP/Kafka measurement semantics, availability, distributed summaries, and archive compatibility |
| [EVENT_HUBS_HOWTO.md](EVENT_HUBS_HOWTO.md) | Native Event Hubs deployment defaults, observer isolation, Kafka read-committed seek, and terminal lag gates |
| [IMPORT_FIDELITY_AND_REPAIRS.md](IMPORT_FIDELITY_AND_REPAIRS.md) | Multi-format import fidelity (LoadRunner, JMeter, Bruno, MuleSoft/Agentforce, OpenAPI, HAR, k6, CloudTest, etc.), guided repair, and archive compatibility |
| [RUNTIME_SETTINGS_PARITY.md](RUNTIME_SETTINGS_PARITY.md) | Runtime-settings parity evidence and API contract |
| [BROWSER_EVIDENCE_ARCHITECTURE.md](BROWSER_EVIDENCE_ARCHITECTURE.md) | Internal native HAR working-file ownership, crash reclamation, gRPC `AcknowledgeEvidence` transfer, memory watchdog, and CDP projection internals |
| [JAVA_SHARED_AND_PILOT_RUNTIME.md](JAVA_SHARED_AND_PILOT_RUNTIME.md) | Shared JVMs, LoadRunner Thread Mode, IPC v3, `HostSupervisor`, pooled admission/lifecycle, pilot gates, and AppCDS |
| [JAVA_DEDICATED_BROKER_EXPERIMENTAL.md](JAVA_DEDICATED_BROKER_EXPERIMENTAL.md) | Experimental dedicated Java mTLS broker, execution-pod supervision, and external boundary attestation contract |
| [NATIVE_ANALYTICS.md](NATIVE_ANALYTICS.md) | Project/DuckDB enablement, Parquet imports, writer ownership, measurement identities, byte/Java-HTTP queries, panels, and CLI workflows |
| [ROOT_CAUSE_ANALYSIS.md](ROOT_CAUSE_ANALYSIS.md) | Run validity, budgets, onset detection, browser anomalies, graph/hypotheses, RCA v2, and baseline comparison |
| [PERFORMANCE_BENCHMARKS.md](PERFORMANCE_BENCHMARKS.md) | Harness-overhead benchmark, local `apexbench` (incl. `apexbench java`), and browser benchmark procedures |
| [ACCESS_SURFACES.md](ACCESS_SURFACES.md) | Supported hosted dashboard, Wails desktop, and `apexctl` CLI access surfaces |
| [ENTERPRISE_CONTRACTS.md](ENTERPRISE_CONTRACTS.md) | Enterprise authoring lifecycle, workload shape/load model, and project-first governance contracts |
| [contracts/orchestration-v1/README.md](contracts/orchestration-v1/README.md) | Orchestration v1 contract and wire schemas |
| [COMPETITIVE_COMPARISON.md](COMPETITIVE_COMPARISON.md) | Competitive positioning, evaluator trust pack, REST API comparison, and protocol bundle links |
| [RETIRED_INTEGRATIONS.md](RETIRED_INTEGRATIONS.md) | Retired AppDynamics monitoring and Bruno Runner, retained data behavior, and cold-archive restoration |
| [dashboard/README.md](../dashboard/README.md) | React/TypeScript SPA scripts, Vitest/Playwright auth profiles, and bundle checks |
| [desktop/README.md](../desktop/README.md) | Wails v2 native desktop shell build and runtime notes |
| [mcp/README.md](../mcp/README.md) | Python MCP server setup, tool inventory, and two-tier confirmation fences |
| [runners/java_vuser/README.md](../runners/java_vuser/README.md) | Java Vuser sidecar harness, `lrapi`, and container profiles |
| [runners/monitor-java/README.md](../runners/monitor-java/README.md) | Restricted JMX/JDBC collector helper metrics and safety bounds |
| [scripts/README.md](../scripts/README.md) | Repository verification, codegen, fault-harness, and manifest-rendering scripts |
| [examples/README.md](../examples/README.md) | Sample DSL scripts, data feeds, and protocol fixtures |
| [api/proto/README.md](../api/proto/README.md) | Controller↔generator gRPC protobuf definitions and regeneration |

Every Markdown file directly under `docs/` (other than this index), every `docs/guides/*.md` guide, and every `deployments/**/README.md` file appears in a table row on this page. That is machine-enforced by `check_index_membership` in `scripts/check-docs.py`. Point-in-time material under `docs/qa/reviews/` and `docs/archive/` is indexed through the Historical section below.

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

These files contain tactical plans, future work, or parity/qualification tracking. They are
not the source of truth for current API or DSL contracts.

| Document | Status |
| --- | --- |
| [ROADMAP.md](ROADMAP.md) | Remaining external qualification gates and open engineering work; dated delivery history is archived |
| [FEATURE_COMPLETION_SPEC.md](FEATURE_COMPLETION_SPEC.md) | Maintained specification for Event Hubs/messaging, browser evidence/intelligence, telemetry reporting, native analytics, and authoring lifecycle |
| [NODE_FAILURE_READINESS_PLAN.md](NODE_FAILURE_READINESS_PLAN.md) | Worker recovery, KEDA capacity, liveness, storage, and fault qualification ledger (`REC-C1` local gate passed; cloud soak open) |
| [HTTP3_IMPLEMENTATION.md](HTTP3_IMPLEMENTATION.md) | HTTP/3 `quic-go` transport decision records (D1–D4), test fixture architecture, and Wave 0–6 delivery ledger |
| [qa/TEST_PROGRAM.md](qa/TEST_PROGRAM.md) | Nine-month progressive test and infrastructure-readiness program, risk register, and opt-in suite inventory |
| [qa/DASHBOARD.md](qa/DASHBOARD.md) | Generated current QA status, headline metrics, Tier-0/API coverage, and execution lane status |
| [qa/STATUS.md](qa/STATUS.md) | Milestone state and current program narrative (tracker-enforced) |
| [qa/E2E_SUITES.md](qa/E2E_SUITES.md) | Browser E2E suites: Tier-0 journey and assertion matrix, workflow regression suite, and recording/replay suite |
| [qa/DEFECTS.md](qa/DEFECTS.md) | Rolling defect log and resolution evidence |
| [qa/FLAKE_LOG.md](qa/FLAKE_LOG.md) | Quarantined non-deterministic tests, root causes, and un-quarantine dates |
| [qa/ELASTIC_QUALIFICATION.md](qa/ELASTIC_QUALIFICATION.md) | Exact-version Elasticsearch 8.14/9.1 and OpenSearch 2.19/3.3 vendor qualification trials and limits |
| [qa/LOCAL_BROKER_CHECKS.md](qa/LOCAL_BROKER_CHECKS.md) | Local Docker broker integration checks (`Kafka`, `Solace`, `ActiveMQ`, `PostgreSQL`) |
| [qa/rca-eval/README.md](qa/rca-eval/README.md) | Root-cause analysis evaluation corpus and scoring thresholds |

### Acceptance references

| Acceptance reference | Purpose |
| --- | --- |
| [Analytics acceptance](qa/ANALYTICS_ACCEPTANCE.md) | Native platform and reference-workload gates |
| [Browser acceptance](qa/BROWSER_ACCEPTANCE.md) | Browser evidence inventory and qualification |
| [Browser intelligence acceptance](qa/BROWSER_INTELLIGENCE_ACCEPTANCE.md) | Findings, baselines and journey workflows |
| [Protocol telemetry](qa/PROTOCOL_TELEMETRY_MATRIX.md) | Non-Messaging local measurement fixtures, propagation evidence and pending qualification |
| [Telemetry acceptance](qa/TELEMETRY_ACCEPTANCE.md) | Fault matrix and reference-workload limits |
| [Lifecycle parity](qa/LIFECYCLE_PARITY_MATRIX.md) | Authoring lifecycle evidence |
| [Adapter retirement](qa/ADAPTER_RETIREMENT.md) | Retired authoring adapters and ratchet evidence |

## Historical

Historical material is preserved for audit context, is excluded from current-document freshness checks, and should not be treated as current product documentation.

| Location | Contents |
| --- | --- |
| [archive/README.md](archive/README.md) | Archive map: September audits, October themed slice review ledgers, archived specs/plans, roadmap history, and restore commands |
| [qa/reviews/README.md](qa/reviews/README.md) | Active capstone qualification records (`INT-1`, `REC-C1`, `NA-D1`), foundational evaluation ledgers, and links to consolidated historical review archives |
