# Enterprise Contracts

Status: Current

Consolidated enterprise workstream contracts: script authoring (P3), scenario load models (P4), and project-first governance (P1). Merged from the former ENTERPRISE_AUTHORING_CONTRACT, ENTERPRISE_LOAD_MODEL_CONTRACT, and ENTERPRISE_GOVERNANCE_MODEL documents; endpoints are code-owned and mirrored by the generated endpoint catalog.

Contents:

1. [Authoring contract](#authoring-contract)
2. [Load model contract](#load-model-contract)
3. [Governance model](#governance-model)
4. [Plan mapping](#plan-mapping)

## Authoring contract

This section tracks Workstream P3's shared authoring lifecycle.

### API Contract

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
| `POST /api/v1/scripts/import/enterprise-script-archive` | Convert enterprise script archive ZIPs into executable scripts, review artifacts, or external-runner-required drafts. The response includes `detected_protocol`, `protocol_compat_report`, fidelity/source review, repair suggestions, and optional `review_artifact`. |
| `POST /api/v1/scripts/import/enterprise-script-archive/validate` | Dry-run the same enterprise archive conversion and review envelope without saving. |
| `GET /api/v1/scripts/import/enterprise-script-archive/support-matrix` | Publish source protocol routing, TruClient script-type coverage, Web 2.0 fixture expectations, runtime-setting parity, and external-runner requirements. |
| `GET /api/v1/scripts/web-api/support-matrix` | Publish Web/API protocol/import support, benchmark envelopes, diagnostics, and sample projects. |
| `POST /api/v1/scripts/import/openapi` | Convert OpenAPI 3.x or Swagger 2.0 JSON/YAML into an HTTP draft with auth hints, data-source seeds, request body templates, assertions, and review sections. |
| `POST /api/v1/scripts/import/wsdl` | Convert WSDL into a SOAP draft with operation discovery, SOAPAction preservation, namespace mapping, sample envelopes, and payload-review sections. |
| `GET /api/v1/scripts/import/devweb/support-matrix` | Publish construct-by-construct DevWeb import support and review requirements. |

Web 2.0 browser-profile archives are active through Enterprise Script Archive acquisition. They preserve source browser-profile metadata, map executable browser steps to APEX Load browser DSL, and add profile-specific replay, repair, runtime, and optional browser-to-HTTP conversion review.

Java-family and SAP GUI imports use the same envelope but separate execution posture from import success. Every LoadRunner Java archive produces one `java_vuser` authoring draft, with `init`, `action`, and `end` lifecycle calls kept in source order; detected HTTP, JMS, JDBC, SOAP, and custom Java are source-review evidence rather than independently executable lanes. Java drafts return `import_mode: "external_runner_required"` and require `java_vuser_jvm_runner`; launch additionally requires the generator to advertise `java_vuser_persistent_lifecycle_v1` and `java_vuser_session_frame_token_v1`. A qualified local or loopback-sidecar runner that advertises both capabilities retains one JVM and one `Actions` instance per VUser through `init`, repeated `action` iterations, and `end`. Windows Job Object containment or a narrowly delegated Linux cgroup can qualify; the stock non-privileged Helm, Kustomize, and Compose sidecars remain legacy `full_lifecycle` only. The deprecated `allow_java_vuser_runner` field is accepted but ignored. SAP GUI imports preserve SAP GUI scripting calls and source identity as `sap_gui` drafts that require `windows_sap_gui_runner`.

External-runner posture does not by itself make an otherwise complete Java draft fidelity-blocked. Authors save one review artifact as one authoring session and may approve it into the library after structural review. Launch preflight independently requires the governed runner, approved classpath, and available runner capacity.

AI-assisted authoring now has a dedicated governed workflow envelope:

| Endpoint | Purpose |
| --- | --- |
| `GET /api/v1/ai/authoring/contract` | Publish the AI contract for allowed sources, required evidence, approval gates, audit events, policy endpoint, and review queue. |
| `GET /api/v1/ai/policy?project_id=...` | Expose enabled/disabled state, provider allow-list, redaction posture, retention posture, project disablement, and audit export paths. |
| `POST /api/v1/ai/drafts` | Generate deterministic drafts from OpenAPI, HAR, sample requests, user story text, or existing scripts, then save them as blocked authoring sessions by default. |
| `POST /api/v1/ai/repairs` | Return deterministic repair suggestions and preview scripts for validation failures, data-source fit, correlation gaps, hard-coded data, missing assertions, unstable waits, and runtime parity warnings. |
| `POST /api/v1/ai/explain` | Explain scripts, scenarios, runtime settings, and reports using local citations. |
| `GET /api/v1/ai/review-queue?project_id=...` | List AI-generated or AI-repaired sessions waiting on diff review, validation, replay, and approval. |
| `GET /api/v1/tests/{id}/archive/ai-root-cause` | Wrap archived root-cause evidence in an AI workflow response with citations and limitations. |

SQLite-backed controllers persist these sessions in `authoring_sessions`. Controllers without a metadata store keep a local in-memory fallback for development and tests.

Approval is the final backend handoff before a migrated, recorded, manual, or generated draft becomes a reusable scenario asset. Session responses include backend-computed `approval_ready`, `blocking_health_inputs`, and `next_actions` so dashboards can explain why promotion is allowed or blocked before the user clicks approve. The approval endpoint returns HTTP 400 with the standard validation payload when validation fails or the authoring health status is `blocked`. Successful approval stores the draft through the normal script library path, marks the session `approved`, and records `approved_script_id`, `approved_at`, and `approval_health_status` in `review_state`.

The dashboard Scripts view lists open authoring sessions for the active project as a review queue. Authors can resume the draft in the shared editor, approve a ready session through `POST /api/v1/scripts/authoring/sessions/{id}/approve`, or discard stale sessions through the delete endpoint. Blocked sessions show next actions and disable approval until validation, data fit, runtime parity, replay readiness, or AI guardrail blockers are resolved.

Guided correlation repair accepts a proposed variable name, extractor source/type/expression, sample value, and source/usage action hints. Preview mode returns a mutated draft and field-level diffs without saving. Apply mode persists the changed draft, resets the session to `editing`, and records `last_correlation_preview` metadata for audit and UI resume. The dashboard review queue exposes these controls on open authoring sessions, showing preview/apply results with change counts, changed paths, and resulting authoring health.

### Sources

The matrix covers:

| Source | Status | Key capabilities |
| --- | --- | --- |
| Enterprise Script Archive | Active | ZIP acquisition, validate-only review, runtime parity, repair suggestions, replay, browser replay where applicable, save handoff |
| Mobile browser profile | Active | Enterprise Script Archive acquisition, browser replay readiness, unsupported-step review, repair suggestions |
| Web 2.0 browser profile | Active | Enterprise Script Archive acquisition, executable browser draft import, object descriptor review, step outline creation, browser replay evidence, optional browser-to-HTTP conversion |
| SAP GUI | Active, external runner required | Enterprise Script Archive acquisition, SAP GUI call preservation, source mapping, review artifact save, Windows SAP GUI runner gate |
| Java Vuser combined import | Active, external runner required | Enterprise Script Archive acquisition, singleton Java draft, phase-source order preservation, source idiom review, authoring approval, isolated JVM runner launch gate |
| JMeter | Active | JMX acquisition, validate-only review, multi-script output, scenario draft generation |
| DevWeb | Active | JavaScript/ZIP/JSON acquisition, protocol coverage review |
| HAR | Active | HAR/JSON acquisition, correlation and base URL readiness |
| OpenAPI | Active | OpenAPI/Swagger JSON/YAML acquisition, auth hints, parameter data-source seeds, request body templates, status assertions, review sections |
| WSDL | Active | WSDL acquisition, SOAP operation discovery, SOAPAction preservation, namespace mapping, sample envelopes, payload review |
| Recorder | Active | Recording session preview conversion, correlation suggestions |
| Sample | Active | Template/curl acquisition and shared save handoff |
| Manual | Active | Editor draft validation, replay, browser replay for browser scripts |
| AI Generated | Active | Deterministic draft generation, provenance, policy controls, diff review, validation, replay, repair suggestions, explain mode, review queue, approval gate candidate |

### Health Inputs

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

AI-generated drafts from `POST /api/v1/ai/drafts` start as authoring sessions, not stored scripts. Their metadata includes `ai_contract_version`, `ai_source_type`, `ai_provider`, `diff_reviewed=false`, `replay_status=not_run`, and `validation_status`, which allows the review queue and approval endpoint to enforce the same evidence gate across generated and repaired drafts.

The ScriptEditor renders validation evidence for both reports after draft or saved-script validation. Authors see authoring health, data-source fit status, row-count context, and AI guardrail blockers in the same validation flow used before replay, save, or approval.

The Script Library adds health filters for blocked drafts, replay evidence gaps, missing data, runtime parity warnings, and AI guardrail warnings. These filters use the same `authoring_health.inputs` payload returned by validation and stored with script records, so dashboard triage follows the backend scoring model rather than a separate UI-only taxonomy.

The draft editor now carries a convergence checklist across manual, recorder, import, OpenAPI, WSDL, HAR, DevWeb, and repair flows: validate, repair, replay, and approve/save. Deterministic repair actions cover missing data sources, hard-coded bearer/session tokens, missing assertions, unstable browser waits, and brittle selectors. Repairs are auditable through the editor change log, reversible through guided history, and visible in Draft Diff Review before save.

## Load model contract

This section tracks Workstream P4's shared workload-shape contract.

### API Contract

`GET /api/v1/scenarios/load-models/support-matrix` returns the code-owned load-model support matrix. The dashboard scenario authoring flow renders the same matrix so authors can see supported workload shapes, field mappings, status, and caveats before saving:

- `support_matrix`: supported workload shapes with stable IDs, categories, status, APEX Load shape, fields, and notes
- `status_counts`: count of entries by status
- `notes`: status semantics for operator and dashboard display

`GET /api/v1/scenarios/load-models/templates` returns reusable scenario-shape starters. The dashboard scenario authoring flow consumes this catalog so operators can apply LoadRunner-style workload starters before saving:

- `templates`: scenario drafts with stable IDs, `shape_id`, default duration, group timing, VUser counts, and optional `goal_config`
- `count`: number of returned templates

Query parameters:

- `shape_id`: optional exact filter matching a support-matrix ID, such as `closed-vuser`, `open-arrival-rate`, or `mixed-protocol`

`POST /api/v1/scenarios/validate-load-model` accepts the same draft payload as scenario create and returns a pre-save validation report. The dashboard exposes this report in the scenario form before save:

- `status`: `ready`, `needs_review`, or `blocked`
- `items`: findings across `modeled_demand`, `rendezvous`, `capacity`, `browser_canary`, `data_pool_rows`, `data_source_fit`, and `generator_placement`
- `capacity_advisory`: the same capacity envelope used before execution starts
- `assignment_recommendation`: placement guidance for auto, selected-host, or region-weighted generator assignment

Scenario and test group payloads can now carry staged load segments. The dashboard scenario form includes a staged segment editor that emits this same `groups[].stages` contract and validates the draft before save:

```json
{
  "groups": [
    {
      "script_id": "checkout",
      "stages": [
        { "name": "Warmup", "type": "ramp_up", "duration": "5m", "target_vusers": 100 },
        { "name": "Steady", "type": "hold", "duration": "20m", "target_vusers": 100 },
        { "name": "Cooldown", "type": "ramp_down", "duration": "2m", "target_vusers": 1 }
      ]
    }
  ]
}
```

Supported stage types are `ramp_up`, `hold`, and `ramp_down`. Each stage requires `duration` plus either `target_vusers` or `target_tps`. The runtime derives compatible `ramp_up`, `ramp_down`, and group VUsers from the staged model while preserving the full stage list in API, SQLite scenario records, run provenance, archive reports, and the dashboard archive overview.

#### Production traffic profiles

`workload_shape_id: production-replay` uses a separate, versioned
`traffic_profile` contract. It does not expand production buckets into
`groups[].stages`: a mapped series may retain up to 10,000 normalized points,
and one scenario may retain up to 50,000 mapped points. This keeps a 24-hour,
15-second profile (5,760 points) intact while leaving the manual stage editor
usable for unmapped groups.

Traffic profiles can be analyzed from CSV, XLSX, Prometheus range queries, or
guided OpenSearch date-histogram queries. Analysis and derivation are stateless;
uploaded originals are not retained. Saved query provenance contains the query
and non-secret connection metadata, but launches never refresh from production.
An operator must explicitly refresh and apply a new result. Queued, scheduled,
running, and archived tests retain their immutable profile snapshot.

The profile uses step interpolation and records source semantics
(`transactions_per_bucket`, `cumulative_counter`, `tps`, or `vusers`), gap and
window policy, time/amplitude transforms, warnings, and series-to-group
mappings. Profile-managed groups cannot also carry manual stages or a nonzero
start offset. Unmapped groups keep their static/manual workload.

Peak-window selection can resolve the highest-area interval independently for
each mapped group (`peak_scope: per_group`) or select one shared relative
interval from the aggregate of all mapped groups (`peak_scope: overall`). The
shared option keeps the groups aligned even when their source files use
different calendar dates. Profiles saved before peak scope was introduced
retain the original per-group behavior.

For scenarios with multiple groups, the builder supports either one analyzed
series fanned out to every group or a separate CSV/XLSX upload per group. A
per-group profile stores a composite, non-secret source descriptor plus the
specific file provenance and original span on each mapping. If source spans
differ, derivation proportionally aligns them to the requested test duration,
or to the longest selected span when no duration is supplied. Uploaded file
contents are still not retained.

File timestamp columns may contain absolute timestamps or relative elapsed
values such as `0:00`, `0:15`, and `1:30:00`. Relative values are added to an
operator-selected local start clock (for example, `0:00` mapped to `12:00`) in
the selected IANA timezone before normalization. The timestamp mode, relative
start clock, and timezone are retained as non-secret source provenance; the
builder restores those saved selections when the profile is edited.
Explicit windows for relative sources are stored as `start_offset` and
`end_offset` Go durations measured from each selected series' first point.
This keeps per-group files independent of the browser timezone and avoids
exposing the internal calendar anchor used during tabular parsing.

VUser profiles apply each target directly through the normal local or
distributed scaling backend, including zero. TPS profiles run in
`goal_oriented` mode: each boundary changes the per-group adaptive target while
configured minimum/maximum VUser guardrails remain authoritative. A zero TPS
target suspends the mapped group at zero VUsers; a later positive target seeds
one VUser before closed-loop scaling resumes. Open-arrival scripts cannot be
mapped to adaptive TPS profiles.

TPS mappings may also carry optional average-response-time (ART) sizing
evidence. File sources can map a timestamped ART column in milliseconds or
seconds; query sources currently use an operator-entered peak ART. ART is
normalized to milliseconds and transformed onto the same selected window and
runtime clock as its workload, while TPS amplitude transforms do not affect
ART. If a file-backed ART series does not cover every workload bucket, the
profile records a conservative constant-peak fallback and a warning.

When every profile-managed TPS group has ART evidence, the platform evaluates
Little's Law (`ceil(TPS × ART_ms / 1000)`) at the union of all TPS and ART
boundaries. It records per-group estimates, the actual simultaneous managed
peak, static-group demand, and the total estimated scenario peak. The suggested
global guardrails are a minimum of 1 and a maximum of static demand plus twice
the simultaneous managed estimate, clamped to platform limits. Applying those
guardrails requires explicit dashboard confirmation; incomplete ART evidence
leaves existing guardrails unchanged. Capacity advice uses the suggested
maximum because the adaptive controller may scale to that bound.

Estimated VUsers are planning evidence only. Goal-oriented load charts remain
TPS-only because runtime VUser demand cannot be known in advance; live scaling
continues to be controlled by the adaptive TPS controller.

Adaptive TPS replay is a convergent closed-loop approximation, not an exact
arrival schedule. The goal controller evaluates on a five-second cadence and
uses bounded scale steps. TPS buckets below five seconds are rejected; buckets
from five to sixty seconds are allowed with trackability warnings, and large
adjacent target ratios warn that achieved TPS may lag the requested curve.

The scheduler restores the active point when generator readiness or controller
activation attaches late to an already-started run. A controller process
restart does not resume an active run: the platform's existing fail-closed
reconciliation marks ambiguous in-flight runs failed. Their immutable profile,
target/achieved interval evidence, and archive provenance remain available.

Scripts can also set shared run goals in `config.run_logic`. The dashboard script editor exposes these fields in Runtime Settings > Run Logic:

- `global_iterations`: maximum total started iterations across all VUsers in the executing script group
- `max_transactions`: maximum total protocol action attempts across all VUsers in the executing script group

Archive reports expose `overview.load_model_goals` and `overview.goal_completion` for configured staged groups, once-only scopes, target TPS, global iteration goals, max-transaction goals, achieved transactions, transaction completion percentage, and throughput miss counts.

#### Schedule authority over script iteration caps

Every run is duration-driven, so the scenario schedule — not the script — decides
when a group's VUsers stop. When a test is compiled, a per-VUser iteration cap
that came only from the script (`config.run_logic.iterations`) is dropped, and
the run is logged and reported as such. Without this, a group whose script
carries a cap retires its VUsers as soon as they complete that many iterations
and shuts down mid-run while the other groups keep loading. Imported LoadRunner
scripts are the common case: the enterprise archive importer maps the RTS
"Number of Iterations" into that field and falls back to `1` when the run-logic
profile is missing or ambiguous. LoadRunner Controller resolves the same
conflict the same way — with a scenario duration configured, the RTS iteration
count does not apply.

Precedence, highest first:

1. `iterations` set on the scenario group (`groups[].iterations`) or in
   `runtime_settings.run_logic.iterations` / `runtime_settings.iterations` at
   either the scenario or group scope. This is explicit operator intent — the
   equivalent of LoadRunner's "run until iterations complete" — and it wins.
2. The scenario schedule. With no explicit iteration value, VUsers iterate until
   the schedule (plus ramp-down and baseline windows) expires.
3. The script's own `config.run_logic.iterations`, which is preserved in the
   stored script for authoring and re-import fidelity but is not applied to a
   duration-driven run.

Two things are deliberately outside this rule:

- Shared goals (`global_iterations`, `max_transactions`) still end a run when they
  are met. They are authored run goals, and archive reports surface them as
  configured goals with achieved-versus-configured completion.
- Open-model scripts (`workload_model: open`) keep `iterations`, where it is the
  per-worker arrival budget bounding total offered arrivals rather than a
  per-VUser loop cap.

Applied overrides appear in the pre-launch runtime-settings review as an
informational `run_logic_iterations_schedule_override` finding naming the group
and the cap that stopped applying.

Scenario groups support `initialization` values. The dashboard scenario form exposes this scope so reusable scenarios can preserve shared setup/teardown behavior:

- `at_runtime`: default; every VUser executes script `init` and `teardown`
- `at_once`: the group executes script `init` once before VUsers start and script `teardown` once after the group completes

Status values:

| Status | Meaning |
| --- | --- |
| `executed` | First-class runtime or controller behavior exists today. |
| `partial` | A future shape can be approximated with current fields but still needs dedicated authoring or validation work. The current matrix has no partial entries. |
| `planned` | The shape is tracked but not yet represented as a first-class model. |

### Initial Matrix

The first backend matrix covers:

| Shape | Current status | Current APEX Load shape |
| --- | --- | --- |
| Closed VUser | Executed | Scenario groups with fixed VUsers, duration, ramp-up, and ramp-down. |
| Open arrival-rate | Executed | Open workload runtime with TPS targets and throughput-gap telemetry. |
| Stepped ramp | Executed | First-class `groups[].stages` with ramp-up, hold, ramp-down, target VUsers, target TPS, compatible runtime derivation, and archive/report provenance for staged evidence. |
| Production traffic profile | Executed | Full-resolution CSV/XLSX/Prometheus/OpenSearch step profiles with direct VUser or adaptive-TPS scheduling and immutable run provenance. |
| Rendezvous spike | Executed | `rendezvous` actions synchronize VUsers before a target transaction. |
| Soak | Executed | Long-duration closed or open workload. |
| Stress | Executed | High VUser/TPS targets with capacity advice, staged escalation, goal control, pre-run validation, and archived goal completion. |
| Goal-based | Executed | Goal controller adjusts VUsers within guardrails. |
| Mixed protocol | Executed | Scenario groups can reference different protocol scripts with density-aware capacity advice. |

The first template catalog covers:

| Template | Shape | Purpose |
| --- | --- | --- |
| CI API Smoke | Closed VUser | Pull-request or release-candidate smoke gates. |
| Open API Arrival Rate | Open arrival-rate | Throughput-oriented API testing with goal guardrails. |
| Rendezvous Spike | Rendezvous spike | Spike scenarios for scripts with rendezvous actions. |
| Mixed HTTP Load With Browser Canary | Mixed protocol | Bulk HTTP load plus low-density browser UX canaries. |

## Governance model

This section defines APEX Load's project-first governance model for enterprise parity.

### Ownership Model

APEX Load uses a repository hierarchy for discovery and delegated creation: `Domain > Application Repository > Category > Project > Release`. Projects remain the first-class execution and governance boundary. A project owns authoring assets, execution assets, schedules, data pools, live controls, archives, and retention policy. Administrators grant domain and application-repository roles so delegated users can create categories, projects, and releases without receiving system-wide administration.

| Resource | Owner scope | Project behavior | Audit scope | Retention behavior |
| --- | --- | --- | --- | --- |
| Domain | Global catalog | Tenant/discovery layer above repositories | Global | Retained until empty and deleted by system admin |
| Application Repository | Domain | One repository workspace per domain | Domain/repository | Retained while categories/projects exist |
| Repository category | Application Repository | Groups projects by product area or portfolio slice | Domain/repository | Retained while projects reference it |
| Project | Repository category | Execution and governance boundary | Project/domain | Retained until deleted by project/system admin |
| Folder | Project | Organizes scripts and runs | Project | Deleted with project or explicit folder deletion |
| Application | Project compatibility alias | Legacy grouping for application-scoped release clients | Project | Retained while releases/results reference it |
| Release | Project, with application compatibility alias | Governance context for scenario runs | Project | Archived before deletion when history exists |
| Script | Project/folder | Reusable DSL asset | Project | Retained while referenced by scenarios/results |
| Scenario | Derived from scripts/project | Reusable execution definition | Project | Retained with schedule and result references |
| Scenario schedule | Scenario/project | Recurring run intent | Project | Deleted or disabled explicitly |
| Shared data pool | Project | Reusable parameter data | Project | Can be archived to prevent new use |
| Test run | Project/release/scenario | Execution instance | Project | Retained with archive/report policy |
| Run archive | Test run/project | Immutable evidence bundle | Project | Pruned by future retention policy |
| Service account | Global identity, optional project roles | Can be limited to project roles/scopes | Global | Rotated, disabled, or deleted by system admin |
| Audit event | Global or project | Carries project ID when resource-scoped | Global/project | Retained by audit retention policy |

Legacy or imported resources without a known tenant normalize to the `default` domain, the default application repository, the `uncategorized` category, and the `unassigned` project. This keeps old evidence visible while making ownership gaps explicit.

### Teams Decision

APEX Load does not need first-class teams before project-level quotas and chargeback. Existing identity integration can map IdP groups into global roles or project roles, while projects remain the stable resource boundary.

Add first-class teams only when at least one of these becomes necessary:

- A quota, budget, or approval policy must span multiple projects.
- Chargeback must roll up several projects under one organizational owner.
- Delegated administration needs team-owned service accounts or team-owned schedules.
- External identity groups are too volatile to use directly in policy records.

Until then, use projects for governance and preserve team-like concepts as external identity metadata.

### Usage Accounting Primitive

`GET /api/v1/system/usage` returns usage rollups from current run metadata and archive paths:

- Totals across the query scope.
- Optional `project_id` filter.
- Buckets by project, protocol, and requested generator region.
- Run counts, group counts, total VUsers, duration seconds, VUser-seconds, archive bytes, and status counts.

`GET /api/v1/system/usage/export` returns the same rollups as CSV for chargeback, capacity review, and external governance packs. The export includes one totals row plus project, protocol, and region bucket rows, with deterministic status-count encoding.

`POST /api/v1/system/usage/snapshots` captures the current rollup into durable metadata storage for audit evidence, and `GET /api/v1/system/usage/snapshots` lists saved snapshots. Both support the same optional `project_id` scope as the live usage API; snapshot capture is system-admin only while snapshot reads follow the global reader roles. The controller also captures a project-scoped snapshot when a run reaches a terminal completion path, giving usage history a durable checkpoint without depending on dashboard polling.

For governance windows and scheduled chargeback evidence, `general.usage_snapshot_interval_seconds` enables controller-managed all-project snapshots on a fixed wall-clock cadence. The default is `0` (disabled); set a positive interval in system settings before controller startup to enable automatic cadence capture.

This is intentionally a primitive, not a billing engine. It gives quota, fair-share, and capacity-planning work a shared source of usage facts without requiring raw archive replay.

Known limits for the initial primitive:

- Region buckets use requested assignment metadata, not finalized per-generator placement.
- Archive bytes are computed from local archive paths visible to the controller.
- The automatic server cadence runs only on the admitted execution leader and reloads the latest durable global snapshot timestamp after leadership acquisition. It does not have a separate usage-specific transactional lease or unique time-bucket key, so manual/external captures may coexist and a production AKS failover exercise must still check cadence evidence for the deployment's required duplicate tolerance.

### Audit Extensions

Current audit coverage includes script, scenario, schedule, test lifecycle, service account, settings, release, comparison baseline, release SLA waivers, external result imports, recorder, browser replay, usage snapshots, retention pruning, and capacity governance actions. Capacity governance audit events are emitted when a run is blocked by insufficient generator capacity, deferred by quota/fair-share policy, or promoted by the system after capacity becomes available.

| Event family | Example actions |
| --- | --- |
| Usage policy | `usage.policy.create`, `usage.policy.update`, `usage.policy.delete` |
| Quota and fair share | `capacity.placement.reject`, `capacity.fair_share.defer`, `capacity.reservation.consume` |
| Schedule decisions | `scenario.schedule.occurrence.materialized`, `scenario.schedule.occurrence.skipped`, `scenario.schedule.occurrence.blocked`, `scenario.schedule.approval_required` |
| Run promotion | `test.pending.promote`, `test.result.promote`, `test.result.reject`, `test.baseline.update` |
| Analysis governance | `release.sla.waiver.create`, `external_result.import.create` |
| Retention | `archive.retention.prune`, `archive.retention.hold`, `archive.retention.release` |

Audit details should include the policy revision, evaluated project, actor, decision reason, and affected run or schedule ID.

Archive retention is exposed as an operator-controlled dry-run/apply workflow:

- `POST /api/v1/archives/retention/plan` returns expired archive candidates from the active retention policy without deleting artifacts.
- `POST /api/v1/archives/retention/apply` removes eligible archive directories and writes `archive.retention.prune` audit events with project, release, run status, policy, artifact type, and retention timing details.

## Plan mapping

Combined status of the workstream items covered above (P1 governance, P3 authoring, P4 load model, plus related C/W parity items).

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
| W5 AI-assisted workflow parity | Complete for governed local workflow | `GET /api/v1/ai/authoring/contract`, `POST /api/v1/ai/drafts`, `POST /api/v1/ai/repairs`, `POST /api/v1/ai/explain`, `GET /api/v1/ai/review-queue`, `GET /api/v1/ai/policy`, and `GET /api/v1/tests/{id}/archive/ai-root-cause` |
| W6 Web 2.0 browser-profile parity | Active | Import, replay, runtime settings, and optional browser-to-HTTP conversion preview (`POST /api/v1/scripts/browser-to-http/preview`) are implemented in `pkg/api`; `docs/COMPETITIVE_COMPARISON.md#web-20-browser-profile` remains the workstream definition and evidence reference |
| P4.1 Load-model support matrix | Complete for backend and scenario-authoring UI | `GET /api/v1/scenarios/load-models/support-matrix`; dashboard scenario form support matrix panel |
| P4.2 Workload-shape templates | Complete for backend and scenario-authoring UI | `GET /api/v1/scenarios/load-models/templates`; dashboard scenario form template picker |
| P4.3 Staged ramp segments | Complete | Group `stages` round-trip through API/storage, derive compatibility ramp execution, can be authored from the dashboard scenario form, and appear in archive load-model goals |
| P4.4 Capacity validation | Complete for current placement model | Draft validation checks modeled demand, capacity, browser canaries, data-pool rows, data-source fit, and generator placement; dashboard scenario form renders pre-save findings |
| P4.5 Rendezvous points | Complete | Runtime `rendezvous` actions synchronize VUsers and validation reports spike readiness |
| P4.6 Global goals | Complete | Runtime enforces group-level `global_iterations` and `max_transactions` across VUsers, dashboard script editor can author both caps, and archive reports expose configured goals plus achieved transaction completion |
| P4.7 Once-only scopes | Complete | Multi-group runtime honors `initialization: at_once` for group-level init/teardown, dashboard scenario authoring can persist the scope, and archive reports summarize once-only groups |
| P4.8 Coordinated-omission reporting | Complete | Archive interval summaries, JSON reports, HTML reports, Parquet exports, archived comparison CSV exports, and the archive UI preserve target TPS, throughput gap, missed interval counts, miss flags, and goal completion context |
| P1.6 Ownership semantics | Complete | Ownership model table |
| P1.7 First-class teams decision | Complete | Teams decision section |
| P1.8 Usage accounting primitives | Complete for first-pass live rollups, CSV export, manual snapshot capture/listing, terminal-run snapshot capture, configurable cadence snapshots, and audit events | `/api/v1/system/usage`, `/api/v1/system/usage/export`, `/api/v1/system/usage/snapshots`, `general.usage_snapshot_interval_seconds` |
| P1.9 Governance audit extensions | Complete for current policy engines: settings, resource lifecycle, usage snapshots, retention pruning, schedule decisions, capacity rejection/defer, and pending-run promotion emit audit evidence | Audit extensions section |
