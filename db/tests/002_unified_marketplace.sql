\set ON_ERROR_STOP on

BEGIN;

INSERT INTO delivery_zones (name, boundary)
VALUES ('Union Market test zone',
        ST_Multi(ST_MakeEnvelope(-77.05, 38.85, -76.95, 38.95, 4326)))
RETURNING id AS zone_id \gset
INSERT INTO delivery_zones (name, boundary)
VALUES
    ('Metro Downtown Sector',
     ST_Multi(ST_MakeEnvelope(-77.05, 38.85, -76.95, 38.95, 4326))),
    ('North Logistics Hub',
     ST_Multi(ST_MakeEnvelope(-77.05, 38.85, -76.95, 38.95, 4326)));
DO $$
BEGIN
    IF (SELECT driver_mileage_cost_per_mile_cents
        FROM delivery_zones WHERE name = 'Union Market test zone') <> 65
       OR (SELECT driver_mileage_cost_per_mile_cents
           FROM delivery_zones WHERE name = 'Metro Downtown Sector') <> 65
       OR (SELECT driver_mileage_cost_per_mile_cents
           FROM delivery_zones WHERE name = 'North Logistics Hub') <> 80 THEN
        RAISE EXCEPTION 'zone mileage rates did not use the configured defaults and overrides';
    END IF;
END;
$$;
INSERT INTO jurisdictions (code, name, boundary)
VALUES ('US-DC', 'District of Columbia test polygon',
        ST_Multi(ST_MakeEnvelope(-77.12, 38.79, -76.91, 39.0, 4326)));

INSERT INTO merchants (name, merchant_type)
VALUES ('Anchor Restaurant', 'restaurant'),
       ('Adjacent Market', 'grocery'),
       ('Unsubscribed Shop', 'retail'),
       ('Third Active Shop', 'retail');
SELECT id AS anchor_merchant_id FROM merchants
WHERE name = 'Anchor Restaurant' \gset
SELECT id AS partner_merchant_id FROM merchants
WHERE name = 'Adjacent Market' \gset
SELECT set_config('shopstops.test_partner_merchant_id', :'partner_merchant_id', true);
SELECT id AS third_merchant_id FROM merchants
WHERE name = 'Unsubscribed Shop' \gset
SELECT id AS cap_merchant_id FROM merchants
WHERE name = 'Third Active Shop' \gset

INSERT INTO merchant_subscriptions (
    merchant_id, status, stripe_customer_id, stripe_subscription_id
)
VALUES (:'partner_merchant_id', 'active', 'cus_test', 'sub_test'),
       (:'third_merchant_id', 'past_due', 'cus_late', 'sub_late'),
       (:'cap_merchant_id', 'active', 'cus_cap', 'sub_cap');
UPDATE merchant_subscriptions
SET payment_failed_at = now() - interval '49 hours'
WHERE merchant_id = :'third_merchant_id';

INSERT INTO store_locations (name, address, location, merchant_id)
VALUES
    ('Anchor location', '1 Test St',
     ST_SetSRID(ST_MakePoint(-76.99, 38.91), 4326)::geography,
     :'anchor_merchant_id'),
    ('Market location', '2 Test St',
     ST_SetSRID(ST_MakePoint(-76.989, 38.91), 4326)::geography,
     :'partner_merchant_id'),
    ('Third location', '3 Test St',
     ST_SetSRID(ST_MakePoint(-76.988, 38.91), 4326)::geography,
     :'third_merchant_id'),
    ('Capacity test location', '4 Test St',
     ST_SetSRID(ST_MakePoint(-76.987, 38.91), 4326)::geography,
     :'cap_merchant_id');
SELECT id AS anchor_store_id FROM store_locations
WHERE name = 'Anchor location' \gset
SELECT id AS partner_store_id FROM store_locations
WHERE name = 'Market location' \gset
SELECT set_config('shopstops.test_partner_store_id', :'partner_store_id', true);
SELECT id AS third_store_id FROM store_locations
WHERE name = 'Third location' \gset
SELECT id AS cap_store_id FROM store_locations
WHERE name = 'Capacity test location' \gset

INSERT INTO unified_checkouts (
    idempotency_key, customer_ref, anchor_store_location_id, delivery_zone_id, dropoff_location,
    delivery_window_start, delivery_window_end, delivery_fee_cents, service_fee_cents
)
VALUES (
    'manual-test-checkout', 'customer-test', :'anchor_store_id', :'zone_id',
    ST_SetSRID(ST_MakePoint(-76.98, 38.91), 4326)::geography,
    now(), now() + interval '2 hours', 500, 100
)
RETURNING id AS checkout_id \gset

INSERT INTO orders (
    customer_ref, store_location_id, delivery_zone_id, dropoff_location,
    delivery_window_start, delivery_window_end, volume_liters, state, ready_at
)
VALUES (
    'customer-test', :'anchor_store_id', :'zone_id',
    ST_SetSRID(ST_MakePoint(-76.98, 38.91), 4326)::geography,
    now(), now() + interval '2 hours', 2, 'ready', now()
)
RETURNING id AS anchor_order_id \gset
INSERT INTO orders (
    customer_ref, store_location_id, delivery_zone_id, dropoff_location,
    delivery_window_start, delivery_window_end, volume_liters, state, ready_at
)
VALUES (
    'customer-test', :'partner_store_id', :'zone_id',
    ST_SetSRID(ST_MakePoint(-76.98, 38.91), 4326)::geography,
    now(), now() + interval '2 hours', 1, 'ready', now()
)
RETURNING id AS partner_order_id \gset
INSERT INTO checkout_orders (checkout_id, order_id)
VALUES (:'checkout_id', :'anchor_order_id'),
       (:'checkout_id', :'partner_order_id');
INSERT INTO checkout_invoice_lines (
    checkout_id, merchant_id, line_type, description, amount_cents
)
VALUES (:'checkout_id', :'anchor_merchant_id', 'merchant_items',
        'Restaurant items', 1500),
       (:'checkout_id', :'partner_merchant_id', 'merchant_items',
        'Market items', 1000),
       (:'checkout_id', NULL, 'delivery_fee', 'Delivery fee', 500),
       (:'checkout_id', NULL, 'service_fee', 'Service fee', 100);
