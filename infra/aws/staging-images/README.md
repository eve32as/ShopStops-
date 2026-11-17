# Bounded staging image publishing

This is a **separate Terraform root module**, not the parent AWS application
scaffold. It is not a production-ready application deployment: no running API,
OSRM service, networking, database, secrets, TLS endpoint, or release pipeline is
created. No GitHub workflow is included and nothing is published automatically.

## Resources and boundaries

- Two private ECR repositories: `shopstops/staging/api` and
  `shopstops/staging/osrm`, AES256 encrypted, immutable tags, basic scan-on-push.
- One private OSRM artifact bucket:
  `shopstops-staging-osrm-<account>-<region>`, versioned, SSE-S3 AES256 encrypted,
  bucket-owner-enforced, all four public access blocks, and HTTPS-only access.
  Dataset writes must use `If-None-Match: *` (create only). Destruction is guarded
  by Terraform `prevent_destroy`; nonempty buckets/repositories are not forcibly
  deleted. Administrators can still change policies or delete objects; this is
  not S3 Object Lock/WORM storage.
- One one-hour GitHub publishing role using an **existing** OIDC provider.
  Trust uses exact `StringEquals` for audience `sts.amazonaws.com` and subject
  `repo:eve32as/ShopStops-:ref:refs/heads/main`. No wildcard, PR subject, other
  repository, tag, or GitHub environment subject is accepted.
- Role permissions: ECR authorization token on `*` (AWS requires this), layer
  checks/uploads and `PutImage` only on the two repositories; `PutObject` only
  below this bucket's `datasets/new-york/`, with AES256 and conditional-create
  headers. **No** S3 listing/reading/deletion, arbitrary-bucket access, state
  access, KMS, IAM administration, Terraform deployment or runtime app permissions.
  Do not attach additional policies to this role. A same-account existing backend
  must not independently grant it access through a public/broad resource policy.

Costs are usage-based: ECR image storage/scanning configuration, S3 artifact and
version storage/requests/transfers, plus the already-existing state bucket and
KMS key requests/monthly key costs. No compute or database charges are introduced.
No automatic retention/deletion is configured: immutable image tags, incomplete
uploads and historical versions accumulate until an administrator reviews them.

## Prerequisites and local bootstrap

AWS CLI v2 and Terraform **>=1.10, <2** must already be installed. The AWS provider
is constrained to major 6. Initial `init` downloads the provider and writes a
local lockfile; review and commit that lockfile before shared use. No tool install,
login, state bucket creation, KMS creation or OIDC provider creation is performed
by the script. Authenticate **directly in your terminal**, never paste credentials
into chat or source control. `InvalidClientTokenId` must be resolved before use.

An administrator must approve these existing backend prerequisites:

- A bucket owned by the expected account, in the selected region, versioning
  Enabled, all four public access blocks, and a non-public **bucket policy**.
  A missing policy fails closed even if other protections make the bucket private.
- Default SSE-KMS encryption using the exact full key ARN supplied, not an alias
  or bare key ID. Key must be Enabled, symmetric `ENCRYPT_DECRYPT`, same account
  and region. Multi-region `mrk-` key IDs are intentionally not accepted.
- The existing GitHub OIDC provider in that account must have the expected URL
  and include the STS audience.
- Dedicated backend key `shopstops/staging-images/terraform.tfstate`, separate
  from the parent scaffold, and the adjacent `.tflock` key. The state bucket and
  KMS policies must restrict these to local infrastructure administrators, not CI.

The local bootstrap identity needs STS identity verification; the S3 bucket
owner/location/versioning/encryption/public-block/policy-status reads; KMS
`DescribeKey`; IAM `GetOpenIDConnectProvider`; S3 ListBucket/GetObject/PutObject
for that state key and GetObject/PutObject/DeleteObject for its `.tflock` (prefix
scope listing when possible); and KMS Encrypt/Decrypt/GenerateDataKey on the
approved state key. Terraform additionally needs ECR repository creation/settings
and read/tag management, S3 bucket creation and configuration read/write, and
IAM role/inline-policy creation/read/tag management for the resources above.
Use an administrator-approved, temporary **local bootstrap identity**, not the
GitHub image-only role. Backend verification does not prove that every future
write will be authorized or that policies cannot subsequently change.

