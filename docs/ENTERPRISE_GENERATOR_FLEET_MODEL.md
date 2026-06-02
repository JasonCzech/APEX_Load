# Enterprise Generator Fleet Model

This document defines the scale-out inventory contract for the APEX Load generator fleet.

## Fleet Inventory Contract

`GET /api/v1/system/inventory` is the fleet inventory surface. Each generator entry exposes:

| Field | Meaning |
| --- | --- |
| `target` | Controller-configured RPC target. |
| `generator_id` | Generator-reported identity when reachable; target address otherwise. |
| `region` | Placement region from controller target configuration. |
| `status` | Current reachability, such as `connected` or `unreachable`. |
| `version` | Generator binary/API version reported by heartbeat when reachable; target metadata or `unknown` otherwise. |
| `supported_protocols` | Protocol names the generator can execute, negotiated from generator heartbeat when reachable. |
| `compatible_features` | Negotiated feature/protocol surface used by fleet capacity responses and dashboard filtering. |
| `drain_state` | Scheduling state: `active`, `cordoned`, or `draining`. |
| `capacity_state` | `available`, `constrained`, `saturated`, `unknown`, or `unavailable`. |
| `health_score` | 0-100 rollup for operator scanning and dashboard sort order. |
| `available_capacity`, `active_vusers`, `max_vusers` | VUser capacity envelope. |
| `cpu_percent`, `memory_bytes`, `goroutine_count` | Runtime resource telemetry from heartbeat. |
| `cpu_limit_cores`, `memory_limit_bytes` | Generator pod/container resource envelope when reported. |

## Capacity State

Capacity state is intentionally simple at this stage:

| State | Rule |
| --- | --- |
| `unavailable` | Generator is not reachable. |
| `unknown` | Generator is reachable but does not report a max VUser envelope. |
| `saturated` | Generator is reachable but has no available VUser capacity. |
| `constrained` | Available capacity is at or below 20 percent of max VUsers. |
| `available` | Generator is reachable and above the constrained threshold. |

## Drain State

Operators can update generator placement state with `PATCH /api/v1/system/generators/drain`:

```json
{
  "target": "apexload-generator-0.apexload-generator:9090",
  "drain_state": "cordoned"
}
```

Supported states:

- `active`: eligible for new placements.
- `cordoned`: no new placements, existing runs continue.
- `draining`: no new placements and existing work should finish or migrate where supported.

Drain state persists through controller restarts in the metadata store. Operators can restart a controller without losing cordon/drain intent, and inventory plus fleet alerts continue to reflect the persisted placement view.

## Version And Protocol Compatibility

Reachable generators negotiate version and protocol capability through the heartbeat stream. The controller prefers those live values for inventory, placement, and compatibility decisions. Controller generator targets can still declare fallback version and protocol compatibility in `APEXLOAD_GENERATORS` for bootstrap and legacy generators:

```text
apexload-generator-0.apexload-generator:9090|region=us-east|version=0.1.0|protocols=http,grpc,websocket
```

Placement enforces the effective capability set before work is assigned:

- A group can only be placed on a generator whose `supported_protocols` contains the group's script protocol.
- A generator with an explicit incompatible major version is excluded from placement.
- `version=unknown` remains allowed for backwards compatibility with generators that do not report version metadata.

The inventory response exposes `compatibility_state` and `compatibility_issues` so operators can see why a target is excluded before a run starts.

## Saturation Metrics And Rejection Reasons

Prometheus exposes fleet rollups for dashboarding and alerts:

- `apexload_generator_capacity_state_count{region,state}`
- `apexload_generator_drain_state_count{region,state}`
- `apexload_generator_compatibility_state_count{region,state}`
- `apexload_generator_region_available_capacity{region}`
- `apexload_generator_region_active_vusers{region}`

Placement rejection messages now include compatibility causes when no target can run a group, including unsupported protocol and incompatible negotiated or declared generator version.

## Plan Mapping

| Plan item | Status | Artifact |
| --- | --- | --- |
| P2.1 Formal fleet model | Complete for first-pass negotiated inventory | Inventory fields and this model |
| P2.2 Drain/cordon workflows | Complete for first-pass persisted placement control | `PATCH /api/v1/system/generators/drain`, persisted drain settings |
| P2.3 Version compatibility checks | Complete for heartbeat-negotiated and target-declared protocol/version compatibility | `version`, `supported_protocols`, `compatible_features`, `compatibility_state`, placement rejection reasons |
| P2.4 Saturation alerts and rejection reasons | Complete for API, metrics, and dashboard alert cards | Fleet Prometheus metrics, fleet alert cards, and placement rejection messages |
