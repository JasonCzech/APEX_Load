var e=`# Deployment Guide

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

## Validation Baseline

Before promoting a Kubernetes or AKS deployment, validate manifests and generated
configuration with the relevant toolchain:

\`\`\`bash
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
\`\`\`

The \`make k8s-*\` targets (\`k8s-local-*\`, \`k8s-kind-*\`, \`k8s-onprem-*\`, \`k8s-aks-*\`,
\`k8s-eks-*\`, and \`k8s-smoke-test\`) wrap the apply, status, and delete steps for
these overlays; see the \`Makefile\` for the exact commands.

## AI provider setup for local Kubernetes

Use this when you want APEX Load scripts running in the controller pod to read provider credentials from Kubernetes instead of your local shell.

The local controller deployment supports these optional environment variables:

- \`OPENAI_API_KEY\`
- \`OPENAI_MODEL\`
- \`XAI_API_KEY\`
- \`XAI_MODEL\`

If you do not create the matching Kubernetes resources, the controller still starts normally, but scripts using those variables will fail at runtime.

### Grok / xAI

Create the secret and default model config:

\`\`\`bash
kubectl -n apexload-local create secret generic apexload-xai-secrets \\
  --from-literal=XAI_API_KEY=REPLACE_WITH_XAI_API_KEY \\
  --dry-run=client -o yaml | kubectl apply -f -

kubectl -n apexload-local create configmap apexload-xai-settings \\
  --from-literal=XAI_MODEL=grok-4.3 \\
  --dry-run=client -o yaml | kubectl apply -f -
\`\`\`

### OpenAI

Create the secret and default model config:

\`\`\`bash
kubectl -n apexload-local create secret generic apexload-openai-secrets \\
  --from-literal=OPENAI_API_KEY=REPLACE_WITH_OPENAI_API_KEY \\
  --dry-run=client -o yaml | kubectl apply -f -

kubectl -n apexload-local create configmap apexload-openai-settings \\
  --from-literal=OPENAI_MODEL=REPLACE_WITH_OPENAI_MODEL \\
  --dry-run=client -o yaml | kubectl apply -f -
\`\`\`

### Reload The Controller

After creating or updating either secret or configmap, restart the controller so the pod picks up the new values:

\`\`\`bash
kubectl -n apexload-local rollout restart deployment/apexload-controller
kubectl -n apexload-local rollout status deployment/apexload-controller
\`\`\`

### Verify The Environment

You can confirm a non-secret model variable is present with:

\`\`\`bash
kubectl -n apexload-local exec deploy/apexload-controller -- printenv XAI_MODEL
kubectl -n apexload-local exec deploy/apexload-controller -- printenv OPENAI_MODEL
\`\`\`

Do not print the API key values back out of the pod.

## Kubernetes monitoring stack

These manifests are production-oriented examples for Phase 3 of the enterprise data platform upgrade.

APEX Load keeps exposing \`/metrics\` from the controller. Prometheus scrapes that endpoint for live operational metrics, then remote-writes to the enterprise retention backend:

- Grafana Mimir
- Thanos Receive
- A managed Prometheus-compatible remote-write endpoint

Use these examples as overlays, not as local-kind defaults. \`APEXLOAD_PROMETHEUS_REMOTE_WRITE_URL\` is consumed by the external Prometheus configuration in \`deployments/k8s/monitoring\`; the APEX Load controller remains scrape-only and does not remote-write samples itself.

### Files

- \`servicemonitor.yaml\` - Prometheus Operator scrape definition for the controller.
- \`prometheus-remote-write-example.yaml\` - example Prometheus config with remote-write destinations.
- \`prometheusrule.yaml\` - starter alert rules for scrape absence, telemetry staleness, telemetry drops, and WebSocket fan-out drops.

### Required Labels

The controller metrics endpoint should preserve bounded labels suitable for retention:

- \`environment\`
- \`domain\`
- \`project\`
- \`release\`
- \`scenario\`
- \`run\`
- \`protocol\`
- \`generator_region\`
- generator identity is intentionally kept out of controller \`/metrics\`; use Kubernetes pod labels, cAdvisor, and kube-state-metrics for per-generator host telemetry.

High-cardinality raw transaction details stay in APEX Load archive bundles instead of Prometheus.

## Browser generator for observer pools

Browser observers measure what a user sees, so they run the Chromium build the
Playwright driver was tested with. Each release publishes
\`apexload-browser-generator\` (standard profile only, tag \`<release>-standard\`):
the generator on Debian with Playwright's own Chromium, built from
\`Dockerfile.browser-generator\`. The controller image carries the same build for
recording and Verify. The default \`apexload-generator\` image stays the lean
Alpine build with Alpine's Chromium; use it for protocol-only pools.

Deploy an observer pool as a second generator from the Helm chart with
\`image.generatorRepository=apexload-browser-generator\` and the \`-standard\` tag,
pinned to the release digest, and register its generators in their own fleet
pool. The AKS deployment workflow deploys the protocol generator only. Image
builds download Chromium from Playwright's CDN; set the
\`PLAYWRIGHT_DOWNLOAD_HOST\` build argument to use a mirror.

## Java Vuser runner component

This opt-in component adds the loopback-only, credential-minimized Java Vuser
runner to an APEX Load generator pod (\`deployments/k8s/components/java-vuser-runner\`).
Add it to a local, on-prem, or AKS overlay:

\`\`\`yaml
components:
  - ../components/java-vuser-runner
\`\`\`

The published image includes JDK 21 and a Java 17-compatible harness. User
classes must be precompiled into a signed custom runner image, or supplied by a
separate read-only volume patch. Do not mount generator state, gRPC credentials,
service-account tokens, or workload-identity files into this container.

This stock non-privileged component does not receive writable cgroup v2
delegation. It therefore supports legacy \`full_lifecycle\` execution only;
combined persistent Java imports fail sidecar capability preflight with
\`persistent_lifecycle_unavailable\`. Do not add a privileged host-cgroup mount
as a workaround. Persistent lifecycle execution requires a separately governed
Windows runner or Linux runner with a narrowly delegated cgroup subtree. See
[Java Vuser Runtime](../docs/PROTOCOL_SUPPORT.md#java-vuser-execution) for the platform
and containment contract.

The sidecar separates those credentials from reviewed Java code; it is not a
sandbox for mutually untrusted scripts because it still shares the pod network.
`;export{e as default};