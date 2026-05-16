# Implementation Plan: Platform Gap Analysis

## Objective
Execute the gap analysis (`docs/PLATFORM_GAP_ANALYSIS.md`) through a structured, phased approach to elevate Stormrunner from a powerful execution engine to a first-class enterprise performance testing platform.

## Execution Strategy
The 19 gaps are organized into five sequential delivery waves. Each wave targets a specific enterprise challenge and unlocks a new tier of platform maturity.

- **Wave 1: Enterprise Foundation (Weeks 1-4)** — Targets DevOps engineers. Makes Stormrunner a fully automated, headless, and secure pipeline gate.
- **Wave 2: Cloud-Native Engine & Compliance (Weeks 5-7)** — Targets backend accuracy and regulatory compliance.
- **Wave 3: Shared Platform Readiness (Weeks 8-12)** — Targets Enterprise Architects. Turns Stormrunner into a governable shared service with controls and provenance.
- **Wave 4: Script Quality and Analysis Depth (Weeks 13-16)** — Targets Performance Engineers. Reduces the time spent authoring scripts and debugging failed runs.
- **Wave 5: Ecosystem and Advanced Modeling (Weeks 17-21)** — Targets global deployment and edge-case modeling.

---

## Wave 1: Enterprise Foundation (Weeks 1-4)
**Goal:** Enable automated, reportable, and secure pipeline execution.

### 1.1 CI/CD Integration (P0)
**Effort:** 5-8 days
- **CLI Enhancements:** Update `cmd/stormctl` to parse SLA thresholds from the scenario. Implement non-zero exit codes when SLAs are breached or the test fails. Add `--format json` for machine-readable stdout.
- **API Endpoints:** Create `GET /api/v1/tests/{id}/sla-status` to return a summarized boolean evaluation of all SLAs.
- **Pipeline Templates:** Create reusable GitHub Actions (`action.yml`) and Azure DevOps tasks (`azure-pipelines-task.yml`).
- **Progress:** Implemented via script-level `goal_config` thresholds, explicit CLI guardrail overrides, machine-readable JSON output, non-zero exit codes on failures or SLA breaches, `GET /api/v1/tests/{id}/sla-status`, and reusable GitHub Actions / Azure Pipelines YAML templates.

### 1.2 Report Generation (P0)
**Effort:** 7-10 days
- **Backend Generator:** Implement `pkg/api/report_generator.go`. It will read the Parquet/JSONL archives and construct a standalone HTML document using a Go template.
- **API Endpoint:** `GET /api/v1/tests/{id}/report` returning `text/html`.
- **Dashboard UI:** Add a "Download Report" button to the `TestExecutionArchive` and `ResultsPage` components.
- **Progress:** Implemented via `pkg/api/report_generator.go`, `GET /api/v1/tests/{id}/report`, `GET /api/v1/tests/{id}/archive/report`, and report download affordances in both `TestExecutionArchive` and `ResultsPage`.

### 1.3 Secrets, PKI, and Secure Credential Handling (P0)
**Effort:** 5-8 days
- **Secret Abstraction:** Add `SecretReference` to the DSL (e.g., `{{secret.db_password}}`).
- **Runtime Injection:** Resolve secrets only at the generator level during execution; never log or serialize them.
- **External Vault Integration:** Support Azure Key Vault integration for fetching secret payloads.
- **Progress:** Implemented via `{{secret.*}}` template support in the DSL/runtime, engine-side secret resolution and redaction, provenance filtering to avoid serializing secret-like values, and Azure Key Vault lookup support in `pkg/engine/secrets.go`.

### 1.4 Ephemeral Environment Target Resolution (P1)
**Effort:** 3-5 days
- **Dynamic Targeting:** Support runtime variables passed via `stormctl` (e.g., `-env target_namespace=pr-123`).
- **Base URL Overrides:** Enable overriding the base URL of a scenario at runtime without modifying the JSON DSL.
- **Progress:** Implemented via `stormctl` runtime `--env` and `--base-url` overrides plus controller-side runtime overrides that compile ephemeral targets without mutating the stored DSL.

---

## Wave 2: Cloud-Native Engine & Compliance (Weeks 5-7)
**Goal:** Ensure the core engine handles microservice queuing correctly and respects data privacy.

### 2.1 Open vs. Closed Workload Modeling (P0)
**Effort:** 6-10 days
- **Arrival-Rate Engine:** Implement an open workload scheduler in `pkg/engine/pacing.go` that spawns ephemeral goroutines to achieve exact target throughput.
- **Coordinated Omission Tracking:** Compare expected arrival rate vs. actual departure rate and flag divergence in the telemetry stream.
- **Progress:** Implemented via open-workload arrival scheduling in the engine, scenario/runtime support for throughput-oriented execution, and throughput-gap telemetry fields (`target_tps`, miss flags, and gap percentage) in metrics snapshots.

