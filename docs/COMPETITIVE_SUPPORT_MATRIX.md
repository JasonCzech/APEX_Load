# Competitive Support Matrix

This matrix is the docs-owned source of truth for Stormrunner's current support claims against k6, NeoLoad, LoadRunner, JMeter, DevWeb, HAR, and native Stormrunner flows.

It complements `docs/K6_NEOLOAD_PARITY_PLAN.md` and feeds the competitive parity evidence exposed by `GET /api/v1/system/parity-status`.

## Evidence Baseline

External baseline references:

- Grafana Cloud k6 introduction: https://grafana.com/docs/grafana-cloud/testing/k6/introduction/
- k6 browser API: https://grafana.com/docs/k6/latest/javascript-api/k6-browser/
- Grafana Cloud k6 Test Builder: https://grafana.com/docs/grafana-cloud/testing/k6/author-run/test-builder/
- Tricentis NeoLoad features: https://www.tricentis.com/products/performance-testing-neoload/features
- Tricentis NeoLoad overview: https://docs.tricentis.com/neoload-2025.3/en-us/content/get_started/about_tricentis_neoload.htm
- Tricentis NeoLoad system requirements and specialized protocol sizing: https://docs.tricentis.com/neoload-latest/en-us/content/get_started/system_requirements.htm

Internal Stormrunner evidence references:

- `README.md`
- `API_AND_DSL_REFERENCE.md`
- `docs/LOADRUNNER_RTS_REFERENCE.md`
- `docs/ENTERPRISE_AUTHORING_CONTRACT.md`
- `docs/ENTERPRISE_GENERATOR_FLEET_MODEL.md`
- `docs/ENTERPRISE_LOAD_MODEL_CONTRACT.md`
- `docs/MOBILE_BROWSER_EMULATION_PLAN.md`
- `docs/BROWSER_CLIENT_METRICS_PLAN.md`
- `docs/RUNTIME_TELEMETRY_SLA_PLAN.md`
- `docs/K6_NEOLOAD_PARITY_PLAN.md`
- `docs/WEB_API_EXECUTION_SUPPORT_MATRIX.md`

## Status Vocabulary

| Status | Meaning |
| --- | --- |
| Supported | Stormrunner has an implemented runtime, API, or dashboard workflow and code/test/docs evidence. |
| Partial | Stormrunner has meaningful support, but one or more runtime, UX, evidence, or scale claims remain incomplete. |
| Import/review | Stormrunner can ingest or preserve the asset for review, migration, or conversion, but it is not a full native runtime equivalent. |
| Evidence required | The feature exists, but release or sales claims need benchmark, deployment, or external integration artifacts before being treated as production-grade parity. |
| Gap | Stormrunner does not currently provide equivalent support. |
| Non-goal | Stormrunner should not claim this without a new product decision. |

## Competitive Parity API Coverage

`GET /api/v1/system/parity-status` exposes these competitive dimensions. Each dimension is intentionally backed by this matrix so dashboard and release-review claims stay aligned with docs.

