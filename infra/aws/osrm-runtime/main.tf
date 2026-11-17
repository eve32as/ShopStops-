locals {
  release_prefix = "datasets/new-york/${var.osrm_dataset_version}/"
  extensions = [
    "cell_metrics", "cells", "cnbg", "cnbg_to_ebg", "datasource_names", "ebg",
    "ebg_nodes", "edges", "enw", "fileIndex", "geometry", "icd",
    "maneuver_overrides", "mldgr", "names", "nbg_nodes", "partition", "properties",
    "ramIndex", "restrictions", "timestamp", "tld", "tls", "turn_duration_penalties",
    "turn_penalties_index", "turn_weight_penalties"
  ]
  filenames = concat(["SHA256SUMS", "new-york.osrm"], [for ext in local.extensions : "new-york.osrm.${ext}"])
}

resource "aws_iam_role" "osrm_task" {
  name_prefix = "shopstops-osrm-runtime-"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ecs-tasks.amazonaws.com" }
      Action    = "sts:AssumeRole"
      Condition = {
        StringEquals = { "aws:SourceAccount" = var.expected_aws_account_id }
        # ECS task trust currently requires an account/region ECS wildcard,
        # rather than scoping SourceArn to a particular cluster.
        ArnLike = { "aws:SourceArn" = "arn:aws:ecs:${var.aws_region}:${var.expected_aws_account_id}:*" }
      }
    }]
  })
}

resource "aws_iam_role_policy" "release_reads" {
  name = "verified-osrm-release-reads"
  role = aws_iam_role.osrm_task.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["s3:GetObject"]
      Resource = [for filename in local.filenames : "arn:aws:s3:::${var.osrm_artifact_bucket}/${local.release_prefix}${filename}"]
      Condition = {
        Bool         = { "aws:SecureTransport" = "true" }
        StringEquals = { "s3:ResourceAccount" = var.expected_aws_account_id }
      }
    }]
  })
}