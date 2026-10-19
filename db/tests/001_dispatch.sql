\set ON_ERROR_STOP on

BEGIN;

INSERT INTO delivery_zones (name, boundary)
VALUES ('test zone', ST_Multi(ST_MakeEnvelope(-74.1, 40.6, -73.8, 40.9, 4326)))
RETURNING id AS zone_id \gset
INSERT INTO delivery_zones (name, boundary)
VALUES ('capacity test zone', ST_Multi(ST_MakeEnvelope(-74.1, 40.6, -73.8, 40.9, 4326)))
RETURNING id AS capacity_zone_id \gset

INSERT INTO store_locations (name, address, location)
VALUES
    ('seed store', '1 Test Way',
     ST_SetSRID(ST_MakePoint(-73.98, 40.75), 4326)::geography),
    ('nearby store', '2 Test Way',
     ST_SetSRID(ST_MakePoint(-73.981, 40.75), 4326)::geography),
    ('distant store', '3 Test Way',
     ST_SetSRID(ST_MakePoint(-73.9, 40.75), 4326)::geography);

SELECT id AS seed_store_id FROM store_locations WHERE name = 'seed store' \gset
SELECT id AS nearby_store_id FROM store_locations WHERE name = 'nearby store' \gset
SELECT id AS distant_store_id FROM store_locations WHERE name = 'distant store' \gset

INSERT INTO orders (
    customer_ref, store_location_id, delivery_zone_id, dropoff_location,
    delivery_window_start, delivery_window_end, volume_liters, state, ready_at
)
VALUES (
    'seed-customer', :'seed_store_id', :'zone_id',
    ST_SetSRID(ST_MakePoint(-73.97, 40.75), 4326)::geography,
    now(), now() + interval '2 hours', 2, 'ready', now()
)
RETURNING id AS seed_order_id \gset

INSERT INTO orders (
    customer_ref, store_location_id, delivery_zone_id, dropoff_location,
    delivery_window_start, delivery_window_end, volume_liters, state, ready_at
)
VALUES
    ('near-customer', :'nearby_store_id', :'zone_id',
     ST_SetSRID(ST_MakePoint(-73.971, 40.75), 4326)::geography,
     now(), now() + interval '2 hours', 3, 'ready', now()),
    ('far-customer', :'distant_store_id', :'zone_id',
     ST_SetSRID(ST_MakePoint(-73.9, 40.75), 4326)::geography,
     now(), now() + interval '2 hours', 1, 'ready', now()),
    ('late-customer', :'nearby_store_id', :'zone_id',
     ST_SetSRID(ST_MakePoint(-73.971, 40.75), 4326)::geography,
     now() + interval '4 hours', now() + interval '6 hours', 1, 'ready', now());

INSERT INTO orders (
    customer_ref, store_location_id, delivery_zone_id, dropoff_location,
    delivery_window_start, delivery_window_end, volume_liters, state, ready_at
)
VALUES (
    'capacity-seed', :'seed_store_id', :'capacity_zone_id',
    ST_SetSRID(ST_MakePoint(-73.97, 40.75), 4326)::geography,
    now(), now() + interval '2 hours', 2, 'ready', now()
)
RETURNING id AS capacity_seed_order_id \gset

INSERT INTO orders (
    customer_ref, store_location_id, delivery_zone_id, dropoff_location,
    delivery_window_start, delivery_window_end, volume_liters, state, ready_at
)
VALUES (
    'capacity-candidate', :'nearby_store_id', :'capacity_zone_id',
    ST_SetSRID(ST_MakePoint(-73.971, 40.75), 4326)::geography,
    now(), now() + interval '2 hours', 3, 'ready', now()
);

SELECT id AS far_order_id FROM orders WHERE customer_ref = 'far-customer' \gset
SELECT id AS capacity_candidate_id
FROM orders WHERE customer_ref = 'capacity-candidate' \gset
SELECT create_proximity_bundle(:'seed_order_id', 'dispatch-test-key', 10)
    AS bundle_id \gset
