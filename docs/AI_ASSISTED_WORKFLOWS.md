# AI-Assisted Workflows

APEX Load's AI workflow is governed, deterministic by default, and authoring-session first. AI can generate drafts, suggest repairs, explain artifacts, and summarize archived run evidence, but it cannot silently save or promote a reusable script.

## Contract

`GET /api/v1/ai/authoring/contract` publishes the code-owned AI authoring contract:

- Allowed sources: OpenAPI, HAR, sample requests, user story text, and existing scripts.
- Required evidence: provenance, diff review, structural validation, replay-before-save evidence, and approval.
- Promotion rule: AI output is saved as an authoring session only; reusable script creation still goes through `POST /api/v1/scripts/authoring/sessions/{id}/approve`.
- Audit events: `ai.draft.generate`, `ai.repair.suggest`, `ai.explain`, `ai.root_cause.summarize`, and the existing authoring approval/audit export events.

## Policy

`GET /api/v1/ai/policy?project_id=...` returns enabled state, allowed providers, redaction posture, retention posture, audit export paths, and project-specific disablement.

Environment controls:

| Setting | Purpose | Default |
| --- | --- | --- |
| `APEXLOAD_AI_ENABLED` | Set to `false`, `0`, `off`, `no`, or `disabled` to disable AI assistance globally. | enabled |
| `APEXLOAD_AI_DISABLED_PROJECTS` | Comma-separated project IDs where AI assistance is disabled. | none |
| `APEXLOAD_AI_ALLOWED_PROVIDERS` | Comma-separated provider allow-list. | `deterministic-local,mocked-provider` |
| `APEXLOAD_AI_REDACTION_POSTURE` | Label surfaced in policy and workflow responses. | `standard-redaction` |
| `APEXLOAD_AI_ARTIFACT_RETENTION_DAYS` | Retention label for AI draft/review evidence. | `30` |

The built-in `deterministic-local` provider is the production-safe local contract implementation. `mocked-provider` exists for golden fixtures and provider contract tests.

## Draft Generation

`POST /api/v1/ai/drafts` accepts:

- `source_type`: `openapi`, `har`, `sample_request`, `user_story`, or `existing_script`
- `project_id`
- optional `provider`
- optional `name`, `source_name`, `text`, `prompt`, `source_payload`, or `existing_script_id`
- optional `create_session` flag, defaulting to `true`

The response includes a generated DSL draft, validation results, AI guardrail status, authoring health, repair hints, policy snapshot, citations, and a review queue item when a session was created.

Generated drafts carry:

- `generated_by`
- `generated_from = ai_generated`
- import metadata source `ai_generated`
- preserved `ai_contract_version`, `ai_source_type`, `ai_provider`, `diff_reviewed=false`, `replay_status=not_run`, and `validation_status`

Because diff review and replay evidence start missing, AI-generated sessions are blocked until the user completes the review path.

## Repair Suggestions

`POST /api/v1/ai/repairs` accepts an inline `script`, `script_id`, or `session_id` and returns deterministic suggestions for:

- validation blockers
- data-source fit blockers
- correlation gaps
- hard-coded bearer/session/token/password data
- missing assertions
- unstable browser waits/selectors
- TruClient Web 2.0 selector repair against imported object descriptors and replay DOM evidence
- runtime parity warnings

The endpoint also returns a preview script for safe, mechanical fixes such as adding a `status_code` assertion or browser readiness wait. Preview output remains a draft and is not promoted automatically.

## Explain Mode

`POST /api/v1/ai/explain` supports:

| `target_type` | Evidence source |
| --- | --- |
| `script` | Inline script or stored script ID, validation, runtime report path, AI guardrails |
| `scenario` | Inline scenario draft or stored scenario ID, script composition, runtime report path |
| `runtime_settings` | Unified runtime settings support matrix |
| `report` | Inline archived report or run archive ID, root-cause summary, completeness |

Every explanation includes citations and limitations so prose is grounded in local artifacts.

## AI Root Cause

`GET /api/v1/tests/{id}/archive/ai-root-cause` wraps the deterministic archive root-cause engine in an AI workflow response. It cites the archive report, root-cause engine, and AI contract, and declares that conclusions are evidence-bound to local metrics, errors, resource samples, browser evidence, and imported telemetry.

## Review Queue

`GET /api/v1/ai/review-queue?project_id=...` lists generated or repaired authoring sessions that still need review. Each item includes provider/source provenance, guardrail status, approval readiness, blocking health inputs, and next actions.

The queue complements the broader authoring session APIs and gives dashboard/workflow clients a direct AI-specific review lane.

## MCP Server Integration

Coding and operations agents invoke APEX Load's deterministic authoring, run management, analytics, browser intelligence, and root-cause analysis workflows through the Python Model Context Protocol bridge in `mcp/` (see `mcp/README.md` for stdio client setup and tool contracts):

- **Deterministic authoring and review sessions**: Agents inspect `ai_authoring_contract` and `ai_policy`, generate or repair drafts via `ai_drafts`, `ai_repairs`, `ai_explain`, and `ai_review_queue`, validate and replay drafts (`script_validate_draft`, `script_replay_draft`, `script_diagnose_replay`, `script_authoring_correlation_preview`), and manage conditional edits using `script_get` / `scenario_get` (`query.view="authoring"`) paired with `if_match` on `script_update` / `scenario_update`. AI drafts remain authoring sessions until explicitly promoted via `script_authoring_session_approve`.
- **Run orchestration and governed monitoring**: Agents estimate and manage runs (`run_estimate`, `run_create`, `run_start`, `run_pause`, `run_resume`, `run_stop`, `run_metrics`, `run_sla_status`) and inspect project-scoped Prometheus, Dynatrace, and Elasticsearch/OpenSearch telemetry (`prometheus_*`, `dynatrace_*`, `elastic_*`, `run_monitors_get`, `run_monitor_series`, `run_monitor_evidence`).
- **Browser intelligence and RCA v2 narration verification**: Agents query finalized browser findings, interaction patterns, journey accounting, synthetic/native HAR entries, and revisioned analysis policies (`run_archive_browser_findings`, `run_archive_browser_interactions`, `run_archive_journeys`, `run_archive_browser_har_entry`, `analysis_policy_*`). For root-cause analysis, agents trigger or read v2 assessments (`run_rca_analyze`, `run_rca_status`, `run_rca_assessment`), fetch the bounded `apexload.rca_bundle.v2` evidence bundle (`run_rca_bundle`), verify narrative citations and numeric rounding against that bundle (`run_rca_check_narration`), and persist immutable sidecar critiques (`run_rca_critique_add`, `run_rca_critiques`) without altering the underlying deterministic assessment.
- **Two-tier agent safety fence**: Controller RBAC and token scopes remain authoritative, while the `mcp/` bridge enforces `confirm: true` on mutating tools and both `confirm: true` and a non-empty `reason` on destructive or administrative tools before any HTTP request is sent.

## Verification

Current backend evidence:

- `pkg/api/ai_assistance.go`
- `pkg/api/ai_assistance_test.go`
- `pkg/api/authoring_ai_guardrails.go`
- `pkg/api/c4_analysis_integrations_test.go`

Run:

```bash
go test ./pkg/api -run 'TestAI|TestAIAuthoring|TestAIRoot'
go test -timeout=20m ./...
```