INSERT INTO unified_checkouts (
    idempotency_key, customer_ref, anchor_store_location_id, delivery_zone_id, dropoff_location,
    delivery_window_start, delivery_window_end
)
VALUES (
    'manual-non-dc', 'customer-outside-dc', :'anchor_store_id', :'zone_id',
    ST_SetSRID(ST_MakePoint(-77.2, 38.91), 4326)::geography,
    now(), now() + interval '2 hours'
)
RETURNING id AS non_dc_checkout_id \gset

INSERT INTO orders (
    customer_ref, store_location_id, delivery_zone_id, dropoff_location,
    delivery_window_start, delivery_window_end, volume_liters, state,
    ready_at, item_subtotal_cents
)
VALUES (
    'api-checkout-customer', :'anchor_store_id', :'zone_id',
    ST_SetSRID(ST_MakePoint(-76.98, 38.91), 4326)::geography,
    now(), now() + interval '2 hours', 1, 'ready', now(), 1200
)
RETURNING id AS api_anchor_order_id \gset
INSERT INTO orders (
    customer_ref, store_location_id, delivery_zone_id, dropoff_location,
    delivery_window_start, delivery_window_end, volume_liters, state,
    ready_at, item_subtotal_cents
)
VALUES (
    'api-checkout-customer', :'partner_store_id', :'zone_id',
    ST_SetSRID(ST_MakePoint(-76.98, 38.91), 4326)::geography,
    now(), now() + interval '2 hours', 1, 'ready', now(), 800
)
RETURNING id AS api_partner_order_id \gset
SELECT create_unified_checkout(
    'api-checkout-customer', :'anchor_store_id',
    ARRAY[:'api_anchor_order_id'::uuid, :'api_partner_order_id'::uuid],
    38.91, -76.98, now(), now() + interval '2 hours',
    500, 100, 'api-checkout-idempotency'
) AS api_checkout_id \gset
SELECT create_unified_checkout(
    'api-checkout-customer', :'anchor_store_id',
    ARRAY[:'api_anchor_order_id'::uuid, :'api_partner_order_id'::uuid],
    38.91, -76.98, now(), now() + interval '2 hours',
    500, 100, 'api-checkout-idempotency'
) AS api_checkout_replay_id \gset
SELECT set_config('shopstops.test_api_checkout_id', :'api_checkout_id', true);
SELECT set_config('shopstops.test_api_checkout_replay_id',
                  :'api_checkout_replay_id', true);
SELECT set_config('shopstops.test_anchor_store_id', :'anchor_store_id', true);

INSERT INTO orders (
    customer_ref, store_location_id, delivery_zone_id, dropoff_location,
    delivery_window_start, delivery_window_end, volume_liters, state,
    ready_at, item_subtotal_cents
)
VALUES (
    'standard-tier-customer', :'anchor_store_id', :'zone_id',
    ST_SetSRID(ST_MakePoint(-76.98, 38.91), 4326)::geography,
    now(), now() + interval '2 hours', 1, 'ready', now(), 1500
)
RETURNING id AS standard_anchor_order_id \gset
INSERT INTO orders (
    customer_ref, store_location_id, delivery_zone_id, dropoff_location,
    delivery_window_start, delivery_window_end, volume_liters, state,
    ready_at, item_subtotal_cents
)
VALUES (
    'standard-tier-customer', :'third_store_id', :'zone_id',
    ST_SetSRID(ST_MakePoint(-76.98, 38.91), 4326)::geography,
    now(), now() + interval '2 hours', 1, 'ready', now(), 1000
)
RETURNING id AS standard_partner_order_id \gset
SELECT create_unified_checkout(
    'standard-tier-customer', :'anchor_store_id',
    ARRAY[:'standard_anchor_order_id'::uuid, :'standard_partner_order_id'::uuid],
    38.91, -76.98, now(), now() + interval '2 hours',
    700, 379, 'standard-tier-checkout'
) AS standard_tier_checkout_id \gset
SELECT set_config('shopstops.test_standard_tier_checkout_id',
                  :'standard_tier_checkout_id', true);
INSERT INTO checkout_financial_estimates (
    checkout_id, item_subtotal_cents, commission_cents,
    delivery_fee_cents, service_fee_cents, pass_through_cents,
    estimated_processor_fee_cents, driver_payout_cents,
    driver_base_payout_cents, along_route_pickup_bonus_cents,
    fixed_order_cost_cents, target_profit_cents,
    projected_net_profit_cents, target_margin_bps,
    stripe_fee_bps, stripe_fixed_fee_cents,
    along_route_pickup_count, typical_basket_cents
) VALUES (
    :'standard_tier_checkout_id', 2500, 250, 700, 379, 20, 134, 700,
    700, 450, 120, 375, 375, 1500, 290, 30, 0, 4500
);

DO $$
DECLARE
    v_checkout_id uuid := current_setting('shopstops.test_api_checkout_id')::uuid;
    v_replay_id uuid :=
        current_setting('shopstops.test_api_checkout_replay_id')::uuid;
    v_order_ids uuid[];
    v_message text;
BEGIN
    SELECT array_agg(order_id ORDER BY order_id) INTO v_order_ids
    FROM checkout_orders WHERE checkout_id = v_checkout_id;
    IF v_checkout_id <> v_replay_id THEN
        RAISE EXCEPTION 'idempotent checkout retry created a different checkout';
    END IF;
    IF (SELECT status FROM unified_checkouts WHERE id = v_checkout_id) <> 'quoted'
       OR (SELECT sum(amount_cents) FROM checkout_invoice_lines
           WHERE checkout_id = v_checkout_id) <> 2620
       OR (SELECT count(*) FROM checkout_invoice_lines
           WHERE checkout_id = v_checkout_id) <> 5 THEN
        RAISE EXCEPTION 'checkout invoice should contain two merchant amounts, fees, and DC surcharge';
    END IF;
    BEGIN
        PERFORM create_unified_checkout(
            'different-customer', current_setting('shopstops.test_anchor_store_id')::uuid,
            v_order_ids, 38.91, -76.98, now(), now() + interval '2 hours',
            500, 100, 'api-checkout-idempotency'
        );
        RAISE EXCEPTION 'idempotency key reuse with different request unexpectedly succeeded';
    EXCEPTION WHEN OTHERS THEN
        GET STACKED DIAGNOSTICS v_message = MESSAGE_TEXT;
        IF v_message NOT LIKE '%idempotency key reused%' THEN
            RAISE;
        END IF;
    END;
