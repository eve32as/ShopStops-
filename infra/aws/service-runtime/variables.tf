variable "expected_aws_account_id" {
  type = string
  validation {
    condition     = can(regex("^[0-9]{12}$", var.expected_aws_account_id)) && var.expected_aws_account_id != "000000000000"
    error_message = "Use the approved nonzero 12-digit account."
  }
}

variable "aws_region" {
  type = string
  validation {
    condition     = can(regex("^(us|eu|ap|ca|sa|af|me|il|mx)-[a-z]+-[1-9][0-9]*$", var.aws_region))
    error_message = "Use a commercial AWS region."
  }
}

variable "name" {
  type    = string
  default = "shopstops-runtime"
  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{2,23}$", var.name))
    error_message = "Use a unique 3-24 character lowercase resource prefix."
  }
}

variable "vpc_id" {
  type = string
  validation {
    condition     = can(regex("^vpc-([a-f0-9]{8}|[a-f0-9]{17})$", var.vpc_id))
    error_message = "Supply an existing approved VPC ID."
  }
}

variable "private_subnet_ids" {
  type = set(string)
  validation {
    condition     = length(var.private_subnet_ids) >= 2 && alltrue([for id in var.private_subnet_ids : can(regex("^subnet-([a-f0-9]{8}|[a-f0-9]{17})$", id))])
    error_message = "Supply at least two existing private subnets in distinct AZs."
  }
}

variable "public_subnet_ids" {
  type = set(string)
  validation {
    condition = (
      length(var.public_subnet_ids) >= 2 &&
      alltrue([for id in var.public_subnet_ids : can(regex("^subnet-([a-f0-9]{8}|[a-f0-9]{17})$", id))]) &&
      length(setintersection(var.private_subnet_ids, var.public_subnet_ids)) == 0
    )
    error_message = "Supply at least two distinct public subnets, disjoint from private subnets; AZs/routing require external validation."
  }
}

variable "rds_security_group_id" {
  type        = string
  description = "Existing RDS SG; only API egress is created. Its owner must separately authorize API ingress."
  validation {
    condition     = can(regex("^sg-([a-f0-9]{8}|[a-f0-9]{17})$", var.rds_security_group_id))
    error_message = "Supply the existing database SG."
  }
}

variable "database_url_secret_arn" {
  type        = string
  description = "Existing Secrets Manager secret whose entire value is the DATABASE_URL; no secret reads in Terraform."
  validation {
    condition     = can(regex("^arn:aws:secretsmanager:${var.aws_region}:${var.expected_aws_account_id}:secret:[A-Za-z0-9/_+=.@-]+-[A-Za-z0-9]{6}$", var.database_url_secret_arn))
    error_message = "Use an exact secret ARN in the selected account and region, without JSON/version selectors."
  }
}

variable "internal_token_secret_arn" {
  type = string
  validation {
    condition     = can(regex("^arn:aws:secretsmanager:${var.aws_region}:${var.expected_aws_account_id}:secret:[A-Za-z0-9/_+=.@-]+-[A-Za-z0-9]{6}$", var.internal_token_secret_arn)) && var.internal_token_secret_arn != var.database_url_secret_arn
    error_message = "Use a separate exact same-account/region secret ARN holding the shared internal token."
  }
}

variable "secret_kms_key_arns" {
  type        = set(string)
  default     = []
  description = "Only exact customer-managed keys encrypting the supplied secrets; empty for the AWS managed key."
  validation {
    condition     = alltrue([for arn in var.secret_kms_key_arns : can(regex("^arn:aws:kms:${var.aws_region}:${var.expected_aws_account_id}:key/([a-f0-9-]{36}|mrk-[a-f0-9]{32})$", arn))])
    error_message = "Use exact same-account/region KMS key ARNs."
  }
}

variable "images" {
  type = object({ api = string, gateway = string, admin = string, osrm = string })
  validation {
    condition = alltrue([for image in values(var.images) :
      can(regex("^${var.expected_aws_account_id}\\.dkr\\.ecr\\.${var.aws_region}\\.amazonaws\\.com/[a-z0-9]+([._/-][a-z0-9]+)*@sha256:[a-f0-9]{64}$", image)) &&
      !can(regex("@sha256:0{64}$", image))
    ])
    error_message = "All four images must use existing same-account/region ECR repositories and nonzero immutable lowercase SHA256 digests, never tags."
  }
}

variable "certificate_arn" {
  type = string
  validation {
    condition     = can(regex("^arn:aws:acm:${var.aws_region}:${var.expected_aws_account_id}:certificate/[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$", var.certificate_arn))
    error_message = "Supply an existing ACM certificate ARN in the selected account and region."
  }
}

