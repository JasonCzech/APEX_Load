variable "name_prefix" {
  description = "Prefix for every resource name. Keep it short: some AWS names are length-capped."
  type        = string
  default     = "apexload"

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{2,20}$", var.name_prefix))
    error_message = "name_prefix must be 3-21 lowercase alphanumeric or hyphen characters and start with a letter."
  }
}

variable "aws_region" {
  description = "Region for every resource. It is also the STS signing region for the EKS cluster connection."
  type        = string
  default     = "us-east-1"

  validation {
    condition     = can(regex("^[a-z0-9-]{1,32}$", var.aws_region))
    error_message = "aws_region must be a valid AWS region name."
  }
}

variable "environment" {
  description = "Environment tag applied to every resource."
  type        = string
  default     = "production"
}

variable "vpc_cidr" {
  description = "CIDR for the VPC. Must be large enough for the pod-per-node density the VPC CNI requires."
  type        = string
  default     = "10.60.0.0/16"
}

variable "availability_zones" {
  description = "AZs for the subnets. EKS requires at least two."
  type        = list(string)
  default     = ["us-east-1a", "us-east-1b", "us-east-1c"]

  validation {
    condition     = length(var.availability_zones) >= 2
    error_message = "EKS requires subnets in at least two availability zones."
  }
}

variable "kubernetes_version" {
  description = "EKS control-plane version."
  type        = string
  default     = "1.30"
}

variable "controller_node_instance_type" {
  description = "Instance type for the controller node group."
  type        = string
  default     = "m6i.large"
}

variable "controller_node_count" {
  description = "Desired size of the controller node group. Use 2 or more for the eks-ha overlay."
  type        = number
  default     = 2
}

variable "generator_node_instance_type" {
  description = "Instance type for the generator node group. m6i.xlarge is 4 vCPU / 16 GiB; validate KEDA max replicas against pod requests and node reservations."
  type        = string
  default     = "m6i.xlarge"
}

variable "generator_node_min_count" {
  description = "Minimum size of the generator node group."
  type        = number
  default     = 2
}

variable "generator_node_max_count" {
  description = "Maximum size of the generator node group. This bounds how many generator pods KEDA can actually schedule."
  type        = number
  default     = 20
}

variable "generator_keda_max_replicas" {
  description = "KEDA maxReplicaCount from deployments/k8s/eks/generator-keda-scaledobject.yaml. Used to validate generator node-group capacity."
  type        = number
  default     = 50

  validation {
    condition     = var.generator_keda_max_replicas > 0
    error_message = "generator_keda_max_replicas must be greater than zero."
  }
}

variable "generator_pod_cpu_millicores" {
  description = "CPU request per generator pod in millicores. Keep in sync with deployments/k8s/eks/generator-statefulset.yaml."
  type        = number
  default     = 1000

  validation {
    condition     = var.generator_pod_cpu_millicores > 0
    error_message = "generator_pod_cpu_millicores must be greater than zero."
  }
}

variable "generator_pod_memory_mib" {
  description = "Memory request per generator pod in MiB. Keep in sync with deployments/k8s/eks/generator-statefulset.yaml."
  type        = number
  default     = 2048

  validation {
    condition     = var.generator_pod_memory_mib > 0
    error_message = "generator_pod_memory_mib must be greater than zero."
  }
}

variable "generator_node_reserved_fraction" {
  description = "Conservative fraction of each generator node reserved for kubelet/system overhead and scheduling headroom."
  type        = number
  default     = 0.25

  validation {
    condition     = var.generator_node_reserved_fraction >= 0 && var.generator_node_reserved_fraction < 1
    error_message = "generator_node_reserved_fraction must be >= 0 and < 1."
  }
}

variable "postgres_instance_class" {
  description = "RDS instance class for the APEX Load database."
  type        = string
  default     = "db.m6g.large"
}

variable "postgres_version" {
  description = "PostgreSQL engine version."
  type        = string
  default     = "16"
}

variable "postgres_allocated_storage_gb" {
  description = "Allocated storage for the RDS instance."
  type        = number
  default     = 100
}

variable "postgres_backup_retention_days" {
  description = "Automated backup retention. Zero disables backups and is refused."
  type        = number
  default     = 14

  validation {
    condition     = var.postgres_backup_retention_days >= 1
    error_message = "postgres_backup_retention_days must be at least 1; run evidence is not reconstructible without backups."
  }
}

variable "redis_node_type" {
  description = "ElastiCache node type for the live-state / HA fanout cache."
  type        = string
  default     = "cache.m6g.large"
}

variable "archive_retention_days" {
  description = "Lifecycle expiry for run-evidence objects in the archive bucket. Zero keeps them indefinitely."
  type        = number
  default     = 0
}
