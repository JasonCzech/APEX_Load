# APEX Load — Competitive Comparison and Migration Reference

**Audience:** internal engineering, product, and evaluators.

APEX Load is a Kubernetes-native HTTP/API load testing platform built on goroutine-based VUsers, an open JSON DSL, and first-class migration paths from enterprise script archives, JMeter, DevWeb, and HAR. It is architecturally cleaner and more usable than LoadRunner Enterprise for day-to-day HTTP/API testing. It trails in correlation intelligence, browser fidelity, offline analysis depth, and battle-tested scale.

This document consolidates all competitor positioning, support matrices, the evaluator summary/trust pack, the REST API comparison, and migration/import reference into one place. The [Migration and Import Reference](#migration-and-import-reference) appendix contains the functional tables (RTS mappings, protocol routing, protocol bundle coverage) that the product's evidence API cites.

---

## Table of Contents

- [Support Matrix](#support-matrix)
- [Per-Competitor Comparison](#per-competitor-comparison)
  - [LoadRunner / LRE](#loadrunner)
  - [Akamai CloudTest](#akamai-cloudtest)
  - [k6](#k6)
  - [NeoLoad](#neoload)
  - [JMeter](#jmeter)
  - [DevWeb](#devweb)
- [Competitive Parity Areas](#competitive-parity-areas)
  - [Developer Experience](#developer-experience)
  - [Specialized Enterprise Protocols](#specialized-enterprise-protocols)
  - [Packaging, Samples, and Trust](#packaging-samples-and-trust)
- [Gap Analysis](#gap-analysis)
  - [APM Observability Correlation](#apm-observability-correlation)
- [Evaluator Summary and Trust Pack](#evaluator-summary-and-trust-pack)
  - [One-page summary](#one-page-summary)
  - [Evaluator trust pack](#evaluator-trust-pack)
- [REST API Comparison](#rest-api-comparison)
- [Migration and Import Reference](#migration-and-import-reference)
  - [Enterprise Script Archive Routing](#enterprise-script-archive-routing)
  - [Enterprise Archive Runtime Settings Reference](#enterprise-archive-runtime-settings-reference)
  - [Web 2.0 Browser Profile](#web-20-browser-profile)
  - [JMeter Import](#jmeter-import)
  - [DevWeb Import](#devweb-import)
  - [Protocol Bundle Coverage](#protocol-bundle-coverage)

---

## Support Matrix

Evidence last rechecked 2026-08-25. External baselines: Grafana Cloud k6, Tricentis NeoLoad 2025.3, OpenText LoadRunner Enterprise 26.x, and current Akamai CloudTest product/documentation pages.

**Status vocabulary:**

| Status | Meaning |
| --- | --- |
| Supported | Implemented runtime, API, or dashboard workflow with code/test/docs evidence. |
| Partial | Meaningful support; one or more runtime, UX, evidence, or scale claims remain incomplete. |
| Import/review | APEX Load can ingest and preserve the asset, but it is not a full native runtime equivalent. |
| Evidence required | Feature exists; release claims require benchmark, deployment, or external integration artifacts. |
| Gap | No equivalent support today. |
| Non-goal | Should not be claimed without a new product decision. |

### Competitive Parity API Dimensions

These dimensions are returned by `GET /api/v1/system/parity-status` in the `competitive_parity` array. Each maps to a section in this document.

| API dimension | Status | Section |
| --- | --- | --- |
| Developer Script-As-Code Experience | Partial | [Developer Experience](#developer-experience) |
| Managed Scale And Load Zones | Partial | [LoadRunner — load zones](#loadrunner) |
| Authoring, Recording, And Guided Repair | Supported | [Support Matrix — authoring flows](#source-and-authoring-flows) |
| Browser Testing And Client UX Evidence | Partial | [LoadRunner — browser testing](#loadrunner) |
| Analysis, APM, And Root-Cause Integrations | Supported | [APM Observability Correlation](#apm-observability-correlation) |
| Specialized Enterprise Protocol Breadth | Gap | [Specialized Enterprise Protocols](#specialized-enterprise-protocols) |
| Packaging, Samples, And Trust | Partial | [Packaging, Samples, and Trust](#packaging-samples-and-trust) |
| Visual Composition And Timed Tracks | Partial | [CloudTest — composition](#akamai-cloudtest) |
| Streaming Analytics And Safe Result Drill-Down | Partial | [CloudTest — analytics](#akamai-cloudtest) |
| Run-Bound Private-Network Monitoring | Partial | [CloudTest — monitoring](#akamai-cloudtest) |

### Source and Authoring Flows

| Source flow | APEX Load status | Evidence | Remaining gap |
| --- | --- | --- | --- |
| Native APEX Load JSON DSL | Supported | `pkg/dsl`, `/api/v1/scripts`, `docs/DSL_ACTION_REFERENCE.md` | More first-party examples. |
| Dashboard guided script creation | Supported | `ScriptEditor.tsx`, `/api/v1/scripts/authoring/sessions` | Expand packaged demo projects. |
| Enterprise Script Archive HTTP import | Supported | `/api/v1/scripts/import/enterprise-script-archive`, `pkg/convert/enterprisearchive` | Long-tail historical protocol families outside planned scope. |
| TrueClient Mobile Web | Partial | `docs/ROADMAP.md#browser-and-mobile` | Browser VUsers are diagnostic canaries; desktop TruClient parity is out of scope. |
| JMeter plan import | Supported | `/api/v1/scripts/import/jmeter`, `/api/v1/scripts/import/jmeter/support-matrix` | Expand fixture coverage and trend overlays. |
| OpenAPI import | Supported | `/api/v1/scripts/import/openapi`, `pkg/convert/openapi` | Dashboard guided create flow. |
| WSDL import | Supported | `/api/v1/scripts/import/wsdl`, `pkg/convert/wsdl` | Complex XSD payload synthesis remains review-scaffolded. |
| DevWeb import | Supported for import-review scope | `/api/v1/scripts/import/devweb`, `/api/v1/scripts/import/devweb/support-matrix` | Dynamic JavaScript runtime compatibility is review-only. |
| HAR import | Supported | `/api/v1/scripts/import/sample`, `pkg/convert/har` | More guided repair affordances. |
| Raw HTTP / cURL / REST samples | Supported | `/api/v1/scripts/import/sample`, `pkg/convert/sample` | Expand canonical sample projects. |
| Bruno collections | Native HTTP import supported | `/api/v1/scripts/import/bruno/*`, `pkg/convert/bruno` | Runner retired; conversion findings require review before replay. |
| k6 script import/export | Supported for constrained HTTP subset | `/api/v1/scripts/import/k6`, `/api/v1/scripts/export/k6`, `/api/v1/scripts/import/k6/support-matrix` | Broader fixture coverage; APEX Load does not run k6 JavaScript. |
| k6 result import | Supported for summary ingestion | `/api/v1/results/import` | Richer trend overlays after script conversion work. |
| Gatling result import | Supported for summary ingestion | `/api/v1/results/import` | Gatling-specific fixture packs. |
| NeoLoad project import | Gap | — | Demand-gate; prioritize coexistence and result ingestion first. |

### Protocol and Runtime Support

| Capability | APEX Load status | k6 | NeoLoad | Evidence | Remaining gap |
| --- | --- | --- | --- | --- | --- |
| HTTP/HTTPS protocol load | Supported | Strong | Strong | `pkg/protocol/http`, `pkg/engine` | Publish repeatable density benchmarks. |
| REST / OpenAPI | Supported | Supported | Supported | `/api/v1/scripts/import/openapi` | Dashboard guided create flow. |
| SOAP / WSDL | Supported | Via HTTP scripting | Supported | `pkg/protocol/soap`, `pkg/convert/wsdl` | Complex XSD generation is scaffolded. |
| Java-over-HTTP | Supported | Not primary | Enterprise coverage | `pkg/protocol/java_http` | Enterprise fixtures on demand. |
| Bruno collections | Native HTTP import supported | N/A | Varies | `pkg/convert/bruno` | Runner retired; canonical source execution unavailable. |
| JMS / broker | Partial (Solace and Azure Service Bus real adapters opt-in) | Extensions | Enterprise | `pkg/protocol/jms` | IBM MQ real adapter remains review-only; Service Bus requires `-tags jms_servicebus_real`. |
| Kafka / event streaming | Supported (mock default; real adapter opt-in) | Extensions | Enterprise | `pkg/protocol/kafka`, `pkg/convert/asyncapi` | Mock broker + AsyncAPI import; live execution via `-tags kafka_real`. |
| WebSocket | Supported | Supported | Supported | `pkg/protocol/websocket` | More subscription-protocol fixtures. |
| gRPC | Supported | Supported | Varies | `pkg/protocol/grpc` | Service-specific proto fixtures. |
| Browser UX canaries | Partial | k6 browser (Playwright-based) | RealBrowser (packaged) | `/api/v1/system/browser-benchmarks`, `docs/ROADMAP.md#browser-and-mobile` | Attach benchmark artifacts before raising density claims. |
| High-density browser load | Non-goal until evidence | Resource intensive | Packaged sizing guidance | — | Do not claim without hardware-specific benchmark evidence. |
| SAP GUI / Citrix / Oracle Forms | Gap / non-goal | Not core | Packaged | — | Complete demand assessment first. |

### Load Modeling, Execution, and Operations

| Capability | APEX Load status | Evidence | Remaining gap |
| --- | --- | --- | --- |
| Open + closed workload, pacing, think time | Supported | `pkg/engine/open_workload.go`, `pacing.go` | Benchmark-backed goal evidence. |
| Staged ramps and arrival modeling | Supported | `pkg/engine`, `/api/v1/scenarios/validate-load-model` | Published benchmark evidence. |
| Thresholds, SLAs, release gates | Partial | `docs/ROADMAP.md#runtime-telemetry-sla`, archive report endpoints | First-class dashboard authoring for checks/thresholds. |
| Distributed execution | Supported | `pkg/api/distributed_execution_backend.go`, `/api/v1/fleet/load-zones` | Publish benchmark envelopes per named self-hosted load zone. |
| Scheduling and governance | Supported | `/api/v1/scenarios/{id}/schedules`, `/api/v1/schedules/health` | External environment calendar/conflict evidence. |
| Auth, RBAC, audit, service accounts | Supported | `docs/AUTH_RBAC_OPERATIONS_RUNBOOK.md`, `/api/v1/auth/access-matrix` | Native SAML out of scope; trusted-header/OIDC is the path. |

### Monitoring, Reporting, and Evidence

| Capability | APEX Load status | Evidence | Remaining gap |
| --- | --- | --- | --- |
| Live metrics and WebSocket | Supported | `/api/v1/ws`, `/metrics`, dashboard Monitoring page | More operator dashboards as release evidence. |
| OpenTelemetry and Prometheus | Supported | `/metrics`, `/api/v1/integrations/apm/profiles` | More provider setup guides. |
| Archive reports and comparison | Supported | `/api/v1/tests/{id}/archive/report`, `/api/v1/tests/{id}/archive/comparison` | More profile-specific export fixtures. |
| External result ingestion | Supported | `/api/v1/results/import` | Richer release trend overlays. |
| Root-cause and augmented analysis | Supported | `/api/v1/tests/{id}/archive/root-cause`, `/api/v1/integrations/apm/profiles` | Deeper vendor-specific causal scoring. |

### Competitive Claim Rules

1. A claim can be marketed as **Supported** only if it appears as such in this matrix and has code/test/docs evidence.
2. **Partial** items can be demonstrated, but demos must include the remaining gap.
3. Browser feature support and browser density claims are separate. Browser canary support does not imply high-density browser load.
4. k6 compatibility means import/export or result ingestion only — APEX Load does not run k6 JavaScript.
5. NeoLoad SAP GUI / Citrix / Oracle Forms parity is a gap/non-goal unless a customer-backed decision changes it.
6. Benchmark claims require reproducible artifacts with hardware, generator class, protocol mix, and run metadata.

---

## Per-Competitor Comparison

### Akamai CloudTest

**Positioning:** APEX targets CloudTest outcome parity: preserve high-value
composition, execution, analytics, monitoring, automation, and migration
workflows using APEX's scripts, scenario groups, collectors, archives, and
Kubernetes-native capacity model. It does not copy CloudTest repository types or
its Band/Maestro/alpha-instance architecture. The canonical status and evidence
ledger is [CloudTest outcome parity
roadmap](ROADMAP.md#cloudtest-outcome-parity) and is also published by
`GET /api/v1/system/parity-status`.

Current official evidence confirms visual composition, reusable Clips,
automated Grids, Preview Mode, adjustable live load, live analytics, multi-CDN
testing, HLS/HDS/CMAF claims, SCommand automation, and Word/HTML reporting. The
[CloudTest HTTP target documentation](https://techdocs.akamai.com/cloudtest/docs/edit-target-information)
also documents HTTP/2; APEX must not claim CloudTest lacks it.

| Outcome | CloudTest | APEX Load now | Status / decision |
| --- | --- | --- | --- |
| HTTP/2 load | Documented target option | Native HTTP protocol support | **Supported / parity** |
| Visual multi-track composition | Compositions, Tracks, Clips, timing | Scripts are reusable clips; scenario groups carry active-time offsets, round-trip through the draggable composition timeline, and use overlap-aware launch admission; richer track editing remains | **Partial** |
| Conditional and parallel containers | Repository control elements | If/loop, ordered CEL switch, and project-gated bounded parallel execution exist with isolated protocol instances, deterministic extraction namespaces, replay, and safety admission | **Partial** |
| Page/resource execution | Page and resource hierarchy | HTTP document action already loads static/dynamic resources and emits network entries; typed page hierarchy remains | **Partial** |
| Preview and live ramp adjustment | Preview Mode and adjustable load | Tentative runtime-adjustment preview and apply are implemented | **Supported** |
| Regional live analytics | Location dimensions and live dashboards | Bounded `by_region` is carried through local/distributed live metrics, archives, and the regional activity view; geographic polish remains | **Partial** |
| Message drill-down and waterfall | Request/response and waterfall analysis | The transaction archive supports bounded success/detail capture, regional/hierarchy filters, redacted inspection, and network waterfalls; formal annotation phases remain | **Partial** |
| Shared dashboards | Composable dashboards and slideshows | Versioned project dashboards support optimistic revisions, bounded analytics definitions, save/import from private workspace drafts, and viewer-visible shared reopening; Grafana imports remain intact | **Supported** |
| Private infrastructure monitoring | CloudTest monitors | Signed run-bound Linux/Prometheus collector jobs, retry spooling, durable samples and archive overlays exist; additional connectors remain | **Partial** |
| Elastic cloud grid | Automated multi-cloud Grid | Kubernetes/KEDA, Azure and AWS capacity backends exist; fenced prepare mode reuses these backends; all-target acceptance and capacity/spot policy remain | **Partial** |
| HTTP media streaming | HLS/HDS/CMAF and multi-CDN marketing claims | Streaming-response pacing exists; bounded HLS/DASH playback metrics remain | **Partial** |
| CLI and transaction thresholds | SCommand repository/run/monitor/report workflows | Run/wait/status, JSON/JUnit, global and exact archived transaction rules, and library export/import exist; run-bound monitor, preparation and analysis verbs are implemented | **Partial** |
| Report output | Word/HTML and widget image/CSV export | HTML/PDF/DOCX/CSV/JSON/JSONL/Parquet exist; report charts export accessible CSV/SVG, while remaining operational charts still need the shared export wrapper | **Partial** |
| Native mobile automation | TouchTest remains in current terminology | Browser device emulation and observer canaries only | **Non-goal by product fit** |

#### CloudTest outcome-parity implementation rules

- A CloudTest Clip maps to a stored APEX script referenced by a scenario group.
  Do not add a generator-side repository lookup or runtime `call_script` without
  a fixture proving irreducible nested launch semantics.
- Extend `/archive/transactions` and the current inspector; do not create a
  duplicate message archive/viewer.
- Add hierarchy to the existing HTTP resource loader; do not add a duplicate
  Page transport action.
- Keep optional message-detail capture independent from
  `error_handling.snapshot_on_error`, and never globally broadcast bodies.
- Extend the restricted-network collector for monitors. The controller must not
  dial arbitrary customer endpoints.
- Spot capacity uses an availability-oriented on-demand floor and spot ceiling;
  no non-spot alpha role exists in APEX's topology.
- HDS, raw OpenStack VM provisioning, CA Introscope, unrestricted commands, and
  arbitrary imported JavaScript are demand-gated or non-goals.

**Evidence/fixture gate:** current docs establish product outcomes, not export
wire shapes. XML/ZIP mappings remain provisional until sanitized,
dependency-complete exports prove them. Each recognized import element must be
classified `executable_exact`, `executable_bounded`, `blocked_draft`, or
`rejected`.

### LoadRunner

> Source: June 2026 repository reality-check across the protocol support matrix, import coverage docs, API comparison, and evaluator trust pack. Deliberately unvarnished. Protocol breadth is excluded from this comparison — APEX Load is treated as having sufficient protocol coverage.

**One-line verdict:** APEX wins on architecture and day-to-day usability; LRE wins on migration intelligence, analysis depth, and battle-tested scale.

> **REST API / integration layer:** for a focused comparison of the programmable surface (auth, versioning, streaming, test-as-code, results extraction, CI/webhooks) and the bidirectional API gap analysis, see [REST API Comparison](#rest-api-comparison). The rows below cover product capability; that section covers the API layer.

#### Capability Matrix

Legend: **▲ APEX leads** · **= parity** · **▼ APEX trails** · **▼▼ APEX trails badly**

**Authoring and Recording**

| Capability | APEX | LRE 26.x | Verdict |
| --- | --- | --- | --- |
| Script authoring UI | Web editor, multi-protocol, single pane | Thick-client VuGen, protocol-specific | **▲** |
| HTTP/WS recording | MITM proxy, TLS, WS frames, binary/compressed, cookies | VuGen recorder (mature) | **=** |
| Browser/SPA recording | Embedded **DOM-aware record/replay** (Playwright-Go + CDP): shadow-piercing capture, ranked selectors + replay self-heal, sections/transactions, device emulation, conditional HAR (proxy recorder remains for HTTP/WS) | TruClient DOM-level record/replay | **▼** (DOM recorder shipped; TruClient still ahead on at-scale browser replay maturity) |
| Script format | Open JSON DSL | Proprietary C/JS VUser | **▲** |
| Import from other tools | LR, JMeter, Postman, OpenAPI, WSDL, DevWeb, HAR, cURL | LR-native; limited import | **▲** |

**Correlation**

| Capability | APEX | LRE 26.x | Verdict |
| --- | --- | --- | --- |
| Auto-correlation | Response-family rule packs, replay-failure detection, stale-literal evidence, and guided/batch repair (`pkg/convert/correlate/`) | Full rules engine per protocol + design studio + learning from record/replay | **▼** |
| Correlation depth | Improved for HTTP/API, HTML, JSON, XML/SOAP, redirects, WebSocket, and JMS IDs; still narrower than VuGen | LRE's defining moat | **▼** |

**Load Modeling and Execution**

| Capability | APEX | LRE 26.x | Verdict |
| --- | --- | --- | --- |
| Open + closed workload, pacing, think time | Implemented incl. arrival-rate scheduler | Implemented | **=** |
| Goal-oriented mode | First-class | First-class | **=** |
| Live VUser scaling / pause-resume / duration adjust | Smooth, API-driven, transaction-integrity-preserving | Possible mid-run but heavier | **▲** |
| Distributed execution | Real gRPC multi-generator fleet | Controller + LGs (mature) | **=** |
| Region / load-zone distribution | Flat fleet targeting; no named geo distribution | Named hosts/locations, cloud LGs | **▼** |

**Analysis and Reporting**

| Capability | APEX | LRE 26.x | Verdict |
| --- | --- | --- | --- |
| Offline analysis depth | Archive report/comparison plus merged graph and transaction drilldown (`/api/v1/tests/{id}/archive/analysis`); still lighter than Analysis | Analysis: graph merge, page diagnostics, deep drill-down | **▼** |
| Multi-run trending | Release trend cards; `GET /api/v1/releases/{id}/trend` | Rich trend reports | **▼** |
| Report output | HTML + archive comparison + baseline pin + executive/engineer PDF and DOCX; custom templates remain narrower | HTML/PDF/Word, custom templates | **▼** |
| Open results format | Parquet/JSONL | Proprietary .lrr/.lra | **▲** |
| Root-cause | Deterministic correlation summary | APM-correlated diagnostics | **▼** |

**Enterprise and Governance**

| Capability | APEX | LRE 26.x | Verdict |
| --- | --- | --- | --- |
| AuthN/SSO | OIDC + trusted-header | LDAP/SAML/SSO | **=** |
| RBAC + service accounts + audit | Implemented | Domains/projects/roles | **=** |
| Scheduling | Cron + approvals + blackout windows | Timeslot/calendar reservation | **=** |
| Notifications | Slack/Teams/Dynatrace webhooks; no email | Email + integrations | **=** |
| Secrets / PKI | Template refs + redaction; no Vault, no mTLS | CyberArk/Vault, client certs | **▼** |
| CI/CD ecosystem | GitHub Actions, signed canonical action, JUnit/JSON; no maintained cross-platform marketplace plugins | Maintained Jenkins/ADO plugins, Git sync | **▼** |

**Architecture and Scale**

| Capability | APEX | LRE 26.x | Verdict |
| --- | --- | --- | --- |
| Engine efficiency | Goroutine-per-VUser, K8s-native HPA | Process/thread VUsers on LG VMs | **▲** |
| Single pane of glass | Author → run → analyze → admin in one web app | Three tools: web portal + VuGen + Analysis | **▲** |
| Proven scale | No benchmark artifacts for 10k/50k targets | Decade-hardened, reference-scaled | **▼** |
| Persistence | SQLite default; Postgres via pgx (unproven at scale) | Enterprise DB-backed | **▼** |

#### Honesty Corrections

These overstatements existed in earlier docs and are now corrected:

1. **AI/LLM generation** — the default provider is `deterministic-local` (`pkg/api/ai_assistance.go`). No OpenAI/Azure/Anthropic client is wired. Describe as governed deterministic assisted-authoring with external LLM policy-gated.
2. **Storage stack** — SQLite is the runtime default. Postgres is wired but unproven at scale. Redis is optional. No Azure Blob.
3. **Browser parity** — browser VUsers are diagnostic canaries for *load execution* (not RealBrowser-class high-density browser load). The *recorder/replay* is now a first-class **DOM-aware** embedded recorder (Playwright-Go + CDP) with selector self-heal — the "proxy-only, no DOM capture" caveat no longer applies.
4. **Scale targets** — 10k VUsers/pod and 50k total are design targets with no published benchmark artifacts.
5. **OpenTelemetry** — controller/generator binaries provide opt-in OTLP traces for API requests, test lifecycle/orchestration, sampled engine actions, and controller↔generator gRPC; W3C context continues into HTTP requests to the system under test. OTel metrics/log export and desktop OTLP export are not implemented.

#### Prioritized Gaps (LoadRunner parity)

| Priority | Gap | Effort |
| --- | --- | --- |
| P0 | Correlation intelligence — broaden response-family fixtures and UI-assisted medium-confidence review | M |
| P0 | CI/CD ecosystem — maintained marketplace plugins, Git script sync | M |
| P1 | Offline analysis depth — multi-run trend polish and report profile hardening | M |
| P1 | Benchmark evidence — publish one reproducible density benchmark | S–M |
| P2 | RealBrowser-class high-density browser **load** (DOM-aware record/replay already shipped; at-scale browser VUsers remain canary-scoped) | L |
| P2 | Region/load-zone distribution with per-region response metrics | M |
| P3 | Enterprise hardening — Vault/mTLS, test-level quota enforcement | M each |

---

### k6

**Positioning:** APEX Load competes as a Kubernetes-native self-hosted platform with open telemetry, auditable archives, and strong import paths. It does not claim JavaScript compatibility or drop-in k6 portability.

**Where APEX leads k6:**
- Protocol breadth (SOAP, Java-over-HTTP, JMS, Kafka, WebSocket, gRPC native)
- Single-pane authoring + execution + analysis + admin
- Enterprise Script Archive/JMeter/HAR migration paths
- Governed scheduling, RBAC, and audit out of the box for self-hosted

**Where APEX trails k6:**
- JavaScript-first scripting familiarity and community ecosystem
- Grafana Cloud managed infrastructure / load zones
- Extension ecosystem and CLI usability
- Published developer-facing examples and quick-start materials

**Do not claim:**
- Drop-in k6 or JavaScript-runtime compatibility; APEX Load supports only the tested static HTTP import/export subset
- High-density browser load parity until benchmark evidence exists
- Managed SaaS control plane

---

### NeoLoad

**Positioning:** APEX Load offers stronger migration paths from LoadRunner, more transparent DSL scripting, and Kubernetes-native self-hosting. NeoLoad wins on specialized enterprise protocol breadth (SAP GUI, Citrix, Oracle Forms), established deployment history, and packaged APM/analysis integration maturity.

**Where APEX leads NeoLoad:**
- Open JSON DSL vs proprietary project format
- Enterprise Script Archive/JMeter/HAR migration paths
- Goroutine-based engine efficiency
- Single-pane web experience

**Where APEX trails NeoLoad:**
- SAP GUI, Citrix, Oracle Forms, desktop UI protocols (gap/non-goal)
- Packaged APM correlation and analysis depth
- Established enterprise deployment benchmarks
- RealBrowser-class browser load (APEX browser VUsers are canaries only)

---

### JMeter

**Where APEX leads:**
- Modern web dashboard; no XML test plan editing
- Kubernetes-native execution; no per-generator JVM tuning
- First-class migration path from JMeter (`.jmx` import → DSL with support matrix review)
- Parquet/JSONL archives; better pipeline integration

**APEX JMeter import status:** Supported. See [JMeter Import](#jmeter-import) for details. Result import for trend centralization is also supported via `/api/v1/results/import`.

---

### DevWeb

DevWeb (OpenText's JavaScript-based HTTP testing engine) overlaps heavily with APEX Load's HTTP/WebSocket/gRPC protocol coverage. The key differentiator is runtime model: APEX Load uses Go goroutines rather than a JavaScript VM.

**Coverage:** APEX Load's HTTP, WebSocket, and gRPC protocols cover DevWeb's core transport scenarios natively. A DevWeb `.js` script import path converts DevWeb constructs to APEX Load DSL with a construct-by-construct support matrix. Full JavaScript runtime compatibility is not a goal — dynamic JavaScript execution remains review-only.

See [DevWeb Import](#devweb-import) for the construct support matrix and API surface.

---

## Competitive Parity Areas

These areas correspond to the `competitive_parity` dimensions in `GET /api/v1/system/parity-status`.

### Developer Experience

**Status: Partial.** APEX Load has CLI execution, local script validation/explain commands, governed explain output, CI templates, k6 result ingestion, and constrained k6 HTTP subset import/export, but it is not a JavaScript-first local/cloud workflow.

**Evidence:** `README.md#build--run`, `.github/actions/apexload-run/action.yml`, `/api/v1/results/import`, `/api/v1/scripts/import/k6`, `/api/v1/scripts/export/k6`

**Remaining:** Expand k6 fixture coverage and keep unsupported JavaScript/browser constructs review-only.

**Decision:** differentiate — APEX Load's controlled JSON DSL and governed CI APIs are deliberate architectural choices, not gaps to replicate.

---

### Specialized Enterprise Protocols

**Status: Gap.** APEX Load covers HTTP/API, SOAP, Java-over-HTTP, JMS, Kafka, WebSocket, gRPC, HAR, JMeter, DevWeb, AsyncAPI, and enterprise script archive migration paths. SAP GUI, Citrix, Oracle Forms, and desktop UI protocols are demand-gated non-goals.

**Evidence:** `docs/PROTOCOL_SUPPORT.md`, `docs/ROADMAP.md#browser-and-mobile`, `/api/v1/protocol-bundles/support-matrix`

**Remaining:** Complete customer-backed demand assessment. Publish explicit non-goals or coexistence guidance for unsupported legacy protocols. JMS/broker-specific diagnostics (queue depth, DLQ, consumer lag) are partially implemented.

**Decision:** demand-gated non-goal unless a customer-funded product decision changes it.

---

### Packaging, Samples, and Trust

**Status: Partial.** APEX Load has docs, a support matrix, CI templates, dashboard workflows, canonical sample projects, one-command demo seeding, and release evidence manifests. Evaluators still need release-specific benchmark deltas attached before scale claims are raised.

**Evidence:** `README.md#quick-start`, `docs/CICD_INTEGRATION.md`, `docs/COMPETITIVE_COMPARISON.md#evaluator-summary-and-trust-pack`, `docs/GITHUB_ACTIONS_RELEASE_AND_DEPLOYMENT.md#release-evidence-manifest`, `scripts/seed-evaluator-demo.sh`

**Remaining:** Attach release notes with benchmark deltas for each release candidate.

---

## Gap Analysis

A comprehensive audit of APEX Load against enterprise performance testing platform capabilities, compared against LoadRunner Enterprise, Gatling Enterprise, k6 Cloud, NeoLoad, and Locust.

**Already built (representative):** goroutine VUser engine, multi-protocol support, JSON DSL, real-time dashboard, CLI, REST API, Enterprise Script Archive/JMeter import, distributed execution, live VUser controls, Parquet/JSONL archives, system metrics, duration-sketch percentiles, telemetry pipeline, capacity planning, browser recording, script editor, project/folder organization, shared data pools, error snapshots, browser replay, settings management.

**Remaining significant gaps:**

| Gap | Impact | Status |
| --- | --- | --- |
| CI/CD pipeline integration — JUnit XML, maintained marketplace plugins, Git script sync | Critical | GitHub Actions + JUnit/JSON delivered; cross-platform marketplace plugins out of scope |
| Offline report depth — graph merge, per-txn drill-down, multi-run trend, PDF export | High | Archive + trend analysis, graph/drilldown API/dashboard UX, and executive/engineer PDF export delivered; heavier custom report tooling pending |
| Correlation intelligence depth | High | Response-family rule packs + replay stale-literal evidence delivered; full VuGen-class parity pending |
| Named load zones with benchmark envelopes | High | Named self-hosted zones delivered; benchmark envelopes pending |
| Browser/SPA recording fidelity | Medium | DOM-aware embedded recorder (Playwright-Go + CDP) + replay self-heal delivered; high-density browser *load* execution remains canary-scoped (separate effort) |
| PDF/Word report export | Medium | Executive/engineer PDF and DOCX export emitted from the common archive report profiles; richer custom report tooling remains |
| Vault / mTLS | Medium | Template refs + redaction exist; Vault/mTLS not started |
| OpenTelemetry signal expansion | Low | Opt-in OTLP traces cover control-plane and sampled action paths; metrics/log export and desktop OTLP export remain |
| SAML | Medium | OIDC + trusted-header is the recommended path; SAML out of scope |
| Hosted/managed control plane | Non-goal | Self-hosted first |

### APM Observability Correlation

**Status: Supported.** HTTP and SOAP execution propagates W3C `traceparent` headers; `trace_id` and `span_id` are archived alongside JSONL/Parquet transaction events. Prometheus controller and generator inventory metrics are available with region-labeled gauges. Dynatrace Events API v2 lifecycle notifications are supported (test start/stop/pause/resume/completion/failure events). Scripts can declare Prometheus-format external metric scrapes under `config.observability.external_metrics`; samples attach to live `resources.external_metrics`, JSONL archives, and Parquet `external_metrics_json`.

**Evidence:** `/metrics`, `/api/v1/integrations/apm/profiles`, `/api/v1/monitoring/packs`, `pkg/api/notifications.go`

**Remaining:** Deepen vendor-specific causal scoring. OTLP span export is implemented for controller HTTP, lifecycle/orchestration, sampled actions, and controller↔generator gRPC; OpenTelemetry metrics/log export and desktop OTLP export remain deferred.

---

## Evaluator Summary and Trust Pack

Point-in-time (rechecked June 2026); this document is authoritative. Consolidated 2026-09-24 from the former evaluator one-pager and trust pack.

### One-page summary

#### Positioning

APEX Load is a self-hosted, Kubernetes-native performance testing platform for HTTP/API-heavy enterprise workloads. It is strongest where teams need open JSON DSL assets, controller APIs, governance, CI gates, migration paths, and auditable archives.

#### Supported Today

| Area | Status |
| --- | --- |
| HTTP/API, SOAP, WebSocket, gRPC, Java-over-HTTP | Supported |
| JMeter, Enterprise Script Archive HTTP, HAR, OpenAPI, WSDL, DevWeb import-review workflows | Supported for documented scopes |
| CI/CD gating with JSON and JUnit XML | Supported |
| Live metrics, archive reports, comparison, release trends, Prometheus/Dynatrace hooks | Supported for current scope |
| Self-hosted named load zones | Supported as region-derived generator pools |

#### Partial / Evidence-Gated

| Area | Current boundary |
| --- | --- |
| k6 compatibility | Constrained HTTP import/export and result ingestion only; APEX does not run k6 JavaScript. |
| Browser testing | Diagnostic browser canaries paired with protocol load; no high-density browser claim without release benchmark artifacts. |
| LoadRunner correlation parity | Rules, replay diagnostics, and guided repair exist; VuGen-class breadth remains deeper. |
| Published scale | Design targets require release evidence before customer-facing claims. |

#### Non-Goals Unless Customer-Backed

- Managed SaaS load-zone control plane.
- SAP GUI, Citrix, Oracle Forms, and native desktop UI protocols.
- Full k6 JavaScript runtime compatibility.
- Full TruClient/native desktop automation parity.

#### Fast Evaluation Path

1. Seed demos with `./scripts/seed-evaluator-demo.sh`.
2. Validate local authoring with `apexctl script validate`.
3. Inspect `/api/v1/system/parity-status` and `/api/v1/fleet/load-zones`.
4. Run HTTP/API protocol load with a small browser canary group.
5. Review archive report, comparison, and CI/JUnit output.

### Evaluator trust pack

This pack gives evaluators a short, reproducible path through the current competitive claims without implying unsupported parity.

#### One-Command Seed

Run a controller locally, then seed the canonical demo assets:

```bash
APEXLOAD_API=http://localhost:8080 PROJECT_ID=default ./scripts/seed-evaluator-demo.sh
```

The seed script loads:

| Area | Asset |
| --- | --- |
| HTTP/API | `examples/web-api/ecommerce-http.json` |
| Mixed protocol + browser canary | `examples/web-api/browser-canary-plus-protocol-load.json` |
| SOAP | `examples/web-api/soap-account-service.json` |
| gRPC | `examples/web-api/grpc-inventory.json` |
| WebSocket | `examples/web-api/websocket-order-feed.json` |
| JMS | `examples/web-api/jms-order-flow.json` |

#### Evidence Rules

- Treat browser VUsers as diagnostic canaries unless a release evidence manifest includes controlled raw benchmark output.
- Treat load zones as self-hosted named generator pools, not managed SaaS infrastructure.
- Treat k6 compatibility as constrained import/export and result ingestion, not JavaScript runtime compatibility.
- Treat SAP GUI, Citrix, Oracle Forms, and native desktop automation as demand-gated non-goals.

#### Acceptance Checklist

- `/api/v1/system/parity-status` shows the competitive parity dimensions and evidence levels.
- `/api/v1/fleet/load-zones` returns named self-hosted load zones derived from generator regions.
- `apexctl script validate --script examples/web-api/ecommerce-http.json --format json` returns `valid: true`.
- `POST /api/v1/scripts/import/k6` returns executable HTTP DSL for statically parseable k6 HTTP scripts and review diagnostics for unsupported JavaScript/browser constructs.

---

## REST API Comparison

Scope: the **REST API / integration layer** of APEX Load against **OpenText LoadRunner Enterprise (LRE) 26.x**: how each platform is driven as-code and wired into automation, and where each falls short of the other. Non-API dimensions (VuGen correlation depth, desktop-protocol breadth, offline Analysis depth, proven scale) are in the [LoadRunner](#loadrunner) section. Point-in-time (evidence rechecked 2026-06-15); LRE's REST surface varies by patch level and some admin resources are deployment-specific. This section is also the **living scorecard** for the API-parity program: its "APEX trails" rows map to dimensions in `GET /api/v1/system/parity-status` (see [Parity program linkage](#parity-program-linkage)).

### Legend

Verdict (APEX-relative): **▲ APEX leads** · **= parity** · **▼ APEX trails** · **▼▼ APEX trails badly**

Status vocabulary (shared with the competitive matrix): **Supported** · **Partial** · **Gap** ·
**Evidence required** · **Non-goal**.


### REST API verdict

- **APEX leads on modern API ergonomics and test-as-code.** Stateless OIDC + scoped API-key auth,
  explicit `/api/v1` versioning, generated OpenAPI, JSON throughout, WebSocket live-metrics streaming,
  full script/test CRUD via REST + open JSON DSL, JSON KPI/transaction/trend extraction, and native
  GitHub/GitOps with PR check-runs.
- **LRE leads on enterprise resource governance exposed at the admin layer** — first-class timeslot
  reservations, domain/project administration, test-sets/test-instances, host pools, AUT registry, and
  ALM/Quality Center traceability — backed by a maintained CI-marketplace plugin ecosystem (Jenkins,
  Azure DevOps).
- **The headline asymmetry:** LRE's REST API **cannot create or modify test/script definitions** (VuGen/
  UI only) and **cannot stream live metrics** (poll-only; KPIs locked in proprietary `.lrr`/`.lra`
  result archives). APEX's REST API **lacks a forward-booking reservation resource** and a managed
  fleet/host-pool admin surface. Those two gaps define most of the build-out below.


### Chart A — API ergonomics & protocol design

| Dimension | LRE 26.x | APEX Load | Verdict |
| --- | --- | --- | --- |
| Resource model / base path | Domains → Projects → Tests → Runs; `/LoadTest/rest/domains/{domain}/projects/{project}/...` | Flat `/api/v1/...` with project/domain scoping via params + RBAC | **=** |
| Authentication | LWSSO cookie (stateful); HTTP Basic → `Set-Cookie: LWSSO_COOKIE_KEY`; re-auth on mid-flight 401 | Stateless: OIDC sessions, `Authorization: Bearer`/`X-APEXLoad-API-Key` (SHA-256-hashed), trusted-header mode (`pkg/api/middleware.go`) | **▲** |
| Authorization / RBAC | Domain/project roles, ALM-linked permissions | 4 roles (`system_admin`/`project_admin`/`editor`/`viewer`) × global/project/domain/repo scopes, single-source policy (`pkg/api/access_policy.go`) | **=** |
| Multi-tenancy | Domains + Projects (strong, mature) | Projects (primary) + Domains (app-repo sharing) | **=** |
| API versioning | None in path; implicit cross-version compatibility | Explicit `/api/v1` prefix | **▲** |
| Machine-readable contract | No published OpenAPI/Swagger | Generated OpenAPI 3.0.3 (`docs/swagger.yaml` via `cmd/swaggergen`, freshness test-enforced) | **▲** |
| Response schemas in contract | n/a (no spec) | Present but mostly generic `200 Successful operation` (concrete schemas in progress) | **▲** (caveated) |
| Payload format | JSON (`Accept: application/json`) | JSON; multipart for uploads; CSV export on some endpoints | **=** |
| Error envelope | `{ExceptionMessage, ErrorMessage, Message, ErrorCode}` | `{error, ref}`; opaque `ref` + server log on 5xx (`writeError`, `pkg/api/handlers.go`) | **=** |
| Live metrics over API | Not REST-exposed; online stats behind the run dashboard | `GET /api/v1/tests/{id}/metrics` snapshot + WebSocket streams | **▲** |
| Async / streaming | Polling only (no webhooks/SSE/WebSocket) | Polling + WebSocket (`/api/v1/ws`, `/api/v1/tests/{id}/stream`); async jobs poll (GitHub sync-jobs) | **▲** |
| Inbound webhooks | None documented | GitHub receiver with HMAC-SHA256 (`POST /api/v1/integrations/github/webhook`) | **▲** |
| Outbound completion webhooks | None | None (Dynatrace is dry-run only) — shared gap | **=** |
| Idempotency | Convention only (`RunComment` marker) | `Idempotency-Key` header, actor-scoped, 409 on conflict (coordination store) | **▲** |
| Rate limiting | Not documented | Per-service-account `X-RateLimit-*` headers (env-configurable) | **▲** |
| Pagination / filtering | `?query={...}` server filter; no cursor/limit/offset | Offset (`limit`/`offset`) on some lists, not yet standardized; no cursor | **=** |
| CORS / browser security | n/a (server-rendered) | Allow-list CORS, CSP/HSTS/`nosniff`/`DENY` headers (`pkg/api/middleware.go`) | **▲** |
| Request size limits | Not documented | Global 128 MiB backstop + per-route `MaxBytesReader` overrides | **▲** |
| Agent-tool surface (MCP/LLM) | None | MCP server exposes the REST API as tools with confirm/reason gating (`mcp/apexload_mcp/`) | **▲** |


### Chart B — Capability surface (what each API can actually do)

| Capability | LRE 26.x | APEX Load | Verdict |
| --- | --- | --- | --- |
| Test/script definition as code | **Cannot** create/modify test or script definitions via REST (VuGen/UI only) | Full CRUD + validate + replay + export over JSON DSL (`/api/v1/scripts*`) | **▲▲** |
| Run lifecycle control | Create+start, stop, abort; duration/VUsers fixed at creation | Start/stop/abort/pause/resume **plus** mid-run scale-VUsers, adjust duration, adjust goal targets | **▲** |
| Capacity model | First-class **timeslot reservation** (forward booking, calendar, conflict prevention) | Real-time admission control + estimate/preflight + scenario schedules/approvals, **plus load-bearing forward-booking timeslots** (`/api/v1/capacity/timeslots`: CRUD, availability/conflict, approval, atomic bind) — a run binds a `timeslot_id` strictly at create and admission control is **reservation-aware** (an approved, bound timeslot reserves generators for its window; other tenants are held PENDING) | **≈** (at parity) |
| Results & KPI extraction | Download result zip; parse proprietary `.lrr`/`.lra`/Analysis; only `RunSLAStatus` on the run entity | JSON metrics/transactions/trend/comparison endpoints; Parquet/JSONL archives; error evidence | **▲** |
| Trend / regression analysis | Analysis tool (offline); limited REST | `GET /api/v1/releases/{id}/trend` (+ export), pinned-baseline comparison, SLA governance | **▲** |
| Scheduling / run calendar | Timeslot calendar (reservation-centric) | Cron schedules, occurrences, approvals, governance windows, `/api/v1/run-calendar` | **=** |
| Test-set / ordered execution | Test Sets / Test Instances (ordered collections) | **Execution plans** (`/api/v1/execution-plans`): ordered scenario entries with per-entry data-pool/timeslot/runtime overrides + failure policy; **dependency-gated sequential execution** (run N+1 awaits run N's terminal state) with persisted, queryable plan-run progress | **✓** (parity) |
| Imports / interop | LR-native; one-way archive import | 11 formats (LR archive, JMeter, k6, HAR, OpenAPI, RAML, AsyncAPI, Postman, WSDL, Salesforce, Oracle EBS) | **▲** |
| Fleet / host administration | Host pools, load generators, monitor-over-firewall, SiteScope (admin layer) | Managed pools, persistent generator metadata/labels/cordon, provisioning requests (`/api/v1/fleet/pools\|generators\|provision-requests`), a **real in-cluster Kubernetes provisioner** that scales the generator StatefulSet, pool-aware inventory + pool-scoped run assignment (cloud backends future) | **✓** (parity) |
| Monitoring integration | SiteScope + monitor-over-firewall agents (registered collectors) | Inbound monitoring **plus a full collector loop** (`/api/v1/monitoring/collectors`: register, SHA-256 scoped token + rotation, metric packs, health) with **collector-token heartbeat/metric ingestion** (`…/{id}/heartbeat`, `…/{id}/metrics`) routed to live monitoring + a **dependency-free agent runtime** (`cmd/apexload-collector-agent`) | **✓** (parity) |
| Entitlements / consumption | License-tier model + usage | **Read-only entitlements** (`/api/v1/system/entitlements[/usage]`, `/api/v1/projects/{id}/entitlements/usage`): edition, enabled bundles, per-limit used-vs-cap (concurrent tests + active VUsers, global and per project), per-bundle historical consumption, and a per-project breakdown (surfaced, not enforced) | **=** (read parity; APEX doesn't enforce by design) |
| Correlation as-code | Per-protocol rules engine + design studio (persistent, learned) | detect/diagnose/diff engine (`pkg/convert/correlate/`) + persisted per-script recorded baselines (`/api/v1/scripts/{id}/recording/status`, `/api/v1/scripts/{id}/diagnose-replay`) **plus learned, reusable project-scoped correlation rulesets** (`/api/v1/correlation-rulesets`) — `discover-from-script` saves the correlations a baseline reveals, `{id}/apply-to-script` previews or writes the extractors + value-templating into a target script | **≈** (at parity) |
| Browser object repository | VuGen/TruClient object repository | Selector model + **persisted project-scoped object repositories** (`/api/v1/object-repositories`, seedable `from-script`) with **script↔repository binding** (action `object_ref`, resolved controller-side into selectors + fallbacks for replay and load) and **repair-on-replay write-back** (self-heals feed the working locator back into the bound object) | **≈** (at parity) |
| CI/CD integration | Maintained Jenkins + Azure DevOps marketplace plugins | GitHub-native (push/pull, PR check-runs, webhooks, sync-jobs) + CI templates; **no marketplace plugins** | **=** |
| Git / GitOps | No native Git script sync | Native GitHub script push/pull, drift status, project policy | **▲** |
| ALM / requirements traceability | Quality Center / ALM integration | None (out of scope) | **▼** |


### Where APEX falls short of LRE (and what closes it)

These are the API-layer gaps the parity program addresses. Each maps to a build family.

1. **Forward-booking capacity reservation — *supported*.** APEX exposes timeslot reservations
   (`/api/v1/capacity/timeslots`: CRUD, availability/conflict checks, an approval state, atomic
   single-test binding — `pkg/api/handlers_timeslots.go`, `pkg/storage/sqlite_timeslots.go`) that are
   now **load-bearing**: `POST /api/v1/tests` accepts a `timeslot_id` and binds it strictly at create
   (the create fails if it can't be bound), and `pkg/api/capacity.go` admission control is
   **reservation-aware** — an approved, bound timeslot reserves generators for its window
   (`pkg/api/timeslot_reservation.go`), so other tenants are held PENDING for that capacity. Tracked
   as the `capacity_reservation` dimension (now `supported`) in `parity-status`.
2. **Fleet pool & provisioning management — *supported*.** APEX exposes managed generator pools,
   persistent generator metadata (labels/region/cordon/pool membership), and provisioning requests
   (`/api/v1/fleet/pools|generators|provision-requests`). A **real in-cluster Kubernetes provisioner**
   (`APEXLOAD_GENERATOR_PROVISIONER=kubernetes`) scales the generator StatefulSet's replicas via the
   controller service account on `POST /fleet/provision-requests` (lifecycle `provisioning → success/failed`
   with events — **no new cloud credentials**; same RBAC as `/system/restart`). The live inventory is
   **pool-aware** (`GeneratorInventoryEntry.fleet_pool_id/pool_name/cordoned` from persisted metadata) and a
   run can target a pool (`assignment.pool_id`, resolved to the pool's members at placement)
   (`pkg/api/fleet_provisioner.go`, `pkg/kubeops/client.go`). Cloud (AWS/Azure/GCP) backends remain future via
   the `GeneratorProvisioner` registry. Tracked as `fleet_management` (now `supported`).
3. **Ordered cross-run execution plan — *supported*.** APEX exposes execution plans
   (`/api/v1/execution-plans`: CRUD + `POST /{id}/runs`) with per-entry runtime overrides, **per-entry
   data-pool overrides**, **optional per-entry timeslot binding**, and failure policy. **Sequential plans
   are dependency-gated** — each entry's run starts only after the prior run reaches a terminal state
   (honoring halt/skip), driven by an in-memory orchestrator on the runner
   (`pkg/api/execution_plan_orchestrator.go`); parallel plans stage all entries. Plan-run progress is
   persisted and queryable (`GET /{id}/runs`, `GET /{id}/runs/{runId}`) and streamed via
   `execution_plan_*` WebSocket events. Tracked as `execution_planning` (now `supported`).
4. **Monitoring collectors — *supported*.** Restricted-network collectors register via
   `/api/v1/monitoring/collectors` with SHA-256 scoped tokens (one-time issue + rotation) and allowed metric
   packs, then push **collector-token-authenticated heartbeats** (`POST …/{id}/heartbeat`) that record
   health + last-seen and ingest **in-scope** metric packs; the latest snapshot is routed to live monitoring
   (`monitoring_collector_heartbeat` WebSocket event + `GET …/{id}/metrics`). A dependency-free **agent
   runtime** (`cmd/apexload-collector-agent`) collects host metrics and pushes on an interval
   (`pkg/api/collector_metrics.go`, `pkg/collectoragent`). Live snapshot is in-memory (like running-test
   metrics); durable history is future work. Tracked as `monitoring_collectors` (now `supported`).
5. **Entitlements read model — *supported*.** `/api/v1/system/entitlements[/usage]` and
   `/api/v1/projects/{id}/entitlements/usage` project edition, enabled bundles, **per-limit used-vs-cap**
   (concurrent tests + active VUsers, global and per project), **per-bundle historical consumption**, and
   a **per-project breakdown** — a pure read-only projection over existing usage accounting + quota config
   (`pkg/api/entitlement_usage.go`, `handlers_entitlements.go`). Surfaced, never enforced (no licensing
   engine by design). Tracked as `entitlements` (now `supported`).
6. **Object repositories and correlation rulesets are both load-bearing (supported).**
   Project-scoped object repositories are stored, bound to scripts by logical name (action
   `object_ref`, resolved controller-side into selectors + fallbacks for replay and load), and
   replay self-heals write the working locator back into the bound object. Per-script recorded
   correlation baselines are stored, and learned **reusable project-scoped correlation rulesets**
   are discoverable from a baseline and applicable to other scripts (preview or write the extractors
   + value-templating into a new version).
   → Cross-project / org-wide ruleset sharing (future).
7. **No ALM/Quality Center traceability and no CI-marketplace plugins.** Out of scope for this program;
   GitOps/PR-check-runs are the deliberate alternative. ALM remains a documented non-goal.

Note: LRE's most cited advantages (VuGen correlation, desktop protocols, offline Analysis depth, proven scale) are **not REST-API features**; see [LoadRunner](#loadrunner).

### Where LRE falls short of APEX (deliberately not "fixed" — APEX advantages to keep)

1. **No test-as-code.** The REST API cannot create/modify test or script definitions; everything must
   pre-exist in VuGen/UI. APEX is API-first over an open JSON DSL.
2. **Stateful, token-less auth.** LWSSO cookie + Basic, no API keys/JWT/scoped service accounts.
3. **No API versioning and no OpenAPI spec.** Clients reverse-engineer; no codegen, no contract tests.
4. **No live-metrics REST surface and no streaming.** Poll-only; no WebSocket/SSE. KPIs require
   downloading and parsing proprietary `.lrr`/`.lra` Analysis archives — there are no JSON
   KPI/transaction/trend endpoints.
5. **No native Git / GitOps / PR check integration.**
6. **No idempotency keys** (only the `RunComment` marker convention) and **no documented rate-limit
   contract**.
7. **No agent-tool (MCP) surface.**

The build program explicitly **does not** chase these — they are documented as APEX-ahead so effort
isn't spent regressing toward LRE's model.

### Shared gaps / parity

- Neither offers cursor pagination (LRE has none; APEX has offset on some lists, not standardized).
- Neither emits outbound test-completion webhooks.
- Neither publishes rate-limit SLOs (APEX at least emits `X-RateLimit-*`).
- Both are JSON-over-HTTP at the wire level.


### Parity program linkage

The "▼ APEX trails" rows above are tracked as dimensions in `GET /api/v1/system/parity-status`
(`pkg/api/parity_status.go`): `capacity_reservation`, `fleet_management`, `execution_planning`,
`monitoring_collectors`, `entitlements`, `correlation_persistence`, `object_repository`. As each family
lands, its dimension flips `gap → partial → supported`, and the corresponding row here is updated in the
same change so this scorecard never drifts from the endpoint.

### Honesty caveats

- **LRE's admin REST surface is broader than a minimal run-orchestration client exercises.** Timeslots,
  hosts, test-sets, AUT, and monitor management exist at the administration layer in LRE; exact
  availability is patch-/deployment-specific. This doc represents them as present without claiming an
  exhaustive 26.x endpoint inventory.
- **APEX claims are code-grounded.** Every APEX row cites `pkg/...` or `/api/v1/...`. Where a capability
  is partial (response schemas, entitlements, pagination standardization), it is marked partial, not
  supported.
- **API-scope only.** Verdicts here rate the *programmable surface*, not the underlying engine,
  analysis depth, or proven scale.

### Evidence & sources

- **APEX Load (code-owned):** `pkg/api/server.go`, `pkg/api/access_policy.go`, `pkg/api/middleware.go`,
  `pkg/api/handlers.go`, `pkg/api/capacity.go`, `pkg/api/parity_status.go`, `docs/swagger.yaml`,
  `docs/API_USER_GUIDE.md`, `mcp/apexload_mcp/`.
- **LoadRunner Enterprise 26.x:** OpenText LoadRunner Enterprise REST API Reference
  (admhelp.microfocus.com / OpenText documentation portal); LWSSO/run/results wire behavior corroborated
  against an internal LRE orchestration adapter.
- **Companion:** [COMPETITIVE_COMPARISON.md](#loadrunner) (non-API dimensions).

---

## Migration and Import Reference

The following sections carry functional tables referenced by `pkg/api/` evidence fields. Anchors are stable and load-bearing — do not rename headings.

### Shared HTTP Import Safety Contract

Postman, JMeter HTTP, OpenAPI, WSDL, the constrained k6 HTTP subset, and sample/HAR requests retain their source-specific parsers but share effective-request projection, structured credential handling, runtime capability checks, durable review gates, and final DSL validation.

The shared result vocabulary is `executable_exact` (preserved supported behavior), `executable_bounded` (replay-safe runtime clamp plus diagnostic), `blocked_draft` (valid sanitized draft that cannot reach transport), and `rejected` (no valid disclosure-safe draft). These outcomes do not replace existing endpoint response fields or the broader `supported`/`partial`/`review_only` support labels. Full per-importer dispositions and archive-ingress rejection rules are documented in [Web/API Execution Support Matrix](PROTOCOL_SUPPORT.md#tested-adopter-dispositions).

---

### Enterprise Script Archive Routing

APEX Load's Enterprise Script Archive importer is protocol-aware. It classifies ZIP archives containing C-based virtual-user scripts, runtime settings, parameter files, and protocol metadata before conversion using project metadata as the primary signal.

**Import modes:**

| Mode | Meaning |
| --- | --- |
| `executable` | APEX Load has a native converter/runtime path and returns a generated script. |
| `review_only` | Asset can be classified and preserved, but promotion requires operator review or a different importer. |
| `external_runner_required` | Protocol needs a non-APEX Load runner bridge before execution. |
| `unsupported` | Protocol recognized or suspected; no current conversion path available. |

**Current routing:**

| Source protocol family | APEX Load route |
| --- | --- |
| Web HTTP/HTML | `http` executable import |
| Web Services / SOAP | `soap` executable import (exclusive SOAP evidence); mixed SOAP/HTTP stays HTTP with warning |
| Mobile browser-profile archives | `browser` executable import with browser replay gate |
| Web 2.0 browser-profile archives | `browser` review path with required replay and selector evidence |
| Database / ODBC SQL-style scripts | `database` executable import for SQL statements, binds, transactions, and DSN/runtime hints |
| DNS, FTP, SMTP, POP3, IMAP, LDAP, Winsock | `network` executable import with subprotocol config and endpoint placeholders |
| Oracle-Web / Siebel-Web | `http` executable import plus Oracle E-Business correlation review pack |
| SAP GUI, Oracle NCA, Oracle 2-Tier | External-runner review artifact |
| MAPI, Tuxedo, Template VUsers | Review artifact until runner/converter is approved |

If `.usr` metadata and source content conflict, APEX Load returns a review artifact rather than claiming an executable import. The harmless exception: Web HTTP/HTML metadata with exclusive SOAP evidence routes to `soap`.

**API surface:** `POST /api/v1/scripts/import/enterprise-script-archive` and `POST /api/v1/scripts/import/enterprise-script-archive/validate` return `detected_protocol`, `review_artifact`, `summary.import_mode`, confidence, signals, conflicts, and warnings.

---

### Enterprise Archive Runtime Settings Reference

Maps Web HTTP/HTML source runtime settings from enterprise script archives to APEX Load fields and support status.

Code-owned source of truth: `pkg/convert/enterprisearchive/rts_support.go`. Published through `GET /api/v1/runtime-settings/support-matrix` and `GET /api/v1/scripts/import/enterprise-script-archive/support-matrix`.

**Status legend:** `Executed` — enforced at runtime. `Partial` — partial surface enforced. `Compatibility-only` — preserved/editable; no runtime semantics yet. `Non-applicable` — concept doesn't map to APEX Load architecture.

| Runtime-setting area | Source concept | APEX Load field | Status |
| --- | --- | --- | --- |
| Run Logic | Iterations | `config.run_logic.iterations` | Executed |
| Run Logic | Auto-transactions | `config.run_logic.auto_transactions` | Executed |
| Run Logic | Block scheduling | `config.run_logic.block_scheduling` | Compatibility-only |
| Run Logic | Blocks (sequential, random, percentage, nested, block iterations) | `config.run_logic.blocks` | Executed |
| Run Logic | Action sequence | `config.run_logic.action_sequence` | Compatibility-only |
| Pacing | Delay mode and bounds | `config.pacing.*` | Executed |
| Think Time | Ignore, multiplier, limit | `config.think_time.*` | Executed |
| Logging | Log level, error-only, extended options | `config.log.*` | Executed |
| Misc | Continue on error | `config.error_handling.continue_on_error` | Executed |
| Misc | Snapshot on error | `config.error_handling.snapshot_on_error` | Executed |
| Browser Emulation | Cache enabled | `config.browser.cache_enabled` | Executed |
| Browser Emulation | Cache URLs requiring content | `config.browser.cache_content_urls` | Executed |
| Browser Emulation | Check for newer versions | `config.browser.check_newer_versions` | Executed |
| Browser Emulation | New user each iteration | `config.browser.new_user_per_iteration` | Executed |
| Browser Emulation | Download resources = all/none/recorded | `config.browser.download_resources` | Executed |
| Browser Emulation | User agent | `config.browser.user_agent`, `config.http.user_agent` | Executed |
| Browser Emulation | Accept-Language / Accept-Encoding | `config.http.accept_language`, `config.http.accept_encoding` | Executed |
| Proxy | Proxy mode and custom proxy | `config.http.proxy_mode`, `config.http.proxy.*` | Executed |
| Additional Attributes | Arbitrary RTS name/value metadata | `config.web_runtime.additional_attributes` | Compatibility-only |
| Download Filters | Allow list / block list hosts | `config.web_runtime.download_filters.*` | Executed |
| Content Check | Global required/forbidden text checks | `config.web_runtime.content_checks[]` | Executed |
| Network Speed Simulation | Preset and custom bandwidth/latency | `config.web_runtime.network_simulation.*` | Executed |
| Streaming | Adaptive vs fixed mode, buffer, bitrate, bandwidth estimation | `config.web_runtime.streaming.*` | Executed |
| Data Formatting | Auto-detect DFEs, transform chain | `config.web_runtime.data_formatting.*` | Executed |
| Replay engine mode | WinInet / process-vs-thread | N/A | Non-applicable |

**Java runtime settings:** Java-over-HTTP transport settings map to `pkg/protocol/http` fields. For governed `java_vuser` scripts, JDK selection/home, exact JVM argument tokens, and script-controlled path references to operator-provisioned precompiled artifacts map to `config.java_vuser.*` and execute after generator-local preflight. Dedicated user-class loading is partial parity. The legacy Xboot checkbox is preserved as compatibility-only metadata and warning evidence on Java 17+; APEX Load does not emit an `-Xbootclasspath` flag. Runner enablement, harness, work directory, shared classpath, and process capacity remain operator-owned. Multithreading concepts are non-applicable to Go-native protocols — APEX Load uses goroutines — while each Java runner process is separately capacity-bounded.

**Web Services (SOAP) RTS:** WSDL definitions map to `pkg/protocol/soap`. WS-Security configurations map to automated header management in the Go engine. Transport-level settings (proxy, HTTP version, keep-alive, timeouts, SOAP fault content check) map to standard HTTP protocol config.

**Intentional caveats:**
- `iterations` is subject to scenario-schedule authority: a duration-driven scenario ignores a script-only per-VUser cap (imported RTS iteration counts included) so no group retires before the schedule ends, exactly as LoadRunner Controller does. Set iterations on the scenario or group to pin a fixed count. See [ENTERPRISE_CONTRACTS.md](ENTERPRISE_CONTRACTS.md#schedule-authority-over-script-iteration-caps).
- `download_resources = recorded` depends on recorded-resource URL metadata in the import/recording flow.
- Streaming/media settings execute at HTTP response pacing level; no full browser media player.
- DFE parity is bounded to APEX Load's supported HTTP response transform chain.
- Logging parity is scoped to APEX Load's trace envelope, not byte-for-byte VuGen log formatting.

---

### Web 2.0 Browser Profile

First-class APEX Load support for browser-driven Web 2.0 source profiles: import, authoring, replay, repair, and optional protocol-load conversion.

**Scope:**

| Area | Decision |
| --- | --- |
| TruClient 2.0 - Web import | In scope |
| APEX Load Web 2.0 browser authoring profile | In scope |
| Browser replay/debug evidence | In scope |
| Browser-to-HTTP conversion for protocol-load density | In scope |
| Legacy desktop/native application automation | Out of scope |
| High-density real-browser load claims | Evidence-gated non-claim |
| Byte-for-byte VuGen proprietary project compatibility | Out of scope |

**Profile shape:**

```json
{
  "protocol": "browser",
  "metadata": {
    "import": {
      "source": "web2_browser_profile",
      "preserved": {
        "apexload_profile": "web2_browser_profile",
        "source_script_type": "TruClient 2.0 - Web",
        "trueclient2_promotion_review": {
          "status": "replay_evidence_attached",
          "replay_evidence": {
            "session_id": "draft-browser-validation",
            "screenshots": 1,
            "network_entries": 12
          }
        }
      }
    }
  }
}
```

**API surface:** `POST /api/v1/scripts/import/enterprise-script-archive` routes TruClient 2.0 - Web archives to a `browser` review path. `GET /api/v1/runtime-settings/support-matrix` includes Web 2.0 runtime settings rows. `POST /api/v1/scripts/browser-to-http/preview` supports optional protocol-load conversion preview.

**Import detection:** `.usr` script-type detection distinguishes `TruClient 2.0 - Web`, `TruClient - Web`, `TruClient - Mobile Web`, and legacy desktop/native variants.

**Implementation status:** Scope matrix, fixture contract, import detection, runtime settings rows, iframe/window replay evidence, replay preflight evidence gates, saved `trueclient2_promotion_review` artifacts, and browser-to-HTTP conversion preview are complete. Remaining: real TruClient If/Loop archive mapping and deeper selector repair fixture coverage.

---

### JMeter Import

APEX Load converts `.jmx` test plans into APEX Load JSON DSL scripts and optional scenario drafts.

**Status:** Implemented. `POST /api/v1/scripts/import/jmeter`, `POST /api/v1/scripts/import/jmeter/validate`, `POST /api/v1/scripts/import/jmeter/save`, `GET /api/v1/scripts/import/jmeter/support-matrix`.

**Supported constructs:**

| Element | Status |
| --- | --- |
| Test Plan, Thread Group | Supported |
| HTTP Request Defaults, HTTP Header Manager, HTTP Cookie Manager | Supported; ordered HeaderManager duplicates and casing are preserved |
| HTTP Authorization Manager | Basic, bearer, and digest supported; Kerberos/NTLM/negotiate become blocked drafts |
| User Defined Variables, CSV Data Set Config | Supported |
| HTTP Request samplers | Supported |
| Constant Timer, Uniform Random Timer | Supported |
| Response Assertions | Supported |
| Regex / JSONPath / JMESPath / CSS / XPath / XPath2 post-processors | Supported — map to APEX Load action extractors |
| Boundary extractors | Supported for HTTP response body and headers |
| Simple If / fixed Loop / Once Only / Transaction controllers | Supported for the tested deterministic subset |
| Dynamic or complex controller behavior | Valid flattened draft with a durable review gate |
| Basic variable substitution (`${variable}`) | Supported |

**Review/adapter boundaries:** Fully recognized safe single-statement JSR223 patterns can translate, while residual JSR223 / BeanShell / Groovy and third-party plugin behavior becomes disclosure-safe review evidence and cannot execute. JDBC, JMS, FTP/TCP, and SOAP-shaped samplers retain their existing protocol/adapter routes and are not part of the shared JMeter HTTP request pipeline. JMeter listeners remain reporting metadata rather than replay actions.

**Import report contract:** Every import returns `warnings[]`, `unsupported_elements[]`, `assumptions[]`, and metadata (`file name`, `thread group count`, `sampler count`, `variables discovered`, `CSV sources discovered`).

**Thread group mapping:** Thread groups become separate reusable APEX Load scripts with an optional scenario draft (named workload components with VUser counts, ramp-up hints).

**Result import:** k6, Gatling, and JMeter JSON-style summary payloads can be imported and trended via `/api/v1/results/import`.

---

### DevWeb Import

DevWeb is OpenText's JavaScript-based protocol-level HTTP testing engine. APEX Load's HTTP, WebSocket, and gRPC protocols cover DevWeb's core transport scenarios natively.

**API surface:** `POST /api/v1/scripts/import/devweb`, `GET /api/v1/scripts/import/devweb/support-matrix`.

**Coverage:**

| DevWeb capability | APEX Load status |
| --- | --- |
| HTTP/1.1 and HTTP/2 requests | Supported (`pkg/protocol/http`) |
| Request headers, body, cookies | Supported |
| Response extraction (JSON, regex, XPath) | Supported |
| Response assertions | Supported |
| TLS/SSL configuration | Supported |
| Proxy support | Supported |
| Auth (Basic, Bearer) | Supported |
| Data parameterization | Supported |
| Think time / pacing | Supported |
| WebSocket connect/send/receive/ping/close | Supported (`pkg/protocol/websocket`) |
| gRPC unary, streaming, descriptor-set loading | Supported (`pkg/protocol/grpc`) |
| DevWeb `.js` script import (static conversion) | Supported (`pkg/convert/devweb`) |
| Full JavaScript runtime compatibility | Non-goal — dynamic JS execution is review-only |

**Design principles:** One Go goroutine per VUser. WebSocket and gRPC reuse HTTP TLS/proxy/auth config. Protocols compose with the existing VUser loop, metrics, and pause control.

---

### Protocol Bundle Coverage

APEX Load's protocol bundle support is code-owned. Bundle entitlements, runner-pool placement, and the Database / Network / Oracle E-Business / Templates / SAP rows are published through `GET /api/v1/protocol-bundles/support-matrix`, whose `web_api` entries cover native Bruno HTTP import. The protocol-level **Web / API** rows are published through `GET /api/v1/scripts/web-api/support-matrix` (HTTP, SOAP/WSDL, Java-over-HTTP, WebSocket, gRPC, JMS, browser canaries) and `GET /api/v1/protocols/native-support-matrix` (WebSocket, gRPC, JMS Solace / Azure Service Bus / IBM MQ, Kafka).

Bundles correspond to OpenText LoadRunner Enterprise protocol-bundle groups.

| Bundle | Protocol | APEX Load status | Runner class |
| --- | --- | --- | --- |
| **Web / API** | HTTP/HTTPS, WebSocket, gRPC | Supported | Go generator (Linux) |
| **Web / API** | SOAP/WSDL | Supported | Go generator (Linux) |
| **Web / API** | Java-over-HTTP | Supported (bounded codec) | Go generator (Linux) |
| **Web / API** | Bruno collections | Supported as native HTTP conversion; compatibility runner retired | Native HTTP engine |
| **Web / API** | JMS (Solace, IBM MQ, Azure Service Bus) | Partial (Service Bus real adapter opt-in via `-tags jms_servicebus_real`; transactions/no-op ack parity documented) | Go generator (Linux) |
| **Web / API** | Kafka (event streaming) | Supported (mock default; real adapter opt-in via `-tags kafka_real`) | Go generator (Linux) |
| **Database (DB1)** | SQL / ODBC-style (database/sql) | Supported | Go generator (Linux) |
| **Database (DB1)** | Oracle-2 Tier | External-runner required | Governed Oracle client runner |
| **Network (NET1)** | DNS, FTP (control), SMTP, POP3, IMAP, LDAP, Winsock (TCP/UDP) | Supported | Go generator (Linux) |
| **Network (NET1)** | MAPI, Tuxedo | Review-only | Client-runner candidate |
| **Oracle E-Business (OEB1)** | Oracle-Web, Siebel-Web (via HTTP + correlation pack) | Supported for HTTP + review pack | Go generator (Linux) |
| **Oracle E-Business (OEB1)** | Oracle NCA | External-runner / review artifact | Client runner TBD |
| **Oracle E-Business (OEB1)** | DevWeb and JMeter import paths | Supported | Go generator (Linux) |
| **Oracle E-Business (OEB1)** | Gatling script migration | Review-only classification; result-summary ingestion is supported separately | Import review |
| **Templates (TPL1)** | C/C#/VB.NET/Java Template VUsers | Review artifact; worker SDK planned | Sandboxed external worker |
| **SAP (SAP1)** | SAP-Web / Fiori (via HTTP/browser canary) | Supported for HTTP load plus browser-canary evidence | Go generator (Linux) |
| **SAP (SAP1)** | SAP GUI desktop | Foundation entitlement + runner architecture planned | Windows SAP GUI runner pool |

**Entitlement model:** Project governance controls protocol entitlements independently of runtime availability. The scheduler places protocols on appropriate runner pools (Linux Go generators vs. Windows/client runners) based on bundle, OS, client prerequisites, and drain state.

**Parity principles:**
1. Do not compromise high-density Linux protocol execution — HTTP-like protocols stay on Go pods.
2. Protocol bundle support is code-owned and API-published, not docs-only.
3. Proprietary desktop protocols (SAP GUI, Oracle NCA, MAPI) need explicit placement, prerequisites, density limits, and audit evidence.
4. Import and review come before execution — classify assets, preserve source metadata, show gaps before attempting native execution.
5. Separate entitlement from capability — the UI/API distinguishes whether a project is entitled, whether the runtime exists, and whether required runner pools are available.

**Implementation checkpoint (2026-05-19):**

| Workstream | Status |
| --- | --- |
| B0 — Bundle registry, entitlement, support matrix | Implemented |
| DB1 — Database (native SQL path; Oracle-2 Tier external-runner) | Implemented |
| NET1 — Network (standards-based native; MAPI/Tuxedo review-only) | Implemented |
| OEB1 — Oracle E-Business (HTTP-backed + NCA review governance) | Implemented |
| TPL1 — Template VUser worker SDK | Planned |
| SAP1 — SAP GUI foundation entitlement and runner architecture | Foundation entitlement planned |
