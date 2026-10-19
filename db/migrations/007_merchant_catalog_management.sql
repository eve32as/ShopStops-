ALTER TABLE catalog_items
    ADD COLUMN creation_idempotency_key text,
    ADD COLUMN creation_fingerprint text,
    ADD CONSTRAINT catalog_items_creation_key_unique UNIQUE (creation_idempotency_key),
    ADD CONSTRAINT catalog_items_creation_fingerprint_pair CHECK (
        (creation_idempotency_key IS NULL AND creation_fingerprint IS NULL)
        OR (creation_idempotency_key IS NOT NULL AND creation_fingerprint IS NOT NULL)
    );

CREATE FUNCTION create_merchant_catalog_item(
    p_merchant_id uuid,
    p_store_location_id uuid,
    p_name text,
    p_description text,
    p_unit_price_cents integer,
    p_volume_liters numeric,
    p_idempotency_key text,
    p_request_fingerprint text
) RETURNS uuid
LANGUAGE plpgsql
AS $$
DECLARE
    v_item_id uuid;
    v_existing_store_id uuid;
    v_existing_fingerprint text;
BEGIN
    IF p_merchant_id IS NULL OR p_store_location_id IS NULL
       OR p_name IS NULL OR length(btrim(p_name)) NOT BETWEEN 1 AND 200
       OR p_description IS NULL OR length(p_description) > 1000
       OR p_unit_price_cents IS NULL OR p_unit_price_cents NOT BETWEEN 1 AND 10000000
       OR p_volume_liters IS NULL OR p_volume_liters <= 0 OR p_volume_liters > 100000
       OR p_volume_liters <> round(p_volume_liters, 2)
       OR p_idempotency_key IS NULL OR btrim(p_idempotency_key) = ''
       OR length(p_idempotency_key) > 200
       OR p_request_fingerprint IS NULL OR btrim(p_request_fingerprint) = '' THEN
        RAISE EXCEPTION 'catalog item fields are invalid';
    END IF;

    PERFORM pg_advisory_xact_lock(
        hashtextextended(p_merchant_id::text || ':' || p_idempotency_key, 0)
    );
    SELECT id, store_location_id, creation_fingerprint
    INTO v_item_id, v_existing_store_id, v_existing_fingerprint
    FROM catalog_items
    WHERE creation_idempotency_key = p_merchant_id::text || ':' || p_idempotency_key
    FOR UPDATE;
    IF FOUND THEN
        IF v_existing_store_id <> p_store_location_id
           OR v_existing_fingerprint <> p_request_fingerprint THEN
            RAISE EXCEPTION 'catalog idempotency key conflicts with another request';
        END IF;
        RETURN v_item_id;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM store_locations
        WHERE id = p_store_location_id AND merchant_id = p_merchant_id
    ) THEN
        RAISE EXCEPTION 'store location does not belong to this merchant'
            USING ERRCODE = 'P0002';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM merchants WHERE id = p_merchant_id AND active
    ) THEN
        RAISE EXCEPTION 'merchant not found' USING ERRCODE = 'P0002';
    END IF;

    INSERT INTO catalog_items (
        store_location_id, name, description, unit_price_cents, volume_liters,
        creation_idempotency_key, creation_fingerprint
    ) VALUES (
        p_store_location_id, btrim(p_name), p_description,
        p_unit_price_cents, p_volume_liters,
        p_merchant_id::text || ':' || p_idempotency_key, p_request_fingerprint
    ) RETURNING id INTO v_item_id;
    INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
    VALUES (
        'catalog_item', v_item_id, 'catalog.item.created',
        jsonb_build_object('itemId', v_item_id, 'merchantId', p_merchant_id,
                           'storeLocationId', p_store_location_id)
    );
    RETURN v_item_id;
END;
$$;

CREATE FUNCTION update_merchant_catalog_item(
    p_merchant_id uuid,
    p_item_id uuid,
    p_name text,
    p_description text,
    p_unit_price_cents integer,
    p_volume_liters numeric,
    p_available boolean
) RETURNS boolean
LANGUAGE plpgsql
AS $$
DECLARE
    v_store_location_id uuid;
BEGIN
    IF p_merchant_id IS NULL OR p_item_id IS NULL
       OR p_name IS NULL OR length(btrim(p_name)) NOT BETWEEN 1 AND 200
       OR p_description IS NULL OR length(p_description) > 1000
       OR p_unit_price_cents IS NULL OR p_unit_price_cents NOT BETWEEN 1 AND 10000000
       OR p_volume_liters IS NULL OR p_volume_liters <= 0 OR p_volume_liters > 100000
       OR p_volume_liters <> round(p_volume_liters, 2)
       OR p_available IS NULL THEN
        RAISE EXCEPTION 'catalog item fields are invalid';
    END IF;

    SELECT s.id INTO v_store_location_id
    FROM catalog_items i
    JOIN store_locations s ON s.id = i.store_location_id
    WHERE i.id = p_item_id AND s.merchant_id = p_merchant_id
    FOR UPDATE OF i;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'catalog item not found for merchant'
            USING ERRCODE = 'P0002';
    END IF;

    UPDATE catalog_items
    SET name = btrim(p_name), description = p_description,
        unit_price_cents = p_unit_price_cents, volume_liters = p_volume_liters,
        available = p_available, updated_at = now()
    WHERE id = p_item_id;
    INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
    VALUES (
        'catalog_item', p_item_id, 'catalog.item.updated',
        jsonb_build_object('itemId', p_item_id, 'merchantId', p_merchant_id,
                           'storeLocationId', v_store_location_id,
                           'available', p_available)
    );
    RETURN true;
END;
$$;
