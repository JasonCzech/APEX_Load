# Gap Analysis: First-Class Performance Testing Harness

## What This Document Is
A comprehensive audit of APEX Load against the capabilities that define a production-grade, enterprise performance testing platform. Each gap is assessed for impact, effort, and priority. Items already built or planned in existing docs are marked accordingly.

## Audit Methodology
Compared against the combined feature sets of LoadRunner Enterprise, Gatling Enterprise, k6 Cloud, NeoLoad, and Locust — filtered to capabilities that genuinely matter for an enterprise-grade harness (not checkbox features).

---

## Capability Matrix

### ✅ Already Built

| Capability | Implementation |
|:---|:---|
| Goroutine-based VUser engine | `pkg/engine/` |
| Multi-protocol support (HTTP, SOAP, Java HTTP, JMS, Browser) | `pkg/protocol/` |
| JSON DSL scripting | `pkg/dsl/` |
| Real-time dashboard with WebSocket streaming | `dashboard/`, `pkg/api/websocket.go` |
| CLI test execution | `cmd/apexctl/` |
| REST API for test management | `pkg/api/handlers.go` |
| Script import (LoadRunner, JMeter) | `pkg/convert/` |
| Distributed multi-generator execution | `pkg/api/distributed_execution_backend.go` |
| Live VUser scaling, duration adjustment, pause/resume | `pkg/api/runner.go` |
| Transaction archives (JSONL + Parquet) | `pkg/api/archive.go` |
| System metrics (CPU, memory, disk, network) | `pkg/engine/sys_metrics.go` |
| Network latency probing | `pkg/api/` |
| T-digest percentile computation | `pkg/engine/metrics.go` |
| Telemetry pipeline with SLA monitoring | `RUNTIME_TELEMETRY_SLA_PLAN.md` |
| Capacity planning / resource estimation | `pkg/api/capacity_planning.go` |
| Browser recording studio | `RecordingStudio.tsx` |
| Script editor with visual authoring | `ScriptEditor.tsx` |
| Project/folder organization | `handlers_projects.go`, `handlers_folders.go` |
| Shared data pools | `handlers_data_pools.go` |
| Error snapshots with full request/response | `protocol.ErrorSnapshot` |
| Browser replay debugger | `BrowserReplayDebugger.tsx` |
| Settings management | `SettingsPage.tsx`, `pkg/api/settings.go` |

### 🔜 Already Planned (In Existing Docs)

| Capability | Plan |
|:---|:---|
| Mobile device emulation | `MOBILE_BROWSER_EMULATION_PLAN.md` |
| Client-side Core Web Vitals | `BROWSER_CLIENT_METRICS_PLAN.md`, `MOBILE_UX_PLATFORM_STRATEGY.md` |
| WebSocket + gRPC protocols | `DEVWEB_SUPPORT_PLAN.md` |
| DevWeb/HAR script import | `DEVWEB_SUPPORT_PLAN.md` |
| TrueClient script import | `MOBILE_BROWSER_EMULATION_PLAN.md` |
| Deep per-protocol telemetry | `PROTOCOL_TELEMETRY_PARITY_PLAN.md` |
| SLA threshold evaluation | `PROTOCOL_TELEMETRY_PARITY_PLAN.md` |
| Baseline run comparison | `PROTOCOL_TELEMETRY_PARITY_PLAN.md` |
| External LLM script generation (current engine is deterministic-local; LLM policy-gated) | `AGENTS.md` Phase 4 |
| Multi-test concurrency | `AGENTS.md` Phase 5 |
| 50k VUser HA hardening | `AGENTS.md` Phase 6 |

### ❌ Not Built, Not Planned — The Gaps

These are the capabilities missing from both the codebase and all existing plans, plus a few areas that need tighter enterprise scoping to avoid roadmap blind spots.

---

## Gap 1: CI/CD Pipeline Integration
**Impact: Critical** | **Effort: 5-8 days**

No performance testing platform is first-class without CI/CD integration. Teams need to run performance tests as automated gates in their deployment pipelines.

