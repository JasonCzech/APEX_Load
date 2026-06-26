# APEX Load Fleet Management Review

## Summary

Fleet management was reviewed across controller APIs, generator lifecycle, local
Docker/local-kind/on-prem Kubernetes/AKS/Helm, and Azure Terraform. The governance
and quota model is strong, but the fleet control plane had several correctness gaps
that matter most under HA, autoscaling, and generator loss.

This document records the corrected findings used for the remediation work. It avoids
earlier false positives:

- Pool quotas have a UI.
- The Generator Impact chart already shows per-generator live resource impact.
- Core execution paths are leader-guarded when `APEXLOAD_CONTROLLER_HA=true`.
- Docker has a root `compose.yaml`, but it was controller + Prometheus only.
- Pool-scoped launches already have preflight validation for missing, suspended, and
  empty pools.
- KEDA scale-down stabilization is already 600 seconds in the KEDA manifests.

## P0 Findings

| ID | Finding | Risk | Remediation |
| --- | --- | --- | --- |
| FLEET-STATE | Dashboard cordon patched only generator metadata; placement consulted only scheduler drain state. | Admin sees a generator as cordoned while it still receives VUsers. | Reconcile metadata cordon into enforced drain state; inventory reflects enforced state. |
| FLEET-IDENTITY | Drain state was keyed by dial target, including DNS-discovered pod IPs. | Cordon/drain intent can disappear after pod recreation. | Canonicalize enforced state by stable `generator_id`, with legacy target-key dual-read. |
| FLEET-LEADER | Fleet mutators did not require the execution leader. | HA followers could mutate only local memory or race StatefulSet scaling. | Leader-gate drain, pool CRUD, metadata patch, provisioning, and restart mutators. |
| CAPACITY-TRUTH | Live capacity used `APEXLOAD_LIVE_AVAILABLE_GENERATORS`, which nothing maintained; generators always advertised `MaxVUsers=10000`. | Admission and estimates could be detached from the actual fleet. | Feed live inventory into admission and configure generator capacity per environment. |
| SCALE-IN | Generator pods used a blind `preStop: sleep 15`; generator shutdown did not reject new work or wait for active work. | Scale-in can kill active traffic mid-test. | Use a controller-side pod termination watcher, gRPC health, and generator draining on SIGTERM; reject new assignments and wait inside the 120s grace. |
| OWNER-GUARD | Kubernetes scaling ownership was ambiguous when the owner env var was missing. | Controller and KEDA can fight over StatefulSet replicas. | Require explicit `APEXLOAD_GENERATOR_AUTOSCALING_OWNER=controller` for controller-owned scaling; KEDA installs set `keda`. |

## P1 Findings

| ID | Finding | Remediation |
| --- | --- | --- |
| HA-DEPLOY | HA code existed but deploy surfaces lacked a supported toggle/invariant. | Add Helm and Kustomize HA surfaces that set replicas and `APEXLOAD_CONTROLLER_HA` together and require shared storage. |
| HEALTH | Probes were TCP-only and did not verify gRPC service readiness. | Register `grpc_health_v1` and switch generator probes to gRPC health. |
| RESILIENCE | Controller dials lacked keepalive/backoff settings. | Add bounded connect backoff and keepalive to the shared gRPC dial options. |
| GENERATOR-LOSS | A metrics-stream loss failed the entire distributed run. | Add opt-in `assignment.tolerate_generator_loss` for stateless groups, emit `fleet_degraded`, and reshard future/new VUsers onto surviving generators. Browser/JVM-style groups refuse live resharding. |
| EVACUATE | Admins had no dry-run/apply evacuation API for a busy generator. | Add `POST /fleet/generators/{id}/evacuate`, backed by enforced drain state and the stateless reshard primitive. |
| LIFECYCLE-API | Cordon/drain operations were split between metadata PATCH and a legacy system endpoint. | Add explicit leader-gated `POST /fleet/generators/{id}/cordon`, `/uncordon`, and `/drain` endpoints while keeping the legacy target-based drain API compatible. |
| PROVISIONING-HISTORY | `ListProvisioningRequests` existed in storage but was not exposed. | Wire `GET /fleet/provision-requests` and the dashboard hook. |
| FLEET-OPS | Fleet alerts and provisioning operations were not actionable from the admin surface. | Add persisted alert acknowledgements, configurable fleet capacity thresholds, webhook/Dynatrace routing for stable fleet-alert events, provisioning cancel/retry, WebSocket `fleet_event` refresh, and a first-class Fleet navigation entry. |
| FLEET-AUDIT | Admins had to leave the fleet context to inspect who changed generator/pool/provisioning state. | Add a Fleet Audit card backed by the existing audit-event API and filtered to fleet actions/resources. |
| DOCKER-FLEET | Docker compose did not run generators or configure distributed execution. | Add a generator service and controller DNS discovery. |
| LOCAL-DISCOVERY | Local/local-kind used hardcoded StatefulSet ordinals. | Use the same headless-Service `dns://` discovery path as AKS/on-prem so local scale events exercise discovery. |
| CAPACITY-VALIDATION | Terraform documented the KEDA/node-capacity risk but did not enforce it. | Add a parametric Terraform precondition tying KEDA max replicas to VM size, pod requests, node count, and reservation headroom. |
| DOC-DRIFT | Docs/manifests referred to HPA and unsafe reconcile behavior that is no longer accurate. | Update CLAUDE/AGENTS and K8s comments to KEDA and supported HA wording. |

## Backlog

- Native Kubernetes streaming watch for pod termination; the P0 implementation uses
  the existing restart-control pod-list seam as a conservative polling watcher.
- Full transient stream re-subscribe without evacuating a generator.
- Richer topology/drift visualizations.
- Async provisioning readiness wait and KEDA pre-warm operation state.
- Fleet history time series, topology map, earliest-fit planner, per-pool StatefulSets,
  cloud provisioners, scale-to-zero, image rollout controls, chargeback, and stronger
  tenant isolation.

## Verification Expectations

- Go unit tests for leader gating, identity-keyed drain, metadata cordon enforcement,
  pool validation, generator env config, gRPC health/draining behavior, and provisioning
  list.
- `go test ./...`.
- Dashboard typecheck and targeted hook/UI tests for any Fleet page work.
- `go generate ./pkg/api` after route changes, then OpenAPI freshness tests.
- `kustomize build` for touched overlays and `helm template` for the chart.
