variable "aws_region" {
  description = "AWS region for the deployment."
  type        = string
}

variable "project_name" {
  description = "Prefix used for AWS resource names."
  type        = string
  default     = "shopstops"

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{1,20}$", var.project_name))
    error_message = "project_name must be 2-21 lowercase letters, digits, or hyphens and start with a letter."
  }
}

variable "environment" {
  description = "Deployment environment name."
  type        = string

  validation {
    condition     = contains(["staging", "production"], var.environment)
    error_message = "environment must be staging or production."
  }
}

variable "vpc_cidr" {
  description = "IPv4 CIDR for the application VPC."
  type        = string
  default     = "10.40.0.0/16"
}

variable "availability_zones" {
  description = "Two or three distinct availability zones in aws_region."
  type        = list(string)

  validation {
    condition     = length(var.availability_zones) >= 2 && length(var.availability_zones) <= 3 && length(distinct(var.availability_zones)) == length(var.availability_zones)
    error_message = "Provide two or three distinct availability zones."
  }
}

variable "certificate_arn" {
  description = "ARN of an issued ACM certificate covering gateway_hostname in aws_region."
  type        = string

  validation {
    condition     = can(regex("^arn:aws[a-zA-Z-]*:acm:${var.aws_region}:[0-9]{12}:certificate/[A-Za-z0-9-]+$", var.certificate_arn))
    error_message = "certificate_arn must be an ACM certificate ARN in aws_region."
  }
}

variable "gateway_hostname" {
  description = "Public DNS hostname served by the HTTPS gateway listener."
  type        = string

  validation {
    condition = (
      lower(var.gateway_hostname) == var.gateway_hostname &&
      length(split(".", var.gateway_hostname)) >= 2 &&
      alltrue([
        for label in split(".", var.gateway_hostname) :
        length(label) >= 1 && length(label) <= 63 &&
        can(regex("^[a-z0-9]([a-z0-9-]*[a-z0-9])?$", label))
      ])
    )
    error_message = "gateway_hostname must be a lowercase DNS hostname without a trailing dot or wildcard."
  }
}

variable "route53_zone_id" {
  description = "ID of the public Route 53 hosted zone containing gateway_hostname."
  type        = string

  validation {
    condition     = can(regex("^Z[A-Z0-9]+$", var.route53_zone_id))
    error_message = "route53_zone_id must be a Route 53 hosted zone ID."
  }
}

variable "oidc_issuer" {
  description = "HTTPS issuer URL for the externally configured OIDC provider."
  type        = string

  validation {
    condition     = can(regex("^https://[^[:space:]]+$", var.oidc_issuer))
    error_message = "oidc_issuer must be an HTTPS issuer URL."
  }
}

variable "oidc_audience" {
  description = "Audience expected by the identity gateway."
  type        = string
}

variable "oidc_role_claim" {
  description = "OIDC claim containing the ShopStops roles."
  type        = string
  default     = "roles"
}

variable "oidc_actor_claim" {
  description = "OIDC claim containing the stable actor identifier."
  type        = string
  default     = "sub"
}

variable "gateway_allowed_origins" {
  description = "Exact HTTPS origins allowed by the gateway CORS policy."
  type        = list(string)
  default     = []

  validation {
    condition = alltrue([
      for origin in var.gateway_allowed_origins :
      can(regex("^https://[^/[:space:]]+$", origin))
    ])
    error_message = "Browser origins must be exact HTTPS origins without paths."
  }
}

variable "api_image" {
  description = "Immutable API image reference, preferably an ECR image digest."
  type        = string

  validation {
    condition     = can(regex("@sha256:[0-9a-f]{64}$", var.api_image))
    error_message = "api_image must use an immutable sha256 digest reference."
  }
}

variable "gateway_image" {
  description = "Immutable gateway image reference, preferably an ECR image digest."
  type        = string

  validation {
    condition     = can(regex("@sha256:[0-9a-f]{64}$", var.gateway_image))
    error_message = "gateway_image must use an immutable sha256 digest reference."
  }
}

variable "migration_image" {
  description = "Immutable database migration runner image built from Dockerfile.db-migrate."
  type        = string

  validation {
    condition     = can(regex("@sha256:[0-9a-f]{64}$", var.migration_image))
    error_message = "migration_image must use an immutable sha256 digest reference."
  }
}

variable "db_engine_version" {
  description = "Supported PostgreSQL 16 minor version available in the selected AWS region."
  type        = string

  validation {
    condition     = can(regex("^16\\.[0-9]+$", var.db_engine_version))
    error_message = "db_engine_version must be a PostgreSQL 16 minor version such as 16.8."
  }
}

variable "db_instance_class" {
  description = "RDS instance class; choose a size based on measured load."
  type        = string
  default     = "db.t4g.large"
}

variable "db_allocated_storage_gib" {
  description = "Initial encrypted gp3 storage allocation."
  type        = number
  default     = 100

  validation {
    condition     = var.db_allocated_storage_gib >= 100 && var.db_allocated_storage_gib <= 65536
    error_message = "RDS allocated storage must be between 100 and 65536 GiB."
  }
}

variable "db_max_storage_gib" {
  description = "Maximum gp3 autoscaling storage allocation."
  type        = number
  default     = 1000

  validation {
    condition     = var.db_max_storage_gib >= var.db_allocated_storage_gib && var.db_max_storage_gib <= 65536
    error_message = "db_max_storage_gib must be at least the allocated size and no more than 65536 GiB."
  }
}

variable "enable_stripe" {
  description = "Inject Stripe secrets into the API. Secrets must be populated before enabling."
  type        = bool
  default     = false
}

variable "deploy_application" {
  description = "Start ECS services only after required secrets, database role, and ordered migrations are ready."
  type        = bool
  default     = false
}

variable "enable_api_autoscaling" {
  description = "Enable API and gateway CPU-based ECS service autoscaling."
  type        = bool
  default     = true
}

variable "alarm_email" {
  description = "Optional email subscription for infrastructure alarms."
  type        = string
  default     = ""
}

variable "ecs_cpu_alarm_percent" {
  description = "Average ECS service CPU utilization percentage that triggers an alarm."
  type        = number
  default     = 85

  validation {
    condition     = var.ecs_cpu_alarm_percent > 0 && var.ecs_cpu_alarm_percent <= 100
    error_message = "ecs_cpu_alarm_percent must be greater than 0 and at most 100."
  }
}

variable "ecs_memory_alarm_percent" {
  description = "Average ECS service memory utilization percentage that triggers an alarm."
  type        = number
  default     = 85

  validation {
    condition     = var.ecs_memory_alarm_percent > 0 && var.ecs_memory_alarm_percent <= 100
    error_message = "ecs_memory_alarm_percent must be greater than 0 and at most 100."
  }
}

variable "ecs_min_healthy_tasks" {
  description = "Minimum running tasks expected for each deployed API and gateway service."
  type        = number
  default     = 2

  validation {
    condition     = var.ecs_min_healthy_tasks >= 1 && floor(var.ecs_min_healthy_tasks) == var.ecs_min_healthy_tasks
    error_message = "ecs_min_healthy_tasks must be a positive integer."
  }
}
