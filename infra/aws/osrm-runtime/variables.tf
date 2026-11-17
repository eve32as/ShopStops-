variable "expected_aws_account_id" {
  type        = string
  description = "Approved account; also enforced by the provider."
  validation {
    condition     = can(regex("^[0-9]{12}$", var.expected_aws_account_id)) && var.expected_aws_account_id != "000000000000"
    error_message = "Use a real 12-digit account ID."
  }
}

variable "aws_region" {
  type        = string
  description = "Commercial AWS region of the existing artifact bucket and ECS tasks."
  validation {
    condition     = can(regex("^(us|eu|ap|ca|sa|af|me|il|mx)-[a-z]+-[1-9][0-9]*$", var.aws_region))
    error_message = "Use a commercial AWS region."
  }
}

variable "osrm_artifact_bucket" {
  type        = string
  description = "Existing designated staging bucket; never created or inspected here."
  validation {
    condition = (
      var.osrm_artifact_bucket == "shopstops-staging-osrm-${var.expected_aws_account_id}-${var.aws_region}" &&
      length(var.osrm_artifact_bucket) <= 63 &&
      can(regex("^[a-z0-9][a-z0-9-]*[a-z0-9]$", var.osrm_artifact_bucket))
    )
    error_message = "Use the designated staging bucket in the approved account and region."
  }
}

variable "osrm_dataset_version" {
  type        = string
  description = "Immutable published release, using exactly the publisher validation."
  validation {
    condition = (
      can(regex("^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$", var.osrm_dataset_version)) &&
      !can(regex("placeholder|replace|changeme|example|your[_-]|[<>]", lower(var.osrm_dataset_version)))
    )
    error_message = "Use 1-64 safe letters/digits/underscores/hyphens, initially alphanumeric, without placeholders."
  }
}