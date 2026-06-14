resource "azurerm_redis_cache" "main" {
  name                          = "${local.name}-redis-${random_string.suffix.result}"
  resource_group_name           = azurerm_resource_group.main.name
  location                      = azurerm_resource_group.main.location
  capacity                      = var.redis_capacity
  family                        = "C"
  sku_name                      = var.redis_sku
  non_ssl_port_enabled          = false
  minimum_tls_version           = "1.2"
  public_network_access_enabled = !local.private_network_enabled
  tags                          = local.tags

  redis_configuration {
  }
}
