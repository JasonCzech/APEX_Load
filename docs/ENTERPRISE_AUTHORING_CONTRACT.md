# Enterprise Authoring Contract

This document tracks Workstream P3's shared authoring lifecycle.

## API Contract

`GET /api/v1/scripts/authoring/capabilities` returns the code-owned authoring contract:

- `contract_version`: currently `authoring.v1`
- `draft_lifecycle`: canonical lifecycle stages: acquire, review, validate, edit, replay, save, reopen
- `health_inputs`: shared health-score inputs that future scoring will normalize
- `sources`: source-specific capability matrix
- `source_map`: compact lookup of feature flags by source key

`GET /api/v1/scripts/authoring/templates` returns code-owned protocol starter templates. Query parameters:

- `protocol`: optional exact protocol filter, such as `http`, `browser`, or `soap`
- `use_case`: optional exact use-case filter, such as `ci_smoke` or `enterprise_flow`

Each template includes an executable DSL draft plus `recommended_scenario` hints for duration, VUsers, ramp-up, and iteration count. The catalog covers HTTP API smoke, browser checkout canary, SOAP, Java-over-HTTP, WebSocket, gRPC, JMS, JMeter, HAR, and DevWeb starters.

The dashboard script creation flow loads this backend catalog and renders starter templates ahead of the manual/import lanes. Choosing a template opens the shared draft editor with the executable DSL payload so template-based scripts use the same validation, replay, and save path as manual drafts.

`GET /api/v1/scripts/authoring/components` returns reusable authoring blocks. Query parameters:

- `kind`: optional exact component-kind filter, such as `request_group`, `auth_flow`, `login_transaction`, `data_pool`, or `teardown`
- `protocol`: optional protocol filter; components with wildcard protocol support also match

Each component includes the applicable protocols, summary, tags, and DSL snippets for config, init, actions, teardown, data sources, or parameters. The dashboard Create Script template lane surfaces this component library beside starter templates so authors can discover reusable request groups, auth flows, login transactions, data pools, and teardown blocks before entering the full editor. The ScriptEditor Flow Assembly side lane loads the same backend component catalog filtered by the draft protocol and can insert matching blocks directly into the working draft with guided undo/redo history.

Authoring sessions persist unsaved drafts and repair workflow state:

| Endpoint | Purpose |
| --- | --- |
| `POST /api/v1/scripts/authoring/sessions` | Create a persisted draft session with source, status, draft DSL, and review state. |
| `GET /api/v1/scripts/authoring/sessions?project_id=...` | List resumable sessions, optionally scoped to a project. |
| `GET /api/v1/scripts/authoring/sessions/{id}` | Fetch one draft session and refresh its last-accessed timestamp. |
| `PUT /api/v1/scripts/authoring/sessions/{id}` | Replace the draft payload, status, and review state after edits or repairs. |
| `DELETE /api/v1/scripts/authoring/sessions/{id}` | Remove a draft session after save, approval, or discard. |
| `POST /api/v1/scripts/authoring/sessions/{id}/approve` | Validate a ready draft, block unsafe health states, promote the draft into the reusable script library, and stamp approval metadata on the session. |
| `POST /api/v1/scripts/authoring/sessions/{id}/correlations/preview` | Preview or apply a guided correlation repair by adding an extractor, replacing sample values with `{{var.name}}`, and returning before/after diffs. |

Web/API source acquisition also uses the shared import review envelope:

| Endpoint | Purpose |
| --- | --- |
| `GET /api/v1/scripts/web-api/support-matrix` | Publish Web/API protocol/import support, benchmark envelopes, diagnostics, and sample projects. |
| `POST /api/v1/scripts/import/openapi` | Convert OpenAPI 3.x or Swagger 2.0 JSON/YAML into an HTTP draft with auth hints, data-source seeds, request body templates, assertions, and review sections. |
| `POST /api/v1/scripts/import/wsdl` | Convert WSDL into a SOAP draft with operation discovery, SOAPAction preservation, namespace mapping, sample envelopes, and payload-review sections. |
| `GET /api/v1/scripts/import/devweb/support-matrix` | Publish construct-by-construct DevWeb import support and review requirements. |

SQLite-backed controllers persist these sessions in `authoring_sessions`. Controllers without a metadata store keep a local in-memory fallback for development and tests.

Approval is the final backend handoff before a migrated, recorded, manual, or generated draft becomes a reusable scenario asset. Session responses include backend-computed `approval_ready`, `blocking_health_inputs`, and `next_actions` so dashboards can explain why promotion is allowed or blocked before the user clicks approve. The approval endpoint returns HTTP 400 with the standard validation payload when validation fails or the authoring health status is `blocked`. Successful approval stores the draft through the normal script library path, marks the session `approved`, and records `approved_script_id`, `approved_at`, and `approval_health_status` in `review_state`.

The dashboard Scripts view lists open authoring sessions for the active project as a review queue. Authors can resume the draft in the shared editor, approve a ready session through `POST /api/v1/scripts/authoring/sessions/{id}/approve`, or discard stale sessions through the delete endpoint. Blocked sessions show next actions and disable approval until validation, data fit, runtime parity, replay readiness, or AI guardrail blockers are resolved.

Guided correlation repair accepts a proposed variable name, extractor source/type/expression, sample value, and source/usage action hints. Preview mode returns a mutated draft and field-level diffs without saving. Apply mode persists the changed draft, resets the session to `editing`, and records `last_correlation_preview` metadata for audit and UI resume. The dashboard review queue exposes these controls on open authoring sessions, showing preview/apply results with change counts, changed paths, and resulting authoring health.

## Sources

The matrix covers:

