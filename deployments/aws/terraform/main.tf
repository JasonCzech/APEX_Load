locals {
  name        = var.name_prefix
  eks_enabled = var.controller_compute == "eks" || var.generator_compute == "eks"

  tags = {
    Application = "apexload"
    Environment = var.environment
  }

  # Generator node-group capacity, mirroring the AKS module. KEDA will happily
  # request more replicas than the node group can ever schedule; without this
  # check the surplus pods sit Pending and a load test silently runs at a
  # fraction of its declared concurrency.
  #
  # Capacity is expressed per instance type rather than looked up dynamically so
  # `terraform validate` can fail on a mismatch without any AWS credentials.
  generator_instance_capacity_by_type = {
    "m6i.large"   = { cpu_millicores = 2000, memory_mib = 8192 }
    "m6i.xlarge"  = { cpu_millicores = 4000, memory_mib = 16384 }
    "m6i.2xlarge" = { cpu_millicores = 8000, memory_mib = 32768 }
    "m6i.4xlarge" = { cpu_millicores = 16000, memory_mib = 65536 }
    "m6i.8xlarge" = { cpu_millicores = 32000, memory_mib = 131072 }
    "c6i.xlarge"  = { cpu_millicores = 4000, memory_mib = 8192 }
    "c6i.2xlarge" = { cpu_millicores = 8000, memory_mib = 16384 }
    "c6i.4xlarge" = { cpu_millicores = 16000, memory_mib = 32768 }
  }
  generator_instance_capacity       = lookup(local.generator_instance_capacity_by_type, var.generator_node_instance_type, null)
  generator_schedulable_cpu_pods    = local.generator_instance_capacity == null ? 0 : floor((var.generator_node_max_count * local.generator_instance_capacity.cpu_millicores * (1 - var.generator_node_reserved_fraction)) / var.generator_pod_cpu_millicores)
  generator_schedulable_memory_pods = local.generator_instance_capacity == null ? 0 : floor((var.generator_node_max_count * local.generator_instance_capacity.memory_mib * (1 - var.generator_node_reserved_fraction)) / var.generator_pod_memory_mib)
  generator_schedulable_pods        = min(local.generator_schedulable_cpu_pods, local.generator_schedulable_memory_pods)
}

data "aws_caller_identity" "current" {}

data "aws_partition" "current" {}

# Suffix for resources with globally unique names (S3 buckets).
resource "random_string" "suffix" {
  length  = 6
  lower   = true
  upper   = false
  numeric = true
  special = false
}

# The capacity assertion lives in a lifecycle precondition on the generator node
# group (eks.tf), matching the AKS module. A `check` block would only emit a
# WARNING and let the apply proceed, which for a sizing error that silently caps
# fleet size is not good enough.
