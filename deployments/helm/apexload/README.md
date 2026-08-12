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

Audited Kubernetes restart controls are enabled by default. The chart grants
the controller ServiceAccount only namespace-scoped pod read/delete, workload
read/patch, and ResourceQuota read permissions. Generator pods do not receive
the default Kubernetes API token or restart RBAC; an audience-specific Azure
Workload Identity token may still be injected for cloud API access. Set
`restartControls.enabled=false` to omit that RBAC and keep the restart token
unmounted. The controller renders
`APEXLOAD_K8S_RESTART_ENABLED=false` directly in its environment so a value from
the shared ConfigMap or Secret cannot re-enable the integration. An HA
controller still receives the separate projected token required for
EndpointSlice leadership, without restart permissions.
Registered remote clusters still require their own RBAC; see
[Kubernetes restart controls](../../../docs/KUBERNETES_RESTART_CONTROLS.md).

| Value | Default | Purpose |
|---|---|---|
| `restartControls.enabled` | `true` | Enable the integration, controller API token, and scoped RBAC. |
| `restartControls.allowForce` | `true` | Allow explicitly confirmed forced restarts. |
| `restartControls.controllerDeployment` | `apexload-controller` | Controller Deployment restart target. |
| `restartControls.generatorStatefulSet` | `apexload-generator` | Generator StatefulSet restart target. |
| `restartControls.generatorSelector` | `app.kubernetes.io/name=apexload-generator` | Generator pod selector. The chart accepts one validated existence, equality, or canonical set requirement; see the linked runbook for the exact subset. |

On an upgrade with `--reuse-values`, a legacy release whose stored values do
not contain `restartControls` receives the chart's current default: restart
controls become enabled and the controller receives the scoped RBAC and token.
Use this upgrade command to remain opted out:

```bash
helm upgrade apexload deployments/helm/apexload \
  --namespace apexload \
  --reuse-values \
  --set restartControls.enabled=false
```

If `Role/apexload-restart-controls` or
`RoleBinding/apexload-restart-controls` was applied manually before Helm owned
it, an enabled upgrade cannot adopt the object automatically. Complete the
ownership preflight and adoption (or controlled deletion) in the linked
restart-controls runbook before upgrading.

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

Generator runs also use a mandatory renewable controller lease. The chart ships
the fail-closed 60-second TTL, 10-second renewal, and 5-second abandonment drain;
its values schema caps renewal at 10 seconds so every allowed TTL retains at
least three renewal opportunities. The first upgrade from a tokenless release
requires a no-active-tests maintenance window because mixed controller/generator
versions intentionally reject new work. Follow the rollout gate and connectivity
drill in [Generator run supervision](../../../docs/GENERATOR_RUN_SUPERVISION.md).
The controller pod template checksums the shared ConfigMap, so changing the TTL
or renewal value triggers a controller rollout instead of leaving stale process
environment behind.

Generic import assets also use persistent storage by default. For a single
controller, configure `importAssets.persistence` or its StorageClass. When
`importAssets.persistence.enabled=false`, the chart mounts a bounded `emptyDir`
at `importAssets.root`; imported assets are writable but intentionally disappear
when the pod is replaced. Single-controller releases use a `Recreate` rollout so
the RWO claim is never mounted by overlapping controller revisions. HA
controllers must supply an RWX claim through
`importAssets.persistence.existingClaim`; copy pre-upgrade assets from
`/var/lib/apexload/bruno-artifacts` (legacy Helm import-only installs) into the
new claim before rollout. The generated generator discovery target uses the
effective chart namespace (`namespace.name`, falling back to the Helm release
namespace) unless `dataPlatform.generators` is set explicitly.

Generator pods no longer assume a dedicated node pool. Clusters that used the
old defaults must set `generator.nodeSelector` and `generator.tolerations`
explicitly before upgrading if those scheduling constraints remain required.

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

Java Vuser execution is also opt-in. Set `javaVuser.enabled=true` to add the
credential-minimized runner to each generator pod. The runner binds only the
shared pod loopback interface and receives no generator credentials or
platform-identity mounts. The stock non-privileged sidecar has no writable
cgroup v2 delegation and therefore supports legacy `full_lifecycle` execution
only; combined persistent imports fail capability preflight with
`persistent_lifecycle_unavailable`. Do not use a privileged host-cgroup mount
as a workaround. A persistent lifecycle requires a separately governed Windows
runner or Linux runner with a narrowly delegated cgroup subtree.

The runner is released only in the `standard` coordinate: an empty
`javaVuser.runner.image.tag` derives
`<base image.tag>-standard`, including when controller/generator images use the
enterprise profile. Set an explicit runner tag for a signed custom image.
Supply precompiled classes/JARs through that image or set
`javaVuser.assets.existingClaim` to a read-only PVC; source compilation and
artifact upload are intentionally out of scope. See
[`docs/JAVA_VUSER_RUNTIME.md`](../../../docs/JAVA_VUSER_RUNTIME.md) for the JDK,
classpath, and trust contract.

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
