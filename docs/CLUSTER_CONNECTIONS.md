# Cluster connections & host resource allocation

System administrators can register the Kubernetes and AKS control planes APEX
Load drives, and set the CPU/memory budget and node placement for each fleet
pool's generator pods — all from the dashboard, under **Fleet → Cluster
Connections** and **Fleet → Host Resource Allocation**.

Everything here is `manage_system` / `system_admin` only. Even the redacted read
shape carries API server endpoints and Azure directory identifiers, so there is
no viewer or project-admin tier on any of these routes.

## What a cluster connection is

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

## Credentials

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

## Network policy

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

### Controller egress

The shipped NetworkPolicies restrict ingress only; egress is unrestricted by
default in every profile, so registered clusters and the Azure AD login endpoint
are reachable out of the box.

If you apply the opt-in `deployments/k8s/onprem/egress-policies.example.yaml`,
note that it allows the controller HTTPS on port 443 only. AKS and most managed
distributions serve the API on 443, but kubeadm, RKE2, k3s, and many on-prem
clusters use **6443** — that port is now in the template, but remove it if you
register no such clusters. A blocked port surfaces as the `api_server` probe
timing out in the connection test.

## RBAC in the target cluster

Apply `deployments/k8s/<profile>/restart-controls.example.yaml`, which grants
the namespaced permissions:

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

The Helm chart does not template this RBAC — its only Role covers the HA
active-endpoint EndpointSlice. Restart, scaling, and allocation permissions have
always come from the standalone `restart-controls.example.yaml` manifests, so
Helm installs apply those (and the optional node ClusterRole) separately.

## Host resource allocation

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

### Quota preflight

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

## Amazon EKS

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

### Diagnosing an EKS connection

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

### Node pools

`AgentPool()` resolves an EKS node group from `eks.amazonaws.com/nodegroup`, and
a Karpenter pool from `karpenter.sh/nodepool`. Neither is the generic
`agentpool` key that AKS nodes also carry, so an allocation records the label
KEY alongside the pool name (`node_pool_label`). An allocation written with the
generic key against EKS produces a nodeSelector that matches no node, and the
generator pods stay `Pending` indefinitely. The cluster capacity response
reports the key per pool so the editor echoes back the right one.

## Domain deployment targets

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

## API

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
