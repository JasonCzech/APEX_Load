# LoadRunner Web/API, Browser, Authoring, RTS, And AI Parity Plan

## Objective

Bring Stormrunner to practical parity with the modern OpenText LoadRunner Enterprise / OpenText Enterprise Performance Engineering surface in five focused areas:

- Web/API execution
- Script authoring UX
- Runtime settings
- Browser testing
- AI-assisted workflows

This plan extends the completed `docs/LOADRUNNER_PARITY_EXECUTION_PLAN.md`. That earlier plan closed the planned migration-facing scope for browser testing, script authoring, runtime settings, and Auth/RBAC. This follow-on plan is narrower and more product-facing: it targets the remaining gaps that matter when an evaluator compares Stormrunner against the current LoadRunner Enterprise family for modern web, API, browser, and AI-assisted testing.

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

## Current Stormrunner Baseline

Stormrunner already has strong footing:

- HTTP, SOAP, Java-over-HTTP, WebSocket, gRPC, browser, and JMS execution paths.
- LoadRunner Web HTTP/HTML import, runtime settings classification, migration readiness, validation, replay, and support-matrix review.
- TrueClient Mobile Web import MVP and browser replay validation.
- Guided Create Script UX, backend templates, reusable authoring components, authoring sessions, validation health, data-source fit, guided correlation repair, and approval gates.
- Runtime settings parity for the planned Web HTTP/HTML scope, including executed support for run logic, pacing, think time, logging, error behavior, browser emulation, proxy, download filters, content checks, network shaping, and DFE transforms.
- Browser replay, mobile browser profiles, client metrics, screenshots, DOM, console, network evidence, archive drilldown, comparison views, and diagnostics bundles.
- AI guardrail evidence in validation and approval flows for AI-generated drafts.

Major remaining gaps:

- No first-class OpenAPI/WSDL-assisted authoring flow comparable to mature API design workflows.
- No native k6 or DevWeb round-trip parity beyond import/conversion surfaces.
- No high-confidence benchmark publication for Web/API density and browser canary envelopes.
- Browser support is strong as diagnostic canaries, but not marketed as high-density real-browser load.
- AI support is currently guardrails-first, not generation/repair/analysis assistant parity with OpenText Aviator-style workflows.

## Importance Bands

| Band | Theme | Why It Matters |
| --- | --- | --- |
| P0 | Demo-critical parity | Required for a buyer/evaluator to create, run, validate, and explain a realistic web/API/browser test without falling back to repo knowledge. |
| P1 | Enterprise confidence | Required for migration trust, operator readiness, and repeatable release evidence. |
| P2 | Competitive differentiation | Makes Stormrunner feel modern and credible beyond basic parity, especially against AI-assisted and designer-led workflows. |
| P3 | Demand-gated scope | Valuable only when customer-backed demand justifies the engineering and support cost. |

## Feature Parity Matrix

| Category | Stormrunner State | LoadRunner Enterprise Baseline | Priority | Parity Gap |
| --- | --- | --- | --- | --- |
| Web/API execution | Strong modern execution for HTTP, SOAP, Java-over-HTTP, WebSocket, gRPC, JMS partial, browser canaries | Very broad web/API plus legacy protocol family, VuGen, DevWeb, Web Services, Java, database, SAP, Citrix, RDP, Oracle and more | P0/P1 | Add packaged Web/API evidence, OpenAPI/WSDL authoring, benchmark envelopes, and clarify non-goals for legacy protocols. |
| Script authoring UX | Guided workflow lanes, templates, reusable components, import review, validation/replay/repair/approval | VuGen, DevWeb, TruClient/TruClient 2.0, web client script creation, AI-assisted scripting | P0 | Improve component insertion, visual flow assembly, OpenAPI/WSDL import, and one-click repair loops. |
| Runtime settings | Planned Web HTTP/HTML RTS scope complete with explicit compatibility-only caveats | Deep VuGen protocol-specific RTS across many protocol families | P0/P1 | Expand matrix beyond Web HTTP/HTML and add execution tests for browser and API-specific RTS edge cases. |
| Browser testing | Browser replay, diagnostics, mobile profiles, TrueClient Mobile Web MVP, archive evidence | TruClient, TruClient 2.0, Mobile Web, Native Mobile via Digital Lab, browser workflow maturity | P0/P1 | Publish browser benchmark evidence, deepen TrueClient 2.0/Mobile Web conversion, and improve selector/wait repair. |
| AI-assisted workflows | Validation guardrails for AI-generated drafts and approval blocking | AI-assisted scripting and analysis in current OpenText positioning | P1/P2 | Add actual AI draft generation, AI repair suggestions, AI explanation, and AI-assisted report/root-cause summaries with audit controls. |

## Implementation Order

1. P0 Web/API execution evidence and API authoring foundation.
2. P0 authoring UX completion for reusable component insertion and source-guided workflows.
3. P0/P1 runtime settings expansion beyond the completed Web HTTP/HTML scope.
4. P1 browser benchmark and migration-depth improvements.
5. P1/P2 AI-assisted draft, repair, and analysis workflows.
6. P2 packaging: demos, docs, sample projects, and parity gate evidence.