| API dimension | Status | Matrix coverage | Claim rule |
| --- | --- | --- | --- |
| Developer Script-As-Code Experience | Partial | Native Stormrunner JSON DSL, k6 script import/export, k6 result import, thresholds/SLAs | Stormrunner differentiates with controlled JSON DSL and CI APIs, but must not imply k6 JavaScript compatibility until import/export is implemented. |
| Managed Scale And Load Zones | Partial | Distributed execution and load zones, live metrics, OpenTelemetry and Prometheus | Stormrunner can demonstrate fleet capacity and Kubernetes-native placement, but named load zones and benchmark envelopes remain evidence gates. |
| Authoring, Recording, And Guided Repair | Supported for C5 scope | Dashboard script creation, reusable component library, LoadRunner Web HTTP/HTML, TrueClient Mobile Web, HAR import, raw HTTP/cURL/REST samples | Stormrunner can show multi-source acquisition, review, repair, replay, approval, and reusable component workflows. Remaining evidence is richer public examples and competitive demo packs. |
| Browser Testing And Client UX Evidence | Partial | Browser UX canaries, high-density browser load, TrueClient Mobile Web | Browser canaries are supported; high-density browser load remains a separate external-evidence requirement. |
| Analysis, APM, And Root-Cause Integrations | Supported for C4 scope | OpenTelemetry and Prometheus, archive reports and comparison, root-cause and augmented analysis | Stormrunner packages APM profiles, infrastructure monitor packs, root-cause summaries, SLA governance, report profiles, and external result ingestion; deeper vendor-specific causality remains an evidence expansion. |
| Specialized Enterprise Protocol Breadth | Gap | SAP GUI, Citrix, Oracle Forms, desktop UI | These are demand-gated gaps/non-goals unless a customer-backed product decision funds them. |
| Packaging, Samples, And Trust | Partial | Competitive claim rules plus source/protocol/runtime matrices | Public examples, one-command demos, and benchmark artifacts are required before broad sales claims. |

## Source And Authoring Flow Matrix

| Source flow | Stormrunner status | Supported path | Evidence | Remaining gap |
| --- | --- | --- | --- | --- |
| Native Stormrunner JSON DSL | Supported | Author, validate, replay, run, archive, report, and schedule JSON DSL scripts. | `pkg/dsl`, `/api/v1/scripts`, `/api/v1/tests`, `API_AND_DSL_REFERENCE.md` | Improve local explain output and more first-party examples. |
| Dashboard script creation | Supported for C5 scope | Guided script creation, backend templates, reusable component library, validation, replay evidence, runtime settings dialogs, and data-source fit surfaces. | `dashboard/src/components/ScriptCreateOptions.tsx`, `dashboard/src/components/ScriptEditor.tsx`, `docs/ENTERPRISE_AUTHORING_CONTRACT.md` | Expand packaged demo projects and insertion affordances for reusable components. |
| LoadRunner Web HTTP/HTML | Supported for planned RTS scope | ZIP import, runtime setting classification, migration readiness, validation, replay, and support matrix review. | `/api/v1/scripts/import/loadrunner`, `docs/LOADRUNNER_RTS_REFERENCE.md`, `pkg/convert/loadrunner` | Long-tail historical protocol families remain outside the planned LoadRunner parity scope. |
| TrueClient Mobile Web | Partial | LoadRunner acquisition, mobile web migration review, browser replay readiness, unsupported-step visibility, and repair guidance. | `docs/MOBILE_BROWSER_EMULATION_PLAN.md`, `docs/BROWSER_CLIENT_METRICS_PLAN.md` | Browser VUsers remain diagnostic canaries; desktop TrueClient-style parity is out of scope. |
| JMeter plan import | Supported for planned import scope | JMeter plan conversion, save, validate, support matrix endpoints, and external result ingestion. | `/api/v1/scripts/import/jmeter`, `/api/v1/scripts/import/jmeter/support-matrix`, `/api/v1/results/import`, `docs/JMETER_IMPORT_PLAN.md` | Expand JMeter fixture coverage and dashboard trend overlays. |
| OpenAPI import | Supported | OpenAPI 3.x and Swagger 2.0 JSON/YAML import into reviewable HTTP DSL drafts with auth hints, parameter data-source seeds, request body templates, and status assertions. | `/api/v1/scripts/import/openapi`, `pkg/convert/openapi`, `docs/WEB_API_EXECUTION_SUPPORT_MATRIX.md` | Add dashboard guided create flow and customer-grade example packs. |
| WSDL import | Supported | WSDL import into SOAP DSL drafts with operation discovery, SOAPAction preservation, namespace mapping, sample envelopes, and payload review sections. | `/api/v1/scripts/import/wsdl`, `pkg/convert/wsdl`, `docs/WEB_API_EXECUTION_SUPPORT_MATRIX.md` | Complex XSD payload synthesis remains review-scaffolded. |
| DevWeb import | Supported for current import-review scope | DevWeb conversion into Stormrunner DSL for migration review and execution where supported constructs map cleanly, with a construct-by-construct support matrix. | `/api/v1/scripts/import/devweb`, `/api/v1/scripts/import/devweb/support-matrix`, `pkg/convert/devweb`, `docs/WEB_API_EXECUTION_SUPPORT_MATRIX.md` | Dynamic JavaScript runtime compatibility remains review-only. |
| HAR import | Supported for HTTP acquisition | HAR and sample import to JSON DSL with correlation/base URL readiness. | `/api/v1/scripts/import/sample`, `pkg/convert/har`, `docs/ENTERPRISE_AUTHORING_CONTRACT.md` | Add more guided repair affordances for parameterization and correlation. |
| Raw HTTP, cURL, REST samples | Supported | Sample import builds executable Stormrunner scripts from common request formats. | `/api/v1/scripts/import/sample`, `pkg/convert/sample` | Expand canonical sample projects and docs. |
| k6 script import/export | Gap | No native k6 JavaScript runtime, importer, or exporter exists today. | `docs/K6_NEOLOAD_PARITY_PLAN.md#phase-c2-k6-aligned-developer-experience` | Implement constrained HTTP subset conversion and simple Stormrunner-to-k6 smoke-test export. |
| k6 result import | Supported for summary/trend ingestion | k6 JSON summary payloads can be imported and listed as release-scoped external results. | `/api/v1/results/import`, `/api/v1/results/imports`, `docs/K6_NEOLOAD_PARITY_PLAN.md#phase-c4-neoload-grade-analysis-and-integrations` | Add richer k6 trend overlays after the C2 script conversion/export work is complete. |
| Gatling result import | Supported for summary/trend ingestion | Gatling JSON-style summary payloads can be imported and listed as release-scoped external results. | `/api/v1/results/import`, `/api/v1/results/imports`, `docs/K6_NEOLOAD_PARITY_PLAN.md#phase-c4-neoload-grade-analysis-and-integrations` | Add Gatling-specific fixture packs and comparison chart overlays. |
| NeoLoad project import | Gap | No NeoLoad project/YAML importer exists today. | `docs/K6_NEOLOAD_PARITY_PLAN.md#phase-c6-specialized-enterprise-protocol-decisions` | Demand-gate any importer work; prioritize coexistence and external result ingestion first. |

