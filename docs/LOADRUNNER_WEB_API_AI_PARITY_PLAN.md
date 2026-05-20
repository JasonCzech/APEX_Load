# LoadRunner Web/API, Browser, Authoring, RTS, And AI Parity Plan

## Objective

Bring APEX Load to practical parity with the modern OpenText LoadRunner Enterprise / OpenText Enterprise Performance Engineering surface in five focused areas:

- Web/API execution
- Script authoring UX
- Runtime settings
- Browser testing
- AI-assisted workflows
- VuGen Web 2.0 / TruClient 2.0 Web scripting profile support

This plan extends the completed `docs/LOADRUNNER_PARITY_EXECUTION_PLAN.md`. That earlier plan closed the planned migration-facing scope for browser testing, script authoring, runtime settings, and Auth/RBAC. This follow-on plan is narrower and more product-facing: it targets the remaining gaps that matter when an evaluator compares APEX Load against the current LoadRunner Enterprise family for modern web, API, browser, and AI-assisted testing.

## External Baseline

Current OpenText positioning emphasizes:

- Centralized enterprise performance engineering and shared assets.
- Broad protocol and technology support, including 180+ protocol/technology coverage.
- VuGen, DevWeb, TruClient, TruClient 2.0, Web HTTP/HTML, Web Services, Java-over-HTTP, and many legacy enterprise protocol families.
- Real-time analytics, analysis templates, trend reports, system reports, and CI/CD integrations.
- AI-assisted scripting and analysis in the 26.1 generation through Aviator and MCP-enabled workflows.

Primary public references:

- OpenText Enterprise Performance Engineering product page: https://www.opentext.com/products/loadrunner-enterprise
- LoadRunner Enterprise 26.1 UI modules: https://admhelp.microfocus.com/lre/en/26.1/online_help/Content/PC/LoadRunner_Enterprise_UI.htm
- VuGen 26.1 protocols: https://admhelp.microfocus.com/vugen/en/26.1/help/WebHelp/Content/VuGen/101200_c_vuser_types.htm
- LoadRunner 26.1 supported protocols: https://admhelp.microfocus.com/documents/lre/Supported_Protocols/26.1/LR_Protocols.htm
- VuGen 26.1 what's new: https://admhelp.microfocus.com/vugen/en/latest/help/WebHelp/Content/WelcomeContent/c_WhatsNew.htm

## Current APEX Load Baseline

APEX Load already has strong footing:

- HTTP, SOAP, Java-over-HTTP, WebSocket, gRPC, browser, and JMS execution paths.
- Code-owned Web/API support matrix with benchmark envelopes, protocol diagnostics, and sample-project references.
- OpenAPI and WSDL backend import/review foundations for HTTP and SOAP authoring.
- DevWeb construct support matrix for mapped executable, partial, and review-only constructs.
- LoadRunner Web HTTP/HTML import, runtime settings classification, migration readiness, validation, replay, and support-matrix review.
- TrueClient Mobile Web import MVP and browser replay validation.
- Guided Create Script UX, backend templates, reusable authoring components, authoring sessions, validation health, data-source fit, guided correlation repair, and approval gates.
- Runtime settings parity for the planned Web HTTP/HTML scope, including executed support for run logic, pacing, think time, logging, error behavior, browser emulation, proxy, download filters, content checks, network shaping, and DFE transforms.
- Browser replay, mobile browser profiles, client metrics, screenshots, DOM, console, network evidence, archive drilldown, comparison views, and diagnostics bundles.
- AI guardrail evidence in validation and approval flows for AI-generated drafts.
- A documented plan for VuGen Web 2.0 / TruClient 2.0 Web import and creation as a distinct browser-profile scope.

Major remaining gaps:

- OpenAPI/WSDL backend import/review is in place; the dashboard guided create flow remains a W2 source-guided UX item.
- No native k6 or DevWeb round-trip parity beyond import/conversion surfaces.
- No high-confidence benchmark publication for Web/API density and browser canary envelopes.
- Browser support is strong as diagnostic canaries, but not marketed as high-density real-browser load.
- AI support now includes governed deterministic draft generation, repair suggestions, explanation, review queue, policy controls, and evidence-bound archived root-cause summaries. External LLM provider integration remains intentionally demand-gated behind the same policy contract.
- VuGen Web 2.0 / TruClient 2.0 Web desktop-browser archives are planned but not yet implemented as a first-class import or creation profile.

## Importance Bands

