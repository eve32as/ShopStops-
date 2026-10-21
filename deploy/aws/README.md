# AWS deployment foundation

This Terraform root defines an AWS foundation for ShopStops. It is not a
deployed environment and does not configure live identity, Stripe, maps, or
mobile push credentials. It creates a VPC with public load-balancer subnets,
private ECS subnets and isolated database subnets; NAT gateways in each
availability zone; an HTTPS Application Load Balancer; ECS Fargate API and OIDC
gateway task definitions and opt-in services; encrypted, Multi-AZ RDS PostgreSQL;
Secrets Manager entries; CloudWatch logs and alarms; and least-privilege service
security groups.

This guide is for an authorized staging deployment only. Production deployment
requires separate approval and the release gates documented by the service owner.

The API is never exposed by the load balancer. Public traffic reaches only the
gateway. `/v1/readyz` is the load-balancer health check. The RDS master password
is managed by RDS and its secret ARN is output for controlled bootstrap only.
The application database URL, shared gateway token, and optional Stripe values
are empty Secrets Manager entries: Terraform never accepts or stores their
values.

The RDS parameter group requires TLS for database connections. The populated
application `DATABASE_URL` must use certificate-verifying TLS (for example,
`sslmode=verify-full`) and the appropriate AWS RDS CA trust chain.

## Prerequisites

An operator must provide:

- An AWS account and region, using an approved short-lived IAM role or AWS
  IAM Identity Center credentials. Do not put AWS access keys in this
  repository or in Terraform variables.
- An encrypted, versioned S3 bucket and KMS key for remote Terraform state.
  Enable S3 state locking with `use_lockfile`; restrict bucket and KMS access
  to the deployment role and state administrators.
- Existing private ECR repositories and immutable API, gateway, and database
  migration image references using `@sha256:<digest>`. Build the migration image
  from `Dockerfile.db-migrate`.
- Two or three available AZ names in the selected region and an ACM certificate
  already issued in that same region for the staging hostname.
- A configured OIDC provider. Its issuer, audience, role claim, and actor claim
  must match real signed tokens accepted by the gateway. Cognito remains a
  provisional option; validate its access-token audience and role-claim
  behavior before setting staging values.

- A staging database connection and payment configuration plan. The
  `scripts/db-migrate.sh` runner provisions a constrained `shopstops_app` login,
  applies checksummed migrations under a separate no-login migration role, and
  grants runtime table/function access. The one-off ECS task definition runs
  this runner inside private subnets with the RDS master secret available only
  to its separate execution role.

Terraform uses the S3 backend declared in `versions.tf`. Create a private local
`backend.hcl` (ignored by Git) with the actual state bucket, key, region, KMS
key ARN, encryption, and `use_lockfile = true`, for example:

```hcl
bucket       = "organization-terraform-state"
key          = "shopstops/staging/terraform.tfstate"
region       = "us-east-1"
encrypt      = true
kms_key_id   = "arn:aws:kms:us-east-1:000000000000:key/replace-with-key-id"
use_lockfile = true
```

Build and publish the migration runner to the private ECR repository before
planning, then use the image digest (not its mutable tag) for `migration_image`:

```sh
docker build --file Dockerfile.db-migrate --tag "$MIGRATION_IMAGE_TAG" .
docker push "$MIGRATION_IMAGE_TAG"
```

Resolve the pushed image digest from ECR and set `migration_image` to
`<repository-uri>@<image-digest>` in the private variables file.

Then initialize and validate:

```sh
terraform -chdir=deploy/aws init -backend-config=backend.hcl
terraform -chdir=deploy/aws fmt -check -recursive
terraform -chdir=deploy/aws validate
```

Set deployment inputs through an ignored `*.tfvars` file or the approved
deployment system. Do not store credentials or secret values in that file.
Always inspect a saved plan before applying:

```sh
terraform -chdir=deploy/aws plan -var-file=staging.tfvars -out=staging.tfplan
terraform -chdir=deploy/aws show staging.tfplan
terraform -chdir=deploy/aws apply staging.tfplan
```

