# Isolated service runtime — wiring only, not deployed or release-approved

This separate Terraform root requires **Terraform >=1.10,<2 and AWS provider 6**.
It does not import/call/change the legacy root, staging-images root or OSRM role
root. No plan against AWS, apply, image publication, database migration, Git commit
or push is performed by adding these files. Validation uses backend-disabled init
and a mocked provider test suite; neither proves an AWS deployment works.

## Existing prerequisites and inputs

All identifiers must come from an independently approved environment. Terraform
checks syntax/account/region bindings, not existence, routing, ownership or approval.
No data source reads secret values, RDS, VPC, certificate, bucket, images or IAM.

| Input | Existing contract |
| --- | --- |
| `expected_aws_account_id`, `aws_region` | Approved commercial account/region; provider `allowed_account_ids` enforces account during future use |
| `name` | Unique resource prefix, default `shopstops-runtime`; use only one `shopstops.internal` namespace per VPC |
| `vpc_id`, `private_subnet_ids`, `public_subnet_ids` | Existing VPC with DNS support/hostnames; at least two subnets of each kind, distinct AZs and disjoint sets; public ALB routing through existing IGW |
| `rds_security_group_id` | Existing private PostgreSQL RDS on port 5432; this root adds API egress only, NEVER modifies RDS or its SG |
| `database_url_secret_arn`, `internal_token_secret_arn` | Two existing same-account/region Secrets Manager **full ARNs**, entire secret values (no JSON selectors); no actual values in Terraform inputs/state/output |
| `secret_kms_key_arns` | Optional exact CMKs encrypting those secrets; default empty for AWS-managed key; Decrypt restricted to Secrets Manager and exact secret encryption contexts |
| `images` | Object with `api`, `gateway`, `admin`, `osrm`: approved existing same-account/region ECR `repository@sha256:<64 lowercase hex>` references; all must support Linux amd64 |
| `certificate_arn`, `domain` | Existing issued ACM cert matching the approved public domain, same account/region; certificate status/coverage and DNS checked externally |
| `oidc_issuer`, `oidc_audience` | Exact public HTTPS discovery issuer and dedicated API JWT audience; HTTPS validation excludes credentials/query/fragment/placeholder issuers |
| `oidc_trusted_claims` | Explicit bool, **false by default**; enables issuer-managed elevated claims only after signed role/actor binding review |
| `osrm_task_role_arn` | Existing [OSRM role root](../osrm-runtime/README.md) output, with its exact 28-key read policy matching this bucket/version; never execution role |
| `osrm_artifact_bucket`, `osrm_dataset_version` | Existing account/region-bound staging bucket and immutable New York release, manifest published last, preparation/runtime digest/profile compatible |
| `osrm_desired_count`, `osrm_ephemeral_storage_gib` | Defaults 1 and 80 GiB; 1–4 replicas, 21–200 GiB disk; review real dataset/load/memory measurements |
| `aws_https_egress_cidrs` | Required explicit approved IPv4 AWS service/endpoint destinations; see network caveats below |
| `enable_waf`, `waf_requests_per_five_minutes` | Default true, 2000 requests per source IP per five minutes; disabling requires security-owner review |

Private subnets must ALREADY have validated routes, DNS and approved NAT and/or
endpoints for ECR API/DKR, S3 image layers/dataset, Logs, Secrets Manager, STS and
public OIDC discovery/JWKS. Endpoint policies and endpoint SG ingress must permit
these tasks. No route, gateway, endpoint, database, secret, certificate, ECR
repository, bucket, publisher permission, IdP or domain record is created here.
Public tasks are never used as a workaround: **all four services assign no public IP**.

Supply `DATABASE_URL` with least-privilege application login and verified TLS
(`sslmode=verify-full`, approved RDS hostname/root CA). The scratch image contains
system CA certificates only; independently check whether the selected RDS chain
is trusted, and bake a reviewed RDS CA bundle into a new immutable API image if
needed. Merely choosing verify-full without the right trust material is not
evidence of connectivity. Internal token must be securely generated 32–4096
printable non-whitespace ASCII characters, shared only by API/gateway; validate
actual value privately and rotate both services together via new task starts.

