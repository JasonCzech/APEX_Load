# Enterprise Generator Fleet Model

This document defines the scale-out inventory contract for Workstream P2 in `docs/ENTERPRISE_PLATFORM_PARITY_EXECUTION_PLAN.md`.

## Fleet Inventory Contract

`GET /api/v1/system/inventory` is the fleet inventory surface. Each generator entry exposes:

| Field | Meaning |
| --- | --- |
| `target` | Controller-configured RPC target. |
| `generator_id` | Generator-reported identity when reachable; target address otherwise. |
| `region` | Placement region from controller target configuration. |
| `status` | Current reachability, such as `connected` or `unreachable`. |
| `version` | Generator binary/API version. Initial value is `unknown` until generator negotiation is added. |
| `supported_protocols` | Protocol names the generator can execute. Initially derived from controller-compiled protocols. |
| `drain_state` | Scheduling state. Initial value is `active`; future values include `cordoned` and `draining`. |
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
  "target": "stormrunner-generator-0.stormrunner-generator:9090",
  "drain_state": "cordoned"
}
```

Supported states:

- `active`: eligible for new placements.
- `cordoned`: no new placements, existing runs continue.
- `draining`: no new placements and existing work should finish or migrate where supported.

Drain state is currently controller-local. A future production HA implementation should persist this operator intent in the metadata store so all controller replicas share the same placement view.

## Version And Protocol Compatibility

Controller generator targets can declare version and protocol compatibility in `STORMRUNNER_GENERATORS`:

```text
stormrunner-generator-0.stormrunner-generator:9090|region=us-east|version=0.1.0|protocols=http,grpc,websocket
```

Placement enforces these declarations before work is assigned:

- A group can only be placed on a generator whose `supported_protocols` contains the group's script protocol.
- A generator with an explicit incompatible major version is excluded from placement.
- `version=unknown` remains allowed for backwards compatibility with generators that do not yet negotiate version metadata.

The inventory response exposes `compatibility_state` and `compatibility_issues` so operators can see why a target is excluded before a run starts.

## Plan Mapping

| Plan item | Status | Artifact |
| --- | --- | --- |
| P2.1 Formal fleet model | In progress | Inventory fields and this model |
| P2.2 Drain/cordon workflows | Complete for controller-local placement control; persistence remains future work | `PATCH /api/v1/system/generators/drain` |
| P2.3 Version compatibility checks | Complete for target-declared protocol/version compatibility; generator-side negotiation remains future work | `version`, `protocols`, `compatibility_state`, placement rejection reasons |
| P2.4 Saturation alerts and rejection reasons | Planned | Capacity state primitive added |
