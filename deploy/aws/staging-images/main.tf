data "aws_caller_identity" "current" {}

locals {
  repositories = {
    api       = "${var.repository_prefix}-api"
    gateway   = "${var.repository_prefix}-gateway"
    migration = "${var.repository_prefix}-migrations"
  }
  github_subject = "repo:${var.github_repository}:environment:${var.github_environment}"
}

resource "aws_ecr_repository" "staging" {
  for_each             = local.repositories
  name                 = each.value
  image_tag_mutability = "IMMUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  encryption_configuration {
    encryption_type = "AES256"
  }
}

resource "aws_iam_role" "image_publisher" {
  name = "${var.repository_prefix}-image-publisher"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        Federated = var.github_oidc_provider_arn
      }
      Action = "sts:AssumeRoleWithWebIdentity"
      Condition = {
        StringEquals = {
          "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
          "token.actions.githubusercontent.com:sub" = local.github_subject
        }
      }
    }]
  })

  lifecycle {
    precondition {
      condition     = data.aws_caller_identity.current.account_id == var.expected_aws_account_id
      error_message = "The active AWS credentials do not belong to expected_aws_account_id."
    }
    precondition {
      condition     = split(":", var.github_oidc_provider_arn)[4] == var.expected_aws_account_id
      error_message = "The GitHub OIDC provider ARN must belong to expected_aws_account_id."
    }
  }
}

resource "aws_iam_role_policy" "image_publisher" {
  name = "${var.repository_prefix}-ecr-push"
  role = aws_iam_role.image_publisher.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "AuthenticateToECR"
        Effect   = "Allow"
        Action   = ["ecr:GetAuthorizationToken"]
        Resource = "*"
      },
      {
        Sid    = "PushOnlyShopStopsStagingImages"
        Effect = "Allow"
        Action = [
          "ecr:BatchCheckLayerAvailability",
          "ecr:CompleteLayerUpload",
          "ecr:DescribeImages",
          "ecr:InitiateLayerUpload",
          "ecr:PutImage",
          "ecr:UploadLayerPart"
        ]
        Resource = [for repository in aws_ecr_repository.staging : repository.arn]
      }
    ]
  })
}