| Band | Theme | Why It Matters |
| --- | --- | --- |
| P0 | Demo-critical parity | Required for a buyer/evaluator to create, run, validate, and explain a realistic web/API/browser test without falling back to repo knowledge. |
| P1 | Enterprise confidence | Required for migration trust, operator readiness, and repeatable release evidence. |
| P2 | Competitive differentiation | Makes APEX Load feel modern and credible beyond basic parity, especially against AI-assisted and designer-led workflows. |
| P3 | Demand-gated scope | Valuable only when customer-backed demand justifies the engineering and support cost. |

## Feature Parity Matrix

| Category | APEX Load State | LoadRunner Enterprise Baseline | Priority | Parity Gap |
| --- | --- | --- | --- | --- |
| Web/API execution | Strong modern execution for HTTP, SOAP, Java-over-HTTP, WebSocket, gRPC, JMS partial, browser canaries, Web/API support matrix, OpenAPI/WSDL import foundations, and sample pack | Very broad web/API plus legacy protocol family, VuGen, DevWeb, Web Services, Java, database, SAP, Citrix, RDP, Oracle and more | P0/P1 | Add archived benchmark/report evidence and clarify non-goals for legacy protocols. |
| Script authoring UX | Guided workflow lanes, templates, reusable components, import review, validation/replay/repair/approval | VuGen, DevWeb, TruClient/TruClient 2.0, web client script creation, AI-assisted scripting | P0 | Improve component insertion, visual flow assembly, OpenAPI/WSDL guided create flows, and one-click repair loops. |
| Runtime settings | Planned Web HTTP/HTML RTS scope complete with explicit compatibility-only caveats | Deep VuGen protocol-specific RTS across many protocol families | P0/P1 | Expand matrix beyond Web HTTP/HTML and add execution tests for browser and API-specific RTS edge cases. |
| Browser testing | Browser replay, diagnostics, mobile profiles, TrueClient Mobile Web MVP, archive evidence | TruClient, TruClient 2.0, Mobile Web, Native Mobile via Digital Lab, browser workflow maturity | P0/P1 | Publish browser benchmark evidence, deepen TrueClient 2.0/Mobile Web conversion, and improve selector/wait repair. |
| AI-assisted workflows | Governed deterministic draft generation, repair suggestions, explain mode, review queue, policy controls, guardrail blocking, and archive root-cause summaries | AI-assisted scripting and analysis in current OpenText positioning | P1/P2 | External provider integrations and richer dashboard-specific AI affordances remain demand-gated; core governed workflow parity is complete. |
| VuGen Web 2.0 scripting | Planned as a new `vugen_web2` browser-profile workstream | TruClient 2.0 - Web browser scripting, object identification, replay/debug, parameterization, transactions, and conversion to Web HTTP/HTML | P0/P1 | Implement import and creation profile in `docs/VUGEN_WEB2_TRUCLIENT2_PARITY_PLAN.md`. |

## Implementation Order

1. P0 Web/API execution evidence and API authoring foundation.
2. P0 authoring UX completion for reusable component insertion and source-guided workflows.
3. P0/P1 runtime settings expansion beyond the completed Web HTTP/HTML scope.
4. P1 browser benchmark and migration-depth improvements.
5. P1/P2 AI-assisted draft, repair, and analysis workflows.
6. P0/P1 VuGen Web 2.0 / TruClient 2.0 Web import and creation profile.
7. P2 packaging: demos, docs, sample projects, and parity gate evidence.

## Workstream W1: Web/API Execution Parity

### Goal

Make APEX Load feel first-class for modern Web/API execution, not merely capable at the protocol layer.

### Work Items