Authenticate privately using ONE fixed, explicitly selected approved AWS profile
and region. Record only the verified non-secret account/region/profile identifier
in the release record; never tokens/keys/secret values or shell tracing. This change
does not run AWS identity commands or inspect local credentials.

## State, external migration gate and staged operation

Before any future plan, separately approve an existing encrypted/versioned/private
S3 backend, exact KMS key, account/owner checks, Terraform 1.10 native lockfile and
dedicated key such as `shopstops/service-runtime/terraform.tfstate`. Configure the
empty S3 backend with the approved settings; do not use local state for a deployment,
reuse another root's state, grant the image publisher state/IAM access or auto-apply.
Backend bootstrap and apply automation are intentionally absent.

1. Review isolated state/IAM/network plan, secret permissions, images/provenance,
   certificate/domain, capacity, budget and operational owners externally.
2. Verify RDS backups/PITR, deletion/final-snapshot protection and restore rehearsal.
   Run the reviewed [001→007 migration sequence](../../../db/migrations/001_delivery_foundation.sql)
   using an external private one-shot migration job with separate privileged role,
   version/checksum ledger and exclusive lock. Do NOT use the fresh-schema CI SQL
   runner on deployed data. **Finish migrations before starting API services.**
   There is no migration resource, automatic replica startup migration or invented
   Terraform migration dependency. The API health endpoint checks DB connectivity,
   not migration completion. Operators must enforce this release gate.
3. RDS owner separately approves ingress from the output API SG on 5432; this root
   deliberately cannot make the existing DB accessible. Plan separate staged
   authorization (or platform-managed prerequisites) before starting tasks.
4. Start fresh private OSRM tasks and verify actual route JSON from inside the VPC.
   Start API/gateway/admin only after database/identity prerequisites are ready.
5. Manually create approved domain alias/CNAME to `alb_dns_name` (Route 53 aliases
   may use `alb_zone_id`). Verify DNS/certificate/TLS and edge security. Outputs
   describe intended URLs, never successful or ready endpoints.
6. Run read-only smoke probes, authenticated negative/cross-tenant tests and
   staging manual scenarios; obtain explicit release approval separately.

## Resources, identity and runtime contracts

- One ECS cluster with Container Insights; four Fargate Linux amd64 services/task
  definitions, platform **1.4.0** (not mutable `LATEST`), circuit breaker/rollback,
  100% minimum/200% maximum deployment capacity. Desired counts **API 2, gateway 2,
  admin 2, OSRM 1 by default** (7 main tasks); overlap can double compute/disk costs.
  API/gateway/admin tasks use 0.5 vCPU/1 GiB; OSRM 2 vCPU/8 GiB is a starting
  configuration, NOT proven sufficient for New York. No autoscaling is supplied.
- Four execution roles: each can pull only its image's exact ECR repository,
  write its existing 30-day log group streams, and read only its injected secret
  ARNs (API: DB/token; gateway: token; admin/OSRM: none). ECR authorization token
  requires resource `*`; image **digest** selection is the task definition, while
  IAM pulls scope to repository. No GetSecretValue resource wildcard, secret
  creation, logging CreateLogGroup or publisher writes. Three application task
  roles have no AWS permissions. OSRM reuses the prior read-only task role.
- Application containers are non-root, all capabilities dropped, read-only root.
  API/gateway scratch need no writable paths or shell health probe in current code.
  Gateway target `/healthz` forwards API/database readiness; CloudMap API custom
  health is based on ECS process status, NOT an independent database HTTP probe.
  API must not be advertised as having a nonexistent curl healthcheck.
- Admin and OSRM each use a same-digest short-lived root `volume-init`, override
  entrypoint `/bin/sh -ec`, with read-only root and only default CHOWN capability
  retained. It makes mounted paths mode 0700 **before** chown (no FOWNER needed).
  It runs no downloader/AWS/network command. ECS task IAM identity is shared by
  both containers, so OSRM init necessarily has access to the same task role;
  this is not container-scoped IAM. Main depends on init **SUCCESS**.
- Fargate does not support tmpfs: empty task-scoped ephemeral bind volumes mount
  `/tmp` (admin UID 101; OSRM UID 10001). OSRM additionally shares `/data/osrm`,
  owned 10001:10001/mode0700, with init. No host/source path, EFS, root-starting
  downloader or persistent cache is used. Wrapper stays the image entrypoint,
  verifies all artifact hashes and fails closed on leftovers. No container restart
  policy is enabled: replacement tasks receive fresh volumes. Writable volumes
  are not tmpfs/noexec and are not individually size-quota enforced on Fargate.
