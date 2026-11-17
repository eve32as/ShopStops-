# Extra pickup platform pricing — test only

## Fixed rule and separate scopes

The platform surcharge is **0.25% (25 basis points)** of the **merchandise subtotal** per additional pickup per checkout. This is not a 0.25 multiplier (25%). Delivery, service, priority fees, tax and tips are excluded from the basis. The first pickup is included:

`extra = max(count(DISTINCT orders.store_location_id) - 1, 0)`

Same-store orders do not multiply pickups. The actual accepted `checkout_orders` relation supplies the orders; `merchant_items` invoice lines supply the authoritative merchandise subtotal. No raw coordinates, customer count, customer subtotal or customer rate is accepted for this rule. Existing catalog order creation determines merchandise prices server-side. The selected anchor must actually appear in accepted orders, as required by the original checkout function.

Integer cents, **HALF UP once** on the combined surcharge:

`feeCents = (subtotalCents * 25 * extra + 5000) / 10000` (integer division).

- 5000 cents, one extra: **13 cents**.
- 10000 cents, two extras: **50 cents**.
- 5000 cents, two extras: **25 cents**, not 26.
- No extra pickups: **0 cents**.

The existing checkout merchant limit is unchanged (anchor restaurant plus at most one eligible adjacent merchant), so actual checkouts currently have at most one extra location. Two-extra examples describe the planner's arithmetic, not relaxed checkout eligibility.

Planner inputs `merchandiseSubtotalCents` (0–100,000,000) and `additionalPickupsPerCheckout` (0–100) are optional and default to zero. The rate is fixed, returned as `additionalPickupPlatformFeeBasisPoints = 25`; no configurable rate exists. Per-checkout and monthly fee breakdowns are included **once** in customer fees, platform revenue forecast, contribution, net and break-even; do not add them again. Merchandise itself remains excluded from platform revenue/margin.

**Driver scope remains per bundle:** each `additionalPickupsPerBundle` adds 50% of `originalPickupCents`, individually half-up (501 → 251). These independent planner counts must not be substituted for each other for multi-drop bundles. Platform pricing creates no driver earnings, wage, payable, discount or payout record. Existing shopper completion records and priority worker allocation intentions are untouched.

## Operational gate

`SHOPSTOPS_EXTRA_PICKUP_TEST_ONLY` is independent of `SHOPSTOPS_PRIORITY_TEST_ONLY`. Unset/empty or exact `false` disables the surcharge. Only exact `true` enables it, and then a valid `sk_test_` Stripe secret key and a nonblank Stripe webhook secret are mandatory. Malformed flags, live/restricted keys, malformed test keys and missing webhook configuration fail startup closed. No credentials or rates are set by this implementation. There is no client opt-in or configurable rate for extra pickups, and priority choice/rate validation still works independently.

[Migration 011](../db/migrations/011_extra_pickup_test_checkout.sql) replaces the 009 checkout entry point with a final optional server boolean; original positional arguments and defaults keep their meaning. It invokes the inspected original base function, retaining customer/order ownership, anchor selection, eligible subscribed merchants, tax and geographic validation. All base, extra-pickup and priority invoice lines are built **before either immutable snapshot**. Calling the old priority wrapper first would freeze the invoice too early.

Each new checkout snapshots test-only state, merchandise basis, distinct extra count, fixed rate, fee cents and invoice total. The snapshot is immutable even when disabled. Enabled invoices, including zero-extra quotes, are immutable; deferred consistency checks prevent orphan fee lines and mismatched snapshot/total commits. Snapshots grant nothing to PUBLIC and use invoker security. Operators must review dedicated-role EXECUTE/SELECT/INSERT grants, including the new 15-argument function signature; no automatic production grants are applied.

Idempotency remains locked by checkout key. Retries validate the original customer, anchor, orders, destination, windows and priority choice, but keep original prices and feature state, even after enabling/disabling flags or changing base/priority configuration. Pre-011 retries are not repriced or backfilled and may omit the extra-pickup response snapshot.

Stripe initialization uses the **stored exact invoice total**, not a recomputed quote. At payment time, a snapshot marked test-only requires a gateway that positively reports test mode; live or unknown gateways are rejected before creating **or retrieving** a PaymentIntent. This also protects retries after a configuration change. The static Stripe test gateway rejects live webhook events. Existing merchant transfer allocation still uses merchandise lines only, and full refund reservation includes the platform surcharge. No new worker transfer logic exists.

## Unchanged restrictions

- Public gateway checkout/payment routes remain blocked; existing actor/customer ownership checks remain mandatory. This is not public checkout enablement or production identity clearance.
- No real payments, payouts, migrations, deployment, cloud resources or git operations are performed by adding this code.
- The planner's 0.500-mile FIRST-pickup radius remains metadata, not enforced eligibility. Existing checkout geographic rules (including the existing 500-meter adjacent-merchant condition) are retained; the surcharge does not assert qualification for a new 0.5-mile policy.
- No nearby-driver discount, actual extra pickup worker pay or speed promise is introduced.

## Validation and migration review

Go and frontend tests cover fixed-rate math, aggregate half-up rounding, omitted-zero compatibility, bounded inputs, separate bundle/checkout scopes, included-once contributions and minimum break-even, client input rejection, configuration gates and live/unknown payment guards. The rollback SQL fixture covers authoritative basis, same-store deduplication, first pickup inclusion, independent flags, retries, immutable snapshots/invoices, exact payment/refund totals, merchant allocation and privileges.

The disposable runner explicitly orders migrations 001–011 and discovers all SQL fixtures. CI also runs `TestExtraPickupPostgresIntegration` with a local disposable `shopstops_ci` database/user. Without `SHOPSTOPS_EXTRA_PICKUP_TEST_DSN`, that real repository test is **skipped**; offline runner tests validate shell control flow only, not SQL correctness. Review and apply migration 011 separately through the established migration process before starting this code against a database. No production migration is applied automatically.