CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE delivery_zones (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    boundary geometry(MultiPolygon, 4326) NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX delivery_zones_boundary_gix ON delivery_zones USING gist (boundary);

CREATE TABLE store_locations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    address text NOT NULL,
    location geography(Point, 4326) NOT NULL,
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX store_locations_location_gix ON store_locations USING gist (location);

CREATE TABLE orders (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_ref text NOT NULL,
    store_location_id uuid NOT NULL REFERENCES store_locations(id),
    delivery_zone_id uuid NOT NULL REFERENCES delivery_zones(id),
    dropoff_location geography(Point, 4326) NOT NULL,
    delivery_window_start timestamptz NOT NULL,
    delivery_window_end timestamptz NOT NULL,
    volume_liters numeric(10, 2) NOT NULL CHECK (volume_liters > 0),
    state text NOT NULL DEFAULT 'placed'
        CHECK (state IN ('placed', 'preparing', 'ready', 'bundled',
                         'picked_up', 'delivered', 'cancelled')),
    ready_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (delivery_window_end > delivery_window_start),
    CHECK ((state = 'ready' AND ready_at IS NOT NULL)
        OR state <> 'ready')
);
CREATE INDEX orders_dropoff_location_gix ON orders USING gist (dropoff_location);
CREATE INDEX orders_ready_for_dispatch_idx
    ON orders (delivery_zone_id, delivery_window_start, created_at)
    WHERE state = 'ready';

CREATE TABLE delivery_bundles (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    delivery_zone_id uuid NOT NULL REFERENCES delivery_zones(id),
    status text NOT NULL CHECK (status IN
        ('active', 'in_progress', 'completed', 'cancelled')),
    driver_ref text,
    capacity_liters numeric(10, 2) NOT NULL CHECK (capacity_liters > 0),
    max_orders integer NOT NULL CHECK (max_orders > 0),
    idempotency_key text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    activated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE bundle_orders (
    bundle_id uuid NOT NULL REFERENCES delivery_bundles(id) ON DELETE CASCADE,
    order_id uuid NOT NULL UNIQUE REFERENCES orders(id),
    bundle_position integer NOT NULL CHECK (bundle_position > 0),
    PRIMARY KEY (bundle_id, order_id),
    UNIQUE (bundle_id, bundle_position)
);

CREATE TABLE delivery_stops (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    bundle_id uuid NOT NULL,
    order_id uuid NOT NULL,
    sequence integer NOT NULL CHECK (sequence > 0),
    stop_type text NOT NULL CHECK (stop_type IN ('pickup', 'dropoff')),
    store_location_id uuid REFERENCES store_locations(id),
    location geography(Point, 4326) NOT NULL,
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'arrived', 'completed', 'skipped')),
    created_at timestamptz NOT NULL DEFAULT now(),
    arrived_at timestamptz,
    completed_at timestamptz,
    FOREIGN KEY (bundle_id, order_id)
        REFERENCES bundle_orders(bundle_id, order_id) ON DELETE CASCADE,
    UNIQUE (bundle_id, sequence),
    UNIQUE (bundle_id, order_id, stop_type),
    CHECK ((stop_type = 'pickup' AND store_location_id IS NOT NULL)
        OR (stop_type = 'dropoff' AND store_location_id IS NULL)),
    CHECK ((status IN ('arrived', 'completed') AND arrived_at IS NOT NULL)
        OR status NOT IN ('arrived', 'completed')),
    CHECK ((status = 'completed' AND completed_at IS NOT NULL)
        OR status <> 'completed')
);
CREATE INDEX delivery_stops_bundle_queue_idx
    ON delivery_stops (bundle_id, sequence);
CREATE INDEX delivery_stops_location_gix
    ON delivery_stops USING gist (location);

CREATE TABLE delivery_stop_events (
    event_id text PRIMARY KEY,
    stop_id uuid NOT NULL REFERENCES delivery_stops(id) ON DELETE CASCADE,
    driver_ref text NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('arrived', 'completed', 'skipped')),
    observed_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    payload jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX delivery_stop_events_stop_idx
    ON delivery_stop_events (stop_id, received_at);

