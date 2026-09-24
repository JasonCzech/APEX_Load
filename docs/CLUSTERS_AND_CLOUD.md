# Help Center

A guided collection of operator articles for the APEX Load dashboard.


## Cluster connections & host resource allocation

System administrators can register the Kubernetes and AKS control planes APEX
Load drives, and set the CPU/memory budget and node placement for each fleet
pool's generator pods — all from the dashboard, under **Fleet → Cluster
Connections** and **Fleet → Host Resource Allocation**.

Everything here is `manage_system` / `system_admin` only. Even the redacted read
shape carries API server endpoints and Azure directory identifiers, so there is
no viewer or project-admin tier on any of these routes.

### What a cluster connection is

A connection is a stored description of one control plane: where it is, how to
trust it, how to authenticate to it, and which workload holds the generators.
Fleet pools bind to a connection by ID.

| Kind | Endpoint | Authentication |
|---|---|---|
| `in_cluster` | Derived from `KUBERNETES_SERVICE_HOST`/`PORT` | The controller's own projected service-account token |
| `generic` | Administrator-supplied `https://` API server URL | A long-lived ServiceAccount token |
| `aks` | Administrator-supplied `https://` API server URL | Azure AD service principal (client credentials) |
| `eks` | Administrator-supplied `https://` API server URL | AWS IAM (a presigned STS `GetCallerIdentity` URL) |

**A pool with no cluster binding is unchanged.** Its `cluster_id` is empty, and
it resolves to exactly the scaler the controller always used —
`APEXLOAD_GENERATOR_PROVISIONER` and `APEXLOAD_GENERATOR_AUTOSCALING_OWNER` keep
their existing meaning. A pool that names a cluster which cannot be loaded
**fails closed**; it never silently falls back to the controller's own cluster,
because that would start generators somewhere the administrator did not choose.

### Credentials

Credentials are write-only end to end. The read API carries no credential field
at all — not even a redacted one — only `has_client_secret` and
`credential_source`. On edit, leaving the field blank keeps the stored
credential; the dashboard omits the key entirely rather than sending an empty
string.

At rest they are sealed with the same envelope scheme as project secrets: a
fresh per-record AES-256-GCM data key encrypts the value, and that key is
wrapped by the configured `keywrap` key wrapper, which can be backed by Azure
Key Vault or AWS KMS. The AAD is bound to the connection ID and credential slot,
so an envelope copied between rows fails authentication rather than decrypting.

With KMS the binding is stronger: KMS authenticates its encryption context, so a
stolen wrapped key cannot be unwrapped against a different record even at the
KMS layer. Azure Key Vault's RSA wrap cannot authenticate additional data, so
there the binding rests entirely on the inner AEAD layer.

Configure **exactly one** wrapping key. Setting both
`APEXLOAD_WRAPPING_KMS_KEY_ID` and `APEXLOAD_WRAPPING_KEY_VAULT_KEY_ID` is
refused at startup: silently choosing one would make every envelope sealed by
the other permanently unreadable. `/api/v1/system/readiness` reports the
selection under `platform_integrations.wrapping_key`.

If no key wrapper is configured, creating or updating a connection with a
credential returns **503**. Storing a cluster credential in plaintext is not an
available fallback.

As an alternative, set `client_secret_env_var` to name an environment variable
the controller reads at use time. Nothing sensitive is persisted, which suits
GitOps deployments. It is mutually exclusive with a stored credential.

`client_secret_env_var` is a credential *reference*, so it follows the same
presence-aware rule as the credential itself rather than the replace-on-omit
rule every other field uses: omitting it on a `PUT` keeps the stored reference,
and clearing it requires sending an explicit empty string. Without that, a
client sending a partial update would silently detach the connection's
credential. The name (never a value) is returned on read so an edit form can
round-trip it.

### Network policy

Administrator-supplied API server endpoints use a dedicated egress policy, not
the general outbound SSRF guard — that guard blocks non-public addresses, which
would reject private AKS API servers, on-prem clusters, and the in-cluster
`kubernetes.default` service IP, i.e. the normal case.

The cluster policy is:

