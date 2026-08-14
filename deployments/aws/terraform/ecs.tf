locals {
  ecs_controller_enabled  = var.controller_compute == "ecs_fargate"
  ecs_generator_enabled   = var.generator_compute == "ecs_fargate"
  ecs_generator_reverse   = local.ecs_generator_enabled && (var.ecs_generator_connectivity_mode == "reverse" || (var.ecs_generator_connectivity_mode == "auto" && local.ecs_controller_enabled))
  ecs_enabled             = local.ecs_controller_enabled || local.ecs_generator_enabled
  generator_gateway_token = trimspace(var.generator_gateway_token) != "" ? var.generator_gateway_token : try(random_password.generator_gateway_token[0].result, "")
}

resource "random_password" "generator_gateway_token" {
  count   = local.ecs_generator_reverse && trimspace(var.generator_gateway_token) == "" ? 1 : 0
  length  = 48
  special = false
}

resource "aws_ecs_cluster" "apexload" {
  count = local.ecs_enabled ? 1 : 0
  name  = "${local.name}-fargate"
  setting {
    name  = "containerInsights"
    value = "enabled"
  }
  tags = local.tags
}

data "aws_iam_policy_document" "ecs_tasks_assume_role" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "ecs_execution" {
  count              = local.ecs_enabled ? 1 : 0
  name               = "${local.name}-ecs-execution"
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks_assume_role.json
  tags               = local.tags
}

