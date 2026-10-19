# ShopStops

ShopStops is a multi-store delivery platform. The initial backend foundation is a
PostgreSQL/PostGIS schema and transactional dispatch routine for grouping nearby
ready orders into a bundle and persisting its pickup/drop-off queue.

## Database

Apply `db/migrations/001_delivery_foundation.sql`,
`db/migrations/002_unified_marketplace.sql`, and
`db/migrations/003_stripe_checkout_payments.sql` to PostgreSQL 14+ with PostGIS 3.x
installed. The migrations create the dispatch and unified-basket relational
models, spatial queries, transaction-safe order/merchant limits, immutable
multi-stop routes, payment/tax ledger records, and outbox events.

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
# Set both to enable Stripe payments; keep them in a server-side secret manager.
export STRIPE_SECRET_KEY='sk_live_...'
export STRIPE_WEBHOOK_SECRET='whsec_...'
go run ./cmd/shopstops-api
```

The service listens on `:8080` by default; set `HTTP_ADDR` to override it. It
currently implements health checks, nearby subscribed-merchant discovery,
atomic dispatch, driver route reads, and GPS ingestion. Protected endpoints use
a shared internal bearer token and must be placed behind a gateway that
authenticates users, enforces roles, strips
client-supplied `X-ShopStops-Actor` headers, and injects the authenticated actor
identity. Do not distribute this internal token to client applications.
Delivery/service fee configuration is in integer cents and defaults to zero if
unset. The checkout endpoint builds the merchant item lines from each order's
`item_subtotal_cents` snapshot and applies the D.C. surcharge from the configured
jurisdiction polygon.

The payment endpoint creates one idempotent PaymentIntent from the persisted
invoice. Configure Stripe to send `payment_intent.succeeded`,
`payment_intent.processing`, and `payment_intent.payment_failed` to
`/webhooks/stripe`. Verified, deduplicated events update the payment/tax ledger
and create separate, idempotent Connect transfers to merchant accounts. Retries
resume transfers not recorded after Stripe successfully created them.

Stripe processing fees are allocated across merchant item subtotals in
proportion to each merchant's items; leftover cents go by descending fractional
remainder, then merchant ID. Allocation is capped at total item value and the
platform bears any excess. Delivery/service fees and the D.C. surcharge remain
in the platform ledger. Merchants with positive item subtotals must have
connected accounts before payment initialization. Payment secrets are never
returned to client applications; only the PaymentIntent client secret is
returned to the authenticated customer.

Native iOS and Android apps, the admin web app, merchant subscription billing,
routing, and WebSocket integrations remain follow-on work.
