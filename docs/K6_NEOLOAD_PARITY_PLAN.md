# k6 and NeoLoad Competitive Parity Plan

## Objective

Close Stormrunner's most important competitive gaps against Grafana k6 and Tricentis NeoLoad without copying either product screen-for-screen.

This plan complements:

- `docs/LOADRUNNER_PARITY_EXECUTION_PLAN.md` for LoadRunner migration parity
- `docs/ENTERPRISE_PLATFORM_PARITY_EXECUTION_PLAN.md` for Stormrunner's shared-platform hardening
- `docs/PLATFORM_GAP_IMPLEMENTATION_PLAN.md` for general enterprise harness gaps
- `docs/MOBILE_BROWSER_EMULATION_PLAN.md` and `docs/BROWSER_CLIENT_METRICS_PLAN.md` for browser/client UX telemetry

The goal is not to become "k6 with a dashboard" or "NeoLoad with Kubernetes." The target is a credible enterprise alternative with Stormrunner's own strengths: Kubernetes-native execution, high-density Go VUsers, open telemetry, strong import paths, governed scheduling, and first-class evidence archives.

## External Competitive Baseline

Primary references used for this plan:

- Grafana Cloud k6 introduction: https://grafana.com/docs/grafana-cloud/testing/k6/introduction/
- k6 browser API: https://grafana.com/docs/k6/latest/javascript-api/k6-browser/
- Grafana Cloud k6 Test Builder: https://grafana.com/docs/grafana-cloud/testing/k6/author-run/test-builder/
- Tricentis NeoLoad features: https://www.tricentis.com/products/performance-testing-neoload/features
- Tricentis NeoLoad overview: https://docs.tricentis.com/neoload-2025.3/en-us/content/get_started/about_tricentis_neoload.htm
- Tricentis NeoLoad system requirements for specialized protocols: https://docs.tricentis.com/neoload-latest/en-us/content/get_started/system_requirements.htm

## Current Stormrunner Position

Stormrunner is already credible in several areas:

- Controller API, dashboard, CLI, REST APIs, WebSocket live metrics, and archive/report endpoints
- Local and distributed generator execution with regional placement and capacity guidance
- HTTP, SOAP, Java-over-HTTP, browser, JMS, WebSocket, and gRPC authoring/import paths
- LoadRunner, TrueClient MobileWeb, JMeter, DevWeb, HAR, sample, SOAP, and Java-over-HTTP import workflows
- Scenario authoring, recurring schedules, runtime overrides, SLA/goal status, and CI/CD-friendly guardrails
- Auth/RBAC, service accounts, audit feeds, service settings, and operator readiness surfaces
- Browser replay, browser device profiles, browser client metrics, archive drilldown, and comparison views
- JSONL/Parquet archives, standalone HTML reports, CSV exports, provenance manifests, and release trends

The remaining gap is product maturity: competitive polish, ecosystem depth, packaged integrations, operator evidence, and specialized enterprise coverage.

## Where Stormrunner Comes Up Short

