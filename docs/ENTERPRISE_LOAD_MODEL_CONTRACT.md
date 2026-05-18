# Enterprise Load Model Contract

This document tracks Workstream P4's shared workload-shape contract.

## API Contract

`GET /api/v1/scenarios/load-models/support-matrix` returns the code-owned load-model support matrix. The dashboard scenario authoring flow renders the same matrix so authors can see supported workload shapes, field mappings, status, and caveats before saving:

- `support_matrix`: supported workload shapes with stable IDs, categories, status, Stormrunner shape, fields, and notes
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

Supported stage types are `ramp_up`, `hold`, and `ramp_down`. Each stage requires `duration` plus either `target_vusers` or `target_tps`. The current runtime derives compatibility `ramp_up`, `ramp_down`, and group VUsers from the staged model while preserving the full stage list in API and SQLite scenario records.

Scripts can also set shared run goals in `config.run_logic`. The dashboard script editor exposes these fields in Runtime Settings > Run Logic:

- `global_iterations`: maximum total started iterations across all VUsers in the executing script group
- `max_transactions`: maximum total protocol action attempts across all VUsers in the executing script group

Scenario groups support `initialization` values. The dashboard scenario form exposes this scope so reusable scenarios can preserve shared setup/teardown behavior:

- `at_runtime`: default; every VUser executes script `init` and `teardown`
- `at_once`: the group executes script `init` once before VUsers start and script `teardown` once after the group completes

Status values:

| Status | Meaning |
| --- | --- |
| `executed` | First-class runtime or controller behavior exists today. |
| `partial` | The shape can be approximated with current fields but still needs dedicated authoring or validation work. |
| `planned` | The shape is tracked but not yet represented as a first-class model. |

## Initial Matrix

The first backend matrix covers:

| Shape | Current status | Current Stormrunner shape |
| --- | --- | --- |
| Closed VUser | Executed | Scenario groups with fixed VUsers, duration, ramp-up, and ramp-down. |
| Open arrival-rate | Executed | Open workload runtime with TPS targets and throughput-gap telemetry. |
| Stepped ramp | Partial | Approximate with multiple groups and timing offsets until staged segments land. |
| Rendezvous spike | Executed | `rendezvous` actions synchronize VUsers before a target transaction. |
| Soak | Executed | Long-duration closed or open workload. |
| Stress | Partial | High VUser/TPS targets with capacity advice and goal control. |
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
| P4.3 Staged ramp segments | Partial backend and UI complete | Group `stages` round-trip through API/storage, derive compatibility ramp execution, and can be authored from the dashboard scenario form |
| P4.4 Capacity validation | Partial backend and UI complete | Draft validation checks modeled demand, capacity, browser canaries, data-pool rows, data-source fit, and generator placement; dashboard scenario form renders pre-save findings |
| P4.5 Rendezvous points | Complete | Runtime `rendezvous` actions synchronize VUsers and validation reports spike readiness |
| P4.6 Global goals | Partial backend and UI complete | Runtime enforces group-level `global_iterations` and `max_transactions` across VUsers, and the dashboard script editor can author both caps |
| P4.7 Once-only scopes | Partial backend and UI complete | Multi-group runtime honors `initialization: at_once` for group-level init/teardown, and dashboard scenario authoring can persist the scope |
| P4.8 Coordinated-omission reporting | Partial backend/UI complete | Archive interval summaries, JSON reports, HTML reports, Parquet exports, archived comparison CSV exports, and the archive UI preserve target TPS, throughput gap, missed interval counts, and miss flags |
