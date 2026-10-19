CREATE TABLE merchants (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    merchant_type text NOT NULL CHECK
        (merchant_type IN ('restaurant', 'retail', 'grocery')),
    stripe_account_id text UNIQUE,
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE store_locations
    ADD COLUMN merchant_id uuid REFERENCES merchants(id);

CREATE TABLE merchant_subscriptions (
    merchant_id uuid PRIMARY KEY REFERENCES merchants(id),
    stripe_customer_id text,
    stripe_subscription_id text UNIQUE,
    status text NOT NULL CHECK
        (status IN ('trialing', 'active', 'past_due', 'cancelled', 'incomplete')),
    monthly_amount_cents integer NOT NULL DEFAULT 19900
        CHECK (monthly_amount_cents = 19900),
    currency char(3) NOT NULL DEFAULT 'usd' CHECK (currency = 'usd'),
    current_period_end timestamptz,
    payment_failed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE VIEW merchant_storefront_visibility AS
SELECT m.id AS merchant_id,
       (m.active
        AND COALESCE(
            (s.status = 'active'
             AND (s.current_period_end IS NULL OR s.current_period_end > now()))
            OR (s.status = 'past_due'
                AND s.payment_failed_at > now() - interval '48 hours'),
            false
        )) AS visible
FROM merchants m
LEFT JOIN merchant_subscriptions s ON s.merchant_id = m.id;

CREATE TABLE jurisdictions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code text NOT NULL UNIQUE,
    name text NOT NULL,
    boundary geometry(MultiPolygon, 4326) NOT NULL,
    enabled boolean NOT NULL DEFAULT true
);
CREATE INDEX jurisdictions_boundary_gix ON jurisdictions USING gist (boundary);

CREATE TABLE unified_checkouts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_ref text NOT NULL,
    anchor_store_location_id uuid NOT NULL REFERENCES store_locations(id),
    delivery_zone_id uuid NOT NULL REFERENCES delivery_zones(id),
    dropoff_location geography(Point, 4326) NOT NULL,
    delivery_window_start timestamptz NOT NULL,
    delivery_window_end timestamptz NOT NULL,
    status text NOT NULL DEFAULT 'building' CHECK
        (status IN ('building', 'quoted', 'paid', 'dispatching',
                    'in_progress', 'completed', 'cancelled')),
    delivery_fee_cents integer NOT NULL DEFAULT 0 CHECK (delivery_fee_cents >= 0),
    service_fee_cents integer NOT NULL DEFAULT 0 CHECK (service_fee_cents >= 0),
    currency char(3) NOT NULL DEFAULT 'usd' CHECK (currency = 'usd'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (delivery_window_end > delivery_window_start)
);

CREATE FUNCTION enforce_checkout_state_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.status IS DISTINCT FROM OLD.status AND NOT (
        (OLD.status = 'building' AND NEW.status IN ('quoted', 'paid', 'cancelled'))
        OR (OLD.status = 'quoted' AND NEW.status IN ('paid', 'cancelled'))
        OR (OLD.status = 'paid' AND NEW.status IN ('dispatching', 'cancelled'))
        OR (OLD.status = 'dispatching' AND NEW.status IN ('in_progress', 'cancelled'))
        OR (OLD.status = 'in_progress' AND NEW.status IN ('completed', 'cancelled'))
    ) THEN
        RAISE EXCEPTION 'invalid checkout state transition: % -> %',
            OLD.status, NEW.status;
    END IF;
    IF OLD.status <> 'building' AND (
        NEW.anchor_store_location_id <> OLD.anchor_store_location_id
        OR NEW.delivery_zone_id <> OLD.delivery_zone_id
        OR NEW.dropoff_location IS DISTINCT FROM OLD.dropoff_location
        OR NEW.delivery_window_start <> OLD.delivery_window_start
        OR NEW.delivery_window_end <> OLD.delivery_window_end
    ) THEN
        RAISE EXCEPTION 'checkout destination and delivery window are immutable after quoting';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER unified_checkout_state_guard
BEFORE UPDATE ON unified_checkouts
FOR EACH ROW EXECUTE FUNCTION enforce_checkout_state_transition();

CREATE TABLE checkout_orders (
    checkout_id uuid NOT NULL REFERENCES unified_checkouts(id) ON DELETE CASCADE,
    order_id uuid NOT NULL UNIQUE REFERENCES orders(id),
    added_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (checkout_id, order_id)
);
CREATE INDEX checkout_orders_checkout_idx ON checkout_orders (checkout_id);

CREATE FUNCTION enforce_checkout_merchant_limit()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    v_checkout unified_checkouts%ROWTYPE;
    v_order_customer text;
    v_merchant_id uuid;
    v_anchor_merchant_id uuid;
    v_order_store_id uuid;
    v_order_zone_id uuid;
    v_order_dropoff geography(Point, 4326);
    v_order_store_location geography(Point, 4326);
    v_order_window_start timestamptz;
    v_order_window_end timestamptz;
    v_existing_merchants integer;
BEGIN
    PERFORM pg_advisory_xact_lock(
        hashtextextended(NEW.checkout_id::text, 0)
    );

    SELECT * INTO v_checkout
    FROM unified_checkouts
    WHERE id = NEW.checkout_id
    FOR UPDATE;
    IF NOT FOUND OR v_checkout.status <> 'building' THEN
        RAISE EXCEPTION 'checkout does not exist or is no longer editable';
    END IF;

    SELECT o.customer_ref, s.merchant_id, s.id, o.delivery_zone_id,
           o.dropoff_location, s.location, o.delivery_window_start,
           o.delivery_window_end
    INTO v_order_customer, v_merchant_id, v_order_store_id,
         v_order_zone_id, v_order_dropoff, v_order_store_location,
         v_order_window_start, v_order_window_end
    FROM orders o
    JOIN store_locations s ON s.id = o.store_location_id AND s.active
    JOIN merchants m ON m.id = s.merchant_id AND m.active
    WHERE o.id = NEW.order_id;
    IF NOT FOUND OR v_merchant_id IS NULL THEN
        RAISE EXCEPTION 'order must belong to an active merchant';
    END IF;
    IF v_order_customer <> v_checkout.customer_ref THEN
        RAISE EXCEPTION 'order and checkout customers must match';
    END IF;
    IF v_order_zone_id <> v_checkout.delivery_zone_id
       OR ST_Distance(v_order_dropoff, v_checkout.dropoff_location) > 1
       OR v_order_window_start > v_checkout.delivery_window_end
       OR v_order_window_end < v_checkout.delivery_window_start
       OR NOT EXISTS (
           SELECT 1 FROM delivery_zones z
           WHERE z.id = v_checkout.delivery_zone_id AND z.enabled
             AND ST_Covers(z.boundary, v_order_store_location::geometry)
             AND ST_Covers(z.boundary, v_order_dropoff::geometry)
       ) THEN
        RAISE EXCEPTION 'checkout orders must share its service area, destination, and window';
    END IF;

    SELECT merchant_id INTO v_anchor_merchant_id
    FROM store_locations
    WHERE id = v_checkout.anchor_store_location_id;
    IF v_merchant_id <> v_anchor_merchant_id THEN
        IF NOT EXISTS (
            SELECT 1
            FROM merchant_subscriptions ms
            WHERE ms.merchant_id = v_merchant_id
              AND ms.status = 'active'
              AND (ms.current_period_end IS NULL OR ms.current_period_end > now())
              AND ms.payment_failed_at IS NULL
        ) THEN
            RAISE EXCEPTION 'additional merchants must have a positive subscription';
        END IF;
        IF NOT EXISTS (
            SELECT 1
            FROM store_locations s
            JOIN merchants m ON m.id = s.merchant_id
            WHERE s.id = v_checkout.anchor_store_location_id
              AND s.active
              AND m.active
              AND m.merchant_type = 'restaurant'
        ) THEN
            RAISE EXCEPTION 'unified checkout anchor must be an active restaurant';
        END IF;
    END IF;

    IF EXISTS (
        SELECT 1
        FROM checkout_orders co
        JOIN orders o ON o.id = co.order_id
        JOIN store_locations s ON s.id = o.store_location_id
        WHERE co.checkout_id = NEW.checkout_id
          AND co.order_id <> NEW.order_id
          AND s.merchant_id = v_merchant_id
          AND s.id <> v_order_store_id
    ) THEN
        RAISE EXCEPTION 'each merchant may contribute only one pickup location';
    END IF;

    SELECT count(DISTINCT s.merchant_id) INTO v_existing_merchants
    FROM checkout_orders co
    JOIN orders o ON o.id = co.order_id
    JOIN store_locations s ON s.id = o.store_location_id
    WHERE co.checkout_id = NEW.checkout_id
      AND co.order_id <> NEW.order_id;
    IF v_merchant_id NOT IN (
        SELECT s.merchant_id
        FROM checkout_orders co
        JOIN orders o ON o.id = co.order_id
        JOIN store_locations s ON s.id = o.store_location_id
        WHERE co.checkout_id = NEW.checkout_id
          AND co.order_id <> NEW.order_id
    ) AND v_existing_merchants >= 2 THEN
        RAISE EXCEPTION 'a unified checkout may contain at most two merchants';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER checkout_orders_merchant_limit
BEFORE INSERT OR UPDATE OF checkout_id, order_id ON checkout_orders
FOR EACH ROW EXECUTE FUNCTION enforce_checkout_merchant_limit();

CREATE FUNCTION prevent_checkout_order_removal()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM unified_checkouts
        WHERE id = OLD.checkout_id AND status = 'building'
    ) THEN
        RAISE EXCEPTION 'checkout orders cannot be removed after checkout is quoted';
    END IF;
    RETURN OLD;
