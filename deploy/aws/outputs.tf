output "load_balancer_dns_name" {
  description = "AWS load balancer DNS name behind the configured gateway_hostname alias."
  value       = aws_lb.gateway.dns_name
}

output "gateway_hostname" {
  description = "Configured public HTTPS hostname mapped to the gateway load balancer."
  value       = aws_route53_record.gateway.fqdn
}

output "load_balancer_zone_id" {
  description = "Route 53 hosted zone ID for the load balancer alias target."
  value       = aws_lb.gateway.zone_id
}

output "database_endpoint" {
  description = "Private RDS endpoint. Do not expose this endpoint publicly."
  value       = aws_db_instance.main.address
}

output "database_master_secret_arn" {
  description = "AWS-managed RDS master credential secret; reserve for controlled bootstrap and break-glass use."
  value       = aws_db_instance.main.master_user_secret[0].secret_arn
}

output "application_database_url_secret_arn" {
  description = "Populate this secret with a least-privilege application DATABASE_URL outside Terraform."
  value       = aws_secretsmanager_secret.database_url.arn
}

output "internal_api_token_secret_arn" {
  description = "Populate with a randomly generated token of at least 32 characters outside Terraform."
  value       = aws_secretsmanager_secret.internal_api_token.arn
}

output "application_database_password_secret_arn" {
  description = "Populate with the generated password for shopstops_app and use the same value in DATABASE_URL."
  value       = aws_secretsmanager_secret.app_database_password.arn
}

output "database_migration_task_definition" {
  description = "One-off Fargate migration task definition; run only during a controlled deployment."
  value       = aws_ecs_task_definition.migrator.family
}

output "database_migration_security_group_id" {
  description = "Security group for private migration tasks; it has no inbound access."
  value       = aws_security_group.migrator.id
}

output "private_application_subnet_ids" {
  description = "Private subnets for ECS Fargate tasks and the one-off migration task."
  value       = [for subnet in aws_subnet.application : subnet.id]
}

output "stripe_secret_arns" {
  description = "Optional Stripe secret ARNs; secret values are never stored in Terraform."
  value = var.enable_stripe ? {
    secret_key    = aws_secretsmanager_secret.stripe_secret_key[0].arn
    webhook       = aws_secretsmanager_secret.stripe_webhook_secret[0].arn
    monthly_price = aws_secretsmanager_secret.stripe_merchant_price[0].arn
  } : {}
}

output "ecs_cluster_name" {
  value = aws_ecs_cluster.main.name
}

output "alb_access_log_bucket" {
  description = "Private S3 bucket receiving ALB access logs; configure approved retention and review access."
  value       = aws_s3_bucket.alb_access_logs.id
}

output "alarm_topic_arn" {
  description = "Subscribe the operations on-call rotation to this topic."
  value       = aws_sns_topic.alarms.arn
}
