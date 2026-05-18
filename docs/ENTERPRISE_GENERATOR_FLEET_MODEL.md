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

The current API reports every configured generator as `active`. P2.2 will add explicit cordon/drain mutations and persist operator intent separately from heartbeat reachability:

- `active`: eligible for new placements.
- `cordoned`: no new placements, existing runs continue.
- `draining`: no new placements and existing work should finish or migrate where supported.

## Version And Protocol Compatibility

The initial inventory model reserves `version` and `supported_protocols` so P2.3 can reject incompatible placements before execution starts. Until generator-side version negotiation lands, version remains `unknown` and protocol support mirrors controller-compiled protocol plugins.

## Plan Mapping

| Plan item | Status | Artifact |
| --- | --- | --- |
| P2.1 Formal fleet model | In progress | Inventory fields and this model |
| P2.2 Drain/cordon workflows | Planned | Drain state vocabulary reserved |
| P2.3 Version compatibility checks | Planned | Version and protocol fields reserved |
| P2.4 Saturation alerts and rejection reasons | Planned | Capacity state primitive added |