- `https` only; no userinfo, query, fragment, or path.
- A CA certificate is required unless TLS mode is explicitly `system_roots`.
- **There is no insecure-skip-TLS option.** Every managed distribution issues a
  CA, and an unverified TLS connection paired with a stored control-plane
  credential is a permanent man-in-the-middle foothold.
- Private ranges are allowed. Loopback, link-local (which covers cloud instance
  metadata at `169.254.169.254`), unspecified, and multicast are always denied.
- Addresses are resolved and vetted inside the dialer, and the validated IP
  literal is dialed, closing the DNS-rebinding window.

Azure AD token requests additionally go only to an allow-list of sovereign-cloud
login endpoints (`login.microsoftonline.com`, `.us`, `.partner…cn`).

#### Controller egress

The shipped NetworkPolicies restrict ingress only; egress is unrestricted by
default in every profile, so registered clusters and the Azure AD login endpoint
are reachable out of the box.

If you apply the opt-in `deployments/k8s/onprem/egress-policies.example.yaml`,
note that it allows the controller HTTPS on port 443 only. AKS and most managed
distributions serve the API on 443, but kubeadm, RKE2, k3s, and many on-prem
clusters use **6443** — that port is now in the template, but remove it if you
register no such clusters. A blocked port surfaces as the `api_server` probe
timing out in the connection test.

### RBAC in the target cluster

The shipped Kubernetes bases include
`deployments/k8s/<profile>/restart-controls.yaml` for the controller's own
cluster. For every registered remote cluster, apply the equivalent manifest in
that target cluster and bind it to the identity used by the connection. It
grants the namespaced permissions:

| Resource | Verbs | Used for |
|---|---|---|
| `pods` | get, list, delete | Generator inventory and restarts |
| `deployments`, `statefulsets` | get, patch | Scaling and allocation patches |
| `resourcequotas` | get, list | Quota preflight |

`deployments/k8s/aks/node-read.optional.yaml` is **opt-in** and grants
cluster-scoped `nodes get, list`. Without it everything still works:

- The connection test reports the `nodes` probe as a capability gap, not a
  failure, and the overall status is `degraded` rather than `failed`.
- Capacity returns `node_capacity: {available: false, unavailable_reason:
  "rbac_forbidden"}`.
- Node pool and zone inputs accept free text instead of offering discovered
  suggestions.
- The allocation preflight downgrades pod-fit from a block to a warning. Quota
  enforcement is unaffected.

For a remote cluster, apply the equivalents **in that cluster**, bound to the
identity the connection authenticates as.

The Helm chart templates this Role for the controller's own namespace by
default. That local Role does not authorize a registered remote cluster; remote
cluster RBAC and the optional node ClusterRole must still be applied separately
in each target cluster.

### Host resource allocation

Per pool: CPU and memory requests and limits, a node pool, a zone, additional
node selectors, and tolerations.

The change is applied as a strategic-merge patch on the generator StatefulSet's
pod template. Two details matter:

- The container is selected **by name**, never by index: the shipped Helm chart
  runs a `java-vuser-runner` sidecar beside `generator`, and patching by index
  would resize the sidecar. Resolution order is the connection's configured
  container name, then a container literally named `generator`, then the sole
  container if there is exactly one. Anything else is refused with a 409 listing
  the container names.
- `nodeSelector` is a map, so strategic merge only adds and overwrites keys. A
  key the administrator removed is sent as an explicit `null` so it is actually
  deleted. Tolerations have no merge key, so an empty list is sent to clear them.

Before patching, the target is verified: the workload must match the
connection's generator selector, or the request is refused. A workload annotated
as Helm- or Argo CD-managed produces a warning that the next sync will revert
the change; an `OnDelete` update strategy produces a warning that existing pods
keep their current resources until deleted.

#### Quota preflight

The required delta is computed against the workload's **current** per-pod
requests and replica count, not from zero — otherwise every scale-down and every
unchanged re-save would look like a fresh allocation of the whole pool and be
falsely refused.

