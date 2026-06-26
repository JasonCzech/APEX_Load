resource "azurerm_kubernetes_cluster" "main" {
  name                = "${local.name}-aks"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  dns_prefix          = local.name
  kubernetes_version  = var.kubernetes_version
  sku_tier            = "Standard"
  tags                = local.tags

  # Workload identity: pods exchange federated service-account tokens for
  # Entra ID tokens (Key Vault, Blob Storage) — no secrets in the cluster.
  oidc_issuer_enabled               = true
  workload_identity_enabled         = true
  local_account_disabled            = var.aks_local_account_disabled
  role_based_access_control_enabled = true
  automatic_upgrade_channel         = var.aks_auto_upgrade_channel
  node_os_upgrade_channel           = var.aks_node_os_upgrade_channel

  azure_active_directory_role_based_access_control {
    azure_rbac_enabled     = true
    admin_group_object_ids = var.aks_admin_group_object_ids
  }

  # Managed KEDA add-on drives the generator ScaledObject
  # (deployments/k8s/aks/generator-keda-scaledobject.yaml).
  workload_autoscaler_profile {
    keda_enabled = true
  }

  # Managed NGINX ingress (Web Application Routing) backs the controller
  # Ingress class webapprouting.kubernetes.azure.com.
  web_app_routing {
    dns_zone_ids = []
  }

  default_node_pool {
    name                 = "system"
    node_count           = var.system_node_count
    vm_size              = var.system_node_vm_size
    auto_scaling_enabled = false
    vnet_subnet_id       = local.private_network_enabled ? azurerm_subnet.aks_system[0].id : null
    zones                = var.availability_zones
    upgrade_settings {
      max_surge = "33%"
    }
  }

  identity {
    type = "SystemAssigned"
  }

  network_profile {
    network_plugin      = "azure"
    network_plugin_mode = "overlay"
    # Network policy enforcement for deployments/k8s/aks/network-policy.yaml.
    network_policy     = "cilium"
    network_data_plane = "cilium"
  }

  dynamic "api_server_access_profile" {
    for_each = length(var.authorized_ip_ranges) > 0 ? [1] : []
    content {
      authorized_ip_ranges = var.authorized_ip_ranges
    }
  }
}

# Dedicated autoscaling pool for load generators. The taint + label pair
# matches the nodeSelector/toleration in generator-statefulset.yaml, so load
# generation never competes with the controller or system pods.
resource "azurerm_kubernetes_cluster_node_pool" "generators" {
  name                  = "loadgen"
  kubernetes_cluster_id = azurerm_kubernetes_cluster.main.id
  vm_size               = var.generator_node_vm_size
  auto_scaling_enabled  = true
  min_count             = var.generator_node_min_count
  max_count             = var.generator_node_max_count
  vnet_subnet_id        = local.private_network_enabled ? azurerm_subnet.aks_generators[0].id : null
  mode                  = "User"
  tags                  = local.tags

  node_labels = {
    "apexload.io/node-pool" = "generators"
  }
  zones = var.availability_zones
  node_taints = [
    "apexload.io/dedicated=generators:NoSchedule",
  ]

  lifecycle {
    precondition {
      condition     = local.generator_vm_capacity != null
      error_message = "generator_node_vm_size must be one of the VM sizes in local.generator_vm_capacity_by_size so KEDA/node capacity can be validated."
    }
    precondition {
      condition     = local.generator_schedulable_pods >= var.generator_keda_max_replicas
      error_message = "generator node pool capacity is below the KEDA ceiling. Increase generator_node_max_count or VM size, lower generator_keda_max_replicas, or adjust generator pod request/reservation inputs."
    }
  }
}

resource "azurerm_role_assignment" "current_user_aks_cluster_admin" {
  count                = var.aks_grant_current_user_cluster_admin ? 1 : 0
  scope                = azurerm_kubernetes_cluster.main.id
  role_definition_name = "Azure Kubernetes Service RBAC Cluster Admin"
  principal_id         = data.azurerm_client_config.current.object_id
}
