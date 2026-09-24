# Deployment Guide

Status: Current

Choose the deployment surface that matches your environment. Deployment-specific
READMEs own the details; this page is the router.

| Target | Start Here |
| --- | --- |
| Local Kubernetes | [k8s/local/README.md](k8s/local/README.md) |
| Docker Desktop kind mode | [k8s/local/README.md](k8s/local/README.md#docker-desktop-kind-mode) |
| Production on-prem Kubernetes | [k8s/onprem/README.md](k8s/onprem/README.md) |
| AKS (deployment, migration, readiness) | [k8s/aks/README.md](k8s/aks/README.md) |
| EKS (deployment, readiness) | [k8s/eks/README.md](k8s/eks/README.md) |
| Helm chart | [helm/apexload/README.md](helm/apexload/README.md) |
| Docker Compose monitoring | [docker/README.md](docker/README.md) |
| Azure Terraform | [azure/terraform/README.md](azure/terraform/README.md) |
| AWS Terraform | [aws/terraform/README.md](aws/terraform/README.md) |
| AKS Azure Monitor overlay | [k8s/aks/README.md](k8s/aks/README.md#azure-monitor-managed-prometheus-overlay) |
| Kubernetes monitoring examples | [k8s/monitoring/README.md](k8s/monitoring/README.md) |
| Local AI provider secrets | [AI provider setup for local Kubernetes](#ai-provider-setup-for-local-kubernetes) |

## Validation Baseline

Before promoting a Kubernetes or AKS deployment, validate manifests and generated
configuration with the relevant toolchain:

```bash
kubectl kustomize deployments/k8s/local
kubectl kustomize deployments/k8s/local-kind
kubectl kustomize deployments/k8s/onprem
kubectl kustomize deployments/k8s/onprem-ha
kubectl kustomize deployments/k8s/aks
kubectl kustomize deployments/k8s/aks-ha
kubectl kustomize deployments/k8s/eks
kubectl kustomize deployments/k8s/eks-ha
helm lint deployments/helm/apexload
helm template apexload deployments/helm/apexload
```

The `make k8s-*` targets (`k8s-local-*`, `k8s-kind-*`, `k8s-onprem-*`, `k8s-aks-*`,
`k8s-eks-*`, and `k8s-smoke-test`) wrap the apply, status, and delete steps for
these overlays; see the `Makefile` for the exact commands.

## AI provider setup for local Kubernetes

Use this when you want APEX Load scripts running in the controller pod to read provider credentials from Kubernetes instead of your local shell.

The local controller deployment supports these optional environment variables:

- `OPENAI_API_KEY`
- `OPENAI_MODEL`
- `XAI_API_KEY`
- `XAI_MODEL`

If you do not create the matching Kubernetes resources, the controller still starts normally, but scripts using those variables will fail at runtime.

### Grok / xAI

Create the secret and default model config:

```bash
kubectl -n apexload-local create secret generic apexload-xai-secrets \
  --from-literal=XAI_API_KEY=REPLACE_WITH_XAI_API_KEY \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl -n apexload-local create configmap apexload-xai-settings \
  --from-literal=XAI_MODEL=grok-4.3 \
  --dry-run=client -o yaml | kubectl apply -f -
```

### OpenAI

Create the secret and default model config:

```bash
kubectl -n apexload-local create secret generic apexload-openai-secrets \
  --from-literal=OPENAI_API_KEY=REPLACE_WITH_OPENAI_API_KEY \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl -n apexload-local create configmap apexload-openai-settings \
  --from-literal=OPENAI_MODEL=REPLACE_WITH_OPENAI_MODEL \
  --dry-run=client -o yaml | kubectl apply -f -
```

### Reload The Controller

After creating or updating either secret or configmap, restart the controller so the pod picks up the new values:

```bash
kubectl -n apexload-local rollout restart deployment/apexload-controller
kubectl -n apexload-local rollout status deployment/apexload-controller
```

### Verify The Environment

You can confirm a non-secret model variable is present with:

```bash
kubectl -n apexload-local exec deploy/apexload-controller -- printenv XAI_MODEL
kubectl -n apexload-local exec deploy/apexload-controller -- printenv OPENAI_MODEL
```

Do not print the API key values back out of the pod.
