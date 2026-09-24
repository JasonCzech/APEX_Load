# APEX Load on Amazon EKS — deployment guide

Production-grade manifests for running APEX Load on EKS with the full AWS data
platform: RDS PostgreSQL, ElastiCache Redis, S3 archives, Secrets Manager script
secrets (IRSA — no static keys), a KEDA-autoscaled generator fleet, and
DNS-based fleet discovery.

Controller-loss fencing is mandatory. Follow the maintenance-window rollout
gate and acceptance drill in [Generator run supervision](../../../docs/ENTERPRISE_FLEET_AND_CAPACITY.md#generator-run-supervision)
when upgrading from a tokenless release.

The matching infrastructure lives in [`deployments/aws/terraform`](../../aws/terraform/README.md).

```
                        ┌─ EKS (apexload namespace) ─────────────────────┐
  users ── Ingress ──▶  │  controller ×1 active (Deployment)             │
  (AWS LB Controller)   │     │ gRPC, dns:// discovery of headless svc   │
                        │  generator ×2–50 (StatefulSet + KEDA)          │
                        │     └ dedicated "generator" node group         │
                        └──────┬───────────┬───────────┬───────────┬─────┘
                               ▼           ▼           ▼           ▼
                           RDS         ElastiCache      S3      Secrets Mgr
                        PostgreSQL    (live state,   (run       ({{secret.*}},
                        (system of     WS fanout)     archives)   IRSA)
                         record)
```

## How the AWS pieces map to APEX Load config

| AWS service | Env vars (set in ConfigMap/Secret here) | Auth |
|---|---|---|
| RDS PostgreSQL | `APEXLOAD_STORE=postgres`, `APEXLOAD_DATABASE_URL` | password in Secret |
| ElastiCache Redis | `APEXLOAD_CACHE_BACKEND=redis`, `APEXLOAD_REDIS_URL` (rediss://, port 6379) | auth token in Secret |
| S3 | `APEXLOAD_ARCHIVE_BACKEND=s3`, `APEXLOAD_ARCHIVE_BUCKET`, `AWS_REGION` | controller/generator IRSA |
| Secrets Manager | `APEXLOAD_AWS_SECRETS_MANAGER_REGION`, controller-only `APEXLOAD_AWS_SECRETS_MANAGER_WRITE_ENABLED=true` | controller rotates; generator reads |
| KMS | `APEXLOAD_WRAPPING_KMS_KEY_ID` (full key ARN) | controller IRSA |

## Apply

Never `kubectl apply -k` this directory directly: it carries `CHANGE-ME` values
and `${PLACEHOLDER}`s that only the render script substitutes, and the script
refuses to emit a manifest that still contains any.

```bash
make k8s-eks-apply
```

## The two things that differ most from AKS

**1. The generator node-pool label.** An EKS managed node group labels its nodes
`eks.amazonaws.com/nodegroup`, and Karpenter uses `karpenter.sh/nodepool`.
Neither is the generic `agentpool` key that AKS nodes also carry. The allocation
editor therefore records the label KEY alongside the pool name; if it wrote the
generic key, the generator pods would match no node and sit `Pending` forever.

**2. Authentication is two-stage.** The controller presigns an STS
`GetCallerIdentity` URL as its bearer token. STS accepting that credential and
the cluster authorizing the resulting ARN are separate things, and both fail
with `401`. Use **Fleet → Clusters → Test**: the `sts_identity` probe reports the
caller ARN, and when it passes while `api_server` fails, the message names the
missing EKS access entry directly.

## Deployment controls and optional add-ons

| File | Purpose |
|---|---|
| `restart-controls.yaml` | Included by default: namespaced Role for pod restart, workload scaling, and ResourceQuota reads. Generator pods receive no Kubernetes API token or restart RBAC; IRSA may separately inject an AWS STS-audience token. |
| `node-read.optional.yaml` | Cluster-scoped node read for node-pool discovery and pod-fit checks. Opt-in; the capacity probe degrades gracefully without it. |
| `../eks-ha` | Multi-replica controller with a single execution owner. Uses `efs-sc` for the ReadWriteMany import-assets volume. |
| `../eks-prometheus` | kube-prometheus-stack ServiceMonitor wiring. |

## Integration readiness

APEX Load has enough EKS plumbing for a pilot deployment and an enterprise
migration rehearsal, but it is not yet a hands-off production migration target.
It mirrors the AKS path deliberately: the same controller drives a remote
Kubernetes API server, and only the authentication, storage, and secret
providers differ. Everything said in the [AKS integration readiness](../aks/README.md#integration-readiness)
section about HA being an opt-in, single-execution-owner capability rather than
production-validated active-active execution applies here unchanged.

### Verified Baseline

The following checks were run against the repository during the EKS
implementation pass:

| Check | Result |
| --- | --- |
| `kubectl kustomize deployments/k8s/eks` | Passed; the overlay renders (748 lines). |
| `kubectl kustomize deployments/k8s/eks-ha`, `deployments/k8s/eks-prometheus` | Passed. |
| `terraform fmt` + `terraform validate` in `deployments/aws/terraform` | Passed with `hashicorp/aws ~> 5.60`. |
| `go test ./deployments/k8s` | Passed, including the EKS twins of every AKS render invariant. |
| `go test ./pkg/kubeops ./pkg/api ./pkg/storage ./pkg/keywrap ./pkg/secretstore` | Passed. |
| EKS bearer-token wire format | Verified **offline** by `TestAwsIAMTokenFormatMatchesEKSWebhookContract`: presigning makes no network call, so the real signed URL is asserted in CI. |

**Not verified in CI:** the presigned URL has never been presented to a real EKS
authentication webhook from this repository. The signature math comes from the
AWS SDK rather than hand-rolled code, and every property the webhook documents
is asserted, but end-to-end acceptance is a manual release gate (gate 6 below).

### Integration Matrix

| Area | Current state | EKS migration need |
| --- | --- | --- |
| Cluster authentication | `ClusterConnection` kind `eks` with `aws_iam` auth. The bearer token is a presigned STS `GetCallerIdentity` URL carrying the cluster name as a signed header. | Prefer IRSA / EKS Pod Identity (`ambient`). Static keys and `assume_role` exist for cross-account cases. |
| Credential storage | Access key and role external ID are held as separate keywrap envelopes, never in plaintext, each bound to its connection and slot. | Configure a wrapping key before registering any connection; writes fail closed with 503 otherwise. |
| Envelope wrapping | AWS KMS with an authenticated encryption context, pinned by full key ARN. | Set exactly one of `APEXLOAD_WRAPPING_KMS_KEY_ID` / `APEXLOAD_WRAPPING_KEY_VAULT_KEY_ID`; both is refused at startup. |
| Generator fleet | StatefulSet, headless service, DNS discovery, KEDA scale-out, mTLS/token gRPC — unchanged from AKS. | Verify scale-out/scale-in on EKS and keep cert provisioning automated. |
| Node placement | `NodeRef.AgentPool()` reads `eks.amazonaws.com/nodegroup`, then `karpenter.sh/nodepool`. The allocation carries the label KEY alongside the pool name. | Pin pools through the allocation editor; the generic `agentpool` key matches no EKS node. |
| Metadata | RDS PostgreSQL, Multi-AZ, encrypted with a customer managed key. | Use PostgreSQL for shared environments; SQLite stays local-only. |
| Live state | ElastiCache Redis with transit and at-rest encryption and automatic failover. | Required for multi-controller or HA deployments. |
| Archives | S3 through Go CDK, SSE-KMS enforced by bucket policy, versioned. | Use IRSA; keep local staging disposable. |
| Secrets | AWS Secrets Manager resolver via IRSA, scoped to one secret path. | Controller rotates, generator reads. Never grant generators `PutSecretValue`. |
| Identity | Split controller/generator IRSA roles with distinct policies. | Keep them split; a shared role hands script-executing pods rotation authority. |
| Observability | Prometheus metrics, ServiceMonitor, KEDA Prometheus scaler; CloudWatch log groups with governed retention. | Either kube-prometheus-stack or Amazon Managed Service for Prometheus. |
| Domain deployment targets | A superuser constrains each domain to Kubernetes, Azure AKS, or AWS EKS. Enforced at pool write time and again, fail-closed, at scale time. | Set a target per domain before onboarding tenants; unscoped pools keep pre-existing behavior. |

### Production Migration Gates

An EKS environment is ready for cutover only when all gates below pass:

1. `scripts/render-eks-manifests.sh` renders without placeholders.
2. Terraform validates with the committed lockfile, and the generator capacity
   check passes at plan time (`generator_schedulable_pods >=
   generator_keda_max_replicas`).
3. `kubectl kustomize` passes for `eks`, `eks-ha`, and `eks-prometheus`.
4. `/api/v1/system/readiness` is `ready` or has only approved `degraded` checks,
   and `platform_integrations.wrapping_key.conflict` is `false`.
5. The controller reports `metadata_store=postgres`, `cache_backend=redis`, S3
   archive details, `aws_secrets_manager_configured=true`, and a secured
   `grpc_transport`.
6. **The cluster-connection test passes against the real cluster.** `sts_identity`
   is green and reports the caller ARN; `api_server` is green. This is the gate
   that covers what CI structurally cannot: acceptance of the presigned token by
   a live EKS authentication webhook.
7. A fleet pool bound to the EKS cluster provisions, and a node-pool pinned
   allocation schedules — pods must reach `Running`, not `Pending`.
8. KEDA scale-out and scale-in are observed while the controller tracks
   generators through `dns://` discovery.
9. A smoke test creates a script, queues a distributed run, streams metrics,
   writes an S3 archive, and downloads the report.
10. OIDC or trusted-header auth is configured before exposing the ingress
    outside the platform network.
11. The HA gates from the [AKS integration readiness](../aks/README.md#integration-readiness) section (single execution owner, lease
    generations, failover evidence) pass unchanged.

### Known Limitations

- **Clock skew is fatal and non-obvious.** The presigned token embeds
  `X-Amz-Date`; more than 15 minutes of controller drift makes every token
  401 with no other symptom. The `sts_identity` probe surfaces
  `RequestTimeTooSkewed`, and the `api_server` probe names clock skew as a
  possible cause of a 401.
- **An unmapped caller ARN looks like a bad credential.** STS accepting the
  credential and the API server rejecting it are different failures. The probe
  pair distinguishes them; without it, both present as `401 Unauthorized`.
- **PrivateLink hostnames are not accepted.** The STS host allow-list requires
  `sts[.region].amazonaws.com`; enable private DNS on the VPC endpoint (this
  module does) so the standard name resolves privately. A raw `vpce-*` host is
  refused by design — a presigned URL is a replayable credential, so the
  endpoint cannot be an arbitrary operator-supplied host.
- **IMDS is disabled by default** in ambient mode. IMDS and EKS Pod Identity are
  link-local addresses, which the cluster dial guard blocks; Pod Identity is
  resolved through the container credentials provider instead.
- **The HA overlay needs two manual add-on steps.** AKS ships an
  `azurefile-csi` StorageClass; EKS ships no ReadWriteMany class at all.
  Terraform provisions the EFS filesystem and its mount targets, but the EFS CSI
  driver and the `efs-sc` StorageClass must be installed separately — see the
  prerequisite block in `deployments/k8s/eks-ha/kustomization.yaml`. Skip them
  and the import-assets PVC stays `Pending`. The single-controller deployment is
  unaffected.
- **The ALB idle timeout is load-bearing.** The dashboard streams live metrics
  over WebSockets, and an ALB defaults to a 60-second idle timeout. The
  `idle_timeout.timeout_seconds=3600` annotation on the ingress is what keeps
  streams alive; nginx-style `proxy-read-timeout` annotations are silently
  ignored by the ALB controller.
- **An ingress controller is not included.** EKS ships none; install the AWS
  Load Balancer Controller before applying the overlay.
- **No AWS-native compute provisioner.** Like the Azure path, this is EKS over
  the Kubernetes API. There is no EC2 Auto Scaling Group or ECS/Fargate
  generator backend; `GeneratorProvisioner` remains the seam for that work.
- **Event Hubs / Service Bus have no AWS twin here.** They remain supported
  protocol targets, but the module provisions nothing for them.

### Evidence Locations

| Evidence | Location |
| --- | --- |
| Token wire format | `pkg/kubeops/auth_aws_test.go` |
| STS endpoint allow-list | `TestAwsSTSEndpointMustBeAnAllowedSTSHost` |
| No ambient credential fallback | `TestAwsIAMTokenSourceNeverFallsBackToAmbientCredentials` |
| KMS encryption-context binding | `pkg/keywrap/wrapping_aws_test.go` |
| Credential non-serialization | `pkg/storage/sqlite_cluster_connections_eks_test.go` |
| Domain constraint fail-closed | `pkg/api/fleet_domain_target_test.go` |
| Overlay invariants | `deployments/k8s/deployment_artifacts_test.go` |
| Terraform capacity check | `TestTerraformValidatesEKSGeneratorCapacity` |
