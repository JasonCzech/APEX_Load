# Implementation Plan: Platform Gap Analysis

## Objective
Execute the gap analysis (`docs/PLATFORM_GAP_ANALYSIS.md`) through a structured, phased approach to elevate Stormrunner from a powerful execution engine to a first-class enterprise performance testing platform.

## Execution Strategy
The 12 gaps are organized into three sequential delivery phases. Each phase targets a specific persona and unlocks a new tier of enterprise value.

- **Phase 1: CI/CD & Operations (Weeks 1-4)** — Targets DevOps engineers. Makes Stormrunner a fully automated, headless pipeline gate.
- **Phase 2: Intelligence & Diagnostics (Weeks 5-8)** — Targets Performance Engineers. Reduces the time spent authoring scripts and debugging failed runs.
- **Phase 3: Enterprise Scale & Integrations (Weeks 9-14)** — Targets Enterprise Architects. Connects Stormrunner to the broader observability and security ecosystem.

---

## Phase 1: CI/CD & Operations (Weeks 1-4)
**Goal:** Enable automated, scheduled, and reportable pipeline execution.

### 1.1 CI/CD Integration (P0)
**Effort:** 5-8 days
- **CLI Enhancements:** Update `cmd/stormctl` to parse SLA thresholds from the scenario. Implement non-zero exit codes when SLAs are breached or the test fails. Add `--format json` for machine-readable stdout.
- **API Endpoints:** Create `GET /api/v1/tests/{id}/sla-status` to return a summarized boolean evaluation of all SLAs.
- **Pipeline Templates:**
  - Create `.github/actions/stormrunner-test/action.yml` (GitHub Actions).
  - Create `azure-pipelines-task.yml` (Azure DevOps).
  - Both templates will poll the API and fail the pipeline step on SLA breach.

### 1.2 Report Generation (P0)
**Effort:** 7-10 days
- **Backend Generator:** Implement `pkg/api/report_generator.go`. It will read the Parquet/JSONL archives and construct a standalone HTML document using a Go template.
- **Data Model:** Define `ReportData` struct containing the executive summary, SLA results, transaction latency distribution, and error summaries.
- **API Endpoint:** `GET /api/v1/tests/{id}/report` returning `text/html`.
- **Dashboard UI:** Add a "Download Report" button to the `TestExecutionArchive` and `ResultsPage` components.

### 1.3 Alerting & Notifications (P1)
**Effort:** 3-4 days
- **Data Model:** Add `NotificationRules` to `ManagedTest` and project settings.
- **Webhook Engine:** Create `pkg/api/notifications.go` that triggers asynchronously on test state changes (started, completed, failed, sla_breached).
- **Integrations:** Implement formatters for generic JSON Webhooks, Slack Block Kit, and MS Teams Adaptive Cards.
- **Dashboard UI:** Add a "Notifications" tab in the `CreateTestModal` and Project Settings.

### 1.4 Scheduled Tests (P1)
**Effort:** 3-5 days
- **Data Model:** Create `Schedule` struct (cron string, active flag, next run time).
- **Scheduler Loop:** Add a ticker in `cmd/controller/main.go` that evaluates active schedules every minute.
- **Execution Bridge:** When a schedule triggers, construct a `ManagedTest` and hand it to the `TestRunner`.
- **Dashboard UI:** Add a "Schedules" tab to the main sidebar. Create a `SchedulesPage` to list, create, and toggle recurring tests.

---

## Phase 2: Intelligence & Diagnostics (Weeks 5-8)
**Goal:** Make script authoring smarter and result analysis faster.

### 2.1 Correlation Intelligence (P1)
**Effort:** 5-7 days
- **Analysis Engine:** Create `pkg/engine/correlation.go`. Compare recorded HAR responses against subsequent HAR requests. Identify strings (e.g., tokens) that were received and then re-transmitted.
- **Extractor Suggestions:** Generate DSL `Extractor` blocks for identified values.
- **Dashboard UI:** Add a "Correlation Assistant" panel in the `ScriptEditor`. Highlight potential dynamic values and offer a 1-click "Add Extractor" button.
- **Agentic Hook:** Pass identified correlations to the MCP server for LLM refinement.