### 2.2 PII / PHI Redaction & Compliance (P0)
**Effort:** 4-6 days
- **Scrubbing Pipeline:** Add a regex/entropy-based redaction step in `pkg/engine/metrics.go` for all HTTP headers and body payloads before they are serialized to Parquet or sent over WebSockets.
- **Compliance Config:** Allow users to define custom redaction rules per project.
- **Progress:** Implemented via built-in sensitive-data scrubbing across runtime/archive surfaces plus project-scoped custom redaction rules managed through the API, persistence layer, and dashboard governance settings.

---

## Wave 3: Shared Platform Readiness (Weeks 8-12)
**Goal:** Make Stormrunner a governable, multi-tenant service.

### Implemented Status
- **3.2 Multi-Tenant Governance & Capacity Controls:** Project-level concurrent-test and VUser quotas are enforced, quota-blocked runs enter a persisted pending queue, startup/quota reconciliation promotes eligible work, and queue metadata is exposed in the API and dashboard.
- **3.3 Alerting & Notifications:** Lifecycle webhook notifications are available through system settings with Slack Block Kit and Microsoft Teams payload formatters.
- **3.4 Scheduled Tests:** Scenarios can now carry persisted recurring schedules, the controller evaluates five-field cron expressions on a background loop, and the dashboard includes a Schedules view for create, edit, pause, and delete flows.
- **3.5 Run Provenance & Reproducibility:** Archived manifests now capture immutable execution provenance snapshots, and completed runs can be explicitly pinned as the preferred future comparison baseline.

### 3.1 User Management & RBAC (P1)
**Effort:** 8-12 days
- **Auth Middleware:** Implement OIDC/SAML integration in `pkg/api/middleware.go`.
- **Role Model:** Define System Admin, Project Admin, Editor, and Viewer roles.
- **Dashboard UI:** Add Login screen and User Management panels.
- **Progress:** Partially implemented via trusted-header auth middleware, the full System Admin / Project Admin / Editor / Viewer role model, request-scoped RBAC enforcement, `GET /api/v1/auth/me`, `GET /api/v1/auth/roles`, the dashboard auth gate, sidebar identity visibility, and settings panels that show the current forwarded identity plus the built-in role reference. Direct user lifecycle management still remains delegated to the upstream OIDC/SAML gateway rather than Stormrunner itself.

### 3.2 Multi-Tenant Governance & Capacity Controls (P1)
**Effort:** 6-9 days
- **Quotas:** Implement concurrent execution limits and VUser caps per project/team.
- **Queueing System:** If a team exceeds capacity, place the test in a pending queue rather than failing immediately.

### 3.3 Alerting & Notifications (P1)
**Effort:** 3-4 days
- **Webhook Engine:** Create `pkg/api/notifications.go` that triggers asynchronously on test state changes.
- **Integrations:** Implement formatters for Slack Block Kit and MS Teams Adaptive Cards.

### 3.4 Scheduled Tests (P1)
**Effort:** 3-5 days
- **Scheduler Loop:** Add a cron evaluator in `cmd/controller/main.go`.
- **Dashboard UI:** Add a Schedules tab to manage recurring tests.
- **Status:** Implemented. Schedules are stored per scenario, reconciled across controller restarts, and materialize queued runs through the existing scenario-backed execution path.

### 3.5 Run Provenance & Reproducibility (P1)
**Effort:** 4-6 days
- **Immutable Manifests:** Generate a canonical JSON manifest of all inputs, environment variables, and generator states at the start of a test.
- **Pinning:** Allow explicit pinning of a specific test run as the baseline for future comparisons.

---

## Wave 4: Script Quality and Analysis Depth (Weeks 13-16)
**Goal:** Make script authoring smarter and result analysis faster.

### Implemented Status
- **4.2 Error Analysis & Pattern Detection:** Archived run reports now cluster similar error messages, retain the first captured snapshot for each pattern, and surface those clusters in both the embedded dashboard archive view and the standalone HTML report.
- **4.3 Test Data Management:** Faker-style template generators are available for synthetic email and UUID values, and shared data pools now support `pop` semantics so rows are consumed exactly once.
- **4.4 Think Time & Pacing Intelligence:** HAR sample imports now preserve inter-request pauses as per-action `think_time` blocks when entry timestamps are present.