END;
$$;

CREATE TRIGGER checkout_orders_delete_guard
BEFORE DELETE ON checkout_orders
FOR EACH ROW EXECUTE FUNCTION prevent_checkout_order_removal();

CREATE FUNCTION nearby_subscribed_merchants(
    p_anchor_store_location_id uuid,
    p_radius_meters integer DEFAULT 500
) RETURNS TABLE (
    merchant_id uuid,
    merchant_name text,
    merchant_type text,
    store_location_id uuid,
    distance_meters double precision
)
LANGUAGE sql
STABLE
AS $$
    SELECT m.id, m.name, m.merchant_type, s.id,
           ST_Distance(anchor.location, s.location)
    FROM store_locations anchor
    JOIN merchants anchor_merchant ON anchor_merchant.id = anchor.merchant_id
    JOIN store_locations s ON s.active
    JOIN merchants m ON m.id = s.merchant_id AND m.active
    JOIN merchant_subscriptions ms ON ms.merchant_id = m.id
    JOIN merchant_storefront_visibility visibility
      ON visibility.merchant_id = m.id AND visibility.visible
    WHERE anchor.id = p_anchor_store_location_id
      AND anchor.active
      AND anchor_merchant.active
      AND anchor_merchant.merchant_type = 'restaurant'
      AND s.id <> anchor.id
      AND m.id <> anchor_merchant.id
      AND m.merchant_type IN ('retail', 'grocery')
      AND ms.status = 'active'
      AND (ms.current_period_end IS NULL OR ms.current_period_end > now())
      AND ms.payment_failed_at IS NULL
      AND p_radius_meters >= 0
      AND ST_DWithin(anchor.location, s.location, p_radius_meters)
    ORDER BY ST_Distance(anchor.location, s.location), m.name, s.id