END;
$$;

SELECT set_config('shopstops.test_checkout_id', :'checkout_id', true);
SELECT set_config('shopstops.test_anchor_order_id', :'anchor_order_id', true);
SELECT set_config('shopstops.test_anchor_store_id', :'anchor_store_id', true);
SELECT set_config('shopstops.test_anchor_merchant_id', :'anchor_merchant_id', true);
SELECT set_config('shopstops.test_partner_merchant_id', :'partner_merchant_id', true);
SELECT set_config('shopstops.test_third_merchant_id', :'third_merchant_id', true);
SELECT set_config('shopstops.test_third_store_id', :'cap_store_id', true);
SELECT set_config('shopstops.test_non_dc_checkout_id', :'non_dc_checkout_id', true);

DO $$
DECLARE
    v_checkout_id uuid := current_setting('shopstops.test_checkout_id')::uuid;
    v_anchor_order_id uuid := current_setting('shopstops.test_anchor_order_id')::uuid;
    v_third_order_id uuid;
    v_message text;
    v_first_surcharge integer;
    v_second_surcharge integer;
    v_surcharge_count integer;
BEGIN
    IF apply_dc_delivery_surcharge(
        current_setting('shopstops.test_non_dc_checkout_id')::uuid
    ) <> 0 THEN
        RAISE EXCEPTION 'non-DC checkout must not receive the surcharge';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM nearby_subscribed_merchants(
            current_setting('shopstops.test_anchor_store_id')::uuid, 500
        ) WHERE merchant_id =
            current_setting('shopstops.test_partner_merchant_id')::uuid
    ) THEN
        RAISE EXCEPTION 'subscribed nearby merchant was not discovered';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM nearby_subscribed_merchants(
            current_setting('shopstops.test_anchor_store_id')::uuid, 500
        ) WHERE merchant_id =
            current_setting('shopstops.test_third_merchant_id')::uuid
    ) THEN
        RAISE EXCEPTION 'subscription-free active merchant should be suggested';
    END IF;
    IF NOT (SELECT visible FROM merchant_storefront_visibility
        WHERE merchant_id =
            current_setting('shopstops.test_third_merchant_id')::uuid) THEN
        RAISE EXCEPTION 'subscription-free active merchant storefront should remain visible';
    END IF;
    IF (SELECT commission_rate_bps FROM checkout_merchant_financials
        WHERE checkout_id = current_setting('shopstops.test_standard_tier_checkout_id')::uuid
          AND merchant_id = current_setting('shopstops.test_third_merchant_id')::uuid) <> 1000
       OR (SELECT platform_fee_cents FROM checkout_merchant_financials
           WHERE checkout_id = current_setting('shopstops.test_standard_tier_checkout_id')::uuid
             AND merchant_id = current_setting('shopstops.test_third_merchant_id')::uuid) <> 100 THEN
        RAISE EXCEPTION 'subscription-free partner should be charged the 10 percent standard commission';
    END IF;
    IF (SELECT commission_rate_bps FROM checkout_merchant_financials
        WHERE checkout_id = v_checkout_id
          AND merchant_id = current_setting('shopstops.test_partner_merchant_id')::uuid) <> 500
       OR (SELECT platform_fee_cents FROM checkout_merchant_financials
           WHERE checkout_id = v_checkout_id
             AND merchant_id = current_setting('shopstops.test_partner_merchant_id')::uuid) <> 50 THEN
        RAISE EXCEPTION 'premium subscriber should be charged the 5 percent commission';
    END IF;
    v_first_surcharge := apply_dc_delivery_surcharge(v_checkout_id);
    v_second_surcharge := apply_dc_delivery_surcharge(v_checkout_id);
    SELECT count(*) INTO v_surcharge_count
    FROM checkout_invoice_lines
    WHERE checkout_id = v_checkout_id AND line_type = 'dc_surcharge';
    IF v_first_surcharge <> 20 OR v_second_surcharge <> 20
       OR v_surcharge_count <> 1 THEN
        RAISE EXCEPTION 'DC surcharge should be itemized exactly once (%, %, %)',
            v_first_surcharge, v_second_surcharge, v_surcharge_count;
    END IF;

    INSERT INTO orders (
        customer_ref, store_location_id, delivery_zone_id, dropoff_location,
        delivery_window_start, delivery_window_end, volume_liters, state, ready_at
    ) VALUES (
        'customer-test', current_setting('shopstops.test_third_store_id')::uuid,
        (SELECT delivery_zone_id FROM unified_checkouts WHERE id = v_checkout_id),
        ST_SetSRID(ST_MakePoint(-76.98, 38.91), 4326)::geography,
        now(), now() + interval '2 hours', 1, 'ready', now()
    ) RETURNING id INTO v_third_order_id;
    BEGIN
        INSERT INTO checkout_orders (checkout_id, order_id)
        VALUES (v_checkout_id, v_third_order_id);
        RAISE EXCEPTION 'third merchant unexpectedly accepted';
    EXCEPTION WHEN OTHERS THEN
        GET STACKED DIAGNOSTICS v_message = MESSAGE_TEXT;
        IF v_message NOT LIKE '%at most two merchants%' THEN
            RAISE;
        END IF;
    END;
END;
$$;