## Workstream W1: Web/API Execution Parity

### Goal

Make Stormrunner feel first-class for modern Web/API execution, not merely capable at the protocol layer.

### Work Items

| ID | Work Item | Priority | Status |
| --- | --- | --- | --- |
| W1.1 | Publish a Web/API execution support matrix covering HTTP, SOAP, Java-over-HTTP, WebSocket, gRPC, JMS, DevWeb import, HAR import, JMeter import, and LoadRunner Web HTTP/HTML import | P0 | Not started |
| W1.2 | Add OpenAPI import/authoring review: parse OpenAPI, generate grouped request flows, detect auth schemes, variables, examples, and candidate data pools | P0 | Not started |
| W1.3 | Add WSDL-assisted SOAP authoring: operation picker, SOAPAction, namespace mapping, sample envelope generation, and fault assertions | P1 | Not started |
| W1.4 | Publish benchmark-calibrated density envelopes for HTTP, SOAP, Java-over-HTTP, WebSocket, gRPC, and JMS | P0 | Partial through advisory docs only |
| W1.5 | Add execution smoke fixtures for each Web/API protocol with archived report artifacts checked by CI | P0 | Partial |
| W1.6 | Add DevWeb construct support matrix and upgrade DevWeb import warnings into a first-class review experience | P1 | Partial |
| W1.7 | Add protocol-specific diagnostics for JMS, gRPC, WebSocket, SOAP faults, and Java-over-HTTP decode failures in live and archive views | P1 | Partial |
| W1.8 | Add Web/API sample projects: ecommerce HTTP, SOAP account service, gRPC inventory, WebSocket order feed, JMS order flow, mixed workload with browser canary | P1 | Partial |

### Acceptance Criteria

- A new evaluator can create and run a realistic Web/API test without hand-writing DSL.
- Every supported Web/API protocol has a runnable sample, archive report, and documented support envelope.
- Protocol failures explain what failed in domain terms, not only generic request errors.
- Benchmark claims include hardware, generator profile, script complexity, VUsers, TPS, and archive evidence.

## Workstream W2: Script Authoring UX Parity

### Goal

Close the usability gap with VuGen, DevWeb, TruClient, and designer-style workflows for Stormrunner-supported protocol families.

### Work Items

| ID | Work Item | Priority | Status |
| --- | --- | --- | --- |
| W2.1 | Add component insertion from the Create Script component library into the ScriptEditor, not only discovery cards | P0 | Not started |
| W2.2 | Add visual flow assembly for request groups, auth flows, login transactions, data pools, and teardown blocks | P0 | Not started |
| W2.3 | Add OpenAPI/WSDL/source-file guided create flows to the Create Script workflow picker | P0 | Not started |
| W2.4 | Add recorder/import/manual convergence checklist inside each draft: validate, repair, replay, approve/save | P0 | Partial |
| W2.5 | Add visual diff review before saving migrated, generated, or repaired drafts, including a compact summary and full JSON diff | P1 | Partial |
| W2.6 | Add undo/redo history for guided correlation, parameter, runtime, and component insertion changes | P1 | Partial for parameter data only |
| W2.7 | Add authoring health dashboard filters: blocked, needs replay, missing data, runtime parity warning, AI guardrail warning | P1 | Partial |
| W2.8 | Add sample-backed "fix this script" actions for common import warnings: selector instability, missing waits, hard-coded tokens, no data source, missing assertions | P1 | Partial |

### Acceptance Criteria

- Users can build a meaningful script by choosing source, protocol, starter, components, and data without touching raw JSON first.
- Every authoring path shows the same health, next action, replay evidence, and approval readiness model.
- Suggested changes are previewable, reversible, and auditable.
- Advanced RTS/replay/parameter controls are available in dialogs and side lanes, not permanent first-screen clutter.

## Workstream W3: Runtime Settings Parity

### Goal

Move from "Web HTTP/HTML RTS parity is complete for the planned scope" to a broader runtime-setting parity program across Stormrunner's supported modern protocols.

### Work Items

| ID | Work Item | Priority | Status |
| --- | --- | --- | --- |
| W3.1 | Add a unified runtime settings support endpoint across all supported protocols, not only LoadRunner Web HTTP/HTML import | P0 | Not started |
| W3.2 | Expand RTS matrices for browser, SOAP, Java-over-HTTP, WebSocket, gRPC, JMS, DevWeb, HAR, and JMeter-derived scripts | P0 | Partial |
| W3.3 | Add runtime compatibility warnings to scenario validation and run creation, not only script validation/import review | P0 | Partial |
| W3.4 | Add execution tests for browser network/cache/resource RTS edge cases, including recorded-only assets and missing recorded-resource metadata | P1 | Partial |
| W3.5 | Add protocol-specific log/snapshot behavior tests for SOAP faults, gRPC status codes, WebSocket close/error frames, JMS broker errors, and Java decode failures | P1 | Partial |
| W3.6 | Add runtime profile presets: CI smoke, release gate, migration replay, browser canary, debug trace, soak/stress | P1 | Not started |
| W3.7 | Add RTS parity report export per script and per scenario | P1 | Partial through import/validation metadata |
| W3.8 | Decide whether streaming/media RTS remains compatibility-only or gets a funded runtime implementation | P2 | Not started |

