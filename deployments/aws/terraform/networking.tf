resource "aws_vpc" "main" {
  cidr_block           = var.vpc_cidr
  enable_dns_support   = true
  enable_dns_hostnames = true

  tags = merge(local.tags, { Name = "${local.name}-vpc" })
}

resource "aws_internet_gateway" "main" {
  vpc_id = aws_vpc.main.id
  tags   = merge(local.tags, { Name = "${local.name}-igw" })
}

resource "aws_subnet" "public" {
  count = length(var.availability_zones)

  vpc_id                  = aws_vpc.main.id
  cidr_block              = cidrsubnet(var.vpc_cidr, 8, count.index)
  availability_zone       = var.availability_zones[count.index]
  map_public_ip_on_launch = true

  tags = merge(local.tags, {
    Name = "${local.name}-public-${var.availability_zones[count.index]}"
    # Required by the AWS Load Balancer Controller to place internet-facing
    # load balancers.
    "kubernetes.io/role/elb" = "1"
  })
}

resource "aws_subnet" "private" {
  count = length(var.availability_zones)

  vpc_id            = aws_vpc.main.id
  cidr_block        = cidrsubnet(var.vpc_cidr, 8, count.index + 100)
  availability_zone = var.availability_zones[count.index]

  tags = merge(local.tags, {
    Name                              = "${local.name}-private-${var.availability_zones[count.index]}"
    "kubernetes.io/role/internal-elb" = "1"
  })
}

resource "aws_eip" "nat" {
  count  = length(var.availability_zones)
  domain = "vpc"
  tags   = merge(local.tags, { Name = "${local.name}-nat-${count.index}" })
}

# One NAT gateway per AZ. A single shared gateway is cheaper but makes an AZ
# outage take down egress for every node, which during a load test looks like a
# generator fleet failure rather than an infrastructure one.
resource "aws_nat_gateway" "main" {
  count = length(var.availability_zones)

  allocation_id = aws_eip.nat[count.index].id
  subnet_id     = aws_subnet.public[count.index].id
  depends_on    = [aws_internet_gateway.main]

  tags = merge(local.tags, { Name = "${local.name}-nat-${var.availability_zones[count.index]}" })
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.main.id
  tags   = merge(local.tags, { Name = "${local.name}-public" })
}

resource "aws_route" "public_internet" {
  route_table_id         = aws_route_table.public.id
  destination_cidr_block = "0.0.0.0/0"
  gateway_id             = aws_internet_gateway.main.id
}

resource "aws_route_table_association" "public" {
  count          = length(var.availability_zones)
  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

resource "aws_route_table" "private" {
  count  = length(var.availability_zones)
  vpc_id = aws_vpc.main.id
  tags   = merge(local.tags, { Name = "${local.name}-private-${var.availability_zones[count.index]}" })
}

resource "aws_route" "private_nat" {
  count                  = length(var.availability_zones)
  route_table_id         = aws_route_table.private[count.index].id
  destination_cidr_block = "0.0.0.0/0"
  nat_gateway_id         = aws_nat_gateway.main[count.index].id
}

resource "aws_route_table_association" "private" {
  count          = length(var.availability_zones)
  subnet_id      = aws_subnet.private[count.index].id
  route_table_id = aws_route_table.private[count.index].id
}

# --- VPC endpoints ---
#
# These keep control-plane traffic off the public internet. The STS endpoint
# matters most for this application: the controller presigns an STS
# GetCallerIdentity URL as its EKS bearer token, and that presigned URL is a
# replayable credential for its lifetime. Private DNS is enabled so the endpoint
# still resolves as sts.<region>.amazonaws.com, which is what the controller's
# STS host allow-list accepts — a raw vpce-* hostname would be refused.
resource "aws_security_group" "vpc_endpoints" {
  name        = "${local.name}-vpce"
  description = "APEX Load interface VPC endpoints"
  vpc_id      = aws_vpc.main.id

  ingress {
    description = "HTTPS from inside the VPC"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = [var.vpc_cidr]
  }

  tags = merge(local.tags, { Name = "${local.name}-vpce" })
}

resource "aws_vpc_endpoint" "interface" {
  for_each = toset([
    "sts",
    "kms",
    "secretsmanager",
    "ecr.api",
    "ecr.dkr",
    "logs",
    "ecs",
    "ecs-agent",
    "ecs-telemetry",
  ])

  vpc_id              = aws_vpc.main.id
  service_name        = "com.amazonaws.${var.aws_region}.${each.value}"
  vpc_endpoint_type   = "Interface"
  subnet_ids          = aws_subnet.private[*].id
  security_group_ids  = [aws_security_group.vpc_endpoints.id]
  private_dns_enabled = true

  tags = merge(local.tags, { Name = "${local.name}-vpce-${each.value}" })
}

# S3 is a gateway endpoint: archive uploads are the highest-volume egress this
# deployment produces, and routing them through NAT would be both slower and
# billed per gigabyte.
resource "aws_vpc_endpoint" "s3" {
  vpc_id            = aws_vpc.main.id
  service_name      = "com.amazonaws.${var.aws_region}.s3"
  vpc_endpoint_type = "Gateway"
  route_table_ids   = aws_route_table.private[*].id

  tags = merge(local.tags, { Name = "${local.name}-vpce-s3" })
}
