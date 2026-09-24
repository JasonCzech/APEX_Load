# Deployment Guide

Status: Current

Choose the deployment surface that matches your environment. Deployment-specific
READMEs own the details; this page is the router.

| Target | Start Here |
| --- | --- |
| Local Kubernetes | [k8s/local/README.md](k8s/local/README.md) |
| Docker Desktop kind mode | [k8s/local-kind/README.md](k8s/local-kind/README.md) |
| Production on-prem Kubernetes | [k8s/onprem/README.md](k8s/onprem/README.md) |
| AKS | [k8s/aks/README.md](k8s/aks/README.md) |
| EKS | [k8s/eks/README.md](k8s/eks/README.md) |
| Helm chart | [helm/apexload/README.md](helm/apexload/README.md) |
| Docker Compose monitoring | [docker/README.md](docker/README.md) |
| Azure Terraform | [azure/terraform/README.md](azure/terraform/README.md) |
| AWS Terraform | [aws/terraform/README.md](aws/terraform/README.md) |
| AKS Azure Monitor overlay | [k8s/aks-azure-monitor/README.md](k8s/aks-azure-monitor/README.md) |
| Kubernetes monitoring examples | [k8s/monitoring/README.md](k8s/monitoring/README.md) |
| Local AI provider secrets | [k8s/AI_PROVIDER_SETUP.md](k8s/AI_PROVIDER_SETUP.md) |

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