A change is blocked when `used + delta > hard` for any declared quota key
(`requests.cpu`/`cpu`, `requests.memory`/`memory`, `pods`/`count/pods`), and
when node data is available and no eligible node could hold a single pod. There
is deliberately no whole-cluster bin-packing — that is the scheduler's job and
approximating it would refuse valid changes.

Placement is validated even when no CPU or memory is requested, so a typo in the
node pool or zone is caught at save time rather than leaving pods `Pending` with
nothing explaining why. Tolerations alone never trigger the check: they widen
where a pod may land and cannot make placement unsatisfiable on their own.

The check runs in two places so it cannot be bypassed: the allocation endpoint,
and a decorator around the pool scaler, which covers both the provisioning
create path and the retry path.

### Amazon EKS

An `eks` connection authenticates the way `aws eks get-token` does: the
controller presigns an STS `GetCallerIdentity` URL and sends it as the bearer
token, with the cluster name carried as a **signed** header so the token is
bound to one cluster.

| Credential mode | When to use | What is stored |
|---|---|---|
| `ambient` | IRSA or EKS Pod Identity. The default and the recommended mode. | Nothing |
| `assume_role` | Cross-account access, or a dedicated least-privilege role | Role ARN; the external ID is enveloped |
| `static_keys` | Only where no role-based path exists | Access key ID; the secret key is enveloped |

Required fields: `aws_region` and `aws_cluster_name`. `aws_sts_endpoint` is
optional and defaults to the regional endpoint.

**The credential mode is always explicit.** It is never inferred from which
fields happen to be filled in, and a connection that declares `static_keys` will
not fall back to the controller's own identity if the key is wrong — a typo
fails rather than silently authenticating as something else.

**The STS endpoint is allow-listed** to `sts[.region].amazonaws.com` and the
GovCloud/China partitions. A presigned `GetCallerIdentity` URL *is* a replayable
bearer credential for its lifetime, so an arbitrary administrator-supplied host
would be a credential-exfiltration channel. Use a VPC endpoint with private DNS
if you need private connectivity; a raw `vpce-*` hostname is refused.

#### Diagnosing an EKS connection

The dominant EKS failure is a perfectly valid credential whose ARN has no
Kubernetes RBAC, and it presents as a bare `401` — the same as a bad key. The
connection test therefore runs an `sts_identity` probe *before* it touches the
API server:

- **`sts_identity` fails** — the AWS credential itself is wrong. The message
  names the cause: an invalid access key, a mismatched secret, an expired
  credential, a refused `AssumeRole`, or clock skew.
- **`sts_identity` passes, `api_server` returns 401/403** — the credential is
  fine and the caller ARN is unmapped. The `api_server` message says so, and the
  `sts_identity` message reports the exact ARN to add as an EKS access entry or
  an `aws-auth` mapping.

A 401 can also mean the controller's clock is more than 15 minutes out of sync
with AWS, because the presigned URL embeds `X-Amz-Date`. The probe surfaces
`RequestTimeTooSkewed` when STS is the one rejecting it.

#### Node pools

`AgentPool()` resolves an EKS node group from `eks.amazonaws.com/nodegroup`, and
a Karpenter pool from `karpenter.sh/nodepool`. Neither is the generic
`agentpool` key that AKS nodes also carry, so an allocation records the label
KEY alongside the pool name (`node_pool_label`). An allocation written with the
generic key against EKS produces a nodeSelector that matches no node, and the
generator pods stay `Pending` indefinitely. The cluster capacity response
reports the key per pool so the editor echoes back the right one.

### Domain deployment targets

A superuser can constrain each domain to exactly one deployment provider —
`kubernetes`, `azure_aks`, or `aws_eks` — under **Settings → Domains**. The
target is both a **default** and a **hard constraint**:

- A new fleet pool scoped to the domain inherits the domain's default cluster
  when it names none, and derives its `cloud_provider`.
- A pool bound to a cluster of a different provider is refused at write time
  (409), on every path that can change a binding — including the allocation
  editor, which is the only other route that writes `cluster_id`.
- The check runs **again at scale time**, fail-closed. Write-time validation
  cannot cover a domain retargeted after a pool was already bound.

