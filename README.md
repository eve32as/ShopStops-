# ShopStops

ShopStops is a multi-store delivery platform. The initial backend foundation is a
PostgreSQL/PostGIS schema and transactional dispatch routine for grouping nearby
ready orders into a bundle and persisting its pickup/drop-off queue.

## Database

Apply `db/migrations/001_delivery_foundation.sql`,
`db/migrations/002_unified_marketplace.sql`,
`db/migrations/003_stripe_checkout_payments.sql`,
`db/migrations/004_billing_refunds_disputes.sql`, and
`db/migrations/005_driver_stop_progress.sql`,
`db/migrations/006_catalog_orders.sql`,
`db/migrations/007_merchant_catalog_management.sql`,
`db/migrations/008_merchant_order_fulfillment.sql`, and
`db/migrations/009_delivery_zone_dispatch_defaults.sql` on PostgreSQL 14+ with
PostGIS 3.x installed. The migrations create dispatch, unified-basket, catalog,
and order models; spatial queries; transaction-safe order/merchant limits;
immutable multi-stop routes; payment/tax ledger records; and outbox events.

See [`docs/architecture.md`](docs/architecture.md) for MVP assumptions, service
boundaries, workflows, and operational guidance. The initial HTTP interface is
specified in [`api/openapi.yaml`](api/openapi.yaml).

Database integration fixtures are in `db/tests/`; apply all migrations before
running the matching SQL test files.

## Go ORM models

`internal/models` provides GORM mappings for all 30 persisted tables and the
`merchant_storefront_visibility` view, including custom PostGIS, JSONB, and
exact-decimal field types. Open an existing PostgreSQL database with
`models.OpenPostgres(DATABASE_URL)`. The numbered SQL migrations remain the
schema authority: they define constraints, indexes, triggers, views, and
stored procedures that GORM cannot safely recreate. Apply migrations before
using the models, and do not call `AutoMigrate`. The existing HTTP repository
continues to use `database/sql`; this is a separate ORM layer for code that
needs model-based queries. User identities are issued by the configured OIDC
provider and are not persisted as local account rows.

## Run the API

After applying all migrations, set `DATABASE_URL` and a private internal API
token (at least 32 characters), then run:

```sh
export DATABASE_URL='postgres://<user>:<password>@localhost:5432/shopstops?sslmode=disable'
export SHOPSTOPS_INTERNAL_API_TOKEN="$(openssl rand -hex 32)"
export SHOPSTOPS_DELIVERY_FEE_CENTS=500
export SHOPSTOPS_SERVICE_FEE_CENTS=100
# Set both through a server-side secret manager to enable Stripe payments.
export STRIPE_SECRET_KEY
export STRIPE_WEBHOOK_SECRET
# Configure a Stripe Price for USD 199/month to enable merchant subscriptions.
export STRIPE_MERCHANT_MONTHLY_PRICE_ID
go run ./cmd/shopstops-api
```

The API listens on `:8080` by default; set `HTTP_ADDR` to override it.
`GET /healthz` is a database-independent liveness probe; `GET /readyz` checks
database reachability with a one-second timeout and is suitable for deployment
readiness checks. The gateway permits both probes without a user token. Neither
probe replaces production monitoring.

The API
implements health checks, nearby subscribed-merchant discovery, atomic
dispatch, checkout creation/payment, Stripe webhooks, driver route reads,
catalog discovery, order creation, ordered stop arrival/completion, and GPS
ingestion. Merchant catalog endpoints let authenticated merchants manage item
names, descriptions, prices, delivery volumes, and customer-facing availability.
Protected endpoints use a shared internal bearer token and must not be exposed
directly to clients. Run the included OIDC identity gateway as a separate
process, with both services receiving the same private internal token:

```sh
export SHOPSTOPS_OIDC_ISSUER='https://identity.example.com/'
export SHOPSTOPS_OIDC_AUDIENCE='shopstops-api'
export SHOPSTOPS_OIDC_ROLE_CLAIM='roles'
export SHOPSTOPS_OIDC_ACTOR_CLAIM='sub'
export SHOPSTOPS_API_URL='http://127.0.0.1:8080'
# Comma-separated exact HTTPS origins for browser clients; omit for native-only.
export SHOPSTOPS_GATEWAY_ALLOWED_ORIGINS='https://admin.shopstops.example'
export SHOPSTOPS_INTERNAL_API_TOKEN='<same secret used by the API>'
HTTP_ADDR=':8081' go run ./cmd/shopstops-gateway
```

