# The envelope-wrapping key.
#
# APEX Load seals cluster-connection credentials and project-secret ciphertext
# with a per-record data key, and wraps that data key with this CMK, so the
# master key never sits in the database. The wrapper also binds each envelope to
# its record through the KMS encryption context, which means a stolen wrapped
# key cannot be unwrapped against a different record even at the KMS layer.
#
# Rotation is enabled, and the controller pins the FULL key ARN rather than an
# alias: an alias can be repointed at a different key, which would strand every
# envelope sealed under the old one as unrecoverable ciphertext.
resource "aws_kms_key" "wrapping" {
  description             = "APEX Load credential envelope wrapping key"
  enable_key_rotation     = true
  deletion_window_in_days = 30

  tags = merge(local.tags, { Name = "${local.name}-wrapping" })
}

resource "aws_kms_alias" "wrapping" {
  # Provided for human navigation in the console only. Do NOT configure
  # APEXLOAD_WRAPPING_KMS_KEY_ID with this alias; use the key ARN.
  name          = "alias/${local.name}-wrapping"
  target_key_id = aws_kms_key.wrapping.key_id
}

# A separate key for data at rest (RDS, S3, ElastiCache). Keeping it distinct
# from the wrapping key means a storage-key rotation or policy change cannot
# accidentally make every stored credential unreadable.
resource "aws_kms_key" "storage" {
  description             = "APEX Load storage encryption key"
  enable_key_rotation     = true
  deletion_window_in_days = 30

  tags = merge(local.tags, { Name = "${local.name}-storage" })
}

resource "aws_kms_alias" "storage" {
  name          = "alias/${local.name}-storage"
  target_key_id = aws_kms_key.storage.key_id
}