| ID | Work Item | Priority | Status |
| --- | --- | --- | --- |
| W1.1 | Publish a Web/API execution support matrix covering HTTP, SOAP, Java-over-HTTP, WebSocket, gRPC, JMS, DevWeb import, HAR import, JMeter import, and LoadRunner Web HTTP/HTML import | P0 | Complete - `GET /api/v1/scripts/web-api/support-matrix` |
| W1.2 | Add OpenAPI import/authoring review: parse OpenAPI, generate grouped request flows, detect auth schemes, variables, examples, and candidate data pools | P0 | Complete for backend import/review foundation - `POST /api/v1/scripts/import/openapi` |
| W1.3 | Add WSDL-assisted SOAP authoring: operation picker, SOAPAction, namespace mapping, sample envelope generation, and fault assertions | P1 | Complete for backend import/review foundation - `POST /api/v1/scripts/import/wsdl` |
| W1.4 | Publish benchmark-calibrated density envelopes for HTTP, SOAP, Java-over-HTTP, WebSocket, gRPC, and JMS | P0 | Complete for code-owned publication of current envelopes |
| W1.5 | Add execution smoke fixtures for each Web/API protocol with archived report artifacts checked by CI | P0 | Complete for sample pack, archive-signal fixture contract, and import/API tests; live archived-report publication remains benchmark evidence |
| W1.6 | Add DevWeb construct support matrix and upgrade DevWeb import warnings into a first-class review experience | P1 | Complete - `GET /api/v1/scripts/import/devweb/support-matrix` |
| W1.7 | Add protocol-specific diagnostics for JMS, gRPC, WebSocket, SOAP faults, and Java-over-HTTP decode failures in live and archive views | P1 | Complete for code-owned support publication using existing live/archive signals |
| W1.8 | Add Web/API sample projects: ecommerce HTTP, SOAP account service, gRPC inventory, WebSocket order feed, JMS order flow, mixed workload with browser canary | P1 | Complete - `examples/web-api` sample pack |

### Acceptance Criteria

- A new evaluator can create and run a realistic Web/API test without hand-writing DSL.
- Every supported Web/API protocol has a runnable sample, archive report, and documented support envelope.
- Protocol failures explain what failed in domain terms, not only generic request errors.
- Benchmark claims include hardware, generator profile, script complexity, VUsers, TPS, and archive evidence.

## Workstream W2: Script Authoring UX Parity

### Goal

Close the usability gap with VuGen, DevWeb, TruClient, and designer-style workflows for APEX Load-supported protocol families.

### Work Items

| ID | Work Item | Priority | Status |
| --- | --- | --- | --- |
| W2.1 | Add component insertion from the Create Script component library into the ScriptEditor, not only discovery cards | P0 | Complete - ScriptEditor Flow Assembly inserts controller-owned components into drafts |
| W2.2 | Add visual flow assembly for request groups, auth flows, login transactions, data pools, and teardown blocks | P0 | Complete - grouped request/auth/login/data/teardown blocks are insertable from the editor side lane |
| W2.3 | Add OpenAPI/WSDL/source-file guided create flows to the Create Script workflow picker | P0 | Complete - Start From Source workflow routes OpenAPI, WSDL, HAR, DevWeb, and samples into review/edit flows |
| W2.4 | Add recorder/import/manual convergence checklist inside each draft: validate, repair, replay, approve/save | P0 | Complete - every draft now shows a convergence checklist with validate, repair, replay, and approve/save actions |
| W2.5 | Add visual diff review before saving migrated, generated, or repaired drafts, including a compact summary and full JSON diff | P1 | Complete - Draft Diff Review compares imported or guided-change baselines against current JSON |
| W2.6 | Add undo/redo history for guided correlation, parameter, runtime, and component insertion changes | P1 | Complete - guided history covers runtime edits, parameterization, repair, migration, starter, and component changes |
| W2.7 | Add authoring health dashboard filters: blocked, needs replay, missing data, runtime parity warning, AI guardrail warning | P1 | Complete - Script Library includes category chips with counts and filtered results |
| W2.8 | Add sample-backed "fix this script" actions for common import warnings: selector instability, missing waits, hard-coded tokens, no data source, missing assertions | P1 | Complete - deterministic repair actions cover data sources, tokens, assertions, waits, and selectors |

### Acceptance Criteria

- Users can build a meaningful script by choosing source, protocol, starter, components, and data without touching raw JSON first.
- Every authoring path shows the same health, next action, replay evidence, and approval readiness model.
- Suggested changes are previewable, reversible, and auditable.
- Advanced RTS/replay/parameter controls are available in dialogs and side lanes, not permanent first-screen clutter.

## Workstream W3: Runtime Settings Parity

### Goal

Move from "Web HTTP/HTML RTS parity is complete for the planned scope" to a broader runtime-setting parity program across APEX Load's supported modern protocols.

### Work Items