**A pool with no domain binding is completely unaffected.** `domain_id` is empty
for every pool that predates this feature, and such a pool skips the constraint
entirely — setting a domain target can never break an existing deployment. That
is why existing rows are not backfilled to the default domain: pool scoping is a
deploy blast-radius concern, not an org-chart one.

Retargeting a domain whose pools would conflict is **refused** with the list of
conflicting pools, rather than allowed with a warning; the alternative leaves
every conflicting pool failing closed at its next scale, discovered during an
incident. Clearing a target is always allowed — removing a constraint can only
widen what a pool may do.

| Route | Access |
|---|---|
| `GET /api/v1/system/deployment-targets` | system admin, `system:read` |
| `GET /api/v1/system/deployment-targets/{id}` | system admin, `system:read` |
| `PUT /api/v1/system/deployment-targets/{id}` | system admin, `settings:manage` |
| `DELETE /api/v1/system/deployment-targets/{id}` | system admin, `settings:manage` |

`GET` on an unconfigured domain returns **200** with `configured: false`, not
404: having no constraint is a normal state, not an error.

### API

All routes require the `system_admin` global role. Mutating cluster routes
require the `settings:manage` service-account scope (reads use `system:read`).

| Method | Path |
|---|---|
| POST / GET | `/api/v1/system/clusters` |
| GET / PUT / DELETE | `/api/v1/system/clusters/{id}` |
| POST | `/api/v1/system/clusters/{id}/test` |
| GET | `/api/v1/system/clusters/{id}/capacity` |
| PUT | `/api/v1/fleet/pools/{id}/allocation` |
| GET | `/api/v1/fleet/pools/{id}/allocation/preflight` |

`PUT`s carry the resource's `updated_at` as an optimistic-concurrency token; a
stale value returns **409** rather than overwriting a concurrent edit.

Deleting a connection that fleet pools still reference returns **409** naming
those pools. It refuses rather than cascading, because an orphaned pool would
fall back to the controller's own cluster on its next scale.

Cluster CRUD, connection tests, and capacity reads are **not** gated on the HA
execution-leader lease — they are CAS-guarded database writes and read-only
probes, safe on any replica. `PUT …/allocation` **is** gated, because it mutates
a live cluster.

The connection test returns a per-probe array (`api_server`,
`generator_workload`, `resource_quotas`, `nodes`) rather than a boolean, so an
administrator sees exactly which capability is missing. It is throttled to one
run per connection per 5 seconds. Authentication failures report a status code
and a fixed string, never the API server's response body.


## Kubernetes Restart Controls

APEX Load Kubernetes installs enable restart controls by default. They let a
`system_admin` restart one generator pod, roll the generator StatefulSet, or
roll the controller Deployment from the admin console. The controller uses
narrow in-cluster Kubernetes REST calls and does not depend on `client-go`.
Docker, desktop, and App Service runtimes remain disabled unless explicitly
configured because they do not have an in-cluster Kubernetes identity.

When enabled, the same audited pod-list permission also powers the generator
termination watcher: the active controller leader marks a generator pod as
`draining` as soon as Kubernetes reports a `deletionTimestamp`, so scale-in does
not place new VUsers onto a terminating pod.

### Default Installation

The local, on-prem, AKS, and EKS Kustomize bases include their profile's
`restart-controls.yaml`. The Helm chart renders the same permissions when
`restartControls.enabled=true`, which is the default. The Role is scoped to the
controller namespace and grants only:

   - pods: `get`, `list`, `delete`
   - deployments: `get`, `patch`
   - statefulsets: `get`, `patch`
   - resourcequotas: `get`, `list`

Only the controller pod receives the default Kubernetes API token and restart
RBAC. Generator pods explicitly disable that automatic token mount. AKS
Workload Identity and EKS IRSA may still inject an audience-specific federated
identity token into a generator pod for cloud APIs; that is not the default
Kubernetes API token and does not grant the restart Role. The Kubernetes
manifests set:

```yaml
- name: APEXLOAD_K8S_RESTART_ENABLED
  value: "true"
- name: APEXLOAD_K8S_NAMESPACE
  valueFrom:
    fieldRef:
      fieldPath: metadata.namespace
```