**What's needed:**
- **Exit codes from apexctl** — non-zero exit on SLA breach so CI pipelines fail the build
- **Machine-readable output** — `--format json` flag on apexctl for pipeline parsing
- **JUnit / test-summary output** — optional pipeline-friendly XML or summary JSON for native CI test reporting
- **GitHub Actions workflow template** — reusable action that runs a test, waits for completion, checks SLA
- **Azure DevOps pipeline task** — same for ADO (primary enterprise target per AKS deployment)
- **CLI wait / inspect flow** — `apexctl` subcommands that block on run completion and emit final SLA status cleanly
- **API polling endpoint** — `GET /api/v1/tests/{id}/status` with SLA pass/fail summary for pipeline checks

**Why it matters:** Without this, APEX Load is a manual tool. With it, every PR gets a performance gate.

---

## Gap 2: Report Generation
**Impact: High** | **Effort: 7-10 days**

The dashboard shows live data. Post-test, teams need shareable, permanent reports for stakeholders who don't have dashboard access.

**What's needed:**
- **HTML report generation** — self-contained HTML file with embedded charts and data, downloadable from dashboard or API
- **Executive summary** — one-page overview: pass/fail, key metrics, SLA status, comparison to baseline
- **Transaction detail pages** — per-transaction timing breakdown, percentile distribution, error analysis
- **Trend report** — multi-run comparison showing performance trajectory over time
- **API endpoint** — `GET /api/v1/tests/{id}/report` returns HTML or JSON summary
- **PDF export** — optional, generated from the HTML report

**What's NOT needed:** Complex BI reporting. The archive Parquet files already enable external analytics (Jupyter, Grafana, etc.).

---

## Gap 3: Scheduled / Recurring Tests
**Impact: High** | **Effort: 3-5 days**

Timed run configuration exists, but there is still no first-class recurring scheduler or policy-driven synthetic monitoring layer.

**What's needed:**
- **Cron-style scheduler** — `schedule: "0 2 * * *"` on a scenario definition
- **Recurring test management** — dashboard UI to configure, enable/disable, and view history
- **Notification on completion** — webhook or email when a scheduled test finishes
- **Retention policy** — auto-cleanup of old scheduled run results

---

## Gap 4: Alerting & Notifications
**Impact: High** | **Effort: 3-4 days**

When tests complete or SLA breaches occur, stakeholders need to know immediately.

**What's needed:**
- **Webhook integration** — configurable POST on test events (complete, failed, SLA breach)
- **Slack integration** — formatted Slack message with test summary and link to results
- **Microsoft Teams integration** — same, via incoming webhook connector
- **Email** — optional, for organizations that require it
- **Notification rules** — filter by project, severity, event type

---

## Gap 5: Correlation Intelligence
**Impact: Medium-High** | **Effort: 5-7 days**

LoadRunner's biggest competitive advantage is auto-correlation. APEX Load has extractors but no intelligence around them.

**What's needed:**
- **Auto-correlation detection** — during recording or HAR import, identify dynamic values that change between requests (session IDs, CSRF tokens, viewstate)
- **Correlation suggestion engine** — analyze request/response pairs and suggest extractors with boundaries or JSONPath
- **Validation mode** — replay a recorded script, detect correlation failures, suggest fixes
- **Integration with Agentic Engine** — use the LLM script generation pipeline to auto-correlate during import

This is the difference between "we have a converter" and "we have a migration assistant."

---

## Gap 6: Test Data Management
**Impact: Medium-High** | **Effort: 5-7 days**

Shared data pools exist but lack the sophistication for enterprise test data needs.

**What's needed:**
- **Data generation** — built-in generators for names, addresses, emails, credit cards, dates (faker-style)
- **Data masking** — obfuscate production data for safe use in test environments
- **Unique allocation** — guarantee each VUser gets unique data rows (already partially supported)
- **Data refresh** — API to reload/replace data pools between runs
- **CSV/database data sources** — connect to external data sources, not just uploaded CSVs
- **Data pool templates** — reusable data configurations across scripts

---

## Gap 7: Multi-Region / Geographic Distribution
**Impact: Medium** | **Effort: 8-12 days**

