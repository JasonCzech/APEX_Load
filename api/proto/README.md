# Controller ↔ Generator gRPC Protocol (`api/proto`)

Protocol Buffer v3 contract (`package apexload.v1`, Go package `github.com/jasonczech/apexload/api/proto`) defining communication between the APEX Load controller (`pkg/api`) and distributed load generators (`cmd/generator`, `pkg/distributed/generatorrpc`).

## Files

- `generator.proto` — Canonical service and message definitions (`GeneratorControl`).
- `generator.pb.go` — Generated protobuf message types (do not hand-edit).
- `generator_grpc.pb.go` — Generated gRPC client/server stubs (do not hand-edit).

## `GeneratorControl` RPC Surface

| Category | RPCs | Purpose |
| --- | --- | --- |
| Capacity & Execution Fencing | `ReservePodCapacity`, `ClaimExecutionEpoch`, `ClaimRunSupervision` | Reserves per-pod CPU/memory/JVM capacity, fences stale HA controller terms via monotonic execution epochs, and renews generator run-supervision leases (`APEXLOAD_GENERATOR_RUN_SUPERVISION_TTL`). |
| Two-Phase Work Staging | `DistributeScript`, `AssignWork`, `CheckPreparation`, `CommitWork` | Distributes scripts/assets, stages VUser groups (`AssignWork`), polls asynchronous runtime preparation (such as shared/pooled JVMs via `CheckPreparation`), and starts or re-adopts execution (`CommitWork`). |
| Live Run Controls | `AdjustVUsers`, `AdjustDuration`, `SetThroughputCap`, `SetPaused`, `StopTest` | Routes live per-group VUser scaling, duration updates, goal-oriented TPS caps, soft pause/resume, and graceful stop/abort commands to owning generators. |
| Bidirectional Runtime Streams | `StreamMetrics`, `LiveTableChannel`, `RendezvousChannel`, `Heartbeat` | Streams periodic `MetricsBatch` snapshots (including mergeable `DurationSketch` histograms, lag timestamps, and protocol diagnostics), coordinates distributed native live tables and rendezvous barriers, and exchanges liveness/capacity heartbeats. |
| Archive & Retained Evidence | `GetArchiveArtifact`, `StreamArchiveArtifact`, `AcknowledgeEvidence` | Transfers finalized run archive manifests, JSONL/Parquet chunks, and retained browser/HAR evidence back to the controller before releasing generator-local working files (`AcknowledgeEvidence`). |
| Transport Diagnostics | `ProbeHTTP3` | Executes bounded QUIC/HTTP/3 preflight probes from generator network namespaces. |

## Transport Security

Transport credentials and interceptors live in `pkg/distributed/generatorrpc`:

- Configured symmetrically via `APEXLOAD_GRPC_TLS_CERT`, `APEXLOAD_GRPC_TLS_KEY`, `APEXLOAD_GRPC_TLS_CA`, `APEXLOAD_GRPC_TLS_SERVER_NAME`, and `APEXLOAD_GRPC_AUTH_TOKEN` (certificates reload per handshake/dial for zero-downtime rotation).
- In `APEXLOAD_ENV=production` or `prod`, both controller and generator **fail closed at startup** (`TransportSecurity.MustRefuseInsecure`) unless the channel uses mTLS, verified TLS + `APEXLOAD_GRPC_AUTH_TOKEN` (>= 32 bytes when not using mTLS), or an authenticated WSS reverse tunnel (`APEXLOAD_GRPC_REVERSE_TUNNEL_SECURE=true` with `APEXLOAD_GENERATOR_CONNECTIVITY_MODE=reverse`).

## Regenerating Stubs

Requires `protoc`, `protoc-gen-go`, and `protoc-gen-go-grpc` on `PATH`. From the repository root:

```bash
make proto
```

Run `go test ./pkg/distributed/... ./pkg/api/...` after changing `generator.proto` to verify wire compatibility across controller and generator implementations.