Operator environment overrides:

```text
APEXLOAD_K8S_CONTROLLER_DEPLOYMENT=apexload-controller
APEXLOAD_K8S_GENERATOR_STATEFULSET=apexload-generator
APEXLOAD_K8S_GENERATOR_SELECTOR=app.kubernetes.io/name=apexload-generator
APEXLOAD_K8S_RESTART_ALLOW_FORCE=true
```

`APEXLOAD_K8S_RESTART_ALLOW_FORCE` defaults to `true`; set it to `false` when
forced controller and generator restarts must be prohibited.

The Helm chart exposes the equivalent settings:

| Value | Default | Purpose |
|---|---|---|
| `restartControls.enabled` | `true` | Render restart RBAC, mount the controller Kubernetes API token, and enable the runtime integration. |
| `restartControls.allowForce` | `true` | Permit a system administrator to confirm a forced restart when active-run protection would otherwise block it. |
| `restartControls.controllerDeployment` | `apexload-controller` | Deployment patched for a controller rollout restart. |
| `restartControls.generatorStatefulSet` | `apexload-generator` | StatefulSet patched for generator rollout restart and scaling. |
| `restartControls.generatorSelector` | `app.kubernetes.io/name=apexload-generator` | Pod selector used for generator inventory and targeted pod restart. |

The chart deliberately accepts one Kubernetes label-selector requirement for
`generatorSelector`: an existence requirement (`key` or `!key`), an equality
requirement (`key=value`, `key==value`, or `key!=value`), or a canonical set
requirement (`key in (value)` or `key notin (value)`). Set requirements may list
multiple values separated by `, `, but combining multiple requirements is not
supported. Keys and values must satisfy Kubernetes label length and character
rules, and the complete selector is limited to 253 characters.

#### Helm upgrades from earlier releases

`helm upgrade --reuse-values` carries forward the release's stored values and
does not add new keys from the current `values.yaml`. The chart therefore
supplies its own fallback for every `restartControls` key. A legacy release
with no `restartControls` map receives the current default: restart controls
become enabled, and the upgrade renders the scoped Role, RoleBinding, and
controller token mount.

To keep a legacy release opted out, set the value on the upgrade itself:

```bash
helm upgrade apexload deployments/helm/apexload \
  --namespace apexload \
  --reuse-values \
  --set restartControls.enabled=false
```

If restart RBAC with the chart's object names was applied manually before Helm
managed it, an enabled upgrade stops with an `invalid ownership metadata`
error. Helm will not silently adopt those objects. First inspect the two exact
objects (adjust the release and namespace values as needed):

```bash
release=apexload
namespace=apexload
kubectl --namespace "$namespace" get \
  role/apexload-restart-controls \
  rolebinding/apexload-restart-controls \
  -o yaml
```

Compare the live Role rules and sole controller ServiceAccount subject with
the permissions documented above. Stop if either object grants additional
permissions, targets a different ServiceAccount, or has
`meta.helm.sh/release-*` annotations for another release. When the objects are
an exact match and must remain continuously available, adopt them into this
release before the upgrade:

```bash
kubectl --namespace "$namespace" label \
  role/apexload-restart-controls \
  rolebinding/apexload-restart-controls \
  app.kubernetes.io/managed-by=Helm --overwrite
kubectl --namespace "$namespace" annotate \
  role/apexload-restart-controls \
  rolebinding/apexload-restart-controls \
  meta.helm.sh/release-name="$release" \
  meta.helm.sh/release-namespace="$namespace" \
  --overwrite
helm upgrade "$release" deployments/helm/apexload \
  --namespace "$namespace" \
  --reuse-values
```

If a brief loss of restart authority is acceptable, controlled deletion is the
simpler alternative. Keep the original, clean source manifest ready for
rollback (raw `kubectl get -o yaml` output contains server-owned metadata and is
not a safe restore file), make sure no other workload uses the binding, then
delete the binding before the Role and immediately upgrade so Helm recreates
both:

