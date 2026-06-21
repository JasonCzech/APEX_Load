# Azure Event Hubs (opt-in via var.eventhubs_enabled). Provisions a namespace +
# event hub with separate Send (producer) and Listen (observer probe) SAS rules,
# stores both connection strings in Key Vault for {{secret.*}} resolution, and
# grants AAD/workload-identity data-plane roles so the controller/generators can
# authenticate without connection strings (auth_type: aad).

resource "azurerm_eventhub_namespace" "main" {
  count                         = var.eventhubs_enabled ? 1 : 0
  name                          = "${local.name}-ehns-${random_string.suffix.result}"
  resource_group_name           = azurerm_resource_group.main.name
  location                      = azurerm_resource_group.main.location
  sku                           = var.eventhubs_sku
  capacity                      = var.eventhubs_capacity
  public_network_access_enabled = !local.private_network_enabled
  tags                          = local.tags
}

resource "azurerm_eventhub" "main" {
  count             = var.eventhubs_enabled ? 1 : 0
  name              = var.eventhubs_name
  namespace_id      = azurerm_eventhub_namespace.main[0].id
  partition_count   = var.eventhubs_partition_count
  message_retention = 1
}

# Producers send to the hub; the observer probe listens. Split rules so the
# generator fleet's load credential cannot also drain the hub.
resource "azurerm_eventhub_authorization_rule" "send" {
  count               = var.eventhubs_enabled ? 1 : 0
  name                = "apexload-send"
  namespace_name      = azurerm_eventhub_namespace.main[0].name
  eventhub_name       = azurerm_eventhub.main[0].name
  resource_group_name = azurerm_resource_group.main.name
  listen              = false
  send                = true
  manage              = false
}

resource "azurerm_eventhub_authorization_rule" "listen" {
  count               = var.eventhubs_enabled ? 1 : 0
  name                = "apexload-listen"
  namespace_name      = azurerm_eventhub_namespace.main[0].name
  eventhub_name       = azurerm_eventhub.main[0].name
  resource_group_name = azurerm_resource_group.main.name
  listen              = true
  send                = false
  manage              = false
}

# Connection strings -> Key Vault (referenced as
# config.eventhubs.connection_string_secret in scripts that use SAS auth).
resource "azurerm_key_vault_secret" "eventhubs_send" {
  count        = var.eventhubs_enabled ? 1 : 0
  name         = "eventhubs-send-connection-string"
  value        = azurerm_eventhub_authorization_rule.send[0].primary_connection_string
  key_vault_id = azurerm_key_vault.main.id
  tags         = local.tags

  depends_on = [azurerm_role_assignment.deployer_keyvault_admin]
}

resource "azurerm_key_vault_secret" "eventhubs_listen" {
  count        = var.eventhubs_enabled ? 1 : 0
  name         = "eventhubs-listen-connection-string"
  value        = azurerm_eventhub_authorization_rule.listen[0].primary_connection_string
  key_vault_id = azurerm_key_vault.main.id
  tags         = local.tags

  depends_on = [azurerm_role_assignment.deployer_keyvault_admin]
}

# AAD data-plane roles for connection-string-free auth (auth_type: aad). The
# controller and generators produce; only the generator path receives (probe).
resource "azurerm_role_assignment" "controller_eventhubs_sender" {
  count                = var.eventhubs_enabled ? 1 : 0
  scope                = azurerm_eventhub_namespace.main[0].id
  role_definition_name = "Azure Event Hubs Data Sender"
  principal_id         = azurerm_user_assigned_identity.apexload.principal_id
}

resource "azurerm_role_assignment" "generator_eventhubs_sender" {
  count                = var.eventhubs_enabled ? 1 : 0
  scope                = azurerm_eventhub_namespace.main[0].id
  role_definition_name = "Azure Event Hubs Data Sender"
  principal_id         = azurerm_user_assigned_identity.generator.principal_id
}

resource "azurerm_role_assignment" "generator_eventhubs_receiver" {
  count                = var.eventhubs_enabled ? 1 : 0
  scope                = azurerm_eventhub_namespace.main[0].id
  role_definition_name = "Azure Event Hubs Data Receiver"
  principal_id         = azurerm_user_assigned_identity.generator.principal_id
}