The distributed backend supports multiple generators but doesn't model geographic distribution.

**What's needed:**
- **Region labels on generators** — tag generators with location (US-East, EU-West, APAC)
- **Region-aware load distribution** — "Run 40% from US-East, 30% from EU-West, 30% from APAC"
- **Per-region metrics** — dashboard shows response time by source region
- **Latency baseline per region** — separate expected response times by region
- **Region selector in Create Scenario** — dropdown or map-based selection

**Current direction:** Generator region tags, region-weighted assignment, and inventory exposure are implemented. Prometheus now exports generator count, available capacity, and active VUsers by region, giving operators a first Grafana-ready regional visibility layer. Per-region **response** metrics are now also archived: transactions are stamped with a region (`APEXLOAD_REGION`) and the run-archive report exposes `by_region` response-time/error stats (plus a Parquet `region` column). True multi-region attribution across the gRPC boundary — each generator stamping its own region per result — remains the next step.

---

## Gap 8: APM / Observability Correlation
**Impact: Medium** | **Effort: 5-8 days**

The most powerful analysis comes from correlating load test metrics with application telemetry.

**What's needed:**
- **OpenTelemetry trace propagation** — inject `traceparent` headers into test requests so APM tools (Dynatrace, Datadog, New Relic) can link load test traffic to server-side traces
- **Trace ID in results** — store trace/span IDs alongside transaction results for cross-reference
- **Prometheus metrics export** — expose APEX Load controller metrics in Prometheus format for Grafana dashboards
- **Dynatrace event injection** — push test start/stop events to Dynatrace for timeline correlation
- **External metrics import** — pull target system metrics (CPU, memory, queue depth) during test execution

**Current direction:** HTTP and SOAP execution now propagates or preserves W3C `traceparent` headers, archives extracted `trace_id` and `span_id` values alongside transaction events in JSONL and Parquet, and allows archived transaction queries to filter by `trace_id`. Prometheus controller and generator inventory metrics are also available, including region-labeled generator capacity gauges. Lifecycle notifications can now post Dynatrace Events API v2 payloads with service entity selectors so test start, stop, pause, resume, completion, and failure events appear on APM timelines. Scripts can also declare Prometheus-format external metric scrapes under `config.observability.external_metrics`; matching samples are attached to live `resources.external_metrics`, JSONL resource archives, and the Parquet `external_metrics_json` column.

---

## Gap 9: User Management & RBAC
**Impact: High** | **Effort: 8-12 days**

Enterprise deployments need access control.

**What's needed:**
- **Authentication** — Microsoft Entra ID-backed SSO via the trusted-header gateway pattern, with APEX Load enforcing RBAC from forwarded identity and role headers
- **Role-based access** — Admin, Editor, Viewer roles at project level
- **Audit log** — who created/started/stopped which test
- **API key management** — service account keys for CI/CD pipelines
- **Team/project isolation** — teams only see their projects and results

**Current direction:** APEX Load now defaults local environments to an explicit `dev` bypass mode while keeping production aligned to Microsoft Entra ID-backed trusted-header auth. Service-account API keys, audit-event persistence, and dashboard visibility for both governance surfaces are now in place, leaving upstream identity lifecycle and broader team isolation as the remaining enterprise-auth expansion areas.

**Why it matters:** Once APEX Load is shared across teams, this becomes a rollout blocker rather than a late-stage enhancement.

---

## Gap 10: Think Time & Pacing Intelligence
**Impact: Medium** | **Effort: 2-3 days**

Think time and pacing are implemented but lack the analytical tools to tune them.

**What's needed:**
- **Think time analysis** — during recording/import, analyze real user delays and suggest think time distributions
- **Pacing recommendations** — given target TPS and VUser count, recommend iteration pacing
- **Think time replay mode** — honor recorded think times exactly during first replay, then allow override
- **Think time contribution analysis** — in results, show what % of iteration time was think time vs. transaction time

---

## Gap 11: Error Analysis & Pattern Detection
**Impact: Medium** | **Effort: 4-6 days**

Errors are captured but not analyzed.

