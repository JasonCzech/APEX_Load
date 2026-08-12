# APEX Load on AWS — Terraform

Provisions the AWS side of an APEX Load deployment: an EKS cluster with separate
controller and generator node groups, RDS PostgreSQL, ElastiCache Redis, an S3
archive bucket, ECR repositories, two KMS keys, and the IRSA roles the workloads
authenticate with.

This is the AWS twin of `deployments/azure/terraform`. The Kubernetes manifests
live in `deployments/k8s/eks` and are rendered from these outputs by
`scripts/render-eks-manifests.sh`.

## Usage

```bash
terraform -chdir=deployments/aws/terraform init -backend-config=backend.hcl
terraform -chdir=deployments/aws/terraform plan  -var-file=terraform.tfvars
terraform -chdir=deployments/aws/terraform apply -var-file=terraform.tfvars

# Render and apply the manifests with these outputs substituted.
make k8s-eks-apply
```

## Output → manifest wiring

`scripts/render-eks-manifests.sh` consumes these output names exactly. Renaming
one without updating the script leaves a `CHANGE-ME` in the rendered manifest,
which the script then refuses to emit.

| Terraform output | Lands in |
|---|---|
| `aws_region` | `AWS_REGION`, `APEXLOAD_AWS_SECRETS_MANAGER_REGION` |
| `ecr_controller_repository_url` | `kustomization.yaml` controller image |
| `ecr_generator_repository_url` | `kustomization.yaml` generator image |
| `archive_bucket_name` | `APEXLOAD_ARCHIVE_BUCKET` |
| `wrapping_kms_key_arn` | `APEXLOAD_WRAPPING_KMS_KEY_ID` |
| `controller_irsa_role_arn` | `eks.amazonaws.com/role-arn` on the controller SA |
| `generator_irsa_role_arn` | `eks.amazonaws.com/role-arn` on the generator SA |

`database_url_secret_arn` and `redis_url_secret_arn` are Secrets Manager ARNs,
not values. The credentials are generated here and written to Secrets Manager
deliberately: emitting them as outputs would put them in plaintext in the state
file and in any CI log that prints outputs. Sync them into the
`apexload-data-platform` Secret with the Secrets Store CSI driver, or with
`aws secretsmanager get-secret-value` in a controlled step.

## Registering the cluster in APEX Load

After apply, register the cluster under **Fleet → Clusters**:

| Field | Value |
|---|---|
| Provider | Amazon EKS |
| API server URL | `terraform output eks_cluster_endpoint` |
| AWS region | `terraform output aws_region` |
| EKS cluster name | `terraform output eks_cluster_name` |
| AWS credentials | Workload identity (IRSA) |
| TLS trust | Host system roots |

Then press **Test**. The `sts_identity` probe reports the exact caller ARN the
cluster sees. If `sts_identity` passes but `api_server` returns 401/403, that
ARN authenticates but is not authorized.

Authentication and authorization are **two separate steps**, and this module
only does the first:

1. The access entry it creates maps the controller role into the cluster as a
   member of the `apexload-controllers` group.
2. That group still has no permissions until a RoleBinding grants them. Apply
   `deployments/k8s/eks/restart-controls.yaml` **and**
   `cluster-access.example.yaml` to bind the least-privilege Role to the group.

No AWS-managed access policy is attached deliberately: `AmazonEKSAdminPolicy`
and friends are wildly over-privileged for a controller that only needs to scale
one StatefulSet.

This applies only to a **remote** registration — a controller elsewhere driving
this cluster. A controller running *inside* this cluster registers it as kind
`in_cluster` and authorizes through its own ServiceAccount, so neither step
applies.

## Notes on specific choices

- **Two KMS keys.** The wrapping key seals credential envelopes; the storage key
  encrypts RDS/S3/ElastiCache/CloudWatch. Keeping them separate means a
  storage-key policy change cannot make every stored credential unreadable.
- **The wrapping key is pinned by ARN, never alias.** An alias can be repointed
  at a different key, which would strand existing ciphertext permanently.
- **Set exactly one wrapping key.** Configuring both
  `APEXLOAD_WRAPPING_KMS_KEY_ID` and `APEXLOAD_WRAPPING_KEY_VAULT_KEY_ID` is
  refused at startup rather than resolved silently.
- **Split IRSA roles.** The controller may rotate project secrets; the generator
  may only read them. Generators execute user-supplied scripts, so rotation
  authority there would let a script rewrite the credentials other scripts read.
- **STS VPC endpoint with private DNS.** The controller's EKS bearer token is a
  presigned STS URL — a replayable credential. Private DNS keeps it resolving as
  `sts.<region>.amazonaws.com`, which is what the controller's STS host
  allow-list accepts; a raw `vpce-*` hostname would be refused.
- **Generator capacity is checked at plan time.** KEDA will request more
  replicas than the node group can schedule; the surplus pods sit `Pending` and
  the test silently runs under its declared concurrency.

## Not provisioned

Azure Event Hubs and Service Bus have no AWS twin here. They remain supported
protocol *targets* from an EKS deployment — that is a load-test payload concern,
not infrastructure — but nothing in this module creates them.
