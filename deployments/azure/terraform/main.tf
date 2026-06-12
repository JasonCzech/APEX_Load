locals {
  name = "${var.prefix}-${var.environment}"
  tags = merge({
    project     = "apexload"
    environment = var.environment
    managed-by  = "terraform"
  }, var.tags)

  # Kubernetes namespace + service accounts the federated credentials trust.
  # Must match deployments/k8s/aks (namespace.yaml / serviceaccounts.yaml).
  k8s_namespace              = "apexload"
  controller_service_account = "apexload-controller"
  generator_service_account  = "apexload-generator"
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
