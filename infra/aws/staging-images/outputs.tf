output "ecr_repository_urls" {
  description = "Immutable-tag staging API and OSRM repositories."
  value       = { for name, repository in aws_ecr_repository.images : name => repository.repository_url }
}

output "osrm_artifact_bucket" {
  value = aws_s3_bucket.osrm.id
}

output "osrm_dataset_prefix" {
  value = local.dataset_prefix
}

output "github_publishing_role_arn" {
  value = aws_iam_role.github_publisher.arn
}