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

For KEDA, scrape the controller `/metrics` endpoint into Prometheus and use the Prometheus scaler with queued VUsers as the trigger query. The local Kubernetes overlay includes `deployments/k8s/local/generator-keda-scaledobject.yaml` as the reference scale-out artifact for the generator StatefulSet.

Example trigger query:

```promql
sum(stormrunner_scaling_vusers{state="queued"})
```

Example regional trigger query:

```promql
sum by (region) (stormrunner_generator_region_active_vusers)
```

## Cluster Artifacts

`deployments/k8s/local/generator-keda-scaledobject.yaml` defines a KEDA `ScaledObject` for `stormrunner-generator`:

- `minReplicaCount: 2` and `maxReplicaCount: 50`, matching the enterprise scale-out envelope.
- Queued VUsers trigger: `sum(stormrunner_scaling_vusers{state="queued"})`.
- Browser canary trigger: `sum(stormrunner_scaling_protocol_vusers{state="queued",protocol="browser"})`.
- Saturation trigger: `sum(stormrunner_generator_capacity_state_count{state="saturated"})`.
- Conservative scale-down stabilization so active or recently queued demand does not collapse the generator pool too quickly.

The default Prometheus endpoint is `http://prometheus-operated.monitoring.svc:9090`; production overlays should replace it when Prometheus uses a different service name or namespace.

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
- `/api/v1/system/health`, `/api/v1/fleet/capacity`, and `/metrics` expose reserved/shared capacity rollups for operator visibility.

The fleet capacity API also includes `project_active_slots`, `project_pending_slots`, and `project_slots[]` so operators can see how much active and queued demand each project is contributing to the shared or reserved pool.

## Fair-Share Queue Promotion

Pending test promotion now uses a project-aware fair-share order:

1. Prefer a project with reserved generator capacity that is not yet consumed by that project's active tests.
2. Then prefer the project with fewer active tests.
3. Then prefer the project with fewer active VUsers.
4. Use pending age, creation time, and test ID as deterministic tie breakers.

This keeps old FIFO behavior as the final tie breaker while preventing one busy project from indefinitely occupying every newly available start slot. Capacity shortfall checks still run before promotion, so non-reserved projects cannot consume generator capacity that has been removed from their shared-pool advisory. `/api/v1/fleet/capacity` publishes the same project-level slot accounting used to explain contention in the Monitoring capacity card.

## Protocol Density Envelopes

Capacity advice now publishes protocol-specific density envelopes in `capacity_advisory.protocol_densities` and uses them when computing VUser-driven generator recommendations.

| Protocol | Max VUsers / generator | Confidence | Notes |
| --- | ---: | --- | --- |
| HTTP / HTTPS / HTTP/2 | 10,000 | Measured target | Baseline goroutine-per-VUser target. |
| gRPC | 12,000 | Model | High-density HTTP/2 transport with compact payloads. |
| WebSocket | 6,000 | Model | Persistent connection footprint lowers density relative to stateless HTTP. |
| SOAP | 5,000 | Model | XML parsing and larger payload assumptions. |
| Java-over-HTTP | 7,000 | Model | HTTP-family behavior with heavier client/runtime assumptions. |
| JMS / Solace / IBM MQ | 3,000 | Model | Broker/session resources drive lower default density. |
| Browser canaries | 20-40 | Device-tier model | Real browser runtime canaries, not HTTP-scale VUsers. |
| Unknown | 4,000 | Conservative | Applied with a warning until the protocol has an explicit envelope. |

The advisor applies the configured headroom percentage to protocol envelopes before calculating required generators. For mixed workloads, the advisory includes one envelope per protocol and the generator recommendation is the maximum of CPU, memory, network, global VUser density, and protocol-specific density.

## Plan Mapping

| Plan item | Status | Artifact |
| --- | --- | --- |
| P2.5 HPA/KEDA scaling signals | Complete for first-pass Prometheus signal contract and KEDA generator scale-out artifact | `/metrics`, this document, `deployments/k8s/local/generator-keda-scaledobject.yaml` |
| P2.6 Capacity reservations | Complete for settings, advisory, health, metrics, fleet API, and Monitoring visibility | `capacity_planning.reservations`, capacity advisory fields, `/metrics`, `/api/v1/fleet/capacity` |
| P2.7 Fair-share placement | Complete for reservation-aware pending promotion and project slot accounting | Project-aware pending promotion, `/api/v1/fleet/capacity` |
| P2.8 Protocol density envelopes | Complete for code-owned advisory envelopes; benchmark calibration remains future hardening | `capacity_advisory.protocol_densities` |
