# Enterprise Autoscaling Signals

This document defines the first HPA/KEDA signal contract for Stormrunner scale-out workstream P2.2.

## Metrics

Stormrunner exposes these Prometheus metrics for generator autoscaling:

| Metric | Labels | Purpose |
| --- | --- | --- |
| `stormrunner_scaling_vusers` | `state=queued|active` | Primary demand signal for pending generator capacity and currently allocated VUsers. |
| `stormrunner_scaling_tests` | `state=queued|active` | Run concurrency signal for controller/generator scheduling pressure. |
| `stormrunner_scaling_protocol_vusers` | `state`, `protocol` | Protocol mix signal for density-aware pools. |
| `stormrunner_generator_capacity_state_count` | `region`, `state` | Saturation signal for available, constrained, saturated, unknown, and unavailable generator capacity. |
| `stormrunner_generator_region_available_capacity` | `region` | Regional headroom signal. |
| `stormrunner_generator_region_active_vusers` | `region` | Regional allocation signal. |
| `stormrunner_capacity_available_generators` | `scope=configured|effective|live|reserved|shared` | Controller capacity-planning envelope, including reserved and shared generator pools. |

`queued` includes tests in `QUEUED` and `PENDING`. `active` includes `INITIALIZING`, `RUNNING`, and `PAUSED`, because paused tests may still reserve execution capacity.

## HPA/KEDA Policy

Recommended autoscaling policy:

1. Scale out when `stormrunner_scaling_vusers{state="queued"}` remains above zero for multiple scrape intervals.
2. Scale out faster when queued VUsers are concentrated in protocols with lower density, especially browser canaries.
3. Hold or scale out when active VUsers approach regional available capacity.
4. Avoid scaling on `unavailable` generators alone; use it as an alert and replacement signal.
5. Scale in only when queued VUsers are zero and active VUsers remain below the pool's lower utilization target for a cooldown window.

For KEDA, the controller metrics endpoint can be used through the Prometheus scaler with queued VUsers as the trigger query.

Example trigger query:

```promql
sum(stormrunner_scaling_vusers{state="queued"})
```

Example regional trigger query:

```promql
sum by (region) (stormrunner_generator_region_active_vusers)
```

## Capacity Reservations

System settings now include project-level reserved generator pools under `capacity_planning.reservations`:

```json
{
  "capacity_planning": {
    "availability": {
      "source": "configured",
      "configured_available_generators": 5
    },
    "reservations": [
      {
        "project_id": "critical-checkout",
        "reserved_generators": 2
      }
    ]
  }
}
```

Capacity advice evaluates reservations against the test project:

- Tests from a reserved project can use the shared pool plus their reserved generators.
- Tests from other projects only see the shared pool after all reservations are removed.
- Advisory responses include `reservation_project_id`, `reserved_generators`, and `shared_available_generators` so schedulers and dashboards can explain capacity shortfalls.
- `/api/v1/system/health` and `/metrics` expose reserved/shared capacity rollups for operator visibility.

This is the first reserved-pool primitive. Future fair-share placement will consume the same reservation contract when multiple projects contend for generator slots.

## Fair-Share Queue Promotion

Pending test promotion now uses a project-aware fair-share order:

1. Prefer a project with reserved generator capacity that is not yet consumed by that project's active tests.
2. Then prefer the project with fewer active tests.
3. Then prefer the project with fewer active VUsers.
4. Use pending age, creation time, and test ID as deterministic tie breakers.

This keeps old FIFO behavior as the final tie breaker while preventing one busy project from indefinitely occupying every newly available start slot. Capacity shortfall checks still run before promotion, so non-reserved projects cannot consume generator capacity that has been removed from their shared-pool advisory.

## Plan Mapping

| Plan item | Status | Artifact |
| --- | --- | --- |
| P2.5 HPA/KEDA scaling signals | Complete for Prometheus signal contract; cluster manifests remain future work | `/metrics` and this document |
| P2.6 Capacity reservations | Complete for settings, advisory, health, and metrics primitives; placement fair-share remains future work | `capacity_planning.reservations`, capacity advisory fields, `/metrics` |
| P2.7 Fair-share placement | Complete for controller pending-queue promotion; distributed per-generator slot accounting remains future work | Project-aware pending promotion |