$$;

CREATE TABLE checkout_invoice_lines (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    checkout_id uuid NOT NULL REFERENCES unified_checkouts(id) ON DELETE CASCADE,
    merchant_id uuid REFERENCES merchants(id),
    line_type text NOT NULL CHECK
        (line_type IN ('merchant_items', 'delivery_fee', 'service_fee', 'dc_surcharge')),
    description text NOT NULL,
    amount_cents integer NOT NULL CHECK (amount_cents >= 0),
    currency char(3) NOT NULL DEFAULT 'usd' CHECK (currency = 'usd'),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((line_type = 'merchant_items' AND merchant_id IS NOT NULL)
        OR (line_type <> 'merchant_items' AND merchant_id IS NULL)),
    CHECK (line_type <> 'dc_surcharge' OR amount_cents = 20)
);
CREATE UNIQUE INDEX checkout_invoice_dc_surcharge_once
    ON checkout_invoice_lines (checkout_id)
    WHERE line_type = 'dc_surcharge';

CREATE FUNCTION validate_checkout_invoice_line()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    v_checkout unified_checkouts%ROWTYPE;
BEGIN
    SELECT * INTO v_checkout FROM unified_checkouts
    WHERE id = NEW.checkout_id FOR UPDATE;
    IF NOT FOUND OR v_checkout.status NOT IN ('building', 'quoted') THEN
        RAISE EXCEPTION 'invoice lines can only be changed before payment';
    END IF;
    IF NEW.line_type = 'merchant_items' AND NOT EXISTS (
        SELECT 1
        FROM checkout_orders co
        JOIN orders o ON o.id = co.order_id
        JOIN store_locations s ON s.id = o.store_location_id
        WHERE co.checkout_id = NEW.checkout_id
          AND s.merchant_id = NEW.merchant_id
    ) THEN
        RAISE EXCEPTION 'merchant item line must belong to a checkout merchant';
    END IF;
    IF NEW.line_type = 'dc_surcharge' AND NOT EXISTS (
        SELECT 1 FROM jurisdictions j
        WHERE j.code = 'US-DC' AND j.enabled
          AND ST_Covers(j.boundary, v_checkout.dropoff_location::geometry)
    ) THEN
        RAISE EXCEPTION 'DC surcharge requires a destination inside Washington, DC';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER checkout_invoice_lines_validate