UPDATE unified_checkouts SET status = 'quoted'
WHERE id = :'checkout_id';
INSERT INTO checkout_financial_estimates (
    checkout_id, item_subtotal_cents, commission_cents,
    delivery_fee_cents, service_fee_cents, pass_through_cents,
    estimated_processor_fee_cents, driver_payout_cents,
    driver_base_payout_cents, along_route_pickup_bonus_cents,
    fixed_order_cost_cents, target_profit_cents,
    projected_net_profit_cents, target_margin_bps,
    stripe_fee_bps, stripe_fixed_fee_cents,
    along_route_pickup_count, typical_basket_cents
) VALUES (
    :'checkout_id', 2500, 200, 500, 100, 20, 120, 550,
    100, 450, 0, 0, 130, 0, 290, 30, 1, 4500
);
UPDATE merchants SET stripe_account_id = 'acct_anchor_test'
WHERE id = :'anchor_merchant_id';
UPDATE merchants SET stripe_account_id = 'acct_partner_test'
WHERE id = :'partner_merchant_id';
SELECT record_checkout_payment(
    :'checkout_id', 'pi_test_success', 3120, 0, 'requires_payment_method'
);
SELECT process_stripe_checkout_event(
    'evt_test_success', 'payment_intent.succeeded',
    '{"eventId":"evt_test_success","type":"payment_intent.succeeded"}',
    :'checkout_id', 'pi_test_success', 3120, 'usd', 'succeeded', 100, 'ch_test_success'
);
SELECT process_stripe_checkout_session_completed(
    'evt_checkout_session',
    '{"eventId":"evt_checkout_session","type":"checkout.session.completed"}',
    :'api_checkout_id', 'pi_test_session', 2620, 'usd', 80, 'ch_test_session'
) AS checkout_session_route_id \gset
SELECT process_stripe_checkout_session_completed(
    'evt_checkout_session',
    '{"eventId":"evt_checkout_session","type":"checkout.session.completed"}',
    :'api_checkout_id', 'pi_test_session', 2620, 'usd', 80, 'ch_test_session'
) AS checkout_session_replay_route_id \gset
SELECT set_config('shopstops.test_checkout_session_route_id',
                  :'checkout_session_route_id', true);
SELECT set_config('shopstops.test_checkout_session_replay_route_id',
                  :'checkout_session_replay_route_id', true);
DO $$
BEGIN
    IF current_setting('shopstops.test_checkout_session_route_id')::uuid <>
       current_setting('shopstops.test_checkout_session_replay_route_id')::uuid THEN
        RAISE EXCEPTION 'checkout session retry created a duplicate route';
    END IF;
    IF (SELECT event_type FROM stripe_webhook_events
        WHERE stripe_event_id = 'evt_checkout_session') <>
           'checkout.session.completed'
       OR (SELECT processed_at FROM stripe_webhook_events
           WHERE stripe_event_id = 'evt_checkout_session') IS NULL
       OR (SELECT status FROM unified_checkouts
           WHERE id = current_setting('shopstops.test_api_checkout_id')::uuid) <>
           'dispatching'
       OR (SELECT count(*) FROM unified_delivery_routes
           WHERE idempotency_key = 'stripe-checkout-session-evt_checkout_session') <> 1 THEN
        RAISE EXCEPTION 'paid checkout session did not reconcile to one active route';
    END IF;
END;
$$;
UPDATE unified_delivery_routes
SET idempotency_key = 'dispatch-session-assignment-key',
    driver_ref = 'driver-session-test', status = 'in_progress'
WHERE id = current_setting('shopstops.test_checkout_session_route_id')::uuid;
DO $$
DECLARE
    v_route_id uuid;
BEGIN
    v_route_id := process_stripe_checkout_session_completed(
        'evt_checkout_session',
        '{"eventId":"evt_checkout_session","type":"checkout.session.completed"}',
        current_setting('shopstops.test_api_checkout_id')::uuid,
        'pi_test_session', 2620, 'usd', 80, 'ch_test_session'
    );
    IF current_setting('shopstops.test_checkout_session_route_id')::uuid <> v_route_id
       OR (SELECT count(*) FROM unified_delivery_routes
           WHERE checkout_id = current_setting('shopstops.test_api_checkout_id')::uuid) <> 1
       OR (SELECT driver_ref FROM unified_delivery_routes
           WHERE checkout_id = current_setting('shopstops.test_api_checkout_id')::uuid) <>
           'driver-session-test' THEN
        RAISE EXCEPTION 'checkout session replay did not preserve the assigned route';
    END IF;
END;
$$;
SELECT process_stripe_checkout_event(
    'evt_test_success', 'payment_intent.succeeded',
    '{"eventId":"evt_test_success","type":"payment_intent.succeeded"}',
    :'checkout_id', 'pi_test_success', 3120, 'usd', 'succeeded', 100, 'ch_test_success'
);
DO $$
BEGIN
    IF (SELECT count(*) FROM dc_tax_ledger
        WHERE checkout_id = current_setting('shopstops.test_checkout_id')::uuid
          AND amount_cents = 20) <> 1 THEN
        RAISE EXCEPTION 'successful payment should post surcharge to tax ledger';
    END IF;
    IF (SELECT count(*) FROM connect_transfers
        WHERE checkout_id = current_setting('shopstops.test_checkout_id')::uuid
          AND status = 'pending'
          AND source_charge_id = 'ch_test_success') <> 2
       OR (SELECT sum(processing_fee_cents) FROM connect_transfers
           WHERE checkout_id = current_setting('shopstops.test_checkout_id')::uuid) <> 100
       OR (SELECT sum(transfer_amount_cents) FROM connect_transfers
           WHERE checkout_id = current_setting('shopstops.test_checkout_id')::uuid) <>
          (SELECT sum(item_gross_cents - platform_fee_cents) FROM connect_transfers
           WHERE checkout_id = current_setting('shopstops.test_checkout_id')::uuid)
       OR (SELECT sum(platform_fee_cents) FROM connect_transfers
           WHERE checkout_id = current_setting('shopstops.test_checkout_id')::uuid) <>
          (SELECT sum(platform_fee_cents) FROM checkout_merchant_financials
           WHERE checkout_id = current_setting('shopstops.test_checkout_id')::uuid)
       OR (SELECT bool_and(processor_fee_borne_by_platform) FROM connect_transfers
           WHERE checkout_id = current_setting('shopstops.test_checkout_id')::uuid) IS NOT TRUE
       OR (SELECT sum(platform_fee_cents) FROM connect_transfers
           WHERE checkout_id = current_setting('shopstops.test_checkout_id')::uuid) <= 0 THEN
       RAISE EXCEPTION 'successful payment should deduct the snapshotted platform commission from merchant transfers';
    END IF;
    IF (SELECT processing_fee_cents FROM connect_transfers
        WHERE checkout_id = current_setting('shopstops.test_checkout_id')::uuid
          AND merchant_id = current_setting('shopstops.test_partner_merchant_id')::uuid) <> 40
       OR (SELECT processing_fee_cents FROM connect_transfers
           WHERE checkout_id = current_setting('shopstops.test_checkout_id')::uuid
             AND merchant_id = current_setting('shopstops.test_anchor_merchant_id')::uuid) <> 60 THEN
        RAISE EXCEPTION 'merchant fee shares should be proportional to item totals';
    END IF;