| ID | Work Item | Priority | Status |
| --- | --- | --- | --- |
| W3.1 | Add a unified runtime settings support endpoint across all supported protocols, not only LoadRunner Web HTTP/HTML import | P0 | Complete - `GET /api/v1/runtime-settings/support-matrix` |
| W3.2 | Expand RTS matrices for browser, SOAP, Java-over-HTTP, WebSocket, gRPC, JMS, DevWeb, HAR, and JMeter-derived scripts | P0 | Complete - unified matrix includes native protocol, import, and migration classifications |
| W3.3 | Add runtime compatibility warnings to scenario validation and run creation, not only script validation/import review | P0 | Complete - scenario validation, estimates, scenario save, and queued-run creation share `runtime_settings_review`; blocking settings stop save/run |
| W3.4 | Add execution tests for browser network/cache/resource RTS edge cases, including recorded-only assets and missing recorded-resource metadata | P1 | Complete - browser cache/resource tests now include recorded-resource metadata guard |
| W3.5 | Add protocol-specific log/snapshot behavior tests for SOAP faults, gRPC status codes, WebSocket close/error frames, JMS broker errors, and Java decode failures | P1 | Complete - support matrix references protocol snapshot/error fixtures for SOAP, gRPC, WebSocket, JMS, and Java-over-HTTP |
| W3.6 | Add runtime profile presets: CI smoke, release gate, migration replay, browser canary, debug trace, soak/stress | P1 | Complete - presets published by the unified support endpoint |
| W3.7 | Add RTS parity report export per script and per scenario | P1 | Complete - JSON/CSV reports at script and scenario runtime-settings report endpoints |
| W3.8 | Decide whether streaming/media RTS remains compatibility-only or gets a funded runtime implementation | P2 | Complete - documented as compatibility-only in `docs/RUNTIME_SETTINGS_PARITY.md` until funded runtime/benchmark evidence exists |

### Acceptance Criteria

- Runtime settings support is visible before save, before run, and in reports.
- Every setting is classified as executed, partial, compatibility-only, non-applicable, blocked, or planned.
- Scenario validation blocks settings that would silently mislead the operator at runtime.
- Runtime setting evidence is exportable for migration review.

## Workstream W4: Browser Testing Parity

### Goal

Strengthen APEX Load's browser story as a trustworthy diagnostic canary workflow while staying honest about high-density browser load.

### Work Items

| ID | Work Item | Priority | Status |
| --- | --- | --- | --- |
| W4.1 | Publish browser benchmark artifacts for desktop Chromium, mobile Chromium, and mobile WebKit profiles on defined hardware | P0 | Complete - `docs/BROWSER_BENCHMARK_ARTIFACTS.md` and `GET /api/v1/system/browser-benchmarks`; controlled raw release runs remain required before raising density envelopes |
| W4.2 | Add browser benchmark results to `/api/v1/system/parity-status` and dashboard parity checklist | P1 | Complete - parity status includes `browser_benchmarks`; dashboard checklist shows browser benchmark evidence and claim gating |
| W4.3 | Deepen TrueClient Mobile Web and TruClient 2.0 conversion fixtures using representative sample archives | P0 | Complete - TruClient 2.0 Mobile Web aliases and representative checkout archive fixture covered |
| W4.4 | Add selector stability scoring and wait repair recommendations for browser imports and recorder output | P0 | Complete - import repair suggestions include selector stability score/signals and readiness-wait actions; recorder/editor repair flow already applies browser wait/selector fixes |
| W4.5 | Add browser replay timeline view: action timeline, network waterfall, console/errors, screenshots, Core Web Vitals, and DOM snapshots in one aligned display | P1 | Complete - Browser Replay Debugger timeline aligns action, network, console, vitals, screenshot, and DOM evidence |
| W4.6 | Add browser environment matrix validation in CI for Chromium/WebKit/device profiles after dependency upgrades | P1 | Complete - benchmark endpoint publishes Chromium/WebKit matrix and `pkg/protocol/browser` tests verify registered profiles in normal CI |
| W4.7 | Add explicit "browser canary plus protocol load" scenario templates and examples | P0 | Complete - `examples/web-api/browser-canary-plus-protocol-load.json` |
| W4.8 | Keep high-density browser load as evidence-gated and document it as non-claim until benchmark artifacts prove otherwise | P0 | Complete as claim rule |

### Acceptance Criteria

- Browser scripts can be authored, replayed, debugged, archived, and compared from a single coherent workflow.
- Browser benchmark data is reproducible and checked into docs or artifacts.
- Migration review identifies unsupported TrueClient constructs and gives practical repair guidance.
- Browser density claims are separated from browser capability claims.

## Workstream W5: AI-Assisted Workflow Parity

### Goal

Move beyond AI guardrails into auditable AI assistance for generation, repair, explanation, and analysis.