**What's needed:**
- **Error grouping** — cluster errors by message pattern, not exact string (e.g., "connection refused to *.api.example.com" groups all hosts)
- **Error timeline** — when did each error type first appear and stop?
- **Error correlation** — which transactions produce which errors? Which VUser groups?
- **First-occurrence snapshots** — keep full request/response for the first instance of each error pattern
- **Error rate by transaction** — not just global error rate, but per-transaction error breakdown
- **Retry/recovery detection** — identify transactions that failed then succeeded (transient errors vs. persistent)

---

## Gap 12: Iteration & Rendezvous Control
**Impact: Medium** | **Effort: 3-4 days**

Advanced load modeling patterns used in enterprise testing.

**What's needed:**
- **Rendezvous points** — synchronize VUsers at a specific action before proceeding (simulate spike load)
- **Iteration control** — run exactly N iterations total (not per VUser), useful for data-driven tests
- **Goal-based iteration** — "stop after 10,000 total transactions" regardless of duration
- **Init-once execution** — run an action once across all VUsers (e.g., seed data setup), not per-VUser

---

## Gap 13: Secrets, PKI, and Secure Credential Handling
**Impact: Critical** | **Effort: 5-8 days**

Enterprise test harnesses must handle credentials, certificates, and auth artifacts safely. Without this, adoption stalls in regulated or security-conscious environments.

**What's needed:**
- **Secret references in scripts/scenarios** — refer to secrets by logical name instead of embedding them in JSON DSL
- **Runtime secret injection** — resolve secrets only at execution time and keep them out of archives and logs
- **Redaction controls** — mask secrets in dashboard views, API responses, and error snapshots
- **mTLS / client certificate handling** — support test-scoped certificates, trust bundles, and rotation
- **Vault integration** — Azure Key Vault first, plus a generic external secret-provider abstraction
- **Secret usage audit** — record who configured which secret reference and when it was used

---

## Gap 14: Multi-Tenant Governance & Capacity Controls
**Impact: High** | **Effort: 6-9 days**

The platform already has strong execution primitives, but an enterprise shared service also needs guardrails so one team cannot starve or destabilize the rest of the organization.

**What's needed:**
- **Project and team quotas** — limits on concurrent tests, VUsers, duration, and generator consumption
- **Queueing and approval policy** — hold or require approval for high-cost runs above configured thresholds
- **Reserved capacity pools** — dedicated generators or capacity slices for critical teams or environments
- **Maintenance windows and blackout periods** — prevent tests during deploy windows or regulated business periods
- **Usage accounting** — per-project historical usage for chargeback, governance, and planning
- **Fair-share scheduling** — avoid one tenant monopolizing distributed capacity

**Current direction:** Aggregate quotas are enforced today — global and per-project max concurrent tests and a per-project VUser pool, with queueing when capacity is unavailable. Per-test hard caps (`MaxVUsersPerTest`, `MaxTestDurationMinutes`) now reject oversized individual tests at creation. Remaining: cost-based approval policy, reserved capacity pools, usage accounting/chargeback, and fair-share scheduling.

---

## Gap 15: Run Provenance, Versioning, and Reproducibility
**Impact: High** | **Effort: 4-6 days**

Shareable reports are not enough if teams cannot prove exactly what was executed. Enterprise regression decisions require immutable run provenance.

**What's needed:**
- **Scenario and script revision capture** — record the exact script/scenario version used for a run
- **Settings and environment fingerprinting** — persist the thresholds, runtime settings, generator set, and environment profile used
- **Data source revisioning** — link runs to the exact uploaded data pool or external data snapshot used
- **Baseline pinning** — allow teams to explicitly mark and lock comparison baselines instead of relying only on latest prior run logic
- **Immutable run manifest** — one canonical artifact describing all inputs, config, and runtime selections
- **Re-run from manifest** — reproduce a prior run configuration directly from stored provenance

---

## Gap 16: Open vs. Closed Workload Modeling (Coordinated Omission Prevention)
**Impact: Critical** | **Effort: 6-10 days**

The current goroutine-based engine operates on a closed workload model (1 goroutine = 1 VUser). Modern systems, especially microservices, require open workload models to accurately simulate real-world traffic bursts and prevent coordinated omission.

