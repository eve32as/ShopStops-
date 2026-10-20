# ShopStops

ShopStops is a multi-store delivery platform. The initial backend foundation is a
PostgreSQL/PostGIS schema and transactional dispatch routine for grouping nearby
ready orders into a bundle and persisting its pickup/drop-off queue.

## Database

Apply `db/migrations/001_delivery_foundation.sql`,
`db/migrations/002_unified_marketplace.sql`,
`db/migrations/003_stripe_checkout_payments.sql`,
`db/migrations/004_billing_refunds_disputes.sql`, and
`db/migrations/005_driver_stop_progress.sql`, and
`db/migrations/006_catalog_orders.sql`, and
`db/migrations/007_merchant_catalog_management.sql`, and
`db/migrations/008_merchant_order_fulfillment.sql` on PostgreSQL 14+ with PostGIS 3.x
installed. The migrations create dispatch, unified-basket, catalog, and order
models; spatial queries; transaction-safe order/merchant limits; immutable
multi-stop routes; payment/tax ledger records; and outbox events.

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
# Set both through a server-side secret manager to enable Stripe payments.
export STRIPE_SECRET_KEY
export STRIPE_WEBHOOK_SECRET
# Configure a Stripe Price for USD 199/month to enable merchant subscriptions.
export STRIPE_MERCHANT_MONTHLY_PRICE_ID
go run ./cmd/shopstops-api
```

The service listens on `:8080` by default; set `HTTP_ADDR` to override it. It
implements health checks, nearby subscribed-merchant discovery, atomic
dispatch, checkout creation/payment, Stripe webhooks, driver route reads,
catalog discovery, order creation, ordered stop arrival/completion, and GPS
ingestion. Merchant catalog endpoints let authenticated merchants manage item
names, descriptions, prices, delivery volumes, and customer-facing availability.
Protected endpoints use a shared internal bearer token and must be placed
behind a gateway that authenticates users, enforces roles, strips client-supplied
`X-ShopStops-Actor` headers, and injects the authenticated actor
identity. Do not distribute this internal token to client applications.
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
assigned-route viewing and opt-in foreground/background GPS sharing, and
merchant order fulfillment and subscription setup. Background tracking stores
the gateway session in OS secure storage only while tracking is active and
clears it when tracking stops. The admin client provides the existing
full-refund operation. Both
expect an identity-aware public gateway to validate user session tokens and
inject backend actor/role headers. Neither client may receive the backend
Stripe secret or internal API token.

Run the mobile client from `apps/mobile` with `npm ci`, then use
`npx expo run:ios` or `npx expo run:android` to create a development build.
Run the admin client from `apps/admin` with `npm ci` and `npm run dev`. Configure
`EXPO_PUBLIC_STRIPE_PUBLISHABLE_KEY` for mobile PaymentSheet flows. The shopper
client can browse visible store catalogs and create orders before checkout; it
still requires an identity-aware gateway and a valid customer delivery
location. Full merchant operations, admin transaction/dispute search, and
real-time updates also remain follow-on work.
