# AWS deployment scaffold for ShopStops

This directory contains a **legacy, incomplete Terraform scaffold**, not a
production-ready or approved AWS deployment. Do not apply it as production.
Release packaging and factual approval gates are documented in
[production readiness](../../docs/production-readiness.md). No production
provisioning or automatic deployment is authorized by the test-only CI workflow.

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
- The app requires PostgreSQL 14+ and PostGIS 3.x. Confirm the selected RDS
	engine supports the extension, then run SQL `CREATE EXTENSION postgis` with an
	authorized migration role (migration 001 includes it). A parameter group does
	**not** install the extension. The hard-coded PostgreSQL 16.3 version needs
	current availability/support review, backups, restore rehearsal and protection.
- Update the secret values before deployment.
- OSRM is only a broken placeholder here: mutable `latest`, no mounted/downloaded
	dataset, no MLD/explicit 5001 startup, no task role, discovery or usable API→OSRM
	ingress rule, and unvalidated 1 GiB memory. The
	[separate runtime root](osrm-runtime/README.md) supplies a narrowly scoped IAM
	role and verified loader contract, **not** an ECS service or automatic wiring.
- Public HTTP currently forwards directly to the shared-token API. There is no
	HTTPS listener/certificate, OIDC gateway, actor-header boundary, CORS or DNS.
- Private subnets have no NAT/endpoints/egress routing for image pulls, secrets,
	logs, S3/STS or Stripe. ECS startup and routing are not established.
- RDS is single-AZ with `skip_final_snapshot=true` and no explicit production
	deletion protection/backup policy. Secrets entered through Terraform can be
	retained in state. State isolation/security and least-privilege roles need review.

## Historical scaffold flow — not approved to execute

The following is historical scaffolding, not a safe production release procedure.
Use the reviewed gates and future sequence in the production-readiness document;
the commands below must not be interpreted as approval.

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
