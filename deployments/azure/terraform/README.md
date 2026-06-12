# APEX Load — Azure infrastructure (Terraform)

Provisions everything APEX Load needs on Azure:

| Resource | Purpose | Maps to |
|---|---|---|
| AKS cluster | Runs controller + generators. OIDC issuer, workload identity, managed KEDA, Web Application Routing, Cilium network policy. | `deployments/k8s/aks` |
| `loadgen` node pool | Dedicated autoscaling pool for generators (taint/label `apexload.io/...=generators`). | `generator-statefulset.yaml` nodeSelector |
| ACR | Container images. Kubelet identity gets `AcrPull` — no pull secrets. | `kustomization.yaml` images |
| PostgreSQL Flexible Server 16 | Enterprise persistence (`APEXLOAD_STORE=postgres`). | Secret `APEXLOAD_DATABASE_URL` |
| Azure Cache for Redis | Live-run state + WebSocket fanout for the 2-replica controller. | Secret `APEXLOAD_REDIS_URL` |
| Storage account + `apexload-archives` container | Run-evidence archives (`APEXLOAD_ARCHIVE_BACKEND=azure_blob`). Account keys disabled — workload identity only. | ConfigMap `AZURE_STORAGE_ACCOUNT`, `APEXLOAD_ARCHIVE_CONTAINER` |
| Key Vault (RBAC) | `{{secret.*}}` script secrets via `APEXLOAD_AZURE_KEY_VAULT_AUTH=azure_identity`. | ConfigMap `APEXLOAD_AZURE_KEY_VAULT_URL` |
| User-assigned identity + federated credentials | Workload identity for the `apexload-controller` / `apexload-generator` service accounts. Roles: `Storage Blob Data Contributor`, `Key Vault Secrets User`. | `serviceaccounts.yaml` client-id annotation |

## Usage

```bash
cd deployments/azure/terraform
cp terraform.tfvars.example terraform.tfvars   # edit
terraform init
terraform plan
terraform apply
```

## Wiring the outputs into the manifests

```bash
# Cluster credentials
$(terraform output -raw kubeconfig_command)

# Service accounts: workload identity client ID
export WORKLOAD_IDENTITY_CLIENT_ID=$(terraform output -raw workload_identity_client_id)

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

## Notes

- **State**: configure the `azurerm` backend in `versions.tf` before team use.
- **Networking**: PostgreSQL uses the public endpoint restricted to Azure-internal
  traffic plus `postgres_allowed_cidrs`. For private-only access, move the server
  onto a delegated subnet with private DNS (requires adding a VNet definition).
- **Prometheus**: the KEDA ScaledObject expects a Prometheus query endpoint.
  Installing kube-prometheus-stack in-cluster mirrors the on-prem setup; Azure
  Monitor managed Prometheus also works with a workload-identity
  TriggerAuthentication (see `deployments/k8s/aks/README.md`).
- **Purge protection** is enabled on Key Vault; destroying the stack leaves the
  vault in soft-deleted state for 30 days.