END;
$$;

INSERT INTO catalog_items (
    id, store_location_id, name, description, unit_price_cents, volume_liters
)
VALUES (
    '00000000-0000-0000-0000-000000000901',
    :'partner_store_id', 'Test loaf', 'Catalog snapshot fixture', 250, 0.25
);
SELECT (now() + interval '1 hour') AS catalog_window_start,
       (now() + interval '3 hours') AS catalog_window_end \gset
SELECT set_config('shopstops.test_catalog_window_start', :'catalog_window_start', true);
SELECT set_config('shopstops.test_catalog_window_end', :'catalog_window_end', true);
SELECT create_customer_order(
    'catalog-customer', :'partner_store_id',
    38.91, -76.98, :'catalog_window_start', :'catalog_window_end',
    ARRAY['00000000-0000-0000-0000-000000000901']::uuid[],
    ARRAY[2]::integer[], 'catalog-order-test-key', 'catalog-order-fingerprint'
) AS catalog_order_id \gset
SELECT set_config('shopstops.test_catalog_order_id', :'catalog_order_id', true);
SELECT create_customer_order(
    'catalog-customer', :'partner_store_id',
    38.91, -76.98, :'catalog_window_start', :'catalog_window_end',
    ARRAY['00000000-0000-0000-0000-000000000901']::uuid[],
    ARRAY[2]::integer[], 'catalog-order-test-key', 'catalog-order-fingerprint'
) AS catalog_order_replay_id \gset
SELECT set_config('shopstops.test_catalog_order_replay_id', :'catalog_order_replay_id', true);
DO $$
DECLARE
    v_order_id uuid := current_setting('shopstops.test_catalog_order_id')::uuid;
    v_message text;
BEGIN
    IF v_order_id <> current_setting('shopstops.test_catalog_order_replay_id')::uuid
       OR (SELECT item_subtotal_cents FROM orders WHERE id = v_order_id) <> 500
       OR (SELECT sum(quantity) FROM order_line_items WHERE order_id = v_order_id) <> 2
       OR (SELECT volume_liters FROM orders WHERE id = v_order_id) <> 0.50 THEN
        RAISE EXCEPTION 'catalog order price, quantity, volume, or retry was incorrect';
    END IF;
    BEGIN
        PERFORM create_customer_order(
            'catalog-customer', current_setting('shopstops.test_partner_store_id')::uuid,
            38.91, -76.98, current_setting('shopstops.test_catalog_window_start')::timestamptz,
            current_setting('shopstops.test_catalog_window_end')::timestamptz,
            ARRAY['00000000-0000-0000-0000-000000000901']::uuid[],
            ARRAY[3]::integer[], 'catalog-order-test-key', 'different-fingerprint'
        );
        RAISE EXCEPTION 'idempotency key payload mismatch unexpectedly succeeded';
    EXCEPTION WHEN OTHERS THEN
        GET STACKED DIAGNOSTICS v_message = MESSAGE_TEXT;
        IF v_message NOT LIKE '%idempotency key conflicts%' THEN
            RAISE;
        END IF;
    END;
    UPDATE catalog_items SET unit_price_cents = 300
    WHERE id = '00000000-0000-0000-0000-000000000901';
    IF (SELECT unit_price_cents FROM order_line_items WHERE order_id = v_order_id) <> 250 THEN
        RAISE EXCEPTION 'order line did not preserve price at order creation';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM outbox_events
        WHERE aggregate_id = v_order_id AND event_type = 'order.created'
    ) THEN
        RAISE EXCEPTION 'customer order should create an outbox event';
    END IF;
