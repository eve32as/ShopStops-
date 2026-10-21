output "load_balancer_dns_name" {
  description = "Public HTTPS endpoint DNS name; create a DNS alias after certificate and hostname validation."
  value       = aws_lb.gateway.dns_name
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

output "alarm_topic_arn" {
  description = "Subscribe the operations on-call rotation to this topic."
  value       = aws_sns_topic.alarms.arn
}