```bash
kubectl --namespace "$namespace" delete rolebinding/apexload-restart-controls
kubectl --namespace "$namespace" delete role/apexload-restart-controls
helm upgrade "$release" deployments/helm/apexload \
  --namespace "$namespace" \
  --reuse-values
```

If that upgrade fails, restore the objects with
`kubectl apply -f path/to/original-restart-controls.yaml` while the failure is
investigated. When opting out instead of enabling, do not adopt the manually
applied objects: confirm they are unused, delete the RoleBinding and Role, and
use the opt-out upgrade command above. Helm cannot prune resources that the
release never owned.

Set `restartControls.enabled=false` to omit the Role and RoleBinding, keep the
restart token unmounted, and render
`APEXLOAD_K8S_RESTART_ENABLED=false` directly in the controller environment.
That explicit value takes precedence over a conflicting value from the shared
ConfigMap or Secret. For a release that already owns them, a Helm upgrade tracks
and removes the previously rendered restart Role and RoleBinding. HA controllers
still receive their separate projected EndpointSlice leadership token; that
token does not gain restart permissions after the restart binding is removed.

A new custom Kustomize distribution can opt out by excluding
`restart-controls.yaml` from its resource graph (or deleting the inherited Role
and RoleBinding with patches), setting the controller pod's
`automountServiceAccountToken` to `false`, and replacing
`APEXLOAD_K8S_RESTART_ENABLED` with `false`.

For an **existing** Kustomize installation, removing objects from the rendered
resource graph does not delete the live RBAC objects on a normal
`kubectl apply -k`. Revoke the binding explicitly before applying the opt-out
overlay (replace the namespace as needed):

```bash
kubectl -n apexload delete rolebinding/apexload-restart-controls --ignore-not-found
kubectl -n apexload delete role/apexload-restart-controls --ignore-not-found
kubectl apply -k path/to/restart-controls-disabled-overlay
```

A carefully scoped `kubectl apply --prune` workflow can provide the same
deletion, but simple omission or a Kustomize delete patch cannot remove an
already-created object by itself. In HA, the projected leadership token remains
mounted and retains only the separately bound EndpointSlice permissions after
the restart binding is deleted.

### Behavior

- `GET /api/v1/system/restarts/capabilities` reports disabled unless the feature
  flag, namespace, and in-cluster Kubernetes client are configured.
- `GET /api/v1/system/readiness` reports Kubernetes API and pod-list access as
  a dedicated check, including the effective namespace, safe pod counts, and
  probe latency. Connectivity or RBAC failures degrade readiness without making
  the controller pod unavailable.
- `POST /api/v1/system/restarts` requires a reason and exact target
  confirmation.
- Generator pod restart resolves the pod from Kubernetes by pod name, pod IP, or
  StatefulSet DNS ordinal, then best-effort marks the old target as `draining`
  before deleting the pod.
- Drain state is canonicalized by stable `generator_id` when the controller has
  observed generator identity, with a one-release dual-read fallback for legacy
  target/IP keys.
- The generator termination watcher polls pod inventory through the same RBAC
  path and marks terminating generator pods as `draining`; it is a safety net for
  KEDA or manual scale-in, not a replacement for graceful generator shutdown.
- Generator workload restart is blocked when generators have active VUsers or a
  run is active unless `force` is set.
- Controller restart is blocked while runs are `INITIALIZING`, `RUNNING`,
  `PAUSED`, `STOPPING`, or `SHAKEOUT` unless `force` is set. Forced controller
  restarts will cause those runs to reconcile as `FAILED` after startup.

All success, blocked, and failed attempts are audit-recorded with namespace,
workload, pod, scope, active VUsers, reachability, force, and reason.


## Multi-cloud PaaS compute

APEX Load supports registered, provider-managed generator capacity alongside
the existing Kubernetes and static-generator paths:

- Azure Container Apps for warm capacity and Container Apps Jobs for burst.
- Azure Container Instances for burst-only capacity the controller can dial
  directly. A container group is a single instance with its own IP, so it holds
  no warm floor; it exists for operators who need a directly addressable Azure
  generator instead of the reverse tunnel.