| Gap | k6 Advantage | NeoLoad Advantage | Stormrunner Shortfall |
| --- | --- | --- | --- |
| Managed execution | Grafana Cloud k6 owns scaling, distributed zones, metric storage, and frontend operations | NeoLoad has established controller/load-generator deployment patterns | Stormrunner is self-hosted first; operators own control-plane operations, HA, and evidence publication |
| Script-as-code ecosystem | k6 JavaScript API, local/cloud portability, extensions, checks, thresholds, and broad community examples | Supports enterprise tester workflows more than developer scripting | Stormrunner's JSON DSL is controlled and importable, but less familiar and less extensible than k6 JS |
| Graphical test builder maturity | k6 Test Builder covers scenarios, requests, checks, variables, groups, load zones, and thresholds | NeoLoad Designer/recording workflows are mature and enterprise-oriented | Stormrunner authoring is improving but still needs a calmer, guided, source-agnostic workflow |
| Browser testing maturity | k6 browser API is Playwright-inspired and documented around Chromium/device usage | NeoLoad RealBrowser is commercially established | Stormrunner browser VUsers are positioned as diagnostic canaries; density and fidelity need more evidence |
| Specialized enterprise protocols | k6 relies on extensions and code-level flows | NeoLoad covers specialized enterprise surfaces such as RealBrowser, Citrix, and SAP GUI | Stormrunner has strong HTTP/JMS/SOAP/API coverage but does not yet match NeoLoad's legacy enterprise breadth |
| Observability and root-cause | k6 pairs naturally with Grafana observability | NeoLoad packages APM, infrastructure monitoring, SLA validation, and augmented analysis | Stormrunner has OTel, Prometheus, archives, trends, and diagnostics, but less packaged root-cause guidance |
| Marketplace and integrations | k6 has extensions, outputs, Grafana ecosystem, and common CI patterns | NeoLoad has CI/CD, APM, Tosca, APIs, and enterprise SDLC integrations | Stormrunner needs more official connectors, examples, templates, and supported integration contracts |
| Benchmark confidence | k6 and NeoLoad have market-proven operating envelopes | NeoLoad has established enterprise deployment history | Stormrunner has runbooks and advisory envelopes, but fewer published, repeatable benchmark artifacts |
| Enterprise UX polish | k6 Cloud is focused and developer-friendly | NeoLoad is designed for performance engineering teams | Stormrunner still has rough edges in navigation, authoring, report interpretation, and operator workflows |

## Positioning Decision

Stormrunner should compete as:

1. A Kubernetes-native enterprise performance testing platform.
2. A self-hosted alternative for teams that want control-plane ownership, open telemetry, and auditable archives.
3. A migration target for LoadRunner/JMeter/DevWeb/HAR assets.
4. A hybrid load model: high-density protocol VUsers plus low-concurrency browser/client UX canaries.

Stormrunner should not claim:

- High-density browser load parity until benchmark evidence supports it.
- Full NeoLoad Citrix/SAP GUI parity unless those protocols become explicit customer-funded work.
- Drop-in k6 script compatibility unless a real k6 importer/bridge is implemented and tested.

## Delivery Roadmap

### Phase C1: Competitive Evidence And Support Matrix

**Goal:** Make Stormrunner's current competitive position machine-readable and release-reviewable.

| ID | Work Item | Priority | Status |
| --- | --- | --- | --- |
| C1.1 | Add a `competitive_parity` section to `/api/v1/system/parity-status` covering k6 and NeoLoad dimensions | P0 | Complete for backend contract |
| C1.2 | Publish a docs-owned support matrix for k6, NeoLoad, LoadRunner, JMeter, DevWeb, HAR, and native Stormrunner flows | P0 | Complete |
| C1.3 | Add dashboard operator view for competitive support status, evidence level, and known limitations | P1 | Complete for Monitoring card |
| C1.4 | Link every parity claim to code, tests, docs, or explicit external evidence requirements | P1 | Complete for current competitive dimensions |

Acceptance criteria:

- Every "supported" competitive claim has a code/test/docs evidence pointer.
- Every partial item has explicit limitations and a next action.
- Browser and protocol density claims are separated from feature support claims.

Implementation notes:

- `GET /api/v1/system/parity-status` now includes `competitive_parity`, a machine-readable list of k6 and NeoLoad competitive dimensions with status, competitors, summary, evidence pointers, remaining work, product decision, and evidence level.
- The first backend contract deliberately marks browser load and specialized enterprise protocols as bounded or gap areas rather than treating them as supported claims.
- The Monitoring dashboard now renders a Competitive Parity card with gap, partial, external-evidence, known-limitation, competitor, and per-dimension evidence-level details.
- `docs/COMPETITIVE_SUPPORT_MATRIX.md` is now the docs-owned source of truth for source/import flows, protocol/runtime support, load modeling, operations, monitoring, reporting, and evidence claim rules across k6, NeoLoad, LoadRunner, JMeter, DevWeb, HAR, and native Stormrunner flows.
- The API test suite now verifies that each `competitive_parity` dimension is documented in the matrix and carries evidence level, decision, and remaining-work fields.

