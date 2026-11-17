variable "aws_region" {
  type        = string
  description = "Commercial AWS region, verified against the approved backend."
  validation {
    condition     = can(regex("^(us|eu|ap|ca|sa|af|me|il|mx)-[a-z]+-[1-9][0-9]*$", var.aws_region))
    error_message = "Use a commercial AWS region."
  }
}

variable "expected_aws_account_id" {
  type        = string
  description = "Approved account, also enforced by the provider."
  validation {
    condition     = can(regex("^[0-9]{12}$", var.expected_aws_account_id)) && var.expected_aws_account_id != "000000000000"
    error_message = "An explicit real 12-digit account ID is required."
  }
}

variable "github_repository" {
  type        = string
  description = "Exactly this repository is approved; no owner/repository wildcards."
  validation {
    condition     = var.github_repository == "eve32as/ShopStops-"
    error_message = "Only eve32as/ShopStops- is approved."
  }
}

variable "github_oidc_provider_arn" {
  type        = string
  description = "Existing GitHub OIDC provider; this module never creates one."
  validation {
    condition     = var.github_oidc_provider_arn == "arn:aws:iam::${var.expected_aws_account_id}:oidc-provider/token.actions.githubusercontent.com"
    error_message = "Provider must be the GitHub Actions provider in the approved account."
  }
}