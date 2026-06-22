# AKS Azure Monitor Managed Prometheus Overlay

This overlay switches the AKS KEDA scaler from an in-cluster
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
