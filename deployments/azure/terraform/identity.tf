# The original `apexload` identity resource is retained as the controller
# identity so existing Terraform state does not need a resource move. Generators
# get a separate identity with Key Vault-only access by default.
resource "azurerm_user_assigned_identity" "apexload" {
  name                = "${local.name}-controller-workload"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  tags                = local.tags
}

resource "azurerm_user_assigned_identity" "generator" {
  name                = "${local.name}-generator-workload"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  tags                = local.tags
}

resource "azurerm_user_assigned_identity" "keda_scaler" {
  name                = "${local.name}-keda-scaler-workload"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  tags                = local.tags
}

resource "azurerm_federated_identity_credential" "controller" {
  name                = "${local.name}-controller"
  resource_group_name = azurerm_resource_group.main.name
  parent_id           = azurerm_user_assigned_identity.apexload.id
  audience            = ["api://AzureADTokenExchange"]
  issuer              = azurerm_kubernetes_cluster.main.oidc_issuer_url
  subject             = "system:serviceaccount:${local.k8s_namespace}:${local.controller_service_account}"
}

resource "azurerm_federated_identity_credential" "generator" {
  name                = "${local.name}-generator"
  resource_group_name = azurerm_resource_group.main.name
  parent_id           = azurerm_user_assigned_identity.generator.id
  audience            = ["api://AzureADTokenExchange"]
  issuer              = azurerm_kubernetes_cluster.main.oidc_issuer_url
  subject             = "system:serviceaccount:${local.k8s_namespace}:${local.generator_service_account}"
}

resource "azurerm_federated_identity_credential" "keda_scaler" {
  name                = "${local.name}-keda-scaler"
  resource_group_name = azurerm_resource_group.main.name
  parent_id           = azurerm_user_assigned_identity.keda_scaler.id
  audience            = ["api://AzureADTokenExchange"]
  issuer              = azurerm_kubernetes_cluster.main.oidc_issuer_url
  subject             = "system:serviceaccount:${local.keda_operator_namespace}:${local.keda_operator_service_account}"
}

# Run-evidence archives: read/write blobs in the archives container.
resource "azurerm_role_assignment" "workload_blob" {
  scope                = azurerm_storage_account.main.id
  role_definition_name = "Storage Blob Data Contributor"
  principal_id         = azurerm_user_assigned_identity.apexload.principal_id
}

# Script secrets: read-only Key Vault access for {{secret.*}} resolution.
resource "azurerm_role_assignment" "workload_keyvault" {
  scope                = azurerm_key_vault.main.id
  role_definition_name = "Key Vault Secrets User"
  principal_id         = azurerm_user_assigned_identity.apexload.principal_id
}

resource "azurerm_role_assignment" "generator_keyvault" {
  scope                = azurerm_key_vault.main.id
  role_definition_name = "Key Vault Secrets User"
  principal_id         = azurerm_user_assigned_identity.generator.principal_id
}

resource "azurerm_role_assignment" "keda_monitoring_reader" {
  scope                = azurerm_resource_group.main.id
  role_definition_name = "Monitoring Reader"
  principal_id         = azurerm_user_assigned_identity.keda_scaler.principal_id
}