## Protocol And Runtime Support Matrix

| Capability | Stormrunner status | k6 baseline | NeoLoad baseline | Stormrunner evidence | Remaining gap |
| --- | --- | --- | --- | --- | --- |
| HTTP/HTTPS protocol load | Supported | Strong k6 OSS and Cloud path. | Strong protocol-based path. | `pkg/protocol/http`, `pkg/engine`, `docs/LOADRUNNER_RTS_REFERENCE.md` | Publish repeatable density benchmark artifacts. |
| REST/OpenAPI API testing | Supported | Script-as-code and Test Builder HTTP workflows. | REST and Swagger/OpenAPI-assisted API design. | `/api/v1/scripts/import/openapi`, `/api/v1/scripts/import/sample`, `pkg/convert/openapi`, `docs/WEB_API_EXECUTION_SUPPORT_MATRIX.md` | Add dashboard guided create flow and richer customer examples. |
| SOAP/WSDL | Supported | Possible through HTTP scripting, not a primary k6 differentiator. | SOAP/Web Services support. | `/api/v1/scripts/import/wsdl`, `pkg/protocol/soap`, `pkg/convert/wsdl`, `docs/WEB_API_EXECUTION_SUPPORT_MATRIX.md` | Complex XSD payload generation remains scaffolded for review. |
| Java-over-HTTP | Supported | Not a primary k6 built-in workflow. | Enterprise protocol-style coverage. | `pkg/protocol/java_http`, `examples/web-api`, `docs/WEB_API_EXECUTION_SUPPORT_MATRIX.md` | Add codec-specific enterprise fixtures as demand appears. |
| JMS and broker workflows | Partial | k6 typically relies on extensions or code-level flows. | Enterprise messaging/integration workflows. | `pkg/protocol/jms`, `examples/web-api/jms-order-flow.json`, `docs/WEB_API_EXECUTION_SUPPORT_MATRIX.md` | Real broker execution remains controlled by adapter availability and environment setup. |
| WebSocket | Supported | k6 supports WebSocket scripting. | NeoLoad covers WebSocket/server push technologies. | `pkg/protocol/websocket`, `examples/web-api/websocket-order-feed.json`, `docs/WEB_API_EXECUTION_SUPPORT_MATRIX.md` | Add more subscription-protocol assertion fixtures. |
| gRPC | Supported | k6 supports gRPC scripting. | API testing support varies by NeoLoad version/workflow. | `pkg/protocol/grpc`, `examples/web-api/grpc-inventory.json`, `docs/WEB_API_EXECUTION_SUPPORT_MATRIX.md` | Add service-specific proto fixtures and metadata examples. |
| Browser UX canaries | Partial | k6 browser APIs are Playwright-inspired and use Chromium/device emulation. | NeoLoad RealBrowser is a packaged browser-based testing capability. | `/api/v1/scripts/browser-replay/sessions`, `/api/v1/system/browser-benchmarks`, `docs/BROWSER_BENCHMARK_ARTIFACTS.md`, `docs/MOBILE_BROWSER_EMULATION_PLAN.md`, `docs/BROWSER_CLIENT_METRICS_PLAN.md` | Attach controlled raw benchmark artifacts per release generator image before raising density envelopes. |
| High-density browser load | Non-goal until evidence | k6 browser exists, but browser load remains resource intensive compared with protocol VUsers. | NeoLoad publishes RealBrowser sizing guidance and positions RealBrowser as a packaged workflow. | `/api/v1/system/browser-benchmarks`, `docs/BROWSER_BENCHMARK_ARTIFACTS.md`, `docs/MOBILE_BROWSER_EMULATION_PLAN.md`, `docs/K6_NEOLOAD_PARITY_PLAN.md` | Do not claim high-density browser parity without hardware-specific benchmark evidence. |
| SAP GUI, Citrix, Oracle Forms, desktop UI | Gap/non-goal | Outside core k6 OSS model unless implemented through external tooling/extensions. | NeoLoad documents specialized features such as RealBrowser, Citrix, SAP GUI, and Oracle Forms. | `docs/K6_NEOLOAD_PARITY_PLAN.md#phase-c6-specialized-enterprise-protocol-decisions`, `docs/BROWSER_CLIENT_METRICS_PLAN.md#non-goals` | Complete demand assessment and publish coexistence guidance or funded protocol plans. |

