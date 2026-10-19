# ShopStops

ShopStops is a multi-store delivery platform. The initial backend foundation is a
PostgreSQL/PostGIS schema and transactional dispatch routine for grouping nearby
ready orders into a bundle and persisting its pickup/drop-off queue.

## Database

Apply `db/migrations/001_delivery_foundation.sql` to PostgreSQL 14+ with PostGIS
3.x installed. The migration enables PostGIS and creates the relational model,
dispatch indexes, event outbox, and `create_proximity_bundle(...)` function.
The function locks eligible orders, creates an active bundle, and writes its
ordered stops in one transaction. Calls should use a stable idempotency key.

See [`docs/architecture.md`](docs/architecture.md) for MVP assumptions, service
boundaries, workflows, API contracts, and operational guidance.

This repository currently contains the database/backend foundation only. Native
iOS and Android apps, the admin web app, and deployable service/API processes
remain follow-on implementation work.