- Admin `/healthz` is checked by ECS wget and ALB. OSRM Bash TCP probe on 5001
  proves listener liveness ONLY, not route/data correctness. 300-second startup
  grace plus 10 retries/60s is roughly a 15-minute startup budget, not unlimited
  download time. Measure dataset download/load time; task health grace has ECS
  limits and may require a separately reviewed startup/readiness design.

Gateway variables exactly match [the implementation contract](../../../docs/identity-gateway.md):
`SHOPSTOPS_BACKEND_URL=http://api.shopstops.internal:8080` passes private-dialing
validation; `SHOPSTOPS_OIDC_ISSUER`, `SHOPSTOPS_OIDC_AUDIENCE`,
`SHOPSTOPS_OIDC_TRUSTED_CLAIMS`, `SHOPSTOPS_CORS_ORIGINS=https://<domain>`,
`SHOPSTOPS_INTERNAL_API_TOKEN` and `HTTP_ADDR=:8080`. API uses
`OSRM_URL=http://osrm.shopstops.internal:5001`, `DATABASE_URL`, shared token and
`HTTP_ADDR=:8080`. OSRM gets the exact five prior-module environment values and
`HOME=/tmp`. No environment contains AWS credentials.

Trusted claims default off: ordinary verified subjects get customer privileges
only. Turning on the bool requires security-owner review of the fixed namespaced
role/actor claims, merchant UUID ownership, admin provisioning, immutable subject
bindings and issuer-controlled claims that users cannot edit. The current admin
UI is a static SPA and in-memory token consumer, **not a completed login flow**.
Neither ALB nor this wiring fixes that. Driver/checkout/payment/dispatch operations
remain blocked by gateway policy.

## Network and edge boundaries

CloudMap private namespace `shopstops.internal` has API/OSRM A records (10s TTL).
Only gateway SG may enter API:8080; only API SG may enter OSRM:5001. Only ALB SG
may enter gateway/admin:8080. ALB egress goes only to those two SGs. API has DB SG
egress:5432 and OSRM SG egress:5001. Gateway has API SG egress:8080 and internet
HTTPS:443 for discovery/JWKS and task startup through existing approved NAT.
Admin/API/OSRM HTTPS egress uses supplied explicit CIDRs for AWS dependencies.
If API payment credentials are later enabled, Stripe HTTPS destinations also
need separate reviewed egress. S3 gateway endpoint routes are **not** destination
SGs: include approved S3 public CIDRs or deliberately reviewed NAT HTTPS fallback
in the input. Route/endpoint/SG prerequisites and CIDR maintenance remain external.
SGs cannot block Amazon-provided VPC DNS; DNS Firewall/query logging and NACLs,
endpoint policies and egress allowlisting need platform review. No IPv6 rules.

Public IPv4 ALB listens HTTPS using existing ACM and TLS1.2/1.3 policy; port80 only
redirects to HTTPS. HSTS and nosniff set at the HTTPS edge, server banner disabled.
Paths `/v1`, `/v1/*`, and **exactly** `/webhooks/stripe` forward unchanged to gateway;
gateway strips one `/v1` and enforces its exact method/path allowlist. `/v10` and
webhook subtrees do not become API routes. Remaining paths default to admin SPA
(including `/healthz`, which is static admin health; use `/v1/healthz` for DB).
An ALB wildcard path rule is not authorization or a raw-path canonicalization
guarantee: verify encoded/normalized edge paths live; gateway remains fail-closed.

Optional WAF is **on by default**, regional IP rate block at 2000 requests/5min
across all paths. Uses connection source IP, NOT caller-controlled forwarding
headers; sampled requests disabled to avoid capturing Authorization. No WAF body
logging or ALB access log bucket is added. Threshold is approximate, can penalize
shared NATs/Stripe delivery bursts, and is not per-user quota or bot protection.
Review production traffic/Stripe retries, metrics/alarms and privacy-redacted access
logging; disable only with explicit alternative abuse controls. ALB deletion
protection needs deliberate disable before approved teardown; no auto-destroy.

