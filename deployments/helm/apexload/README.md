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
  `APEXLOAD_REDIS_URL`, `APEXLOAD_GRPC_AUTH_TOKEN`, and—when
  `projectSecrets.provider=database`—`APEXLOAD_WRAPPING_MASTER_KEY`
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

OpenTelemetry trace export is disabled by default. To enable it, set
`tracing.enabled=true`, `tracing.endpoint`, and the desired protocol/sampling
values. For an authenticated collector, create a Kubernetes Secret whose data
contains the OpenTelemetry header string (for example under
`OTEL_EXPORTER_OTLP_HEADERS`), then set `tracing.headersSecretName` and, if the
key differs, `tracing.headersSecretKey`. The chart projects that key as
`OTEL_EXPORTER_OTLP_HEADERS` into both controller and generator containers;
the credential is never copied into values or the shared ConfigMap. See
[OpenTelemetry tracing](../../../docs/OPENTELEMETRY.md) for configuration and
data-handling details.

Set `controller.ha.enabled=true` before setting more than one controller
replica; the chart fails rendering otherwise. HA also requires PostgreSQL and
Redis. In HA, CI run mutations use the selectorless
`apexload-controller-active` Service. Its EndpointSlice records the durable
execution-lease owner/generation so an old leader cannot replace a successor;
OIDC exchange and read traffic continue through the ordinary all-replica
Service.

Each generator ordinal receives a retained PVC for its execution-fencing
high-water and generator-side run evidence awaiting controller ingestion.
Configure `generator.statePersistence.storageClassName` when the cluster has no
default StorageClass, and size it with `generator.statePersistence.size` (the
default is `10Gi`).

Project Secrets are writable by default through the encrypted database
provider. Set `projectSecrets.provider=database` and supply one stable wrapping
key through `dataPlatform.secretName`. When `dataPlatform.createSecret=true`,
deployment automation may provide it through
`projectSecrets.wrappingMasterKey`. This key is required even when
`bruno.enabled=false`.

To store managed values in Azure Key Vault instead, set
`projectSecrets.provider=azure_key_vault`. The chart sets
`APEXLOAD_AZURE_KEY_VAULT_WRITE_ENABLED=true` in the controller ConfigMap; the
controller workload identity must have `Key Vault Secrets Officer`. Generators
never receive the write-enable setting. See
[Project Secrets](../../../docs/PROJECT_SECRETS.md) for security and runtime
delivery details.

Bruno source import is available independently of runner deployment. Enable it
with `bruno.importEnabled=true` and configure a wrapping key. To enable
canonical Bruno execution, also set `bruno.enabled=true` and provide:

- `bruno.artifactPersistence.existingClaim`: a pre-created `ReadWriteMany` PVC
  mounted by the controller and every generator for encrypted,
  content-addressed source artifacts.
- `bruno.egressBrokerUrl`: the governed HTTP CONNECT broker endpoint. The
  chart fails closed without it unless `bruno.allowDirectEgress=true` is
  explicitly selected for an isolated local cluster.
- Either a versioned `projectSecrets.wrappingKeyVaultKeyId`, or
  `APEXLOAD_WRAPPING_MASTER_KEY` in `dataPlatform.secretName` (the chart
  can populate it from `projectSecrets.wrappingMasterKey` when
  `createSecret=true`). The older `bruno.keyVaultKeyId` and
  `bruno.artifactMasterKey` values remain compatible aliases.
- A published `apexload-bruno-runner` image compatible with Bruno 3.5.2.

The safe runner is injected as a loopback-only generator sidecar and never
receives a Kubernetes service-account token. Developer sandbox execution is
not enabled by this chart; it requires the separate isolated worker class
described by the Bruno execution policy. The `bruno.maxJumps`,
`maxRunRequestDepth`, `maxParallelRequests`, `maxOutputBytes`, and
`maxStreamTimeoutMs` values are operator ceilings: imported scripts may select
smaller values but cannot raise them.

Set `certManager.enabled=true` when the cluster uses cert-manager to issue the
controller/generator mTLS secrets. If the platform syncs those secrets from Key
Vault CSI, leave `certManager.enabled=false` and create the Kubernetes Secrets
out of band.
