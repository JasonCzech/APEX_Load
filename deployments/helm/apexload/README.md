# APEX Load Helm Chart

This chart mirrors the AKS Kustomize overlay and is the preferred enterprise
install interface once values are supplied by Terraform outputs or release
automation.

```bash
helm lint deployments/helm/apexload
helm template apexload deployments/helm/apexload --namespace apexload
```

Minimum production values:

- `image.registry`
- `image.tag`
- `dataPlatform.azureStorageAccount`
- `dataPlatform.keyVaultUrl`
- `workloadIdentity.controllerClientId`
- `workloadIdentity.generatorClientId`
- `dataPlatform.secretName` pointing at a Secret with `APEXLOAD_DATABASE_URL`,
  `APEXLOAD_REDIS_URL`, and `APEXLOAD_GRPC_AUTH_TOKEN`
- `ingress.host`

Set `certManager.enabled=true` when the cluster uses cert-manager to issue the
controller/generator mTLS secrets. If the platform syncs those secrets from Key
Vault CSI, leave `certManager.enabled=false` and create the Kubernetes Secrets
out of band.