### Phase C2: k6-Aligned Developer Experience

**Goal:** Close the largest k6 gap: developer-friendly script-as-code workflows.

| ID | Work Item | Priority | Status |
| --- | --- | --- | --- |
| C2.1 | Add `stormctl script validate --format json` and `stormctl script explain` for local authoring feedback | P0 | Not started |
| C2.2 | Add first-class checks/threshold authoring in the script and scenario editors, mapped to existing goal/SLA contracts | P0 | Partial via goal/SLA backend |
| C2.3 | Add k6 result import so teams can centralize k6 JSON summaries and trend them in Stormrunner reports | P1 | Complete via C4 external result ingestion API and dashboard import evidence |
| C2.4 | Add k6-to-Stormrunner conversion MVP for HTTP scripts: requests, groups, checks, thresholds, sleep, and basic env vars | P1 | Not started |
| C2.5 | Add Stormrunner-to-k6 export for simple HTTP smoke tests to help developer teams adopt gradually | P2 | Not started |
| C2.6 | Add official examples for API smoke, staged ramp, arrival-rate, threshold-gated CI, and browser canary patterns | P0 | Partial via templates |

Acceptance criteria:

- Developers can author and validate Stormrunner scripts locally without opening the dashboard.
- k6 teams can import historical k6 output into Stormrunner trends.
- A constrained k6 HTTP subset can be converted with clear unsupported warnings.

### Phase C3: Managed Scale And Load-Zone Experience

**Goal:** Close the k6 Cloud gap around distributed zones and managed operational simplicity.

| ID | Work Item | Priority | Status |
| --- | --- | --- | --- |
| C3.1 | Promote regions into named load zones with capacity, protocol support, saturation, and cost metadata | P0 | Partial via region-tagged generators |
| C3.2 | Add load-zone selection UX with manual weights, capacity warnings, and projected generator placement | P0 | Partial |
| C3.3 | Add private/public generator pool model with reserved capacity and quota policy | P1 | Partial via project quotas and capacity |
| C3.4 | Add KEDA/HPA reference deployment with production dashboards and alert rules | P1 | Partial via manifests/docs |
| C3.5 | Publish benchmark-backed density envelopes by protocol, region, and generator class | P0 | Partial, evidence pending |
| C3.6 | Add "managed-like" setup profile: one command/script to deploy controller, generators, storage, and dashboards to a Kubernetes namespace | P1 | Not started |

Acceptance criteria:

- A user can select zones before running, see capacity fit, and understand placement decisions.
- Operators can prove what each generator class supports and how many VUsers are safe.
- Stormrunner has a repeatable reference deployment that feels packaged, not assembled by hand.

### Phase C4: NeoLoad-Grade Analysis And Integrations

**Goal:** Close NeoLoad's strongest advantage: packaged analysis, APM, infrastructure monitoring, and governance reporting.

| ID | Work Item | Priority | Status |
| --- | --- | --- | --- |
| C4.1 | Add APM integration profiles for Dynatrace, Datadog, New Relic, AppDynamics, and generic OpenTelemetry correlation | P0 | Complete: `/api/v1/integrations/apm/profiles`, Monitoring dashboard APM card |
| C4.2 | Add infrastructure monitor packs for Kubernetes, Linux hosts, JVM, database, web server, and queue brokers | P0 | Complete: `/api/v1/monitoring/packs`, Monitoring dashboard monitor-pack card |
| C4.3 | Add root-cause summary engine that correlates run regressions with dependency, resource, telemetry, and browser signals | P1 | Complete: `/api/v1/tests/{id}/archive/root-cause`, Results root-cause action |
| C4.4 | Add SLA governance dashboard: release pass/fail, breach history, owner, waiver, and audit trail | P1 | Complete: `/api/v1/releases/{id}/sla-governance`, waiver endpoint, Results SLA gate card |
| C4.5 | Add report profiles that produce executive, engineer, and audit exports with different section defaults | P1 | Complete: `/api/v1/reports/profiles` defines executive, engineer, and audit section/export defaults |
| C4.6 | Add external result ingestion for JMeter, Gatling, and k6 to centralize trend reporting | P1 | Complete: `/api/v1/results/import`, `/api/v1/results/imports`, Results external import card |