BEFORE INSERT OR UPDATE ON checkout_invoice_lines
FOR EACH ROW EXECUTE FUNCTION validate_checkout_invoice_line();

CREATE TABLE dc_tax_ledger (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    checkout_id uuid NOT NULL UNIQUE REFERENCES unified_checkouts(id),
    invoice_line_id uuid NOT NULL UNIQUE REFERENCES checkout_invoice_lines(id),
    amount_cents integer NOT NULL CHECK (amount_cents = 20),
    status text NOT NULL DEFAULT 'held'
        CHECK (status IN ('held', 'remitted', 'refunded')),
    payment_reference text,
    remitted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'remitted' AND remitted_at IS NOT NULL)
        OR status <> 'remitted')
);

CREATE FUNCTION apply_dc_delivery_surcharge(p_checkout_id uuid)
RETURNS integer
LANGUAGE plpgsql
AS $$
DECLARE
    v_checkout unified_checkouts%ROWTYPE;
    v_line_id uuid;
BEGIN
    SELECT * INTO v_checkout
    FROM unified_checkouts
    WHERE id = p_checkout_id
    FOR UPDATE;
    IF NOT FOUND OR v_checkout.status NOT IN ('building', 'quoted') THEN
        RAISE EXCEPTION 'checkout is not eligible for surcharge calculation';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM jurisdictions j
        WHERE j.code = 'US-DC'
          AND j.enabled
          AND ST_Covers(j.boundary, v_checkout.dropoff_location::geometry)
    ) THEN
        INSERT INTO checkout_invoice_lines (
            checkout_id, line_type, description, amount_cents, currency
        ) VALUES (
            p_checkout_id, 'dc_surcharge', 'District of Columbia delivery surcharge',
            20, v_checkout.currency
        )
        ON CONFLICT (checkout_id) WHERE line_type = 'dc_surcharge' DO NOTHING
        RETURNING id INTO v_line_id;

        RETURN 20;
    END IF;

    DELETE FROM checkout_invoice_lines
    WHERE checkout_id = p_checkout_id AND line_type = 'dc_surcharge';
    RETURN 0;
END;
$$;