END;
$$;
UPDATE connect_transfers
SET stripe_transfer_id = 'tr_test_' || merchant_id::text, status = 'transferred'
WHERE checkout_id = :'checkout_id';
SELECT id AS refund_record_id
FROM reserve_checkout_refund(
    :'checkout_id', 'admin-test', 'refund-test-key', 'requested_by_customer'
) \gset
SELECT set_config('shopstops.test_refund_id', :'refund_record_id', true);
SELECT record_checkout_refund(:'refund_record_id', 're_test', 'pending');
SELECT process_checkout_refund_event(
    'evt_refund_success', 'refund.updated',
    '{"refundIdempotencyKey":"refund-test-key"}',
    're_test', 'succeeded'
);
SELECT process_checkout_refund_event(
    'evt_refund_success', 'refund.updated',
    '{"refundIdempotencyKey":"refund-test-key"}',
    're_test', 'succeeded'
);
SELECT process_merchant_billing_event(
    'evt_setup_success', 'setup_intent.succeeded',
    '{"eventId":"evt_setup_success"}',
    :'anchor_merchant_id', 'cus_anchor', NULL, NULL, NULL, false, 'pm_anchor'
);
SELECT process_merchant_billing_event(
    'evt_subscription_created', 'customer.subscription.created',
    '{"eventId":"evt_subscription_created"}',
    :'anchor_merchant_id', 'cus_anchor', 'sub_anchor', 'active',
    now() + interval '1 month', false, NULL
);
SELECT process_merchant_billing_event(
    'evt_invoice_paid', 'invoice.payment_succeeded',
    '{"eventId":"evt_invoice_paid"}',
    :'anchor_merchant_id', 'cus_anchor', 'sub_anchor', NULL,
    now() + interval '1 month', false, NULL
);
SELECT process_merchant_billing_event(
    'evt_invoice_failed', 'invoice.payment_failed',
    '{"eventId":"evt_invoice_failed"}',
    :'anchor_merchant_id', 'cus_anchor', 'sub_anchor', NULL,
    now() + interval '1 month', true, NULL
);
UPDATE merchant_subscriptions
SET payment_failed_at = now() - interval '49 hours'
WHERE merchant_id = :'anchor_merchant_id';
SELECT process_stripe_dispute_event(
    'evt_dispute_created', 'charge.dispute.created',
    '{"eventId":"evt_dispute_created"}',
    'dp_test', :'checkout_id', 'pi_test_success', 1250,
    'product_not_received', 'needs_response', now() + interval '7 days'
);
SELECT process_stripe_dispute_event(
    'evt_dispute_won', 'charge.dispute.closed',
    '{"eventId":"evt_dispute_won"}',
    'dp_test', :'checkout_id', 'pi_test_success', 3120,
    'product_not_received', 'won', NULL
);
DO $$
BEGIN
    IF (SELECT status FROM checkout_payments
        WHERE checkout_id = current_setting('shopstops.test_checkout_id')::uuid) <> 'refunded'
       OR (SELECT status FROM dc_tax_ledger
           WHERE checkout_id = current_setting('shopstops.test_checkout_id')::uuid) <> 'refunded'
       OR (SELECT count(*) FROM connect_transfer_reversals
              WHERE checkout_refund_id =
                  current_setting('shopstops.test_refund_id')::uuid) <> 2 THEN
        RAISE EXCEPTION 'refund should update ledgers and reserve one payout reversal per merchant';
    END IF;
    IF (SELECT status FROM merchant_subscriptions
        WHERE merchant_id = current_setting('shopstops.test_anchor_merchant_id')::uuid) <> 'past_due'
       OR NOT (SELECT visible FROM merchant_storefront_visibility
           WHERE merchant_id = current_setting('shopstops.test_anchor_merchant_id')::uuid) THEN
       RAISE EXCEPTION 'subscription failure should retain standard-tier storefront visibility';
    END IF;
    IF (SELECT status FROM stripe_disputes WHERE stripe_dispute_id = 'dp_test') <> 'won' THEN
        RAISE EXCEPTION 'dispute result was not persisted';
    END IF;
    IF (SELECT sum(amount_cents) FROM connect_transfer_reversals
        WHERE stripe_dispute_id = 'dp_test') <> 1250
       OR (SELECT amount_cents FROM connect_transfer_reversals
           WHERE stripe_dispute_id = 'dp_test'
             AND merchant_id = current_setting('shopstops.test_anchor_merchant_id')::uuid) <> 734
       OR (SELECT amount_cents FROM connect_transfer_reversals
           WHERE stripe_dispute_id = 'dp_test'
             AND merchant_id = current_setting('shopstops.test_partner_merchant_id')::uuid) <> 516 THEN
        RAISE EXCEPTION 'dispute reversals should be proportional and capped at the disputed amount';
    END IF;
END;
$$;
SELECT create_unified_route(:'checkout_id', 'route-test-key', 'osrm')
    AS route_id \gset
UPDATE unified_delivery_routes
SET driver_ref = 'driver-test', status = 'in_progress'
WHERE id = :'route_id';
SELECT set_config('shopstops.test_route_id', :'route_id', true);
SELECT set_config('shopstops.test_partner_store_id', :'partner_store_id', true);
SELECT id AS route_stop_1 FROM unified_route_stops
WHERE route_id = :'route_id' AND sequence = 1 \gset
SELECT id AS route_stop_2 FROM unified_route_stops
WHERE route_id = :'route_id' AND sequence = 2 \gset
SELECT id AS route_stop_3 FROM unified_route_stops
WHERE route_id = :'route_id' AND sequence = 3 \gset
SELECT create_merchant_catalog_item(
    :'partner_merchant_id', :'partner_store_id',
    'Merchant-created item', 'Created through merchant workflow', 375, 0.75,
    'merchant-catalog-item-1', 'merchant-catalog-fingerprint-1'
) AS merchant_catalog_item_id \gset
SELECT set_config('shopstops.test_merchant_catalog_item_id', :'merchant_catalog_item_id', true);
SELECT create_merchant_catalog_item(
    :'partner_merchant_id', :'partner_store_id',
    'Merchant-created item', 'Created through merchant workflow', 375, 0.75,
    'merchant-catalog-item-1', 'merchant-catalog-fingerprint-1'
) AS merchant_catalog_item_replay_id \gset
SELECT set_config('shopstops.test_merchant_catalog_item_replay_id', :'merchant_catalog_item_replay_id', true);
DO $$
DECLARE
    v_item_id uuid := current_setting('shopstops.test_merchant_catalog_item_id')::uuid;
    v_message text;
BEGIN
    IF v_item_id <> current_setting('shopstops.test_merchant_catalog_item_replay_id')::uuid THEN
        RAISE EXCEPTION 'merchant catalog creation retry created a duplicate item';
    END IF;
    BEGIN
        PERFORM create_merchant_catalog_item(
            current_setting('shopstops.test_partner_merchant_id')::uuid,
            current_setting('shopstops.test_anchor_store_id')::uuid,
            'Not owned', '', 100, 1, 'merchant-catalog-unowned', 'fingerprint'
        );
        RAISE EXCEPTION 'merchant created catalog item for another merchant store';
    EXCEPTION WHEN OTHERS THEN
        GET STACKED DIAGNOSTICS v_message = MESSAGE_TEXT;
        IF v_message NOT LIKE '%does not belong%' THEN
            RAISE;
        END IF;
    END;
    PERFORM update_merchant_catalog_item(
        current_setting('shopstops.test_partner_merchant_id')::uuid,
        v_item_id, 'Updated item', 'Updated description', 400, 0.80, false
    );
    IF (SELECT available FROM catalog_items WHERE id = v_item_id) IS DISTINCT FROM false THEN
        RAISE EXCEPTION 'merchant catalog update did not persist availability';
    END IF;
    IF (SELECT unit_price_cents FROM order_line_items
        WHERE order_id = current_setting('shopstops.test_catalog_order_id')::uuid) <> 250 THEN
        RAISE EXCEPTION 'merchant price changes must not rewrite existing order snapshots';
    END IF;
    BEGIN
        PERFORM update_merchant_catalog_item(
            current_setting('shopstops.test_anchor_merchant_id')::uuid,
            v_item_id, 'Hijacked item', '', 1, 1, true
        );
        RAISE EXCEPTION 'different merchant updated catalog item';
    EXCEPTION WHEN OTHERS THEN
        GET STACKED DIAGNOSTICS v_message = MESSAGE_TEXT;
        IF v_message NOT LIKE '%not found for merchant%' THEN
            RAISE;
        END IF;
    END;
