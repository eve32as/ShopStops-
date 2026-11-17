# OSRM verified S3 runtime (not deployed)

This **separate Terraform root** defines only an ECS task IAM role and its inline
read policy. It does not provision ECS, S3, ECR, networking, state storage, or any
service. It does not modify the legacy application scaffold or publisher role.
No provisioning, uploads, image builds, or deployment are performed by this change.
Future use requires an approved, separate state/backend; never share the legacy
or staging-images state. The root has no backend bootstrap or apply automation.

## Role and inputs

Terraform requires 1.10–1.x and AWS provider 6.x. All inputs are mandatory:

| Terraform input | Runtime environment | Contract |
| --- | --- | --- |
| `expected_aws_account_id` | `EXPECTED_AWS_ACCOUNT_ID` | Real nonzero 12-digit account |
| `aws_region` | `AWS_REGION` | Commercial region of bucket and ECS tasks |
| `osrm_artifact_bucket` | `OSRM_ARTIFACT_BUCKET` | `shopstops-staging-osrm-<account>-<region>` |
| `osrm_dataset_version` | `OSRM_DATASET_VERSION` | Publisher version: 1–64 letters/digits/underscores/hyphens, initially alphanumeric, no placeholders |

Outputs: `task_role_arn` and `runtime_environment` (including
`OSRM_DATA_PARENT=/data/osrm`). No credentials are included.

Assign the role to **taskRoleArn**, not **executionRoleArn**. The execution role
separately handles ECR image pulls and logging; it is not the identity used by
the downloader. Task trust allows only `ecs-tasks.amazonaws.com`, with exact
SourceAccount and region/account-scoped ECS SourceArn. ECS currently requires
the ECS ARN wildcard rather than a cluster-specific trust condition. Restrict
who can pass this role and register/run task definitions separately.

The only permission granted is `s3:GetObject` on the **28 exact keys** in one
release: SHA256SUMS, 26 required files, and the optional base file. There is no
ListBucket, bucket inspection, write, delete, GetObjectVersion, state, KMS, or
publisher access. STS GetCallerIdentity needs no explicit IAM allow. The bucket
uses SSE-S3, not SSE-KMS. ResourceAccount and TLS conditions further bound reads.
Future bucket policy changes or encryption changes need separate review.

Use ECS-provided temporary IAM credentials through the default AWS CLI provider
chain. Do not bake keys into the image, pass client credentials, or put AWS
credentials in the mobile/admin apps. Network access to regional S3 and STS
is required; no endpoint overrides are accepted. The Go API **never downloads
the dataset**: point its existing OSRM URL configuration at this separate,
privately reachable routing server.

## Startup contract

[Startup wrapper](../../../scripts/start-osrm-from-s3.sh) accepts no arguments.
It shares identity/bucket/placeholder validation and endpoint rejection with
[common validation](../../../scripts/staging-images-common.sh). Configuration
and filesystem validation precede AWS calls. It uses only AWS CLI v2 STS identity
and exact S3 GetObject requests with `--expected-bucket-owner`; it deliberately
does not inspect/list the bucket. Region is explicitly selected for every call;
an incorrect bucket region fails without requiring GetBucketLocation permission.

Mount a dedicated **ephemeral** writable volume at `/data/osrm`, owned by UID/GID
10001 with mode **0700**. A mount hides the image directory permissions: arrange
ownership/permissions before starting the container, not using the downloader.
For local use, `OSRM_DATA_PARENT` may select another existing clean absolute path
owned by the current user with mode 0700. All path components must be nonsymlink
directories; on macOS use the resolved `/private/tmp` rather than `/tmp`.
Do not share the parent with other same-UID writers. The private parent plus
exclusive download lock prevents cooperating starts from racing directory rename.

Startup downloads into a mode-0700 temporary directory in that parent, never into
an existing dataset. It GETs at most **4097 manifest bytes**, requiring ContentRange
to prove the complete manifest is **1–4096 bytes**, then strictly parses exact
newline-terminated lowercase SHA256 records with two spaces. It rejects malformed,
binary, duplicate, traversal, unknown, or missing-required entries before artifact
GETs. The optional base `new-york.osrm` is allowed only with a checksum. The exact
26-file list matches [the publisher](../../../scripts/publish-osrm-data.sh).

