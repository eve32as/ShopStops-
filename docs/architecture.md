# ShopStops platform foundation

## MVP scope and defaults

The initial workflows are:

- **Customer:** browse stores, place an order, select a delivery window, and
  follow order and driver progress.
- **Merchant:** manage a store catalog, accept an order, prepare it, and mark it
  ready for pickup.
- **Driver:** receive a bundle, follow its ordered stops, and report arrival,
  pickup, and delivery progress.
- **Admin:** configure delivery zones and dispatch limits, monitor orders and
  bundles, and recover cancelled or failed deliveries.

Dispatch defaults are configurable per request: at most 3 orders, 2 km from the
seed order's store, 2 km from its drop-off, and vehicle capacity expressed in
liters. Eligible orders must be ready, share an enabled delivery zone, have
overlapping delivery windows, and have both locations inside that zone.
Proximity uses straight-line geography distance, not road distance or ETA.
The initial queue uses nearest-next stop ordering while ensuring an order is
picked up before it can be dropped off. It is a dispatch baseline, not a
turn-by-turn route optimizer.

## Platform and service boundaries

The product targets native iOS and Android customer/driver clients and a web
admin client. Clients should use versioned HTTPS APIs (`/v1`) and never connect
directly to the database. The repository includes a Go API and API-backed
mobile/admin MVP clients; an external identity-aware gateway remains required.

Backend capabilities can be deployed independently as traffic and ownership
needs emerge:

| Capability | Owns |
| --- | --- |
| Identity | Customer, merchant, driver, and admin identities and authorization |
| Catalog | Store locations, merchant catalogs, and availability |
| Ordering | Order lifecycle, delivery windows, and customer-facing order state |
| Dispatch | Delivery zones, eligibility, bundles, and stop queue generation |
| Delivery | Driver assignment and stop/order progress |
| Notifications | Push, email, and SMS delivery from domain events |

PostgreSQL with PostGIS is the source of truth for order/location relationships,
dispatch reservations, and stop ordering. `outbox_events` provides a
transactional handoff to asynchronous consumers; consumers should be
idempotent. Services should own writes to their domain data and communicate
through versioned APIs/events rather than sharing in-process state.

## Relational model and dispatch behavior

- `orders.store_location_id` and `orders.dropoff_location` explicitly connect
  every order to its pickup and destination.
- `delivery_bundles` represents the dispatch unit; `bundle_orders` is the
  many-to-many-ready membership relation, with a uniqueness constraint that
  reserves an order to no more than one bundle.
- `delivery_stops` persists the ordered pickup/drop-off queue and each stop's
  progress independently. Queue rows reference bundle membership and preserve
  the store location for pickups.
- `delivery_stop_events` provides an event-ID uniqueness guard for idempotent
  driver progress submissions.
- `delivery_zones` stores service-area polygons. GiST indexes support zone and
  proximity checks.

`create_proximity_bundle(...)` runs in the caller's database transaction. It
serializes duplicate idempotency keys, locks eligible ready orders with
`FOR UPDATE SKIP LOCKED`, enforces order-count and volume limits, then writes
membership, order-state changes, an active bundle, its pickup-before-dropoff
queue, and an activation outbox event atomically. Competing dispatch calls skip
locked orders; the unique membership constraint is a final reservation guard.
If a transaction fails, its reservation and queue writes roll back together.

The routine starts at the seed store because no driver start location is
provided. It sorts eligible candidates relative to the seed's store and
drop-off; after selection, nearest-next ordering produces the queue. A
production router should replace this approximation with road-network travel
times and delivery-window ETA checks before assigning a driver.

## API and event contract

The dispatch entry point is `POST /v1/dispatch/bundles`, authenticated for
dispatch/admin roles. It accepts a seed order, capacity in liters, optional
maximum order count/radii, and an `Idempotency-Key` header. Dispatch calls the
database function and returns the bundle identifier and ordered stops. Driver
clients fetch `GET /v1/driver/bundles/{bundleId}` and report progress through
`POST /v1/driver/bundles/{bundleId}/stops/{stopId}/events` with an event ID,
event type, and observed timestamp. Progress writes must verify driver
assignment, bundle/stop relationship, legal state transition, and that a
drop-off's pickup is complete. Admin APIs manage zones and limits; customer
APIs expose only that customer's orders and tracking data.

