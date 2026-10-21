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

Each delivery zone stores defaults of at most 3 orders, 2 km from the seed
order's store, and 2 km from its drop-off; vehicle capacity is configured per
dispatch run in liters. Eligible orders must be ready, share an enabled delivery zone, have
overlapping delivery windows, and have both locations inside that zone.
Proximity uses straight-line geography distance, not road distance or ETA.
The initial queue uses nearest-next stop ordering while ensuring an order is
picked up before it can be dropped off. It is a dispatch baseline, not a
turn-by-turn route optimizer.

## Platform and service boundaries

The product targets native iOS and Android customer/driver clients and a web
admin client. Clients should use versioned HTTPS APIs (`/v1`) and never connect
directly to the database. The repository includes a Go API, an OIDC identity
gateway, and API-backed mobile/admin MVP clients; a real OIDC provider and
TLS-terminating deployment edge remain required.

The root Compose stack is a local staging scaffold: it publishes only the
gateway on loopback, keeps PostgreSQL private, and initializes a fresh database
volume from the ordered SQL files in `db/migrations`. It is not a substitute for
managed secrets, TLS termination, backups, or a reviewed migration process for
an existing database.

## Production release gates

This repository is not a production deployment. The following provider choices
are provisional planning assumptions, not configured infrastructure: AWS Cognito
for hosted OIDC, ECS/Fargate and RDS PostgreSQL/PostGIS for hosting, AWS Secrets
Manager and CloudWatch for secrets and telemetry. Select a road-network routing
provider after cost and coverage review. Push delivery requires APNs/FCM
credentials and platform setup. Production use remains blocked until owners
provision and verify these services.

Release criteria:

- **Shopper:** native sign-in and recovery; successful, failed, and pending
  payment handling; order history and live/recoverable delivery updates; consent
  and privacy controls.
- **Merchant:** verified store ownership; complete paid-order fulfillment,
  catalog, and billing workflows; clear subscription and payout failure handling.
- **Driver:** authenticated assignment discovery; safe foreground/background
  location consent; ordered navigation and stop progression; retry/offline
  recovery and completion behavior.
- **Dispatch/admin:** least-privilege access; assignment, reassignment and
  recovery workflows; actionable monitoring, audit history, and support tools.
- **Platform:** TLS at the deployment edge; managed secrets; tested database
  backup restoration and migrations; dashboards and actionable alerts; load,
  security, accessibility, and end-to-end role-flow testing; rollback and
  incident procedures.

The API exposes `/healthz` for process liveness and `/readyz` for database
readiness. These probes do not replace production monitoring or a deployment
health-check configuration. Do not treat a successful local build or smoke test
as evidence that any release gate has passed.

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
drop-off's pickup is complete. The admin control room lists delivery zones and
can enable or disable them for new orders and dispatch; existing orders and
bundles are unchanged. Admins can also initiate a bundle with per-request
vehicle capacity. Each zone stores admin-managed maximum order-count and
pickup/drop-off radius defaults. Bundle creation reads those defaults while
holding a shared zone lock; explicit dispatch-request overrides remain
supported. Limit updates record the admin subject and emit
`delivery.zone.dispatch_limits.updated` to the transactional outbox. Customer
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

Dispatch/admin users can create and assign a unified route to a driver for a
paid eligible checkout through `POST /v1/dispatch/routes`. This activates the
route and creates its ordered stops atomically. Drivers retrieve their active
assignments through `GET /v1/driver/routes` or fetch one route by ID; route
creation retries require the same idempotency key and assignment details.

The database emits `delivery.bundle.activated` with `bundleId`, `orderIds`, and
`stopCount` in the outbox. Suggested follow-on events are
`order.ready_for_dispatch`, `delivery.stop.completed`,
`delivery.bundle.cancelled`, and `order.delivered`. Consumers should deduplicate
by event ID and handlers should accept request idempotency keys.

Customers can long-poll `GET /orders/{orderId}/events` with a `Last-Event-ID`
cursor for order state, bundle activation, and the checkout route's stop events.
The API verifies order ownership and omits sibling order IDs and driver identity
from event payloads. Requests wait up to 25 seconds and return at most 100
events; clients should continue from `nextAfterId`. The mobile shopper persists
the cursor in OS secure storage per order and stops polling when the order or
route completes, the screen loses focus, the app backgrounds, or connection
credentials change. Push notifications and WebSocket fan-out remain external
integrations.

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

Migration `008_merchant_order_fulfillment.sql` lets authenticated merchants
advance paid orders through `placed` → `preparing` → `ready`. The update function
locks the order, verifies store ownership and paid checkout status, sets
`ready_at` when applicable, and treats repeating the current state as
idempotent. The mobile merchant screen lists only the merchant's paid orders
and exposes these transitions; customer identity and drop-off coordinates are
not returned by the order-list endpoint.

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
`unified_route_legs` stores road-network distance/duration returned by an OSRM
or Google Maps matrix integration; this repository does not call either vendor
or provide turn-by-turn navigation. Driver clients should sample GPS about every five
seconds; mobile background location updates are best-effort and subject to OS
scheduling and permissions. `record_driver_location` enforces assigned active
routes, idempotency, and no more than one sample per five seconds. Route/authenticated API processes
must reject client attempts to reorder stops.

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
the internal token to applications. The gateway must strip any incoming
`X-ShopStops-Actor` value and inject the authenticated actor identity; driver
route handlers trust this internal header for assignment checks.

The OpenAPI contract describes subscription, refund, webhook, and customer
order-event flows. `cmd/shopstops-gateway` is an OIDC JWT-verifying reverse
proxy for this API. Configure its issuer, audience, role/actor claim names, and
the same internal API token used by `cmd/shopstops-api`; keep the API private
behind the gateway. The gateway strips untrusted identity/forwarding headers,
enforces the route role, and injects the verified actor and role. The OIDC
provider must issue tokens with a stable actor claim and a top-level role string
or string array. The gateway validates tokens but does not provide native sign-in
or issue user tokens.
The
`apps/mobile` Expo client targets iOS and Android and exposes shopper, driver,
merchant order fulfillment and billing API flows; `apps/admin` provides the current
full-refund operation. Both use the trusted gateway and require externally
issued user tokens; the mobile client also requires Stripe publishable-key
configuration. The clients are API-backed MVP foundations, not complete
consumer, driver, merchant, or admin products.
Native identity-provider sign-in, full operational reporting and alerting,
production routing, and push or WebSocket integrations remain unimplemented.

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

The admin-only operations snapshot reports ready-order age, active bundle and
open-stop counts per zone, pending outbox count, and the age of its oldest
unpublished event. Monitor dispatch latency,
skipped/locked candidates, stop completion delays, and cancellations through
additional operational telemetry. Retain
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