SELECT create_proximity_bundle(:'seed_order_id', 'dispatch-test-key', 10)
    AS replay_bundle_id \gset
SELECT create_proximity_bundle(:'far_order_id', 'single-order-test-key', 1, 1)
    AS single_order_bundle_id \gset
SELECT create_proximity_bundle(
    :'capacity_seed_order_id', 'capacity-test-key', 4
) AS capacity_bundle_id \gset
SELECT set_config('shopstops.test_bundle_id', :'bundle_id', true);
SELECT set_config('shopstops.test_replay_bundle_id', :'replay_bundle_id', true);
SELECT set_config('shopstops.test_seed_order_id', :'seed_order_id', true);
SELECT set_config('shopstops.test_single_order_bundle_id',
                  :'single_order_bundle_id', true);
SELECT set_config('shopstops.test_capacity_bundle_id',
                  :'capacity_bundle_id', true);
SELECT set_config('shopstops.test_capacity_candidate_id',
                  :'capacity_candidate_id', true);

DO $$
DECLARE
    v_bundle_id uuid := current_setting('shopstops.test_bundle_id')::uuid;
    v_replay_bundle_id uuid :=
        current_setting('shopstops.test_replay_bundle_id')::uuid;
    v_seed_id uuid := current_setting('shopstops.test_seed_order_id')::uuid;
    v_single_order_bundle_id uuid :=
        current_setting('shopstops.test_single_order_bundle_id')::uuid;
    v_capacity_bundle_id uuid :=
        current_setting('shopstops.test_capacity_bundle_id')::uuid;
    v_capacity_candidate_id uuid :=
        current_setting('shopstops.test_capacity_candidate_id')::uuid;
BEGIN
    IF v_bundle_id <> v_replay_bundle_id THEN
        RAISE EXCEPTION 'idempotent dispatch returned a different bundle';
    END IF;
    IF (SELECT count(*) FROM bundle_orders WHERE bundle_id = v_bundle_id) <> 2 THEN
        RAISE EXCEPTION 'bundle should contain only nearby, overlapping orders';
    END IF;
    IF (SELECT count(*) FROM delivery_stops WHERE bundle_id = v_bundle_id) <> 4 THEN
        RAISE EXCEPTION 'each bundled order should have a pickup and drop-off stop';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM delivery_stops pickup
        JOIN delivery_stops dropoff
          ON dropoff.bundle_id = pickup.bundle_id
         AND dropoff.order_id = pickup.order_id
        WHERE pickup.bundle_id = v_bundle_id
          AND pickup.stop_type = 'pickup'
          AND dropoff.stop_type = 'dropoff'
          AND pickup.sequence >= dropoff.sequence
    ) THEN
        RAISE EXCEPTION 'pickup must precede its order drop-off';
    END IF;
    IF (SELECT state FROM orders WHERE id = v_seed_id) <> 'bundled' THEN
        RAISE EXCEPTION 'seed order was not reserved';
    END IF;
    IF (SELECT count(*) FROM orders WHERE state = 'ready') <> 2 THEN
        RAISE EXCEPTION 'non-overlapping and over-capacity orders should remain ready';
    END IF;
    IF (SELECT count(*) FROM outbox_events
        WHERE aggregate_id = v_bundle_id
          AND event_type = 'delivery.bundle.activated') <> 1 THEN
        RAISE EXCEPTION 'bundle activation should emit exactly one event';
    END IF;
    IF (SELECT count(*) FROM bundle_orders
        WHERE bundle_id = v_single_order_bundle_id) <> 1 THEN
        RAISE EXCEPTION 'max_orders=1 should create a single-order bundle';
    END IF;
    IF (SELECT count(*) FROM bundle_orders
        WHERE bundle_id = v_capacity_bundle_id) <> 1 THEN
        RAISE EXCEPTION 'order exceeding the remaining capacity must be excluded';
    END IF;
    IF (SELECT state FROM orders WHERE id = v_capacity_candidate_id) <> 'ready' THEN
        RAISE EXCEPTION 'over-capacity candidate should remain ready';
    END IF;
END;
$$;

ROLLBACK;