The initial dispatch and driver HTTP request/response shapes are specified in
`api/openapi.yaml`; the Go API implements proximity dispatch, assigned route
reads, driver GPS ingestion, and ordered route stop progression. The API also
exposes visible storefront catalogs and idempotent customer order creation with
server-priced item and volume snapshots. Merchants can list, create, edit, and
hide catalog items for their own store locations; ownership is enforced in the
database as well as by the trusted merchant-role gateway.
Initial API-backed iOS/Android shopper, driver, and merchant billing screens
are in `apps/mobile`; the current admin refund
workflow is in `apps/admin`. Broader merchant/admin workflows remain
unimplemented.

The database emits `delivery.bundle.activated` with `bundleId`, `orderIds`, and
`stopCount` in the outbox. Suggested follow-on events are
`order.ready_for_dispatch`, `delivery.stop.completed`,
`delivery.bundle.cancelled`, and `order.delivered`. Consumers should deduplicate
by event ID and handlers should accept request idempotency keys.

## D.C. Unified Basket, routing, and payments

Migration `002_unified_marketplace.sql` adds merchants, $199/month Stripe
subscription references, unified customer checkouts, jurisdiction polygons,
invoice lines, Connect transfer records, and driver route/location tables.
Existing store rows can be associated with a merchant by setting
`store_locations.merchant_id`.

Migration `006_catalog_orders.sql` adds store-scoped catalog items and
order-line price/volume snapshots. Customer order creation resolves current
prices from the database, verifies a visible storefront and service-zone
coverage, and creates the order and outbox event atomically.

Migration `007_merchant_catalog_management.sql` adds idempotent merchant item
creation and ownership-checked item updates. Item availability controls
customer catalog visibility; changing an item does not rewrite existing order
snapshots.

`nearby_subscribed_merchants(anchor_store, 500)` uses PostGIS geography distance
to suggest active grocery/retail storefronts within 500 meters. It excludes
inactive stores, expired/unpaid subscriptions, and subscriptions with a payment
failure. The storefront visibility view allows an active subscription or a
past-due subscription for less than 48 hours after payment failure; clients
should use that view to suppress late-paying stores.

`checkout_orders` represents the unified basket while existing `orders` remain
merchant-level fulfillment orders. A transaction-scoped lock in the membership
trigger serializes basket edits and rejects a third merchant, a second pickup
location for one merchant, mismatched customer/destination/zone, or an
unsubscribed additional merchant. D.C. delivery addresses are identified by
`US-DC` jurisdiction polygons. `apply_dc_delivery_surcharge` idempotently adds
one $0.20 invoice line. Only after Stripe confirms payment does
`record_checkout_payment` create the tax-holding ledger record and mark the
checkout paid.

The Go `POST /checkouts` handler accepts an authenticated customer's anchor
store, order IDs, destination coordinates, and delivery window. Delivery and
service fees come from `SHOPSTOPS_DELIVERY_FEE_CENTS` and
`SHOPSTOPS_SERVICE_FEE_CENTS`; order item amounts come from the immutable
`orders.item_subtotal_cents` snapshot. `create_unified_checkout` reserves all
orders, applies merchant/zone/window constraints, writes merchant-level invoice
lines and fees, applies the D.C. surcharge, and marks the checkout quoted in a
single transaction. Repeating an idempotency key with the same request returns
the original checkout; a different request using that key is rejected.

`create_unified_route` requires a paid basket containing an anchor restaurant
order and exactly one partner merchant. It persists the mandatory immutable
sequence: restaurant pickup, adjacent merchant pickup, customer drop-off.
`POST /checkouts/{checkoutId}/route` reads these stop coordinates from the
database, calculates both legs with the OSRM client, and persists the route and
measurements atomically. Each OSRM request has a 450 ms deadline and propagates
caller cancellation. Set `SHOPSTOPS_OSRM_BASE_URL` to enable this endpoint; it
returns service unavailable when no router is configured and gateway timeout
when OSRM exceeds its deadline. Route creation is idempotent by
`Idempotency-Key`, and OSRM calls occur outside the database transaction. The
repository does not provide turn-by-turn navigation. Driver clients should
sample GPS every five seconds;
`record_driver_location` enforces assigned active routes, idempotency, and no
more than one sample per five seconds. Drivers can request
`POST /driver/routes/{routeId}/reroute` after a fresh GPS sample; it is separate
from paid route creation, which remains dispatch/admin-only. The reroute
operation compares the OSRM distance from the latest driver location to the
next eligible stop against that stored leg, and persists a replacement only
when it is more than 0.2 miles longer. The update is audited, updates the
remaining leg's ETA, and publishes `delivery.route.rerouted`. Customers can poll
`GET /shopper/orders/{orderId}/track`; ownership is checked against the order's
customer reference, stale driver locations are hidden after 60 seconds, and the
response omits driver identity. The mobile shopper screen polls every four
seconds while open. Route/authenticated API processes must reject client
attempts to reorder stops.

