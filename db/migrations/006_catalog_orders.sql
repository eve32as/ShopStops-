CREATE TABLE catalog_items (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    store_location_id uuid NOT NULL REFERENCES store_locations(id),
    name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 200),
    description text NOT NULL DEFAULT '' CHECK (length(description) <= 1000),
    unit_price_cents integer NOT NULL CHECK (unit_price_cents > 0),
    volume_liters numeric(10, 2) NOT NULL CHECK (volume_liters > 0),
    available boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (store_location_id, id)
);
CREATE INDEX catalog_items_store_available_idx
    ON catalog_items (store_location_id, name, id) WHERE available;

CREATE TABLE order_line_items (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    catalog_item_id uuid NOT NULL REFERENCES catalog_items(id),
    item_name text NOT NULL,
    quantity integer NOT NULL CHECK (quantity BETWEEN 1 AND 99),
    unit_price_cents integer NOT NULL CHECK (unit_price_cents > 0),
    volume_liters numeric(10, 2) NOT NULL CHECK (volume_liters > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (order_id, catalog_item_id)
);
CREATE INDEX order_line_items_order_idx ON order_line_items (order_id);

ALTER TABLE orders
    ADD COLUMN creation_idempotency_key text UNIQUE,
    ADD COLUMN creation_fingerprint text;
ALTER TABLE orders
    ADD CONSTRAINT orders_creation_fingerprint_pair CHECK (
        (creation_idempotency_key IS NULL AND creation_fingerprint IS NULL)
        OR (creation_idempotency_key IS NOT NULL AND creation_fingerprint IS NOT NULL)
    );

CREATE FUNCTION create_customer_order(
    p_customer_ref text,
    p_store_location_id uuid,
    p_dropoff_latitude double precision,
    p_dropoff_longitude double precision,
    p_window_start timestamptz,
    p_window_end timestamptz,
    p_item_ids uuid[],
    p_quantities integer[],
    p_idempotency_key text,
    p_request_fingerprint text
) RETURNS uuid
LANGUAGE plpgsql
AS $$
DECLARE
    v_order_id uuid;
    v_existing_customer text;
    v_existing_fingerprint text;
    v_zone_id uuid;
    v_item catalog_items%ROWTYPE;
    v_quantity integer;
    v_index integer;
    v_volume numeric := 0;
    v_subtotal bigint := 0;
BEGIN
    IF p_customer_ref IS NULL OR btrim(p_customer_ref) = ''
       OR length(p_customer_ref) > 200
       OR p_store_location_id IS NULL
       OR p_dropoff_latitude IS NULL OR p_dropoff_longitude IS NULL
       OR p_dropoff_latitude NOT BETWEEN -90 AND 90
       OR p_dropoff_longitude NOT BETWEEN -180 AND 180
       OR p_window_start IS NULL OR p_window_end IS NULL
       OR p_window_end <= p_window_start
       OR p_item_ids IS NULL OR p_quantities IS NULL
       OR cardinality(p_item_ids) < 1 OR cardinality(p_item_ids) > 50
       OR cardinality(p_item_ids) <> cardinality(p_quantities)
       OR p_idempotency_key IS NULL OR btrim(p_idempotency_key) = ''
       OR length(p_idempotency_key) > 200
       OR p_request_fingerprint IS NULL OR btrim(p_request_fingerprint) = '' THEN
        RAISE EXCEPTION 'customer order fields are invalid';
    END IF;
    IF (SELECT count(DISTINCT item_id) FROM unnest(p_item_ids) AS requested(item_id))
       <> cardinality(p_item_ids) THEN
        RAISE EXCEPTION 'catalog item IDs must be unique';
    END IF;
    IF EXISTS (
        SELECT 1 FROM unnest(p_quantities) AS requested(quantity)
        WHERE quantity IS NULL OR quantity NOT BETWEEN 1 AND 99
    ) THEN
        RAISE EXCEPTION 'item quantity must be between 1 and 99';
    END IF;

    PERFORM pg_advisory_xact_lock(hashtextextended(p_idempotency_key, 0));
    SELECT id, customer_ref, creation_fingerprint
    INTO v_order_id, v_existing_customer, v_existing_fingerprint
    FROM orders
    WHERE creation_idempotency_key = p_idempotency_key
    FOR UPDATE;
    IF FOUND THEN
        IF v_existing_customer <> p_customer_ref
           OR v_existing_fingerprint <> p_request_fingerprint THEN
            RAISE EXCEPTION 'idempotency key conflicts with a different customer order';
        END IF;
        RETURN v_order_id;
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM store_locations s
        JOIN merchants m ON m.id = s.merchant_id AND m.active
        JOIN merchant_storefront_visibility visibility
          ON visibility.merchant_id = m.id AND visibility.visible
        WHERE s.id = p_store_location_id AND s.active
    ) THEN
        RAISE EXCEPTION 'storefront is unavailable' USING ERRCODE = 'P0002';
    END IF;

    SELECT z.id INTO v_zone_id
    FROM delivery_zones z
    JOIN store_locations s ON s.id = p_store_location_id
    WHERE z.enabled
      AND ST_Covers(z.boundary, s.location::geometry)
      AND ST_Covers(
          z.boundary,
          ST_SetSRID(ST_MakePoint(p_dropoff_longitude, p_dropoff_latitude), 4326)
      )
    ORDER BY z.id
    LIMIT 1;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'store or drop-off is outside an enabled delivery zone'
            USING ERRCODE = 'P0002';
    END IF;

    FOR v_index IN 1..cardinality(p_item_ids) LOOP
        SELECT * INTO v_item
        FROM catalog_items
        WHERE id = p_item_ids[v_index]
          AND store_location_id = p_store_location_id
          AND available
        FOR SHARE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'catalog item is unavailable' USING ERRCODE = 'P0002';
        END IF;
        v_quantity := p_quantities[v_index];
        v_volume := v_volume + v_item.volume_liters * v_quantity;
        v_subtotal := v_subtotal + v_item.unit_price_cents::bigint * v_quantity;
        IF v_subtotal > 2147483647 OR v_volume > 99999999.99 THEN
            RAISE EXCEPTION 'order total or volume exceeds supported limits';
        END IF;
    END LOOP;

    INSERT INTO orders (
        customer_ref, store_location_id, delivery_zone_id, dropoff_location,
        delivery_window_start, delivery_window_end, volume_liters,
        item_subtotal_cents, creation_idempotency_key, creation_fingerprint
    ) VALUES (
        p_customer_ref, p_store_location_id, v_zone_id,
        ST_SetSRID(ST_MakePoint(p_dropoff_longitude, p_dropoff_latitude), 4326)::geography,
        p_window_start, p_window_end, v_volume, v_subtotal::integer,
        p_idempotency_key, p_request_fingerprint
    ) RETURNING id INTO v_order_id;

    FOR v_index IN 1..cardinality(p_item_ids) LOOP
        SELECT * INTO v_item FROM catalog_items WHERE id = p_item_ids[v_index];
        INSERT INTO order_line_items (
            order_id, catalog_item_id, item_name, quantity,
            unit_price_cents, volume_liters
        ) VALUES (
            v_order_id, v_item.id, v_item.name, p_quantities[v_index],
            v_item.unit_price_cents, v_item.volume_liters
        );
    END LOOP;

    INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
    VALUES (
        'order', v_order_id, 'order.created',
        jsonb_build_object('orderId', v_order_id, 'customerRef', p_customer_ref,
                           'storeLocationId', p_store_location_id)
    );
    RETURN v_order_id;
END;
$$;
