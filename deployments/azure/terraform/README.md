# APEX Load — Azure infrastructure (Terraform)

Provisions everything APEX Load needs on Azure. The same Terraform stack serves both deployment topologies — AKS (controller + generators in cluster) and App Service (controller on App Service, generators in AKS).

| Resource | Purpose | Maps to |
|---|---|---|
| AKS cluster | Runs generators (always). Also runs the controller in the AKS topology. | `deployments/k8s/aks` |
| `loadgen` node pool | Dedicated autoscaling pool for generators (taint/label `apexload.io/...=generators`). | `generator-statefulset.yaml` nodeSelector |
| ACR | Container images. Kubelet identity gets `AcrPull` — no pull secrets. | `kustomization.yaml` images |
| PostgreSQL Flexible Server 16 | Enterprise persistence (`APEXLOAD_STORE=postgres`). Required for App Service topology (SQLite is unsafe on the CIFS `/home` share). | Secret `APEXLOAD_DATABASE_URL` / App Service app setting |
| Azure Cache for Redis | Live-run state + WebSocket fanout for multi-replica or HA controller. | Secret `APEXLOAD_REDIS_URL` / App Service app setting |
| Storage account + `apexload-archives` container | Run-evidence archives (`APEXLOAD_ARCHIVE_BACKEND=azure_blob`). Account keys disabled — workload identity / managed identity only. | ConfigMap `AZURE_STORAGE_ACCOUNT`, `APEXLOAD_ARCHIVE_CONTAINER` |
| Key Vault (RBAC) | `{{secret.*}}` script secrets and gRPC TLS PEM blobs (App Service topology). | ConfigMap `APEXLOAD_AZURE_KEY_VAULT_URL`; App Service Key Vault references |
| User-assigned identities + federated credentials | Split workload identities for `apexload-controller` and `apexload-generator`. Controller gets Blob + Key Vault; generators get Key Vault only by default. | `serviceaccounts.yaml` client-id annotations; App Service system-assigned identity needs controller-equivalent roles |
| Private networking | Production default private endpoints/private DNS for PostgreSQL, Redis, Blob, and Key Vault. | `network_mode=private` |

## Usage

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

## Wiring the outputs into the manifests

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
  --from-literal=APEXLOAD_REDIS_URL="$(terraform output -raw apexload_redis_url)"

# Images (deployments/k8s/aks/kustomization.yaml)
terraform output -raw acr_login_server       # → images newName prefix
```

Full migration steps (data, archives, cutover): `deployments/k8s/aks/README.md`.

For Kustomize deployments, prefer the render script so placeholders are checked
before anything is applied:

```bash
scripts/render-aks-manifests.sh --output /tmp/apexload-aks.yaml --image-tag "$(git rev-parse --short HEAD)"
kubectl apply -f /tmp/apexload-aks.yaml
```

## Wiring the outputs into App Service

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
  SCM_DO_BUILD_DURING_DEPLOYMENT=false
```

The App Service's system-assigned managed identity must have `Storage Blob Data Contributor` on the storage account and `Key Vault Secrets User` on the Key Vault — the same roles as the AKS workload identity. Generator connectivity requires VNet integration and static `APEXLOAD_GENERATORS` targets (see `deployments/azure/appservice/` and `docs/CICD_INTEGRATION.md`).

## Notes

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
  TriggerAuthentication (see `deployments/k8s/aks/README.md`).
- **Purge protection** is enabled on Key Vault; destroying the stack leaves the
  vault in soft-deleted state for 30 days.
