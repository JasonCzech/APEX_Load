# Deployment Guide

Status: Current

## Overview

Choose the deployment surface that matches your environment. Deployment-specific
READMEs own the details; this page is the router.

| Target | Start Here |
| --- | --- |
| Local Kubernetes | [k8s/local/README.md](k8s/local/README.md) |
| Docker Desktop kind mode | [k8s/local/README.md](k8s/local/README.md#docker-desktop-kind-mode) |
| Production on-prem Kubernetes | [k8s/onprem/README.md](k8s/onprem/README.md) |
| AKS (deployment, migration, readiness) | [k8s/aks/README.md](k8s/aks/README.md) |
| EKS (deployment, readiness) | [k8s/eks/README.md](k8s/eks/README.md) |
| Helm chart | [helm/apexload/README.md](helm/apexload/README.md) |
| Docker Compose monitoring | [docker/README.md](docker/README.md) |
| Azure Terraform | [k8s/aks/README.md](k8s/aks/README.md#azure-terraform) |
| AWS Terraform | [k8s/eks/README.md](k8s/eks/README.md#aws-terraform) |
| AKS Azure Monitor overlay | [k8s/aks/README.md](k8s/aks/README.md#azure-monitor-managed-prometheus-overlay) |
| Kubernetes monitoring examples | [Kubernetes monitoring stack](#kubernetes-monitoring-stack) |
| Java Vuser runner component | [Java Vuser runner component](#java-vuser-runner-component) |
| Browser generator for observer pools | [Browser generator for observer pools](#browser-generator-for-observer-pools) |
| Local AI provider secrets | [AI provider setup for local Kubernetes](#ai-provider-setup-for-local-kubernetes) |
| Azure App Service controller launcher | `deployments/azure/appservice/README.md` |

### Kubernetes directory map

Every directory under `deployments/k8s/` serves a distinct base, cloud/HA overlay, or opt-in Kustomize component:

| Directory | Purpose |
| --- | --- |
| `k8s/local` | Single-controller + 2-generator development base for Docker Desktop `kubeadm` mode (no KEDA/ServiceMonitor CRDs). |
| `k8s/local-kind` | Local overlay rewriting images to `localhost:5001/apexload-*` for multi-node Docker Desktop `kind` clusters. |
| `k8s/local-java-vuser` | Local overlay composing `components/java-vuser-runner` into the generator pod. |
| `k8s/onprem` | Hardened self-managed Kubernetes base (mTLS + token gRPC, TLS ingress, PSA `restricted`, NetworkPolicies, PDBs). |
| `k8s/onprem-ha` | Multi-replica controller HA overlay on `onprem` with selectorless `apexload-controller-active` Service, fenced EndpointSlice, and RWX import-assets PVC. |
| `k8s/onprem-autoscaling` | Adds the KEDA `ScaledObject` and `monitoring` Prometheus Operator resources to `onprem`. |
| `k8s/onprem-ha-autoscaling` | Combines `onprem-ha` controller high availability with KEDA autoscaling and `monitoring`. |
| `k8s/onprem-java-vuser` | On-premises overlay composing `components/java-vuser-runner` into the generator StatefulSet. |
| `k8s/aks` | Production Azure AKS base (PostgreSQL, Managed Redis, Blob Storage, Key Vault workload identity, KEDA, headless DNS discovery). |
| `k8s/aks-ha` | Multi-replica controller HA overlay on `aks` using `azurefile-csi` for RWX import assets and fenced active EndpointSlice. |
| `k8s/aks-prometheus` | Adds `monitoring` (`ServiceMonitor` + `PrometheusRule`) to `aks` for in-cluster kube-prometheus-stack. |
| `k8s/aks-azure-monitor` | Switches the AKS KEDA `ScaledObject` to Azure Monitor managed Prometheus with workload-identity `TriggerAuthentication`. |
| `k8s/aks-java-vuser` | AKS overlay composing `components/java-vuser-runner` into the generator StatefulSet. |
| `k8s/eks` | Production Amazon EKS base (RDS PostgreSQL, ElastiCache Redis, S3, Secrets Manager + KMS via IRSA, KEDA, ALB ingress). |
| `k8s/eks-ha` | Multi-replica controller HA overlay on `eks` using `efs-sc` for RWX import assets and fenced active EndpointSlice. |
| `k8s/eks-prometheus` | Adds `monitoring` (`ServiceMonitor` + `PrometheusRule`) to `eks`. |
| `k8s/components/archive-staging` | Opt-in Kustomize component mounting a shared RWX `apexload-controller-archive-staging` PVC and isolating per-pod reader caches. |
| `k8s/components/java-vuser-runner` | Opt-in Kustomize component adding the credential-minimized loopback Java Vuser sidecar (`legacy full_lifecycle` only). |
| `k8s/monitoring` | Shared Kustomize base containing `servicemonitor.yaml`, `prometheusrule.yaml`, and remote-write reference config. |
| `k8s/ha` | Standalone reference examples for HA controller deployments (`controller-ha-deployment.example.yaml`) and Valkey (`redis-valkey.example.yaml`). |
| `k8s/java-host-broker` | Experimental mTLS broker manifests for dedicated Java execution pods (disabled by default; not part of standard overlays). |

## Validation Baseline

Before promoting a Kubernetes or AKS deployment, validate manifests and generated
configuration with the relevant toolchain:

```bash
kubectl kustomize deployments/k8s/local
kubectl kustomize deployments/k8s/local-kind
kubectl kustomize deployments/k8s/onprem
kubectl kustomize deployments/k8s/onprem-ha
kubectl kustomize deployments/k8s/aks
kubectl kustomize deployments/k8s/aks-ha
kubectl kustomize deployments/k8s/eks
kubectl kustomize deployments/k8s/eks-ha
helm lint deployments/helm/apexload
helm template apexload deployments/helm/apexload
```

The `make k8s-*` targets (`k8s-local-*`, `k8s-kind-*`, `k8s-onprem-*`, `k8s-aks-*`,
`k8s-eks-*`, and `k8s-smoke-test`) wrap the apply, status, and delete steps for
these overlays, and `scripts/render-onprem-manifests.sh`,
`scripts/render-aks-manifests.sh`, and `scripts/render-eks-manifests.sh` render
validated manifests with environment values; see the `Makefile` for the exact
commands.

> **Important (on-premises rendering):** While `make k8s-aks-apply` and
> `make k8s-eks-apply` invoke their render scripts automatically,
> `make k8s-onprem-apply` and `make k8s-onprem-autoscaling-apply` execute raw
> `kubectl apply -k`. Never apply an unrendered `onprem*` directory directly: edit
> the `CHANGE-ME` values, set a unique `APEXLOAD_FLEET_ID`, render with
> `./scripts/render-onprem-manifests.sh --output /tmp/apexload-onprem.yaml`, and
> apply the rendered file as documented in [k8s/onprem/README.md](k8s/onprem/README.md).

## AI provider setup for local Kubernetes

Use this when you want APEX Load scripts running in the controller pod to read provider credentials from Kubernetes instead of your local shell.

The local controller deployment supports these optional environment variables:

- `OPENAI_API_KEY`
- `OPENAI_MODEL`
- `XAI_API_KEY`
- `XAI_MODEL`

If you do not create the matching Kubernetes resources, the controller still starts normally, but scripts using those variables will fail at runtime.

### Grok / xAI

Create the secret and default model config:

```bash
kubectl -n apexload-local create secret generic apexload-xai-secrets \
  --from-literal=XAI_API_KEY=REPLACE_WITH_XAI_API_KEY \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl -n apexload-local create configmap apexload-xai-settings \
  --from-literal=XAI_MODEL=grok-4.3 \
  --dry-run=client -o yaml | kubectl apply -f -
```

### OpenAI

Create the secret and default model config:

```bash
kubectl -n apexload-local create secret generic apexload-openai-secrets \
  --from-literal=OPENAI_API_KEY=REPLACE_WITH_OPENAI_API_KEY \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl -n apexload-local create configmap apexload-openai-settings \
  --from-literal=OPENAI_MODEL=REPLACE_WITH_OPENAI_MODEL \
  --dry-run=client -o yaml | kubectl apply -f -
```

### Reload The Controller

After creating or updating either secret or configmap, restart the controller so the pod picks up the new values:

```bash
kubectl -n apexload-local rollout restart deployment/apexload-controller
kubectl -n apexload-local rollout status deployment/apexload-controller
```

### Verify The Environment

You can confirm a non-secret model variable is present with:

```bash
kubectl -n apexload-local exec deploy/apexload-controller -- printenv XAI_MODEL
kubectl -n apexload-local exec deploy/apexload-controller -- printenv OPENAI_MODEL
```

Do not print the API key values back out of the pod.

## Kubernetes monitoring stack

These manifests are production-oriented examples for Phase 3 of the enterprise data platform upgrade.

APEX Load keeps exposing `/metrics` from the controller. Prometheus scrapes that endpoint for live operational metrics, then remote-writes to the enterprise retention backend:

- Grafana Mimir
- Thanos Receive
- A managed Prometheus-compatible remote-write endpoint

Use these examples as overlays, not as local-kind defaults. `APEXLOAD_PROMETHEUS_REMOTE_WRITE_URL` is consumed by the external Prometheus configuration in `deployments/k8s/monitoring`; the APEX Load controller remains scrape-only and does not remote-write samples itself.

### Files

- `servicemonitor.yaml` - Prometheus Operator scrape definition for the controller.
- `prometheus-remote-write-example.yaml` - example Prometheus config with remote-write destinations.
- `prometheusrule.yaml` - starter alert rules for scrape absence, telemetry staleness, telemetry drops, and WebSocket fan-out drops.

### Required Labels

The controller metrics endpoint should preserve bounded labels suitable for retention:

- `environment`
- `domain`
- `project`
- `release`
- `scenario`
- `run`
- `protocol`
- `generator_region`
- generator identity is intentionally kept out of controller `/metrics`; use Kubernetes pod labels, cAdvisor, and kube-state-metrics for per-generator host telemetry.

High-cardinality raw transaction details stay in APEX Load archive bundles instead of Prometheus.

## Browser generator for observer pools

Browser observers measure what a user sees, so they run the Chromium build the
Playwright driver was tested with. Each release publishes
`apexload-browser-generator` (standard profile only, tag `<release>-standard`):
the generator on Debian with Playwright's own Chromium, built from
`Dockerfile.browser-generator`. The controller image carries the same build for
recording and Verify. The default `apexload-generator` image stays the lean
Alpine build with Alpine's Chromium; use it for protocol-only pools.

Deploy an observer pool as a second generator from the Helm chart with
`image.generatorRepository=apexload-browser-generator` and the `-standard` tag,
pinned to the release digest, and register its generators in their own fleet
pool. The AKS deployment workflow deploys the protocol generator only. Image
builds download Chromium from Playwright's CDN; set the
`PLAYWRIGHT_DOWNLOAD_HOST` build argument to use a mirror.

## Java Vuser runner component

This opt-in component adds the loopback-only, credential-minimized Java Vuser
runner to an APEX Load generator pod (`deployments/k8s/components/java-vuser-runner`).
Add it to a local, on-prem, or AKS overlay:

```yaml
components:
  - ../components/java-vuser-runner
```

The published image includes JDK 21 and a Java 17-compatible harness. User
classes must be precompiled into a signed custom runner image, or supplied by a
separate read-only volume patch. Do not mount generator state, gRPC credentials,
service-account tokens, or workload-identity files into this container.

This stock non-privileged component does not receive writable cgroup v2
delegation. It therefore supports legacy `full_lifecycle` execution only;
combined persistent Java imports fail sidecar capability preflight with
`persistent_lifecycle_unavailable`. Do not add a privileged host-cgroup mount
as a workaround. Persistent lifecycle execution requires a separately governed
Windows runner or Linux runner with a narrowly delegated cgroup subtree. See
[Java Vuser Runtime](../docs/PROTOCOL_SUPPORT.md#java-vuser-execution) for the platform
and containment contract.

The sidecar separates those credentials from reviewed Java code; it is not a
sandbox for mutually untrusted scripts because it still shares the pod network.
