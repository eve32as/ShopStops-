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

This repository currently contains the database/backend foundation only. Native
iOS and Android apps, the admin web app, and deployable service/API processes
remain follow-on implementation work.
