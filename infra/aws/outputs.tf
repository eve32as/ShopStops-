output "vpc_id" {
  description = "VPC ID"
  value       = aws_vpc.main.id
}

output "database_endpoint" {
  description = "RDS PostgreSQL endpoint"
  value       = aws_db_instance.main.address
}

output "alb_dns_name" {
  description = "Public ALB DNS name"
  value       = aws_lb.main.dns_name
}

output "api_target_group_arn" {
  description = "Target group for the API service"
  value       = aws_lb_target_group.api.arn
}

output "secret_arn" {
  description = "AWS Secrets Manager ARN for the app environment"
  value       = aws_secretsmanager_secret.app_env.arn
}