CREATE TABLE checkout_payments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    checkout_id uuid NOT NULL UNIQUE REFERENCES unified_checkouts(id),
    stripe_payment_intent_id text NOT NULL UNIQUE,
    gross_amount_cents integer NOT NULL CHECK (gross_amount_cents > 0),
    currency char(3) NOT NULL DEFAULT 'usd' CHECK (currency = 'usd'),
    status text NOT NULL CHECK
        (status IN ('requires_payment_method', 'processing', 'succeeded',
                    'failed', 'refunded')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE stripe_webhook_events (
    stripe_event_id text PRIMARY KEY,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz
);
CREATE INDEX stripe_webhook_events_unprocessed_idx
    ON stripe_webhook_events (received_at) WHERE processed_at IS NULL;

CREATE TABLE connect_transfers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    checkout_id uuid NOT NULL REFERENCES unified_checkouts(id),
    merchant_id uuid NOT NULL REFERENCES merchants(id),
    stripe_transfer_id text UNIQUE,
    item_gross_cents integer NOT NULL CHECK (item_gross_cents >= 0),
    processing_fee_cents integer NOT NULL CHECK (processing_fee_cents >= 0),
    transfer_amount_cents integer GENERATED ALWAYS AS
        (item_gross_cents - processing_fee_cents) STORED,
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'transferred', 'reversed', 'failed')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (checkout_id, merchant_id),
    CHECK (processing_fee_cents <= item_gross_cents)
);

