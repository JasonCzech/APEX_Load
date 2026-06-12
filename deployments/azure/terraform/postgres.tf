resource "random_password" "postgres" {
  length  = 32
  special = false
}

resource "azurerm_postgresql_flexible_server" "main" {
  name                   = "${local.name}-pg-${random_string.suffix.result}"
  resource_group_name    = azurerm_resource_group.main.name
  location               = azurerm_resource_group.main.location
  version                = "16"
  administrator_login    = var.postgres_admin_login
  administrator_password = random_password.postgres.result
  sku_name               = var.postgres_sku_name
  storage_mb             = var.postgres_storage_mb
  backup_retention_days  = 14
  zone                   = "1"
  tags                   = local.tags

  # For stricter isolation move to delegated-subnet + private DNS; this setup
  # uses the public endpoint restricted by the firewall rules below.
}

resource "azurerm_postgresql_flexible_server_database" "apexload" {
  name      = "apexload"
  server_id = azurerm_postgresql_flexible_server.main.id
  collation = "en_US.utf8"
  charset   = "utf8"
}

# Allow Azure-internal traffic (covers AKS egress without pinning node IPs).
resource "azurerm_postgresql_flexible_server_firewall_rule" "azure_services" {
  name             = "allow-azure-services"
  server_id        = azurerm_postgresql_flexible_server.main.id
  start_ip_address = "0.0.0.0"
  end_ip_address   = "0.0.0.0"
}

resource "azurerm_postgresql_flexible_server_firewall_rule" "extra" {
  for_each         = var.postgres_allowed_cidrs
  name             = each.key
  server_id        = azurerm_postgresql_flexible_server.main.id
  start_ip_address = cidrhost(each.value, 0)
  end_ip_address   = cidrhost(each.value, pow(2, 32 - tonumber(split("/", each.value)[1])) - 1)
}