- AWS ECS Fargate services for warm capacity and `RunTask` for burst.
- Azure Container Apps or ECS Fargate as a single-replica controller host.

Terraform bootstraps networks, identities, databases, caches, registries, and
runtime templates. APEX Load imports the emitted registration manifest and
manages only capacity attached to those templates. A reverse-connect manifest
contains an enrollment token and must be handled as a secret.

### Model

`CloudConnection` stores provider scope and authentication. `ExecutionTarget`
stores placement, connectivity, capacity ownership, archive mode, runtime
profiles, and provider resource references. Existing cluster connections are
transactionally projected into Kubernetes execution targets; legacy cluster
foreign keys remain populated during the compatibility period.

Provider placement is immutable while referenced. Changing an execution
target's runtime resource, connection, connectivity, ownership, archive mode,
or runtime profile requires a new target and an explicit pool/policy repoint.
Likewise, a referenced cloud connection may rotate credentials but may not
change subscription, account, partition, region, or resource-group scope.
Pools must release all active and durable capacity before their target can be
changed.

A domain deployment policy has a default execution target and an allowlist.
Selecting another target is explicit. APEX Load never silently fails a run over
to a different cloud.

PaaS mutations are disabled unless `APEXLOAD_PAAS_COMPUTE_ENABLED=true`.
Registration, validation, and capability discovery remain available while the
feature is disabled.

#### Validating a credential before building on it

`POST /api/v1/system/cloud-connections/{id}/test` performs a real provider round
trip — an ARM token plus a subscription read on Azure, `GetCallerIdentity` on
AWS — so a credential can be proven before any execution target depends on it.
The AWS probe echoes the resolved caller ARN, because the dominant ECS failure
is a valid credential whose principal lacks task permissions.

`GET /api/v1/system/cloud-connections/{id}/resources` enumerates the provider
resources a target binds to (Container Apps environments, ECS clusters), capped
at 200 with a `truncated` flag. `GET /api/v1/system/execution-targets/{id}/capacity`
reports the configured envelope of every bound pool alongside the live provider
instance inventory, deduplicated by provider ID.

Provider or permission failures on discovery return `502`, not `4xx`: the fix is
in the cloud account, not the request.

#### Container Instances

An ACI target names a pre-provisioned container group template whose shape is
cloned per launch; APEX Load never invents a group definition. Group names are
derived deterministically from the launch nonce, so a lost `PUT` response
updates the existing group instead of creating a second one — the ACI equivalent
of the ECS client token.

Direct connectivity requires a delegated subnet. Without one the group receives
a public address, which would put the generator's gRPC control port on the
internet, so direct mode is refused and the generator must reverse-connect.

### Capacity lifecycle

Cloud capacity is asynchronous. A provisioning request records desired,
observed, ready, pending, draining, and failed counts while a provider backend
reconciles the resource. Provider acceptance is not readiness: a generator is
ready only after its provider instance is healthy and either its reverse
session is authenticated or its direct gRPC heartbeat succeeds.

Operator-created provisioning requests reconcile durable warm capacity only.
Burst tasks and Job executions are launched only for a specific run, so every
ephemeral instance has an archive-safe teardown owner. Provider mutations are
serialized per pool to prevent a cancellation from rolling back a newer scale
operation.

A PaaS pool must be scaled to zero and have a zero warm floor before it can be
deleted. This keeps the pool identity available for maintenance reconciliation
and prevents an ECS service desired count or Container App minimum replica count
from becoming an unowned billable resource.

Every burst launch gets a fresh generator ID, launch nonce, and provider
idempotency token. The intent is persisted before the provider call. Provider
resources are tagged with controller, pool, operation, test, generator, and
launch identities so leadership recovery can discover and reap orphans.

Warm and burst capacity have independent owners. Set an owner to `external`
when KEDA, Container Apps scaling rules, or ECS Application Auto Scaling owns
it; APEX Load then refuses a conflicting mutation.

The current reverse enrollment protocol scopes sessions to an execution target,
not a pool. A PaaS execution target therefore binds to exactly one fleet pool;
the API rejects an ambiguous second binding. Kubernetes and static targets keep
their existing multi-pool behavior.

