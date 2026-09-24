# Local Kubernetes Deployment

This guide targets Docker Desktop Kubernetes with local Docker images only.

The controller and generators use the mandatory fail-closed lease documented in
[Generator run supervision](../../../docs/ENTERPRISE_FLEET_AND_CAPACITY.md#generator-run-supervision). Use its
connectivity-loss drill after changing the 60s/10s/5s defaults.

Important:

- This workflow is intended for Docker Desktop Kubernetes running in `kubeadm` mode.
- If Docker Desktop is running in multi-node `kind` mode, locally built images are not visible to workloads through the normal Docker image store, and the pods will fail with `ErrImageNeverPull`. Use the [kind cluster](#docker-desktop-kind-mode) section below instead.
- If you want the simplest first-time setup, switch Docker Desktop Kubernetes to `kubeadm` mode before applying these manifests.

It deploys:

- 1 controller pod
- 2 load generator pods
- 1 persistent volume claim for controller state and archives

The local base intentionally does not install KEDA or ServiceMonitor resources;
it should apply cleanly on a fresh Docker Desktop cluster with no CRDs.

## What gets created

- Namespace: `apexload-local`
- Controller Service: `apexload-controller`
- Controller Deployment: `apexload-controller`
- Generator headless Service: `apexload-generator`
- Generator StatefulSet: `apexload-generator`
- Controller PVC: `apexload-controller-data`

The local controller enables encrypted database-backed Project Secrets with a
development-only wrapping key embedded in the manifest. Replace it before
retaining real credentials. Because this local overlay does not provision gRPC
TLS, secret-bearing distributed runs fail closed; the production Helm, on-prem,
and AKS deployments provide authenticated TLS delivery.

Audited Kubernetes restart controls are enabled by default through
[restart-controls.yaml](restart-controls.yaml). Only the controller mounts a
default Kubernetes API token; generator pods receive no Kubernetes API token or
restart RBAC. See
[Kubernetes restart controls](../../../docs/KUBERNETES_RESTART_CONTROLS.md).

## Prerequisites

1. Docker Desktop is installed.
2. Kubernetes is enabled in Docker Desktop.
3. `kubectl` can talk to the Docker Desktop cluster.
4. Docker images can be built locally from this repository.
5. Docker Desktop Kubernetes is using the `kubeadm` provisioner.

Quick checks:

```bash
docker version
kubectl config current-context
kubectl get nodes
docker desktop kubernetes status --format json
```

## 1. Build the local images

From the repository root:

```bash
make docker-controller
make docker-generator
```

If you prefer to be explicit:

```bash
docker build --build-arg BINARY=controller -t apexload-controller:0.1.0 -f Dockerfile .
docker build --build-arg BINARY=generator -t apexload-generator:0.1.0 -f Dockerfile .
```

The manifests use `imagePullPolicy: Never`, so Kubernetes expects these tags to exist locally.

Before applying anything, run the preflight check:

```bash
make k8s-local-preflight
```

If that command tells you the cluster is in `kind` mode, stop there and recreate the Docker Desktop cluster in `kubeadm` mode. That is the simplest path for this repo.

## 2. Apply the Kubernetes manifests

```bash
make k8s-local-apply
```

## Optional: Configure AI provider credentials for OpenAI or Grok scripts

The controller pod reads provider credentials and default model names from optional
Kubernetes resources; without them the pod still starts, but scripts using
`{{env.OPENAI_API_KEY}}`, `{{env.OPENAI_MODEL}}`, `{{env.XAI_API_KEY}}`, or
`{{env.XAI_MODEL}}` fail at runtime. The commands are in
[AI provider setup for local Kubernetes](../../README.md#ai-provider-setup-for-local-kubernetes).

## 3. Wait for the pods to become ready

```bash
make k8s-local-status
kubectl -n apexload-local rollout status deployment/apexload-controller
kubectl -n apexload-local rollout status statefulset/apexload-generator
```

You should end up with:

- `apexload-controller-...` ready
- `apexload-generator-0` ready
- `apexload-generator-1` ready

## 4. Check the controller and generator logs

Controller logs:

```bash
kubectl -n apexload-local logs deployment/apexload-controller
```

Generator logs:

```bash
kubectl -n apexload-local logs statefulset/apexload-generator
```

In the controller logs, look for distributed execution being enabled with two generator targets.
The controller discovers generators through the headless Service
(`APEXLOAD_GENERATORS=dns://apexload-generator.apexload-local.svc.cluster.local:9090`),
so manually scaling the StatefulSet updates the fleet without editing controller
configuration:

```bash
kubectl -n apexload-local scale statefulset/apexload-generator --replicas=3
kubectl -n apexload-local rollout status statefulset/apexload-generator
```

## 5. Open the dashboard locally

Port-forward the controller service:

```bash
kubectl -n apexload-local port-forward service/apexload-controller 8080:8080
```

Then open:

- Dashboard: http://127.0.0.1:8080/
- Health check: http://127.0.0.1:8080/api/v1/system/health

## 6. Smoke test the deployment

After the dashboard is up, run the reusable smoke test helper from a second terminal:

```bash
kubectl -n apexload-local port-forward service/apexload-controller 18080:8080
make k8s-smoke-test
```

By default the smoke test uses `examples/k8s-distributed-smoke.json`, which is a GET-only distributed check intended to stay stable in local environments.

If you want a quick health-only check first:

```bash
kubectl -n apexload-local get svc
kubectl -n apexload-local get endpoints
```

## 7. Rebuild after code changes

When the controller or generator code changes:

```bash
make docker-controller
make docker-generator
kubectl -n apexload-local rollout restart deployment/apexload-controller
kubectl -n apexload-local rollout restart statefulset/apexload-generator
```

## 8. Clean up

```bash
make k8s-local-delete
```

If you also want to remove the PVC and any stored controller data:

```bash
kubectl -n apexload-local delete pvc apexload-controller-data
```

## Notes for beginners

- The controller is a Deployment because you only want one controller pod for now.
- The generators use a StatefulSet so they get stable names: `apexload-generator-0` and `apexload-generator-1`.
- The controller discovers generator pod IPs by re-resolving the headless Service DNS; it no longer hard-codes StatefulSet ordinals.
- The controller keeps its SQLite database and archives on the PVC so a controller pod restart does not wipe everything.
- `port-forward` is the simplest way to access the dashboard locally. You do not need ingress for this first setup.
- If Docker Desktop is currently in `kind` mode, switch the cluster to `kubeadm` in the Docker Desktop Kubernetes settings before using this guide. This repo's current local-image workflow is designed around that simpler mode.

## Docker Desktop kind mode

Use this section when Docker Desktop Kubernetes runs in `kind` mode. `kind` node
containerd instances cannot see the local Docker image store, so the overlay in
`deployments/k8s/local-kind` pulls `localhost:5001/apexload-...` images from a local
registry. It deploys the same 1 controller, 2 generators, and controller PVC as the
`kubeadm` flow, plus a local Docker registry on `localhost:5001`.

Prerequisites are the same as above except that Kubernetes must be in `kind` mode
and `kubectl` must point at the `docker-desktop` context.

1. Build the images: `make docker-controller docker-generator`.
2. Set up the registry: `make k8s-kind-registry-setup`. This creates or reuses a
   registry container on `localhost:5001`, connects it to the Docker `kind`
   network, and configures every Docker Desktop node to route `localhost:5001` to it.
3. Push the images: `make k8s-kind-push-images`.
4. Apply the manifests: `make k8s-kind-apply`.
5. Wait for pods with `make k8s-kind-status` and the same `rollout status` commands
   as step 3 above, then port-forward, open the dashboard, and run
   `make k8s-smoke-test` exactly as in steps 5 and 6 above. The smoke helper uploads
   `examples/k8s-distributed-smoke.json`, creates a two-group test, starts it, and
   polls until the run is terminal.
6. AI provider credentials use the same resources and commands as above.
7. After code changes: rebuild both images, run `make k8s-kind-push-images`, then
   `kubectl -n apexload-local rollout restart` the controller Deployment and generator StatefulSet.
8. Clean up with `make k8s-kind-delete` (and delete the `apexload-controller-data` PVC to drop state).

Notes:

- `localhost:5001` in the image names does not mean the pod pulls from itself; node
  containerd reroutes that address to the registry container.
- The overlay inherits the local base's DNS generator discovery, so scaling the
  StatefulSet exercises discovery the same way.
- If you later switch to `kubeadm` mode, use the main flow above instead.
