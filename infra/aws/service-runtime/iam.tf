locals {
  services = toset(["api", "gateway", "admin", "osrm"])
  task_trust = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow", Principal = { Service = "ecs-tasks.amazonaws.com" }, Action = "sts:AssumeRole"
      Condition = {
        StringEquals = { "aws:SourceAccount" = var.expected_aws_account_id }
        ArnLike      = { "aws:SourceArn" = "arn:aws:ecs:${var.aws_region}:${var.expected_aws_account_id}:*" }
      }
    }]
  })
  secrets = {
    api     = [{ name = "DATABASE_URL", valueFrom = var.database_url_secret_arn }, { name = "SHOPSTOPS_INTERNAL_API_TOKEN", valueFrom = var.internal_token_secret_arn }]
    gateway = [{ name = "SHOPSTOPS_INTERNAL_API_TOKEN", valueFrom = var.internal_token_secret_arn }]
    admin   = []
    osrm    = []
  }
}

resource "aws_cloudwatch_log_group" "service" {
  for_each          = local.services
  name              = "/ecs/${var.name}/${each.key}"
  retention_in_days = 30
}

resource "aws_iam_role" "execution" {
  for_each           = local.services
  name_prefix        = "${var.name}-${each.key}-exec-"
  assume_role_policy = local.task_trust
}

resource "aws_iam_role_policy" "execution" {
  for_each = local.services
  role     = aws_iam_role.execution[each.key].id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = concat([
      { Effect = "Allow", Action = ["ecr:GetAuthorizationToken"], Resource = "*" },
      {
        Effect   = "Allow", Action = ["ecr:BatchCheckLayerAvailability", "ecr:GetDownloadUrlForLayer", "ecr:BatchGetImage"]
        Resource = "arn:aws:ecr:${var.aws_region}:${var.expected_aws_account_id}:repository/${split("@", split("amazonaws.com/", var.images[each.key])[1])[0]}"
      },
      { Effect = "Allow", Action = ["logs:CreateLogStream", "logs:PutLogEvents"], Resource = "${aws_cloudwatch_log_group.service[each.key].arn}:*" }
      ], length(local.secrets[each.key]) == 0 ? [] : [
      { Effect = "Allow", Action = ["secretsmanager:GetSecretValue"], Resource = [for secret in local.secrets[each.key] : secret.valueFrom] }
      ], length(local.secrets[each.key]) == 0 || length(var.secret_kms_key_arns) == 0 ? [] : [
      {
        Effect = "Allow", Action = ["kms:Decrypt"], Resource = var.secret_kms_key_arns
        Condition = { StringEquals = {
          "kms:ViaService"                  = "secretsmanager.${var.aws_region}.amazonaws.com"
          "kms:EncryptionContext:SecretARN" = [for secret in local.secrets[each.key] : secret.valueFrom]
        } }
      }
    ])
  })
}

# These application task identities intentionally have NO AWS permissions.
resource "aws_iam_role" "application" {
  for_each           = toset(["api", "gateway", "admin"])
  name_prefix        = "${var.name}-${each.key}-task-"
  assume_role_policy = local.task_trust
}