Migration `003_stripe_checkout_payments.sql` records verified Stripe checkout
events and transfer source charges. The Go API's authenticated
`POST /checkouts/{checkoutId}/payment` creates one idempotent PaymentIntent using
the persisted invoice total. The public client receives only the
PaymentIntent's client secret, not the Stripe API key. Configure both
`STRIPE_SECRET_KEY` and `STRIPE_WEBHOOK_SECRET` server-side; without both, the
payment endpoints return unavailable.

`POST /webhooks/stripe` verifies Stripe's signature, stores a sanitized event
summary, and atomically updates the checkout/payment/tax ledger. Only after
`payment_intent.succeeded` does it initiate separate Connect transfers to each
merchant. Stripe transfer calls use stable idempotency keys and source charge
IDs; an event retry resumes any transfer not persisted after Stripe accepted
it. The database reserves a checkout-level PaymentIntent idempotency key and
records the first transfer-attempt timestamp. After 23 hours without a
persisted Stripe object ID, the API stops retrying that operation to avoid a
duplicate after Stripe expires idempotency keys; operators must reconcile the
Stripe account before retrying. Every merchant with a positive item subtotal
must have a connected account before payment can be initialized.

Processing fees are allocated proportionally over merchant item subtotals,
rounded with largest remainders (merchant ID breaks ties), and capped at the
total item value. The platform absorbs fee amounts above that cap. Delivery and
service fees remain in the platform ledger; the D.C. surcharge is tracked in
the municipal tax ledger. Do not collect or store raw card details.
`POST /merchant/subscription/setup-intent` creates a Stripe Customer when needed
and returns a SetupIntent client secret so the merchant can save a
payment method for off-session billing. `POST /merchant/subscription` validates
the configured Stripe Price is exactly $199 USD monthly and schedules the
subscription on the next first-of-month billing anchor. Subscription webhooks
update the persisted state; storefront visibility is not activated until an
invoice payment succeeds. `invoice.payment_failed` starts the existing 48-hour
grace period, and successful collection clears it.

Admins can request a full remaining checkout refund through
`POST /admin/checkouts/{checkoutId}/refund`. The API persists an idempotent
refund attempt before calling Stripe. Verified refund events update the payment
and tax ledgers and reserve idempotent Connect transfer reversals for each
completed merchant payout. Dispute events persist status/evidence deadlines and
allocate reversals proportionally across completed merchant payouts, capped at
their total. A won
dispute does not automatically restore reversed payouts; that requires
reconciliation. Refunds are currently full-remaining-balance only; partial
refunds are not supported.

Order state updates append transactional outbox events. A WebSocket/push
gateway should consume these events and target a <1.5 second propagation SLO;
the database outbox alone does not deliver real-time client updates.

`cmd/shopstops-api` is the runnable Go service. It implements health, nearby
subscribed-merchant discovery, catalog browsing, customer order creation,
proximity dispatch, checkout creation and Stripe payment, subscription, refund,
dispute, and webhook processing, driver route
reads, ordered stop arrival/completion, and GPS ingestion backed by PostgreSQL.
Driver stop events are idempotent, assignment-checked, and must advance through
the server-defined pickup/pickup/drop-off sequence; completion publishes route
events to the transactional outbox. Billing requires `STRIPE_MERCHANT_MONTHLY_PRICE_ID` set to a $199 USD monthly
Stripe Price in addition to the Stripe API and webhook secrets. It requires
`DATABASE_URL` and a 32-character `SHOPSTOPS_INTERNAL_API_TOKEN`; the shared token is an internal service
credential, not end-user authentication. Keep the API behind an identity-aware
gateway that authenticates client sessions and enforces roles, and do not ship
the internal token to applications. The gateway must strip incoming
`X-ShopStops-Actor` and `X-ShopStops-Actor-Role` values, then inject both from
the authenticated session. The API checks the required customer, driver,
dispatch, merchant, or admin role before sensitive operations; database
assignment and ownership checks remain authoritative. Do not expose the API
directly to clients or treat identity headers as authentication.

