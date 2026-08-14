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
  description = "VM size for the generator node pool. Standard_D4s_v5 is 4 vCPU / 16 GiB; validate KEDA max replicas against pod requests and node reservations."
  type        = string
  default     = "Standard_D4s_v5"
}

variable "generator_node_min_count" {
  description = "Minimum nodes in the autoscaling generator pool."
  type        = number
  default     = 1
}

variable "generator_node_max_count" {
  description = "Maximum nodes in the autoscaling generator pool. Default fits the 50-pod KEDA ceiling on Standard_D4s_v5 with the default pod requests and reservation factor."
  type        = number
  default     = 17
}

variable "generator_keda_max_replicas" {
  description = "KEDA maxReplicaCount from deployments/k8s/aks/generator-keda-scaledobject.yaml. Used to validate generator node-pool capacity."
  type        = number
  default     = 50

  validation {
    condition     = var.generator_keda_max_replicas > 0
    error_message = "generator_keda_max_replicas must be greater than zero."
  }
}

variable "generator_pod_cpu_millicores" {
  description = "CPU request per generator pod in millicores. Keep in sync with deployments/k8s/aks/generator-statefulset.yaml."
  type        = number
  default     = 1000

  validation {
    condition     = var.generator_pod_cpu_millicores > 0
    error_message = "generator_pod_cpu_millicores must be greater than zero."
  }
}

variable "generator_pod_memory_mib" {
  description = "Memory request per generator pod in MiB. Keep in sync with deployments/k8s/aks/generator-statefulset.yaml."
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

variable "availability_zones" {
  description = "Availability zones used by the AKS system and generator pools. Use an empty list only in regions without zone support."
  type        = list(string)
  default     = ["1", "2", "3"]
}

variable "aks_admin_group_object_ids" {
  description = "Microsoft Entra group object IDs granted AKS admin through Azure RBAC."
  type        = list(string)
  default     = []
}

variable "aks_local_account_disabled" {
  description = "Disable local AKS admin accounts. Keep true for production; the bootstrap role assignment below preserves Terraform operator access by default."
  type        = bool
  default     = true
}

variable "aks_grant_current_user_cluster_admin" {
  description = "Grant the current Terraform principal Azure Kubernetes Service RBAC Cluster Admin on the new cluster for bootstrap access."
  type        = bool
  default     = true
}

variable "aks_auto_upgrade_channel" {
  description = "AKS automatic upgrade channel."
  type        = string
  default     = "stable"

  validation {
    condition     = contains(["patch", "rapid", "stable", "node-image", "none"], var.aks_auto_upgrade_channel)
    error_message = "aks_auto_upgrade_channel must be patch, rapid, stable, node-image, or none."
  }
}

variable "aks_node_os_upgrade_channel" {
  description = "AKS node OS image upgrade channel."
  type        = string
  default     = "NodeImage"

  validation {
    condition     = contains(["Unmanaged", "SecurityPatch", "NodeImage", "None"], var.aks_node_os_upgrade_channel)
    error_message = "aks_node_os_upgrade_channel must be Unmanaged, SecurityPatch, NodeImage, or None."
  }
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
  description = "Legacy Azure Cache for Redis tier, used only when redis_service=azure_cache_legacy."
  type        = string
  default     = "Standard"
}

variable "redis_capacity" {
  description = "Legacy Azure Cache for Redis capacity, used only when redis_service=azure_cache_legacy."
  type        = number
  default     = 1
}

variable "redis_service" {
  description = "Redis service for new deployments. Azure Cache is retained only as an explicit migration bridge."
  type        = string
  default     = "managed_redis"

  validation {
    condition     = contains(["managed_redis", "azure_cache_legacy"], var.redis_service)
    error_message = "redis_service must be managed_redis or azure_cache_legacy."
  }
}

variable "managed_redis_sku" {
  description = "Azure Managed Redis SKU used for new deployments."
  type        = string
  default     = "Balanced_B1"
}

variable "managed_redis_private_dns_zone_name" {
  description = "Private DNS zone for Azure Managed Redis private endpoints."
  type        = string
  default     = "privatelink.redis.azure.net"
}

variable "acr_sku" {
  description = "Azure Container Registry SKU."
  type        = string
  default     = "Standard"
}

variable "eventhubs_enabled" {
  description = "Provision an Azure Event Hubs namespace + hub for messaging load tests (opt-in)."
  type        = bool
  default     = false
}

variable "eventhubs_name" {
  description = "Event hub (entity) name created in the namespace."
  type        = string
  default     = "apexload-load"
}

variable "eventhubs_sku" {
  description = "Event Hubs namespace SKU (Basic, Standard, Premium)."
  type        = string
  default     = "Standard"
}

variable "eventhubs_capacity" {
  description = "Event Hubs throughput units (Standard) / processing units."
  type        = number
  default     = 1
}

variable "eventhubs_partition_count" {
  description = "Partition count for the event hub (drives per-partition skew testing)."
  type        = number
  default     = 4
}

variable "authorized_ip_ranges" {
  description = "CIDRs allowed to reach the AKS API server. Empty list = unrestricted (not recommended for prod)."
  type        = list(string)
  default     = []
}

variable "public_network_allowed_cidrs" {
  description = "CIDRs allowed to reach public Storage and Key Vault endpoints when network_mode=public. Empty keeps evaluation-mode public access open."
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

variable "container_apps_subnet_prefixes" {
  description = "Dedicated infrastructure subnet for the Container Apps environment when network_mode=private."
  type        = list(string)
  default     = ["10.42.12.0/23"]
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

variable "controller_compute" {
  description = "Controller runtime. AKS is omitted when neither controller nor generators select it."
  type        = string
  default     = "aks"
  validation {
    condition     = contains(["aks", "container_apps", "external"], var.controller_compute)
    error_message = "controller_compute must be aks, container_apps, or external."
  }
}

variable "generator_compute" {
  description = "Generator runtime. container_apps provisions a warm app plus a manual burst Job; external emits no generator compute."
  type        = string
  default     = "aks"
  validation {
    condition     = contains(["aks", "container_apps", "external"], var.generator_compute)
    error_message = "generator_compute must be aks, container_apps, or external."
  }
}

variable "controller_image" {
  description = "Immutable controller image reference used by Container Apps."
  type        = string
  default     = ""
}

variable "generator_image" {
  description = "Immutable generator image reference used by the warm app and burst Job."
  type        = string
  default     = ""
}

variable "controller_public_url" {
  description = "Externally reachable HTTPS controller origin used by reverse generators. Leave empty until ingress DNS is known and then re-apply."
  type        = string
  default     = ""
}

variable "generator_gateway_token" {
  description = "Target-scoped reverse enrollment token returned once by APEX Load registration. Store in Terraform state through an encrypted remote backend."
  type        = string
  default     = ""
  sensitive   = true
}
