# IRSA roles for the controller and generator service accounts.
#
# The privilege split here is the whole point of having two roles rather than
# one: the controller may ROTATE project secrets and unwrap credential
# envelopes, while the generator may only READ the secrets it resolves during a
# run. Collapsing them would hand every generator pod — the workload that
# executes user-supplied scripts — the authority to rewrite the secrets those
# scripts read.

locals {
  oidc_provider_url = replace(aws_iam_openid_connect_provider.cluster.url, "https://", "")
  secret_prefix     = "${local.name}/project-secrets"
}

data "aws_iam_policy_document" "controller_assume_role" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.cluster.arn]
    }

    # Scoped to one namespace AND one service account. A wildcard here would let
    # any pod in the cluster assume the controller's role.
    condition {
      test     = "StringEquals"
      variable = "${local.oidc_provider_url}:sub"
      values   = ["system:serviceaccount:apexload:apexload-controller"]
    }

    condition {
      test     = "StringEquals"
      variable = "${local.oidc_provider_url}:aud"
      values   = ["sts.amazonaws.com"]
    }
  }
}

data "aws_iam_policy_document" "generator_assume_role" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.cluster.arn]
    }

    condition {
      test     = "StringEquals"
      variable = "${local.oidc_provider_url}:sub"
      values   = ["system:serviceaccount:apexload:apexload-generator"]
    }

    condition {
      test     = "StringEquals"
      variable = "${local.oidc_provider_url}:aud"
      values   = ["sts.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "controller" {
  name               = "${local.name}-controller"
  assume_role_policy = data.aws_iam_policy_document.controller_assume_role.json
  tags               = local.tags
}

resource "aws_iam_role" "generator" {
  name               = "${local.name}-generator"
  assume_role_policy = data.aws_iam_policy_document.generator_assume_role.json
  tags               = local.tags
}

# --- Controller policy ---

data "aws_iam_policy_document" "controller" {
  statement {
    sid    = "ArchiveObjectReadWrite"
    effect = "Allow"
    actions = [
      "s3:GetObject",
      "s3:PutObject",
      "s3:DeleteObject",
      "s3:AbortMultipartUpload",
    ]
    # Confined to the archive prefix rather than the whole bucket.
    resources = ["${aws_s3_bucket.archives.arn}/*"]
  }

  statement {
    sid       = "ArchiveBucketList"
    effect    = "Allow"
    actions   = ["s3:ListBucket", "s3:GetBucketLocation"]
    resources = [aws_s3_bucket.archives.arn]
  }

  statement {
    sid    = "WrappingKeyUse"
    effect = "Allow"
    # Encrypt/Decrypt only: the controller wraps and unwraps data keys but must
    # never be able to schedule the key for deletion or edit its policy.
    actions   = ["kms:Encrypt", "kms:Decrypt", "kms:DescribeKey"]
    resources = [aws_kms_key.wrapping.arn]
  }

  statement {
    sid       = "StorageKeyUse"
    effect    = "Allow"
    actions   = ["kms:Encrypt", "kms:Decrypt", "kms:GenerateDataKey", "kms:DescribeKey"]
    resources = [aws_kms_key.storage.arn]
  }

  statement {
    sid    = "ProjectSecretsRotate"
    effect = "Allow"
    actions = [
      "secretsmanager:GetSecretValue",
      "secretsmanager:DescribeSecret",
      "secretsmanager:PutSecretValue",
    ]
    # Confined to the APEX Load secret path so the controller cannot read or
    # rewrite unrelated secrets in the account.
    resources = ["arn:${data.aws_partition.current.partition}:secretsmanager:${var.aws_region}:${data.aws_caller_identity.current.account_id}:secret:${local.secret_prefix}/*"]
  }
}

resource "aws_iam_role_policy" "controller" {
  name   = "${local.name}-controller"
  role   = aws_iam_role.controller.id
  policy = data.aws_iam_policy_document.controller.json
}

# --- Generator policy ---

data "aws_iam_policy_document" "generator" {
  statement {
    sid       = "ArchiveObjectWrite"
    effect    = "Allow"
    actions   = ["s3:PutObject", "s3:AbortMultipartUpload"]
    resources = ["${aws_s3_bucket.archives.arn}/*"]
  }

  statement {
    sid       = "StorageKeyUse"
    effect    = "Allow"
    actions   = ["kms:Encrypt", "kms:GenerateDataKey", "kms:DescribeKey"]
    resources = [aws_kms_key.storage.arn]
  }

  statement {
    sid    = "ProjectSecretsRead"
    effect = "Allow"
    # READ ONLY. No PutSecretValue: a generator executes user-supplied scripts,
    # so rotation authority there would let a script rewrite the credentials
    # every other script reads.
    actions   = ["secretsmanager:GetSecretValue", "secretsmanager:DescribeSecret"]
    resources = ["arn:${data.aws_partition.current.partition}:secretsmanager:${var.aws_region}:${data.aws_caller_identity.current.account_id}:secret:${local.secret_prefix}/*"]
  }
}

resource "aws_iam_role_policy" "generator" {
  name   = "${local.name}-generator"
  role   = aws_iam_role.generator.id
  policy = data.aws_iam_policy_document.generator.json
}

# --- Cluster access entry ---
#
# An IAM principal that authenticates successfully still has no Kubernetes
# authority until it is mapped AND bound. This is the #1 EKS failure mode, and
# it is what the sts_identity connection probe exists to diagnose: it reports
# the exact caller ARN that needs mapping.
#
# This resource does only HALF the job. It maps the controller role into the
# cluster as a member of the group below; the group still has no permissions
# until a RoleBinding grants them. Deliberately NOT attached here:
#
#   - An aws_eks_access_policy_association with an AWS-managed policy
#     (AmazonEKSAdminPolicy and friends) would be wildly over-privileged for a
#     controller that only needs to scale one StatefulSet.
#   - The permissions it actually needs are already expressed, least-privilege,
#     in deployments/k8s/eks/restart-controls.example.yaml. Bind that Role to
#     this group with cluster-access.example.yaml in the same overlay.
#
# Note this matters only for a REMOTE registration — a controller in another
# cluster driving this one. A controller running inside this cluster registers
# it as kind `in_cluster` and authorizes through its ServiceAccount instead.
resource "aws_eks_access_entry" "controller" {
  cluster_name  = aws_eks_cluster.main.name
  principal_arn = aws_iam_role.controller.arn
  type          = "STANDARD"

  # Binding RBAC to a stable group rather than to the principal's default
  # username keeps the RoleBinding free of the account ID and role path, so the
  # manifest is identical across environments.
  kubernetes_groups = ["apexload-controllers"]

  tags = local.tags
}