CREATE TABLE platform_checkout_ledger (
    checkout_id uuid PRIMARY KEY REFERENCES unified_checkouts(id),
    delivery_fee_cents integer NOT NULL CHECK (delivery_fee_cents >= 0),
    service_fee_cents integer NOT NULL CHECK (service_fee_cents >= 0),
    dc_surcharge_cents integer NOT NULL CHECK (dc_surcharge_cents IN (0, 20)),
    stripe_processing_fee_cents integer NOT NULL DEFAULT 0
        CHECK (stripe_processing_fee_cents >= 0),
    stripe_payment_intent_id text UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE FUNCTION record_checkout_payment(
    p_checkout_id uuid,
    p_stripe_payment_intent_id text,
    p_gross_amount_cents integer,
    p_processing_fee_cents integer,
    p_status text
) RETURNS uuid
LANGUAGE plpgsql
AS $$
DECLARE
    v_payment_id uuid;
    v_checkout unified_checkouts%ROWTYPE;
    v_invoice_total integer;
    v_surcharge_line_id uuid;
BEGIN
    IF p_checkout_id IS NULL OR p_stripe_payment_intent_id IS NULL
       OR btrim(p_stripe_payment_intent_id) = ''
       OR p_gross_amount_cents IS NULL OR p_processing_fee_cents IS NULL
       OR p_gross_amount_cents <= 0 OR p_processing_fee_cents < 0
       OR p_processing_fee_cents > p_gross_amount_cents
       OR p_status IS NULL
       OR p_status NOT IN ('requires_payment_method', 'processing',
                           'succeeded', 'failed', 'refunded') THEN
        RAISE EXCEPTION 'invalid checkout payment details';
    END IF;
    SELECT * INTO v_checkout FROM unified_checkouts
    WHERE id = p_checkout_id FOR UPDATE;
    IF NOT FOUND OR v_checkout.status NOT IN ('building', 'quoted', 'paid') THEN
        RAISE EXCEPTION 'checkout is not eligible for payment';
    END IF;
    SELECT COALESCE(sum(amount_cents), 0) INTO v_invoice_total
    FROM checkout_invoice_lines WHERE checkout_id = p_checkout_id;
    IF v_invoice_total <> p_gross_amount_cents THEN
        RAISE EXCEPTION 'payment amount must match the itemized invoice';
    END IF;

    INSERT INTO checkout_payments (
        checkout_id, stripe_payment_intent_id, gross_amount_cents, currency, status
    ) VALUES (
        p_checkout_id, p_stripe_payment_intent_id, p_gross_amount_cents,
        v_checkout.currency, p_status
    )
    ON CONFLICT (checkout_id) DO UPDATE
    SET status = EXCLUDED.status, updated_at = now()
    WHERE checkout_payments.stripe_payment_intent_id =
          EXCLUDED.stripe_payment_intent_id
    RETURNING id INTO v_payment_id;
    IF v_payment_id IS NULL THEN
        RAISE EXCEPTION 'checkout already has a different payment intent';
    END IF;

    INSERT INTO platform_checkout_ledger (
        checkout_id, delivery_fee_cents, service_fee_cents, dc_surcharge_cents,
        stripe_processing_fee_cents, stripe_payment_intent_id
    ) VALUES (
        p_checkout_id, v_checkout.delivery_fee_cents, v_checkout.service_fee_cents,
        CASE WHEN EXISTS (
            SELECT 1 FROM checkout_invoice_lines
            WHERE checkout_id = p_checkout_id AND line_type = 'dc_surcharge'
        ) THEN 20 ELSE 0 END,
        p_processing_fee_cents, p_stripe_payment_intent_id
    )
    ON CONFLICT (checkout_id) DO UPDATE
    SET stripe_processing_fee_cents = EXCLUDED.stripe_processing_fee_cents;

    IF p_status = 'succeeded' THEN
        UPDATE unified_checkouts SET status = 'paid', updated_at = now()
        WHERE id = p_checkout_id;
        SELECT id INTO v_surcharge_line_id
        FROM checkout_invoice_lines
        WHERE checkout_id = p_checkout_id AND line_type = 'dc_surcharge';
        IF v_surcharge_line_id IS NOT NULL THEN
            INSERT INTO dc_tax_ledger (
                checkout_id, invoice_line_id, amount_cents, payment_reference
            ) VALUES (
                p_checkout_id, v_surcharge_line_id, 20, p_stripe_payment_intent_id
            )
            ON CONFLICT (checkout_id) DO NOTHING;
        END IF;
    END IF;
    RETURN v_payment_id;
END;
$$;

CREATE TABLE unified_delivery_routes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    checkout_id uuid NOT NULL UNIQUE REFERENCES unified_checkouts(id),
    driver_ref text,
    routing_provider text NOT NULL CHECK
        (routing_provider IN ('osrm', 'google_maps', 'manual')),
    provider_route_ref text,
    status text NOT NULL DEFAULT 'planned'
        CHECK (status IN ('planned', 'routed', 'in_progress', 'completed', 'cancelled')),
    idempotency_key text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE unified_route_stops (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    route_id uuid NOT NULL REFERENCES unified_delivery_routes(id) ON DELETE CASCADE,
    sequence integer NOT NULL CHECK (sequence > 0),
    stop_type text NOT NULL CHECK (stop_type IN ('pickup', 'dropoff')),
    store_location_id uuid REFERENCES store_locations(id),
    location geography(Point, 4326) NOT NULL,
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'arrived', 'completed')),
    created_at timestamptz NOT NULL DEFAULT now(),
    arrived_at timestamptz,
    completed_at timestamptz,
    UNIQUE (route_id, sequence),
    CHECK ((stop_type = 'pickup' AND store_location_id IS NOT NULL)
        OR (stop_type = 'dropoff' AND store_location_id IS NULL)),
    CHECK ((status IN ('arrived', 'completed') AND arrived_at IS NOT NULL)
        OR status NOT IN ('arrived', 'completed')),
    CHECK ((status = 'completed' AND completed_at IS NOT NULL)
        OR status <> 'completed')
);

CREATE TABLE unified_route_legs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    route_id uuid NOT NULL REFERENCES unified_delivery_routes(id) ON DELETE CASCADE,
    from_sequence integer NOT NULL,
    to_sequence integer NOT NULL,
    distance_meters integer NOT NULL CHECK (distance_meters >= 0),
    duration_seconds integer NOT NULL CHECK (duration_seconds >= 0),
    provider text NOT NULL CHECK (provider IN ('osrm', 'google_maps')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (route_id, from_sequence),
    FOREIGN KEY (route_id, from_sequence)
        REFERENCES unified_route_stops(route_id, sequence) ON DELETE CASCADE,
    FOREIGN KEY (route_id, to_sequence)
        REFERENCES unified_route_stops(route_id, sequence) ON DELETE CASCADE,
    CHECK (to_sequence = from_sequence + 1)
);