### 4.1 Correlation Intelligence (P1)
**Effort:** 5-7 days
- **Analysis Engine:** Detect dynamic values flowing from responses into subsequent requests during HAR import.
- **Dashboard UI:** Add "Correlation Assistant" in the ScriptEditor.
- **Status:** Implemented. Import conversions now preserve detected correlation suggestions in script metadata, and the ScriptEditor surfaces those mappings so authors can review variable flow while editing the generated draft.

### 4.2 Error Analysis & Pattern Detection (P1)
**Effort:** 4-6 days
- **Error Clustering:** Group similar error messages to reduce noise.
- **Snapshot Retention:** Save full request/response context for the *first* occurrence of each error pattern.

### 4.3 Test Data Management (P2)
**Effort:** 5-7 days
- **Generators:** Add Faker-style functions (`{{fake.email}}`, `{{fake.uuid}}`).
- **Unique Allocation:** Ensure "pop" semantics for data pools so rows are used exactly once.
- **Status:** Implemented. Faker-style generators are available for email and UUID templates, and data-pool bindings can now use `selection_strategy: "pop"` to consume rows exactly once.

### 4.4 Think Time & Pacing Intelligence (P2)
**Effort:** 2-3 days
- **HAR Analyzer:** Automatically calculate wait times between recorded actions and insert think_time blocks.
- **Status:** Implemented for HAR sample imports by deriving the pause between consecutive HAR entries and mapping it onto `think_time_after` in generated DSL actions.

---

## Wave 5: Ecosystem and Advanced Modeling (Weeks 17-21)
**Goal:** Connect to the observability ecosystem and support edge-case loads.

### Implemented Status
- **5.1 APM / Observability Correlation:** Stormrunner now injects W3C `traceparent` headers into HTTP and SOAP test requests, preserves caller-supplied trace headers, and exposes a Prometheus-compatible `/metrics` endpoint with health, websocket, in-memory test counts, test status breakdowns, and capacity-planning gauges.
- **5.2 Iteration & Rendezvous Control:** Fixed-iteration execution is supported through `run_logic.iterations`, and scripts can now use a `rendezvous` action to synchronize VUsers at a specific point before proceeding.

### 5.1 APM / Observability Correlation (P2)
**Effort:** 5-8 days
- **Trace Injection:** Generate W3C `traceparent` headers for test requests.
- **Prometheus Exporter:** Expose controller telemetry in Prometheus format.

### 5.2 Iteration & Rendezvous Control (P2)
**Effort:** 3-4 days
- **Rendezvous Primitive:** Synchronize VUsers at a specific action to simulate spike loads.
- **Iteration Limits:** Support fixed-iteration testing instead of fixed-duration.
- **Status:** Implemented. Fixed-iteration execution is available in the DSL and runtime, and the engine now provides a reusable `rendezvous` action with optional participant counts and timeouts.

### 5.3 Network Emulation & Traffic Shaping (P2)
**Effort:** 5-8 days
- **Traffic Profiles:** Apply bandwidth, jitter, and packet loss constraints via a custom Go dialer.
- **Status:** Implemented. `config.web_runtime.network_simulation` now supports jitter and packet-loss fields, and HTTP-family protocols apply transport shaping through a custom dialer/connection wrapper while browser tests continue to honor the existing preset throttling surface.

### 5.4 Multi-Region Distribution (P3)
**Effort:** 8-12 days
- **Generator Tags:** Deploy generators with region tags and distribute load dynamically based on region weighting.
- **Status:** Implemented. Distributed generators can now be tagged inline through `STORMRUNNER_GENERATORS=host:9090|region=<name>`, inventory responses expose each generator region, and test/scenario assignment supports `mode: "region_weighted"` with `region_weights` for capacity-aware group placement across tagged regions.

---

## Dependencies & Risk Management

### Critical Paths
1. **Reports (1.2)** depend on the stability of the Parquet archives. If the schema changes, the report generator breaks.
2. **Alerts (1.3)** and **Schedules (1.4)** require the Controller to be running 24/7, shifting it from an ephemeral tool to a persistent control plane service.

### Risk Mitigations
- **Database Migrations:** As we add Schedules, Users, and Roles to the SQLite database, we must implement a robust schema migration system (e.g., `golang-migrate`) before Phase 1.4.
- **LLM Hallucinations in Correlation:** The correlation assistant (Phase 2.1) must always present suggestions for user approval, never auto-mutating the script without consent.
- **Prometheus Cardinality:** When exporting metrics to Prometheus (Phase 3.2), we must aggregate by transaction name and status, not by individual VUser, to avoid cardinality explosions in the APM tool.

## Next Steps
1. Create GitHub issues/tickets for Phase 1.
2. Begin implementation of **1.1 CI/CD Integration** (CLI enhancements and GitHub Action template).
3. Draft the HTML template layout for **1.2 Report Generation**.
