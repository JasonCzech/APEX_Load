# Enterprise Load Model Contract

This document tracks Workstream P4's shared workload-shape contract.

## API Contract

`GET /api/v1/scenarios/load-models/support-matrix` returns the code-owned load-model support matrix:

- `support_matrix`: supported workload shapes with stable IDs, categories, status, Stormrunner shape, fields, and notes
- `status_counts`: count of entries by status
- `notes`: status semantics for operator and dashboard display

`GET /api/v1/scenarios/load-models/templates` returns reusable scenario-shape starters:

- `templates`: scenario drafts with stable IDs, `shape_id`, default duration, group timing, VUser counts, and optional `goal_config`
- `count`: number of returned templates

Query parameters:

- `shape_id`: optional exact filter matching a support-matrix ID, such as `closed-vuser`, `open-arrival-rate`, or `mixed-protocol`

Scenario and test group payloads can now carry staged load segments:

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
| P4.1 Load-model support matrix | Complete for backend matrix | `GET /api/v1/scenarios/load-models/support-matrix` |
| P4.2 Workload-shape templates | Complete for backend templates | `GET /api/v1/scenarios/load-models/templates` |
| P4.3 Staged ramp segments | Partial backend complete | Group `stages` round-trip through API/storage and derive compatibility ramp execution |
