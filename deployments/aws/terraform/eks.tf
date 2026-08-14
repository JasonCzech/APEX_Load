resource "aws_iam_role" "cluster" {
  count = local.eks_enabled ? 1 : 0
  name  = "${local.name}-eks-cluster"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "eks.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })

  tags = local.tags
}

resource "aws_iam_role_policy_attachment" "cluster" {
  count      = local.eks_enabled ? 1 : 0
  role       = aws_iam_role.cluster[0].name
  policy_arn = "arn:${data.aws_partition.current.partition}:iam::aws:policy/AmazonEKSClusterPolicy"
}

resource "aws_eks_cluster" "main" {
  count    = local.eks_enabled ? 1 : 0
  name     = local.name
  role_arn = aws_iam_role.cluster[0].arn
  version  = var.kubernetes_version

  vpc_config {
    subnet_ids              = concat(aws_subnet.private[*].id, aws_subnet.public[*].id)
    endpoint_private_access = true
    endpoint_public_access  = true
  }

  # API_AND_CONFIG_MAP keeps the aws-auth ConfigMap working while access
  # entries are adopted. Cluster connections registered in APEX Load map a
  # principal either way; the sts_identity probe reports the exact caller ARN to
  # map, which is the thing that is otherwise invisible.
  access_config {
    authentication_mode                         = "API_AND_CONFIG_MAP"
    bootstrap_cluster_creator_admin_permissions = true
  }

  enabled_cluster_log_types = ["api", "audit", "authenticator", "controllerManager", "scheduler"]

  depends_on = [aws_iam_role_policy_attachment.cluster[0]]

  tags = local.tags
}

# The OIDC provider is what makes IRSA work: it lets a Kubernetes service
# account token be exchanged for AWS credentials, so no static key ever has to
# be stored for the controller's own identity.
data "tls_certificate" "cluster_oidc" {
  count = local.eks_enabled ? 1 : 0
  url   = aws_eks_cluster.main[0].identity[0].oidc[0].issuer
}

resource "aws_iam_openid_connect_provider" "cluster" {
  count           = local.eks_enabled ? 1 : 0
  url             = aws_eks_cluster.main[0].identity[0].oidc[0].issuer
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = [data.tls_certificate.cluster_oidc[0].certificates[0].sha1_fingerprint]

  tags = local.tags
}

# --- Node groups ---

resource "aws_iam_role" "node" {
  count = local.eks_enabled ? 1 : 0
  name  = "${local.name}-eks-node"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })

  tags = local.tags
}

resource "aws_iam_role_policy_attachment" "node" {
  for_each = local.eks_enabled ? toset([
    "AmazonEKSWorkerNodePolicy",
    "AmazonEKS_CNI_Policy",
    "AmazonEC2ContainerRegistryReadOnly",
  ]) : toset([])

  role       = aws_iam_role.node[0].name
  policy_arn = "arn:${data.aws_partition.current.partition}:iam::aws:policy/${each.value}"
}

resource "aws_eks_node_group" "controller" {
  count           = var.controller_compute == "eks" ? 1 : 0
  cluster_name    = aws_eks_cluster.main[0].name
  node_group_name = "controller"
  node_role_arn   = aws_iam_role.node[0].arn
  subnet_ids      = aws_subnet.private[*].id
  instance_types  = [var.controller_node_instance_type]

  scaling_config {
    desired_size = var.controller_node_count
    min_size     = var.controller_node_count
    max_size     = var.controller_node_count + 2
  }

  update_config {
    max_unavailable = 1
  }

  labels = {
    "apexload.io/role" = "controller"
  }

  depends_on = [aws_iam_role_policy_attachment.node]

  tags = merge(local.tags, { Name = "${local.name}-controller" })
}

# The generator node group is the one a fleet pool's allocation pins to. Its
# name is what NodeRef.AgentPool() reports from the
# eks.amazonaws.com/nodegroup label, and it is exactly the value an
# administrator selects in the allocation editor.
resource "aws_eks_node_group" "generator" {
  count           = var.generator_compute == "eks" ? 1 : 0
  cluster_name    = aws_eks_cluster.main[0].name
  node_group_name = "generator"
  node_role_arn   = aws_iam_role.node[0].arn
  subnet_ids      = aws_subnet.private[*].id
  instance_types  = [var.generator_node_instance_type]

  scaling_config {
    desired_size = var.generator_node_min_count
    min_size     = var.generator_node_min_count
    max_size     = var.generator_node_max_count
  }

  update_config {
    max_unavailable = 1
  }

  # This label is REQUIRED, not descriptive: the generator StatefulSet in
  # deployments/k8s/eks carries `nodeSelector: apexload.io/node-pool=generators`.
  # If no node advertises that exact key and value, every generator pod stays
  # Pending forever and the fleet never comes up. Keep it in sync with
  # generator-statefulset.yaml; deployment_artifacts_test.go enforces the pair.
  labels = {
    "apexload.io/node-pool" = "generators"
  }

  # Reserve these nodes for load generation. Without the taint, controller and
  # system pods schedule onto generator nodes and steal the CPU the run is
  # trying to measure — the results look like a product limit rather than
  # co-tenancy. The generator pod template carries the matching toleration.
  taint {
    key    = "apexload.io/dedicated"
    value  = "generators"
    effect = "NO_SCHEDULE"
  }

  lifecycle {
    precondition {
      condition     = local.generator_instance_capacity != null
      error_message = "generator_node_instance_type must be one of the types in local.generator_instance_capacity_by_type so KEDA/node capacity can be validated."
    }
    precondition {
      condition     = local.generator_schedulable_pods >= var.generator_keda_max_replicas
      error_message = "generator node group cannot schedule generator_keda_max_replicas pods; raise generator_node_max_count, use a larger generator_node_instance_type, or lower generator_keda_max_replicas."
    }
  }

  depends_on = [aws_iam_role_policy_attachment.node]

  tags = merge(local.tags, { Name = "${local.name}-generator" })
}

moved {
  from = aws_iam_role.cluster
  to   = aws_iam_role.cluster[0]
}

moved {
  from = aws_iam_role_policy_attachment.cluster
  to   = aws_iam_role_policy_attachment.cluster[0]
}

moved {
  from = aws_eks_cluster.main
  to   = aws_eks_cluster.main[0]
}

moved {
  from = data.tls_certificate.cluster_oidc
  to   = data.tls_certificate.cluster_oidc[0]
}

moved {
  from = aws_iam_openid_connect_provider.cluster
  to   = aws_iam_openid_connect_provider.cluster[0]
}

moved {
  from = aws_iam_role.node
  to   = aws_iam_role.node[0]
}

moved {
  from = aws_eks_node_group.controller
  to   = aws_eks_node_group.controller[0]
}

moved {
  from = aws_eks_node_group.generator
  to   = aws_eks_node_group.generator[0]
}
