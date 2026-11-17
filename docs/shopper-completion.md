# Supervised shopper completion records

The platform's completion-based shopper amount is **per verified completed
shopping order**, not per checkout, pickup, login, signup, acceptance or assignment.
The finance calculator's historical per-checkout assumption remains an estimate,
not an operational pay rule. This feature records scheduled compensation and then
completion earnings **pending review**; it does not transfer money or run payroll.

## Administrative workflow

1. An authenticated gateway admin records a task against an existing merchant order,
   an opaque shopper reference, a jurisdiction reference and an explicitly approved
   positive USD amount in integer cents (1–100,000,000). There are no default rates.
   The amount, shopper, order, assigning admin and currency are immutable snapshots.
2. Assignment creates only a task and an `assigned` audit event. No earnings exist.
   `eligibility_status` remains `pending_review`; there is no legal approval button
   or eligibility transition. This record is not authorization to begin work.
3. A gateway-verified admin confirms the order's item shopping/receipt evidence,
   authorized substitutions (or that none were necessary), and handoff proof.
   Both confirmations must be true and both evidence references present.
4. One database function atomically inserts immutable completion evidence, a
   `verified_completed` audit event, and one `earned_per_order` ledger entry at the
   original `completion_amount_cents`, with status `pending_review`. Task status is
   derived from completion existence, not an independently mutable status column.

Endpoints are exactly POST/GET `/admin/shopper/tasks` and POST
`/admin/shopper/tasks/{taskId}/complete`. Both mutations require UUID
`Idempotency-Key`. Assignment uses that UUID as its task ID. An identical retry
returns the record; another actor, payload, completion key or second assignment
for the same order returns 409. Completed assignment retries return its current
derived completed view, not a stale assigned response. The UI retains retry keys
after failure and clears identity-specific forms/results when credentials change.
The list is limited to 1–100 records (default 50), offset 0–100,000, ordered by
assignment time and UUID; it is a page, not a cumulative balance or payroll export.

## Ownership, payment and privacy

The server resolves merchant ownership and order customer identity from existing
tables. If attached to a checkout, its customer must match the order's customer;
payment status is resolved only through that checkout's payment row. Completion
also compares current merchant/customer ownership with the assignment snapshot.
No client payer or verifier field is accepted. The verifier is the authenticated
admin actor injected by the trusted gateway. Only the gateway should hold the
internal service token; never expose the backend directly to end users.

Payment status is snapshotted as a review fact (`not_recorded` when absent).
Successful customer payment is **not** a sole gate to recording compensation for
verified work. Failed/refunded payments or cancellation after assignment do not
erase verified completed work. New assignments to cancelled orders are rejected.
Cancelled/partially completed tasks, required paid time and other wage obligations
must be handled separately; inability to meet this completion workflow is not a
determination that compensation is legally zero.

Shopper and evidence references allow only 1–100 ASCII letters, digits, underscore
and hyphen. They must identify private independently verified records: not names,
emails, receipt contents, addresses, bank details or URLs. Format validation does
not establish identity or verify the referenced evidence. Future verified shopper
identity, evidence storage access controls and retention policies are prerequisites
for operational use. No shopper-facing routes or new shopper auth role are enabled.

## Durability and concurrency

Migration 010 adds append-only tasks, completions, earnings and transition audit.
Unique order/task constraints prevent reassignment and double earnings across keys.
Transaction-scoped idempotency locks and task/order locks serialize competing
completions and assignments; database function calls commit atomically. Deferred
consistency checks reject orphaned completion/earnings and amounts or actors that
do not match their assignment. Mutation/truncation triggers prevent altering audit,
evidence or the immutable amount. No `paid`/`payable` state exists. Database owners
can disable triggers: owners must never be application credentials.

Functions use invoker security and all new objects/functions revoke PUBLIC access.
A deploy operator must review explicit dedicated internal API-role grants: SELECT
on the view and underlying/new context tables, INSERT on the four append-only
tables, EXECUTE on the three workflow/context functions; row locking additionally
requires UPDATE privilege on locked tasks/orders/store locations and attached checkouts/checkout
membership rows (triggers still reject task
mutation). Never grant ownership, DDL, DELETE, TRUNCATE or payroll privileges.
No grants, migration application or deployment occur automatically here.
Repositories without the optional `ShopperRepository`, or databases missing the
migration, return 503. There are no calls to Stripe/Connect or any payment provider.

## Accounting and launch blockers

Migration 009 priority allocation intentions are untouched and never read as
shopper earnings. The assigned amount is the entire approved per-order completion
amount; include any independently approved premium there, do not import priority
allocations, or count them again. This ledger is separate from the manual finance
cost ledger and planning forecast. Do not add these pending entries to manual
costs/payroll a second time; reconciliation and posting are not implemented.

Before real work, obtain jurisdiction-specific employment/youth-employment review,
verified identity/age, classification, safe task restrictions and supervision,
school-day/hour limits, work-time recording, minimum wage/overtime/reimbursement
handling, tax onboarding, and a separate approved payroll/payment process. No minor
is automatically eligible. No recorded completion bypasses compliance. Pending
review is an administrative accounting state, not permission to delay legally
required wages. There is no enrollment, compliance approval, payout, reassignment,
correction or payable transition endpoint in this implementation.

## Validation

Backend and gateway unit tests cover roles, trusted actor forwarding, bounds,
strict evidence/amount bodies, 409/404/503 mappings and optional repositories.
Frontend tests cover positive decimal cents, opaque references, HTTPS URLs,
user-token-only requests and identical retry keys. SQL fixture 005 is rollback-only
and checks amounts, payer binding, failed-payment completion, immutable records
and PUBLIC revocation. The ordered disposable SQL runner applies migration 010 in
CI; the offline runner suite checks ordering/failure control flow only.
`TestShopperPostgresIntegration` requires an explicit local disposable
`SHOPSTOPS_SHOPPER_TEST_DSN` (shopstops_ci database/user); it tests actual commits and
concurrent completion retries. Without it, database integration is skipped, not
proof that the migration was executed. Do not point any fixture at a real database.