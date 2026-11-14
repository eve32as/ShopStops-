# AWS deployment scaffold for ShopStops

This directory contains a minimal Terraform scaffold for a production-ready AWS deployment of the ShopStops Go API, Postgres database, and supporting networking.

## What it provisions

- VPC with public/private subnets
- RDS PostgreSQL instance for the app data layer
- ECS Fargate cluster and service for the Go API
- Application Load Balancer in front of the API
- Security groups for API, ALB, and database access
- Secrets Manager entries for required runtime values
- CloudWatch log group for API logs

## Notes

- Replace the placeholder container image with your real API image.
- The app requires PostGIS on the PostgreSQL instance. That extension is not enabled automatically in the default Terraform resource; enable it via a DB parameter group or a post-provisioning migration step.
- Update the secret values before deployment.
- OSRM is modeled separately as a small container service in the same ECS cluster; update the container image and network configuration if you want to host it in AWS.

## Typical deployment flow

1. Build and push the API container image to ECR or GHCR.
2. Update the `api_image` variable in `variables.tf`.
3. Populate the secret values in AWS Secrets Manager or update the `aws_secretsmanager_secret_version` inputs.
4. Run:

```bash
terraform init
terraform plan
terraform apply
```

5. Verify the ALB target health and confirm the app is responding on the public endpoint.

## Required app environment

The backend requires these values at runtime:

- DATABASE_URL
- SHOPSTOPS_INTERNAL_API_TOKEN
- SHOPSTOPS_DELIVERY_FEE_CENTS
- SHOPSTOPS_SERVICE_FEE_CENTS
- OSRM_URL
- STRIPE_SECRET_KEY
- STRIPE_WEBHOOK_SECRET
- STRIPE_MERCHANT_MONTHLY_PRICE_ID

These are represented in the scaffold as secret values and task env vars.
