# Controlled protocol endpoint review — 2026-10-20

This test-only slice extends [PROTO-B](../../../ROADMAP_IMPLEMENTATION_SPEC.md#proto-b--controlled-endpoint-qualification)
and the [measurement matrix](../../PROTOCOL_TELEMETRY_MATRIX.md#controlled-grpc-and-websocket-endpoints).
It changes no runtime behavior or support-promotion record.

## Qualified local fixtures

- gRPC unary, client-streaming, server-streaming and bidi compare observed message/byte counts with literal independent protobuf encodings and endpoint counters. Binary payloads survive unchanged; reflection does not enter call populations.
- Default no-configured-retry and test-only grpc-go retry success/exhaustion preserve attempt payload counts, optional first response and final status. Twelve concurrent calls retain independent observations. The retry service configuration belongs to this fixture, not the authored product contract.
- WebSocket holds one post-handshake transport write at a bounded, deadline-aware gate. Tests reconcile application pending writers, elapsed send work, exact endpoint payloads, timeout without delivery and successful existing reconnect behavior. Clients close and endpoint handlers join before final duplicate-delivery checks.

## Validation and review

Applied test and race counts (`evidence/validation-counts.json`), commands (`evidence/run-checks.sh`),
runtime/library pins (`evidence/runtime.txt`), source hashes (`evidence/source-hashes.json`)
and [review notes](evidence/REVIEW.md) identify each cohort. Both affected protocol
packages run in full with `-count=1`, then again with the race detector. Repository
link/help-contract validation follows. In-app Help and embedded UI sources are
unchanged; the immediately preceding Java slice verified Help in both auth profiles.

Retained draft failures exposed two fixture assumptions: reconnect is enabled by
default, and a write deadline has timeout classification. The failure lane now
explicitly disables reconnect, a separate lane checks successful retry, and the
expected reason matches the existing contract. Review also replaced an immediate
negative delivery check with bounded endpoint draining, and strengthened concurrent
gRPC status validation. Original failures and passing repetitions remain separate.

## Limits

One local darwin/arm64 loopback cohort. gRPC bytes are uncompressed protobuf payloads,
not HTTP/2 transport bytes; concurrent calls do not prove a measured codec-overlap
percentage. WebSocket pending writers are application counts, not native TCP/kernel
queue depth. This does not qualify production overhead, separate-host performance,
the full query/report/baseline consumer chain, the Java JVM helper, Linux/Windows
native behavior or real-device profiles. Those roadmap gates remain open.

Evidence checksums (`evidence/SHA256SUMS`) cover the retained bundle. No private HAR
or converted capture content is included; importer behavior is unchanged.
