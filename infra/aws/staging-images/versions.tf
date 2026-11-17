terraform {
  required_version = ">= 1.10.0, < 2.0.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }

  # Approved existing bucket and KMS key are supplied by the local script.
  backend "s3" {
    encrypt      = true
    use_lockfile = true
  }
}

provider "aws" {
  region              = var.aws_region
  allowed_account_ids = [var.expected_aws_account_id]

  default_tags {
    tags = {
      Project     = "ShopStops"
      Environment = "staging"
      Scope       = "image-publishing-only"
      ManagedBy   = "Terraform"
    }
  }
}