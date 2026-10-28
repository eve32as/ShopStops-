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
  already issued in that same region for the public gateway hostname.
- The public Route 53 hosted-zone ID containing the gateway hostname. Terraform
  creates an A-alias record to the load balancer.
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

## GitHub Actions OIDC deployment workflow

`.github/workflows/terraform-deploy.yml` provides a manually dispatched plan and
apply path. It does not use long-lived AWS keys, run automatically on pushes, or
run database migrations. Apply requires the `apply` input, the exact
`confirm_apply=APPLY` input, and approval from the selected GitHub environment.
Configure the `production` environment with required reviewers and restrict
deployments to the protected release branch before allowing production use.

Create four GitHub environments: `staging-plan`, `production-plan`, `staging`,
and `production`. Set environment variables in each:

- `AWS_ROLE_ARN`: the role for that environment; plan environments use a
  read-oriented role, while `staging` and `production` use their separately
  approved deployment roles.
- `AWS_REGION`, `TF_STATE_BUCKET`, `TF_STATE_KEY`, and `TF_STATE_KMS_KEY_ARN`.
  Use a different state key for staging and production.
- The Terraform inputs read by the workflow as environment variables:
  `TF_VAR_project_name`, `TF_VAR_availability_zones` (JSON list),
  `TF_VAR_certificate_arn`, `TF_VAR_gateway_hostname`,
  `TF_VAR_route53_zone_id`, `TF_VAR_oidc_issuer`, `TF_VAR_oidc_audience`,
  `TF_VAR_api_image`, `TF_VAR_gateway_image`, and `TF_VAR_migration_image`,
  plus `TF_VAR_db_engine_version`.

These are configuration values, not credentials. Do not place secret values in
GitHub variables or Terraform inputs. Configure the AWS IAM OIDC provider for
`https://token.actions.githubusercontent.com`; each role trust policy should
require audience `sts.amazonaws.com` and the exact subject for its GitHub
environment, for example
`repo:eve32as/ShopStops-:environment:production`. Do not trust all branches or
all repositories. Grant plan roles only the provider read permissions and
scoped state/lock access needed to plan; grant apply roles only the resource
permissions needed for this Terraform root and the same scoped state access.
Keep the GitHub role ARN as a variable, not an access key or credential.

Run the workflow from the approved protected branch with `apply` false to
produce a plan for review. Download the short-lived plan artifact and inspect it
with Terraform 1.12.2 (`terraform show shopstops.tfplan`). The plan file can
contain sensitive infrastructure details: restrict artifact access and do not
attach it to public issues or logs. When ready to apply, dispatch another run
with the same configuration, select `apply`, enter `APPLY`, inspect that run's
new plan artifact, and approve the protected environment. The apply job waits
for that approval and applies only the plan generated in its own run. For
application rollout, first populate secrets and complete the
migration/bootstrap steps below; only then select `deploy_application`.
Choose the approved ALB access-log retention period in the workflow input; it
defaults to 365 days and is applied through Terraform's
`alb_access_log_retention_days` variable.

This workflow is scaffolding, not evidence of production readiness. It does not
create the AWS account, state bucket, IAM roles, OIDC application/users, ECR
repositories, secrets, or app-store credentials, and it has not been run against
a real AWS account. The production owner must validate the role policies and
trust conditions, run a staging rehearsal, and confirm protected environment
reviewers and branch restrictions before use.

Set deployment inputs through an ignored `*.tfvars` file or the approved
deployment system. Do not store credentials or secret values in that file.
Set `gateway_hostname`, `route53_zone_id`, and `certificate_arn`. Before planning,
verify the certificate using the authorized AWS identity:

```sh
AWS_REGION='us-east-1' \
CERTIFICATE_ARN='arn:aws:acm:us-east-1:123456789012:certificate/replace-me' \
GATEWAY_HOSTNAME='api.staging.example.com' \
  scripts/validate-acm-certificate.sh
```

The preflight rejects certificates that are pending validation, in another
region, or do not cover the hostname (including invalid wildcard depth). The
HTTPS listener attaches the ACM certificate and enforces
`ELBSecurityPolicy-TLS13-1-2-2021-06`; port 80 only redirects to HTTPS. Terraform
maps the configured hostname to the ALB through the specified public hosted
zone. Keep ACM DNS-validation records in place so ACM can renew the certificate.

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
6. Confirm the Terraform-created hostname alias, certificate chain, and
   `/v1/readyz`; subscribe the operations rotation to `alarm_topic_arn`, and
   perform role-based and payment-provider smoke tests before accepting traffic.

Secret values are read by ECS task execution roles at task start and are not
refreshed in running containers. Rotate them by updating Secrets Manager and
rolling the ECS service. The API's currently configured database URL and
internal token must remain consistent.

## ECS monitoring and restart runbook

ECS Container Insights supplies service CPU, memory, and running-task metrics;
CloudWatch alarms notify the configured SNS topic when either service remains
above its configured CPU/memory threshold or falls below
`ecs_min_healthy_tasks`. The ALB separately checks gateway `/v1/readyz`, and
existing alarms report unhealthy gateway targets and elevated 5xx responses.
Subscribe an on-call address to the SNS topic and confirm the subscription
before enabling application services. Alarm thresholds are initial operating
defaults; tune them against measured staging traffic and task sizing.

