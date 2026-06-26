# Kubernetes Restart Controls

APEX Load restart controls are disabled by default. They let a `system_admin`
restart one generator pod, roll the generator StatefulSet, or roll the
controller Deployment from the admin console. The controller uses narrow
in-cluster Kubernetes REST calls and does not depend on `client-go`.

When enabled, the same audited pod-list permission also powers the generator
termination watcher: the active controller leader marks a generator pod as
`draining` as soon as Kubernetes reports a `deletionTimestamp`, so scale-in does
not place new VUsers onto a terminating pod.

## Opt In

1. Add the appropriate example manifest to your overlay:
   - `deployments/k8s/onprem/restart-controls.example.yaml`
   - `deployments/k8s/aks/restart-controls.example.yaml`
   - `deployments/k8s/local/restart-controls.example.yaml`
2. Keep the Role scoped to the controller namespace. It grants only:
   - pods: `get`, `list`, `delete`
   - deployments: `get`, `patch`
   - statefulsets: `get`, `patch`
3. Patch the controller ServiceAccount and controller pod template to mount the
   service-account token. The default manifests keep this off.
4. Set:

```yaml
- name: APEXLOAD_K8S_RESTART_ENABLED
  value: "true"
- name: APEXLOAD_K8S_NAMESPACE
  valueFrom:
    fieldRef:
      fieldPath: metadata.namespace
```

Optional overrides:

```text
APEXLOAD_K8S_CONTROLLER_DEPLOYMENT=apexload-controller
APEXLOAD_K8S_GENERATOR_STATEFULSET=apexload-generator
APEXLOAD_K8S_GENERATOR_SELECTOR=app.kubernetes.io/name=apexload-generator
APEXLOAD_K8S_RESTART_ALLOW_FORCE=true
```

## Behavior

- `GET /api/v1/system/restarts/capabilities` reports disabled unless the feature
  flag, namespace, in-cluster token, and RBAC are all present.
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