END;
$$;
SELECT set_config('shopstops.test_route_stop_1', :'route_stop_1', true);
SELECT set_config('shopstops.test_route_stop_2', :'route_stop_2', true);
SELECT set_config('shopstops.test_route_stop_3', :'route_stop_3', true);
UPDATE orders
SET fulfillment_mode = 'in_store_shopper',
    in_store_shopper_ref = 'shopper-test',
    in_store_shopper_claimed_at = now(),
    packed_at = now(),
    custody_pin_hash = decode(repeat('01', 32), 'hex')
WHERE id = :'anchor_order_id';
SELECT record_driver_location(
    :'route_id', 'driver-test',
    ST_SetSRID(ST_MakePoint(-76.99, 38.91), 4326)::geography,
    now(), 'gps-event-1'
);
SELECT record_driver_location(
    :'route_id', 'driver-test',
    ST_SetSRID(ST_MakePoint(-76.989, 38.91), 4326)::geography,
    now() + interval '5 seconds', 'gps-event-2'
);

DO $$
DECLARE
    v_route_id uuid := current_setting('shopstops.test_route_id')::uuid;
    v_message text;
BEGIN
    IF (SELECT count(*) FROM unified_route_stops WHERE route_id = v_route_id) <> 3
       OR (SELECT string_agg(stop_type, ',' ORDER BY sequence)
           FROM unified_route_stops WHERE route_id = v_route_id)
          <> 'pickup,pickup,dropoff'
       OR (SELECT store_location_id FROM unified_route_stops
           WHERE route_id = v_route_id AND sequence = 1)
          <> current_setting('shopstops.test_anchor_store_id')::uuid
       OR (SELECT store_location_id FROM unified_route_stops
           WHERE route_id = v_route_id AND sequence = 2)
          <> current_setting('shopstops.test_partner_store_id')::uuid THEN
        RAISE EXCEPTION 'unified route should lock anchor pickup, market pickup, dropoff';
    END IF;
    IF (SELECT count(*) FROM driver_location_samples
        WHERE route_id = v_route_id) <> 2 THEN
        RAISE EXCEPTION 'driver GPS samples were not recorded';
    END IF;

    BEGIN
        PERFORM record_driver_location(
            v_route_id, 'driver-test',
            ST_SetSRID(ST_MakePoint(-76.988, 38.91), 4326)::geography,
            now() + interval '9 seconds', 'gps-too-soon'
        );
        RAISE EXCEPTION 'too-frequent GPS sample unexpectedly accepted';
    EXCEPTION WHEN OTHERS THEN
        GET STACKED DIAGNOSTICS v_message = MESSAGE_TEXT;
        IF v_message NOT LIKE '%five seconds apart%' THEN
            RAISE;
        END IF;
    END;

    BEGIN
        UPDATE unified_route_stops SET sequence = 4
        WHERE route_id = v_route_id AND sequence = 2;
        RAISE EXCEPTION 'route stop sequence unexpectedly changed';
    EXCEPTION WHEN OTHERS THEN
        GET STACKED DIAGNOSTICS v_message = MESSAGE_TEXT;
        IF v_message NOT LIKE '%immutable%' THEN
            RAISE;
        END IF;
    END;
END;
$$;

DO $$
DECLARE
    v_route_id uuid := current_setting('shopstops.test_route_id')::uuid;
    v_stop_1 uuid := current_setting('shopstops.test_route_stop_1')::uuid;
    v_stop_2 uuid := current_setting('shopstops.test_route_stop_2')::uuid;
    v_stop_3 uuid := current_setting('shopstops.test_route_stop_3')::uuid;
    v_message text;
    v_status text;