Acceptance criteria:

- Reports answer "what regressed, where, and why" without requiring manual archive spelunking.
- APM/infrastructure data appears alongside Stormrunner measurements in live and archived views.
- Release governance can use Stormrunner as the system of record for performance readiness.

### Phase C5: Authoring UX And Recording Parity

**Goal:** Close the gap with k6 Test Builder and NeoLoad Designer for day-to-day authoring.

| ID | Work Item | Priority | Status |
| --- | --- | --- | --- |
| C5.1 | Simplify Create Script, Edit Script, New Test, and New Scenario into guided task flows with fewer standing cards | P0 | Complete: Create Script uses workflow lanes, ScriptEditor keeps replay/RTS/config/parameter work in dialogs, New Test focuses scenario selection/review, and scenario management uses explicit duplicate/delete dialogs |
| C5.2 | Add scenario-first wizard: choose goal, traffic model, scripts, zones, thresholds, schedule, then review | P0 | Complete: the Scenario builder guides goal, load model, scripts, capacity/assignment, thresholds/runtime defaults, and review before test creation or scheduling |
| C5.3 | Add recorder-to-script review queue with approval, replay evidence, and promotion to reusable asset | P1 | Complete: authoring sessions persist recorded/migrated/generated drafts, the Scripts dashboard lists a review queue, and approval promotes ready drafts through the script library endpoint |
| C5.4 | Add guided correlation/parameter repair as a unified authoring lane across imports, recorder, and manual scripts | P1 | Complete: session correlation preview/apply endpoints and dashboard controls share extractor insertion, sample replacement diffs, parameter data fit, and health scoring across sources |
| C5.5 | Add visual diff and provenance review before saving AI-generated or migrated scripts | P1 | Complete: validation returns provenance/AI guardrail evidence, correlation repairs return before/after diffs, and approval is blocked until validation, review, and replay evidence are ready |
| C5.6 | Add reusable component library for request groups, auth flows, login transactions, data pools, and teardown blocks | P2 | Complete: `/api/v1/scripts/authoring/components` and the Create Script component library expose reusable request groups, auth flows, login transactions, data pools, and teardown blocks |

Acceptance criteria:

- A new user can create a realistic test without knowing Stormrunner's DSL first.
- Advanced controls are discoverable but do not dominate the first screen.
- Imported/generated/recorded scripts all use the same validation, replay, repair, and approval model.

### Phase C6: Specialized Enterprise Protocol Decisions

**Goal:** Decide where to pursue NeoLoad-style specialized protocol parity and where to declare non-goals.

| ID | Work Item | Priority | Status |
| --- | --- | --- | --- |
| C6.1 | Create customer-backed demand assessment for SAP GUI, Citrix, Oracle Forms, desktop UI, and packaged-app protocols | P0 | Not started |
| C6.2 | Define extension architecture for out-of-process protocol workers when compiled-in Go plugins are insufficient | P1 | Not started |
| C6.3 | Add provider-specific JMS/broker diagnostics for queue depth, DLQ, consumer lag, and broker health | P1 | Partial |
| C6.4 | Add SAP/OData/API templates before native SAP GUI protocol work | P1 | Not started |
| C6.5 | Publish explicit non-goals if Citrix/SAP GUI are not funded, with migration guidance to external tools | P0 | Not started |

Acceptance criteria:

- Stormrunner does not silently appear weaker because of ambiguous unsupported enterprise protocols.
- Specialized protocol work is gated by real user demand and architecture fit.
- Where native support is not planned, docs provide integration or coexistence guidance.

