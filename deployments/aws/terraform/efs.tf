# ReadWriteMany storage for the HA overlay.
#
# The eks-ha overlay runs multiple controller replicas that share the
# import-assets volume, so that PVC needs ReadWriteMany. This is a real parity
# gap with AKS rather than a copy of it: AKS ships an `azurefile-csi`
# StorageClass out of the box, and EKS ships NO ReadWriteMany class at all.
# Without this filesystem — and the driver below — applying eks-ha leaves the
# PVC Pending and the controller pods unschedulable, with nothing in the
# manifests to explain why.
#
# This provisions the storage and its networking. The EFS CSI driver and the
# StorageClass are cluster add-ons, not infrastructure; see the "Prerequisite"
# note in deployments/k8s/eks-ha/kustomization.yaml for the two commands.
#
# The single-controller (non-HA) deployment does not use this at all.

resource "aws_security_group" "efs" {
  name        = "${local.name}-efs"
  description = "APEX Load EFS for the HA import-assets volume"
  vpc_id      = aws_vpc.main.id

  ingress {
    description = "NFS from inside the VPC"
    from_port   = 2049
    to_port     = 2049
    protocol    = "tcp"
    cidr_blocks = [var.vpc_cidr]
  }

  tags = merge(local.tags, { Name = "${local.name}-efs" })
}

resource "aws_efs_file_system" "import_assets" {
  creation_token = "${local.name}-import-assets"
  encrypted      = true
  kms_key_id     = aws_kms_key.storage.arn

  # Import assets are read constantly during a run and idle between runs, so
  # bursting matches the access pattern without provisioning throughput.
  throughput_mode = "bursting"

  lifecycle_policy {
    transition_to_ia = "AFTER_30_DAYS"
  }

  tags = merge(local.tags, { Name = "${local.name}-import-assets" })
}

# One mount target per AZ. Without a mount target in a subnet, pods scheduled on
# nodes in that subnet cannot mount the filesystem at all — which presents as a
# pod that starts fine in one AZ and hangs in another.
resource "aws_efs_mount_target" "import_assets" {
  count = length(var.availability_zones)

  file_system_id  = aws_efs_file_system.import_assets.id
  subnet_id       = aws_subnet.private[count.index].id
  security_groups = [aws_security_group.efs.id]
}

resource "aws_efs_file_system_policy" "import_assets" {
  file_system_id = aws_efs_file_system.import_assets.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid    = "DenyInsecureTransport"
      Effect = "Deny"
      # A Deny with NotAction would also block the implicit allow; this is the
      # narrower form: refuse everything not over TLS.
      Principal = { AWS = "*" }
      Action    = "*"
      Resource  = aws_efs_file_system.import_assets.arn
      Condition = {
        Bool = { "aws:SecureTransport" = "false" }
      }
    }]
  })
}