The gateway verifies issuer, audience, signature, and expiry using OIDC discovery
and the issuer's JWKS. Tokens must include a stable `sub` or configured actor
claim, and the configured role claim must be a role string or string array
containing `customer`, `merchant`, `driver`, `dispatch`, or `admin`. It maps
route access to those roles, removes client-supplied actor, role, forwarding,
cookie, and authorization headers, and injects the verified actor/role plus the
internal API bearer token. `/v1/healthz`, `/v1/readyz`, and the
Stripe-signature-verified `/v1/webhooks/stripe` route remain public. Place the
gateway behind TLS
termination and firewall the API so only the gateway and trusted operations
systems can reach it. The gateway validates externally issued JWTs; it does not
provide a sign-in UI or issue tokens. Never distribute the internal token.

## Local staging stack

The AWS Terraform deployment foundation and its prerequisites, staged bootstrap,
and known release blockers are documented in
[`deploy/aws/README.md`](deploy/aws/README.md). It does not provision or apply
cloud resources without an authorized operator.

The private database bootstrap runner is `scripts/db-migrate.sh`; run it only
from an approved network path with privileged migration credentials delivered by
the secret manager. It records migration checksums, uses a separate no-login
migration owner, and grants the API a non-owner database role. The CI harness
`scripts/test-db-bootstrap.sh` verifies fresh setup, repeat execution,
permissions, checksum drift rejection, and a data-preserving upgrade.

The Compose stack builds the API and gateway, starts PostgreSQL/PostGIS, applies
the numbered SQL migrations when creating a **new** database volume, and keeps
the API and database off host-published ports. Only the gateway is published,
bound to `127.0.0.1:8081`; put a TLS-terminating reverse proxy on the same host
in front of it before allowing remote clients.

```sh
cp .env.example .env
chmod 600 .env
openssl rand -hex 32  # use the output for DB_PASSWORD
openssl rand -hex 32  # use a different output for SHOPSTOPS_INTERNAL_API_TOKEN
# Edit .env with both generated values and your HTTPS OIDC issuer/audience.
docker compose up --build -d
docker compose ps
```

The example issuer is a placeholder. The gateway must be able to reach the real
OIDC discovery/JWKS endpoints at startup. Keep `.env` private; Compose passes
these settings as container environment variables, so use the deployment
platform's secret manager for a shared or hosted staging environment. Stripe
settings are optional, but the secret key and webhook secret must be set
together. The database volume persists across restarts. Initialization scripts
run only for a fresh volume; upgrades need an explicitly reviewed migration
procedure and must not rely on re-running PostgreSQL's one-time init scripts.

Run the smoke checks with an externally issued admin access token:

```sh
SHOPSTOPS_SMOKE_BASE_URL='https://staging.example.com' \
SHOPSTOPS_SMOKE_ADMIN_TOKEN='<admin OIDC access token>' \
bash scripts/staging-smoke.sh
```

Optionally set `SHOPSTOPS_SMOKE_CUSTOMER_TOKEN` to check role denial on the
admin endpoint. To exercise the customer-owned event endpoint too, also set
`SHOPSTOPS_SMOKE_ORDER_ID` to an order owned by that customer and optionally
`SHOPSTOPS_SMOKE_LAST_EVENT_ID` to its saved cursor. For a local-only smoke test,
use `http://127.0.0.1:8081` as the base URL. The script does not create data or
perform writes. Inspect startup with `docker compose logs -f db api gateway`.

Delivery/service fee configuration is in integer cents and defaults to zero if
unset. The checkout endpoint builds the merchant item lines from each order's
`item_subtotal_cents` snapshot and applies the D.C. surcharge from the configured
jurisdiction polygon.

The payment endpoint creates one idempotent PaymentIntent from the persisted
invoice. Configure Stripe to send `payment_intent.succeeded`,
`payment_intent.processing`, `payment_intent.payment_failed`,
`customer.subscription.created`, `customer.subscription.updated`,
`customer.subscription.deleted`, `invoice.payment_succeeded`,
`invoice.payment_failed`, `setup_intent.succeeded`, `refund.created`,
`refund.updated`, `charge.dispute.created`, `charge.dispute.updated`, and
`charge.dispute.closed` to `/webhooks/stripe`. Verified, deduplicated events
update the payment/tax/subscription ledgers, create separate Connect transfers,
and handle merchant payout reversals after refunds and disputes.

