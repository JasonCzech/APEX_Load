# Each output maps onto a value in deployments/k8s/aks (configmap.yaml,
# serviceaccounts.yaml, kustomization.yaml) — see the README for the wiring.

output "resource_group_name" {
  value = azurerm_resource_group.main.name
}

output "aks_cluster_name" {
  value = azurerm_kubernetes_cluster.main.name
}

output "aks_oidc_issuer_url" {
  value = azurerm_kubernetes_cluster.main.oidc_issuer_url
}

output "acr_login_server" {
  description = "kustomization.yaml images newName prefix; docker push target."
  value       = azurerm_container_registry.main.login_server
}

output "workload_identity_client_id" {
  description = "Backward-compatible alias for the controller workload identity client ID."
  value       = azurerm_user_assigned_identity.apexload.client_id
}

output "controller_workload_identity_client_id" {
  description = "azure.workload.identity/client-id annotation on the controller service account."
  value       = azurerm_user_assigned_identity.apexload.client_id
}

output "generator_workload_identity_client_id" {
  description = "azure.workload.identity/client-id annotation on the generator service account."
  value       = azurerm_user_assigned_identity.generator.client_id
}

output "keda_workload_identity_client_id" {
  description = "identityId for the KEDA Azure Monitor TriggerAuthentication."
  value       = azurerm_user_assigned_identity.keda_scaler.client_id
}

output "network_mode" {
  value = var.network_mode
}

output "postgres_fqdn" {
  value = azurerm_postgresql_flexible_server.main.fqdn
}

output "apexload_database_url" {
  description = "APEXLOAD_DATABASE_URL for the apexload-data-platform Secret."
  value       = "postgres://${var.postgres_admin_login}:${random_password.postgres.result}@${azurerm_postgresql_flexible_server.main.fqdn}:5432/${azurerm_postgresql_flexible_server_database.apexload.name}?sslmode=require"
  sensitive   = true
}

output "apexload_redis_url" {
  description = "APEXLOAD_REDIS_URL for the apexload-data-platform Secret (TLS port)."
  value       = "rediss://:${azurerm_redis_cache.main.primary_access_key}@${azurerm_redis_cache.main.hostname}:${azurerm_redis_cache.main.ssl_port}/0"
  sensitive   = true
}

output "storage_account_name" {
  description = "AZURE_STORAGE_ACCOUNT in the apexload-data-platform ConfigMap."
  value       = azurerm_storage_account.main.name
}

output "archive_container_name" {
  description = "APEXLOAD_ARCHIVE_CONTAINER in the apexload-data-platform ConfigMap."
  value       = azurerm_storage_container.archives.name
}

output "key_vault_url" {
  description = "APEXLOAD_AZURE_KEY_VAULT_URL in the apexload-data-platform ConfigMap."
  value       = azurerm_key_vault.main.vault_uri
}

output "eventhubs_namespace" {
  description = "APEXLOAD_EVENTHUBS_NAMESPACE (FQDN) for the data-platform ConfigMap; null when eventhubs_enabled=false."
  value       = var.eventhubs_enabled ? "${azurerm_eventhub_namespace.main[0].name}.servicebus.windows.net" : null
}

output "eventhubs_name" {
  description = "Event hub name for config.eventhubs.event_hub; null when eventhubs_enabled=false."
  value       = var.eventhubs_enabled ? azurerm_eventhub.main[0].name : null
}

output "kubeconfig_command" {
  value = "az aks get-credentials --resource-group ${azurerm_resource_group.main.name} --name ${azurerm_kubernetes_cluster.main.name}"
}
