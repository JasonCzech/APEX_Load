# Kubernetes Restart Controls

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

## Default Installation

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

### Helm upgrades from earlier releases

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

## Behavior

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
