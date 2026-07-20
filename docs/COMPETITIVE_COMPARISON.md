# APEX Load — Competitive Comparison and Migration Reference

**Audience:** internal engineering, product, and evaluators.

APEX Load is a Kubernetes-native HTTP/API load testing platform built on goroutine-based VUsers, an open JSON DSL, and first-class migration paths from enterprise script archives, JMeter, DevWeb, and HAR. It is architecturally cleaner and more usable than LoadRunner Enterprise for day-to-day HTTP/API testing. It trails in correlation intelligence, browser fidelity, offline analysis depth, and battle-tested scale.

This document consolidates all competitor positioning, support matrices, and migration/import reference into one place. The [Migration and Import Reference](#migration-and-import-reference) appendix contains the functional tables (RTS mappings, protocol routing, protocol bundle coverage) that the product's evidence API cites.

---

## Table of Contents

- [Support Matrix](#support-matrix)
- [Per-Competitor Comparison](#per-competitor-comparison)
  - [LoadRunner / LRE](#loadrunner)
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
- [Migration and Import Reference](#migration-and-import-reference)
  - [Enterprise Script Archive Routing](#enterprise-script-archive-routing)
  - [Enterprise Archive Runtime Settings Reference](#enterprise-archive-runtime-settings-reference)
  - [Web 2.0 Browser Profile](#web-20-browser-profile)
  - [JMeter Import](#jmeter-import)
  - [DevWeb Import](#devweb-import)
  - [Protocol Bundle Coverage](#protocol-bundle-coverage)

---

## Support Matrix

Evidence last rechecked 2026-06-02. External baselines: Grafana Cloud k6, Tricentis NeoLoad 2025.3, OpenText LoadRunner Enterprise 26.x.

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

### Source and Authoring Flows

| Source flow | APEX Load status | Evidence | Remaining gap |
| --- | --- | --- | --- |
| Native APEX Load JSON DSL | Supported | `pkg/dsl`, `/api/v1/scripts`, `API_AND_DSL_REFERENCE.md` | More first-party examples. |
| Dashboard guided script creation | Supported | `ScriptEditor.tsx`, `/api/v1/scripts/authoring/sessions` | Expand packaged demo projects. |
| Enterprise Script Archive HTTP import | Supported | `/api/v1/scripts/import/enterprise-script-archive`, `pkg/convert/enterprisearchive` | Long-tail historical protocol families outside planned scope. |
| TrueClient Mobile Web | Partial | `docs/MOBILE_BROWSER_EMULATION_PLAN.md` | Browser VUsers are diagnostic canaries; desktop TruClient parity is out of scope. |
| JMeter plan import | Supported | `/api/v1/scripts/import/jmeter`, `/api/v1/scripts/import/jmeter/support-matrix` | Expand fixture coverage and trend overlays. |
| OpenAPI import | Supported | `/api/v1/scripts/import/openapi`, `pkg/convert/openapi` | Dashboard guided create flow. |
| WSDL import | Supported | `/api/v1/scripts/import/wsdl`, `pkg/convert/wsdl` | Complex XSD payload synthesis remains review-scaffolded. |
| DevWeb import | Supported for import-review scope | `/api/v1/scripts/import/devweb`, `/api/v1/scripts/import/devweb/support-matrix` | Dynamic JavaScript runtime compatibility is review-only. |
| HAR import | Supported | `/api/v1/scripts/import/sample`, `pkg/convert/har` | More guided repair affordances. |
| Raw HTTP / cURL / REST samples | Supported | `/api/v1/scripts/import/sample`, `pkg/convert/sample` | Expand canonical sample projects. |
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
| JMS / broker | Partial (Solace and Azure Service Bus real adapters opt-in) | Extensions | Enterprise | `pkg/protocol/jms` | IBM MQ real adapter remains review-only; Service Bus requires `-tags jms_servicebus_real`. |
| Kafka / event streaming | Supported (mock default; real adapter opt-in) | Extensions | Enterprise | `pkg/protocol/kafka`, `pkg/convert/asyncapi` | Mock broker + AsyncAPI import; live execution via `-tags kafka_real`. |
| WebSocket | Supported | Supported | Supported | `pkg/protocol/websocket` | More subscription-protocol fixtures. |
| gRPC | Supported | Supported | Varies | `pkg/protocol/grpc` | Service-specific proto fixtures. |
| Browser UX canaries | Partial | k6 browser (Playwright-based) | RealBrowser (packaged) | `/api/v1/system/browser-benchmarks`, `docs/MOBILE_BROWSER_EMULATION_PLAN.md` | Attach benchmark artifacts before raising density claims. |
| High-density browser load | Non-goal until evidence | Resource intensive | Packaged sizing guidance | — | Do not claim without hardware-specific benchmark evidence. |
| SAP GUI / Citrix / Oracle Forms | Gap / non-goal | Not core | Packaged | — | Complete demand assessment first. |

### Load Modeling, Execution, and Operations

| Capability | APEX Load status | Evidence | Remaining gap |
| --- | --- | --- | --- |
| Open + closed workload, pacing, think time | Supported | `pkg/engine/open_workload.go`, `pacing.go` | Benchmark-backed goal evidence. |
| Staged ramps and arrival modeling | Supported | `pkg/engine`, `/api/v1/scenarios/validate-load-model` | Published benchmark evidence. |
| Thresholds, SLAs, release gates | Partial | `docs/RUNTIME_TELEMETRY_SLA_PLAN.md`, archive report endpoints | First-class dashboard authoring for checks/thresholds. |
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

### LoadRunner

> Source: June 2026 repository reality-check across the protocol support matrix, import coverage docs, API comparison, and evaluator trust pack. Deliberately unvarnished. Protocol breadth is excluded from this comparison — APEX Load is treated as having sufficient protocol coverage.

**One-line verdict:** APEX wins on architecture and day-to-day usability; LRE wins on migration intelligence, analysis depth, and battle-tested scale.

> **REST API / integration layer:** for a focused comparison of the programmable surface (auth, versioning, streaming, test-as-code, results extraction, CI/webhooks) and the bidirectional API gap analysis, see [APEX_LOAD_REST_API_COMPARISON.md](APEX_LOAD_REST_API_COMPARISON.md). The rows below cover product capability; that doc covers the API layer.

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
| Report output | HTML + archive comparison + baseline pin; no PDF | HTML/PDF/Word, custom templates | **▼** |
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
| P3 | Enterprise hardening — real OTel spans, Vault/mTLS, test-level quota enforcement | M each |

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
- k6 script compatibility until a real importer/exporter is implemented
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

**Evidence:** `docs/PROTOCOL_EXTENSION_PLAN.md`, `docs/BROWSER_CLIENT_METRICS_PLAN.md#non-goals`, `/api/v1/protocol-bundles/support-matrix`

**Remaining:** Complete customer-backed demand assessment. Publish explicit non-goals or coexistence guidance for unsupported legacy protocols. JMS/broker-specific diagnostics (queue depth, DLQ, consumer lag) are partially implemented.

**Decision:** demand-gated non-goal unless a customer-funded product decision changes it.

---

### Packaging, Samples, and Trust

**Status: Partial.** APEX Load has docs, a support matrix, CI templates, dashboard workflows, canonical sample projects, one-command demo seeding, and release evidence manifests. Evaluators still need release-specific benchmark deltas attached before scale claims are raised.

**Evidence:** `README.md#quick-start`, `docs/CICD_INTEGRATION.md`, `docs/EVALUATOR_TRUST_PACK.md`, `docs/EVALUATOR_COMPETITIVE_ONE_PAGER.md`, `docs/RELEASE_EVIDENCE_MANIFEST.md`, `scripts/seed-evaluator-demo.sh`

**Remaining:** Attach release notes with benchmark deltas for each release candidate.

---

## Gap Analysis

A comprehensive audit of APEX Load against enterprise performance testing platform capabilities, compared against LoadRunner Enterprise, Gatling Enterprise, k6 Cloud, NeoLoad, and Locust.

**Already built (representative):** goroutine VUser engine, multi-protocol support, JSON DSL, real-time dashboard, CLI, REST API, Enterprise Script Archive/JMeter import, distributed execution, live VUser controls, Parquet/JSONL archives, system metrics, t-digest percentiles, telemetry pipeline, capacity planning, browser recording, script editor, project/folder organization, shared data pools, error snapshots, browser replay, settings management.

**Remaining significant gaps:**

| Gap | Impact | Status |
| --- | --- | --- |
| CI/CD pipeline integration — JUnit XML, maintained marketplace plugins, Git script sync | Critical | GitHub Actions + JUnit/JSON delivered; cross-platform marketplace plugins out of scope |
| Offline report depth — graph merge, per-txn drill-down, multi-run trend, PDF export | High | Archive + trend analysis, graph/drilldown API/dashboard UX, and executive/engineer PDF export delivered; heavier custom report tooling pending |
| Correlation intelligence depth | High | Response-family rule packs + replay stale-literal evidence delivered; full VuGen-class parity pending |
| Named load zones with benchmark envelopes | High | Named self-hosted zones delivered; benchmark envelopes pending |
| Browser/SPA recording fidelity | Medium | DOM-aware embedded recorder (Playwright-Go + CDP) + replay self-heal delivered; high-density browser *load* execution remains canary-scoped (separate effort) |
| PDF/Word report export | Medium | Executive/engineer PDF export emitted from archive report profiles; Word export deferred |
| Vault / mTLS | Medium | Template refs + redaction exist; Vault/mTLS not started |
| OpenTelemetry signal expansion | Low | Opt-in OTLP traces cover control-plane and sampled action paths; metrics/log export and desktop OTLP export remain |
| SAML | Medium | OIDC + trusted-header is the recommended path; SAML out of scope |
| Hosted/managed control plane | Non-goal | Self-hosted first |

### APM Observability Correlation

**Status: Supported.** HTTP and SOAP execution propagates W3C `traceparent` headers; `trace_id` and `span_id` are archived alongside JSONL/Parquet transaction events. Prometheus controller and generator inventory metrics are available with region-labeled gauges. Dynatrace Events API v2 lifecycle notifications are supported (test start/stop/pause/resume/completion/failure events). Scripts can declare Prometheus-format external metric scrapes under `config.observability.external_metrics`; samples attach to live `resources.external_metrics`, JSONL archives, and Parquet `external_metrics_json`.

**Evidence:** `/metrics`, `/api/v1/integrations/apm/profiles`, `/api/v1/monitoring/packs`, `pkg/api/notifications.go`

**Remaining:** Deepen vendor-specific causal scoring. Real OTel span instrumentation (spans + exporter) is deferred.

---

## Migration and Import Reference

The following sections carry functional tables referenced by `pkg/api/` evidence fields. Anchors are stable and load-bearing — do not rename headings.

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
| Run Logic | Block scheduling | `config.run_logic.block_scheduling` | Executed |
| Run Logic | Action sequence | `config.run_logic.action_sequence` | Executed |
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

**Java-over-HTTP RTS:** Java VM settings (`JDK location`, `Java VM arguments`, `classpath`, `Enable Java VM logging`) map to generator pod environment configuration. Internet Protocol / web settings map to `pkg/protocol/http` config fields (proxy, HTTP version, timeouts, keep-alive, browser emulation, content check). Multithreading concepts are non-applicable — APEX Load uses goroutines.

**Web Services (SOAP) RTS:** WSDL definitions map to `pkg/protocol/soap`. WS-Security configurations map to automated header management in the Go engine. Transport-level settings (proxy, HTTP version, keep-alive, timeouts, SOAP fault content check) map to standard HTTP protocol config.

**Intentional caveats:**
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
| HTTP Request Defaults, HTTP Header Manager, HTTP Cookie Manager | Supported |
| User Defined Variables, CSV Data Set Config | Supported |
| HTTP Request samplers | Supported |
| Constant Timer, Uniform Random Timer | Supported |
| Response Assertions | Supported |
| Regex / JSONPath / JMESPath / CSS / XPath / XPath2 post-processors | Supported — map to APEX Load action extractors |
| Boundary extractors | Supported for HTTP response body and headers |
| Simple controllers (flattenable) | Partial |
| Loop behavior (mappable to runtime settings) | Partial |
| Basic variable substitution (`${variable}`) | Supported |

**Explicitly unsupported in current scope:** JSR223 / BeanShell / Groovy, plugin samplers/listeners/controllers, JMS samplers, JDBC/FTP/SOAP/custom protocol plugins, complex conditional controllers, full JMeter listener/reporting model, distributed engine/controller settings.

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

APEX Load's protocol bundle support is code-owned. Published through `GET /api/v1/protocol-bundles/support-matrix`.

Bundles correspond to OpenText LoadRunner Enterprise protocol-bundle groups.

| Bundle | Protocol | APEX Load status | Runner class |
| --- | --- | --- | --- |
| **Web / API** | HTTP/HTTPS, WebSocket, gRPC | Supported | Go generator (Linux) |
| **Web / API** | SOAP/WSDL | Supported | Go generator (Linux) |
| **Web / API** | Java-over-HTTP | Supported (bounded codec) | Go generator (Linux) |
| **Web / API** | JMS (Solace, IBM MQ, Azure Service Bus) | Partial (Service Bus real adapter opt-in via `-tags jms_servicebus_real`; transactions/no-op ack parity documented) | Go generator (Linux) |
| **Web / API** | Kafka (event streaming) | Supported (mock default; real adapter opt-in via `-tags kafka_real`) | Go generator (Linux) |
| **Database (DB1)** | SQL / ODBC-style (database/sql) | Supported | Go generator (Linux) |
| **Database (DB1)** | Oracle-2 Tier | External-runner required | Governed Oracle client runner |
| **Network (NET1)** | DNS, FTP (control), SMTP, POP3, IMAP, LDAP, Winsock (TCP/UDP) | Supported | Go generator (Linux) |
| **Network (NET1)** | MAPI, Tuxedo | Review-only | Client-runner candidate |
| **Oracle E-Business (OEB1)** | Oracle-Web, Siebel-Web (via HTTP + correlation pack) | Supported for HTTP + review pack | Go generator (Linux) |
| **Oracle E-Business (OEB1)** | Oracle NCA | External-runner / review artifact | Client runner TBD |
| **Oracle E-Business (OEB1)** | DevWeb, JMeter, Gatling (import paths) | Supported | Go generator (Linux) |
| **Templates (TPL1)** | C/C#/VB.NET/Java Template VUsers | Review artifact; worker SDK planned | Sandboxed external worker |
| **SAP (SAP1)** | SAP-Web / Fiori (via HTTP/browser canary) | Partial | Go generator (Linux) |
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
