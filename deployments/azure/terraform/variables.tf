variable "prefix" {
  description = "Short name prefix for all resources (lowercase alphanumeric)."
  type        = string
  default     = "apexload"

  validation {
    condition     = can(regex("^[a-z][a-z0-9]{2,15}$", var.prefix))
    error_message = "prefix must be 3-16 lowercase alphanumeric characters and start with a letter."
  }
}

variable "environment" {
  description = "Deployment environment tag (e.g. prod, staging)."
  type        = string
  default     = "prod"
}

variable "location" {
  description = "Azure region for all resources."
  type        = string
  default     = "eastus2"
}

variable "kubernetes_version" {
  description = "AKS Kubernetes version. Leave null for the region default."
  type        = string
  default     = null
}

variable "system_node_vm_size" {
  description = "VM size for the system node pool (controller, ingress, monitoring)."
  type        = string
  default     = "Standard_D4s_v5"
}

variable "system_node_count" {
  description = "Node count for the system node pool."
  type        = number
  default     = 2
}

variable "generator_node_vm_size" {
  description = "VM size for the generator node pool. 4 vCPU / 8 GB matches the 10k-VUser-per-pod design target."
  type        = string
  default     = "Standard_D4s_v5"
}

variable "generator_node_min_count" {
  description = "Minimum nodes in the autoscaling generator pool."
  type        = number
  default     = 1
}

variable "generator_node_max_count" {
  description = "Maximum nodes in the autoscaling generator pool. Size for max generator pods (KEDA scales pods 2-50)."
  type        = number
  default     = 13
}

variable "postgres_sku_name" {
  description = "Azure Database for PostgreSQL Flexible Server SKU."
  type        = string
  default     = "GP_Standard_D2ds_v5"
}

variable "postgres_storage_mb" {
  description = "PostgreSQL storage in MB."
  type        = number
  default     = 65536
}

variable "postgres_admin_login" {
  description = "PostgreSQL administrator login name."
  type        = string
  default     = "apexload"
}

variable "redis_sku" {
  description = "Azure Cache for Redis tier: Basic, Standard, or Premium. Standard+ recommended for HA fanout."
  type        = string
  default     = "Standard"
}

variable "redis_capacity" {
  description = "Azure Cache for Redis capacity (0-6 for C-family)."
  type        = number
  default     = 1
}

variable "acr_sku" {
  description = "Azure Container Registry SKU."
  type        = string
  default     = "Standard"
}

variable "authorized_ip_ranges" {
  description = "CIDRs allowed to reach the AKS API server. Empty list = unrestricted (not recommended for prod)."
  type        = list(string)
  default     = []
}

variable "postgres_allowed_cidrs" {
  description = "Extra CIDRs allowed through the PostgreSQL firewall (e.g. office egress for psql access during migration). AKS egress is allowed automatically."
  type        = map(string)
  default     = {}
}

variable "tags" {
  description = "Extra tags merged onto every resource."
  type        = map(string)
  default     = {}
}
