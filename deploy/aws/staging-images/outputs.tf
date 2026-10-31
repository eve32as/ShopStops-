output "aws_region" {
  description = "Region containing the staging ECR repositories."
  value       = var.aws_region
}

output "github_environment" {
  description = "GitHub Actions environment allowed to publish staging images."
  value       = var.github_environment
}

output "image_publisher_role_arn" {
  description = "GitHub Actions OIDC role ARN for staging image publishing."
  value       = aws_iam_role.image_publisher.arn
}

output "api_repository_url" {
  description = "Staging API ECR repository URI."
  value       = aws_ecr_repository.staging["api"].repository_url
}

output "gateway_repository_url" {
  description = "Staging gateway ECR repository URI."
  value       = aws_ecr_repository.staging["gateway"].repository_url
}

output "migration_repository_url" {
  description = "Staging database migration ECR repository URI."
  value       = aws_ecr_repository.staging["migration"].repository_url
}
