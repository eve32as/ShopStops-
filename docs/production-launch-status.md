# Production launch status — 2026-11-15

## Requested launch

- Full customer launch (not a restricted pilot).
- AWS account: 310746022857; region: us-east-1.
- Domain: shopstops.com; operator reports controlling DNS.
- Infrastructure planning target: $600/month, pay-as-you-go. This is not an
  enforced cap or approval of an unreviewed resource plan.
- Operator subsequently chose to reuse the existing account and
  `my_app_admin` profile rather than create a separate account.
- No production resources or paid transactions were created by this assessment.

## Verified observations

- AWS profile `my_app_admin` authenticates as IAM user `Jess` in the intended account.
- RDS DescribeDBInstances, ECS ListClusters, ECR DescribeRepositories, and
  Route53 ListHostedZonesByName were denied. Existing resources are therefore
  unknown, not proven absent. Use an authorized deployment role; do not grant
  broad administrator access merely to bypass these checks.
- ACM ListCertificates returned no certificates in us-east-1. Domain control
  still needs DNS validation and an issued certificate before HTTPS launch.
- Organizations DescribeOrganization was denied. IAM role discovery was
  permitted. The existing role named `admin` trusts only the API Gateway
  service, not the operator; STS AssumeRole was denied. Do not change this
  service role's trust or permissions to repurpose it for deployment.
- Recent GitHub runs inspected were successful **Copilot cloud agent** runs.
  They do not establish success of the release CI or disposable PostGIS tests.
- Local Docker engine was unavailable and PostgreSQL tools were absent.
  The four PostgreSQL integration tests skipped without disposable DSNs.
- All 23 offline SQL-runner safeguard tests passed; these use mock psql and
  do not establish SQL correctness against PostgreSQL.

## Release gates — do not bypass

1. Inspect account resources using an authorized role and prepare a costed,
   reviewed Terraform plan. Include persistent charges for database, routing,
   load balancer, networking, logs, backups, secrets, and storage. Configure
   budget alerts; alerts do not stop billing.
2. Publish the reviewed source and pass the actual release CI, including all
   eleven migrations, SQL fixtures, concurrency checks, and image builds on a
   disposable PostGIS database. Do not use the disposable runner on production.
3. Configure a real OIDC issuer/audience and verified customer/order ownership.
   Checkout, payment, driver, and dispatch routes currently remain blocked at
   the gateway pending identity binding; do not expose the backend directly.
4. Rehearse production migrations with backup/restore evidence and use a
   separate, serialized migration process and least-privilege application role.
5. Publish and verify the OSRM dataset and immutable container digests; test a
   real route, startup memory, private service networking, and readiness.
6. Issue TLS for the approved hostname, configure DNS, and verify HTTPS,
   authentication, monitoring, rollback, and operational ownership.
7. Complete Stripe test-mode checkout/webhook/refund/reconciliation testing
   before live credentials. Priority and extra-pickup features are test-only;
   do not remove those guards merely to deploy.
8. Shopper completion records are pending review, not transfers. Teen worker
   eligibility, compensation compliance, and payroll must be separately
   verified before enabling work or payouts.

## Immediate blocker

An authorized AWS role/profile is needed for resource discovery and deployment
planning. Supply a profile or role name, not access keys or passwords. Paid
provisioning must follow review of the actual plan and costs; a full launch
cannot be claimed while the release gates above remain open.
