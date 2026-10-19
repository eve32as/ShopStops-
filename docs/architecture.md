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
directly to the database. The repository currently implements the shared
relational dispatch foundation, not client applications or running API
processes.

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
`api/openapi.yaml`; serving implementations are not part of this repository yet.

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

`create_unified_route` requires a paid basket containing an anchor restaurant
order and exactly one partner merchant. It persists the mandatory immutable
sequence: restaurant pickup, adjacent merchant pickup, customer drop-off.
`unified_route_legs` stores road-network distance/duration returned by an OSRM
or Google Maps matrix integration; this repository does not call either vendor
or provide turn-by-turn navigation. Driver clients should sample GPS every five
seconds; `record_driver_location` enforces assigned active routes, idempotency,
and no more than one sample per five seconds. Route/authenticated API processes
must reject client attempts to reorder stops.

Stripe Connect needs one customer charge and separate transfers to each
merchant; a single Stripe destination charge targets one connected account and
does not by itself split across two merchants. Transfer records capture item
gross, allocated processing fee, and net transfer. Delivery/service fees and
the D.C. surcharge are kept in the platform ledger; the surcharge also has a
separate municipal tax ledger. The SaaS subscription table stores Stripe Billing
references and fixed monthly price, but subscription creation, recurring
collection, webhook signature verification, and actual Connect transfers must
be implemented by a trusted backend integration. Do not collect or store raw
card details in ShopStops.

Order state updates append transactional outbox events. A WebSocket/push
gateway should consume these events and target a <1.5 second propagation SLO;
the database outbox alone does not deliver real-time client updates.

`cmd/shopstops-api` is the initial runnable Go service. It implements health,
nearby subscribed-merchant discovery, and proximity dispatch backed by the
PostgreSQL functions. It requires `DATABASE_URL` and a 32-character
`SHOPSTOPS_INTERNAL_API_TOKEN`; the shared token is an internal service
credential, not end-user authentication. Keep the API behind an identity-aware
gateway that authenticates client sessions and enforces roles, and do not ship
the internal token to applications. The gateway must strip any incoming
`X-ShopStops-Actor` value and inject the authenticated actor identity; driver
route handlers trust this internal header for assignment checks.

The OpenAPI contract additionally describes checkout/payment, driver routes/GPS,
merchant subscription status, and Stripe webhooks, but those routes are not yet
implemented. Four separate consumer, driver, merchant, and admin applications,
identity, checkout/payment, routing, and real-time service integrations remain
to be built.

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
```

After applying, exercise `create_proximity_bundle` with test zones and orders,
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
