var e=`# APEX Load on Amazon EKS — deployment guide

## Overview

Production-grade manifests for running APEX Load on EKS with the full AWS data
platform: RDS PostgreSQL, ElastiCache Redis, S3 archives, Secrets Manager script
secrets (IRSA — no static keys), a KEDA-autoscaled generator fleet, and
DNS-based fleet discovery.

Controller-loss fencing is mandatory. Follow the maintenance-window rollout
gate and acceptance drill in [Generator restart controls](../../../docs/CLUSTERS_AND_CLOUD.md#kubernetes-restart-controls)
when upgrading from a tokenless release.

The matching infrastructure lives in \`deployments/aws/terraform\` (see [AWS Terraform](#aws-terraform)).

\`\`\`
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
\`\`\`

## How the AWS pieces map to APEX Load config

| AWS service | Env vars (set in ConfigMap/Secret here) | Auth |
|---|---|---|
| RDS PostgreSQL | \`APEXLOAD_STORE=postgres\`, \`APEXLOAD_DATABASE_URL\` | password in Secret |
| ElastiCache Redis | \`APEXLOAD_CACHE_BACKEND=redis\`, \`APEXLOAD_REDIS_URL\` (rediss://, port 6379) | auth token in Secret |
| S3 | \`APEXLOAD_ARCHIVE_BACKEND=s3\`, \`APEXLOAD_ARCHIVE_BUCKET\`, \`AWS_REGION\` | controller/generator IRSA |
| Secrets Manager | \`APEXLOAD_AWS_SECRETS_MANAGER_REGION\`, controller-only \`APEXLOAD_AWS_SECRETS_MANAGER_WRITE_ENABLED=true\` | controller rotates; generator reads |
| KMS | \`APEXLOAD_WRAPPING_KMS_KEY_ID\` (full key ARN) | controller IRSA |

## Apply

Never \`kubectl apply -k\` this directory directly: it carries \`CHANGE-ME\` values
and \`\${PLACEHOLDER}\`s that only the render script substitutes, and the script
refuses to emit a manifest that still contains any.

\`\`\`bash
make k8s-eks-apply
\`\`\`

## The two things that differ most from AKS

**1. The generator node-pool label.** An EKS managed node group labels its nodes
\`eks.amazonaws.com/nodegroup\`, and Karpenter uses \`karpenter.sh/nodepool\`.
Neither is the generic \`agentpool\` key that AKS nodes also carry. The allocation
editor therefore records the label KEY alongside the pool name; if it wrote the
generic key, the generator pods would match no node and sit \`Pending\` forever.

**2. Authentication is two-stage.** The controller presigns an STS
\`GetCallerIdentity\` URL as its bearer token. STS accepting that credential and
the cluster authorizing the resulting ARN are separate things, and both fail
with \`401\`. Use **Settings → Kubernetes clusters → Test**: the \`sts_identity\` probe reports the
caller ARN, and when it passes while \`api_server\` fails, the message names the
missing EKS access entry directly.

## Deployment controls and optional add-ons

| File | Purpose |
|---|---|
| \`restart-controls.yaml\` | Included by default: namespaced Role for pod restart, workload scaling, and ResourceQuota reads. Generator pods receive no Kubernetes API token or restart RBAC; IRSA may separately inject an AWS STS-audience token. |
| \`node-read.optional.yaml\` | Cluster-scoped node read for node-pool discovery and pod-fit checks. Opt-in; the capacity probe degrades gracefully without it. |
| \`../eks-ha\` | Multi-replica controller with a single execution owner. Uses \`efs-sc\` for the ReadWriteMany import-assets volume. |
| \`../eks-prometheus\` | kube-prometheus-stack ServiceMonitor wiring. |

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
| \`kubectl kustomize deployments/k8s/eks\` | Passed; the overlay renders (748 lines). |
| \`kubectl kustomize deployments/k8s/eks-ha\`, \`deployments/k8s/eks-prometheus\` | Passed. |
| \`terraform fmt\` + \`terraform validate\` in \`deployments/aws/terraform\` | Passed with \`hashicorp/aws ~> 5.60\`. |
| \`go test ./deployments/k8s\` | Passed, including the EKS twins of every AKS render invariant. |
| \`go test ./pkg/kubeops ./pkg/api ./pkg/storage ./pkg/keywrap ./pkg/secretstore\` | Passed. |
| EKS bearer-token wire format | Verified **offline** by \`TestAwsIAMTokenFormatMatchesEKSWebhookContract\`: presigning makes no network call, so the real signed URL is asserted in CI. |

**Not verified in CI:** the presigned URL has never been presented to a real EKS
authentication webhook from this repository. The signature math comes from the
AWS SDK rather than hand-rolled code, and every property the webhook documents
is asserted, but end-to-end acceptance is a manual release gate (gate 6 below).

### Integration Matrix

| Area | Current state | EKS migration need |
| --- | --- | --- |
| Cluster authentication | \`ClusterConnection\` kind \`eks\` with \`aws_iam\` auth. The bearer token is a presigned STS \`GetCallerIdentity\` URL carrying the cluster name as a signed header. | Prefer IRSA / EKS Pod Identity (\`ambient\`). Static keys and \`assume_role\` exist for cross-account cases. |
| Credential storage | Access key and role external ID are held as separate keywrap envelopes, never in plaintext, each bound to its connection and slot. | Configure a wrapping key before registering any connection; writes fail closed with 503 otherwise. |
| Envelope wrapping | AWS KMS with an authenticated encryption context, pinned by full key ARN. | Set exactly one of \`APEXLOAD_WRAPPING_KMS_KEY_ID\` / \`APEXLOAD_WRAPPING_KEY_VAULT_KEY_ID\`; both is refused at startup. |
| Generator fleet | StatefulSet, headless service, DNS discovery, KEDA scale-out, mTLS/token gRPC — unchanged from AKS. | Verify scale-out/scale-in on EKS and keep cert provisioning automated. |
| Node placement | \`NodeRef.AgentPool()\` reads \`eks.amazonaws.com/nodegroup\`, then \`karpenter.sh/nodepool\`. The allocation carries the label KEY alongside the pool name. | Pin pools through the allocation editor; the generic \`agentpool\` key matches no EKS node. |
| Metadata | RDS PostgreSQL, Multi-AZ, encrypted with a customer managed key. | Use PostgreSQL for shared environments; SQLite stays local-only. |
| Live state | ElastiCache Redis with transit and at-rest encryption and automatic failover. | Required for multi-controller or HA deployments. |
| Archives | S3 through Go CDK, SSE-KMS enforced by bucket policy, versioned. | Use IRSA; keep local staging disposable. |
| Secrets | AWS Secrets Manager resolver via IRSA, scoped to one secret path. | Controller rotates, generator reads. Never grant generators \`PutSecretValue\`. |
| Identity | Split controller/generator IRSA roles with distinct policies. | Keep them split; a shared role hands script-executing pods rotation authority. |
| Observability | Prometheus metrics, ServiceMonitor, KEDA Prometheus scaler; CloudWatch log groups with governed retention. | Either kube-prometheus-stack or Amazon Managed Service for Prometheus. |
| Domain deployment targets | A superuser constrains each domain to Kubernetes, Azure AKS, or AWS EKS. Enforced at pool write time and again, fail-closed, at scale time. | Set a target per domain before onboarding tenants; unscoped pools keep pre-existing behavior. |

### Production Migration Gates

An EKS environment is ready for cutover only when all gates below pass:

1. \`scripts/render-eks-manifests.sh\` renders without placeholders.
2. Terraform validates with the committed lockfile, and the generator capacity
   check passes at plan time (\`generator_schedulable_pods >=
   generator_keda_max_replicas\`).
3. \`kubectl kustomize\` passes for \`eks\`, \`eks-ha\`, and \`eks-prometheus\`.
4. \`/api/v1/system/readiness\` is \`ready\` or has only approved \`degraded\` checks,
   and \`platform_integrations.wrapping_key.conflict\` is \`false\`.
5. The controller reports \`metadata_store=postgres\`, \`cache_backend=redis\`, S3
   archive details, \`aws_secrets_manager_configured=true\`, and a secured
   \`grpc_transport\`.
6. **The cluster-connection test passes against the real cluster.** \`sts_identity\`
   is green and reports the caller ARN; \`api_server\` is green. This is the gate
   that covers what CI structurally cannot: acceptance of the presigned token by
   a live EKS authentication webhook.
7. A fleet pool bound to the EKS cluster provisions, and a node-pool pinned
   allocation schedules — pods must reach \`Running\`, not \`Pending\`.
8. KEDA scale-out and scale-in are observed while the controller tracks
   generators through \`dns://\` discovery.
9. A smoke test creates a script, queues a distributed run, streams metrics,
   writes an S3 archive, and downloads the report.
10. OIDC or trusted-header auth is configured before exposing the ingress
    outside the platform network.
11. The HA gates from the [AKS integration readiness](../aks/README.md#integration-readiness) section (single execution owner, lease
    generations, failover evidence) pass unchanged.

### Known Limitations

- **Clock skew is fatal and non-obvious.** The presigned token embeds
  \`X-Amz-Date\`; more than 15 minutes of controller drift makes every token
  401 with no other symptom. The \`sts_identity\` probe surfaces
  \`RequestTimeTooSkewed\`, and the \`api_server\` probe names clock skew as a
  possible cause of a 401.
- **An unmapped caller ARN looks like a bad credential.** STS accepting the
  credential and the API server rejecting it are different failures. The probe
  pair distinguishes them; without it, both present as \`401 Unauthorized\`.
- **PrivateLink hostnames are not accepted.** The STS host allow-list requires
  \`sts[.region].amazonaws.com\`; enable private DNS on the VPC endpoint (this
  module does) so the standard name resolves privately. A raw \`vpce-*\` host is
  refused by design — a presigned URL is a replayable credential, so the
  endpoint cannot be an arbitrary operator-supplied host.
- **IMDS is disabled by default** in ambient mode. IMDS and EKS Pod Identity are
  link-local addresses, which the cluster dial guard blocks; Pod Identity is
  resolved through the container credentials provider instead.
- **The HA overlay needs two manual add-on steps.** AKS ships an
  \`azurefile-csi\` StorageClass; EKS ships no ReadWriteMany class at all.
  Terraform provisions the EFS filesystem and its mount targets, but the EFS CSI
  driver and the \`efs-sc\` StorageClass must be installed separately — see the
  prerequisite block in \`deployments/k8s/eks-ha/kustomization.yaml\`. Skip them
  and the import-assets PVC stays \`Pending\`. The single-controller deployment is
  unaffected.
- **The ALB idle timeout is load-bearing.** The dashboard streams live metrics
  over WebSockets, and an ALB defaults to a 60-second idle timeout. The
  \`idle_timeout.timeout_seconds=3600\` annotation on the ingress is what keeps
  streams alive; nginx-style \`proxy-read-timeout\` annotations are silently
  ignored by the ALB controller.
- **An ingress controller is not included.** EKS ships none; install the AWS
  Load Balancer Controller before applying the overlay.
- **Overlay scope vs. ECS Fargate compute.** This Kustomize overlay provisions
  an EKS cluster scaled over the Kubernetes API (not an EC2 Auto Scaling Group).
  For serverless AWS ECS Fargate generator pools, see
  [Multi-cloud PaaS compute](../../../docs/CLUSTERS_AND_CLOUD.md#multi-cloud-paas-compute).
- **Event Hubs / Service Bus have no AWS twin here.** They remain supported
  protocol targets, but the module provisions nothing for them.

### Evidence Locations

| Evidence | Location |
| --- | --- |
| Token wire format | \`pkg/kubeops/auth_aws_test.go\` |
| STS endpoint allow-list | \`TestAwsSTSEndpointMustBeAnAllowedSTSHost\` |
| No ambient credential fallback | \`TestAwsIAMTokenSourceNeverFallsBackToAmbientCredentials\` |
| KMS encryption-context binding | \`pkg/keywrap/wrapping_aws_test.go\` |
| Credential non-serialization | \`pkg/storage/sqlite_cluster_connections_eks_test.go\` |
| Domain constraint fail-closed | \`pkg/api/fleet_domain_target_test.go\` |
| Overlay invariants | \`deployments/k8s/deployment_artifacts_test.go\` |
| Terraform capacity check | \`TestTerraformValidatesEKSGeneratorCapacity\` |

## AWS Terraform

Provisions the AWS side of an APEX Load deployment: an EKS cluster with separate
controller and generator node groups, RDS PostgreSQL, ElastiCache Redis, an S3
archive bucket, ECR repositories, two KMS keys, and the IRSA roles the workloads
authenticate with.

This is the AWS twin of \`deployments/azure/terraform\`. The Kubernetes manifests
live in \`deployments/k8s/eks\` and are rendered from these outputs by
\`scripts/render-eks-manifests.sh\`.

### Usage

\`\`\`bash
terraform -chdir=deployments/aws/terraform init -backend-config=backend.hcl
terraform -chdir=deployments/aws/terraform plan  -var-file=terraform.tfvars
terraform -chdir=deployments/aws/terraform apply -var-file=terraform.tfvars

# Render and apply the manifests with these outputs substituted.
make k8s-eks-apply
\`\`\`

### Output → manifest wiring

\`scripts/render-eks-manifests.sh\` consumes these output names exactly. Renaming
one without updating the script leaves a \`CHANGE-ME\` in the rendered manifest,
which the script then refuses to emit.

| Terraform output | Lands in |
|---|---|
| \`aws_region\` | \`AWS_REGION\`, \`APEXLOAD_AWS_SECRETS_MANAGER_REGION\` |
| \`ecr_controller_repository_url\` | \`kustomization.yaml\` controller image |
| \`ecr_generator_repository_url\` | \`kustomization.yaml\` generator image |
| \`archive_bucket_name\` | \`APEXLOAD_ARCHIVE_BUCKET\` |
| \`wrapping_kms_key_arn\` | \`APEXLOAD_WRAPPING_KMS_KEY_ID\` |
| \`controller_irsa_role_arn\` | \`eks.amazonaws.com/role-arn\` on the controller SA |
| \`generator_irsa_role_arn\` | \`eks.amazonaws.com/role-arn\` on the generator SA |

\`database_url_secret_arn\` and \`redis_url_secret_arn\` are Secrets Manager ARNs,
not values. The credentials are generated here and written to Secrets Manager
deliberately: emitting them as outputs would put them in plaintext in the state
file and in any CI log that prints outputs. Sync them into the
\`apexload-data-platform\` Secret with the Secrets Store CSI driver, or with
\`aws secretsmanager get-secret-value\` in a controlled step.

### Registering the cluster in APEX Load

After apply, register the cluster under **Settings → Kubernetes clusters** (\`#/settings?setting=clusters\`):

| Field | Value |
|---|---|
| Provider | Amazon EKS |
| API server URL | \`terraform output eks_cluster_endpoint\` |
| AWS region | \`terraform output aws_region\` |
| EKS cluster name | \`terraform output eks_cluster_name\` |
| AWS credentials | Workload identity (IRSA) |
| TLS trust | Host system roots |

Then press **Test**. The \`sts_identity\` probe reports the exact caller ARN the
cluster sees. If \`sts_identity\` passes but \`api_server\` returns 401/403, that
ARN authenticates but is not authorized.

Authentication and authorization are **two separate steps**, and this module
only does the first:

1. The access entry it creates maps the controller role into the cluster as a
   member of the \`apexload-controllers\` group.
2. That group still has no permissions until a RoleBinding grants them. Apply
   \`deployments/k8s/eks/restart-controls.yaml\` **and**
   \`cluster-access.example.yaml\` to bind the least-privilege Role to the group.

No AWS-managed access policy is attached deliberately: \`AmazonEKSAdminPolicy\`
and friends are wildly over-privileged for a controller that only needs to scale
one StatefulSet.

This applies only to a **remote** registration — a controller elsewhere driving
this cluster. A controller running *inside* this cluster registers it as kind
\`in_cluster\` and authorizes through its own ServiceAccount, so neither step
applies.

### Notes on specific choices

- **Two KMS keys.** The wrapping key seals credential envelopes; the storage key
  encrypts RDS/S3/ElastiCache/CloudWatch. Keeping them separate means a
  storage-key policy change cannot make every stored credential unreadable.
- **The wrapping key is pinned by ARN, never alias.** An alias can be repointed
  at a different key, which would strand existing ciphertext permanently.
- **Set exactly one wrapping key.** Configuring both
  \`APEXLOAD_WRAPPING_KMS_KEY_ID\` and \`APEXLOAD_WRAPPING_KEY_VAULT_KEY_ID\` is
  refused at startup rather than resolved silently.
- **Split IRSA roles.** The controller may rotate project secrets; the generator
  may only read them. Generators execute user-supplied scripts, so rotation
  authority there would let a script rewrite the credentials other scripts read.
- **STS VPC endpoint with private DNS.** The controller's EKS bearer token is a
  presigned STS URL — a replayable credential. Private DNS keeps it resolving as
  \`sts.<region>.amazonaws.com\`, which is what the controller's STS host
  allow-list accepts; a raw \`vpce-*\` hostname would be refused.
- **Generator capacity is checked at plan time.** KEDA will request more
  replicas than the node group can schedule; the surplus pods sit \`Pending\` and
  the test silently runs under its declared concurrency.

### Not provisioned

Azure Event Hubs and Service Bus have no AWS twin here. They remain supported
protocol *targets* from an EKS deployment — that is a load-test payload concern,
not infrastructure — but nothing in this module creates them.
`;export{e as default};