## Read-only smoke harness and payment limits

[Smoke script](../../../scripts/test-live-services.sh) accepts environment-only
`PUBLIC_ORIGIN=https://<approved-domain>` (bare origin) and `STORE_LOCATION_ID`
(literal UUID). It performs **8 unauthenticated GETs**: gateway DB health, catalog
401, spoofed actor/admin/internal headers 401, fixed invalid bearer 401, merchant
catalog 401, unknown operation 404, admin health and document 200. Only a constant
obviously invalid synthetic bearer is sent; **never supply a real JWT in CLI args,
environment or chat to this harness**. No response bodies, credentials, submitted
URL values or curl debug/error details are printed. Curl config/proxies/redirects
and TLS bypass are disabled. Public HTTPS is mandatory; DNS ownership/resolution
is still an external trust prerequisite, not an SSRF-proof resolver assertion.

For the optional **9th GET**, run from approved private VPC networking with
`OSRM_PRIVATE_URL=http://osrm.shopstops.internal:5001` and explicit
`OSRM_COORDINATES=<lon1>,<lat1>;<lon2>,<lat2>` (quote in the operator shell).
Only this exact native CloudMap origin permits private HTTP; no arbitrary hosts,
public IP, alternate path or credentials are accepted. Longitude/latitude bounds
are checked and jq requires `code=Ok`, a route and legs. Choose real routable New
York coordinates. No OSRM exposure, tunnel, AWS operation or mutation is performed.
Offline [fake-curl tests](../../../tests/live-services/test-live-services.sh)
are not a live smoke result; script success is NOT release approval.

**Stripe credentials are intentionally not wired by this root.** The backend
starts without payment support; a reachable webhook listener does not mean a
configured/working payment flow. To enable later, review separate exact runtime
secret ARNs/IAM for the paired `STRIPE_SECRET_KEY` and `STRIPE_WEBHOOK_SECRET`,
optional merchant price ID, test-mode-only staging account and HTTPS egress.
Record payment/manual replay evidence separately; this harness remains read-only
and never invokes payment/refund/order mutations or Stripe API/replay commands.
Authorized staging operators must test signature/timestamp rejection, duplicate
and reordered signed webhook delivery, Stripe retries, idempotency, refunds,
transfer reversals/disputes and reconciliation with Stripe **test-mode** fixtures.
Checkout/payment and driver endpoints remain disabled at the public gateway;
do not weaken policy or claim payments complete based on health probes.

## Validation and limitations

CI adds this root alongside the two existing isolated roots: fmt, backend-disabled
init with lockfile, validate and mocked Terraform tests. No AWS plan/apply,
credentials/OIDC, remote state, resource reads or automatic deployment occur.
Mocked plans test private replicas, exact environment/secret boundaries, volume
dependency, default/disabled WAF and invalid inputs. No live SG/IAM/database/TLS,
container init permission execution, image/platform build, real dataset route,
payment replay, alarms, HA/failover, capacity or domain DNS validation is claimed.
Review logs for possible application error details (including DB connection errors),
implement redaction/privacy policy, metrics/SLOs/runbooks and restore/rollback
evidence before release. Log retention is 30 days; Container Insights, logs, ALB,
WAF, Fargate/overlap storage and existing NAT/endpoints carry usage costs.

See [production evidence gates](../../../docs/production-readiness.md). These
remain unresolved; this root is isolated deployment wiring, not production readiness.

### Local validation record (2026-11-14)

- Terraform 1.10.5 with signed AWS provider 6.73.0: all **3** isolated roots
  passed recursive fmt checks and backend-disabled provider validation.
- Service-runtime mock-provider tests: **10 passed, 0 failed**. No live plan.
- Go suite: **104 passing test/subtest cases in 3 tested packages**, no failures
  or skipped tests; `go vet` passed.
- Offline shell suites: **220 passed** (staging 51, OSRM 50, readiness 67,
  SQL-runner mocks 15, live-service mocks 37); **19** Bash files syntax checked.
- Disposable Terraform data/provider caches were removed; the reviewed lockfile
  remains. No AWS API/resource change, real smoke call, container build, database
  integration, commit or push occurred. CI itself has not run here.
