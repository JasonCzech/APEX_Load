resource "aws_db_subnet_group" "main" {
  name       = "${local.name}-postgres"
  subnet_ids = aws_subnet.private[*].id
  tags       = merge(local.tags, { Name = "${local.name}-postgres" })
}

resource "aws_security_group" "postgres" {
  name        = "${local.name}-postgres"
  description = "APEX Load PostgreSQL"
  vpc_id      = aws_vpc.main.id

  ingress {
    description = "PostgreSQL from inside the VPC"
    from_port   = 5432
    to_port     = 5432
    protocol    = "tcp"
    cidr_blocks = [var.vpc_cidr]
  }

  tags = merge(local.tags, { Name = "${local.name}-postgres" })
}

resource "random_password" "postgres" {
  length  = 32
  special = false
}

# The password is generated here and stored in Secrets Manager rather than being
# emitted as a Terraform output: an output would land in plaintext in the state
# file and in any CI log that prints outputs.
resource "aws_secretsmanager_secret" "database_url" {
  name       = "${local.name}/platform/database-url"
  kms_key_id = aws_kms_key.storage.arn
  tags       = local.tags
}

resource "aws_secretsmanager_secret_version" "database_url" {
  secret_id = aws_secretsmanager_secret.database_url.id
  secret_string = format(
    "postgres://%s:%s@%s:5432/%s?sslmode=require",
    aws_db_instance.main.username,
    random_password.postgres.result,
    aws_db_instance.main.address,
    aws_db_instance.main.db_name,
  )
}

resource "aws_db_instance" "main" {
  identifier     = "${local.name}-postgres"
  engine         = "postgres"
  engine_version = var.postgres_version
  instance_class = var.postgres_instance_class

  db_name  = "apexload"
  username = "apexload"
  password = random_password.postgres.result

  allocated_storage     = var.postgres_allocated_storage_gb
  max_allocated_storage = var.postgres_allocated_storage_gb * 4
  storage_type          = "gp3"
  storage_encrypted     = true
  kms_key_id            = aws_kms_key.storage.arn

  db_subnet_group_name   = aws_db_subnet_group.main.name
  vpc_security_group_ids = [aws_security_group.postgres.id]
  publicly_accessible    = false

  multi_az                  = true
  backup_retention_period   = var.postgres_backup_retention_days
  deletion_protection       = true
  skip_final_snapshot       = false
  final_snapshot_identifier = "${local.name}-postgres-final"

  # Without this, an engine upgrade silently discards these logs.
  enabled_cloudwatch_logs_exports = ["postgresql", "upgrade"]

  auto_minor_version_upgrade = true
  apply_immediately          = false

  tags = merge(local.tags, { Name = "${local.name}-postgres" })
}
