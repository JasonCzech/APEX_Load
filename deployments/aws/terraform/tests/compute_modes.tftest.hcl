mock_provider "aws" {
  mock_data "aws_caller_identity" {
    defaults = {
      account_id = "123456789012"
      arn        = "arn:aws:iam::123456789012:user/test"
      user_id    = "AIDATEST"
    }
  }

  mock_data "aws_partition" {
    defaults = {
      dns_suffix         = "amazonaws.com"
      id                 = "aws"
      partition          = "aws"
      reverse_dns_prefix = "com.amazonaws"
    }
  }

  mock_data "aws_iam_policy_document" {
    defaults = {
      json          = "{\"Version\":\"2012-10-17\",\"Statement\":[]}"
      minified_json = "{\"Version\":\"2012-10-17\",\"Statement\":[]}"
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

mock_provider "tls" {}

variables {
  controller_image        = "123456789012.dkr.ecr.us-east-1.amazonaws.com/apexload/controller@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  generator_image         = "123456789012.dkr.ecr.us-east-1.amazonaws.com/apexload/generator@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
  controller_public_url   = "https://apexload.example.invalid"
  ecs_alb_certificate_arn = "arn:aws:acm:us-east-1:123456789012:certificate/00000000-0000-0000-0000-000000000000"
  generator_gateway_token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
}

# The four EKS-disabled modes below run as complete mocked plans. Modes that
# include EKS target the selected compute roots because the AWS mock provider
# does not synthesize EKS's nested computed OIDC certificate chain; ordinary
# `terraform validate` plus deployments/k8s render tests cover its dependents.

run "eks_eks" {
  command = plan

  plan_options {
    target = [aws_eks_cluster.main[0]]
  }

  variables {
    controller_compute = "eks"
    generator_compute  = "eks"
  }

  assert {
    condition     = length(aws_eks_cluster.main) == 1
    error_message = "EKS/EKS must provision the shared EKS cluster."
  }
}

run "eks_external" {
  command = plan

  plan_options {
    target = [aws_eks_cluster.main[0]]
  }

  variables {
    controller_compute = "eks"
    generator_compute  = "external"
  }

  assert {
    condition     = length(aws_eks_cluster.main) == 1
    error_message = "EKS/external must retain EKS for the controller."
  }
}

run "ecs_ecs" {
  command = plan

  variables {
    controller_compute = "ecs_fargate"
    generator_compute  = "ecs_fargate"
  }

  assert {
    condition     = !local.eks_enabled && local.ecs_controller_enabled && local.ecs_generator_enabled
    error_message = "ECS/ECS must disable EKS and provision both PaaS roles."
  }
}

run "ecs_external" {
  command = plan

  variables {
    controller_compute = "ecs_fargate"
    generator_compute  = "external"
  }

  assert {
    condition     = !local.eks_enabled && local.ecs_controller_enabled && !local.ecs_generator_enabled
    error_message = "ECS/external must disable EKS and omit managed generator compute."
  }
}

run "external_ecs" {
  command = plan

  variables {
    controller_compute = "external"
    generator_compute  = "ecs_fargate"
  }

  assert {
    condition     = !local.eks_enabled && !local.ecs_controller_enabled && local.ecs_generator_enabled
    error_message = "External/ECS must disable EKS and provision only generator compute."
  }
}

run "external_external" {
  command = plan

  variables {
    controller_compute = "external"
    generator_compute  = "external"
  }

  assert {
    condition     = !local.eks_enabled && !local.ecs_enabled
    error_message = "External/external must disable EKS and ECS."
  }
}

run "eks_ecs" {
  command = plan

  plan_options {
    target = [aws_eks_cluster.main[0], aws_ecs_service.generator_warm[0]]
  }

  variables {
    controller_compute = "eks"
    generator_compute  = "ecs_fargate"
  }

  assert {
    condition     = length(aws_eks_cluster.main) == 1 && length(aws_ecs_service.generator_warm) == 1
    error_message = "EKS/ECS must retain EKS and provision the warm generator service."
  }
}

run "ecs_eks" {
  command = plan

  plan_options {
    target = [aws_ecs_service.controller[0], aws_eks_cluster.main[0]]
  }

  variables {
    controller_compute = "ecs_fargate"
    generator_compute  = "eks"
  }

  assert {
    condition     = length(aws_ecs_service.controller) == 1 && length(aws_eks_cluster.main) == 1
    error_message = "ECS/EKS must provision the PaaS controller and retain EKS for generators."
  }
}

run "external_eks" {
  command = plan

  plan_options {
    target = [aws_eks_cluster.main[0]]
  }

  variables {
    controller_compute = "external"
    generator_compute  = "eks"
  }

  assert {
    condition     = length(aws_eks_cluster.main) == 1
    error_message = "External/EKS must retain EKS for generators."
  }
}