Creating Multi-AZ RDS, a public ALB, per-AZ NAT gateways, and always-on ECS
services has material recurring cost. Review the regional AWS estimate and the
plan before applying. The RDS instance has deletion protection and Terraform
`prevent_destroy`; do not bypass either as a routine deployment operation.

## Safe bootstrap sequence

1. Set `deploy_application = false` for the first apply. This provisions the
   network, database, load balancer, log groups, task definitions, and empty
   secret entries without starting containers that depend on unpopulated
   secrets. Image references must still be valid immutable digests.
2. Generate an application database password of at least 32 characters and
   store it in `application_database_password_secret_arn`. The same password
   must be used in the app `DATABASE_URL`; store that URL and a separate random
   `SHOPSTOPS_INTERNAL_API_TOKEN` in their Secrets Manager entries. Use the
   organization's secret-entry process; do not pass values as Terraform inputs
   or shell command-line arguments. URL-encode reserved characters in the
   database URL and use `sslmode=verify-full`.
3. Run the one-off ECS task using the output task definition and private network
   identifiers. Its execution role can read the RDS-managed master password and
   application password; the task role has no database or secret permissions.
   Example (substitute the subnet IDs from `private_application_subnet_ids`):

   ```sh
   aws ecs run-task \
     --cluster "$(terraform -chdir=deploy/aws output -raw ecs_cluster_name)" \
     --task-definition "$(terraform -chdir=deploy/aws output -raw database_migration_task_definition)" \
     --launch-type FARGATE \
     --network-configuration 'awsvpcConfiguration={subnets=[subnet-aaa,subnet-bbb],securityGroups=['"$(terraform -chdir=deploy/aws output -raw database_migration_security_group_id)"'],assignPublicIp=DISABLED}'
   ```

   Confirm task exit code zero and inspect its CloudWatch log stream. The runner
   serializes migration attempts, applies each migration transactionally, skips
   matching checksums on repeat runs, and fails closed if an applied migration
   file changes. Do not use the master account for the API.
4. If using Stripe, first populate the three Stripe secrets with values for the
   correct environment, validate webhook signing and Connect/payout readiness,
   then set `enable_stripe = true`. Keep test and live credentials isolated.
5. Verify the configured OIDC issuer, audience, role claim, CORS origins, image
   digests, certificate hostname, database extensions, and applied migrations.
   Then set `deploy_application = true` and apply a reviewed plan.
6. Point staging DNS at the ALB alias target, confirm the certificate and
   `/v1/readyz`, subscribe the operations rotation to `alarm_topic_arn`, and
   perform role-based and payment-provider smoke tests before accepting traffic.

Secret values are read by ECS task execution roles at task start and are not
refreshed in running containers. Rotate them by updating Secrets Manager and
rolling the ECS service. The API's currently configured database URL and
internal token must remain consistent.

## Operations and release limitations

- RDS automated backups are retained for 35 days; a restore drill and recovery
  time objective still need to be demonstrated by the service owner.
- This root does not create the remote-state bucket, ECR repositories, ACM
  certificate, DNS records, OIDC user lifecycle, live Stripe configuration,
  road-routing integration, APNs/FCM setup, app-store builds, or on-call
  procedures.
- Metrics alarms are only a starting point. Subscribe and test the alarm topic,
  set service-specific SLO thresholds, and add tracing and operational
  dashboards before launch.
- The migration runner is tested on a fresh database and a data-preserving
  008-to-009 upgrade in CI. A production owner must still validate its behavior
  against the selected RDS version and rehearse restore and rollback.
- The current mobile dependency graph reports high-severity npm audit findings
  in the Expo/Metro toolchain, including braces and node-forge advisories with
  no upstream patched versions yet. Expo was refreshed to the latest patch in
  SDK 58 and its compatible package versions; this did not clear the advisories.
  The CI audit intentionally blocks release until compatible fixes are
  published and the native build is verified.
- This baseline does not make the shopper, merchant, driver, or admin clients
  release-complete. See `../../docs/architecture.md` for the role-specific
  release gates.

Infrastructure deployment and live provider configuration require authorized
AWS and vendor accounts. This repository provides no production credentials and
does not execute `terraform apply`.