CREATE TABLE outbox_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    aggregate_type text NOT NULL,
    aggregate_id uuid NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);
CREATE INDEX outbox_events_unpublished_idx
    ON outbox_events (id) WHERE published_at IS NULL;

CREATE FUNCTION create_proximity_bundle(
    p_seed_order_id uuid,
    p_idempotency_key text,
    p_capacity_liters numeric,
    p_max_orders integer DEFAULT 3,
    p_pickup_radius_m integer DEFAULT 2000,
    p_dropoff_radius_m integer DEFAULT 2000
) RETURNS uuid
LANGUAGE plpgsql
AS $$
DECLARE
    v_bundle_id uuid;
    v_zone_id uuid;
    v_seed orders%ROWTYPE;
    v_seed_store store_locations%ROWTYPE;
    v_candidate orders%ROWTYPE;
    v_order_ids uuid[];
    v_order_id uuid;
    v_picked_ids uuid[] := ARRAY[]::uuid[];
    v_dropped_ids uuid[] := ARRAY[]::uuid[];
    v_current_location geography(Point, 4326);
    v_next_location geography(Point, 4326);
    v_next_order_id uuid;
    v_next_store_id uuid;
    v_next_type text;
    v_sequence integer := 0;
    v_position integer := 0;
    v_total_volume numeric(10, 2) := 0;
