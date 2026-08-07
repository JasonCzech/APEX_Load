# APEX Load on Amazon EKS — deployment guide

Production-grade manifests for running APEX Load on EKS with the full AWS data
platform: RDS PostgreSQL, ElastiCache Redis, S3 archives, Secrets Manager script
secrets (IRSA — no static keys), a KEDA-autoscaled generator fleet, and
DNS-based fleet discovery.

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

## Optional add-ons

| File | Purpose |
|---|---|
| `restart-controls.example.yaml` | Namespaced Role for pod restart, workload scaling, and ResourceQuota reads. Opt-in; not in `kustomization.yaml`. |
| `node-read.optional.yaml` | Cluster-scoped node read for node-pool discovery and pod-fit checks. Opt-in; the capacity probe degrades gracefully without it. |
| `../eks-ha` | Multi-replica controller with a single execution owner. Uses `efs-sc` for the ReadWriteMany import-assets volume. |
| `../eks-prometheus` | kube-prometheus-stack ServiceMonitor wiring. |
