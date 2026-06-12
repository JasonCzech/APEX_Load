# One user-assigned identity shared by the controller and generator service
# accounts. Split into two identities if you want generators limited to Key
# Vault only (no Blob access).
resource "azurerm_user_assigned_identity" "apexload" {
  name                = "${local.name}-workload"
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
  parent_id           = azurerm_user_assigned_identity.apexload.id
  audience            = ["api://AzureADTokenExchange"]
  issuer              = azurerm_kubernetes_cluster.main.oidc_issuer_url
  subject             = "system:serviceaccount:${local.k8s_namespace}:${local.generator_service_account}"
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
