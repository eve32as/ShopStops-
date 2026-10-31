# ShopStops

ShopStops is a multi-store delivery platform. The initial backend foundation is a
PostgreSQL/PostGIS schema and transactional dispatch routine for grouping nearby
ready orders into a bundle and persisting its pickup/drop-off queue.

## Database

Apply `db/migrations/001_delivery_foundation.sql`,
`db/migrations/002_unified_marketplace.sql`,
`db/migrations/003_stripe_checkout_payments.sql`,
`db/migrations/004_billing_refunds_disputes.sql`,
`db/migrations/005_driver_stop_progress.sql`,
`db/migrations/006_catalog_orders.sql`,
`db/migrations/007_merchant_catalog_management.sql`,
`db/migrations/008_driver_fraud_monitor.sql`,
`db/migrations/010_driver_rerouting_tracking.sql`,
`db/migrations/011_weekly_payout_notifications.sql`,
`db/migrations/012_driver_route_assignment.sql`,
`db/migrations/013_driver_eligibility.sql`, and
`db/migrations/014_checkr_driver_screening.sql` on PostgreSQL 14+ with PostGIS 3.x
installed. The migrations create dispatch, unified-basket, catalog, and order
models; spatial queries; transaction-safe order/merchant limits; immutable
multi-stop routes; payment/tax ledger records; outbox events; and weekly courier
earnings statement and delivery-message audit tables.

See [`docs/architecture.md`](docs/architecture.md) for MVP assumptions, service
boundaries, workflows, and operational guidance. The initial HTTP interface is
specified in [`api/openapi.yaml`](api/openapi.yaml).

Database integration fixtures are in `db/tests/`; apply all migrations before
running the matching SQL test files.

## Run the API

After applying all migrations, set `DATABASE_URL` and a private internal API
token (at least 32 characters), then run:

```sh
export DATABASE_URL='postgres://<user>:<password>@localhost:5432/shopstops?sslmode=disable'
export SHOPSTOPS_INTERNAL_API_TOKEN="$(openssl rand -hex 32)"
export SHOPSTOPS_DELIVERY_FEE_CENTS=500
export SHOPSTOPS_SERVICE_FEE_CENTS=100
# Set to enable OSRM-backed route creation; individual requests time out at 450ms.
export SHOPSTOPS_OSRM_BASE_URL='http://localhost:5000'
# Set both through a server-side secret manager to enable Stripe payments.
export STRIPE_SECRET_KEY
export STRIPE_WEBHOOK_SECRET
# Configure a Stripe Price for USD 199/month to enable merchant subscriptions.
export STRIPE_MERCHANT_MONTHLY_PRICE_ID
# Optional Checkr screening; the default base URL is the Checkr staging API.
# Production must explicitly configure https://api.checkr.com after onboarding.
export CHECKR_API_KEY
export CHECKR_PACKAGE_ID
export CHECKR_BASE_URL
go run ./cmd/shopstops-api
```

The service listens on `:8080` by default; set `HTTP_ADDR` to override it. It
implements health checks, nearby subscribed-merchant discovery, atomic
dispatch, checkout creation/payment, OSRM-backed paid-checkout route creation,
driver-triggered OSRM rerouting, customer-owned shopper tracking, Stripe webhooks, driver route reads,
catalog discovery, order creation, ordered stop arrival/completion, and GPS
ingestion with review-only fraud alerts for extreme GPS speeds and sustained
route expansion. Admins can list and resolve these alerts in the control room.
Dispatchers and admins can assign routed deliveries to drivers; the assignment
is transactionally guarded against duplicate active routes and audited to the
outbox. Admins must provision and verify driver identities in the directory;
driver screening must be completed and reviewed before explicit admin activation.
Only active profiles with a current approved Checkr screening are eligible, and
profile/screening status changes are audited.
The mobile shopper can geocode a typed delivery address on-device or use current
location; only coordinates are sent to the API.
Merchant catalog endpoints let authenticated merchants manage item names,
descriptions, prices, delivery volumes, and customer-facing availability.
The admin dashboard also audits outbound notification attempts and can retry
failed statement emails. Recipient addresses are managed by admins and are not
returned by the notification log endpoint.

