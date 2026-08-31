# Enterprise Load Model Contract

This document tracks Workstream P4's shared workload-shape contract.

## API Contract

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

### Production traffic profiles

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

### Schedule authority over script iteration caps

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

## Initial Matrix

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

## Plan Mapping

| Plan item | Status | Artifact |
| --- | --- | --- |
| P4.1 Load-model support matrix | Complete for backend and scenario-authoring UI | `GET /api/v1/scenarios/load-models/support-matrix`; dashboard scenario form support matrix panel |
| P4.2 Workload-shape templates | Complete for backend and scenario-authoring UI | `GET /api/v1/scenarios/load-models/templates`; dashboard scenario form template picker |
| P4.3 Staged ramp segments | Complete | Group `stages` round-trip through API/storage, derive compatibility ramp execution, can be authored from the dashboard scenario form, and appear in archive load-model goals |
| P4.4 Capacity validation | Complete for current placement model | Draft validation checks modeled demand, capacity, browser canaries, data-pool rows, data-source fit, and generator placement; dashboard scenario form renders pre-save findings |
| P4.5 Rendezvous points | Complete | Runtime `rendezvous` actions synchronize VUsers and validation reports spike readiness |
| P4.6 Global goals | Complete | Runtime enforces group-level `global_iterations` and `max_transactions` across VUsers, dashboard script editor can author both caps, and archive reports expose configured goals plus achieved transaction completion |
| P4.7 Once-only scopes | Complete | Multi-group runtime honors `initialization: at_once` for group-level init/teardown, dashboard scenario authoring can persist the scope, and archive reports summarize once-only groups |
| P4.8 Coordinated-omission reporting | Complete | Archive interval summaries, JSON reports, HTML reports, Parquet exports, archived comparison CSV exports, and the archive UI preserve target TPS, throughput gap, missed interval counts, miss flags, and goal completion context |
