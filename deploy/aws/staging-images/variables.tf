variable "aws_region" {
  description = "AWS region containing the staging ECR repositories."
  type        = string
}

variable "expected_aws_account_id" {
  description = "AWS account ID expected to own the staging publishing role and repositories."
  type        = string

  validation {
    condition     = can(regex("^[0-9]{12}$", var.expected_aws_account_id))
    error_message = "expected_aws_account_id must be a 12-digit AWS account ID."
  }
}

variable "github_repository" {
  description = "GitHub repository in owner/name form."
  type        = string

  validation {
    condition     = can(regex("^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$", var.github_repository))
    error_message = "github_repository must be in owner/name form."
  }
}

variable "github_environment" {
  description = "GitHub Actions environment allowed to assume the image-publisher role."
  type        = string
  default     = "staging-images"

  validation {
    condition     = can(regex("^[A-Za-z0-9_.-]+$", var.github_environment))
    error_message = "github_environment may contain only letters, digits, underscores, dots, and hyphens."
  }
}

variable "github_oidc_provider_arn" {
  description = "ARN of the account's existing GitHub Actions OIDC provider."
  type        = string

  validation {
    condition     = can(regex("^arn:aws[a-zA-Z-]*:iam::[0-9]{12}:oidc-provider/token\\.actions\\.githubusercontent\\.com$", var.github_oidc_provider_arn))
    error_message = "github_oidc_provider_arn must identify the token.actions.githubusercontent.com IAM provider."
  }
}

variable "repository_prefix" {
  description = "Prefix used for the three staging ECR repository names and IAM role."
  type        = string
  default     = "shopstops-staging"

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{1,39}$", var.repository_prefix))
    error_message = "repository_prefix must be 2-40 lowercase letters, digits, or hyphens and start with a letter."
  }
}