## Load Modeling, Execution, And Operations Matrix

| Capability | Stormrunner status | k6 baseline | NeoLoad baseline | Stormrunner evidence | Remaining gap |
| --- | --- | --- | --- | --- | --- |
| VUser/concurrency models | Supported | k6 executors cover scripted VU and arrival-style models. | NeoLoad supports configured virtual users and scenario behavior. | `docs/ENTERPRISE_LOAD_MODEL_CONTRACT.md`, `/api/v1/scenarios/validate-load-model` | Add more examples that explain model choice. |
| Staged ramps and arrival modeling | Supported | k6 supports staged and arrival-rate executor patterns. | NeoLoad supports ramp-up semantics through GUI and YAML approaches. | `docs/ENTERPRISE_LOAD_MODEL_CONTRACT.md`, `pkg/engine` | Publish benchmark-backed achieved-versus-configured goal evidence. |
| Thresholds, SLAs, and release gates | Partial | k6 thresholds and checks are first-class script concepts. | NeoLoad includes SLA/threshold validation and release-friendly reporting. | `docs/RUNTIME_TELEMETRY_SLA_PLAN.md`, archive report endpoints | Add first-class dashboard authoring for checks/thresholds and governance history. |
| Distributed execution and load zones | Partial | Grafana Cloud k6 manages distributed load zones and infrastructure. | NeoLoad supports cloud, Docker, and dynamic infrastructure paths. | `/api/v1/system/inventory`, `/api/v1/fleet/capacity`, `docs/ENTERPRISE_GENERATOR_FLEET_MODEL.md` | Promote regions to named load zones and publish deployment evidence. |
| Scheduling and governance | Supported for current parity scope | k6 scheduling depends on Grafana Cloud workflows and external automation. | NeoLoad supports enterprise run/governance workflows. | `/api/v1/scenarios/{id}/schedules`, `/api/v1/schedules/health`, `docs/ENTERPRISE_PLATFORM_PARITY_EXECUTION_PLAN.md` | Expand external environment calendar/conflict evidence. |
| Auth, RBAC, audit, and service accounts | Supported for current parity scope | Grafana Cloud handles identity/governance in its platform. | NeoLoad enterprise deployments include platform-level governance. | `docs/AUTH_RBAC_OPERATIONS_RUNBOOK.md`, `/api/v1/auth/access-matrix`, `/api/v1/audit/events` | Native SAML remains out of scope; upstream trusted-header/OIDC stays the recommended path. |