### Acceptance Criteria

- Runtime settings support is visible before save, before run, and in reports.
- Every setting is classified as executed, partial, compatibility-only, non-applicable, blocked, or planned.
- Scenario validation blocks settings that would silently mislead the operator at runtime.
- Runtime setting evidence is exportable for migration review.

## Workstream W4: Browser Testing Parity

### Goal

Strengthen Stormrunner's browser story as a trustworthy diagnostic canary workflow while staying honest about high-density browser load.

### Work Items

| ID | Work Item | Priority | Status |
| --- | --- | --- | --- |
| W4.1 | Publish browser benchmark artifacts for desktop Chromium, mobile Chromium, and mobile WebKit profiles on defined hardware | P0 | Partial runbook only |
| W4.2 | Add browser benchmark results to `/api/v1/system/parity-status` and dashboard parity checklist | P1 | Not started |
| W4.3 | Deepen TrueClient Mobile Web and TruClient 2.0 conversion fixtures using representative sample archives | P0 | Partial synthetic fixtures |
| W4.4 | Add selector stability scoring and wait repair recommendations for browser imports and recorder output | P0 | Partial repair suggestions |
| W4.5 | Add browser replay timeline view: action timeline, network waterfall, console/errors, screenshots, Core Web Vitals, and DOM snapshots in one aligned display | P1 | Partial through existing evidence panels |
| W4.6 | Add browser environment matrix validation in CI for Chromium/WebKit/device profiles after dependency upgrades | P1 | Partial |
| W4.7 | Add explicit "browser canary plus protocol load" scenario templates and examples | P0 | Partial |
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
| W5.1 | Define the AI authoring contract: inputs, allowed sources, provenance, diff requirements, replay requirements, approval gates, and audit events | P0 | Partial guardrails exist |
| W5.2 | Add AI draft generation from OpenAPI, HAR, sample requests, user story text, and existing scripts | P0 | Not started |
| W5.3 | Add AI repair suggestions for validation failures, correlation gaps, hard-coded data, missing assertions, unstable waits, and runtime parity warnings | P0 | Not started |
| W5.4 | Add AI explain mode for scripts, scenarios, runtime settings, and reports, with cited evidence from local artifacts | P1 | Not started |
| W5.5 | Add AI-assisted root-cause summary for archived runs using metrics, errors, resource data, browser evidence, and APM/Prometheus imports | P1 | Partial deterministic root-cause exists |
| W5.6 | Add an AI review queue where generated/repaired drafts are blocked until diff review, validation, replay, and approval are complete | P0 | Partial through authoring sessions/guardrails |
| W5.7 | Add policy controls: AI disabled/enabled per project, allowed providers, data redaction posture, artifact retention, and audit export | P1 | Not started |
| W5.8 | Add golden AI fixtures that prove generated output remains deterministic enough for tests through mocked provider responses | P1 | Not started |

### Acceptance Criteria

- AI never silently saves or promotes a reusable asset.
- AI output always carries source provenance, diff review, validation evidence, replay evidence, and approval state.
- AI-generated changes are explainable using local artifact evidence, not ungrounded prose.
- Admins can disable AI or restrict it by project/provider/data policy.

## Cross-Cutting Evidence Gates

| Gate | Requirement | Applies To |
| --- | --- | --- |
| Support matrix | Each feature has a code-owned status matrix or explicit docs-owned claim rule. | W1, W3, W4 |
| Runnable fixture | Each supported protocol/workflow has a runnable fixture and test coverage. | W1, W2, W3, W4 |
| Archive evidence | Key scenarios produce archive/report evidence that can be inspected after a run. | W1, W3, W4, W5 |
| Browser verification | Dashboard authoring/browser workflows are verified in a real browser. | W2, W4, W5 |
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

Outcome: Stormrunner can claim browser canary support with reproducible evidence and explicitly bounded density claims.

### Milestone 4: AI-Assisted Authoring And Analysis

- W5.1, W5.2, W5.3, W5.6, W5.7, W5.8
- W5.4, W5.5

Outcome: AI assistance becomes a governed workflow for draft generation, repair, explanation, and report analysis.

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

- Stormrunner can create, run, report, and explain modern Web/API/browser canary tests from guided UI flows without requiring DSL-first knowledge.
- Runtime settings parity is visible and enforceable across supported protocols, not just import review.
- Browser testing has reproducible benchmark evidence and clear claim boundaries.
- AI assistance is useful, auditable, and policy-controlled.
- Every claim in `docs/COMPETITIVE_SUPPORT_MATRIX.md` has code/test/docs or benchmark evidence behind it.
