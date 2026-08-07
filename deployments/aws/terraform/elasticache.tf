resource "aws_elasticache_subnet_group" "main" {
  name       = "${local.name}-redis"
  subnet_ids = aws_subnet.private[*].id
  tags       = local.tags
}

resource "aws_security_group" "redis" {
  name        = "${local.name}-redis"
  description = "APEX Load Redis"
  vpc_id      = aws_vpc.main.id

  ingress {
    description = "Redis from inside the VPC"
    from_port   = 6379
    to_port     = 6379
    protocol    = "tcp"
    cidr_blocks = [var.vpc_cidr]
  }

  tags = merge(local.tags, { Name = "${local.name}-redis" })
}

resource "random_password" "redis" {
  length  = 48
  special = false
}

resource "aws_elasticache_replication_group" "main" {
  replication_group_id = "${local.name}-redis"
  description          = "APEX Load live state and HA telemetry fanout"

  engine             = "redis"
  node_type          = var.redis_node_type
  num_cache_clusters = 2
  port               = 6379

  subnet_group_name  = aws_elasticache_subnet_group.main.name
  security_group_ids = [aws_security_group.redis.id]

  # The controller HA lease and the telemetry fanout both live here, so a
  # single-node failure must not take the active controller down with it.
  automatic_failover_enabled = true
  multi_az_enabled           = true

  at_rest_encryption_enabled = true
  kms_key_id                 = aws_kms_key.storage.arn
  transit_encryption_enabled = true
  auth_token                 = random_password.redis.result

  snapshot_retention_limit = 7

  tags = merge(local.tags, { Name = "${local.name}-redis" })
}

resource "aws_secretsmanager_secret" "redis_url" {
  name       = "${local.name}/platform/redis-url"
  kms_key_id = aws_kms_key.storage.arn
  tags       = local.tags
}

# rediss:// because transit encryption is on; a redis:// URL would be refused by
# the cluster rather than silently downgraded.
resource "aws_secretsmanager_secret_version" "redis_url" {
  secret_id = aws_secretsmanager_secret.redis_url.id
  secret_string = format(
    "rediss://:%s@%s:6379",
    random_password.redis.result,
    aws_elasticache_replication_group.main.primary_endpoint_address,
  )
}