## Monitoring, Reporting, And Evidence Matrix

| Capability | Stormrunner status | k6 baseline | NeoLoad baseline | Stormrunner evidence | Remaining gap |
| --- | --- | --- | --- | --- | --- |
| Live metrics and WebSocket monitoring | Supported | k6 Cloud provides live visualization in Grafana. | NeoLoad provides real-time graphs and statistical tables. | `/api/v1/ws`, `/metrics`, dashboard Monitoring page | Package more operator dashboards and screenshots as release evidence. |
| OpenTelemetry and Prometheus | Supported | k6 pairs naturally with Grafana observability. | NeoLoad offers packaged APM/infrastructure integrations. | `/metrics`, `/api/v1/integrations/apm/profiles`, `/api/v1/monitoring/packs`, dashboard Monitoring page | Publish more provider setup guides and screenshots as release evidence. |
| Archive reports and comparison | Supported | k6 Cloud stores and aggregates metrics for analysis. | NeoLoad offers analysis, comparison, and shareable reporting. | `/api/v1/tests/{id}/archive/report`, `/api/v1/tests/{id}/archive/comparison`, `/api/v1/tests/{id}/report`, `/api/v1/reports/profiles` | Add more profile-specific export fixtures. |
| External result ingestion | Supported for C4 scope | k6 can remain an external producer with imported run summaries. | NeoLoad advertises JMeter/Gatling result centralization. | `/api/v1/results/import`, `/api/v1/results/imports`, Results dashboard external results card | Add richer release trend chart overlays for imported external runs. |
| Root-cause and augmented analysis | Supported for C4 scope | Grafana ecosystem provides correlation and dashboards. | NeoLoad advertises augmented analysis and APM/root-cause workflows. | `/api/v1/tests/{id}/archive/root-cause`, `/api/v1/integrations/apm/profiles`, Results dashboard root-cause actions | Deepen vendor-specific causal scoring as APM connector evidence matures. |

## Competitive Claim Rules

Stormrunner claims should follow these rules:

1. A claim can be marketed as supported only if it appears as `Supported` in this matrix and has code/test/docs evidence.
2. A partial item can be demonstrated, but demos must include its remaining gap.
3. Browser feature support and browser density claims are separate. Browser canary support does not imply high-density browser load.
4. k6 compatibility means import/export or result ingestion only after explicit implementation; Stormrunner does not run k6 JavaScript.
5. NeoLoad specialized protocol parity for SAP GUI, Citrix, Oracle Forms, and desktop UI remains a gap/non-goal until a customer-backed demand assessment changes the product decision.
6. Benchmark claims require reproducible artifacts with hardware, generator class, protocol mix, script complexity, and run metadata.