For an alarm or failed deployment:

1. Check the alarm details, ECS service events/deployment history, ALB target
   health, and the relevant API or gateway CloudWatch log group. Confirm whether
   the issue is load, a bad task image/configuration, dependency health, or a
   deployment/migration problem before changing capacity.
2. For a routine restart or secret refresh, replace tasks through ECS rather
   than terminating individual tasks:

   ```sh
   aws ecs update-service --cluster "$CLUSTER" --service api --force-new-deployment
   aws ecs wait services-stable --cluster "$CLUSTER" --services api
   ```

   Repeat for `gateway` only when needed. Confirm the service has at least
   `ecs_min_healthy_tasks` running tasks and the gateway targets are healthy
   before declaring recovery. The deployment uses a 100% minimum healthy
   percentage, 200% maximum, and rollback-enabled circuit breaker; preserve that
   capacity policy during recovery.
3. If a new task definition caused the incident, update the service to the
   previously approved immutable task-definition revision, wait for stability,
   and verify readiness and role-based smoke tests. Do not roll back database
   migrations by editing migration history; use an approved forward fix.
4. Record the alarm, impact, mitigation, and follow-up. Escalate if recovery
   requires changing desired capacity, security groups, database state, or
   payment configuration.

Fargate does not provide host SSH. Do not install a privileged in-container
monitor or use manual task termination as a restart mechanism. Use AWS-managed
metrics, service events, application logs, ECS deployments, and the configured
alarm channel for routine operations. ECS Exec is disabled in this foundation.

The `Validate` workflow runs Go, database, Terraform, migration-image, client,
and operational-script checks. Configure the repository's protected release
branch to require the `Release gate` status check; the gate fails unless every
validation job succeeds. Keep the dependency audits blocking: unresolved audit
findings require an approved dependency fix or explicit security-owner exception,
not a CI bypass.

## Operations and release limitations

- RDS automated backups are retained for 35 days; a restore drill and recovery
  time objective still need to be demonstrated by the service owner. These
  automated backups are not a substitute for independent logical exports.
- `scripts/backup-postgres-s3.sh` creates a custom-format `pg_dump`, checks its
  archive and SHA-256 after an SSE-KMS S3 upload/download round trip, and uses
  `verify-full` PostgreSQL TLS. Run it from an approved private-network backup
  runner with an authorized read-only backup database role and an AWS workload
  identity. The role needs scoped S3 `PutObject`/`GetObject` plus KMS
  `GenerateDataKey`/`Decrypt` permissions for the selected key; the bucket must
  enforce TLS, enable versioning, and have an approved lifecycle/retention
  policy. Supply the database password via the approved secret manager or
  protected `PGPASSFILE`, never as a command argument. Example environment:

  ```sh
  export PGHOST='private-rds-endpoint'
  export PGUSER='shopstops_backup'
  export PGDATABASE='shopstops'
  export PGSSLMODE='verify-full'
  export PGSSLROOTCERT='/etc/ssl/certs/rds-global-bundle.pem'
  export BACKUP_S3_URI='s3://approved-backup-bucket/shopstops/production'
  export BACKUP_SSE_KMS_KEY_ID='arn:aws:kms:us-east-1:123456789012:key/replace-me'
  scripts/backup-postgres-s3.sh
  ```

  Schedule it through an approved production scheduler and alert on nonzero exit
  or stale backup age. Restrict bucket and KMS access, keep backups separate
  from the database account, and rehearse checksum verification plus
  `pg_restore` into an isolated database before relying on the recovery point.
- This root does not create the remote-state/backup buckets, ECR repositories,
  ACM certificate or its DNS validation records, OIDC user lifecycle, live Stripe configuration,
  road-routing integration, APNs/FCM setup, app-store builds, or on-call
  procedures.
- Metrics alarms are only a starting point. Subscribe and test the alarm topic,
  set service-specific SLO thresholds, and add tracing and operational
  dashboards before launch.
- CloudWatch application and RDS log groups use a rotated customer-managed KMS
  key. ALB access logs are enabled to an environment-specific private S3 bucket
  using SSE-S3 because ALB log delivery does not support SSE-KMS. The bucket
  lifecycle expires logs after `alb_access_log_retention_days` (365 days by
  default); set this to the approved policy and verify delivery and access
  controls in the target AWS account before launch.
- The migration runner is tested on a fresh database and a data-preserving
  008-to-009 upgrade in CI. A production owner must still validate its behavior
  against the selected RDS version and rehearse restore and rollback.
- The mobile dependency graph still reports Expo toolchain audit findings:
  three high and five low at the last audit, including Zod and deepmerge.
  `node-forge` was overridden to patched 1.4.2. The remaining findings require
  compatible upstream fixes or an explicit security-owner exception; do not
  force a major Expo downgrade to clear the audit. CI intentionally blocks
  release until findings are resolved or formally excepted and the native build
  is verified.
- This baseline does not make the shopper, merchant, driver, or admin clients
  release-complete. See `../../docs/architecture.md` for the role-specific
  release gates.

Infrastructure deployment and live provider configuration require authorized
AWS and vendor accounts. This repository provides no production credentials and
does not execute `terraform apply`.