Merchants use `POST /merchant/subscription/setup-intent` to attach a reusable
payment method, then `POST /merchant/subscription` to start the fixed Stripe
Price. Stripe validates the configured Price as USD 199 billed monthly and
anchors collection to the next first day of the month. Subscription visibility
is not activated until a successful invoice webhook. After a failed invoice,
the storefront remains visible for at most 48 hours, as defined by
`merchant_storefront_visibility`.

Admins can request a full remaining refund with
`POST /admin/checkouts/{checkoutId}/refund`. Refund and dispute webhooks record
their status and create separately idempotent reversals for transfers already
paid to merchants. Dispute amounts are allocated proportionally across merchant
transfers and capped at the total merchant payouts. Review disputes and refund outcomes operationally; a won
dispute does not automatically re-transfer reversed merchant proceeds, and tax
remittance/reporting remains a separate workflow.

The admin control room also lists delivery zones and toggles whether each zone
accepts new orders and dispatch work through `GET /admin/delivery-zones` and
`PATCH /admin/delivery-zones/{zoneId}`. The `dispatch/bundles` form accepts the
per-run vehicle capacity and reuses its idempotency key if a request needs
retrying. Admins can persist each zone's maximum order count and pickup/drop-off
radii through `PATCH /admin/delivery-zones/{zoneId}/dispatch-limits`; dispatch
uses those saved defaults when per-request limits are omitted. Changes record the
admin subject and emit an outbox event. Zone boundaries remain database
managed; disabling a zone does not cancel existing orders or bundles. These
controls require an admin OIDC role through the trusted gateway.
Existing zones receive the former dispatch defaults (3 orders and 2,000 meters)
when migration 009 is applied.
The same control room's `GET /admin/operations` snapshot reports each zone's
ready-order count and oldest-ready age, active bundles, open stops, and the
pending outbox event count and oldest unpublished event age. When a zone has
ready orders, admins can use the
oldest order directly as a dispatch seed. The snapshot returns only its order
ID, not customer details. Refresh it alongside the zone settings during
operations; it is a point-in-time summary, not a streaming alert system.

Retries resume transfers/reversals not recorded after Stripe accepted them. Pin
the webhook endpoint's Stripe API version to the version supported by the
installed Stripe Go SDK. If an external operation remains unresolved for 23
hours, the API stops resubmitting it rather than risk a duplicate after Stripe's
idempotency-key retention window; reconcile it with Stripe before retrying.

Stripe processing fees are allocated across merchant item subtotals in
proportion to each merchant's items; leftover cents go by descending fractional
remainder, then merchant ID. Allocation is capped at total item value and the
platform bears any excess. Delivery/service fees and the D.C. surcharge remain
in the platform ledger. Merchants with positive item subtotals must have
connected accounts before payment initialization. Payment secrets are never
returned to client applications; only the PaymentIntent client secret is
returned to the authenticated customer.

Starter clients are in `apps/mobile` (Expo iOS/Android) and `apps/admin` (Vite
web). The mobile client provides nearby-store discovery, checkout/payment,
cursor-resumed shopper order progress, assigned-route viewing and opt-in foreground/background GPS sharing, and
merchant order fulfillment and subscription setup. Background tracking stores
the gateway session in OS secure storage only while tracking is active and
clears it when tracking stops. The admin client provides the existing
full-refund operation. Both use the identity gateway to validate externally
issued user tokens and inject backend actor/role headers. Neither client may receive the backend
Stripe secret or internal API token.

Run the mobile client from `apps/mobile` with `npm ci`, then use
`npx expo run:ios` or `npx expo run:android` to create a development build.
Run the admin client from `apps/admin` with `npm ci` and `npm run dev`. Configure
`EXPO_PUBLIC_STRIPE_PUBLISHABLE_KEY` for mobile PaymentSheet flows. The shopper
client can browse visible store catalogs and create orders before checkout; it
requires `EXPO_PUBLIC_OIDC_ISSUER` and `EXPO_PUBLIC_OIDC_CLIENT_ID` for native
authorization-code-with-PKCE sign-in. Register the `shopstops://auth` redirect URI
for the iOS/Android public client and configure the provider to issue access tokens
with the audience and role/actor claims expected by the gateway. The app keeps
access tokens in OS secure storage until their reported expiry; it does not store
a client secret or refresh token. The shopper also requires a valid customer
delivery location. Shopper progress resumes from a device-secure per-order
cursor and stops polling on completion, screen blur, app backgrounding, or
account changes. Push/WebSocket delivery remain follow-on work.
