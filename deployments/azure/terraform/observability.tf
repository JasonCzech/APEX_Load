resource "azurerm_log_analytics_workspace" "main" {
  name                = "${local.name}-logs"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  sku                 = "PerGB2018"
  retention_in_days   = 30
  tags                = local.tags
}

resource "azurerm_monitor_diagnostic_setting" "aks" {
  count                      = local.aks_enabled ? 1 : 0
  name                       = "${local.name}-aks-diagnostics"
  target_resource_id         = azurerm_kubernetes_cluster.main[0].id
  log_analytics_workspace_id = azurerm_log_analytics_workspace.main.id

  dynamic "enabled_log" {
    for_each = toset([
      "kube-apiserver",
      "kube-audit",
      "kube-audit-admin",
      "kube-controller-manager",
      "kube-scheduler",
      "cluster-autoscaler",
      "cloud-controller-manager",
      "guard",
    ])
    content {
      category = enabled_log.value
    }
  }

  enabled_metric {
    category = "AllMetrics"
  }
}

moved {
  from = azurerm_monitor_diagnostic_setting.aks
  to   = azurerm_monitor_diagnostic_setting.aks[0]
}

resource "azurerm_monitor_diagnostic_setting" "acr" {
  name                       = "${local.name}-acr-diagnostics"
  target_resource_id         = azurerm_container_registry.main.id
  log_analytics_workspace_id = azurerm_log_analytics_workspace.main.id

  dynamic "enabled_log" {
    for_each = toset([
      "ContainerRegistryLoginEvents",
      "ContainerRegistryRepositoryEvents",
    ])
    content {
      category = enabled_log.value
    }
  }

  enabled_metric {
    category = "AllMetrics"
  }
}

resource "azurerm_monitor_diagnostic_setting" "postgres" {
  name                       = "${local.name}-postgres-diagnostics"
  target_resource_id         = azurerm_postgresql_flexible_server.main.id
  log_analytics_workspace_id = azurerm_log_analytics_workspace.main.id

  enabled_log {
    category = "PostgreSQLLogs"
  }

  enabled_metric {
    category = "AllMetrics"
  }
}

resource "azurerm_monitor_diagnostic_setting" "redis" {
  name                       = "${local.name}-redis-diagnostics"
  target_resource_id         = local.redis_resource_id
  log_analytics_workspace_id = azurerm_log_analytics_workspace.main.id

  enabled_metric {
    category = "AllMetrics"
  }
}

resource "azurerm_monitor_diagnostic_setting" "storage_account" {
  name                       = "${local.name}-storage-diagnostics"
  target_resource_id         = azurerm_storage_account.main.id
  log_analytics_workspace_id = azurerm_log_analytics_workspace.main.id

  enabled_metric {
    category = "AllMetrics"
  }
}

resource "azurerm_monitor_diagnostic_setting" "storage_blob" {
  name                       = "${local.name}-blob-diagnostics"
  target_resource_id         = "${azurerm_storage_account.main.id}/blobServices/default"
  log_analytics_workspace_id = azurerm_log_analytics_workspace.main.id

  dynamic "enabled_log" {
    for_each = toset([
      "StorageRead",
      "StorageWrite",
      "StorageDelete",
    ])
    content {
      category = enabled_log.value
    }
  }

  enabled_metric {
    category = "AllMetrics"
  }
}

resource "azurerm_monitor_diagnostic_setting" "keyvault" {
  name                       = "${local.name}-keyvault-diagnostics"
  target_resource_id         = azurerm_key_vault.main.id
  log_analytics_workspace_id = azurerm_log_analytics_workspace.main.id

  enabled_log {
    category = "AuditEvent"
  }

  enabled_metric {
    category = "AllMetrics"
  }
}
