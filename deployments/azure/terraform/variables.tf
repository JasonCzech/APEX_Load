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

variable "network_mode" {
  description = "Azure data-plane networking mode. Use private for production private endpoints/private DNS; public is evaluation-only."
  type        = string
  default     = "private"

  validation {
    condition     = contains(["private", "public"], var.network_mode)
    error_message = "network_mode must be private or public."
  }
}

variable "vnet_address_space" {
  description = "Address space for the private AKS/data-platform VNet when network_mode=private."
  type        = list(string)
  default     = ["10.42.0.0/16"]
}

variable "aks_system_subnet_prefixes" {
  description = "Subnet prefixes for the AKS system pool when network_mode=private."
  type        = list(string)
  default     = ["10.42.0.0/22"]
}

variable "aks_generator_subnet_prefixes" {
  description = "Subnet prefixes for the AKS generator pool when network_mode=private."
  type        = list(string)
  default     = ["10.42.4.0/22"]
}

variable "private_endpoint_subnet_prefixes" {
  description = "Subnet prefixes for private endpoints when network_mode=private."
  type        = list(string)
  default     = ["10.42.8.0/24"]
}

variable "private_dns_zone_names" {
  description = "Private DNS zone names used by Azure private endpoints."
  type = object({
    postgres = string
    redis    = string
    blob     = string
    keyvault = string
  })
  default = {
    postgres = "privatelink.postgres.database.azure.com"
    redis    = "privatelink.redis.cache.windows.net"
    blob     = "privatelink.blob.core.windows.net"
    keyvault = "privatelink.vaultcore.azure.net"
  }
}

variable "managed_prometheus_mode" {
  description = "Prometheus integration mode for deployment artifacts: kube_prometheus_stack or azure_monitor."
  type        = string
  default     = "kube_prometheus_stack"

  validation {
    condition     = contains(["kube_prometheus_stack", "azure_monitor"], var.managed_prometheus_mode)
    error_message = "managed_prometheus_mode must be kube_prometheus_stack or azure_monitor."
  }
}

variable "cert_manager_mode" {
  description = "How gRPC mTLS secrets are provisioned: cert_manager, key_vault_csi, or external."
  type        = string
  default     = "external"

  validation {
    condition     = contains(["cert_manager", "key_vault_csi", "external"], var.cert_manager_mode)
    error_message = "cert_manager_mode must be cert_manager, key_vault_csi, or external."
  }
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