BEGIN
    BEGIN
        PERFORM record_unified_route_stop_event(
            v_route_id, v_stop_2, 'driver-test', 'stop-out-of-order',
            'arrive', now()
        );
        RAISE EXCEPTION 'out-of-order stop event unexpectedly accepted';
    EXCEPTION WHEN OTHERS THEN
        GET STACKED DIAGNOSTICS v_message = MESSAGE_TEXT;
        IF v_message NOT LIKE '%in sequence%' THEN
            RAISE;
        END IF;
    END;

    SELECT stop_status INTO v_status FROM record_unified_route_stop_event(
        v_route_id, v_stop_1, 'driver-test', 'stop-arrive-1', 'arrive', now()
    );
    IF v_status <> 'arrived' THEN
        RAISE EXCEPTION 'driver arrival did not update stop status';
    END IF;
    SELECT stop_status INTO v_status FROM record_unified_route_stop_event(
        v_route_id, v_stop_1, 'driver-test', 'stop-arrive-1', 'arrive', now()
    );
    IF v_status <> 'arrived' THEN
        RAISE EXCEPTION 'identical stop event retry was not idempotent';
    END IF;
    BEGIN
        PERFORM record_unified_route_stop_event(
            v_route_id, v_stop_1, 'driver-test', 'stop-arrive-1', 'complete', now()
        );
        RAISE EXCEPTION 'event ID reuse with a different payload unexpectedly succeeded';
    EXCEPTION WHEN OTHERS THEN
        GET STACKED DIAGNOSTICS v_message = MESSAGE_TEXT;
        IF v_message NOT LIKE '%already used%' THEN
            RAISE;
        END IF;
    END;
    BEGIN
        PERFORM record_unified_route_stop_event(
            v_route_id, v_stop_1, 'driver-test', 'stop-complete-before-custody',
            'complete', now()
        );
        RAISE EXCEPTION 'in-store pickup completed without custody verification';
    EXCEPTION WHEN OTHERS THEN
        GET STACKED DIAGNOSTICS v_message = MESSAGE_TEXT;
        IF v_message NOT LIKE '%custody must be verified%' THEN
            RAISE;
        END IF;
    END;
    UPDATE orders
    SET custody_verified_at = now(), custody_pin_attempts = 1
    WHERE id = current_setting('shopstops.test_anchor_order_id')::uuid;
    PERFORM record_unified_route_stop_event(
        v_route_id, v_stop_1, 'driver-test', 'stop-complete-1', 'complete', now()
    );
    PERFORM record_unified_route_stop_event(
        v_route_id, v_stop_2, 'driver-test', 'stop-arrive-2', 'arrive', now()
    );
    PERFORM record_unified_route_stop_event(
        v_route_id, v_stop_2, 'driver-test', 'stop-complete-2', 'complete', now()
    );
    PERFORM record_unified_route_stop_event(
        v_route_id, v_stop_3, 'driver-test', 'stop-arrive-3', 'arrive', now()
    );
    PERFORM record_unified_route_stop_event(
        v_route_id, v_stop_3, 'driver-test', 'stop-complete-3', 'complete', now()
    );
    IF (SELECT status FROM unified_delivery_routes WHERE id = v_route_id) <> 'completed'
       OR (SELECT status FROM unified_checkouts
           WHERE id = (SELECT checkout_id FROM unified_delivery_routes WHERE id = v_route_id))
          <> 'completed'
       OR (SELECT count(*) FROM unified_route_stop_events WHERE route_id = v_route_id) <> 6 THEN
        RAISE EXCEPTION 'complete stop sequence should finish route and checkout exactly once';
    END IF;
    IF (SELECT amount_cents FROM driver_route_earnings
        WHERE route_id = v_route_id) <> 550
       OR (SELECT base_payout_cents FROM driver_route_earnings
           WHERE route_id = v_route_id) <> 100
       OR (SELECT along_route_bonus_cents FROM driver_route_earnings
           WHERE route_id = v_route_id) <> 450
       OR (SELECT along_route_pickup_count FROM driver_route_earnings
           WHERE route_id = v_route_id) <> 1 THEN
        RAISE EXCEPTION 'completed route did not settle its base and along-route bonus snapshot';
    END IF;
END;
$$;

UPDATE orders SET state = 'preparing'
WHERE id = :'anchor_order_id';
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM outbox_events
        WHERE aggregate_id = current_setting('shopstops.test_anchor_order_id')::uuid
          AND event_type = 'order.state.changed'
    ) THEN
        RAISE EXCEPTION 'order state changes should publish an outbox event';
    END IF;
END;
$$;

INSERT INTO unified_checkouts (
    idempotency_key, customer_ref, anchor_store_location_id, delivery_zone_id,
    dropoff_location, delivery_window_start, delivery_window_end
)
VALUES (
    'merchant-fulfillment-test', 'merchant-fulfillment-customer',
    :'anchor_store_id', :'zone_id',
    ST_SetSRID(ST_MakePoint(-76.98, 38.91), 4326)::geography,
    now(), now() + interval '2 hours'
)
RETURNING id AS fulfillment_checkout_id \gset

INSERT INTO orders (
    customer_ref, store_location_id, delivery_zone_id, dropoff_location,
    delivery_window_start, delivery_window_end, volume_liters, state
)
VALUES (
    'merchant-fulfillment-customer', :'anchor_store_id', :'zone_id',
    ST_SetSRID(ST_MakePoint(-76.98, 38.91), 4326)::geography,
    now(), now() + interval '2 hours', 1, 'placed'
)
RETURNING id AS fulfillment_order_id \gset

INSERT INTO checkout_orders (checkout_id, order_id)
VALUES (:'fulfillment_checkout_id', :'fulfillment_order_id');
UPDATE unified_checkouts SET status = 'paid'
WHERE id = :'fulfillment_checkout_id';
SELECT set_config('shopstops.test_fulfillment_order_id', :'fulfillment_order_id', true);
SELECT set_config('shopstops.test_fulfillment_merchant_id', :'anchor_merchant_id', true);

DO $$
DECLARE
    v_order_id uuid := current_setting('shopstops.test_fulfillment_order_id')::uuid;
    v_merchant_id uuid := current_setting('shopstops.test_fulfillment_merchant_id')::uuid;
    v_state text;
    v_message text;
BEGIN
    v_state := set_merchant_order_state(v_merchant_id, v_order_id, 'preparing');
    IF v_state <> 'preparing'
       OR (SELECT state FROM orders WHERE id = v_order_id) <> 'preparing' THEN
        RAISE EXCEPTION 'merchant could not accept paid order for preparation';
    END IF;
    v_state := set_merchant_order_state(v_merchant_id, v_order_id, 'ready');
    IF v_state <> 'ready'
       OR (SELECT ready_at FROM orders WHERE id = v_order_id) IS NULL THEN
        RAISE EXCEPTION 'merchant could not mark order ready';
    END IF;
    IF set_merchant_order_state(v_merchant_id, v_order_id, 'ready') <> 'ready' THEN
        RAISE EXCEPTION 'repeating current order state was not idempotent';
    END IF;
    BEGIN
        PERFORM set_merchant_order_state(v_merchant_id, v_order_id, 'preparing');
        RAISE EXCEPTION 'backwards merchant order transition unexpectedly succeeded';
    EXCEPTION WHEN OTHERS THEN
        GET STACKED DIAGNOSTICS v_message = MESSAGE_TEXT;
        IF v_message NOT LIKE '%invalid merchant order state transition%' THEN
            RAISE;
        END IF;
    END;
END;
$$;