resource "aws_iam_role_policy_attachment" "ecs_execution" {
  count      = local.ecs_enabled ? 1 : 0
  role       = aws_iam_role.ecs_execution[0].name
  policy_arn = "arn:${data.aws_partition.current.partition}:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

data "aws_iam_policy_document" "ecs_execution_secrets" {
  statement {
    effect  = "Allow"
    actions = ["secretsmanager:GetSecretValue"]
    resources = compact([
      aws_secretsmanager_secret.database_url.arn,
      aws_secretsmanager_secret.redis_url.arn,
      try(aws_secretsmanager_secret.generator_gateway_token[0].arn, ""),
    ])
  }
  statement {
    effect    = "Allow"
    actions   = ["kms:Decrypt"]
    resources = [aws_kms_key.storage.arn]
  }
}

resource "aws_iam_role_policy" "ecs_execution_secrets" {
  count  = local.ecs_enabled ? 1 : 0
  name   = "${local.name}-ecs-execution-secrets"
  role   = aws_iam_role.ecs_execution[0].id
  policy = data.aws_iam_policy_document.ecs_execution_secrets.json
}

resource "aws_iam_role" "ecs_controller" {
  count              = local.ecs_controller_enabled ? 1 : 0
  name               = "${local.name}-ecs-controller"
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks_assume_role.json
  tags               = local.tags
}

resource "aws_iam_role_policy" "ecs_controller_data" {
  count  = local.ecs_controller_enabled ? 1 : 0
  name   = "${local.name}-ecs-controller-data"
  role   = aws_iam_role.ecs_controller[0].id
  policy = data.aws_iam_policy_document.controller.json
}

data "aws_iam_policy_document" "ecs_capacity_control" {
  statement {
    effect = "Allow"
    actions = [
      "ecs:DescribeServices", "ecs:UpdateService", "ecs:DescribeTaskDefinition",
      "ecs:RunTask", "ecs:StopTask", "ecs:ListTasks", "ecs:DescribeTasks", "ecs:ListTagsForResource"
    ]
    resources = ["*"]
  }
  statement {
    effect    = "Allow"
    actions   = ["iam:PassRole"]
    resources = compact([try(aws_iam_role.ecs_generator[0].arn, ""), try(aws_iam_role.ecs_execution[0].arn, "")])
  }
}

resource "aws_iam_role_policy" "ecs_capacity_control" {
  count  = local.ecs_controller_enabled ? 1 : 0
  name   = "${local.name}-ecs-capacity"
  role   = aws_iam_role.ecs_controller[0].id
  policy = data.aws_iam_policy_document.ecs_capacity_control.json
}

resource "aws_iam_role" "ecs_generator" {
  count              = local.ecs_generator_enabled ? 1 : 0
  name               = "${local.name}-ecs-generator"
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks_assume_role.json
  tags               = local.tags
}

resource "aws_iam_role_policy" "ecs_generator_data" {
  count  = local.ecs_generator_enabled ? 1 : 0
  name   = "${local.name}-ecs-generator-data"
  role   = aws_iam_role.ecs_generator[0].id
  policy = data.aws_iam_policy_document.generator.json
}

resource "aws_cloudwatch_log_group" "ecs_controller" {
  count             = local.ecs_controller_enabled ? 1 : 0
  name              = "/apexload/${local.name}/controller"
  retention_in_days = 30
  kms_key_id        = aws_kms_key.storage.arn
  tags              = local.tags
}

resource "aws_cloudwatch_log_group" "ecs_generator" {
  count             = local.ecs_generator_enabled ? 1 : 0
  name              = "/apexload/${local.name}/generator"
  retention_in_days = 30
  kms_key_id        = aws_kms_key.storage.arn
  tags              = local.tags
}

resource "aws_secretsmanager_secret" "generator_gateway_token" {
  count      = local.ecs_generator_reverse ? 1 : 0
  name       = "${local.name}/generator-gateway-token"
  kms_key_id = aws_kms_key.storage.arn
  tags       = local.tags
}

resource "aws_secretsmanager_secret_version" "generator_gateway_token" {
  count         = local.ecs_generator_reverse ? 1 : 0
  secret_id     = aws_secretsmanager_secret.generator_gateway_token[0].id
  secret_string = local.generator_gateway_token
}

resource "aws_security_group" "ecs_controller" {
  count       = local.ecs_controller_enabled ? 1 : 0
  name        = "${local.name}-ecs-controller"
  description = "Controller ingress from ALB and VPC egress"
  vpc_id      = aws_vpc.main.id
  ingress {
    from_port       = 8080
    to_port         = 8080
    protocol        = "tcp"
    security_groups = [aws_security_group.ecs_alb[0].id]
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
  tags = local.tags
}

resource "aws_security_group" "ecs_generator" {
  count       = local.ecs_generator_enabled ? 1 : 0
  name        = "${local.name}-ecs-generator"
  description = "Generator direct gRPC inside VPC plus outbound reverse connectivity"
  vpc_id      = aws_vpc.main.id
  ingress {
    from_port   = 9090
    to_port     = 9090
    protocol    = "tcp"
    cidr_blocks = [var.vpc_cidr]
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
  tags = local.tags
}

resource "aws_security_group" "ecs_alb" {
  count       = local.ecs_controller_enabled ? 1 : 0
  name        = "${local.name}-ecs-alb"
  description = "HTTPS ingress for controller API and reverse WebSockets"
  vpc_id      = aws_vpc.main.id
  ingress {
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
  ingress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
  tags = local.tags
}

resource "aws_lb" "ecs_controller" {
  count              = local.ecs_controller_enabled ? 1 : 0
  name               = substr("${local.name}-controller", 0, 32)
  load_balancer_type = "application"
  subnets            = aws_subnet.public[*].id
  security_groups    = [aws_security_group.ecs_alb[0].id]
  idle_timeout       = 3600
  tags               = local.tags
}

resource "aws_lb_target_group" "ecs_controller" {
  count       = local.ecs_controller_enabled ? 1 : 0
  name        = substr("${local.name}-controller", 0, 32)
  port        = 8080
  protocol    = "HTTP"
  target_type = "ip"
  vpc_id      = aws_vpc.main.id
  health_check {
    path    = "/api/v1/system/health"
    matcher = "200-399"
  }
  tags = local.tags
}

resource "aws_lb_listener" "ecs_controller" {
  count             = local.ecs_controller_enabled ? 1 : 0
  load_balancer_arn = aws_lb.ecs_controller[0].arn
  port              = 80
  protocol          = "HTTP"
  default_action {
    type = "redirect"
    redirect {
      port        = "443"
      protocol    = "HTTPS"
      status_code = "HTTP_301"
    }
  }
}

resource "aws_lb_listener" "ecs_controller_https" {
  count             = local.ecs_controller_enabled ? 1 : 0
  load_balancer_arn = aws_lb.ecs_controller[0].arn
  port              = 443
  protocol          = "HTTPS"
  certificate_arn   = var.ecs_alb_certificate_arn
  ssl_policy        = "ELBSecurityPolicy-TLS13-1-2-2021-06"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.ecs_controller[0].arn
  }

  lifecycle {
    precondition {
      condition     = var.ecs_alb_certificate_arn != ""
      error_message = "ecs_alb_certificate_arn is required when controller_compute=ecs_fargate."
    }
  }
}

resource "aws_ecs_task_definition" "controller" {
  count                    = local.ecs_controller_enabled ? 1 : 0
  family                   = "${local.name}-controller"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = "1024"
  memory                   = "2048"
  execution_role_arn       = aws_iam_role.ecs_execution[0].arn
  task_role_arn            = aws_iam_role.ecs_controller[0].arn
  container_definitions = jsonencode([{
    name         = "controller", image = var.controller_image, essential = true,
    portMappings = [{ containerPort = 8080, protocol = "tcp" }],
    environment = [
      { name = "APEXLOAD_ADDR", value = ":8080" },
      { name = "APEXLOAD_ENVIRONMENT", value = "aws_ecs_fargate" },
      { name = "APEXLOAD_ENV", value = "production" },
      { name = "APEXLOAD_STORE", value = "postgres" },
      { name = "APEXLOAD_PAAS_COMPUTE_ENABLED", value = "true" },
      { name = "APEXLOAD_REVERSE_GATEWAY_TRUST_FORWARDED_HTTPS", value = "true" },
      { name = "APEXLOAD_GRPC_REVERSE_TUNNEL_SECURE", value = "true" },
      { name = "APEXLOAD_CACHE_BACKEND", value = "redis" },
      { name = "APEXLOAD_ARCHIVE_BACKEND", value = "s3" },
      { name = "APEXLOAD_ARCHIVE_BUCKET", value = aws_s3_bucket.archives.bucket },
      { name = "APEXLOAD_ARCHIVE_REGION", value = var.aws_region },
      { name = "AWS_REGION", value = var.aws_region },
    ],
    secrets = [
      { name = "APEXLOAD_DATABASE_URL", valueFrom = aws_secretsmanager_secret.database_url.arn },
      { name = "APEXLOAD_REDIS_URL", valueFrom = aws_secretsmanager_secret.redis_url.arn },
    ],
    logConfiguration = { logDriver = "awslogs", options = { "awslogs-group" = aws_cloudwatch_log_group.ecs_controller[0].name, "awslogs-region" = var.aws_region, "awslogs-stream-prefix" = "controller" } }
  }])
  lifecycle {
    precondition {
      condition     = var.controller_image != ""
      error_message = "controller_image is required when controller_compute=ecs_fargate."
    }
  }
  tags = local.tags
}

resource "aws_ecs_task_definition" "generator" {
  count                    = local.ecs_generator_enabled ? 1 : 0
  family                   = "${local.name}-generator"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = "1024"
  memory                   = "2048"
  execution_role_arn       = aws_iam_role.ecs_execution[0].arn
  task_role_arn            = aws_iam_role.ecs_generator[0].arn
  container_definitions = jsonencode([{
    name         = "generator", image = var.generator_image, essential = true,
    portMappings = [{ containerPort = 9090, protocol = "tcp" }],
    environment = [
      { name = "APEXLOAD_GENERATOR_CONNECTIVITY_MODE", value = local.ecs_generator_reverse ? "reverse" : "direct" },
      { name = "APEXLOAD_GRPC_REVERSE_TUNNEL_SECURE", value = local.ecs_generator_reverse ? "true" : "false" },
      { name = "APEXLOAD_CONTROLLER_URL", value = local.ecs_generator_reverse ? var.controller_public_url : "" },
      { name = "APEXLOAD_EXECUTION_TARGET_ID", value = "${local.name}-ecs-generators" },
      { name = "APEXLOAD_ENV", value = "production" },
      { name = "AWS_REGION", value = var.aws_region },
    ],
    secrets          = local.ecs_generator_reverse ? [{ name = "APEXLOAD_GENERATOR_GATEWAY_TOKEN", valueFrom = aws_secretsmanager_secret.generator_gateway_token[0].arn }] : [],
    logConfiguration = { logDriver = "awslogs", options = { "awslogs-group" = aws_cloudwatch_log_group.ecs_generator[0].name, "awslogs-region" = var.aws_region, "awslogs-stream-prefix" = "generator" } }
  }])
  lifecycle {
    precondition {
      condition = (var.generator_image != "" &&
        (!local.ecs_generator_reverse || startswith(var.controller_public_url, "https://")) &&
      !(local.ecs_controller_enabled && !local.ecs_generator_reverse))
      error_message = "generator_image is required; reverse ECS generators additionally require an HTTPS controller_public_url, and an ECS-hosted controller requires auto or reverse generator connectivity."
    }
  }
  tags = local.tags
}

resource "aws_ecs_service" "controller" {
  count           = local.ecs_controller_enabled ? 1 : 0
  name            = "${local.name}-controller"
  cluster         = aws_ecs_cluster.apexload[0].id
  task_definition = aws_ecs_task_definition.controller[0].arn
  desired_count   = 1
  launch_type     = "FARGATE"
  network_configuration {
    subnets          = aws_subnet.private[*].id
    security_groups  = [aws_security_group.ecs_controller[0].id]
    assign_public_ip = false
  }
  load_balancer {
    target_group_arn = aws_lb_target_group.ecs_controller[0].arn
    container_name   = "controller"
    container_port   = 8080
  }
  tags = local.tags
}

resource "aws_ecs_service" "generator_warm" {
  count                   = local.ecs_generator_enabled ? 1 : 0
  name                    = "${local.name}-generator-warm"
  cluster                 = aws_ecs_cluster.apexload[0].id
  task_definition         = aws_ecs_task_definition.generator[0].arn
  desired_count           = 1
  launch_type             = "FARGATE"
  enable_ecs_managed_tags = true
  propagate_tags          = "SERVICE"
  network_configuration {
    subnets          = aws_subnet.private[*].id
    security_groups  = [aws_security_group.ecs_generator[0].id]
    assign_public_ip = false
  }
  tags = merge(local.tags, { "apexload.io/capacity" = "warm" })
}

output "ecs_registration_manifest" {
  description = "Sensitive when reverse connectivity is selected: the JSON contains no cloud credential, but does contain the reverse enrollment token."
  sensitive   = true
  value = local.ecs_generator_enabled ? jsonencode({
    cloud_connection = {
      id            = "${local.name}-aws"
      name          = "${local.name} AWS", provider = "aws", auth_mode = "aws_ambient",
      aws_partition = data.aws_partition.current.partition, aws_account_id = data.aws_caller_identity.current.account_id,
      aws_region    = var.aws_region, status = "active"
    }
    execution_targets = [{
      id                = "${local.name}-ecs-generators",
      name              = "${local.name}-ecs-generators", kind = "aws_ecs_fargate",
      connectivity_mode = local.ecs_generator_reverse ? "reverse" : "direct",
      standing_owner    = "apex", burst_owner = "apex", archive_mode = "controller_pull", status = "active",
      enrollment_token  = local.ecs_generator_reverse ? local.generator_gateway_token : null,
      aws_ecs_fargate = {
        cluster                = aws_ecs_cluster.apexload[0].name,
        warm_service           = aws_ecs_service.generator_warm[0].name,
        burst_task_definition  = aws_ecs_task_definition.generator[0].family,
        subnets                = aws_subnet.private[*].id,
        security_groups        = [aws_security_group.ecs_generator[0].id],
        assign_public_ip       = "disabled",
        expected_task_role_arn = aws_iam_role.ecs_generator[0].arn
      }
    }]
  }) : null
}