### 2.2 Error Analysis & Pattern Detection (P1)
**Effort:** 4-6 days
- **Error Clustering:** Implement string similarity clustering (e.g., Levenshtein distance or tokenization) in `pkg/engine/metrics.go` to group similar error messages, masking out dynamic IDs.
- **Snapshot Retention:** Update the engine to automatically capture a full `ErrorSnapshot` for the *first* occurrence of each unique error cluster.
- **Dashboard UI:** Overhaul the Errors tab in `TestExecution`. Show clustered error patterns, a timeline of when the pattern occurred, and a button to view the representative snapshot.

### 2.3 Think Time & Pacing Intelligence (P2)
**Effort:** 2-3 days
- **HAR Analyzer:** Update `pkg/convert/har/converter.go` to calculate the delta between the end of one request and the start of the next. Emit `think_time` blocks based on this delta.
- **Execution Modes:** Add a `PacingMode` to the DSL (recorded, multiplied, random).
- **Dashboard UI:** Add a Pacing & Think Time settings panel in the `ScriptEditor`.

### 2.4 Iteration & Rendezvous Control (P2)
**Effort:** 3-4 days
- **Iteration Limits:** Update `engine.Config` to support `MaxIterations` as an alternative/addition to `Duration`.
- **Rendezvous Primitive:** Create a `sync.Cond` based rendezvous manager in `pkg/engine/vuser.go`. VUsers block at a `rendezvous` action until N users arrive or a timeout occurs.
- **DSL Update:** Add `rendezvous` action type to `pkg/dsl/types.go`.

---

## Phase 3: Enterprise Scale & Integrations (Weeks 9-14)
**Goal:** Connect to the enterprise observability ecosystem and support massive distributed scale.

### 3.1 Test Data Management (P2)
**Effort:** 5-7 days
- **Data Generators:** Add Faker-style functions to the DSL evaluation engine (`{{fake.name}}`, `{{fake.uuid}}`, `{{fake.credit_card}}`).
- **Unique Allocation Strategy:** Enhance the data pool manager to support "pop" semantics (consume row so no other VUser gets it).
- **Data Pool API:** Add endpoints to clear and append data to existing pools via API (useful for CI/CD data seeding).

### 3.2 APM / Observability Correlation (P2)
**Effort:** 5-8 days
- **Trace Injection:** Update `pkg/protocol/http/http.go` to generate W3C `traceparent` headers for every action.
- **Result Tagging:** Include the generated `trace_id` in `protocol.Result`.
- **Prometheus Exporter:** Implement a `/metrics` endpoint in the controller that exposes the live test telemetry in Prometheus text format.
- **Dynatrace/Datadog Hooks:** Use the Webhook Engine (from Phase 1.3) to push start/stop annotation events to APM event APIs.

### 3.3 Multi-Region / Geographic Distribution (P3)
**Effort:** 8-12 days
- **Generator Tags:** Update `GeneratorInventoryEntry` to include a `Region` string (passed via env var to the generator binary).
- **Load Distribution Logic:** Update `distributed_execution_backend.go` to accept a region-weighted assignment (e.g., 60% US-East, 40% EU-West) and distribute groups accordingly.
- **Metrics Tagging:** Ensure `metrics_update` and archives tag results with the source region.
- **Dashboard UI:** Add region selection sliders to the `CreateTestModal`. Add region-breakdown charts to `TestExecution`.

### 3.4 User Management & RBAC (P3)
**Effort:** 8-12 days
- **Auth Middleware:** Implement generic authentication middleware in `pkg/api/middleware.go`.
- **OIDC Integration:** Add support for OpenID Connect to integrate with Entra ID (Azure AD), Okta, etc.
- **Role Model:** Define System Admin, Project Admin, Editor, and Viewer roles.
- **Database Schema:** Add Users, Roles, and API Keys tables to the SQLite schema.
- **Dashboard UI:** Add a Login screen and an Admin/User Management panel.

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