Migration `008_driver_fraud_monitor.sql` creates review-only alerts from driver
GPS samples. A segment above 55 m/s creates a high-severity impossible-speed
alert; at least three samples whose accumulated straight-line travel exceeds
2.5 times planned route-leg distance plus 1 km creates a medium-severity route
expansion alert. At most one alert of each type is created per route. These
conservative heuristics are signals for human review, not proof of fraud; they
do not affect driver assignment, route status, or earnings. Admins can list
alerts with `GET /admin/driver-fraud-alerts` and mark them reviewed or dismissed
with `PATCH /admin/driver-fraud-alerts/{alertId}`. Alert-resolution actor and
time are retained for audit. The admin control room exposes this queue with
status filtering and review/dismiss actions.

The standalone Go `finance` package implements the supplied $7 standard driver
base pay, $4.50 along-route bonus, zone-rate mileage cost, 15% mileage recovery
equation, 2.9% plus $0.30 processing fee, $1.20 protection allocation, and
5% merchant surcharge when unsubscribed. Callers must pass the zone rate in
cents per mile and the distance to price. It validates invalid inputs and
integer overflow. This is not yet wired into checkout creation or persisted
invoices; the current checkout continues to use configured delivery and
service fees. `NetPlatformMargin` is the value from the supplied formula, not
a fully reconciled accounting margin, as it does not deduct every listed cost.

The OpenAPI contract describes subscription, refund, and webhook flows. The
`apps/mobile` Expo client targets iOS and Android and exposes current shopper,
driver, and merchant billing API flows; `apps/admin` provides the full-refund
operation and driver-alert review queue. Both expect an external trusted identity gateway and
Stripe publishable key configuration. The clients are API-backed MVP
foundations, not complete consumer, driver, merchant, or admin products.
Native identity, background location, admin search/zone/reporting controls,
production routing, and
real-time integrations remain unimplemented.

For rollout, configure the initial Union Market corridor as the only enabled
zone and cap onboarding operationally at 15 merchants and 30 screened drivers.
Only expand to NoMa and Capitol Hill after operational review; shift scheduling,
tax remittance reporting, geocoding, and production route-matrix credentials
are not implemented in this database/API foundation.

## Reliability, security, and operations

Use role- and resource-based authorization for customer, merchant, driver, and
admin operations. Validate coordinates, service-zone membership, capacities,
delivery windows, and all state transitions server-side. Keep payment data out
of dispatch records. Do not expose exact customer addresses or contact details
to unauthorized users.

Monitor ready-order age, bundle size, dispatch latency, skipped/locked
candidates, stop completion delays, outbox backlog, and cancellations. Retain
an audit trail for admin changes and driver progress. Recovery flows should
release or re-dispatch unpicked orders after cancellation; picked-up orders
must be handled explicitly rather than silently returned to the ready pool.

## Applying and validating the migration

Install PostgreSQL 14+ and PostGIS 3.x, then apply the migration with `psql`:

```sh
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 \
  -f db/migrations/001_delivery_foundation.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 \
  -f db/migrations/002_unified_marketplace.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 \
  -f db/migrations/003_stripe_checkout_payments.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 \
  -f db/migrations/004_billing_refunds_disputes.sql
```

After applying all migrations, exercise `create_proximity_bundle` with test zones and orders,
checking overlapping/non-overlapping windows, out-of-zone locations, distance
thresholds, capacity and order-count limits, duplicate idempotency keys,
concurrent dispatch, and pickup-before-dropoff ordering.

The fixture test in `db/tests/001_dispatch.sql` covers a basic nearby multi-store
bundle, distance/window exclusion, idempotent retry, stop precedence, and the
activation outbox event. Run it after applying the migration with:

```sh
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f db/tests/001_dispatch.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f db/tests/002_unified_marketplace.sql
```
