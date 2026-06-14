resource "azurerm_kubernetes_cluster" "main" {
  name                = "${local.name}-aks"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  dns_prefix          = local.name
  kubernetes_version  = var.kubernetes_version
  tags                = local.tags

  # Workload identity: pods exchange federated service-account tokens for
  # Entra ID tokens (Key Vault, Blob Storage) — no secrets in the cluster.
  oidc_issuer_enabled       = true
  workload_identity_enabled = true

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
  node_taints = [
    "apexload.io/dedicated=generators:NoSchedule",
  ]
}
