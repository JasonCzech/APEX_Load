# APEX Load on Azure AKS — deployment & on-prem migration guide

Production-grade manifests for running APEX Load on AKS with the full Azure
data platform: PostgreSQL Flexible Server, Azure Cache for Redis, Blob Storage
archives, Key Vault script secrets (workload identity — no static tokens), a
KEDA-autoscaled generator fleet, and DNS-based fleet discovery.

The matching infrastructure lives in [`deployments/azure/terraform`](../../azure/terraform/README.md).

```
                        ┌─ AKS (apexload namespace) ─────────────────────┐
  users ── Ingress ──▶  │  controller ×2 (Deployment)                    │
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
| Blob Storage | `APEXLOAD_ARCHIVE_BACKEND=azure_blob`, `APEXLOAD_ARCHIVE_CONTAINER`, `AZURE_STORAGE_ACCOUNT` | workload identity |
| Key Vault | `APEXLOAD_AZURE_KEY_VAULT_URL`, `APEXLOAD_AZURE_KEY_VAULT_AUTH=azure_identity` | workload identity |
| AKS headless DNS | `APEXLOAD_GENERATORS=dns://apexload-generator.apexload.svc.cluster.local:9090` | n/a |

The `dns://` scheme (also usable on-prem) makes the controller re-resolve the
headless service every `APEXLOAD_GENERATOR_DISCOVERY_INTERVAL` (default 15s),
so generators that KEDA adds or removes join/leave the fleet automatically —
no static pod list, no controller restart.

Audited Kubernetes restart controls are available as an explicit opt-in via
[restart-controls.example.yaml](restart-controls.example.yaml). They remain
disabled and tokenless by default; see
[Kubernetes restart controls](../../../docs/KUBERNETES_RESTART_CONTROLS.md).

## Fresh deployment

1. **Provision** — `terraform apply` in `deployments/azure/terraform`, then
   `$(terraform output -raw kubeconfig_command)`.
2. **Images** — `make docker-push-acr ACR_NAME=<registry>` (or let CI do it;
   see `deployments/ci/azure-pipelines-aks.yml` / `github-actions-aks.yml`).
3. **Configure** — edit the `CHANGE-ME` values in `configmap.yaml`,
   `kustomization.yaml` (ACR login server), and `controller-ingress.yaml`
   (hostname), using the Terraform outputs.
4. **Secrets** —
   ```bash
   kubectl create namespace apexload --dry-run=client -o yaml | kubectl apply -f -
   kubectl -n apexload create secret generic apexload-data-platform \
     --from-literal=APEXLOAD_DATABASE_URL="$(terraform output -raw apexload_database_url)" \
     --from-literal=APEXLOAD_REDIS_URL="$(terraform output -raw apexload_redis_url)"
   ```
5. **Apply** —
   ```bash
   export WORKLOAD_IDENTITY_CLIENT_ID=$(terraform output -raw workload_identity_client_id)
   kubectl kustomize deployments/k8s/aks | envsubst '$WORKLOAD_IDENTITY_CLIENT_ID' | kubectl apply -f -
   ```
6. **Prometheus for KEDA** — the ScaledObject queries
   `http://prometheus-operated.monitoring.svc:9090`. Install
   kube-prometheus-stack (same as on-prem) and the bundled ServiceMonitor is
   picked up. To use Azure Monitor managed Prometheus instead, point
   `serverAddress` at the workspace query endpoint and add a
   `TriggerAuthentication` with `podIdentity.provider: azure-workload`.
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

For each `{{secret.foo_bar}}` reference, create a Key Vault secret named
`foo-bar` (the resolver maps `_`/`.` → `-`):

```bash
az keyvault secret set --vault-name <vault> --name foo-bar --value '...'
```

Workload identity replaces any on-prem `APEXLOAD_SECRET_*` env plumbing — no
token rotation to manage. (`APEXLOAD_AZURE_KEY_VAULT_TOKEN` still works for
non-Azure-identity setups; `APEXLOAD_AZURE_KEY_VAULT_AUTH` selects
`token` / `azure_identity` / `auto`, and `APEXLOAD_AZURE_KEY_VAULT_SCOPE`
overrides the token audience for sovereign clouds.)

### 5. Cutover checklist

- [ ] AKS smoke test green (`deployments/k8s/smoke-test.sh`)
- [ ] KEDA scaling observed: queue a large test, watch `kubectl -n apexload get scaledobject,pods -w`
- [ ] Fleet discovery: `GET /api/v1/system/services` lists all generator pods after a scale event
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
  Also watch generator memory limits (8 Gi default here).