CREATE FUNCTION create_unified_route(
    p_checkout_id uuid,
    p_idempotency_key text,
    p_routing_provider text DEFAULT 'osrm'
) RETURNS uuid
LANGUAGE plpgsql
AS $$
DECLARE
    v_route_id uuid;
    v_checkout unified_checkouts%ROWTYPE;
    v_anchor_store store_locations%ROWTYPE;
    v_partner_store store_locations%ROWTYPE;
    v_partner_merchant_count integer;
BEGIN
    IF p_checkout_id IS NULL OR p_idempotency_key IS NULL
       OR btrim(p_idempotency_key) = '' THEN
        RAISE EXCEPTION 'checkout and idempotency key are required';
    END IF;
    IF p_routing_provider NOT IN ('osrm', 'google_maps', 'manual') THEN
        RAISE EXCEPTION 'unsupported routing provider';
    END IF;

    PERFORM pg_advisory_xact_lock(hashtextextended(p_idempotency_key, 0));
    SELECT id INTO v_route_id
    FROM unified_delivery_routes
    WHERE idempotency_key = p_idempotency_key;
    IF FOUND THEN
        RETURN v_route_id;
    END IF;

    SELECT * INTO v_checkout
    FROM unified_checkouts
    WHERE id = p_checkout_id
    FOR UPDATE;
    IF NOT FOUND OR v_checkout.status <> 'paid' THEN
        RAISE EXCEPTION 'only paid checkouts can be routed';
    END IF;

    SELECT * INTO v_anchor_store
    FROM store_locations
    WHERE id = v_checkout.anchor_store_location_id AND active;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'anchor store is unavailable';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM merchants m
        WHERE m.id = v_anchor_store.merchant_id
          AND m.active AND m.merchant_type = 'restaurant'
    ) THEN
        RAISE EXCEPTION 'route anchor must be an active restaurant';
    END IF;

    SELECT count(DISTINCT s.merchant_id)
    INTO v_partner_merchant_count
    FROM checkout_orders co
    JOIN orders o ON o.id = co.order_id
    JOIN store_locations s ON s.id = o.store_location_id AND s.active
    WHERE co.checkout_id = p_checkout_id
      AND s.merchant_id <> v_anchor_store.merchant_id;
    IF v_partner_merchant_count <> 1 THEN
        RAISE EXCEPTION 'a chained route requires exactly one additional merchant';
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM checkout_orders co
        JOIN orders o ON o.id = co.order_id
        JOIN store_locations s ON s.id = o.store_location_id
        WHERE co.checkout_id = p_checkout_id
          AND s.merchant_id = v_anchor_store.merchant_id
    ) THEN
        RAISE EXCEPTION 'checkout has no anchor-restaurant order';
    END IF;

    SELECT s.* INTO v_partner_store
    FROM checkout_orders co
    JOIN orders o ON o.id = co.order_id
    JOIN store_locations s ON s.id = o.store_location_id AND s.active
    WHERE co.checkout_id = p_checkout_id
      AND s.merchant_id <> v_anchor_store.merchant_id
    ORDER BY s.id
    LIMIT 1;

    INSERT INTO unified_delivery_routes (
        checkout_id, routing_provider, idempotency_key
    ) VALUES (
        p_checkout_id, p_routing_provider, p_idempotency_key
    ) RETURNING id INTO v_route_id;

    INSERT INTO unified_route_stops (
        route_id, sequence, stop_type, store_location_id, location
    ) VALUES
        (v_route_id, 1, 'pickup', v_anchor_store.id, v_anchor_store.location),
        (v_route_id, 2, 'pickup', v_partner_store.id, v_partner_store.location),
        (v_route_id, 3, 'dropoff', NULL, v_checkout.dropoff_location);

    UPDATE unified_checkouts SET status = 'dispatching', updated_at = now()
    WHERE id = p_checkout_id;
    INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
    VALUES (
        'unified_delivery_route', v_route_id, 'delivery.route.created',
        jsonb_build_object('routeId', v_route_id, 'checkoutId', p_checkout_id,
                           'stopCount', 3, 'routingProvider', p_routing_provider)
    );
    RETURN v_route_id;
