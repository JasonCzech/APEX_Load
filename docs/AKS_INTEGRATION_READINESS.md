# AKS Integration Readiness

APEX Load has enough AKS plumbing for a pilot deployment and an enterprise
migration rehearsal, but it is not yet a hands-off production migration target.
The repository contains an opt-in, single-execution-owner HA implementation;
that is a code/configuration capability, not evidence that controller failover
has passed a production AKS disruption test.

## Verified Baseline

The following checks were last run against the repository during the AKS
readiness pass:

| Check | Result |
| --- | --- |
| `kubectl kustomize deployments/k8s/aks` | Passed; the overlay renders. |
| `terraform fmt -check -diff` in `deployments/azure/terraform` | Passed. |
| `terraform init -backend=false` + `terraform validate` | Passed from a temporary copy with `azurerm v4.77.0` and `random v3.9.0`. |
| `go test ./deployments/k8s ./pkg/api ./pkg/engine ./pkg/distributed/generatorrpc ./pkg/kubeops ./pkg/config ./pkg/storage` | Passed. |

## Integration Matrix

| Area | Current state | AKS migration need |
| --- | --- | --- |
| Controller/runtime | Production env posture, readiness endpoint, dashboard embed, optional dedicated metrics port. | Keep readiness as the traffic gate and add dependency details for AKS operators. |
| Generator fleet | StatefulSet, headless service, DNS discovery, KEDA scale-out, mTLS/token gRPC. | Verify scale-out/scale-in under AKS and keep cert provisioning automated. |
| Metadata | SQLite default, PostgreSQL backend implemented and Terraform-provisioned. | Use PostgreSQL Flexible Server for shared environments; SQLite stays local-only. |
| Live state | Redis/Valkey backend for live snapshots, idempotency, rate limits, and WebSocket fanout. | Use Azure Cache for Redis for multi-controller or HA deployments. |
| Archives | Filesystem plus object backends, including Azure Blob through Go CDK. | Use Blob Storage with workload identity; keep local staging disposable. |
| Secrets | Env resolver plus Azure Key Vault resolver using static token or DefaultAzureCredential. | Use AKS workload identity for `{{secret.*}}`; avoid static vault tokens. |
| Observability | Prometheus metrics, ServiceMonitor, KEDA Prometheus scaler, remote-write examples. | Support either kube-prometheus-stack or Azure Monitor managed Prometheus. |
| Identity | Workload identity enabled in Terraform and service accounts. | Split controller and generator identities for least privilege. |
| Packaging | Kustomize overlays and the Helm chart are both present. | Treat Helm values as the enterprise install interface while preserving Kustomize; validate with `helm lint`, `helm template`, and schema checks. |
| HA control plane | Opt-in multi-replica API with one execution owner: PostgreSQL-backed lease generations, transactional write fencing, takeover reconciliation before admission, loss-time runtime teardown, leader-gated execution/scheduling/recovery work, and a generation-fenced active EndpointSlice are implemented. | Keep one controller by default. Enable multiple replicas only with shared PostgreSQL and Redis, the HA active Service/EndpointSlice, durable generator state, and environment-specific failover evidence. Do not describe this as production-validated active-active execution; execution remains single-owner. |

## Production Migration Gates

An AKS environment is ready for cutover only when all gates below pass:

1. `scripts/render-aks-manifests.sh` renders without placeholders.
2. Terraform validates with the committed lockfile and the selected network mode.
3. `kubectl kustomize`, `helm lint`, `helm template`, and schema validation pass.
4. `/api/v1/system/readiness` is `ready` or has only approved `degraded` checks.
5. The controller reports `metadata_store=postgres`, `cache_backend=redis`, Azure Blob archive details, Key Vault resolver details, and a secured `grpc_transport`.
6. A smoke test creates a script, queues a distributed run, streams metrics, writes an archive, and downloads the report.
7. KEDA scale-out and scale-in are observed while the controller tracks generators through `dns://` discovery.
8. OIDC or trusted-header auth is configured before exposing the ingress outside the platform network.
9. With HA disabled, one controller replica is active. With HA enabled, all
   replicas use shared PostgreSQL and Redis, only the admitted lease holder can
   mutate execution state, and the active EndpointSlice owner/generation matches
   the durable lease term.
