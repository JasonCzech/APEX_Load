# Enterprise Authoring Contract

This document tracks Workstream P3's shared authoring lifecycle.

## API Contract

`GET /api/v1/scripts/authoring/capabilities` returns the code-owned authoring contract:

- `contract_version`: currently `authoring.v1`
- `draft_lifecycle`: canonical lifecycle stages: acquire, review, validate, edit, replay, save, reopen
- `health_inputs`: shared health-score inputs that future scoring will normalize
- `sources`: source-specific capability matrix
- `source_map`: compact lookup of feature flags by source key

## Sources

The matrix covers:

| Source | Status | Key capabilities |
| --- | --- | --- |
| LoadRunner | Active | ZIP acquisition, validate-only review, runtime parity, repair suggestions, replay, browser replay where applicable, save handoff |
| TrueClient Mobile Web | Active | LoadRunner acquisition, browser replay readiness, unsupported-step review, repair suggestions |
| JMeter | Active | JMX acquisition, validate-only review, multi-script output, scenario draft generation |
| DevWeb | Active | JavaScript/ZIP/JSON acquisition, protocol coverage review |
| HAR | Active | HAR/JSON acquisition, correlation and base URL readiness |
| Recorder | Active | Recording session preview conversion, correlation suggestions |
| Sample | Active | Template/curl acquisition and shared save handoff |
| Manual | Active | Editor draft validation, replay, browser replay for browser scripts |
| AI Generated | Planned | Provenance, diff review, validation, replay, approval gate candidate |

## Health Inputs

Future P3.3 scoring should normalize these inputs instead of creating source-specific scoring paths:

- Validation issues
- Runtime parity
- Unsupported constructs or steps
- Missing data sources
- Replay readiness
- Provenance and diff-review evidence

## Plan Mapping

| Plan item | Status | Artifact |
| --- | --- | --- |
| P3.1 Generic authoring contract | Complete for API contract | `GET /api/v1/scripts/authoring/capabilities` |
| P3.2 Source capability matrix | Complete for backend matrix | `sources` and `source_map` in the authoring capabilities response |
