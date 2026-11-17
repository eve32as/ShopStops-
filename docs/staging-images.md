# ShopStops AWS staging images and OSRM artifacts

## Purpose

Keep application source in GitHub and publish large New York OSRM datasets to
private AWS storage. GitHub's normal Git file-size limit cannot be increased;
generated routing datasets remain excluded from Git.

This setup prepares image repositories and artifact storage. It does **not**
make the full ShopStops application production-ready or deploy its services.

## Implemented components

- `infra/aws/staging-images/`: private ECR repositories for API and OSRM images,
  immutable image tags, scan-on-push, and a versioned private S3 artifact bucket.
- A GitHub OIDC publishing role restricted to this repository's `main` branch,
  without general infrastructure deployment permissions.
- `scripts/configure-staging-images.sh`: validates configuration and AWS account
  identity, checks the approved Terraform backend, and defaults to planning.
  Applying requires explicit selection and interactive confirmation.
- `scripts/publish-osrm-data.sh`: validates the prepared dataset, generates
  SHA-256 checksums, and uploads artifacts before publishing their manifest.
  Artifact writes are create-only rather than overwriting existing releases.
- `scripts/staging-images-common.sh`: shared input and prerequisite checks.
- `scripts/prepare-osrm-data.sh`: prepares routing data using Docker.
- `tests/staging-images/`: offline tests with fake AWS and Terraform fixtures.
- [Separate OSRM runtime role and operating contract](../infra/aws/osrm-runtime/README.md),
  [verified startup wrapper](../scripts/start-osrm-from-s3.sh), and
  [runtime image](../docker/osrm-runtime/Dockerfile), with
  [offline runtime tests](../tests/osrm-runtime/test-osrm-runtime.sh).

## Required configuration

| Variable | Value required |
| --- | --- |
| `AWS_REGION` | `us-east-1` for the intended staging setup |
| `EXPECTED_AWS_ACCOUNT_ID` | Actual 12-digit AWS account ID |
| `GITHUB_REPOSITORY` | `eve32as/ShopStops-` |
| `GITHUB_OIDC_PROVIDER_ARN` | Existing GitHub OIDC provider ARN in that account |
| `TF_STATE_BUCKET` | Approved, existing Terraform state bucket |
| `TF_STATE_KEY` | `shopstops/staging-images/terraform.tfstate` |
| `TF_STATE_KMS_KEY_ARN` | Approved, existing state encryption key ARN |

Do not use placeholder account IDs, bucket names, or ARNs. Never put AWS access
keys, session tokens, database passwords, or Stripe secrets in this document or
Git. Authenticate directly through the AWS CLI using your approved login method.

## Prerequisites

- Valid AWS authentication in the intended account.
- AWS CLI v2, Terraform 1.10 or newer, and Bash.
- Existing GitHub OIDC provider.
- Approved state bucket with versioning, public-access blocking, and a
  non-public bucket policy.
- Enabled symmetric KMS key in the expected account and region.
- Docker when preparing OSRM data locally.

The GitHub runner path `/home/runner/work/ShopStops-/ShopStops-/` is not a local
macOS path. Locate scripts relative to the repository checkout when running
locally.

## Operating sequence

1. Authenticate AWS and confirm the intended account.
2. Supply the real identifiers listed above.
3. Run the staging configuration script in its default planning mode.
4. Review planned resources, permissions, and recurring AWS costs.
5. Explicitly apply only after approval and confirmation.
6. Publish a versioned, prepared OSRM dataset using the publishing script.
7. Build and publish API and OSRM images using immutable release tags.

Publishing is currently manual; no GitHub Actions image-publishing workflow is
configured. The OIDC role does not by itself create a CI workflow.

## How S3 is accessed

The OSRM bucket is **private**. It is accessed through AWS API requests signed
with IAM credentials, not through a public download link. Do not put AWS
credentials in the admin or mobile applications.

### Manual publishing

An authenticated operator runs `scripts/publish-osrm-data.sh` with a new release
version. In addition to the AWS region and expected account ID, the script
requires `OSRM_ARTIFACT_BUCKET` to match
`shopstops-staging-osrm-<account-id>-<region>`.

The operator needs the bucket inspection/list permissions used by the script
and permission to upload into `datasets/new-york/`. The script verifies account,
bucket ownership and region, and that the chosen release prefix is empty.

Runtime files are stored under `datasets/new-york/<version>/`. The raw OSM PBF
is not uploaded. Every upload uses HTTPS, S3-managed AES256 encryption, a SHA-256
checksum, and `If-None-Match: *` to prevent overwriting an existing object.
`SHA256SUMS` is uploaded last as the completion marker.

### GitHub publishing identity

The configured GitHub OIDC role trusts only
`repo:eve32as/ShopStops-:ref:refs/heads/main`, with audience
`sts.amazonaws.com`. A future workflow can exchange its OIDC token for temporary
AWS credentials without storing long-lived AWS access keys in GitHub.

Its S3 permissions are intentionally upload-only for the dataset prefix: it
does not grant `GetObject`, `ListBucket`, or `DeleteObject`. Therefore the manual
publishing script, which performs bucket inspection and listing, cannot run
unchanged under that role. A dedicated CI publishing flow is still required.

### OSRM runtime reads

The [separate runtime Terraform root](../infra/aws/osrm-runtime/README.md) now
defines an ECS **task role**, distinct from both the publisher and image-pull
execution role, allowing only `s3:GetObject` on exact keys in one approved release.
The startup wrapper verifies the caller account, GETs a size-bounded completion
manifest with expected bucket owner, strictly allowlists the 26 required artifacts
and optional base, and verifies every SHA256 before atomically publishing a fresh
private directory and execing the separate OSRM server on port 5001. It never lists,
inspects, writes or deletes S3 objects. Endpoint overrides and existing local targets
are rejected; no cache is reused. The Go API does not download routing data.

Runtime IAM credentials come from ECS, never admin/mobile clients. Supply the five
runtime environment values documented in the separate README, a dedicated mode-0700
ephemeral volume owned by the runtime user, free disk roughly at least the dataset
size plus margin, and a preparation-compatible digest-pinned OSRM image.
Fresh task starts incur S3 GET/transfer costs and future compute/storage/network
costs. These files **do not deploy anything**. Offline tests do not confirm a live
bucket, successful upload, actual IAM permissions, container build or real route.
The staging Terraform output `osrm_artifact_bucket` supplies the actual bucket name
only after successful provisioning.

## Local validation

From the repository root:

```sh
bash tests/staging-images/test-staging-images.sh
bash tests/osrm-runtime/test-osrm-runtime.sh
find scripts tests/staging-images tests/osrm-runtime -type f \
  \( -name '*.sh' -o -path '*/fixtures/aws' -o -path '*/fixtures/terraform' -o -path '*/fixtures/osrm-routed' \) \
  -exec bash -n {} \;
```

The offline suite covers configuration validation, backend checks, plan/apply
behavior, symlink rejection, and checksummed uploads. Mock tests and shell syntax
checks do not establish that a real AWS deployment has succeeded.

## Remaining production work

- Authenticated identity gateway and role enforcement.
- HTTPS, DNS, and appropriate private networking.
- OSRM container build/real dataset compatibility validation, ECS wiring,
  internal discovery, and health checks (verified runtime loader is implemented).
- Database migrations, backups, and recovery verification.
- CI/CD workflows, monitoring, and end-to-end deployment tests.

Successful local validation is not evidence that AWS resources were provisioned,
images were pushed, or the dataset was uploaded. Record those outcomes separately
after live verification.
