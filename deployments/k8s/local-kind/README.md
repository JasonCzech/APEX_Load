# Local Kubernetes Deployment for Docker Desktop kind Mode

This guide is for Docker Desktop Kubernetes running in `kind` mode.
It uses a local registry so the cluster's node containerd instances can pull the APEX Load images.

It deploys:

- 1 controller pod
- 2 load generator pods
- 1 local Docker registry on `localhost:5001`
- 1 persistent volume claim for controller state and archives

## Why this guide exists

Docker Desktop `kind` clusters do not use the simple local-image workflow from `deployments/k8s/local`.
The fix is to:

1. Start a local Docker registry.
2. Tell each Docker Desktop Kubernetes node how to resolve `localhost:5001`.
3. Push the APEX Load images into that registry.
4. Deploy Kubernetes manifests that reference `localhost:5001/apexload-...` images.

## Prerequisites

1. Docker Desktop is installed.
2. Docker Desktop Kubernetes is enabled.
3. Docker Desktop Kubernetes is running in `kind` mode.
4. `kubectl` is pointing to the `docker-desktop` context.
5. The controller and generator images can be built locally.

Quick checks:

```bash
docker version
kubectl config current-context
docker desktop kubernetes status --format json
kubectl get nodes
```

## 1. Build the local images

```bash
make docker-controller
make docker-generator
```

## 2. Set up the local registry for the kind cluster

```bash
make k8s-kind-registry-setup
```

This creates or reuses a local registry container on `localhost:5001`, connects it to the Docker `kind` network, and configures every Docker Desktop Kubernetes node to treat `localhost:5001` as that registry.

## 3. Push the images into the local registry

```bash
make k8s-kind-push-images
```

## 4. Apply the manifests

```bash
make k8s-kind-apply
```

## Optional: Configure AI provider credentials for OpenAI or Grok scripts

The controller deployment reused by this overlay can read provider credentials and default model names from optional Kubernetes resources.
If you do not create them, the controller still starts, but scripts using `{{env.OPENAI_API_KEY}}`, `{{env.OPENAI_MODEL}}`, `{{env.XAI_API_KEY}}`, or `{{env.XAI_MODEL}}` will fail at runtime.

See [AI_PROVIDER_SETUP.md](../AI_PROVIDER_SETUP.md) for the same commands in a shorter reference.

For Grok/xAI scripts:

```bash
kubectl -n apexload-local create secret generic apexload-xai-secrets \
	--from-literal=XAI_API_KEY=REPLACE_WITH_XAI_API_KEY \
	--dry-run=client -o yaml | kubectl apply -f -

kubectl -n apexload-local create configmap apexload-xai-settings \
	--from-literal=XAI_MODEL=grok-4.3 \
	--dry-run=client -o yaml | kubectl apply -f -
```

For OpenAI scripts:

```bash
kubectl -n apexload-local create secret generic apexload-openai-secrets \
	--from-literal=OPENAI_API_KEY=REPLACE_WITH_OPENAI_API_KEY \
	--dry-run=client -o yaml | kubectl apply -f -

kubectl -n apexload-local create configmap apexload-openai-settings \
	--from-literal=OPENAI_MODEL=REPLACE_WITH_OPENAI_MODEL \
	--dry-run=client -o yaml | kubectl apply -f -
```

After creating or updating either resource, restart the controller so it picks up the new values:

```bash
kubectl -n apexload-local rollout restart deployment/apexload-controller
kubectl -n apexload-local rollout status deployment/apexload-controller
```

## 5. Wait for the pods to become ready

```bash
make k8s-kind-status
kubectl -n apexload-local rollout status deployment/apexload-controller
kubectl -n apexload-local rollout status statefulset/apexload-generator
```

You should end up with:

- `apexload-controller-...` ready
- `apexload-generator-0` ready
- `apexload-generator-1` ready

## 6. Open the dashboard locally

```bash
kubectl -n apexload-local port-forward service/apexload-controller 8080:8080
```

Then open:

- Dashboard: http://127.0.0.1:8080/
- Health check: http://127.0.0.1:8080/api/v1/system/health

## 7. Run the reusable distributed smoke test

In a second terminal while the port-forward is active:

```bash
make k8s-smoke-test
```

The smoke helper uploads `examples/k8s-distributed-smoke.json`, creates a two-group test, starts it, and polls until the run reaches a terminal state.

## 8. Rebuild after code changes

```bash
make docker-controller
make docker-generator
make k8s-kind-push-images
kubectl -n apexload-local rollout restart deployment/apexload-controller
kubectl -n apexload-local rollout restart statefulset/apexload-generator
```

## 9. Clean up

```bash
make k8s-kind-delete
```

If you also want to remove the controller PVC:

```bash
kubectl -n apexload-local delete pvc apexload-controller-data
```

## Notes for beginners

- `localhost:5001` in the Kubernetes image names does not mean the pod pulls from itself. The node containerd runtime is configured to reroute that address to the local registry container.
- The controller still connects to `apexload-generator-0` and `apexload-generator-1` over the headless Service on port `9090`.
- The controller PVC keeps the SQLite database and archives across controller pod restarts.
- If you later switch Docker Desktop Kubernetes to `kubeadm` mode, use `deployments/k8s/local` instead of this guide.
