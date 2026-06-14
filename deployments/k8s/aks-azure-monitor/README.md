# AKS Azure Monitor Managed Prometheus Overlay

This overlay switches the AKS KEDA scaler from an in-cluster
kube-prometheus-stack endpoint to an Azure Monitor managed Prometheus query
endpoint.

Before applying, replace the `CHANGE-ME` server address in
`generator-keda-azure-monitor-patch.yaml` with the workspace query endpoint for
your Azure Monitor workspace. The controller ServiceMonitor from the base AKS
overlay is removed here because Azure Monitor uses `ama-metrics` scrape config
instead of Prometheus Operator CRDs.

```bash
kubectl kustomize deployments/k8s/aks-azure-monitor
```

The `TriggerAuthentication` uses the controller workload identity. Make sure
that identity can query the Azure Monitor workspace.
