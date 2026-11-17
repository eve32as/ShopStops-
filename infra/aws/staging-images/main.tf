locals {
  dataset_prefix = "datasets/new-york/"
  artifact_name  = "shopstops-staging-osrm-${var.expected_aws_account_id}-${var.aws_region}"
}

data "aws_iam_openid_connect_provider" "github" {
  arn = var.github_oidc_provider_arn

  lifecycle {
    postcondition {
      condition     = contains(["token.actions.githubusercontent.com", "https://token.actions.githubusercontent.com"], self.url) && contains(self.client_id_list, "sts.amazonaws.com")
      error_message = "Existing provider must be GitHub Actions and support the STS audience."
    }
  }
}

resource "aws_ecr_repository" "images" {
  for_each             = toset(["api", "osrm"])
  name                 = "shopstops/staging/${each.key}"
  image_tag_mutability = "IMMUTABLE"
  force_delete         = false

  image_scanning_configuration {
    scan_on_push = true
  }

  encryption_configuration {
    encryption_type = "AES256"
  }
}

resource "aws_s3_bucket" "osrm" {
  bucket        = local.artifact_name
  force_destroy = false

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_s3_bucket_ownership_controls" "osrm" {
  bucket = aws_s3_bucket.osrm.id
  rule {
    object_ownership = "BucketOwnerEnforced"
  }
}

resource "aws_s3_bucket_versioning" "osrm" {
  bucket = aws_s3_bucket.osrm.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "osrm" {
  bucket = aws_s3_bucket.osrm.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "osrm" {
  bucket                  = aws_s3_bucket.osrm.id
  block_public_acls       = true
  ignore_public_acls      = true
  block_public_policy     = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_policy" "osrm" {
  bucket = aws_s3_bucket.osrm.id
  depends_on = [
    aws_s3_bucket_public_access_block.osrm,
    aws_s3_bucket_ownership_controls.osrm,
    aws_s3_bucket_versioning.osrm,
    aws_s3_bucket_server_side_encryption_configuration.osrm,
  ]
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid       = "RequireTLS"
        Effect    = "Deny"
        Principal = "*"
        Action    = "s3:*"
        Resource  = [aws_s3_bucket.osrm.arn, "${aws_s3_bucket.osrm.arn}/*"]
        Condition = { Bool = { "aws:SecureTransport" = "false" } }
      },
      {
        # Conditional create protects all keys, including the completion marker.
        Sid       = "RequireConditionalDatasetWrites"
        Effect    = "Deny"
        Principal = "*"
        Action    = "s3:PutObject"
        Resource  = "${aws_s3_bucket.osrm.arn}/${local.dataset_prefix}*"
        Condition = { Null = { "s3:if-none-match" = "true" } }
      },
      {
        Sid       = "RequireCreateOnlyDatasetWrites"
        Effect    = "Deny"
        Principal = "*"
        Action    = "s3:PutObject"
        Resource  = "${aws_s3_bucket.osrm.arn}/${local.dataset_prefix}*"
        Condition = { StringNotEquals = { "s3:if-none-match" = "*" } }
      }
    ]
  })
}

resource "aws_iam_role" "github_publisher" {
  name                 = "shopstops-staging-image-publisher"
  description          = "Main-branch image/artifact publishing only; no infrastructure deployment."
  max_session_duration = 3600
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Federated = data.aws_iam_openid_connect_provider.github.arn }
      Action    = "sts:AssumeRoleWithWebIdentity"
      Condition = {
        StringEquals = {
          "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
          "token.actions.githubusercontent.com:sub" = "repo:${var.github_repository}:ref:refs/heads/main"
        }
      }
    }]
  })
}

resource "aws_iam_role_policy" "publish_only" {
  name = "publish-staging-images-and-new-york-artifacts"
  role = aws_iam_role.github_publisher.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "ECRLogin"
        Effect   = "Allow"
        Action   = ["ecr:GetAuthorizationToken"]
        Resource = "*"
      },
      {
        Sid    = "PushOnlyTheseRepositories"
        Effect = "Allow"
        Action = [
          "ecr:BatchCheckLayerAvailability", "ecr:InitiateLayerUpload",
          "ecr:UploadLayerPart", "ecr:CompleteLayerUpload", "ecr:PutImage",
        ]
        Resource = [for repository in aws_ecr_repository.images : repository.arn]
      },
      {
        Sid       = "CreateOnlyNewYorkDatasetObjects"
        Effect    = "Allow"
        Action    = ["s3:PutObject"]
        Resource  = "${aws_s3_bucket.osrm.arn}/${local.dataset_prefix}*"
        Condition = { StringEquals = { "s3:if-none-match" = "*", "s3:x-amz-server-side-encryption" = "AES256" } }
      }
    ]
  })
}