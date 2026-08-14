resource "azurerm_redis_cache" "main" {
  count                         = var.redis_service == "azure_cache_legacy" ? 1 : 0
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

resource "azurerm_managed_redis" "main" {
  count                     = var.redis_service == "managed_redis" ? 1 : 0
  name                      = "${local.name}-redis-${random_string.suffix.result}"
  resource_group_name       = azurerm_resource_group.main.name
  location                  = azurerm_resource_group.main.location
  sku_name                  = var.managed_redis_sku
  high_availability_enabled = true
  public_network_access     = local.private_network_enabled ? "Disabled" : "Enabled"
  tags                      = local.tags

  default_database {
    access_keys_authentication_enabled = true
    client_protocol                    = "Encrypted"
    clustering_policy                  = "NoCluster"
  }
}

locals {
  redis_resource_id            = var.redis_service == "managed_redis" ? azurerm_managed_redis.main[0].id : azurerm_redis_cache.main[0].id
  redis_hostname               = var.redis_service == "managed_redis" ? azurerm_managed_redis.main[0].hostname : azurerm_redis_cache.main[0].hostname
  redis_port                   = var.redis_service == "managed_redis" ? azurerm_managed_redis.main[0].default_database[0].port : azurerm_redis_cache.main[0].ssl_port
  redis_access_key             = var.redis_service == "managed_redis" ? azurerm_managed_redis.main[0].default_database[0].primary_access_key : azurerm_redis_cache.main[0].primary_access_key
  redis_private_endpoint_group = var.redis_service == "managed_redis" ? "redisEnterprise" : "redisCache"
  redis_private_dns_zone_name  = var.redis_service == "managed_redis" ? var.managed_redis_private_dns_zone_name : var.private_dns_zone_names.redis
}

moved {
  from = azurerm_redis_cache.main
  to   = azurerm_redis_cache.main[0]
}
