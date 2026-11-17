# Production release packaging — NOT approved for production

This is release tooling, **not a deployed or end-to-end verified product**.
No AWS provisioning, upload, registry push, Git commit or push is part of this
work. Local Docker engine access and AWS authentication are unavailable. The
legacy [AWS scaffold](../infra/aws/README.md) must not be applied as production.

## Packaged artifacts

- [API image](../docker/api/Dockerfile): multi-stage static Go build, scratch
  runtime, CA certificates, UID/GID 10001, HTTP port 8080. The existing
  database-backed `/healthz` is the load-balancer probe; it does not validate
  Stripe, OSRM, migrations or identity. Scratch has no shell/curl. Supply server
  secrets at runtime, never as build args. `HTTP_ADDR` must match the task port.
- [Identity gateway image](../docker/gateway/Dockerfile): static Go/scratch,
  CA certificates, UID/GID 10001, private HTTP port 8080. Mandatory HTTPS OIDC
  discovery, reviewed exact route policies and default customer-only identity.
  [Gateway configuration and limits](identity-gateway.md) explain trusted claim
  provisioning and blocked checkout/driver/dispatch routes. This implementation
  does not complete the deployed identity, ALB/TLS or production release gates.
- [Admin image](../docker/admin/Dockerfile): lockfile-based `npm ci`, lint and
  Vite production build, nginx UID/GID 101, port 8080 and static `/healthz`.
  [nginx config](../docker/admin/nginx.conf) provides SPA fallback, real asset
  404s, no-store caching and security headers on errors as well as success.
  No gateway proxy or service credentials are included. Current UI accepts a
  user session token in memory; it is not a completed OIDC login flow.
- [Existing OSRM image](../docker/osrm-runtime/Dockerfile): independent routing
  process; the Go API connects using `OSRM_URL`, not S3 downloads. Its
  [runtime contract](../infra/aws/osrm-runtime/README.md) remains mandatory.
- Dockerfile-specific contexts allowlist only build inputs, excluding the
  multi-GB dataset, Git history, node_modules, environment files and keys.
- [Isolated service runtime](../infra/aws/service-runtime/README.md): separate
   Terraform 1.10/AWS 6 root for private ECS/CloudMap, exact secret ARN injection,
   fresh-volume init, existing-cert HTTPS ALB and optional default-on WAF. It
   consumes approved existing infrastructure; no AWS resources were changed.
   Desired defaults are API/gateway/admin 2 each, OSRM 1. Migrations remain an
   external pre-start gate; Stripe credentials are intentionally not wired.

