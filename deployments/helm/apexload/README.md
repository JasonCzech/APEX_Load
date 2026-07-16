# APEX Load Helm Chart

This chart mirrors the AKS Kustomize overlay and is the preferred enterprise
install interface once values are supplied by Terraform outputs or release
automation.

```bash
helm lint deployments/helm/apexload -f production-values.yaml
helm template apexload deployments/helm/apexload --namespace apexload -f production-values.yaml
```

The chart includes `values.schema.json` and fails on placeholder defaults such
as `changeme.azurecr.io`, `apexload.example.com`, and
`https://change-me.vault.azure.net`.

Minimum production values:

- `image.registry`
- `image.tag`
- `dataPlatform.azureStorageAccount`
- `dataPlatform.keyVaultUrl`
- `dataPlatform.azureMessagingAllowedNamespaces` with exact namespace FQDNs when scripts use workload-identity Event Hubs or Service Bus auth
- `workloadIdentity.controllerClientId`
- `workloadIdentity.generatorClientId`
- `dataPlatform.secretName` pointing at a Secret with `APEXLOAD_DATABASE_URL`,
  `APEXLOAD_REDIS_URL`, and `APEXLOAD_GRPC_AUTH_TOKEN`
- `ingress.host`
- `controller.publicURL` when the externally reachable controller URL is not
  exactly `https://<ingress.host>`; this stable HTTPS URL defines the
  deployment-specific GitHub Actions OIDC audience
- `keda.azureMonitor.identityId` and `keda.azureMonitor.serverAddress` when
  `keda.azureMonitor.enabled=true`

Set `controller.ciOIDCAudience` only when an explicit fixed audience is required
by policy. Production and HA controllers fail CI discovery/exchange closed when
neither that value nor a valid `controller.publicURL`/`APEXLOAD_PUBLIC_URL` is
available; a process-unique controller pod ID is never used as the audience.

Each generator ordinal receives a retained PVC for its execution-fencing
high-water and generator-side run evidence awaiting controller ingestion.
Configure `generator.statePersistence.storageClassName` when the cluster has no
default StorageClass, and size it with `generator.statePersistence.size` (the
default is `10Gi`).

Set `certManager.enabled=true` when the cluster uses cert-manager to issue the
controller/generator mTLS secrets. If the platform syncs those secrets from Key
Vault CSI, leave `certManager.enabled=false` and create the Kubernetes Secrets
out of band.