### Phase C7: Competitive Packaging, Samples, And Trust

**Goal:** Make Stormrunner feel credible before a prospect reads the code.

| ID | Work Item | Priority | Status |
| --- | --- | --- | --- |
| C7.1 | Publish canonical sample projects: e-commerce HTTP, browser canary, JMS order flow, SOAP service, gRPC, WebSocket, and mixed workload | P0 | Partial |
| C7.2 | Add one-page "Stormrunner vs k6 vs NeoLoad" matrix with honest support/evidence levels | P0 | Not started |
| C7.3 | Add release notes that include benchmark deltas, support-matrix changes, and known limitations | P1 | Not started |
| C7.4 | Add operator demo scripts that seed projects, scripts, scenarios, schedules, and sample archives | P1 | Not started |
| C7.5 | Add dashboard onboarding checklist with deployment readiness, first script, first scenario, first report, and first CI gate | P1 | Not started |

Acceptance criteria:

- A buyer/evaluator can reproduce a meaningful demo in under one hour.
- Sample outputs show the report, archive, telemetry, and governance story.
- Claims are compelling because they are evidence-backed, not inflated.

## Implementation Order

Recommended sequence:

1. C1 Competitive Evidence And Support Matrix
2. C5 Authoring UX And Recording Parity
3. C2 k6-Aligned Developer Experience
4. C3 Managed Scale And Load-Zone Experience
5. C4 NeoLoad-Grade Analysis And Integrations
6. C7 Competitive Packaging, Samples, And Trust
7. C6 Specialized Enterprise Protocol Decisions

Rationale:

- Evidence and UX unblock every later sales/demo conversation.
- k6-aligned developer workflows are the fastest way to improve adoption.
- Load-zone and benchmark work prevents overclaiming scale.
- NeoLoad-style analysis requires mature archive, telemetry, and external metric contracts.
- Specialized protocols should be demand-gated because they can consume entire product cycles.

## Cross-Cutting Test Plan

- Unit tests for k6 import/export mappers, threshold conversion, load-zone placement, and support-matrix status calculation.
- API tests for parity status, support matrix, external result ingestion, APM profiles, monitor packs, and benchmark metadata.
- Dashboard tests for support matrix, authoring wizard flows, load-zone selection, root-cause cards, and report profiles.
- CLI tests for local validation/explain flows and k6 conversion commands.
- Browser smoke tests for Create Script, Edit Script, New Scenario, Results, and parity checklist flows.
- Benchmark publication checks that fail release verification when claims lack a referenced artifact.

## Success Metrics

| Metric | Target |
| --- | --- |
| Time to first useful test | Under 15 minutes from clean local/controller setup |
| Time to first CI gate | Under 30 minutes using a documented template |
| Competitive support matrix coverage | 100% of listed claims tied to evidence |
| k6 HTTP subset conversion | At least 80% conversion success on curated examples |
| Load-zone placement explainability | 100% of queued runs include placement/capacity reason codes |
| Report usefulness | Every completed run has executive, engineer, and audit-ready summaries |
| Browser density claims | No published increase without benchmark artifact and hardware metadata |

## Non-Goals Until Explicitly Approved

- Full k6 JavaScript runtime compatibility.
- Native SAP GUI or Citrix execution.
- High-density browser VUser claims.
- A hosted Stormrunner SaaS control plane.
- Replacing Grafana, Datadog, Dynatrace, or other observability systems.

## Definition Of Competitive Parity

Stormrunner reaches practical competitive parity when:

1. Developer teams can adopt it as easily as they can adopt k6 for API and CI smoke workloads.
2. Performance engineers can author, record, migrate, repair, replay, schedule, and report tests without falling into raw JSON for common workflows.
3. Operators can deploy and scale it with repeatable Kubernetes evidence.
4. Release managers can trust its trend, SLA, audit, and executive reports.
5. Browser support is honestly positioned as high-fidelity diagnostic canaries until benchmark evidence justifies more.
6. Specialized protocol limitations are explicit, not surprising.
