resource "azurerm_key_vault" "main" {
  name                          = "${var.prefix}${var.environment}${random_string.suffix.result}"
  resource_group_name           = azurerm_resource_group.main.name
  location                      = azurerm_resource_group.main.location
  tenant_id                     = data.azurerm_client_config.current.tenant_id
  sku_name                      = "standard"
  soft_delete_retention_days    = 30
  purge_protection_enabled      = true
  rbac_authorization_enabled    = true
  public_network_access_enabled = !local.private_network_enabled
  tags                          = local.tags

  network_acls {
    default_action = local.private_network_enabled || length(var.public_network_allowed_cidrs) > 0 ? "Deny" : "Allow"
    ip_rules       = local.private_network_enabled ? [] : var.public_network_allowed_cidrs
    bypass         = "AzureServices"
  }
}

# Let whoever runs terraform manage secrets (seed {{secret.*}} values for scripts).
resource "azurerm_role_assignment" "deployer_keyvault_admin" {
  scope                = azurerm_key_vault.main.id
  role_definition_name = "Key Vault Secrets Officer"
  principal_id         = data.azurerm_client_config.current.object_id
}
