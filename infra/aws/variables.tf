variable "aws_region" {
  description = "AWS region for the deployment"
  type        = string
  default     = "us-east-1"
}

variable "project_name" {
  description = "Project prefix used in AWS resource names"
  type        = string
  default     = "shopstops"
}

variable "environment" {
  description = "Environment name"
  type        = string
  default     = "dev"
}

variable "vpc_cidr" {
  description = "CIDR block for the VPC"
  type        = string
  default     = "10.20.0.0/16"
}

variable "public_subnet_cidrs" {
  description = "Public subnet CIDR blocks"
  type        = list(string)
  default     = ["10.20.1.0/24", "10.20.2.0/24"]
}

variable "private_subnet_cidrs" {
  description = "Private subnet CIDR blocks"
  type        = list(string)
  default     = ["10.20.11.0/24", "10.20.12.0/24"]
}

variable "api_image" {
  description = "Container image for the ShopStops API"
  type        = string
  default     = "ghcr.io/your-org/shopstops-api:latest"
}

variable "db_name" {
  description = "RDS database name"
  type        = string
  default     = "shopstops"
}

variable "db_username" {
  description = "RDS master username"
  type        = string
  default     = "shopstopsadmin"
  sensitive   = true
}

variable "db_password" {
  description = "RDS master password"
  type        = string
  default     = "ChangeMe123!"
  sensitive   = true
}

variable "api_cpu" {
  description = "CPU units for the API task"
  type        = number
  default     = 256
}

variable "api_memory" {
  description = "Memory for the API task in MiB"
  type        = number
  default     = 512
}

variable "api_port" {
  description = "Port exposed by the API container"
  type        = number
  default     = 8080
}

variable "app_token" {
  description = "Backend internal API token"
  type        = string
  default     = "replace-with-strong-random-token"
  sensitive   = true
}

variable "delivery_fee_cents" {
  description = "Delivery fee configuration for the API"
  type        = number
  default     = 500
}

variable "service_fee_cents" {
  description = "Service fee configuration for the API"
  type        = number
  default     = 100
}

variable "osrm_url" {
  description = "OSRM routing service URL"
  type        = string
  default     = "http://osrm.internal:5001"
}

variable "stripe_secret_key" {
  description = "Stripe secret key"
  type        = string
  default     = "replace-me"
  sensitive   = true
}

variable "stripe_webhook_secret" {
  description = "Stripe webhook secret"
  type        = string
  default     = "replace-me"
  sensitive   = true
}

variable "stripe_merchant_monthly_price_id" {
  description = "Stripe monthly merchant price ID"
  type        = string
  default     = "replace-me"
  sensitive   = true
}
