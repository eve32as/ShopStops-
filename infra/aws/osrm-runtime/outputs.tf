output "task_role_arn" {
  description = "Assign to ECS taskRoleArn, NOT executionRoleArn."
  value       = aws_iam_role.osrm_task.arn
}

output "runtime_environment" {
  description = "OSRM container environment; contains no credentials."
  value = {
    AWS_REGION              = var.aws_region
    EXPECTED_AWS_ACCOUNT_ID = var.expected_aws_account_id
    OSRM_ARTIFACT_BUCKET    = var.osrm_artifact_bucket
    OSRM_DATASET_VERSION    = var.osrm_dataset_version
    OSRM_DATA_PARENT        = "/data/osrm"
  }
}
