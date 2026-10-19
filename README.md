# ShopStops

ShopStops is a multi-store delivery platform. The initial backend foundation is a
PostgreSQL/PostGIS schema and transactional dispatch routine for grouping nearby
ready orders into a bundle and persisting its pickup/drop-off queue.

## Database

Apply `db/migrations/001_delivery_foundation.sql` and then
`db/migrations/002_unified_marketplace.sql` to PostgreSQL 14+ with PostGIS 3.x
installed. The migrations create the dispatch and unified-basket relational
models, spatial queries, transaction-safe order/merchant limits, immutable
multi-stop routes, payment/tax ledger records, and outbox events.

See [`docs/architecture.md`](docs/architecture.md) for MVP assumptions, service
boundaries, workflows, and operational guidance. The initial HTTP interface is
specified in [`api/openapi.yaml`](api/openapi.yaml).

Database integration fixtures are in `db/tests/`; apply both migrations before
running the matching SQL test files.

## Run the API

After applying both migrations, set `DATABASE_URL` and a private internal API
token (at least 32 characters), then run:

```sh
export DATABASE_URL='postgres://<user>:<password>@localhost:5432/shopstops?sslmode=disable'
export SHOPSTOPS_INTERNAL_API_TOKEN="$(openssl rand -hex 32)"
go run ./cmd/shopstops-api
```

The service listens on `:8080` by default; set `HTTP_ADDR` to override it. It
currently implements health checks, nearby subscribed-merchant discovery, and
atomic dispatch. Protected endpoints use a shared internal bearer token and
must be placed behind a gateway that authenticates users and enforces roles.
Do not distribute this internal token to client applications.

Native iOS and Android apps, the admin web app, the remaining contract
endpoints, and live Stripe, routing, and WebSocket integrations remain
follow-on work.
