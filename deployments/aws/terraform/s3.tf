resource "aws_s3_bucket" "archives" {
  bucket = "${local.name}-archives-${random_string.suffix.result}"
  tags   = merge(local.tags, { Name = "${local.name}-archives" })
}

# Run evidence is the artifact a performance result is defended with, so an
# accidental overwrite has to be recoverable.
resource "aws_s3_bucket_versioning" "archives" {
  bucket = aws_s3_bucket.archives.id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "archives" {
  bucket = aws_s3_bucket.archives.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = "aws:kms"
      kms_master_key_id = aws_kms_key.storage.arn
    }
    # Cuts KMS request volume dramatically for the many-small-objects pattern a
    # run archive produces.
    bucket_key_enabled = true
  }
}

resource "aws_s3_bucket_public_access_block" "archives" {
  bucket = aws_s3_bucket.archives.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_lifecycle_configuration" "archives" {
  count  = var.archive_retention_days > 0 ? 1 : 0
  bucket = aws_s3_bucket.archives.id

  rule {
    id     = "expire-run-evidence"
    status = "Enabled"

    filter {
      prefix = "production/run-evidence/"
    }

    expiration {
      days = var.archive_retention_days
    }

    # Versioning is on, so non-current versions must expire too or the bucket
    # grows without bound despite the rule above.
    noncurrent_version_expiration {
      noncurrent_days = var.archive_retention_days
    }
  }
}

# Refuse any unencrypted or wrong-key upload at the bucket, not just default it.
data "aws_iam_policy_document" "archives" {
  statement {
    sid       = "DenyUnencryptedUploads"
    effect    = "Deny"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.archives.arn}/*"]

    principals {
      type        = "*"
      identifiers = ["*"]
    }

    condition {
      test     = "StringNotEquals"
      variable = "s3:x-amz-server-side-encryption"
      values   = ["aws:kms"]
    }
  }

  statement {
    sid       = "DenyInsecureTransport"
    effect    = "Deny"
    actions   = ["s3:*"]
    resources = [aws_s3_bucket.archives.arn, "${aws_s3_bucket.archives.arn}/*"]

    principals {
      type        = "*"
      identifiers = ["*"]
    }

    condition {
      test     = "Bool"
      variable = "aws:SecureTransport"
      values   = ["false"]
    }
  }
}

resource "aws_s3_bucket_policy" "archives" {
  bucket     = aws_s3_bucket.archives.id
  policy     = data.aws_iam_policy_document.archives.json
  depends_on = [aws_s3_bucket_public_access_block.archives]
}
