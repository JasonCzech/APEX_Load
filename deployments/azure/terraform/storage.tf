resource "azurerm_storage_account" "main" {
  name                            = "${var.prefix}${var.environment}${random_string.suffix.result}"
  resource_group_name             = azurerm_resource_group.main.name
  location                        = azurerm_resource_group.main.location
  account_tier                    = "Standard"
  account_replication_type        = "ZRS"
  min_tls_version                 = "TLS1_2"
  allow_nested_items_to_be_public = false
  shared_access_key_enabled       = false # workload identity only — no account keys
  public_network_access_enabled   = !local.private_network_enabled
  tags                            = local.tags

  blob_properties {
    versioning_enabled = false
    delete_retention_policy {
      days = 14
    }
  }
}

# Matches APEXLOAD_ARCHIVE_CONTAINER in deployments/k8s/aks/configmap.yaml.
resource "azurerm_storage_container" "archives" {
  name                  = "apexload-archives"
  storage_account_id    = azurerm_storage_account.main.id
  container_access_type = "private"
}