## Weekly courier earnings statements

Run the separate mailer worker with the same database migrations applied. It
creates idempotent statements for completed unified routes in the previous
complete UTC week, using the configured per-mile rate and the finance package's
standard route base pay. On each run it catches up all earlier unprocessed weeks,
up to 52 distinct weeks per run; remaining backlog is picked up on subsequent
hourly runs. It emails statements over SMTP with mandatory STARTTLS;
this worker reports earnings only and does not initiate or confirm funds
transfers. Delivery attempts are retained for admin review, retried with
backoff up to eight times, and can then be manually requeued from the dashboard.

```sh
export DATABASE_URL='postgres://<user>:<password>@localhost:5432/shopstops?sslmode=disable'
export SHOPSTOPS_DRIVER_MILEAGE_RATE_CENTS_PER_MILE=45
export SHOPSTOPS_SMTP_ADDRESS='smtp.example.com:587'
export SHOPSTOPS_SMTP_HOST='smtp.example.com'
export SHOPSTOPS_PAYOUT_FROM='payouts@example.com'
# Optional SMTP AUTH credentials; configure both or neither via a secret manager.
export SHOPSTOPS_SMTP_USERNAME
export SHOPSTOPS_SMTP_PASSWORD
go run ./cmd/shopstops-payout-worker
```

Configure a driver's statement recipient in the admin dashboard before the
worker runs. Statement creation is unique per driver and UTC week; separate
worker replicas safely claim messages using database row locks and expiring
leases.
## OIDC identity gateway

Run the provider-neutral gateway as the only public path to the API. It validates
OIDC-issued JWT bearer tokens against the configured issuer's discovery metadata
and signing keys, checks the exact issuer, audience, expiry and RS256/ES256
signature, maps the configured role claim to ShopStops roles, and uses the
verified token subject as the actor. Role claims may be nested paths such as
`realm_access.roles` and may be a string or an array of strings. Unknown roles
are ignored; tokens without an allowed role are denied. A token mapping to more
than one ShopStops role must select one with `X-ShopStops-Role-Selection`; the
selection is accepted only when present in the verified token's mapped roles.
Client-supplied actor, role, authorization, cookie, and forwarding headers are
never trusted or forwarded. The gateway replaces actor/role headers and injects
the internal API bearer token.

Choose the identity provider and configure its exact issuer, API audience, role
claim, and provider-role mappings for your deployment. No identity provider or
role vocabulary is selected by this repository. The issuer must use HTTPS;
HTTP is accepted only for loopback development. Configure the API upstream as a
private service URL, and restrict network access so clients cannot reach the API
directly. The gateway listens on `:8081` by default; set
`SHOPSTOPS_GATEWAY_HTTP_ADDR` to override it. Configure both TLS certificate and
key file variables for gateway-managed TLS, or terminate TLS at a trusted
ingress. Do not expose the gateway over plaintext HTTP to untrusted networks.
For gateway-managed TLS, set both `SHOPSTOPS_GATEWAY_TLS_CERT_FILE` and
`SHOPSTOPS_GATEWAY_TLS_KEY_FILE`.

```sh
export SHOPSTOPS_OIDC_ISSUER='https://identity.example/tenant'
export SHOPSTOPS_OIDC_AUDIENCE='shopstops-api'
export SHOPSTOPS_OIDC_ROLE_CLAIM='realm_access.roles'
export SHOPSTOPS_OIDC_ROLE_MAPPINGS='{"shopstops-admin":"admin","shopstops-driver":"driver","shopstops-customer":"customer","shopstops-merchant":"merchant","shopstops-dispatch":"dispatch"}'
export SHOPSTOPS_API_UPSTREAM_URL='http://shopstops-api.internal:8080'
# Set the same secret used by the API through your server-side secret manager.
export SHOPSTOPS_INTERNAL_API_TOKEN
go run ./cmd/shopstops-gateway
```

