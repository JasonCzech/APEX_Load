# Java encoding admission and protocol regression inventory — 2026-10-19

A PROTO-A/PROTO-B slice from the [implementation spec](../../../ROADMAP_IMPLEMENTATION_SPEC.md#proto-a--finish-truthful-measurements-and-propagation).

An explicitly unsupported Java HTTP body encoding previously bypassed admission
when the body was nil or empty. The adapter now rejects it before sending target
traffic. Supported empty bodies, default/trimmed base64, existing preflight
snapshots and legacy conversion timing meanings remain unchanged.

An independent loopback endpoint reproduces eight pre-fix nil/empty bypasses.
The regression also checks nonempty rejection, caller input ownership and valid
empty-body execution. No customer capture was replayed.

## Validation

- All Java HTTP, gRPC and WebSocket package tests passed on the applied source:
  80 top-level tests, zero skips or failures.
- Eight targeted generator DTO/API/archive propagation tests passed.
- The [measurement inventory](../../PROTOCOL_TELEMETRY_MATRIX.md) maps the exact
  fixtures and leaves missing consumer, helper, overhead and platform gates open.
- The validation summary (`evidence/validation-summary.json`), compressed logs,
  test binary hashes (`evidence/test-binary-hashes.json`),
  source hashes (`evidence/source-hashes.json`) and runtime (`evidence/runtime.txt`)
  retain the local darwin/arm64 cohort.

`before-stable.log.gz` is the intentional pre-fix failing experiment.
`before.log.gz` is an earlier overlay import-resolution tooling failure and is
not evidence of a product regression. Overlay passes were followed by the
applied-source run. The API propagation run preceded the separate recovery slice.

The private ZIP's actual multipart HAR import was independently verified in the
[scenario review](../roadmap-scenario-prerequisite-2026-10-19/README.md), with
unchanged structural results. This slice changes no importer code.

## Review and limits

The source and test review found no remaining issues: validation precedes HTTP
for every body shape, supported defaults remain executable, caller ownership is
preserved and legacy timing/snapshot fields keep their meanings. Documentation
review maps only tested paths and retains the missing qualification gates.
See review rounds (`evidence/review.txt`). This change
adds no JVM helper, object footprint measurement or release performance claim.
Native platforms, every report/query consumer and remote reference environments
remain unqualified. Local build caches are retained for subsequent slices and
will be cleaned after the session's dependent validation is complete.
