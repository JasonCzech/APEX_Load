locals {
  container_apps_enabled     = var.controller_compute == "container_apps" || var.generator_compute == "container_apps"
  container_apps_controller  = var.controller_compute == "container_apps"
  container_apps_generators  = var.generator_compute == "container_apps"
  container_apps_target_name = "${local.name}-container-apps-generators"
  generator_gateway_token    = trimspace(var.generator_gateway_token) != "" ? var.generator_gateway_token : try(random_password.generator_gateway_token[0].result, "")
}

resource "random_password" "generator_gateway_token" {
  count   = local.container_apps_generators && trimspace(var.generator_gateway_token) == "" ? 1 : 0
  length  = 48
  special = false
}

resource "azurerm_container_app_environment" "apexload" {
  count                      = local.container_apps_enabled ? 1 : 0
  name                       = "${local.name}-cae"
  location                   = azurerm_resource_group.main.location
  resource_group_name        = azurerm_resource_group.main.name
  log_analytics_workspace_id = azurerm_log_analytics_workspace.main.id
  infrastructure_subnet_id   = local.private_network_enabled ? azurerm_subnet.container_apps[0].id : null
  tags                       = local.tags
}

resource "azurerm_role_assignment" "controller_container_apps_operator" {
  count                = local.container_apps_controller ? 1 : 0
  scope                = azurerm_container_app_environment.apexload[0].id
  role_definition_name = "Container Apps Contributor"
  principal_id         = azurerm_user_assigned_identity.apexload.principal_id
}

resource "azurerm_role_assignment" "controller_acr_pull" {
  count                = local.container_apps_controller ? 1 : 0
  scope                = azurerm_container_registry.main.id
  role_definition_name = "AcrPull"
  principal_id         = azurerm_user_assigned_identity.apexload.principal_id
}

resource "azurerm_role_assignment" "generator_acr_pull" {
  count                = local.container_apps_generators ? 1 : 0
  scope                = azurerm_container_registry.main.id
  role_definition_name = "AcrPull"
  principal_id         = azurerm_user_assigned_identity.generator.principal_id
}

resource "azurerm_container_app" "controller" {
  count                        = local.container_apps_controller ? 1 : 0
  name                         = "${local.name}-controller"
  container_app_environment_id = azurerm_container_app_environment.apexload[0].id
  resource_group_name          = azurerm_resource_group.main.name
  revision_mode                = "Single"
  tags                         = local.tags

  identity {
    type         = "UserAssigned"
    identity_ids = [azurerm_user_assigned_identity.apexload.id]
  }
  registry {
    server   = azurerm_container_registry.main.login_server
    identity = azurerm_user_assigned_identity.apexload.id
  }
  secret {
    name  = "database-url"
    value = "postgres://${var.postgres_admin_login}:${random_password.postgres.result}@${azurerm_postgresql_flexible_server.main.fqdn}:5432/${azurerm_postgresql_flexible_server_database.apexload.name}?sslmode=require"
  }
  secret {
    name  = "redis-url"
    value = "rediss://:${local.redis_access_key}@${local.redis_hostname}:${local.redis_port}/0"
  }
  ingress {
    external_enabled = true
    target_port      = 8080
    transport        = "auto"
    traffic_weight {
      percentage      = 100
      latest_revision = true
    }
  }
  template {
    min_replicas = 1
    max_replicas = 1
    container {
      name   = "controller"
      image  = var.controller_image
      cpu    = 1
      memory = "2Gi"
      env {
        name  = "APEXLOAD_ADDR"
        value = ":8080"
      }
      env {
        name  = "APEXLOAD_ENVIRONMENT"
        value = "azure_container_apps"
      }
      env {
        name  = "APEXLOAD_ENV"
        value = "production"
      }
      env {
        name  = "APEXLOAD_STORE"
        value = "postgres"
      }
      env {
        name  = "APEXLOAD_PAAS_COMPUTE_ENABLED"
        value = "true"
      }
      env {
        name  = "APEXLOAD_REVERSE_GATEWAY_TRUST_FORWARDED_HTTPS"
        value = "true"
      }
      env {
        name  = "APEXLOAD_GRPC_REVERSE_TUNNEL_SECURE"
        value = "true"
      }
      env {
        name  = "APEXLOAD_CACHE_BACKEND"
        value = "redis"
      }
      env {
        name        = "APEXLOAD_DATABASE_URL"
        secret_name = "database-url"
      }
      env {
        name        = "APEXLOAD_REDIS_URL"
        secret_name = "redis-url"
      }
      env {
        name  = "APEXLOAD_ARCHIVE_BACKEND"
        value = "azure_blob"
      }
      env {
        name  = "AZURE_STORAGE_ACCOUNT"
        value = azurerm_storage_account.main.name
      }
      env {
        name  = "APEXLOAD_ARCHIVE_CONTAINER"
        value = azurerm_storage_container.archives.name
      }
      env {
        name  = "AZURE_CLIENT_ID"
        value = azurerm_user_assigned_identity.apexload.client_id
      }
    }
  }
  lifecycle {
    precondition {
      condition     = var.controller_image != ""
      error_message = "controller_image is required when controller_compute=container_apps."
    }
  }
}