`GET /healthz` and `POST /webhooks/stripe` are the only unauthenticated
pass-through routes. Stripe webhook signatures are still verified by the API.
The API and gateway share the internal bearer token; store it in a secret manager
and do not distribute it to client applications. The gateway authenticates
tokens but does not implement user sign-in or choose/configure an identity
provider for you.

## Checkr driver screening

The admin driver directory can create Checkr candidates and hosted invitations,
refresh invitation/report status, and record a separate admin review. The
candidate's name and email are sent to Checkr but not stored by ShopStops. The
configured work city/region/country is retained with the screening request so
invitation retries use the original location. Raw reports are not stored. The API
stores provider IDs, package/status/result codes, request/review actors, and
timestamps. Use Checkr's
dashboard to inspect the full report before recording the review. Checkr package
selection is configured by the operator and is not hard-coded by the application.

Set both `CHECKR_API_KEY` and `CHECKR_PACKAGE_ID` to enable screening. If unset,
the screening endpoints fail closed with `503`; if only one is set, API startup
fails. `CHECKR_BASE_URL` defaults to Checkr staging (`https://api.checkr-staging.com`);
production must explicitly configure `https://api.checkr.com` after Checkr
onboarding and package approval. Keep the API key in a server-side secret manager.

Screening requests require an `Idempotency-Key`. Ambiguous network/provider
outcomes are marked for manual reconciliation and are not automatically retried,
to avoid duplicate candidates or invitations. An administrator must inspect
Checkr, cancel or otherwise resolve any existing invitation/check, then explicitly
confirm no active check remains before another request can be created. Uncertain
requests must be inactive for at least two minutes before they can be reconciled.
Completed provider results do not
activate a driver: an administrator must review and approve the latest completed
screening, then separately set the driver's profile to active. Starting a new
screening moves an active profile back to pending, and the database blocks new
route assignment until the new result is reviewed and the administrator
reactivates the profile.

Before enabling production checks, complete Checkr onboarding, select the
appropriate package and work-location requirements, determine applicable
jurisdictions and screening policy, and implement the required notices,
authorizations, dispute handling, and adverse-action process with qualified
compliance counsel. The provider integration does not make legal decisions,
adjudicate results, or automate adverse action. Checkr must be configured for its
hosted candidate consent flow; do not order checks without required candidate
disclosures and authorization.

Delivery/service fee configuration is in integer cents and defaults to zero if
unset. The checkout endpoint builds the merchant item lines from each order's
`item_subtotal_cents` snapshot and applies the D.C. surcharge from the configured
jurisdiction polygon.

The standalone `finance` Go package implements the supplied driver base pay,
mileage recovery, processing fee, merchant surcharge, and platform-share
equations. Callers must provide a non-negative zone rate in cents per mile and
the mileage to price. It is not yet connected to checkout creation: existing
checkout invoices continue to use the delivery/service fees above. The
`NetPlatformMargin` value follows the supplied equation and is not a complete
accounting measure of realized margin because it does not deduct every listed
cost.

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
shopper delivery tracking on native maps, assigned-stop maps and foreground GPS
sharing for drivers, and merchant subscription setup. The admin client provides
the existing full-refund operation. Both
expect the operator-configured identity gateway described above. Neither client
may receive the backend Stripe secret or internal API token.

Run the mobile client from `apps/mobile` with `npm ci`, then use
`npx expo run:ios` or `npx expo run:android` to create a development build.
Run the admin client from `apps/admin` with `npm ci` and `npm run dev`. Configure
`EXPO_PUBLIC_STRIPE_PUBLISHABLE_KEY` for mobile PaymentSheet flows. Checkout
requires existing order IDs. The native map uses the platform's default map
provider without binding the map UI to a vendor. iOS uses Apple Maps; Android
requires a restricted Google Maps API key supplied as `GOOGLE_MAPS_API_KEY` when
building the native app. Do not commit the key. Driver stop maps show ordered
stop context and open an external directions link for the next stop; they do not
provide turn-by-turn navigation.