**What's needed:**
- **Arrival-rate scheduler** — spawn ephemeral goroutines to meet exact throughput targets regardless of how fast previous requests complete
- **Throughput pacing** — "Fire 5,000 requests per second" rather than "Run 5,000 VUsers with 1s think time"
- **Coordinated omission detection** — highlight when target throughput diverges from actual throughput due to queuing

---

## Gap 17: PII / PHI Redaction & Compliance in Telemetry
**Impact: Critical** | **Effort: 4-6 days**

Enterprise environments (FinServ, Healthcare) have strict compliance requirements like GDPR and HIPAA. Storing raw payloads in Parquet files or Postgres is a massive risk.

**What's needed:**
- **Data scrubbing pipeline** — regex/entropy-based redaction of secrets, passwords, SSNs, and auth tokens before serialization to storage
- **UI masking** — ensure sensitive payloads are scrubbed before broadcasting on WebSockets to the dashboard
- **Compliance reporting** — audit logging for telemetry scrubbing policies

---

## Gap 18: Network Emulation & Traffic Shaping
**Impact: Medium-High** | **Effort: 5-8 days**

Testing from uncontended Kubernetes pods masks real-world network turbulence experienced by mobile or global users.

**What's needed:**
- **Traffic shaping engine** — apply bandwidth limits, packet loss, and jitter (via `tc` or a Go dialer)
- **Profile allocation** — run a percentage of VUsers on specific profiles (e.g., "30% 3G, 70% 4G")
- **Per-profile metrics** — break down response times by network profile

**Current direction:** Runtime network shaping is implemented for HTTP-family protocols through `config.web_runtime.network_simulation`, including preset and custom latency, bandwidth, jitter, and packet-loss settings. Results now retain a normalized `network_profile` label, with live `network_profiles` metrics and archive `by_network_profile` summaries so teams can compare response time and errors across shaped groups.

---

## Gap 19: Ephemeral Environment Target Resolution
**Impact: Medium** | **Effort: 3-5 days**

In CI/CD environments, PRs are often deployed to ephemeral namespaces where hostnames change dynamically.

**What's needed:**
- **Dynamic target injection** — runtime variables (e.g., `-env target_namespace=pr-402`) to resolve targets like `http://my-service.pr-402.svc.cluster.local`
- **DNS caching bypassing** — ensure tests to ephemeral environments pick up correctly without DNS cache poisoning issues
- **Environment mapping aliases** — logical environment mapping within scenarios

---

## Priority Ranking

| Priority | Gap | Rationale |
|:---|:---|:---|
| **P0** | CI/CD Integration | Without pipeline gates, the platform is manual-only |
| **P0** | Report Generation | Stakeholders need shareable results |
| **P0** | Secrets, PKI, and Secure Credential Handling | Enterprise adoption stalls if credentials and certificates are not managed safely |
| **P0** | Open vs. Closed Workload Modeling | Coordinated omission hides real issues in microservices |
| **P0** | PII / PHI Redaction & Compliance | Legal/regulatory blocker for saving payload snapshots |
| **P1** | Alerting & Notifications | Automation requires notification |
| **P1** | User Management & RBAC | Shared enterprise deployment needs Microsoft Entra ID-backed SSO, audit, and isolation before broad rollout |
| **P1** | Multi-Tenant Governance & Capacity Controls | A shared service needs quotas, queueing, and fair-share protection |
| **P1** | Scheduled / Recurring Tests | Enables continuous performance monitoring |
| **P1** | Correlation Intelligence | Bridges "converter" to "migration assistant" |
| **P1** | Error Analysis | Current error view is flat; patterns are invisible |
| **P1** | Run Provenance, Versioning, and Reproducibility | Regression decisions need reproducible, auditable runs |
| **P1** | Ephemeral Environment Target Resolution | Necessary for proper CI/CD PR environment testing |
| **P2** | Test Data Management | Enterprise scripts depend on sophisticated data |
| **P2** | APM Correlation | Connects load testing to observability ecosystem |
| **P2** | Think Time Intelligence | Improves script realism |
| **P2** | Iteration & Rendezvous | Unlocks advanced load patterns |
| **P2** | Network Emulation & Traffic Shaping | Real-world testing profile for mobile/global users |
| **P3** | Multi-Region Distribution | Important for global apps; distributed backend already exists |