### Work Items

| ID | Work Item | Priority | Status |
| --- | --- | --- | --- |
| W5.1 | Define the AI authoring contract: inputs, allowed sources, provenance, diff requirements, replay requirements, approval gates, and audit events | P0 | Complete - `GET /api/v1/ai/authoring/contract` and `docs/AI_ASSISTED_WORKFLOWS.md` |
| W5.2 | Add AI draft generation from OpenAPI, HAR, sample requests, user story text, and existing scripts | P0 | Complete - `POST /api/v1/ai/drafts` creates governed draft sessions without saving reusable scripts |
| W5.3 | Add AI repair suggestions for validation failures, correlation gaps, hard-coded data, missing assertions, unstable waits, and runtime parity warnings | P0 | Complete - `POST /api/v1/ai/repairs` returns deterministic suggestions and preview scripts |
| W5.4 | Add AI explain mode for scripts, scenarios, runtime settings, and reports, with cited evidence from local artifacts | P1 | Complete - `POST /api/v1/ai/explain` supports script, scenario, runtime settings, and report targets |
| W5.5 | Add AI-assisted root-cause summary for archived runs using metrics, errors, resource data, browser evidence, and APM/Prometheus imports | P1 | Complete - `GET /api/v1/tests/{id}/archive/ai-root-cause` wraps archive root-cause evidence |
| W5.6 | Add an AI review queue where generated/repaired drafts are blocked until diff review, validation, replay, and approval are complete | P0 | Complete - `GET /api/v1/ai/review-queue` lists AI sessions with guardrails, blockers, and next actions |
| W5.7 | Add policy controls: AI disabled/enabled per project, allowed providers, data redaction posture, artifact retention, and audit export | P1 | Complete - `GET /api/v1/ai/policy` plus environment-backed project/provider/redaction/retention controls |
| W5.8 | Add golden AI fixtures that prove generated output remains deterministic enough for tests through mocked provider responses | P1 | Complete - deterministic local and mocked providers are covered by `pkg/api/ai_assistance_test.go` |

### Acceptance Criteria

- AI never silently saves or promotes a reusable asset.
- AI output always carries source provenance, diff review, validation evidence, replay evidence, and approval state.
- AI-generated changes are explainable using local artifact evidence, not ungrounded prose.
- Admins can disable AI or restrict it by project/provider/data policy.

## Workstream W6: VuGen Web 2.0 / TruClient 2.0 Web Parity

### Goal

Add full practical support for VuGen's browser-driven Web 2.0 scripting profile: import TruClient 2.0 - Web archives and create equivalent APEX Load browser-profile scripts without conflating that scope with legacy native desktop automation or high-density browser-load claims.

Detailed plan: `docs/VUGEN_WEB2_TRUCLIENT2_PARITY_PLAN.md`.

### Work Items

| ID | Work Item | Priority | Status |
| --- | --- | --- | --- |
| W6.1 | Publish a code-owned TruClient 2.0 Web support matrix and fixture contract | P0 | Planned |
| W6.2 | Extend LoadRunner ZIP import detection and parser for TruClient 2.0 - Web metadata, steps, object descriptors, parameters, transactions, and runtime files | P0 | Planned |
| W6.3 | Add a APEX Load `vugen_web2` browser authoring profile that preserves VuGen metadata while mapping executable steps to browser DSL | P0 | Planned |
| W6.4 | Map P0 Web 2.0 steps: navigate, click, fill, select, wait, verify, transactions, think time, and screenshots | P0 | Planned |
| W6.5 | Add object identification model, selector confidence scoring, and guided selector/wait repair | P0 | Planned |
| W6.6 | Add Create Script profile UX for Web 2.0 scripts with step outline, object descriptor editor, parameters, runtime settings, replay evidence, and approval gates | P0 | Planned |
| W6.7 | Extend runtime settings and replay/archive evidence for Web 2.0 migration readiness | P1 | Planned |
| W6.8 | Add optional browser-to-HTTP conversion workflow for pairing browser canaries with protocol-load scripts | P1 | Planned |
| W6.9 | Add parity-status, docs, API, dashboard, and report evidence for Web 2.0 support | P1 | Planned |

### Acceptance Criteria

