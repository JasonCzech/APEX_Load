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

## Plan Mapping

| Plan item | Status | Artifact |
| --- | --- | --- |
| P2.5 HPA/KEDA scaling signals | Complete for Prometheus signal contract; cluster manifests remain future work | `/metrics` and this document |
