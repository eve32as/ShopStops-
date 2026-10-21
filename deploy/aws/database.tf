resource "aws_kms_key" "database" {
  description             = "Encryption key for ${local.name} RDS"
  deletion_window_in_days = 30
  enable_key_rotation     = true
}

resource "aws_kms_alias" "database" {
  name          = "alias/${local.name}-database"
  target_key_id = aws_kms_key.database.key_id
}

resource "aws_db_subnet_group" "main" {
  name       = "${local.name}-database"
  subnet_ids = [for subnet in aws_subnet.database : subnet.id]
}

resource "aws_db_parameter_group" "main" {
  name   = "${local.name}-postgres16"
  family = "postgres16"

  parameter {
    name         = "rds.force_ssl"
    value        = "1"
    apply_method = "immediate"
  }
}

resource "aws_iam_role" "rds_monitoring" {
  name = "${local.name}-rds-monitoring"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        Service = "monitoring.rds.amazonaws.com"
      }
      Action = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "rds_monitoring" {
  role       = aws_iam_role.rds_monitoring.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonRDSEnhancedMonitoringRole"
}

resource "aws_db_instance" "main" {
  identifier                            = local.name
  engine                                = "postgres"
  engine_version                        = var.db_engine_version
  instance_class                        = var.db_instance_class
  allocated_storage                     = var.db_allocated_storage_gib
  max_allocated_storage                 = var.db_max_storage_gib
  storage_type                          = "gp3"
  storage_encrypted                     = true
  kms_key_id                            = aws_kms_key.database.arn
  db_name                               = "shopstops"
  username                              = "shopstops_admin"
  manage_master_user_password           = true
  multi_az                              = true
  db_subnet_group_name                  = aws_db_subnet_group.main.name
  parameter_group_name                  = aws_db_parameter_group.main.name
  vpc_security_group_ids                = [aws_security_group.database.id]
  publicly_accessible                   = false
  backup_retention_period               = 35
  auto_minor_version_upgrade            = true
  deletion_protection                   = true
  copy_tags_to_snapshot                 = true
  performance_insights_enabled          = true
  performance_insights_retention_period = 7
  monitoring_interval                   = 60
  monitoring_role_arn                   = aws_iam_role.rds_monitoring.arn
  enabled_cloudwatch_logs_exports       = ["postgresql", "upgrade"]
  apply_immediately                     = false

  lifecycle {
    prevent_destroy = true
  }

  depends_on = [aws_iam_role_policy_attachment.rds_monitoring]
}

resource "aws_secretsmanager_secret" "database_url" {
  name                    = "${local.name}/api/database-url"
  description             = "Application database URL. Populate only after provisioning the least-privilege application role."
  recovery_window_in_days = 30
}

resource "aws_secretsmanager_secret" "internal_api_token" {
  name                    = "${local.name}/api/internal-token"
  description             = "Shared API/gateway internal token. Set a random value of at least 32 characters."
  recovery_window_in_days = 30
}

resource "aws_secretsmanager_secret" "app_database_password" {
  name                    = "${local.name}/database/application-password"
  description             = "Password for the least-privilege ShopStops application database role."
  recovery_window_in_days = 30
}

resource "aws_secretsmanager_secret" "stripe_secret_key" {
  count = var.enable_stripe ? 1 : 0

  name                    = "${local.name}/stripe/secret-key"
  description             = "Live or test Stripe secret key, selected by the environment owner."
  recovery_window_in_days = 30
}

resource "aws_secretsmanager_secret" "stripe_webhook_secret" {
  count = var.enable_stripe ? 1 : 0

  name                    = "${local.name}/stripe/webhook-secret"
  description             = "Stripe webhook signing secret for this environment."
  recovery_window_in_days = 30
}

resource "aws_secretsmanager_secret" "stripe_merchant_price" {
  count = var.enable_stripe ? 1 : 0

  name                    = "${local.name}/stripe/merchant-monthly-price-id"
  description             = "Stripe monthly merchant subscription Price ID."
  recovery_window_in_days = 30
}