Every artifact must be a nonempty regular nonsymlink file, at most the publisher's
5 GiB per-file limit, with the expected SHA256. Only then is the directory renamed
atomically to `/data/osrm/verified` on the same filesystem, and the wrapper execs
`osrm-routed --algorithm mld --port 5001` with the fixed verified New York base path.
Existing targets (even empty, symlinked, or previously verified) fail closed:
**no unverified cache reuse**. A task restart needs a fresh ephemeral directory.
Download/parser/checksum/exec failures remove owned temporary/verified data and
release the lock; EXIT/INT/TERM are trapped. SIGKILL cannot be trapped. Once exec
succeeds, OSRM owns the process and subsequent OSRM failures are not shell cleanup
events. Dispose of that task volume; never treat leftovers or stale locks as cache.

SHA256 verifies manifest-described bytes, not provenance or OSRM cross-file/ABI
consistency. Keep the completion-marker-last immutable publisher policy, protect
publisher identity, and validate a real route with a matching preparation/runtime
build before approving a dataset. A malicious authorized publisher can replace
both hashes and contents with matching values.

## Runtime image and compatibility

[Dockerfile](../../../docker/osrm-runtime/Dockerfile) uses repository-root build
context and explicit **v26.11.0-debian**. Inspection on 2026-11-14 of the upstream
[package metadata](https://github.com/Project-OSRM/osrm-backend/pkgs/container/osrm-backend)
shows the preparation script's untagged default resolves to `latest`, currently
26.11.0 Debian with Linux amd64 and arm64 images (plus index attestation).
The [upstream Dockerfile](https://github.com/Project-OSRM/osrm-backend/blob/v26.11.0/docker/Dockerfile-debian)
uses Debian trixie and explicitly supports both architectures. The selected
multi-platform digest is
`sha256:e1929ae2818226284d7ef007329098a30851c11528ab3712bc9c8d68da78278b`.

The runtime installs Debian's AWS CLI **v2**, Bash, coreutils checksum tools, and
CA certificates; build checks the CLI major version and runs as UID/GID 10001.
It exposes port 5001 and replaces image entrypoint with the verified wrapper.
No dataset or credentials are copied into the image.

**Digest pinning is required for any approved build**, including overrides of
`OSRM_BASE`. Match the preparation build's actual digest/architecture/profile,
not today's mutable `latest`; the existing local dataset's generating digest is
not established by metadata inspection. Reprepare/revalidate if incompatible.
Package versions are not fully reproducible: lock the final built image digest,
scan it, and review updates. Container build, actual installed CLI, image-platform
execution, and real dataset routing have not been tested here.

## Storage, costs, and validation limits

Allow free ephemeral disk **at least roughly one complete dataset**, plus manifest,
container layers, logs and operational margin; rename does not duplicate files.
Artifacts are downloaded serially, but malformed/oversized artifact objects can
consume disk before post-download checks; size storage limits accordingly.
Provision enough memory/CPU for MLD loading and routes. Startup downloads on each
fresh task add S3 GET and transfer costs; future ECS compute, ephemeral storage,
ECR storage, logs, and NAT/network traffic may also cost money. IAM roles themselves
have no charge. **No compute resources or new charges were provisioned here.**

[Offline suite](../../../tests/osrm-runtime/test-osrm-runtime.sh) uses fake AWS and
OSRM executables and tiny generated datasets. It exercises required/optional
success, manifest/file/checksum failures, strict parser cases, bounded manifest,
identity/endpoints, symlinks/existing paths, CLI versions, executable failures,
locks, cleanup, and audits every AWS command for STS/GET-only behavior. Run it
with Bash alongside the existing staging suite and Bash syntax checks. Offline
tests do not establish a live bucket, upload, IAM assumption, Terraform provider
validation/plan, image build, ECS wiring, health checks, or successful real routes.

### Local validation on 2026-11-14

- Terraform 1.10.5 was downloaded temporarily and checked against the published
  SHA-256 checksum. Both staging-images and osrm-runtime passed formatting and
  `terraform validate` with the signed AWS provider 6.73.0.
- Initialization used `-backend=false`; no remote state or AWS resources were
  accessed. Provider lock files were retained; disposable provider binaries were
  removed afterward to reclaim disk space.
- The Linux amd64 runtime image build was attempted but blocked because Docker's
  engine socket was unavailable. No successful image build is claimed.
- A Dockerfile-specific ignore file excludes datasets, dependencies, and Git
  history from the runtime build context.

Finish Docker Desktop setup/startup before retrying the container build. Provider
validation does not substitute for an approved plan or a live runtime test.
