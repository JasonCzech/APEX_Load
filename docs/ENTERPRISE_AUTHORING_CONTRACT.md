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

Each template includes an executable DSL draft plus `recommended_scenario` hints for duration, VUsers, ramp-up, and iteration count. The initial catalog covers HTTP API smoke, browser checkout canary, and SOAP service smoke starters.

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

SQLite-backed controllers persist these sessions in `authoring_sessions`. Controllers without a metadata store keep a local in-memory fallback for development and tests.

Approval is the final backend handoff before a migrated, recorded, manual, or generated draft becomes a reusable scenario asset. The approval endpoint returns HTTP 400 with the standard validation payload when validation fails or the authoring health status is `blocked`. Successful approval stores the draft through the normal script library path, marks the session `approved`, and records `approved_script_id`, `approved_at`, and `approval_health_status` in `review_state`.

Guided correlation repair accepts a proposed variable name, extractor source/type/expression, sample value, and source/usage action hints. Preview mode returns a mutated draft and field-level diffs without saving. Apply mode persists the changed draft, resets the session to `editing`, and records `last_correlation_preview` metadata for audit and UI resume.

## Sources

The matrix covers:

| Source | Status | Key capabilities |
| --- | --- | --- |
| LoadRunner | Active | ZIP acquisition, validate-only review, runtime parity, repair suggestions, replay, browser replay where applicable, save handoff |
| TrueClient Mobile Web | Active | LoadRunner acquisition, browser replay readiness, unsupported-step review, repair suggestions |
| JMeter | Active | JMX acquisition, validate-only review, multi-script output, scenario draft generation |
| DevWeb | Active | JavaScript/ZIP/JSON acquisition, protocol coverage review |
| HAR | Active | HAR/JSON acquisition, correlation and base URL readiness |
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

## Plan Mapping

| Plan item | Status | Artifact |
| --- | --- | --- |
| P3.1 Generic authoring contract | Complete for API contract | `GET /api/v1/scripts/authoring/capabilities` |
| P3.2 Source capability matrix | Complete for backend matrix | `sources` and `source_map` in the authoring capabilities response |
| P3.3 Draft health scoring | Complete for validation response scoring; dashboard badges remain future work | `authoring_health` in script validation responses |
| P3.4 Authoring session persistence | Complete for API and SQLite persistence | `authoring_sessions` table and session CRUD endpoints |
| P3.5 Approval handoff | Complete for backend gate | Session approval validates health, stores reusable scripts, and stamps approval metadata |
| P3.6 Guided correlation repair | Partial backend complete | Session correlation preview/apply returns extractor insertion and sample-replacement diffs |
| P3.7 Data-source fit checks | Partial backend complete | Validation responses include `data_source_fit` and health scoring for source/column/row-count readiness |
| P3.8 Protocol starter templates | Partial backend complete | `GET /api/v1/scripts/authoring/templates` returns validated protocol starter DSLs and scenario hints |