Set these non-secret environment values to approved real values before invoking
[the configuration script](../../../scripts/configure-staging-images.sh):
`AWS_REGION` (e.g. `us-east-1`), `EXPECTED_AWS_ACCOUNT_ID`,
`GITHUB_REPOSITORY=eve32as/ShopStops-`, `GITHUB_OIDC_PROVIDER_ARN`,
`TF_STATE_BUCKET`, `TF_STATE_KEY=shopstops/staging-images/terraform.tfstate`,
and `TF_STATE_KMS_KEY_ARN`. Missing values/placeholders are rejected before tools
run. Unsupported repository, state key, account/region ARN combinations and
Terraform CLI auto-approval overrides are rejected. Values and AWS CLI error
details are not printed by validation; inspect authentication problems yourself
in your terminal without enabling shell tracing or credential debug logs.

Run with Bash from any working directory. Default mode is `plan`; the only other
accepted argument is `apply`, which requires Terraform's interactive approval.
Both initialize/reconfigure the approved S3 backend, select the default workspace,
and validate this module. `.terraform` remains inside this module and existing
repository ignores cover working directories, state and private inputs. Plan is
not offline: it downloads providers, accesses remote state, and refreshes AWS.
There is no automatic apply. Backend infrastructure is never created here.

## Manual OSRM artifact publishing

[The upload script](../../../scripts/publish-osrm-data.sh) accepts a version and
an optional dataset directory. Default input is the repository's generated
New York MLD directory. Set `AWS_REGION`, `EXPECTED_AWS_ACCOUNT_ID`, and
`OSRM_ARTIFACT_BUCKET` to the module output. The destination is bounded to the
derived staging bucket and `datasets/new-york/<version>/`. Versions are 1-64
letters/digits/underscores/hyphens, start alphanumeric, and never contain dots,
slashes, traversal or whitespace. Use a unique immutable version per attempt.

This **manual** operator needs STS identity verification, bucket owner/location
reads (including ListBucket for HeadBucket), listing the version prefix, and
conditional AES256 PutObject below the dataset prefix. The image-only GitHub
role deliberately lacks those read/list permissions and cannot run this manual
preflight script; do not broaden that role to accommodate it.

All 26 allowlisted existing generated runtime extensions are required, nonempty
and not symlinks; an optional nonempty base `.osrm` file is also included. No
source `.osm.pbf`, unknown extension, subdirectory or local manifest is uploaded.
Stop preparation and do not mutate input while the snapshot is copied: file
checks/hashes cannot prove cross-file OSRM build consistency or ABI compatibility.
Pin the OSRM runtime image/build profile separately and preserve OSM attribution.

The script rejects any nonempty version prefix; 403 and other listing/owner
errors are **not** treated as absence. It snapshots files to a private temporary
directory under `/tmp`, generates `SHA256SUMS` there, and uploads with SHA256
request checksums and `If-None-Match: *`. The marker is uploaded **last**, only
after every artifact succeeds. Conditional writes protect against concurrent
publishers; no overwrite, multipart upload, `--delete` or cleanup of remote
partial versions occurs. Exit/signal traps remove the local snapshot (SIGKILL
cannot be trapped). Allow roughly one extra dataset's disk space in `/tmp`.
Each file is capped at 5 GiB for single-request PutObject; AWS CLI v2 must support
`--if-none-match` and checksums. Failure leaves an incomplete remote prefix;
investigate as an administrator and use a new version, rather than resume/reuse.
Consumers must require the completion marker and verify its hashes before use.

## Future workflow and verification

Manually configure a reviewed main-branch-only GitHub publishing workflow later,
with minimal `contents: read` / `id-token: write`, pinned action commits, the role
ARN output and explicit region/account checks. Use immutable image version tags.
It may publish images and conditional dataset objects, not run Terraform. PRs
and workflows using GitHub environments have different OIDC subjects and fail
this trust. Protect main and review workflow changes: any workflow with a valid
main-branch subject can assume this role. Runtime pull/read roles are out of scope.

[Offline shell tests](../../../tests/staging-images/test-staging-images.sh) use
fake PATH AWS/Terraform executables and tiny local datasets; they never call the
cloud. They cover validation-before-tools, wrong-account/backend gates, plan vs
interactive apply, owner/403/version checks, unsafe input and symlink rejection,
checksummed runtime-only uploads and manifest-last behavior. Run them with Bash;
also use `bash -n` on scripts and fixtures. Terraform provider validation and a
real plan remain unverified until tools, approved backend and valid local AWS
authentication are available. Offline mocks are not an AWS integration test.