END;
$$;

CREATE FUNCTION prevent_unified_route_resequence()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.route_id <> OLD.route_id
       OR NEW.sequence <> OLD.sequence
       OR NEW.stop_type <> OLD.stop_type
       OR NEW.store_location_id IS DISTINCT FROM OLD.store_location_id
       OR NEW.location <> OLD.location THEN
        RAISE EXCEPTION 'driver stop sequence and locations are immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER unified_route_stops_immutable
BEFORE UPDATE ON unified_route_stops
FOR EACH ROW EXECUTE FUNCTION prevent_unified_route_resequence();

CREATE TABLE driver_location_samples (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    route_id uuid NOT NULL REFERENCES unified_delivery_routes(id) ON DELETE CASCADE,
    driver_ref text NOT NULL,
    location geography(Point, 4326) NOT NULL,
    observed_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    event_id text NOT NULL UNIQUE,
    UNIQUE (route_id, driver_ref, observed_at)
);
CREATE INDEX driver_location_samples_route_time_idx
    ON driver_location_samples (route_id, observed_at DESC);
CREATE INDEX driver_location_samples_location_gix
    ON driver_location_samples USING gist (location);

CREATE FUNCTION record_driver_location(
    p_route_id uuid,
    p_driver_ref text,
    p_location geography(Point, 4326),
    p_observed_at timestamptz,
    p_event_id text
) RETURNS bigint
LANGUAGE plpgsql
AS $$
DECLARE
    v_sample_id bigint;
    v_last_observed_at timestamptz;
BEGIN
    IF p_route_id IS NULL OR p_driver_ref IS NULL OR p_location IS NULL
       OR p_observed_at IS NULL OR p_event_id IS NULL OR btrim(p_event_id) = '' THEN
        RAISE EXCEPTION 'route, driver, location, timestamp, and event ID are required';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM unified_delivery_routes r
        WHERE r.id = p_route_id AND r.driver_ref = p_driver_ref
          AND r.status = 'in_progress'
    ) THEN
        RAISE EXCEPTION 'driver is not assigned to an active route';
    END IF;

    PERFORM pg_advisory_xact_lock(
        hashtextextended(p_route_id::text || p_driver_ref, 0)
    );
    SELECT id INTO v_sample_id FROM driver_location_samples
    WHERE event_id = p_event_id;
    IF FOUND THEN
        RETURN v_sample_id;
    END IF;

    SELECT max(observed_at) INTO v_last_observed_at
    FROM driver_location_samples
    WHERE route_id = p_route_id AND driver_ref = p_driver_ref;
    IF v_last_observed_at IS NOT NULL
       AND p_observed_at < v_last_observed_at + interval '5 seconds' THEN
        RAISE EXCEPTION 'driver location samples must be at least five seconds apart';
    END IF;

    INSERT INTO driver_location_samples (
        route_id, driver_ref, location, observed_at, event_id
    ) VALUES (
        p_route_id, p_driver_ref, p_location, p_observed_at, p_event_id
    ) RETURNING id INTO v_sample_id;
    RETURN v_sample_id;
END;
$$;

CREATE FUNCTION publish_order_state_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.state IS DISTINCT FROM OLD.state THEN
        INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
        VALUES (
            'order', NEW.id, 'order.state.changed',
            jsonb_build_object(
                'orderId', NEW.id, 'customerRef', NEW.customer_ref,
                'previousState', OLD.state, 'state', NEW.state,
                'changedAt', NEW.updated_at
            )
        );
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER orders_state_outbox
AFTER UPDATE OF state ON orders
FOR EACH ROW EXECUTE FUNCTION publish_order_state_change();
