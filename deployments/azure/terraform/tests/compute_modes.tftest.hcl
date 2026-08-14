mock_provider "azurerm" {
  mock_data "azurerm_client_config" {
    defaults = {
      client_id       = "00000000-0000-0000-0000-000000000001"
      object_id       = "00000000-0000-0000-0000-000000000002"
      subscription_id = "00000000-0000-0000-0000-000000000003"
      tenant_id       = "00000000-0000-0000-0000-000000000004"
    }
  }

  mock_resource "azurerm_managed_redis" {
    defaults = {
      hostname = "mock.redis.azure.net"
      default_database = [{
        port               = 10000
        primary_access_key = "mock-key"
      }]
    }
  }

}

mock_provider "random" {
  mock_resource "random_string" {
    defaults = {
      result = "abcdef"
    }
  }
}

variables {
  controller_image        = "example.invalid/apexload/controller@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  generator_image         = "example.invalid/apexload/generator@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
  controller_public_url   = "https://apexload.example.invalid"
  generator_gateway_token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
}

# The four AKS-disabled modes below run as complete mocked plans. Modes that
# include AKS target the selected compute roots because the AzureRM mock
# provider does not synthesize AKS's nested computed kubelet-identity block;
# ordinary `terraform validate` plus deployments/k8s render tests cover the
# downstream AKS resources.

run "aks_aks" {
  command = plan

  plan_options {
    target = [azurerm_kubernetes_cluster.main[0]]
  }

  variables {
    controller_compute = "aks"
    generator_compute  = "aks"
  }

  assert {
    condition     = length(azurerm_kubernetes_cluster.main) == 1
    error_message = "AKS/AKS must provision the shared AKS cluster."
  }
}

run "aks_external" {
  command = plan

  plan_options {
    target = [azurerm_kubernetes_cluster.main[0]]
  }

  variables {
    controller_compute = "aks"
    generator_compute  = "external"
  }

  assert {
    condition     = length(azurerm_kubernetes_cluster.main) == 1
    error_message = "AKS/external must retain AKS for the controller."
  }
}

run "container_apps_container_apps" {
  command = plan

  variables {
    controller_compute = "container_apps"
    generator_compute  = "container_apps"
  }

  assert {
    condition     = !local.aks_enabled && local.container_apps_controller && local.container_apps_generators
    error_message = "Container Apps/Container Apps must disable AKS and provision both PaaS roles."
  }
}

run "container_apps_external" {
  command = plan

  variables {
    controller_compute = "container_apps"
    generator_compute  = "external"
  }

  assert {
    condition     = !local.aks_enabled && local.container_apps_controller && !local.container_apps_generators
    error_message = "Container Apps/external must disable AKS and omit managed generator compute."
  }
}

run "external_container_apps" {
  command = plan

  variables {
    controller_compute = "external"
    generator_compute  = "container_apps"
  }

  assert {
    condition     = !local.aks_enabled && !local.container_apps_controller && local.container_apps_generators
    error_message = "External/Container Apps must disable AKS and provision only generator compute."
  }
}

run "external_external" {
  command = plan

  variables {
    controller_compute = "external"
    generator_compute  = "external"
  }

  assert {
    condition     = !local.aks_enabled && !local.container_apps_enabled
    error_message = "External/external must disable AKS and Container Apps."
  }
}

run "aks_container_apps" {
  command = plan

  plan_options {
    target = [azurerm_container_app.generator_warm[0], azurerm_kubernetes_cluster.main[0]]
  }

  variables {
    controller_compute = "aks"
    generator_compute  = "container_apps"
  }

  assert {
    condition     = length(azurerm_kubernetes_cluster.main) == 1 && length(azurerm_container_app.generator_warm) == 1
    error_message = "AKS/Container Apps must retain AKS and provision only generator Container Apps resources."
  }
}

run "container_apps_aks" {
  command = plan

  plan_options {
    target = [azurerm_container_app.controller[0], azurerm_kubernetes_cluster.main[0]]
  }

  variables {
    controller_compute = "container_apps"
    generator_compute  = "aks"
  }

  assert {
    condition     = length(azurerm_container_app.controller) == 1 && length(azurerm_kubernetes_cluster.main) == 1
    error_message = "Container Apps/AKS must provision the PaaS controller and retain AKS for generators."
  }
}

run "external_aks" {
  command = plan

  plan_options {
    target = [azurerm_kubernetes_cluster.main[0]]
  }

  variables {
    controller_compute = "external"
    generator_compute  = "aks"
  }

  assert {
    condition     = length(azurerm_kubernetes_cluster.main) == 1
    error_message = "External/AKS must retain AKS for generators."
  }
}

run "container_apps_container_instances" {
  command = plan

  variables {
    controller_compute = "container_apps"
    generator_compute  = "container_instances"
  }

  assert {
    condition     = !local.aks_enabled && local.container_apps_controller && local.container_instances_enabled && !local.container_apps_generators
    error_message = "Container Apps/Container Instances must disable AKS and provision ACI generators instead of Container Apps generators."
  }

  # Direct connectivity is only safe on a delegated subnet, so the two must
  # stay in lockstep: a public container group would expose the generator
  # control port to the internet.
  assert {
    condition     = local.container_instances_direct == local.private_network_enabled
    error_message = "Container Instances direct connectivity must track the delegated-subnet network mode."
  }
}

run "external_container_instances" {
  command = plan

  variables {
    controller_compute = "external"
    generator_compute  = "container_instances"
  }

  assert {
    condition     = !local.aks_enabled && local.container_instances_enabled
    error_message = "external/Container Instances must disable AKS and still provision ACI generators."
  }
}