variable "domain" {
  type = string
  validation {
    condition = (
      length(var.domain) <= 253 && can(regex("^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\\.)+[a-z]{2,63}$", var.domain)) &&
      !can(regex("(^|\\.)(example|invalid|test|localhost|local)(\\.|$)|placeholder|changeme|replace", var.domain))
    )
    error_message = "Use the approved public lowercase DNS hostname, without placeholders."
  }
}

variable "oidc_issuer" {
  type = string
  validation {
    condition = (
      length(var.oidc_issuer) <= 2048 &&
      can(regex("^https://([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\\.)+[a-z]{2,63}(/[A-Za-z0-9._~/-]*)?$", var.oidc_issuer)) &&
      !can(regex("placeholder|changeme|replace|example|localhost|\\.invalid|\\.test|\\.local(/|$)|[<>]|https://[^/]*[0-9]$|/\\.?\\.(/|$)|https://.*//", var.oidc_issuer))
    )
    error_message = "Use the exact approved public HTTPS issuer, no credentials, port, query, fragment, traversal or placeholders."
  }
}

variable "oidc_audience" {
  type        = string
  description = "API-specific signed JWT audience, not an unrelated login client ID."
  validation {
    condition     = can(regex("^[A-Za-z0-9][A-Za-z0-9:/._-]{0,255}$", var.oidc_audience)) && !can(regex("placeholder|changeme|replace|example", lower(var.oidc_audience)))
    error_message = "Use a non-placeholder bounded API audience."
  }
}

variable "oidc_trusted_claims" {
  type        = bool
  default     = false
  description = "Enable elevated namespaced role/actor claims ONLY after issuer-managed privilege and binding review."
}

variable "osrm_task_role_arn" {
  type        = string
  description = "Existing task_role_arn from the separate osrm-runtime root, scoped to this exact release."
  validation {
    condition     = can(regex("^arn:aws:iam::${var.expected_aws_account_id}:role/[A-Za-z0-9/+=,.@_-]+$", var.osrm_task_role_arn))
    error_message = "Supply an existing same-account OSRM task role ARN."
  }
}

variable "osrm_artifact_bucket" {
  type = string
  validation {
    condition     = var.osrm_artifact_bucket == "shopstops-staging-osrm-${var.expected_aws_account_id}-${var.aws_region}" && length(var.osrm_artifact_bucket) <= 63
    error_message = "Use the existing runtime contract's account/region-bound staging bucket."
  }
}

variable "osrm_dataset_version" {
  type = string
  validation {
    condition     = can(regex("^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$", var.osrm_dataset_version)) && !can(regex("placeholder|replace|changeme|example|your[_-]", lower(var.osrm_dataset_version)))
    error_message = "Supply the immutable published dataset version, matching the prior role policy."
  }
}

variable "osrm_desired_count" {
  type    = number
  default = 1
  validation {
    condition     = var.osrm_desired_count >= 1 && var.osrm_desired_count <= 4 && floor(var.osrm_desired_count) == var.osrm_desired_count
    error_message = "Use 1-4 OSRM replicas after capacity review."
  }
}

variable "osrm_ephemeral_storage_gib" {
  type    = number
  default = 80
  validation {
    condition     = var.osrm_ephemeral_storage_gib >= 21 && var.osrm_ephemeral_storage_gib <= 200 && floor(var.osrm_ephemeral_storage_gib) == var.osrm_ephemeral_storage_gib
    error_message = "Use 21-200 GiB, measured for dataset, image layers and margin."
  }
}

variable "aws_https_egress_cidrs" {
  type        = set(string)
  description = "Approved IPv4 endpoint/service destinations for ECR, S3, Logs, Secrets Manager and STS HTTPS. No routes/endpoints are created. 0.0.0.0/0 explicitly permits NAT HTTPS where endpoints cannot cover dependencies."
  validation {
    condition     = length(var.aws_https_egress_cidrs) > 0 && alltrue([for cidr in var.aws_https_egress_cidrs : can(cidrnetmask(cidr))])
    error_message = "Supply explicitly reviewed IPv4 HTTPS destination CIDRs."
  }
}

variable "enable_waf" {
  type    = bool
  default = true
}

variable "waf_requests_per_five_minutes" {
  type    = number
  default = 2000
  validation {
    condition     = var.waf_requests_per_five_minutes >= 100 && var.waf_requests_per_five_minutes <= 2000000000 && floor(var.waf_requests_per_five_minutes) == var.waf_requests_per_five_minutes
    error_message = "Use a reviewed integer IP rate limit of at least 100 requests per five minutes."
  }
}