---

## Effort Summary

| Priority | Gaps | Combined Effort |
|:---|:---|:---|
| **P0** | CI/CD + Reports + Secrets/PKI + Workload Modeling + PII Redaction | 27-42 days |
| **P1** | Alerts + RBAC + Governance + Scheduling + Correlation + Error Analysis + Provenance + Ephemeral Targets | 36-54 days |
| **P2** | Data Management + APM + Think Time + Iteration Control + Network Emulation | 20-30 days |
| **P3** | Multi-Region Distribution | 8-12 days |
| **Total** | | **91-138 days** |

---

## Recommended Delivery Waves

### Wave 1: Enterprise Foundation
**Target:** Make APEX Load safe and useful in automated delivery.

- CI/CD integration
- Report generation
- Secrets, PKI, and secure credential handling
- Ephemeral environment target resolution

**Why first:** These four close the largest adoption blockers for real delivery pipelines: trustworthy automation, shareable outcomes, safe credential handling, and dynamic targeting.

### Wave 2: Cloud-Native Engine & Compliance
**Target:** Ready the core engine for modern microservice loads and regulatory compliance.

- Open vs. closed workload modeling
- PII / PHI redaction and compliance in telemetry

**Why second:** Once CI/CD initiates tests, the engine must test them properly (avoiding coordinated omission) and ensure resulting data does not violate enterprise PII/PHI standards.

### Wave 3: Shared Platform Readiness
**Target:** Turn APEX Load from a powerful tool into a governable shared service.

- User management and RBAC
- Multi-tenant governance and capacity controls
- Alerting and notifications
- Scheduled / recurring tests
- Run provenance, versioning, and reproducibility

**Why third:** Once teams can automate runs securely, the next failure mode is operational chaos. This wave adds the controls, auditability, and repeatability needed for a widespread enterprise rollout.

### Wave 4: Script Quality and Analysis Depth
**Target:** Improve migration success and shorten diagnosis time.

- Correlation intelligence
- Error analysis and pattern detection
- Test data management
- Think time and pacing intelligence

**Why fourth:** These capabilities increase script realism, reduce maintenance toil, and improve analyst productivity, but they depend on the platform foundation already being reliable and governable.

### Wave 5: Ecosystem and Advanced Modeling
**Target:** Expand APEX Load into broader observability and globally distributed testing use cases.

- APM / observability correlation
- Iteration and rendezvous control
- Network emulation & traffic shaping
- Multi-region / geographic distribution

**Why fifth:** These are valuable expansion capabilities, but they become much easier to productize once the core enterprise platform workflows are already in place.

---

## What's Already Strong

Before adding anything, it's worth recognizing that APEX Load already has capabilities that many competitors lack:

- **Goroutine-per-VUser model** — fundamentally more efficient than thread-based or process-based engines
- **Distributed execution with live aggregation** — the distributed gRPC backend is functional (scale targets remain unbenchmarked)
- **Browser protocol with recording studio** — built-in proxy recorder for HTTP/WS; browser VUsers are diagnostic canaries, not a TruClient-class DOM recorder
- **Assisted script authoring** — deterministic draft/repair/explain over the DSL, exposed via the MCP server. NOTE: this is a deterministic-local provider, **not** an LLM; external LLM integration is policy-gated and not wired by default. Do not market as "LLM-powered."
- **Unified DSL** — one script format across all protocols, not per-protocol languages
- **Real-time pause/resume with transaction integrity** — "soft pause" is a feature most competitors don't have
- **Parquet archives** — native analytics-friendly format, not proprietary binary
- **MCP server** — agent integration layer exposing authoring/run tools

The gaps above are about operational completeness (CI/CD, reports, scheduling, alerts), enterprise controls (security, governance, reproducibility), and analytical depth (correlation, error patterns, APM), not architectural deficiencies. The engine and protocol foundation are solid.