| Source | Status | Key capabilities |
| --- | --- | --- |
| LoadRunner | Active | ZIP acquisition, validate-only review, runtime parity, repair suggestions, replay, browser replay where applicable, save handoff |
| TrueClient Mobile Web | Active | LoadRunner acquisition, browser replay readiness, unsupported-step review, repair suggestions |
| JMeter | Active | JMX acquisition, validate-only review, multi-script output, scenario draft generation |
| DevWeb | Active | JavaScript/ZIP/JSON acquisition, protocol coverage review |
| HAR | Active | HAR/JSON acquisition, correlation and base URL readiness |
| OpenAPI | Active | OpenAPI/Swagger JSON/YAML acquisition, auth hints, parameter data-source seeds, request body templates, status assertions, review sections |
| WSDL | Active | WSDL acquisition, SOAP operation discovery, SOAPAction preservation, namespace mapping, sample envelopes, payload review |
| Recorder | Active | Recording session preview conversion, correlation suggestions |
| Sample | Active | Template/curl acquisition and shared save handoff |
| Manual | Active | Editor draft validation, replay, browser replay for browser scripts |
| AI Generated | Planned | Provenance, diff review, validation, replay, approval gate candidate |

## Health Inputs

Validation responses now include `authoring_health`:

```json
{
  "authoring_health": {
    "score": 90,
    "status": "ready",
    "inputs": [
      {
        "key": "validation_issues",
        "status": "pass",
        "summary": "Script validation passed."
      }
    ]
  }
}
```

The shared status values are:

| Status | Meaning |
| --- | --- |
| `ready` | Draft can proceed through replay/save with no blocking health inputs. |
| `needs_review` | Draft is structurally usable but has warnings or parity gaps that should be reviewed. |
| `blocked` | Draft has validation blockers or a health score below the promotion threshold. |

P3.3 scoring normalizes these inputs instead of creating source-specific scoring paths:

- Validation issues
- Runtime parity
- Unsupported constructs or steps
- Missing data sources
- Replay readiness
- Provenance and diff-review evidence

Validation responses also include `data_source_fit` when a draft declares data sources or data-backed parameters. The fit report checks source existence, required columns, uniqueness/pop strategies, exhaustion posture, and available inline rows against the script's iteration and goal VUser settings. Blocked data-source fit contributes to `authoring_health.status = blocked`, which prevents approval promotion.

AI-generated drafts include `ai_guardrails` in validation responses when `generated_by`, `generated_from`, or import source metadata identifies an AI/LLM source. Guardrails require source provenance, structural validation, diff-review evidence, and replay-before-save evidence. Missing guardrail evidence contributes to `authoring_health.status = blocked`, so AI drafts cannot be approved into reusable assets until review and replay proof are attached.

The ScriptEditor renders validation evidence for both reports after draft or saved-script validation. Authors see authoring health, data-source fit status, row-count context, and AI guardrail blockers in the same validation flow used before replay, save, or approval.

The Script Library adds health filters for blocked drafts, replay evidence gaps, missing data, runtime parity warnings, and AI guardrail warnings. These filters use the same `authoring_health.inputs` payload returned by validation and stored with script records, so dashboard triage follows the backend scoring model rather than a separate UI-only taxonomy.

The draft editor now carries a convergence checklist across manual, recorder, import, OpenAPI, WSDL, HAR, DevWeb, and repair flows: validate, repair, replay, and approve/save. Deterministic repair actions cover missing data sources, hard-coded bearer/session tokens, missing assertions, unstable browser waits, and brittle selectors. Repairs are auditable through the editor change log, reversible through guided history, and visible in Draft Diff Review before save.

## Plan Mapping

| Plan item | Status | Artifact |
| --- | --- | --- |
| P3.1 Generic authoring contract | Complete for API contract | `GET /api/v1/scripts/authoring/capabilities` |
| P3.2 Source capability matrix | Complete for backend matrix | `sources` and `source_map` in the authoring capabilities response |
| P3.3 Draft health scoring | Complete for validation response scoring and dashboard list/review health badges | `authoring_health` in script validation responses |
| P3.4 Authoring session persistence | Complete for API and SQLite persistence | `authoring_sessions` table and session CRUD endpoints |
| P3.5 Approval handoff | Complete | Session approval validates health, stores reusable scripts, stamps approval metadata, exposes approval readiness and next actions, and the Scripts dashboard exposes resume/approve/discard actions for open sessions |
| P3.6 Guided correlation repair | Complete | Session correlation preview/apply returns extractor insertion and sample-replacement diffs; dashboard review queue can preview/apply repairs and display diff summaries |
| P3.7 Data-source fit checks | Complete | Validation responses include `data_source_fit` and health scoring for source/column/row-count readiness; ScriptEditor displays fit evidence after validation |
| P3.8 Protocol starter templates | Complete | `GET /api/v1/scripts/authoring/templates` returns validated protocol starter DSLs and scenario hints; dashboard create flow opens selected templates in the shared draft editor |
| P3.9 AI generation guardrails | Complete for current authoring flow | Validation responses include `ai_guardrails`, block approval health without provenance, diff review, validation, and replay evidence, ScriptEditor displays guardrail evidence after validation, and the authoring review queue disables blocked promotion with next actions |
| C5.6 Reusable component library | Complete | `GET /api/v1/scripts/authoring/components` exposes request groups, auth flows, login transactions, data pools, and teardown blocks; dashboard Create Script shows the catalog beside backend templates and ScriptEditor Flow Assembly inserts protocol-compatible components into drafts |
| W2 Script authoring UX parity | Complete | Source-guided OpenAPI/WSDL/HAR/DevWeb create flows, insertable component blocks, convergence checklist, draft diff review, guided undo/redo, authoring health filters, and deterministic repair actions |