Warm-capacity scale-down transiently cordons every connected generator in the
pool, waits for runs that had already started to reach a durable terminal
archive, and only then lowers provider capacity. Operator-owned cordons are
preserved. This pool-wide drain is intentional because ECS and Container Apps
may choose which service replica to remove.

### Reverse connectivity

Container Apps and Jobs use an outbound generator-to-controller WebSocket on
the controller HTTPS port. The WebSocket is exposed as the existing gRPC byte
stream, so `GeneratorControl` is not reimplemented. The gateway rejects browser
origins, requires a target-scoped enrollment token, and additionally requires
the persisted launch ID and nonce for burst workers. Production generators
refuse plaintext or tokenless reverse connections.

Every reconnect also carries a fresh timestamped session nonce; stale or
replayed enrollment requests are rejected. The reference PaaS stacks explicitly
trust the managed ingress HTTPS scheme header because TLS terminates before the
controller container. Direct and self-managed deployments do not enable that
trust switch and retain the normal trusted-proxy gate.

The outer WSS session is the authenticated encrypted transport for reverse-only
PaaS generators, so the reference stacks mark that channel explicitly rather
than requiring a second TLS handshake inside the WebSocket. That assertion is
rejected for direct or dual-mode generator processes; those paths continue to
require the existing gRPC TLS/mTLS configuration.

Enrollment tokens are returned only during registration or rotation. Only a
SHA-256 verifier is persisted. Provider-bound HTTP handshake identity remains
a future hardening step; provider managed identity/task role is currently
attested independently by the execution-target probe.

Rotation returns the replacement token once and retains the previous verifier
for a 24-hour rolling overlap. Update the Container Apps secret or AWS Secrets
Manager value and roll standing replicas during that window; new burst
instances consume the current provider secret. The controller deliberately
cannot inject the plaintext token through a backend call because it persists
only the verifier.

The Terraform stacks generate a strong enrollment token when one is not
supplied and place it in the generator secret plus a sensitive one-time
registration manifest. This avoids a create-target/apply chicken-and-egg loop;
the manifest contains no cloud credential, but it must still be handled as a
secret because it enrolls generators. AWS additionally supports `auto`,
`direct`, or `reverse` generator connectivity explicitly.

### Data-loss and cost fences

Engine drain and archive drain are separate. Ephemeral compute is terminated
only after the controller-pulled archive is durably terminal. Provider backends
also reject deprovisioning when that terminal marker is absent. This prevents a
successful engine drain from deleting the only copy of generator artifacts.

Maximum instance lifetime is a separate cost fence. On expiry, the reconciler
may terminate an instance whose archive never became durable and records the
archive state as expired. This is intentionally distinguishable from normal
archive-safe cleanup.

Connection tests inspect provider-observed task/app definitions for every
runtime-profile sidecar and separately attest the Azure principal or ECS task
role. Bruno and Java Vuser protocols must be represented by loopback sidecars
in the registered template.

Provisioning happens before run supervision begins, so a 30–120 second cloud
cold start does not consume the supervision deadman TTL.

### PaaS controller requirements

A PaaS-hosted controller requires PostgreSQL, Redis/Valkey, and object-backed
archive/import-asset storage. SQLite and local import assets are refused.
Unsupported process-role splits and controller HA fail closed. The first PaaS
release therefore runs one `all` controller replica; multi-replica API plus a
lease-elected gateway/orchestrator remains future work.

Azure and AWS Terraform can disable AKS/EKS independently and emit an
execution-target registration manifest. The reference PaaS controller
definitions opt into the compute feature and configure durable database,
cache, and object storage.

### Production evidence

The provider contracts, signing, endpoint allowlists, migration behavior,
reverse transport, archive fence, orphan reconciliation, dashboard, OpenAPI,
and Terraform syntax are test-covered offline. Production readiness still
requires opt-in live-cloud evidence for identity permissions, private network
reachability, quota behavior, cold starts, scale, archive durability, restart
recovery, and orphan cleanup. Design targets are not claims of achieved scale.