resource "azurerm_container_app" "generator_warm" {
  count                        = local.container_apps_generators ? 1 : 0
  name                         = "${local.name}-generator-warm"
  container_app_environment_id = azurerm_container_app_environment.apexload[0].id
  resource_group_name          = azurerm_resource_group.main.name
  revision_mode                = "Single"
  tags                         = local.tags
  identity {
    type         = "UserAssigned"
    identity_ids = [azurerm_user_assigned_identity.generator.id]
  }
  registry {
    server   = azurerm_container_registry.main.login_server
    identity = azurerm_user_assigned_identity.generator.id
  }
  secret {
    name  = "gateway-token"
    value = local.generator_gateway_token
  }
  template {
    min_replicas = 1
    max_replicas = 1
    container {
      name   = "generator"
      image  = var.generator_image
      cpu    = 1
      memory = "2Gi"
      env {
        name  = "APEXLOAD_CONTROLLER_URL"
        value = var.controller_public_url
      }
      env {
        name  = "APEXLOAD_EXECUTION_TARGET_ID"
        value = local.container_apps_target_name
      }
      env {
        name        = "APEXLOAD_GENERATOR_GATEWAY_TOKEN"
        secret_name = "gateway-token"
      }
      env {
        name  = "APEXLOAD_GENERATOR_CONNECTIVITY_MODE"
        value = "reverse"
      }
      env {
        name  = "APEXLOAD_GRPC_REVERSE_TUNNEL_SECURE"
        value = "true"
      }
      env {
        name  = "APEXLOAD_ENV"
        value = "production"
      }
      env {
        name  = "AZURE_CLIENT_ID"
        value = azurerm_user_assigned_identity.generator.client_id
      }
    }
  }
  lifecycle {
    precondition {
      condition     = var.generator_image != "" && startswith(var.controller_public_url, "https://")
      error_message = "generator_image and an HTTPS controller_public_url are required for Container Apps generators."
    }
  }
}

resource "azurerm_container_app_job" "generator_burst" {
  count                        = local.container_apps_generators ? 1 : 0
  name                         = "${local.name}-generator-burst"
  location                     = azurerm_resource_group.main.location
  resource_group_name          = azurerm_resource_group.main.name
  container_app_environment_id = azurerm_container_app_environment.apexload[0].id
  replica_timeout_in_seconds   = 14400
  replica_retry_limit          = 1
  tags                         = local.tags
  identity {
    type         = "UserAssigned"
    identity_ids = [azurerm_user_assigned_identity.generator.id]
  }
  registry {
    server   = azurerm_container_registry.main.login_server
    identity = azurerm_user_assigned_identity.generator.id
  }
  secret {
    name  = "gateway-token"
    value = local.generator_gateway_token
  }
  manual_trigger_config {
    parallelism              = 1
    replica_completion_count = 1
  }
  template {
    container {
      name   = "generator"
      image  = var.generator_image
      cpu    = 1
      memory = "2Gi"
      env {
        name  = "APEXLOAD_CONTROLLER_URL"
        value = var.controller_public_url
      }
      env {
        name  = "APEXLOAD_EXECUTION_TARGET_ID"
        value = local.container_apps_target_name
      }
      env {
        name        = "APEXLOAD_GENERATOR_GATEWAY_TOKEN"
        secret_name = "gateway-token"
      }
      env {
        name  = "APEXLOAD_GENERATOR_CONNECTIVITY_MODE"
        value = "reverse"
      }
      env {
        name  = "APEXLOAD_GRPC_REVERSE_TUNNEL_SECURE"
        value = "true"
      }
      env {
        name  = "APEXLOAD_ENV"
        value = "production"
      }
      env {
        name  = "AZURE_CLIENT_ID"
        value = azurerm_user_assigned_identity.generator.client_id
      }
    }
  }
}

output "container_apps_registration_manifest" {
  description = "Sensitive one-time JSON import for the Infrastructure & Fleet UI. It contains no cloud credential, but does contain the reverse enrollment token."
  sensitive   = true
  value = local.container_apps_generators ? jsonencode({
    cloud_connection = {
      id                    = "${local.name}-azure"
      name                  = "${local.name} Azure"
      provider              = "azure"
      auth_mode             = "azure_identity"
      azure_cloud           = "public"
      azure_subscription_id = data.azurerm_client_config.current.subscription_id
      azure_resource_group  = azurerm_resource_group.main.name
      status                = "active"
    }
    execution_targets = [{
      id                = local.container_apps_target_name
      name              = local.container_apps_target_name
      kind              = "azure_container_apps"
      connectivity_mode = "reverse"
      standing_owner    = "apex"
      burst_owner       = "apex"
      archive_mode      = "controller_pull"
      enrollment_token  = local.generator_gateway_token
      status            = "active"
      azure_container_apps = {
        resource_group        = azurerm_resource_group.main.name
        environment_name      = azurerm_container_app_environment.apexload[0].name
        warm_container_app    = azurerm_container_app.generator_warm[0].name
        burst_job             = azurerm_container_app_job.generator_burst[0].name
        expected_principal_id = azurerm_user_assigned_identity.generator.principal_id
      }
    }]
  }) : null
}