- TruClient 2.0 Web archives import through the existing LoadRunner acquisition path without being rejected as legacy desktop TruClient.
- Core Web 2.0 steps replay in APEX Load browser runtime with preserved transactions, parameters, waits, assertions, and screenshots.
- Unsupported proprietary constructs become review sections and repair suggestions.
- Users can create a new APEX Load Web 2.0 browser script from the dashboard without writing JSON first.
- Browser-to-HTTP conversion is available as an explicit review workflow for density use cases.
- High-density browser-load claims remain benchmark-gated and separate from scripting-profile parity.

## Cross-Cutting Evidence Gates

| Gate | Requirement | Applies To |
| --- | --- | --- |
| Support matrix | Each feature has a code-owned status matrix or explicit docs-owned claim rule. | W1, W3, W4 |
| Runnable fixture | Each supported protocol/workflow has a runnable fixture and test coverage. | W1, W2, W3, W4, W6 |
| Archive evidence | Key scenarios produce archive/report evidence that can be inspected after a run. | W1, W3, W4, W5, W6 |
| Browser verification | Dashboard authoring/browser workflows are verified in a real browser. | W2, W4, W5, W6 |
| Security review | AI and credential-handling changes prove redaction, provenance, and audit behavior. | W2, W5 |
| Benchmark evidence | Any density or scale claim includes reproducible hardware/run metadata. | W1, W4 |

## Recommended Milestones

### Milestone 1: Evaluator-Ready Web/API Authoring

- W1.1, W1.2, W1.5
- W2.1, W2.2, W2.3
- W3.1, W3.3

Outcome: a new evaluator can create and run a realistic Web/API scenario from OpenAPI, WSDL, sample traffic, or templates.

### Milestone 2: Migration Confidence

- W3.2, W3.4, W3.5, W3.7
- W4.3, W4.4, W4.7
- W2.5, W2.8

Outcome: LoadRunner/JMeter/HAR/DevWeb/browser imports are reviewable, repairable, and honest about runtime parity before execution.

### Milestone 3: Browser Evidence And Benchmarking

- W1.4
- W4.1, W4.2, W4.5, W4.6

Outcome: APEX Load can claim browser canary support with reproducible evidence and explicitly bounded density claims.

### Milestone 4: AI-Assisted Authoring And Analysis

- W5.1, W5.2, W5.3, W5.6, W5.7, W5.8
- W5.4, W5.5

Outcome: AI assistance becomes a governed workflow for draft generation, repair, explanation, and report analysis.

### Milestone 5: VuGen Web 2.0 Scripting Profile

- W6.1, W6.2, W6.3, W6.4
- W6.5, W6.6
- W6.7, W6.8, W6.9

Outcome: APEX Load can import and create TruClient 2.0 Web-style browser scripts, replay them with migration evidence, and optionally derive HTTP protocol-load scripts.

## Out Of Scope Unless Demand-Gated

- Full parity with every historical LoadRunner protocol family.
- Native desktop TruClient automation.
- High-density browser load claims without hardware-specific benchmark evidence.
- First-class native mobile device farms unless a customer-backed Digital Lab equivalent is funded.
- Unrestricted AI automation that bypasses validation, replay, diff review, or approval.

## Test Plan

- Unit tests for support matrices, OpenAPI/WSDL parsing, runtime classification, AI policy checks, and repair suggestion generators.
- Conversion fixtures for LoadRunner Web HTTP/HTML, TrueClient Mobile Web, TruClient 2.0 where available, HAR, JMeter, DevWeb, OpenAPI, WSDL, and sample requests.
- Runtime tests for protocol-specific RTS behavior and failure/snapshot semantics.
- API handler tests for new support, authoring, AI, validation, and export endpoints with RBAC allow/deny coverage.
- Dashboard tests for workflow picker changes, component insertion, source-guided creation, diff review, repair suggestions, browser evidence, and AI review queues.
- Browser smoke tests for Create Script, ScriptEditor dialogs, replay output, runtime settings, parameter data, browser replay, and AI review workflows.
- Full verification before each checkpoint: `go test ./...`, dashboard tests, `npm run build`, and browser smoke for touched dashboard pages.

## Definition Of Done

This plan is complete when:

- APEX Load can create, run, report, and explain modern Web/API/browser canary tests from guided UI flows without requiring DSL-first knowledge.
- Runtime settings parity is visible and enforceable across supported protocols, not just import review.
- Browser testing has reproducible benchmark evidence and clear claim boundaries.
- AI assistance is useful, auditable, and policy-controlled.
- Every claim in `docs/COMPETITIVE_SUPPORT_MATRIX.md` has code/test/docs or benchmark evidence behind it.
