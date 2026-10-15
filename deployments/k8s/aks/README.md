# APEX Load on Azure AKS — deployment & on-prem migration guide

## Overview

Production-grade manifests for running APEX Load on AKS with the full Azure
data platform: PostgreSQL Flexible Server, Azure Cache for Redis, Blob Storage
archives, Key Vault script secrets (workload identity — no static tokens), a
KEDA-autoscaled generator fleet, and DNS-based fleet discovery.

Controller-loss fencing is mandatory. Follow the maintenance-window rollout
gate and acceptance drill in [Generator run supervision](../../../docs/ENTERPRISE_FLEET_AND_CAPACITY.md#generator-run-supervision)
when upgrading from a tokenless release.

The matching infrastructure lives in [`deployments/azure/terraform`](../../azure/terraform) (see [Azure Terraform](#azure-terraform)).

```
                        ┌─ AKS (apexload namespace) ─────────────────────┐
  users ── Ingress ──▶  │  controller ×1 active (Deployment)             │
  (web app routing)     │     │ gRPC, dns:// discovery of headless svc   │
                        │  generator ×2–50 (StatefulSet + KEDA)          │
                        │     └ dedicated "loadgen" node pool            │
                        └──────┬───────────┬───────────┬───────────┬─────┘
                               ▼           ▼           ▼           ▼
                          PostgreSQL    Redis     Blob Storage  Key Vault
                          (system of  (live state, (run         ({{secret.*}},
                           record)     WS fanout)   archives)    workload identity)
```

## How the Azure pieces map to APEX Load config

| Azure service | Env vars (set in ConfigMap/Secret here) | Auth |
|---|---|---|
| PostgreSQL Flexible Server | `APEXLOAD_STORE=postgres`, `APEXLOAD_DATABASE_URL` | password in Secret |
| Azure Cache for Redis | `APEXLOAD_CACHE_BACKEND=redis`, `APEXLOAD_REDIS_URL` (rediss://, port 6380) | access key in Secret |
| Blob Storage | `APEXLOAD_ARCHIVE_BACKEND=azure_blob`, `APEXLOAD_ARCHIVE_CONTAINER`, `AZURE_STORAGE_ACCOUNT` | controller workload identity |
| Key Vault | `APEXLOAD_AZURE_KEY_VAULT_URL`, `APEXLOAD_AZURE_KEY_VAULT_AUTH=azure_identity`, controller-only `APEXLOAD_AZURE_KEY_VAULT_WRITE_ENABLED=true` | controller Secrets Officer; generator Secrets User |
| AKS headless DNS | `APEXLOAD_GENERATORS=dns://apexload-generator.apexload.svc.cluster.local:9090` | n/a |

The `dns://` scheme (also usable on-prem) makes the controller re-resolve the
headless service every `APEXLOAD_GENERATOR_DISCOVERY_INTERVAL` (default 15s),
so generators that KEDA adds or removes join/leave the fleet automatically —
no static pod list, no controller restart.

Audited Kubernetes restart controls are enabled by default through
[restart-controls.yaml](restart-controls.yaml). Only the controller mounts a
default Kubernetes API token. Generator pods receive no Kubernetes API token or
restart RBAC; the workload-identity webhook separately injects its cloud-audience
token for Azure APIs. See
[Kubernetes restart controls](../../../docs/CLUSTERS_AND_CLOUD.md).

## Fresh deployment

1. **Provision** — `terraform apply` in `deployments/azure/terraform`, then
   `$(terraform output -raw kubeconfig_command)`.
2. **Images** — `make docker-push-acr ACR_NAME=<registry>` (or let CI do it;
   see [deploy-aks.yml](../../../.github/workflows/deploy-aks.yml)).
3. **Configure** — prefer the render script below. If you edit manifests
   directly, replace the `CHANGE-ME` values in `configmap.yaml`,
   `kustomization.yaml` (ACR login server), and `controller-ingress.yaml`
   (hostname), using the Terraform outputs.
4. **Secrets** —
   ```bash
   kubectl create namespace apexload --dry-run=client -o yaml | kubectl apply -f -
   kubectl -n apexload create secret generic apexload-data-platform \
     --from-literal=APEXLOAD_DATABASE_URL="$(terraform output -raw apexload_database_url)" \
     --from-literal=APEXLOAD_REDIS_URL="$(terraform output -raw apexload_redis_url)" \
     --from-literal=APEXLOAD_GRPC_AUTH_TOKEN="$(openssl rand -hex 32)"
   ```
5. **Render + apply** —
   ```bash
   scripts/render-aks-manifests.sh --output /tmp/apexload-aks.yaml --image-tag "$(git rev-parse --short HEAD)"
   kubectl apply -f /tmp/apexload-aks.yaml
   ```
6. **Prometheus for KEDA** — the ScaledObject queries
   `http://prometheus-operated.monitoring.svc:9090`. Install
   kube-prometheus-stack (same as on-prem), then apply
   `kubectl apply -k deployments/k8s/aks-prometheus` if you want the
   Prometheus Operator `ServiceMonitor`. To use Azure Monitor managed
   Prometheus instead, set the workspace query endpoint and
   `keda_workload_identity_client_id` in `deployments/k8s/aks-azure-monitor`
   and apply that overlay.
7. **Smoke test** —
   ```bash
   kubectl -n apexload port-forward svc/apexload-controller 18080:8080 &
   ./deployments/k8s/smoke-test.sh   # runs a 1m distributed test end-to-end
   ```

## Migrating from on-prem Kubernetes

The on-prem manifests (`deployments/k8s/local`, `ha`) and these AKS manifests
run the same images with the same env contract, so migration is configuration
+ data movement, not a re-platform.

### 1. Stand up AKS alongside on-prem (no cutover yet)

Run the fresh-deployment steps above. Both environments can run concurrently;
nothing in APEX Load assumes a single installation.

### 2. Migrate the database

* **On-prem PostgreSQL → Azure PostgreSQL** (typical enterprise setup):
  ```bash
  pg_dump --no-owner --no-privileges -Fc "$ONPREM_DSN" -f apexload.dump
  pg_restore --no-owner --no-privileges -d "$(terraform output -raw apexload_database_url)" apexload.dump
  ```
  Scale the controller to 0 replicas first (`kubectl scale deploy/apexload-controller --replicas=0`
  on-prem) so no writes land mid-dump. The Azure connection requires
  `sslmode=require` (already in the DSN output).
* **On-prem SQLite → Azure PostgreSQL**: there is no automated converter.
  Recommended: start the AKS controller against the empty PostgreSQL database
  (schema migrates automatically on first boot) and move scripts across via
  the API (`GET /api/v1/scripts/{id}/download` → `POST /api/v1/scripts`) or
  the GitHub integration (push from on-prem, import on AKS). Historical run
  rows can stay on the on-prem instance until its retention window lapses.

### 3. Migrate run-evidence archives

On-prem archives live under `APEXLOAD_ARCHIVE_DIR` (filesystem backend).
Upload them into the Blob container under the configured prefix:

```bash
azcopy login --login-type=azcli
azcopy copy "$ONPREM_ARCHIVE_DIR/*" \
  "https://$(terraform output -raw storage_account_name).blob.core.windows.net/apexload-archives/production/run-evidence/" \
  --recursive
```

The controller materializes archives from Blob on demand. After copying,
open one historical run's report on AKS to confirm evidence resolves before
deleting the on-prem copies.

### 4. Migrate script secrets

Use Settings → Projects & Compliance → Project Secrets to create or rotate
logical values. The controller writes them through workload identity; values
are never returned to the browser. Existing values can still be provisioned
out of band with `az keyvault secret set`.

Workload identity replaces any on-prem `APEXLOAD_SECRET_*` env plumbing — no
token rotation to manage. (`APEXLOAD_AZURE_KEY_VAULT_TOKEN` still works for
non-Azure-identity setups; `APEXLOAD_AZURE_KEY_VAULT_AUTH` selects
`token` / `azure_identity` / `auto`, and `APEXLOAD_AZURE_KEY_VAULT_SCOPE`
overrides the token audience for sovereign clouds.)

### 5. Cutover checklist

- [ ] AKS smoke test green (`deployments/k8s/smoke-test.sh`)
- [ ] KEDA scaling observed: queue a large test, watch `kubectl -n apexload get scaledobject,pods -w`
- [ ] Fleet discovery: `GET /api/v1/system/services` lists all generator pods after a scale event
- [ ] Project Secrets reports a writable `azure_key_vault` provider and the
      controller identity has `Key Vault Secrets Officer`
- [ ] Auth: OIDC redirect URIs updated to the new ingress hostname (`APEXLOAD_ENV=production` refuses bypass auth)
- [ ] GitHub integration (if used): GHES webhook + OAuth callback URLs point at the new hostname
- [ ] CI pipelines (`apexctl run --api ...`) repointed at the new URL
- [ ] Dashboards/alerts scraping the new Prometheus
- [ ] DNS flipped; on-prem controller scaled to 0 (keep it for rollback until retention lapses)

### Rollback

Nothing destructive happens to the on-prem environment during migration. To
roll back, flip DNS back and scale the on-prem controller up; re-run the
database dump in the opposite direction if writes happened on AKS.

## Operational notes

- **Controller pods are disposable**: durable state is PostgreSQL + Blob +
  Redis; `/var/lib/apexload` is an emptyDir used for staging/exports only.
- **Controller replicas**: the AKS base keeps one controller. Use the HA
  overlay only with shared PostgreSQL and Redis. The overlay creates a
  selectorless `apexload-controller-active` Service whose EndpointSlice is
  fenced by the durable execution-lease owner and generation. CI run mutations,
  execution-readiness and every other leader-only route (test, script, fleet,
  project, archive-retention and restart controls) go through that Service;
  `deployments/k8s/leader_routes_test.go` derives the leader-only set from the
  controller source and checks each ingress covers it. OIDC exchange,
  dashboard assets, and other read traffic remain on the all-replica
  controller Service.
  The leader does not publish itself until its HTTP listener has bound.
- **mTLS certificates**: use cert-manager with
  `certificates.cert-manager.yaml`, or sync `apexload-controller-grpc-tls` and
  `apexload-generator-grpc-tls` from Key Vault CSI. The base overlay assumes
  those Kubernetes Secrets exist before pods start.
- **Generators run on a tainted node pool** (`apexload.io/node-pool=generators`)
  so load spikes never starve the controller; the cluster autoscaler grows the
  pool as KEDA adds pods. Remove the nodeSelector/toleration from
  `generator-statefulset.yaml` if you don't create the dedicated pool.
- **Egress is intentionally unrestricted** — generators must reach the systems
  under test. Lock down ingress instead (`network-policy.yaml`, API server
  authorized IP ranges).
- **Browser VUsers**: the generator image bundles Alpine Chromium plus the
  Playwright driver and drives the system browser via
  `APEXLOAD_BROWSER_EXECUTABLE` — browser runs work under the hardened
  securityContext (verified with read-only root, uid 65532, drop-ALL).
  Security trade-off: Chromium runs with `--no-sandbox` because the
  restricted profile cannot start its sandbox, so a malicious page rendered
  by a browser VUser executes as the generator process. Point browser tests
  only at trusted targets, or isolate browser generators in a dedicated pool
  with a gVisor/Kata RuntimeClass; where unprivileged user namespaces are
  available, `APEXLOAD_BROWSER_SANDBOX=true` re-enables the Chromium sandbox.
  Also watch generator memory limits (8 Gi default here). For observer pools,
  deploy the `apexload-browser-generator` image instead: it runs Playwright's
  own Chromium, the build the driver was tested with (see
  [Browser generator for observer pools](../../README.md#browser-generator-for-observer-pools)).

## Integration readiness

APEX Load has enough AKS plumbing for a pilot deployment and an enterprise
migration rehearsal, but it is not yet a hands-off production migration target.
The repository contains an opt-in, single-execution-owner HA implementation;
that is a code/configuration capability, not evidence that controller failover
has passed a production AKS disruption test.

### Verified Baseline

The following checks were last run against the repository during the AKS
readiness pass (run date not recorded; this table was last edited in commit `098986604`, 2026-07-20, and the checks were not re-run as part of the 2026-09-24 documentation review):

| Check | Result |
| --- | --- |
| `kubectl kustomize deployments/k8s/aks` | Passed; the overlay renders. |
| `terraform fmt -check -diff` in `deployments/azure/terraform` | Passed. |
| `terraform init -backend=false` + `terraform validate` | Passed from a temporary copy with `azurerm v4.77.0` and `random v3.9.0`. |
| `go test ./deployments/k8s ./pkg/api ./pkg/engine ./pkg/distributed/generatorrpc ./pkg/kubeops ./pkg/config ./pkg/storage` | Passed. |

### Integration Matrix

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

### Production Migration Gates

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

### HA Failover Evidence Harness

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

### Known Limitations

- The Kustomize overlay still contains operator-owned values by design; use the render script or Helm values to stamp environment-specific values.
- The first migration does not require a Kubernetes operator or CRDs. Add those only if Kubernetes users need first-class `LoadTest` or `GeneratorPool` resources.
- Public Azure endpoints remain available for evaluation mode. Production should use private endpoints/private DNS and restricted API server access.
- Browser VUser isolation: see the Browser VUsers note under Operational notes.
- No production AKS controller-failover evidence is committed. The local SQLite mode validates transactional handoff semantics, not a supported shared deployment; the optional PostgreSQL mode still does not exercise Redis, Blob, Kubernetes routing, or generators.

### Evidence Locations

- AKS Kustomize: `deployments/k8s/aks`
- Azure Terraform: `deployments/azure/terraform`
- Helm chart: `deployments/helm/apexload`
- HA Kustomize overlay: `deployments/k8s/aks-ha`
- Render script: `scripts/render-aks-manifests.sh`
- Runtime readiness: `GET /api/v1/system/readiness`
- Lease/fencing implementation: `pkg/api/execution_leadership.go`, `pkg/storage/sqlite_schedules.go`
- Opt-in failover soak: `pkg/api/ha_failover_soak_test.go`

## Azure Monitor managed Prometheus overlay

The `deployments/k8s/aks-azure-monitor` overlay switches the AKS KEDA scaler from an in-cluster
kube-prometheus-stack endpoint to an Azure Monitor managed Prometheus query
endpoint.

Before applying:

1. Replace the `CHANGE-ME` server address in
   `generator-keda-azure-monitor-patch.yaml` with the workspace query endpoint.
2. Replace `${KEDA_WORKLOAD_IDENTITY_CLIENT_ID}` in
   `triggerauthentication.yaml` with
   `terraform output -raw keda_workload_identity_client_id`.

```bash
kubectl kustomize deployments/k8s/aks-azure-monitor
```

The `TriggerAuthentication` uses the dedicated KEDA scaler workload identity.
Terraform grants that identity `Monitoring Reader` at the resource-group scope;
scope it more tightly if your managed Prometheus workspace lives elsewhere.

The checked-in overlay is a template: it needs a cluster-specific workspace query endpoint and KEDA authentication before it works.

## Azure Terraform

Provisions the Azure data platform and the selected APEX Load compute roles. Set
`controller_compute` and `generator_compute` independently to `aks`,
`container_apps`, or `external`; AKS is omitted when neither role selects it.

| Resource | Purpose | Maps to |
|---|---|---|
| AKS cluster | Optional controller and/or generator Kubernetes runtime. | `deployments/k8s/aks` |
| Azure Container Apps + Jobs | Optional PaaS controller, warm generators, and burst generators. | `containerapps.tf` |
| `loadgen` node pool | Dedicated autoscaling pool for generators (taint/label `apexload.io/...=generators`). | `generator-statefulset.yaml` nodeSelector |
| ACR | Container images. Kubelet identity gets `AcrPull` — no pull secrets. | `kustomization.yaml` images |
| PostgreSQL Flexible Server 16 | Enterprise persistence (`APEXLOAD_STORE=postgres`). Required for App Service topology (SQLite is unsafe on the CIFS `/home` share). | Secret `APEXLOAD_DATABASE_URL` / App Service app setting |
| Azure Managed Redis | Default live-run state + WebSocket fanout cache. Legacy Azure Cache remains an explicit migration bridge. | Secret `APEXLOAD_REDIS_URL` / controller environment |
| Storage account + `apexload-archives` container | Run-evidence archives (`APEXLOAD_ARCHIVE_BACKEND=azure_blob`). Account keys disabled — workload identity / managed identity only. | ConfigMap `AZURE_STORAGE_ACCOUNT`, `APEXLOAD_ARCHIVE_CONTAINER` |
| Key Vault (RBAC) | `{{secret.*}}` script secrets and gRPC TLS PEM blobs (App Service topology). | ConfigMap `APEXLOAD_AZURE_KEY_VAULT_URL`; App Service Key Vault references |
| User-assigned identities + federated credentials | Split workload identities for `apexload-controller` and `apexload-generator`. Controller gets Blob + Key Vault secret-management access; generators retain read-only Key Vault access. | `serviceaccounts.yaml` client-id annotations; App Service system-assigned identity needs controller-equivalent roles |
| Private networking | Production default private endpoints/private DNS for PostgreSQL, Redis, Blob, and Key Vault. | `network_mode=private` |

### Usage

```bash
cd deployments/azure/terraform
cp terraform.tfvars.example terraform.tfvars   # edit
terraform init \
  -backend-config=resource_group_name=<tfstate-rg> \
  -backend-config=storage_account_name=<tfstateaccount> \
  -backend-config=container_name=tfstate \
  -backend-config=key=apexload.terraform.tfstate
terraform plan
terraform apply
```

New deployments use Azure Managed Redis. Existing state that contains
`azurerm_redis_cache.main` must first set
`redis_service = "azure_cache_legacy"`. Then migrate application cache state
(or schedule a cache-safe outage), switch to `managed_redis`, review the
replacement plan, and apply. Redis contains live coordination state rather than
durable run evidence, so do not switch while tests or provisioning operations
are active.

### Wiring the outputs into the manifests

```bash
# Cluster credentials
$(terraform output -raw kubeconfig_command)

# Service accounts: workload identity client IDs
export CONTROLLER_WORKLOAD_IDENTITY_CLIENT_ID=$(terraform output -raw controller_workload_identity_client_id)
export GENERATOR_WORKLOAD_IDENTITY_CLIENT_ID=$(terraform output -raw generator_workload_identity_client_id)
terraform output -raw keda_workload_identity_client_id # → KEDA Azure Monitor TriggerAuthentication identityId

# ConfigMap values (deployments/k8s/aks/configmap.yaml)
terraform output -raw storage_account_name   # → AZURE_STORAGE_ACCOUNT
terraform output -raw key_vault_url          # → APEXLOAD_AZURE_KEY_VAULT_URL

# Secret (never committed)
kubectl -n apexload create secret generic apexload-data-platform \
  --from-literal=APEXLOAD_DATABASE_URL="$(terraform output -raw apexload_database_url)" \
  --from-literal=APEXLOAD_REDIS_URL="$(terraform output -raw apexload_redis_url)" \
  --from-literal=APEXLOAD_GRPC_AUTH_TOKEN="$(openssl rand -hex 32)"

# Images (deployments/k8s/aks/kustomization.yaml)
terraform output -raw acr_login_server       # → images newName prefix
```

Full migration steps (data, archives, cutover): [Migrating from on-prem Kubernetes](#migrating-from-on-prem-kubernetes).

For Kustomize deployments, prefer the render script so placeholders are checked
before anything is applied:

```bash
scripts/render-aks-manifests.sh --output /tmp/apexload-aks.yaml --image-tag "$(git rev-parse --short HEAD)"
kubectl apply -f /tmp/apexload-aks.yaml
```

### Generator Capacity Guard

`terraform plan` validates that the autoscaling `loadgen` node pool can schedule
the configured KEDA generator ceiling. The check is intentionally parametric:
`generator_keda_max_replicas`, `generator_node_vm_size`,
`generator_node_max_count`, `generator_pod_cpu_millicores`,
`generator_pod_memory_mib`, and `generator_node_reserved_fraction` all feed the
precondition on the generator node pool. If you change the AKS generator manifest
requests or the KEDA `maxReplicaCount`, update the matching Terraform variables
in the same change.

### Wiring the outputs into App Service

If deploying the controller to Azure App Service instead of AKS, wire the same Terraform outputs as app settings:

```bash
# Get the outputs you need
DB_URL=$(terraform output -raw apexload_database_url)
REDIS_URL=$(terraform output -raw apexload_redis_url)
STORAGE_ACCOUNT=$(terraform output -raw storage_account_name)
KV_URL=$(terraform output -raw key_vault_url)

# Set app settings (use Key Vault references for secrets in production)
az webapp config appsettings set -g <resource-group> -n <app-name> --settings \
  APEXLOAD_STORE=postgres \
  APEXLOAD_DATABASE_URL="$DB_URL" \
  APEXLOAD_ARCHIVE_BACKEND=azure_blob \
  APEXLOAD_ARCHIVE_CONTAINER=apexload-archives \
  AZURE_STORAGE_ACCOUNT="$STORAGE_ACCOUNT" \
  APEXLOAD_AZURE_KEY_VAULT_URL="$KV_URL" \
  APEXLOAD_AZURE_KEY_VAULT_AUTH=azure_identity \
  APEXLOAD_AZURE_KEY_VAULT_WRITE_ENABLED=true \
  SCM_DO_BUILD_DURING_DEPLOYMENT=false
```

The App Service's system-assigned managed identity must have
`Storage Blob Data Contributor` on the storage account and
`Key Vault Secrets Officer` on the Key Vault—the same roles as the AKS
controller workload identity. Generator connectivity requires VNet integration
and static `APEXLOAD_GENERATORS` targets (see
`deployments/azure/appservice/` and `docs/CICD_INTEGRATION.md`).

### Notes

- **State**: the module declares an `azurerm` backend. Use `terraform init
  -backend-config=...`; local state is only for throwaway experiments via
  `terraform init -backend=false`.
- **Networking**: `network_mode=private` is the production default and creates a
  VNet, AKS subnets, private endpoints, and private DNS for PostgreSQL, Redis,
  Blob Storage, and Key Vault. `network_mode=public` keeps the earlier
  evaluation posture with public data-plane endpoints plus PostgreSQL firewall
  rules; set `public_network_allowed_cidrs` to restrict Storage and Key Vault
  public endpoints in that mode.
- **Observability**: a Log Analytics workspace and diagnostic settings are
  created for AKS, ACR, PostgreSQL, Redis, Storage, and Key Vault.
- **Prometheus**: the KEDA ScaledObject expects a Prometheus query endpoint.
  Installing kube-prometheus-stack in-cluster mirrors the on-prem setup; Azure
  Monitor managed Prometheus also works with a workload-identity
  TriggerAuthentication (see [Azure Monitor managed Prometheus overlay](#azure-monitor-managed-prometheus-overlay)).
- **Purge protection** is enabled on Key Vault; destroying the stack leaves the
  vault in soft-deleted state for 30 days.
