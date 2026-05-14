# Local Kubernetes Deployment

This guide targets Docker Desktop Kubernetes with local Docker images only.

Important:

- This workflow is intended for Docker Desktop Kubernetes running in `kubeadm` mode.
- If Docker Desktop is running in multi-node `kind` mode, locally built images are not visible to workloads through the normal Docker image store, and the pods will fail with `ErrImageNeverPull`.
- If you want the simplest first-time setup, switch Docker Desktop Kubernetes to `kubeadm` mode before applying these manifests.

It deploys:

- 1 controller pod
- 2 load generator pods
- 1 persistent volume claim for controller state and archives

## What gets created

- Namespace: `stormrunner-local`
- Controller Service: `stormrunner-controller`
- Controller Deployment: `stormrunner-controller`
- Generator headless Service: `stormrunner-generator`
- Generator StatefulSet: `stormrunner-generator`
- Controller PVC: `stormrunner-controller-data`

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
docker build --build-arg BINARY=controller -t stormrunner-controller:0.1.0 -f Dockerfile .
docker build --build-arg BINARY=generator -t stormrunner-generator:0.1.0 -f Dockerfile .
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

The controller pod can now read provider credentials and default model names from optional Kubernetes resources.
If you do not create them, the pod still starts normally, but any script using `{{env.OPENAI_API_KEY}}`, `{{env.OPENAI_MODEL}}`, `{{env.XAI_API_KEY}}`, or `{{env.XAI_MODEL}}` will fail at runtime.

See [AI_PROVIDER_SETUP.md](../AI_PROVIDER_SETUP.md) for the same commands in a shorter reference.

For Grok/xAI scripts:

```bash
kubectl -n stormrunner-local create secret generic stormrunner-xai-secrets \
	--from-literal=XAI_API_KEY=REPLACE_WITH_XAI_API_KEY \
	--dry-run=client -o yaml | kubectl apply -f -

kubectl -n stormrunner-local create configmap stormrunner-xai-settings \
	--from-literal=XAI_MODEL=grok-4.3 \
	--dry-run=client -o yaml | kubectl apply -f -
```

For OpenAI scripts:

```bash
kubectl -n stormrunner-local create secret generic stormrunner-openai-secrets \
	--from-literal=OPENAI_API_KEY=REPLACE_WITH_OPENAI_API_KEY \
	--dry-run=client -o yaml | kubectl apply -f -

kubectl -n stormrunner-local create configmap stormrunner-openai-settings \
	--from-literal=OPENAI_MODEL=REPLACE_WITH_OPENAI_MODEL \
	--dry-run=client -o yaml | kubectl apply -f -
```

After creating or updating either resource, restart the controller so it picks up the new values:

```bash
kubectl -n stormrunner-local rollout restart deployment/stormrunner-controller
kubectl -n stormrunner-local rollout status deployment/stormrunner-controller
```

## 3. Wait for the pods to become ready

```bash
make k8s-local-status
kubectl -n stormrunner-local rollout status deployment/stormrunner-controller
kubectl -n stormrunner-local rollout status statefulset/stormrunner-generator
```

You should end up with:

- `stormrunner-controller-...` ready
- `stormrunner-generator-0` ready
- `stormrunner-generator-1` ready

## 4. Check the controller and generator logs

Controller logs:

```bash
kubectl -n stormrunner-local logs deployment/stormrunner-controller
```

Generator logs:

```bash
kubectl -n stormrunner-local logs statefulset/stormrunner-generator
```

In the controller logs, look for distributed execution being enabled with two generator targets.

## 5. Open the dashboard locally

Port-forward the controller service:

```bash
kubectl -n stormrunner-local port-forward service/stormrunner-controller 8080:8080
```

Then open:

- Dashboard: http://127.0.0.1:8080/
- Health check: http://127.0.0.1:8080/api/v1/system/health

## 6. Smoke test the deployment

After the dashboard is up, run the reusable smoke test helper from a second terminal:

```bash
kubectl -n stormrunner-local port-forward service/stormrunner-controller 18080:8080
make k8s-smoke-test
```

By default the smoke test uses `examples/k8s-distributed-smoke.json`, which is a GET-only distributed check intended to stay stable in local environments.

If you want a quick health-only check first:

```bash
kubectl -n stormrunner-local get svc
kubectl -n stormrunner-local get endpoints
```

## 7. Rebuild after code changes

When the controller or generator code changes:

```bash
make docker-controller
make docker-generator
kubectl -n stormrunner-local rollout restart deployment/stormrunner-controller
kubectl -n stormrunner-local rollout restart statefulset/stormrunner-generator
```

## 8. Clean up

```bash
make k8s-local-delete
```

If you also want to remove the PVC and any stored controller data:

```bash
kubectl -n stormrunner-local delete pvc stormrunner-controller-data
```

## Notes for beginners

- The controller is a Deployment because you only want one controller pod for now.
- The generators use a StatefulSet so they get stable names: `stormrunner-generator-0` and `stormrunner-generator-1`.
- The controller connects to those two generator pods directly over gRPC on port `9090`.
- The controller keeps its SQLite database and archives on the PVC so a controller pod restart does not wipe everything.
- `port-forward` is the simplest way to access the dashboard locally. You do not need ingress for this first setup.
- If Docker Desktop is currently in `kind` mode, switch the cluster to `kubeadm` in the Docker Desktop Kubernetes settings before using this guide. This repo's current local-image workflow is designed around that simpler mode.
