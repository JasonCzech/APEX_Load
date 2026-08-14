locals {
  container_instances_enabled     = var.generator_compute == "container_instances"
  container_instances_target_name = "${local.name}-container-instances-generators"
  # ACI is burst-only, so it reuses the reverse enrollment token only when the
  # operator chooses reverse connectivity. On a delegated subnet the controller
  # dials the group's own private IP instead, which is ACI's reason to exist
  # beside Container Apps.
  container_instances_direct = local.private_network_enabled
}

# The template container group is never started by Terraform. APEX Load clones
# its shape per run, so this resource exists to pin image, identity, resources,
# and network placement under the operator's control rather than the app's.
resource "azurerm_container_group" "generator_template" {
  count               = local.container_instances_enabled ? 1 : 0
  name                = "${local.name}-generator-template"
  location            = azurerm_resource_group.main.location
  resource_group_name = azurerm_resource_group.main.name
  os_type             = "Linux"
  restart_policy      = "Never"
  tags                = local.tags

  # A private group has no public endpoint at all; a public one would put the
  # generator's gRPC control port on the internet.
  ip_address_type = local.container_instances_direct ? "Private" : "None"
  subnet_ids      = local.container_instances_direct ? [azurerm_subnet.container_instances[0].id] : null

  identity {
    type         = "UserAssigned"
    identity_ids = [azurerm_user_assigned_identity.generator.id]
  }

  image_registry_credential {
    server                    = azurerm_container_registry.main.login_server
    user_assigned_identity_id = azurerm_user_assigned_identity.generator.id
  }

  container {
    name   = "generator"
    image  = var.generator_image
    cpu    = 1
    memory = 2

    ports {
      port     = 9090
      protocol = "TCP"
    }

    environment_variables = {
      APEXLOAD_EXECUTION_TARGET_ID         = local.container_instances_target_name
      APEXLOAD_GENERATOR_CONNECTIVITY_MODE = local.container_instances_direct ? "direct" : "reverse"
      APEXLOAD_GENERATOR_LIFECYCLE         = "burst"
      APEXLOAD_ENV                         = "production"
      AZURE_CLIENT_ID                      = azurerm_user_assigned_identity.generator.client_id
      APEXLOAD_CONTROLLER_URL              = var.controller_public_url
    }

    secure_environment_variables = local.container_instances_direct ? {} : {
      APEXLOAD_GENERATOR_GATEWAY_TOKEN = local.generator_gateway_token
    }
  }

  lifecycle {
    precondition {
      condition     = var.generator_image != ""
      error_message = "generator_image is required for Container Instances generators."
    }
    precondition {
      # Reverse mode is the only safe option without a delegated subnet, and it
      # needs the controller URL to dial out.
      condition     = local.container_instances_direct || startswith(var.controller_public_url, "https://")
      error_message = "Container Instances without a delegated subnet must reverse-connect, which requires an HTTPS controller_public_url."
    }
  }
}

output "container_instances_registration_manifest" {
  description = "Sensitive one-time JSON import for the Infrastructure & Fleet UI. It contains no cloud credential; the reverse enrollment token is present only when the target reverse-connects."
  sensitive   = true
  value = local.container_instances_enabled ? jsonencode({
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
      id                = local.container_instances_target_name
      name              = local.container_instances_target_name
      kind              = "azure_container_instances"
      connectivity_mode = local.container_instances_direct ? "direct" : "reverse"
      # A container group is one instance, so there is no standing capacity for
      # APEX Load to own.
      standing_owner   = "external"
      burst_owner      = "apex"
      archive_mode     = "controller_pull"
      enrollment_token = local.container_instances_direct ? null : local.generator_gateway_token
      status           = "active"
      azure_container_instances = {
        resource_group           = azurerm_resource_group.main.name
        container_group_template = azurerm_container_group.generator_template[0].name
        subnet_id                = local.container_instances_direct ? azurerm_subnet.container_instances[0].id : null
        location                 = azurerm_resource_group.main.location
        expected_principal_id    = azurerm_user_assigned_identity.generator.principal_id
      }
    }]
  }) : null
}
