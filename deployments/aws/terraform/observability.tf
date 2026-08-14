# EKS writes control-plane logs to a log group whose name is fixed by the
# service. Declaring it here means retention and encryption are governed rather
# than defaulting to "never expire, AWS-managed key".
resource "aws_cloudwatch_log_group" "cluster" {
  count             = local.eks_enabled ? 1 : 0
  name              = "/aws/eks/${local.name}/cluster"
  retention_in_days = 90
  kms_key_id        = aws_kms_key.storage.arn

  tags = local.tags
}

moved {
  from = aws_cloudwatch_log_group.cluster
  to   = aws_cloudwatch_log_group.cluster[0]
}

resource "aws_cloudwatch_log_group" "application" {
  name              = "/apexload/${local.name}"
  retention_in_days = 30
  kms_key_id        = aws_kms_key.storage.arn

  tags = local.tags
}

# CloudWatch Logs must be able to use the storage key, or every log write fails
# with an access-denied that surfaces only as missing logs.
data "aws_iam_policy_document" "storage_key" {
  statement {
    sid       = "AccountAdministration"
    effect    = "Allow"
    actions   = ["kms:*"]
    resources = ["*"]

    principals {
      type        = "AWS"
      identifiers = ["arn:${data.aws_partition.current.partition}:iam::${data.aws_caller_identity.current.account_id}:root"]
    }
  }

  statement {
    sid    = "CloudWatchLogs"
    effect = "Allow"
    actions = [
      "kms:Encrypt",
      "kms:Decrypt",
      "kms:ReEncrypt*",
      "kms:GenerateDataKey*",
      "kms:Describe*",
    ]
    resources = ["*"]

    principals {
      type        = "Service"
      identifiers = ["logs.${var.aws_region}.amazonaws.com"]
    }

    condition {
      test     = "ArnLike"
      variable = "kms:EncryptionContext:aws:logs:arn"
      values   = ["arn:${data.aws_partition.current.partition}:logs:${var.aws_region}:${data.aws_caller_identity.current.account_id}:log-group:*"]
    }
  }
}

resource "aws_kms_key_policy" "storage" {
  key_id = aws_kms_key.storage.id
  policy = data.aws_iam_policy_document.storage_key.json
}
