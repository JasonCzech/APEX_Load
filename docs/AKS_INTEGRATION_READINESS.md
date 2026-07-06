# AKS Integration Readiness

APEX Load has enough AKS plumbing for a pilot deployment and an enterprise
migration rehearsal, but it is not yet a hands-off production migration target.
Current readiness is **3.6 / 5**.

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
| HA control plane | Redis and PostgreSQL are present, but singleton loop ownership remains an operational constraint. | Default to one active controller; do not claim active-active controllers until background loops are lease-guarded. |

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
9. Only one active scheduler/reconciler owner is enabled until PostgreSQL lease guarding is complete for singleton loops.

## Known Limitations

- The Kustomize overlay still contains operator-owned values by design; use the render script or Helm values to stamp environment-specific values.
- The first migration does not require a Kubernetes operator or CRDs. Add those only if Kubernetes users need first-class `LoadTest` or `GeneratorPool` resources.
- Public Azure endpoints remain available for evaluation mode. Production should use private endpoints/private DNS and restricted API server access.
- Azure Monitor managed Prometheus needs a cluster-specific workspace query endpoint and KEDA authentication; the checked-in overlay is a template.
- Browser VUsers execute Chromium in the generator pod. Use a dedicated node pool and consider RuntimeClass isolation for untrusted browser targets.

## Evidence Locations

- AKS Kustomize: `deployments/k8s/aks`
- Azure Terraform: `deployments/azure/terraform`
- Helm chart: `deployments/helm/apexload`
- Render script: `scripts/render-aks-manifests.sh`
- Runtime readiness: `GET /api/v1/system/readiness`
