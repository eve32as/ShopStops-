terraform {
  required_version = ">= 1.10.0, < 2.0.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
  # Supply a separately approved backend configuration; never reuse another root.
  backend "s3" {}
}

provider "aws" {
  region              = var.aws_region
  allowed_account_ids = [var.expected_aws_account_id]
  default_tags {
    tags = { Project = "ShopStops", Scope = "service-runtime", ManagedBy = "Terraform" }
  }
}