10. A failover exercise terminates or isolates the admitted controller during a
    distributed run and proves: one execution start, no stale-owner mutation,
    a strictly newer successor lease generation, one terminal test outcome, and
    one terminal archive with no pending finalization intent.

## HA Failover Evidence Harness

The opt-in local harness creates two real `api.Server` instances with independent
connections to one durable store. It alternates leadership while runs are
durably `RUNNING`, verifies follower mutation rejection, checks monotonic lease
generations and a single backend owner/start, lets the successor reconcile the
terminal row, and repeats orphan-archive recovery to prove idempotent publication
of one terminal manifest/catalog record.

Dependency-free transactional simulation (SQLite is intentionally local-only):

```bash
APEXLOAD_RUN_HA_FAILOVER_SOAK=1 \
APEXLOAD_HA_SOAK_ITERATIONS=25 \
go test ./pkg/api -run '^TestHAControllerFailoverSoak$' -count=1 -timeout=10m
```

PostgreSQL metadata path (the database must be isolated, disposable, and empty;
the harness refuses a database that already contains test rows):

```bash
APEXLOAD_RUN_HA_FAILOVER_SOAK=1 \
APEXLOAD_HA_SOAK_ITERATIONS=100 \
APEXLOAD_HA_SOAK_DATABASE_URL='postgres://user:password@host:5432/apexload_ha_soak?sslmode=require' \
go test ./pkg/api -run '^TestHAControllerFailoverSoak$' -count=1 -timeout=30m
```

This is deterministic process-loss simulation. It uses local filesystem archive
staging and an in-memory cache so it can run without external infrastructure.
It does not validate Kubernetes pod termination, network partitions, Azure
Database for PostgreSQL, Azure Cache for Redis, workload identity, Blob Storage,
or a live generator fleet.

Before production cutover, run a separate AKS disruption exercise with at least
two controller pods, shared PostgreSQL/Redis, Azure Blob archives, retained
generator state, and a distributed run long enough to observe multiple lease
renewals. Delete the admitted pod during steady state and retain the lease row,
EndpointSlice transitions, controller/generator logs, test row, archive manifest
and finalization-outbox query as release evidence. A graceful relinquish alone
does not satisfy that production gate.

## Known Limitations

- The Kustomize overlay still contains operator-owned values by design; use the render script or Helm values to stamp environment-specific values.
- The first migration does not require a Kubernetes operator or CRDs. Add those only if Kubernetes users need first-class `LoadTest` or `GeneratorPool` resources.
- Public Azure endpoints remain available for evaluation mode. Production should use private endpoints/private DNS and restricted API server access.
- Azure Monitor managed Prometheus needs a cluster-specific workspace query endpoint and KEDA authentication; the checked-in overlay is a template.
- Browser VUsers execute Chromium in the generator pod. Use a dedicated node pool and consider RuntimeClass isolation for untrusted browser targets.
- No production AKS controller-failover evidence is committed. The local SQLite mode validates transactional handoff semantics, not a supported shared deployment; the optional PostgreSQL mode still does not exercise Redis, Blob, Kubernetes routing, or generators.

## Evidence Locations

- AKS Kustomize: `deployments/k8s/aks`
- Azure Terraform: `deployments/azure/terraform`
- Helm chart: `deployments/helm/apexload`
- HA Kustomize overlay: `deployments/k8s/aks-ha`
- Render script: `scripts/render-aks-manifests.sh`
- Runtime readiness: `GET /api/v1/system/readiness`
- Lease/fencing implementation: `pkg/api/execution_leadership.go`, `pkg/storage/sqlite_schedules.go`
- Opt-in failover soak: `pkg/api/ha_failover_soak_test.go`