BEGIN
    IF p_idempotency_key IS NULL OR btrim(p_idempotency_key) = '' THEN
        RAISE EXCEPTION 'idempotency key is required';
    END IF;
    IF p_seed_order_id IS NULL OR p_capacity_liters IS NULL OR p_max_orders IS NULL
       OR p_pickup_radius_m IS NULL OR p_dropoff_radius_m IS NULL THEN
        RAISE EXCEPTION 'seed order, capacity, limits, and radii are required';
    END IF;
    IF p_capacity_liters <= 0 OR p_max_orders <= 0
       OR p_pickup_radius_m < 0 OR p_dropoff_radius_m < 0 THEN
        RAISE EXCEPTION 'capacity, order limit, and radii must be valid';
    END IF;

    PERFORM pg_advisory_xact_lock(hashtextextended(p_idempotency_key, 0));
    SELECT id INTO v_bundle_id
    FROM delivery_bundles
    WHERE idempotency_key = p_idempotency_key;
    IF FOUND THEN
        RETURN v_bundle_id;
    END IF;

    SELECT * INTO v_seed
    FROM orders
    WHERE id = p_seed_order_id
    FOR UPDATE SKIP LOCKED;
    IF NOT FOUND OR v_seed.state <> 'ready' THEN
        RAISE EXCEPTION 'seed order % is not available for dispatch', p_seed_order_id;
    END IF;
    IF v_seed.volume_liters > p_capacity_liters THEN
        RAISE EXCEPTION 'seed order exceeds driver capacity';
    END IF;

    SELECT * INTO v_seed_store
    FROM store_locations
    WHERE id = v_seed.store_location_id AND active
    FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'seed order store is not active';
    END IF;

    SELECT z.id INTO v_zone_id
    FROM delivery_zones z
    WHERE z.id = v_seed.delivery_zone_id
      AND z.enabled
      AND ST_Covers(z.boundary, v_seed_store.location::geometry)
      AND ST_Covers(z.boundary, v_seed.dropoff_location::geometry);
    IF NOT FOUND THEN
        RAISE EXCEPTION 'seed order locations are outside an enabled delivery zone';
    END IF;

    v_order_ids := ARRAY[p_seed_order_id];
    v_total_volume := v_seed.volume_liters;

    IF cardinality(v_order_ids) < p_max_orders THEN
        FOR v_candidate IN
            SELECT o.*
            FROM orders o
            JOIN store_locations s ON s.id = o.store_location_id AND s.active
            JOIN delivery_zones z ON z.id = o.delivery_zone_id AND z.enabled
            WHERE o.id <> p_seed_order_id
              AND o.delivery_zone_id = v_zone_id
              AND o.state = 'ready'
              AND o.delivery_window_start <= v_seed.delivery_window_end
              AND o.delivery_window_end >= v_seed.delivery_window_start
              AND ST_Covers(z.boundary, s.location::geometry)
              AND ST_Covers(z.boundary, o.dropoff_location::geometry)
              AND ST_DWithin(s.location, v_seed_store.location, p_pickup_radius_m)
              AND ST_DWithin(o.dropoff_location, v_seed.dropoff_location, p_dropoff_radius_m)
              AND NOT EXISTS (
                  SELECT 1 FROM bundle_orders bo WHERE bo.order_id = o.id
              )
            ORDER BY ST_Distance(s.location, v_seed_store.location),
                     ST_Distance(o.dropoff_location, v_seed.dropoff_location),
                     o.created_at, o.id
            FOR UPDATE OF o SKIP LOCKED
        LOOP
            IF v_total_volume + v_candidate.volume_liters <= p_capacity_liters THEN
                v_order_ids := array_append(v_order_ids, v_candidate.id);
                v_total_volume := v_total_volume + v_candidate.volume_liters;
                EXIT WHEN cardinality(v_order_ids) >= p_max_orders;
            END IF;
        END LOOP;
    END IF;

    INSERT INTO delivery_bundles (
        delivery_zone_id, status, capacity_liters, max_orders, idempotency_key
    ) VALUES (
        v_zone_id, 'active', p_capacity_liters, p_max_orders, p_idempotency_key
    ) RETURNING id INTO v_bundle_id;

    FOREACH v_order_id IN ARRAY v_order_ids LOOP
        v_position := v_position + 1;
        INSERT INTO bundle_orders (bundle_id, order_id, bundle_position)
        VALUES (v_bundle_id, v_order_id, v_position);
        UPDATE orders
        SET state = 'bundled', updated_at = now()
        WHERE id = v_order_id;
    END LOOP;

    v_current_location := v_seed_store.location;
    WHILE cardinality(v_dropped_ids) < cardinality(v_order_ids) LOOP
        SELECT task.order_id, task.store_location_id, task.stop_type, task.location
        INTO v_next_order_id, v_next_store_id, v_next_type, v_next_location
        FROM (
            SELECT o.id AS order_id, s.id AS store_location_id,
                   'pickup'::text AS stop_type, s.location
            FROM orders o
            JOIN store_locations s ON s.id = o.store_location_id
            WHERE o.id = ANY(v_order_ids)
              AND NOT (o.id = ANY(v_picked_ids))
            UNION ALL
            SELECT o.id, NULL::uuid, 'dropoff'::text, o.dropoff_location
            FROM orders o
            WHERE o.id = ANY(v_order_ids)
              AND o.id = ANY(v_picked_ids)
              AND NOT (o.id = ANY(v_dropped_ids))
        ) AS task
        ORDER BY ST_Distance(task.location, v_current_location), task.order_id,
                 task.stop_type
        LIMIT 1;

        v_sequence := v_sequence + 1;
        INSERT INTO delivery_stops (
            bundle_id, order_id, sequence, stop_type, store_location_id, location
        ) VALUES (
            v_bundle_id, v_next_order_id, v_sequence, v_next_type,
            v_next_store_id, v_next_location
        );
        v_current_location := v_next_location;
        IF v_next_type = 'pickup' THEN
            v_picked_ids := array_append(v_picked_ids, v_next_order_id);
        ELSE
            v_dropped_ids := array_append(v_dropped_ids, v_next_order_id);
        END IF;
    END LOOP;

    INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
    VALUES (
        'delivery_bundle', v_bundle_id, 'delivery.bundle.activated',
        jsonb_build_object(
            'bundleId', v_bundle_id,
            'orderIds', to_jsonb(v_order_ids),
            'stopCount', v_sequence
        )
    );

    RETURN v_bundle_id;
END;
$$;