All builds use the **repository root** context. API and gateway use official
`golang:1.26.11-bookworm`, a supported newer compiler satisfying
[go.mod](../go.mod)'s Go 1.25.0 minimum; CI uses the same compiler. Official
[Go image metadata](https://github.com/docker-library/golang/blob/master/versions.json)
was inspected for this version/bookworm variant. Admin uses Node 22.16.0
(compatible with Vite's Node 22.12+ requirement) and nginx 1.28.0 Alpine.
These base tags are **not immutable pins**. No digest has been invented.
Before an approved build, review current security-supported patches, resolve
and pin real platform/index digests for Go, Node and nginx, and review the
PostGIS CI service digest. Align compiler/Node upgrades in CI and Dockerfiles.
OSRM already has an inspected upstream digest; it does not prove compatibility
with the existing local dataset. Scan all final images, produce SBOM/provenance,
sign/verify per approved policy, and record final deployed digests and platform.
Package downloads inside images are not fully reproducible from tags alone.

Run API and gateway with a read-only root filesystem and no Linux capabilities. Run admin
read-only with a small writable `/tmp` tmpfs for PID/temp files; logs go to
stdout/stderr. OSRM needs a **separate fresh ephemeral writable volume**, mode
0700 owned by 10001, at `/data/osrm`, enough storage/memory/CPU, and private S3/STS
access. Never bake datasets or credentials into any image. Runtime execution
and filesystem constraints still need real container/ECS validation.

TLS/HSTS belong at the approved HTTPS edge, not this HTTP-only nginx listener.
CSP currently permits HTTPS connections because the UI accepts a gateway URL;
restrict `connect-src` to approved identity/API origins and implement a trusted
login/configuration flow before release. Gateway CORS must allow only approved
admin origins/headers/methods. Never give the admin/mobile client the shared
internal API token or Stripe/AWS secrets.

## Test-only CI, not CD

[CI](../.github/workflows/ci.yml) runs on main pushes, ordinary pull requests
(including forks), or manual invocation. Fork PR tests intentionally remain
enabled: no secrets, no `pull_request_target`, no deployment environments and
only `contents: read`. Checkout does not persist credentials. Actions are
full commit SHAs verified against official repositories with `git ls-remote`:
checkout v4.2.2, setup-go v5.5.0, setup-node v4.4.0, setup-terraform v3.1.2.
Review action upgrades and workflow changes; branch protection must require
tests, SQL and all three Terraform matrix results. CI consumes runner/network minutes
and downloads dependencies but never accesses an AWS account or remote state.

CI runs Go tests, admin clean install/lint/build, Bash syntax and all offline
suites. Terraform fmt/init/validate checks **only the isolated staging-images,
osrm-runtime and service-runtime roots**, with `-backend=false` and reviewed provider lockfiles.
Service-runtime also runs mock-provider Terraform tests, never live AWS plans.
No plan/apply runs; legacy scaffold validation is not approval and is excluded.
Provider validation is not an infrastructure integration test.

A disposable PostGIS PostgreSQL 16 / PostGIS 3.5 service runs
[SQL integration runner](../scripts/test-sql-integration.sh). It refuses a
nonlocal or differently named database/user and a nonempty public schema (except
extension objects). It never drops a schema or truncates production data.
`psql -X --no-password --set=ON_ERROR_STOP=1` fails on the first error. The explicit
001→011 migration list is reviewed; each runs in its own `--single-transaction`.
The inspected migrations have no top-level BEGIN/COMMIT or concurrent-index
operations. PL/pgSQL BEGIN/END is not a transaction boundary. Each existing
fixture has its own BEGIN/ROLLBACK, so fixtures run **without** an extra wrapper.
Change the runner deliberately when migrations or transaction requirements
change. This is fresh-schema CI, **not** a production migration ledger or an
upgrade/rollback/concurrency rehearsal. Do not run it against deployed data.

Only after every test job passes does a separate job build API/admin/OSRM with
the runner's Docker CLI, check non-root identities and nginx syntax, and smoke-test
admin health/SPA/assets/security headers with a read-only root and no capabilities. Images stay
on the ephemeral runner: no registry login, push, dataset upload, deployment,
OIDC exchange, or artifact promotion. These are single runner-platform builds,
not a multi-architecture release or runtime/e2e proof. No local image build is
claimed while the engine is unavailable; CI itself has not been run here.

## Bounded migration command (not production approval)

[One-shot migration tooling](production-migrations.md) provides a read-only
default plan, immutable SHA-256 history, session advisory locking and atomic
per-file apply using the existing pgx dependency. Explicit production apply
requires verified TLS configuration, actual database-name confirmation and
operator backup/restore evidence labels; the tool does not verify that evidence.
Populated databases without its ledger are refused, with no automatic baseline
or adoption. It is never invoked by API startup. A separate fresh PostGIS CI job
tests this command; local offline tests are not real database validation.
Upgrade/restore rehearsal and every production gate remain open.

## Offline preflight contract

Run [readiness script](../scripts/check-production-readiness.sh) with Bash and
non-secret environment inputs. It makes **no external/cloud calls** and prints
field names/errors, not submitted values. It requires:

| Input | Contract |
| --- | --- |
| `AWS_REGION`, `EXPECTED_AWS_ACCOUNT_ID` | Commercial region shape, nonzero 12-digit account, no sample account |
| `GITHUB_OIDC_PROVIDER_ARN` | Exact GitHub OIDC provider in that account; distinct from application user identity |
| `OSRM_ARTIFACT_BUCKET` | Current runtime's account/region-bound staging bucket; a production storage contract needs separate review |
| `PRODUCTION_DOMAIN`, `PUBLIC_GATEWAY_URL` | Public DNS syntax and same hostname, HTTPS only, no credentials/port/query/fragment |
| `OIDC_ISSUER`, `OIDC_AUDIENCE` | Public HTTPS issuer and bounded audience identifier; gateway must actually validate both |
| `API_IMAGE`, `ADMIN_IMAGE`, `OSRM_IMAGE` | Same-account/region ECR repository references using full lowercase `@sha256:` digest, not tags |
| `OSRM_DATASET_VERSION` | Non-placeholder immutable publisher version, 1–64 alphanumeric/underscore/hyphen characters |

Exit **1** means missing/invalid configuration. Exit **2** means syntax passes
but production remains blocked. It **never exits 0** and has no approval flag or
`*_READY` bypass. An offline check cannot determine whether an account/domain,
issuer, audience, registry digest or bucket actually exists or is approved.
Real AWS identity/permissions, public DNS, TLS chain, OIDC discovery/JWKS and
artifact provenance must be independently verified. Do not use this intentionally
red preflight as a normal CI success step.

[Readiness tests](../tests/production-readiness/test-production-readiness.sh)
cover missing/placeholder/malformed inputs, unsafe URLs, account/region binding,
tags/digests, dataset traversal, no-tool execution and non-bypassable evidence
gates. [SQL runner tests](../tests/sql-integration/test-sql-integration.sh) use
mock psql to verify ordering, transaction flags and immediate failure propagation;
they do not execute SQL. Existing shell suites also use only mocks.

## Evidence gates — all unresolved for production

| Gate | Required evidence / accountable owner |
| --- | --- |
| Identity gateway / security | Deployed OIDC issuer/audience/JWKS validation, token expiry/algorithm rules, role and tenant ownership checks; strip all caller actor/role headers and inject trusted identity; isolate internal-token API from direct clients; approved CORS, HTTPS, DNS, rate limiting and audit logs. Security owner signs negative-path tests. |
| ECS / networking / routing | Reviewed new deployment plan, private task egress/endpoints and DNS/service discovery, API→OSRM security group on 5001, execution vs task role separation, dataset release GET policy, fresh volume permissions, readiness/startup grace/load tests, compatible profile/image/data provenance and real route/table responses. Platform owner signs runtime evidence. |
| Database / migrations | Approved RDS engine/PostGIS availability, SQL `CREATE EXTENSION postgis` using an authorized migration role (not a parameter group), TLS certificate verification, app least privilege, migration version/checksum ledger and exclusive locking, upgrade rehearsal, PITR/backups, deletion protection/final snapshots, restore test and recovery objectives. Database owner signs migration/restore evidence. |
| Deployed e2e / payments / operations | Customer catalog/order/checkout/payment, merchant subscription/catalog, assigned-driver ordered stops/GPS, admin refund, signature-verified Stripe webhook retries/transfer reversals/disputes, real OSRM failures and recovery, outbox consumers and reconciliation, alarms/SLO dashboards, incident runbooks and rollback rehearsal. Product/operations owners sign actual environment test results. |
| Supply chain / budget / release | Reviewed immutable bases/final digests, image scans and provenance, least-privilege runtime secrets, authorized release identity, measured cost/capacity estimates, release manifest and explicit human promotion approval. Release owner signs evidence. |

## Staged deployment sequence (manual approval required; none performed here)

1. Fix local AWS authentication privately in the terminal and verify the intended
   account/region with an authorized operator. Never paste credentials into chat.
2. Review the [isolated runtime wiring and prerequisites](../infra/aws/service-runtime/README.md), plan/state/IAM,
   DNS/TLS, secret rotation, database protections, costs and operational ownership.
   Do not reuse legacy or staging Terraform state for production. No automatic apply.
3. Pass CI, resolve/pin approved image bases, build/scan tested target platforms,
   then publish only after separate authorization. Record source revision, all
   final image digests, migration checksums and dataset version/provenance in a
   reviewed release manifest. No admin ECR repository is provisioned by this work.
4. Reprepare/verify the dataset against the exact OSRM digest/profile if provenance
   is unknown. Publish a fresh immutable version with completion marker last using
   the separately authorized workflow; never reuse a partial release prefix.
5. Take/verify recoverable database backups, rehearse forward migrations on a
   restored copy, then run an approved one-shot migration job inside private
   networking with a migration-specific role and serialization/ledger. Do not
   migrate from every API replica or expose the database for convenience.
6. Start private OSRM tasks with the exact dataset and fresh volumes; verify real
   routes and readiness. Start digest-pinned API tasks with runtime secrets and
   database TLS verification. Exercise API health and authenticated routes privately.
7. Deploy static admin behind HTTPS and identity gateway; test CSP/CORS, role/header
   spoofing failures, Stripe webhooks and all deployed e2e gates. Canary traffic,
   observe metrics and obtain release-owner approval before broader promotion.
   First run [read-only smoke checks](../scripts/test-live-services.sh) for health
   and unauthenticated denial paths (8 GETs, optional private OSRM route as 9th).
   No actual JWT is accepted by this harness and no payment/order/refund mutation
   is sent. Stripe test-mode signed replay/reconciliation is a separate authorized
   manual staging exercise; no payment-completion claim follows from these probes.

## Rollback and cost review

Retain previous image digests, task definitions, compatible dataset/profile and
release manifest. Drain/shift traffic back to the last proven tasks; OSRM rollback
starts **new tasks/volumes**, not downloader cache reuse. Admin rollback replaces
its whole immutable image. Ensure old API/schema compatibility **before** any
migration: prefer expand/contract changes and forward repair. No down migrations
or automated database rollback are supplied. Snapshot/PITR restore is a coordinated
recovery to a separate instance with planned RPO/RTO, traffic control and Stripe
reconciliation; it can lose writes and is not a casual task rollback.

Budget actual Fargate CPU/memory and overlapping canaries, OSRM startup duration
and ephemeral disk, ALB hours/LCUs, RDS/Multi-AZ/storage/backups, NAT vs VPC endpoint
hours/traffic, S3 GETs/version retention/transfers, ECR storage/scans, KMS/Secrets
Manager, logs/alarms and CI minutes. Dataset size/load memory must be measured;
the legacy 1 GiB OSRM task is not validated capacity. Review retention/cleanup
without deleting needed rollback datasets. **No new cloud charges were provisioned.**

## Local validation boundary

Local Go tests and shell tests run independently of AWS. The isolated runtime root
uses the existing temporary Terraform binary with disposable provider/data caches
for backend-disabled validation and mocked tests; caches are removed afterward.
Live SQL/container/AWS validation is separate and not implied. Record actual
pass/fail counts in the change report, not assumed CI outcomes. A green build
alone would still leave the production evidence gates above unresolved.
