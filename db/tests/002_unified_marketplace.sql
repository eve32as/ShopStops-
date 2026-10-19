\set ON_ERROR_STOP on

BEGIN;

INSERT INTO delivery_zones (name, boundary)
VALUES ('Union Market test zone',
        ST_Multi(ST_MakeEnvelope(-77.05, 38.85, -76.95, 38.95, 4326)))
RETURNING id AS zone_id \gset
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
    IF EXISTS (
        SELECT 1 FROM nearby_subscribed_merchants(
            current_setting('shopstops.test_anchor_store_id')::uuid, 500
        ) WHERE merchant_id =
            current_setting('shopstops.test_third_merchant_id')::uuid
    ) THEN
        RAISE EXCEPTION 'merchant with a failed subscription should not be suggested';
    END IF;
    IF (SELECT visible FROM merchant_storefront_visibility
        WHERE merchant_id =
            current_setting('shopstops.test_third_merchant_id')::uuid) THEN
        RAISE EXCEPTION 'storefront should hide 48 hours after subscription failure';
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
           WHERE checkout_id = current_setting('shopstops.test_checkout_id')::uuid) <> 2400 THEN
        RAISE EXCEPTION 'successful payment should create idempotent merchant transfers with allocated fees';
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
