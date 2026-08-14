locals {
  name = "${var.prefix}-${var.environment}"
  tags = merge({
    project     = "apexload"
    environment = var.environment
    managed-by  = "terraform"
  }, var.tags)

  # Kubernetes namespace + service accounts the federated credentials trust.
  # Must match deployments/k8s/aks (namespace.yaml / serviceaccounts.yaml).
  k8s_namespace                 = "apexload"
  controller_service_account    = "apexload-controller"
  generator_service_account     = "apexload-generator"
  keda_operator_namespace       = "kube-system"
  keda_operator_service_account = "keda-operator"
  private_network_enabled       = var.network_mode == "private"
  aks_enabled                   = var.controller_compute == "aks" || var.generator_compute == "aks"

  generator_vm_capacity_by_size = {
    Standard_D4s_v5  = { cpu_millicores = 4000, memory_mib = 16384 }
    Standard_D8s_v5  = { cpu_millicores = 8000, memory_mib = 32768 }
    Standard_D16s_v5 = { cpu_millicores = 16000, memory_mib = 65536 }
    Standard_D32s_v5 = { cpu_millicores = 32000, memory_mib = 131072 }
  }
  generator_vm_capacity             = lookup(local.generator_vm_capacity_by_size, var.generator_node_vm_size, null)
  generator_schedulable_cpu_pods    = local.generator_vm_capacity == null ? 0 : floor((var.generator_node_max_count * local.generator_vm_capacity.cpu_millicores * (1 - var.generator_node_reserved_fraction)) / var.generator_pod_cpu_millicores)
  generator_schedulable_memory_pods = local.generator_vm_capacity == null ? 0 : floor((var.generator_node_max_count * local.generator_vm_capacity.memory_mib * (1 - var.generator_node_reserved_fraction)) / var.generator_pod_memory_mib)
  generator_schedulable_pods        = min(local.generator_schedulable_cpu_pods, local.generator_schedulable_memory_pods)
}

data "azurerm_client_config" "current" {}

resource "azurerm_resource_group" "main" {
  name     = "${local.name}-rg"
  location = var.location
  tags     = local.tags
}

# Suffix for resources with global DNS names (storage account, key vault, ACR).
resource "random_string" "suffix" {
  length  = 6
  lower   = true
  upper   = false
  numeric = true
  special = false
}
