resource "aws_ecr_repository" "controller" {
  name                 = "${local.name}-controller"
  image_tag_mutability = "IMMUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  encryption_configuration {
    encryption_type = "KMS"
    kms_key         = aws_kms_key.storage.arn
  }

  tags = merge(local.tags, { Name = "${local.name}-controller" })
}

resource "aws_ecr_repository" "generator" {
  name                 = "${local.name}-generator"
  image_tag_mutability = "IMMUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  encryption_configuration {
    encryption_type = "KMS"
    kms_key         = aws_kms_key.storage.arn
  }

  tags = merge(local.tags, { Name = "${local.name}-generator" })
}

# IMMUTABLE tags above mean a rebuilt image needs a new tag, which is why the
# render script defaults the tag to the git short SHA. Untagged layers left by a
# failed push would otherwise accumulate indefinitely.
resource "aws_ecr_lifecycle_policy" "expire_untagged" {
  for_each = {
    controller = aws_ecr_repository.controller.name
    generator  = aws_ecr_repository.generator.name
  }

  repository = each.value

  policy = jsonencode({
    rules = [{
      rulePriority = 1
      description  = "Expire untagged images after 14 days"
      selection = {
        tagStatus   = "untagged"
        countType   = "sinceImagePushed"
        countUnit   = "days"
        countNumber = 14
      }
      action = { type = "expire" }
    }]
  })
}
