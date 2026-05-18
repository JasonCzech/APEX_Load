# Enterprise Load Model Contract

This document tracks Workstream P4's shared workload-shape contract.

## API Contract

`GET /api/v1/scenarios/load-models/support-matrix` returns the code-owned load-model support matrix:

- `support_matrix`: supported workload shapes with stable IDs, categories, status, Stormrunner shape, fields, and notes
- `status_counts`: count of entries by status
- `notes`: status semantics for operator and dashboard display

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

## Plan Mapping

| Plan item | Status | Artifact |
| --- | --- | --- |
| P4.1 Load-model support matrix | Complete for backend matrix | `GET /api/v1/scenarios/load-models/support-matrix` |
