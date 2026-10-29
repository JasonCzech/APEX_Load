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
the controller ServiceAccount (`Role/apexload-restart-controls`) only
namespace-scoped pod read/delete, workload read/patch, ResourceQuota read, and
`keda.sh/scaledobjects` plus `autoscaling/horizontalpodautoscalers` `list`
permissions. Generator pods do not receive
the default Kubernetes API token or restart RBAC; an audience-specific Azure
Workload Identity token may still be injected for cloud API access. Set
`restartControls.enabled=false` to omit that RBAC and keep the restart token
unmounted. The controller renders
`APEXLOAD_K8S_RESTART_ENABLED=false` directly in its environment so a value from
the shared ConfigMap or Secret cannot re-enable the integration. An HA
controller still receives the separate projected token required for
EndpointSlice leadership, without restart permissions.
Registered remote clusters still require their own RBAC; see
[Kubernetes restart controls](../../../docs/CLUSTERS_AND_CLOUD.md#kubernetes-restart-controls).

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
Missing HTTP/3, archive-staging, and egress-profile settings also receive their
current defaults, with those optional features disabled. Partial settings
inherit only missing keys; explicit `false` values and empty target lists are
preserved. The chart tests upgrades using the September 15, 2026 values snapshot.
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
[OpenTelemetry tracing](../../../docs/OBSERVABILITY_INTEGRATIONS.md#opentelemetry-tracing) for configuration and
data-handling details.

HTTP/3 is off by default. `http3.enabled=true` sets `APEXLOAD_HTTP3` on the
controller and every generator, so runs may use `auto+h3` and exact `3`.
`http3.preflight` (`off`, `report` by default, or `block`) sets the
controller's scenario preflight, a QUIC handshake check of each HTTP/3 origin
before a run starts; `block` refuses to start a run when an exact-3 origin
fails. `http3.quic.*` sets the operator QUIC limits (`APEXLOAD_QUIC_*`); `0`
or `false` keeps the built-in default. Chart pods have their own network
namespace, so both pods set `APEXLOAD_UDP_COUNTER_SCOPE=pod` and run reports
attribute UDP drops to them. See
[HTTP/3 and QUIC](../../../docs/guides/HTTP3_AND_QUIC.md).

The chart's network policies restrict ingress only, so egress is unrestricted
unless the cluster adds its own policies. `networkPolicy.egress.enabled=true`
(which requires `networkPolicy.enabled`) adds egress profiles for the
controller and the generators: DNS to `networkPolicy.egress.dns`, the
load-test targets in `networkPolicy.egress.targets` (any address on TCP 80 and
443 and UDP 443 by default, UDP 443 being HTTP/3), the controller-to-generator
gRPC port and controller peers, plus `controllerExtraRules` and
`generatorExtraRules`. List the controller's database, Redis, Kubernetes API
server (when it is not on 443) and telemetry endpoints in
`controllerExtraRules` before enabling the profiles, and add a DNS rule to both
extra-rule lists when the cluster resolves through NodeLocal DNSCache rather
than the `kube-dns` pods. `exceptCidrs` entries apply to the target CIDRs of
the same address family and must lie inside them.

Set `controller.ha.enabled=true` before setting more than one controller
replica; the chart fails rendering otherwise. HA also requires PostgreSQL and
Redis. In HA, CI run mutations and every other leader-only route (test,
script, fleet, project, archive-retention and restart controls, and the
reverse-generator gateway) use the selectorless `apexload-controller-active`
Service; `deployments/k8s/leader_routes_test.go` derives the leader-only set
from the controller source and fails if the Ingress leaves one on the
all-replica Service. Its EndpointSlice records the durable execution-lease
owner/generation so an old leader cannot replace a successor; OIDC exchange
and other read traffic continue through the ordinary all-replica Service.

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
drill in [Generator run supervision](../../../docs/ENTERPRISE_FLEET_AND_CAPACITY.md#generator-run-supervision).
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

Controller evidence staging can use a separate persistent claim through
`archiveStaging.persistence`. It is disabled by default. HA requires a declared
ReadWriteMany claim with qualified POSIX locking, rename and fsync semantics.
Chart-created staging claims are retained on disable/uninstall. See
[shared controller staging](../../../docs/ARCHIVE_SOURCE_RETENTION.md#shared-controller-staging)
for values, Kustomize setup, migration, capacity limits and qualification boundaries.

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
[Project Secrets](../../../docs/ADMIN_USER_GUIDE.md#application-and-project-secrets) for security and runtime
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

`javaVuser.runner.sharedJVM` passes the shared-JVM policy (many VUsers of one
group per JVM) to the runner. It is off by default and takes effect only on a
runner with that delegated containment; the stock sidecar keeps one JVM per
VUser and the run manifest says why. With `memoryBudgetMB: 0` the runner caps
its JVM budget at the container's memory limit, so size
`javaVuser.runner.resources.limits.memory` for the shared JVMs it should hold.
`javaVuser.runner.sharedJVM.threadMode` also admits LoadRunner thread mode
(`jvm_sharing: threads`), where libraries and their native code load once per
JVM as in LoadRunner.

The runner is released only in the `standard` coordinate: an empty
`javaVuser.runner.image.tag` derives
`<base image.tag>-standard`, including when controller/generator images use the
enterprise profile. Set an explicit runner tag for a signed custom image.
Supply precompiled classes/JARs through that image or set
`javaVuser.assets.existingClaim` to a read-only PVC; source compilation and
artifact upload are intentionally out of scope. See
[Java Vuser execution](../../../docs/PROTOCOL_SUPPORT.md#java-vuser-execution) for the JDK,
classpath, and trust contract.

Bruno native HTTP import remains available with `bruno.importEnabled=true`, a protected artifact PVC through `bruno.artifactPersistence.existingClaim`, and the existing wrapping key. Only controllers mount source artifacts. The physical PVC and artifact directory names remain unchanged, including `/var/lib/apexload-bruno`.

Bruno Runner is retired. The chart rejects `bruno.enabled=true` and no longer deploys a generator sidecar or its egress configuration. When upgrading a runner-enabled release with `--reuse-values`, pass `--set bruno.enabled=false`; retain the existing artifact PVC and wrapping key. The older `bruno.keyVaultKeyId` and `bruno.artifactMasterKey` aliases remain accepted. Canonical scripts remain stored but cannot execute. See [retired integrations](../../../docs/RETIRED_INTEGRATIONS.md).

Set `certManager.enabled=true` when the cluster uses cert-manager to issue the
controller/generator mTLS secrets. If the platform syncs those secrets from Key
Vault CSI, leave `certManager.enabled=false` and create the Kubernetes Secrets
out of band.

## Top-Level Values Summary

| Key | Purpose |
|---|---|
| `namespace.*`, `image.*` | Target namespace creation/override and registry, repository names, tag, and pull policy for controller and generator images. |
| `controller.*`, `controller.ha.*`, `restartControls.*` | Controller replicas, resource requests/limits, public URL, HA active Service settings, and Kubernetes restart/scale RBAC controls. |
| `generator.*`, `generator.runSupervision.*`, `generator.statePersistence.*` | Generator StatefulSet replicas, resources, node placement, run supervision leases, and fencing/evidence PVC settings. |
| `dataPlatform.*`, `projectSecrets.*`, `workloadIdentity.*` | PostgreSQL/Redis/gRPC secret bindings, Azure Blob/Key Vault settings, project secret wrapping provider, and Azure Workload Identity bindings. |
| `importAssets.*`, `archiveStaging.*`, `bruno.*` | Persistent storage for imported script assets, controller archive staging, and protected Bruno HTTP import source artifacts. |
| `http3.*`, `tracing.*`, `javaVuser.*` | Optional HTTP/3 + QUIC tuning, OpenTelemetry OTLP trace export, and loopback Java Vuser sidecar configuration. |
| `ingress.*`, `networkPolicy.*`, `networkPolicy.egress.*`, `pdb.*` | Public TLS Ingress, ingress NetworkPolicies, optional egress profiles (`dns`, `targets`, `controllerExtraRules`, `generatorExtraRules`), and PodDisruptionBudget toggles. |
| `serviceMonitor.*`, `keda.*`, `certManager.*` | ServiceMonitor scrape settings, KEDA autoscaling (in-cluster Prometheus or Azure Monitor), and cert-manager mTLS certificate issuance. |

### Operational and Security Values Reference

| Value | Default | Operational / security behavior |
|---|---|---|
| `dataPlatform.keyVaultAllowedSecrets` | `""` | Exact comma-separated script-safe Key Vault secret names. Empty fail-closes Key Vault `{{secret.*}}` interpolation even when workload identity has vault read access. |
| `dataPlatform.azureMessagingAllowedNamespaces` | `""` | Exact comma-separated Event Hubs / Service Bus namespace FQDNs allowed to receive workload-identity bearer tokens (`auth_type: aad`). Empty disables AAD messaging auth. |
| `networkPolicy.egress.enabled` | `false` | Restricts controller and generator egress to `dns`, `targets` (TCP 80/443 and UDP 443 by default), control-plane gRPC, and explicit `controllerExtraRules` / `generatorExtraRules` (must list PostgreSQL, Redis, telemetry, and any non-443 Kubernetes API endpoint before enabling). |
| `http3.enabled`, `http3.preflight`, `http3.quic.*` | `false`, `report`, `0`/`false` | Enables HTTP/3 (`auto+h3` and exact `3`), controls pre-run QUIC handshake preflight (`off`, `report`, or `block`), and tunes `APEXLOAD_QUIC_*` window/packet limits (`0` keeps built-in defaults). |
| `tracing.enabled`, `tracing.endpoint`, `tracing.headersSecretName` | `false`, `""`, `""` | Enables opt-in OTLP trace export across controller and generators; collector auth headers are projected from `tracing.headersSecretName` (`tracing.headersSecretKey`, default `OTEL_EXPORTER_OTLP_HEADERS`) and never stored in values or the ConfigMap. |
