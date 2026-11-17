-- Fixed approved platform rule; test-only quote, never driver earnings/payouts.
-- The base function validates actual orders, anchor presence, customer ownership,
-- merchant eligibility and existing geography. No customer count/rate is accepted.
CREATE TABLE checkout_extra_pickup_snapshots (
    checkout_id uuid PRIMARY KEY REFERENCES unified_checkouts(id),
    test_only boolean NOT NULL,
    merchandise_subtotal_cents bigint NOT NULL CHECK (merchandise_subtotal_cents BETWEEN 1 AND 2147483647),
    additional_pickups integer NOT NULL CHECK (additional_pickups BETWEEN 0 AND 100),
    rate_basis_points integer NOT NULL CHECK (rate_basis_points = 25),
    amount_cents integer NOT NULL CHECK (amount_cents >= 0),
    invoice_total_cents integer NOT NULL CHECK (invoice_total_cents > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (amount_cents = CASE WHEN test_only THEN
        (merchandise_subtotal_cents * 25 * additional_pickups + 5000) / 10000 ELSE 0 END)
);
REVOKE ALL ON checkout_extra_pickup_snapshots FROM PUBLIC;

CREATE FUNCTION protect_extra_pickup_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'extra pickup snapshots are immutable'; END;
$$;
CREATE TRIGGER extra_pickup_snapshot_immutable BEFORE UPDATE OR DELETE
ON checkout_extra_pickup_snapshots FOR EACH ROW EXECUTE FUNCTION protect_extra_pickup_snapshot();

ALTER TABLE checkout_invoice_lines DROP CONSTRAINT checkout_invoice_lines_line_type_check;
ALTER TABLE checkout_invoice_lines ADD CONSTRAINT checkout_invoice_lines_line_type_check
    CHECK (line_type IN ('merchant_items', 'delivery_fee', 'service_fee', 'dc_surcharge',
                         'priority_shopping', 'priority_delivery', 'extra_pickup_platform_fee'));
CREATE UNIQUE INDEX checkout_extra_pickup_invoice_once ON checkout_invoice_lines(checkout_id)
    WHERE line_type = 'extra_pickup_platform_fee';

CREATE FUNCTION protect_extra_pickup_invoice() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM checkout_extra_pickup_snapshots
        WHERE checkout_id = CASE WHEN TG_OP = 'DELETE' THEN OLD.checkout_id ELSE NEW.checkout_id END AND test_only)
       OR (TG_OP = 'UPDATE' AND EXISTS (SELECT 1 FROM checkout_extra_pickup_snapshots
           WHERE checkout_id = OLD.checkout_id AND test_only)) THEN
        RAISE EXCEPTION 'extra pickup test checkout invoice is immutable';
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER extra_pickup_invoice_immutable BEFORE INSERT OR UPDATE OR DELETE
ON checkout_invoice_lines FOR EACH ROW EXECUTE FUNCTION protect_extra_pickup_invoice();

CREATE FUNCTION validate_extra_pickup_invoice() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    v_id uuid;
    v_snapshot checkout_extra_pickup_snapshots%ROWTYPE;
    v_count integer;
    v_amount bigint;
    v_subtotal bigint;
    v_total bigint;
BEGIN
    v_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.checkout_id ELSE NEW.checkout_id END;
    SELECT * INTO v_snapshot FROM checkout_extra_pickup_snapshots WHERE checkout_id = v_id;
    SELECT count(*), COALESCE(sum(amount_cents), 0) INTO v_count, v_amount
        FROM checkout_invoice_lines WHERE checkout_id = v_id AND line_type = 'extra_pickup_platform_fee';
    IF v_count > 0 AND (v_snapshot.checkout_id IS NULL OR NOT v_snapshot.test_only) THEN
        RAISE EXCEPTION 'extra pickup fee requires a test snapshot';
    END IF;
    IF v_snapshot.test_only THEN
        SELECT sum(amount_cents) FILTER (WHERE line_type = 'merchant_items'), sum(amount_cents)
            INTO v_subtotal, v_total FROM checkout_invoice_lines WHERE checkout_id = v_id;
        IF v_count <> (CASE WHEN v_snapshot.amount_cents > 0 THEN 1 ELSE 0 END)
           OR v_amount <> v_snapshot.amount_cents
           OR v_subtotal IS DISTINCT FROM v_snapshot.merchandise_subtotal_cents
           OR v_total IS DISTINCT FROM v_snapshot.invoice_total_cents::bigint THEN
            RAISE EXCEPTION 'extra pickup invoice must match its snapshot';
        END IF;
    END IF;
    RETURN NULL;
END;
$$;
CREATE CONSTRAINT TRIGGER extra_pickup_snapshot_invoice_consistent AFTER INSERT
ON checkout_extra_pickup_snapshots DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION validate_extra_pickup_invoice();
CREATE CONSTRAINT TRIGGER extra_pickup_invoice_snapshot_consistent AFTER INSERT OR UPDATE OR DELETE
ON checkout_invoice_lines DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION validate_extra_pickup_invoice();

-- Replace, rather than overload, the 009 entry point: all old positional calls
-- retain their meaning through defaults. Build ALL lines before EITHER immutable
-- snapshot. Calling the old priority wrapper first would freeze its invoice.
DROP FUNCTION create_unified_checkout(text, uuid, uuid[], double precision, double precision,
    timestamptz, timestamptz, integer, integer, text, boolean, boolean, integer, integer);
CREATE FUNCTION create_unified_checkout(
    p_customer_ref text, p_anchor_store_location_id uuid, p_order_ids uuid[],
    p_dropoff_latitude double precision, p_dropoff_longitude double precision,
    p_delivery_window_start timestamptz, p_delivery_window_end timestamptz,
    p_delivery_fee_cents integer, p_service_fee_cents integer, p_idempotency_key text,
    p_priority_service boolean DEFAULT false, p_priority_test_enabled boolean DEFAULT false,
    p_priority_shopping_cents integer DEFAULT 0, p_priority_delivery_cents integer DEFAULT 0,
    p_extra_pickup_test_enabled boolean DEFAULT false
) RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE
    v_id uuid;
    v_existing unified_checkouts%ROWTYPE;
    v_priority checkout_priority_snapshots%ROWTYPE;
    v_total bigint;
    v_subtotal bigint;
    v_extra integer;
    v_fee bigint;
BEGIN
    IF p_priority_service IS NULL OR p_priority_test_enabled IS NULL OR p_extra_pickup_test_enabled IS NULL
       OR p_idempotency_key IS NULL OR btrim(p_idempotency_key) = '' THEN
        RAISE EXCEPTION 'invalid priority checkout request';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended(p_idempotency_key, 0));
    SELECT * INTO v_existing FROM unified_checkouts WHERE idempotency_key = p_idempotency_key;
    IF FOUND THEN
        SELECT * INTO v_priority FROM checkout_priority_snapshots WHERE checkout_id = v_existing.id;
        IF COALESCE(v_priority.selected, false) <> p_priority_service THEN
            RAISE EXCEPTION 'idempotency key reused with different priority choice';
        END IF;
        -- Validate original customer, anchor, orders, destination and windows;
        -- never reprice, backfill a fee, or change feature state on retry.
        v_id := create_unified_checkout_base(p_customer_ref, p_anchor_store_location_id,
            p_order_ids, p_dropoff_latitude, p_dropoff_longitude, p_delivery_window_start,
            p_delivery_window_end, v_existing.delivery_fee_cents, v_existing.service_fee_cents, p_idempotency_key);
        -- Preserve 009's compatibility for checkouts originally created before
        -- priority snapshots existed. This is not a new extra-pickup quote.
        IF v_priority.checkout_id IS NULL THEN
            SELECT sum(amount_cents) INTO v_total FROM checkout_invoice_lines WHERE checkout_id = v_id;
            IF v_total > 2147483647 OR v_total <= 0 THEN RAISE EXCEPTION 'checkout invoice total is out of bounds'; END IF;
            INSERT INTO checkout_priority_snapshots(checkout_id, selected, test_only, shopping_cents,
                delivery_cents, shopper_allocation_cents, driver_allocation_cents, allocation_status, invoice_total_cents)
            VALUES(v_id, false, false, 0, 0, 0, 0, 'not_applicable', v_total::integer);
        END IF;
        RETURN v_id;
    END IF;
    IF p_priority_service AND NOT p_priority_test_enabled THEN
        RAISE EXCEPTION 'priority service is unavailable';
    END IF;
    IF p_priority_service AND (p_priority_shopping_cents IS NULL OR p_priority_delivery_cents IS NULL
        OR p_priority_shopping_cents NOT BETWEEN 1 AND 1000000
        OR p_priority_delivery_cents NOT BETWEEN 1 AND 1000000) THEN
        RAISE EXCEPTION 'invalid configured priority rates';
    END IF;
    -- Lock authoritative orders through quote creation (including pickup IDs).
    PERFORM id FROM orders WHERE id = ANY(p_order_ids) ORDER BY id FOR SHARE;
    v_id := create_unified_checkout_base(p_customer_ref, p_anchor_store_location_id,
        p_order_ids, p_dropoff_latitude, p_dropoff_longitude, p_delivery_window_start,
        p_delivery_window_end, p_delivery_fee_cents, p_service_fee_cents, p_idempotency_key);
    SELECT GREATEST(count(DISTINCT o.store_location_id) - 1, 0)::integer INTO v_extra
        FROM checkout_orders co JOIN orders o ON o.id = co.order_id WHERE co.checkout_id = v_id;
    SELECT sum(amount_cents) INTO v_subtotal FROM checkout_invoice_lines
        WHERE checkout_id = v_id AND line_type = 'merchant_items';
    IF v_subtotal IS NULL OR v_subtotal NOT BETWEEN 1 AND 2147483647 THEN
        RAISE EXCEPTION 'checkout merchandise subtotal is out of bounds';
    END IF;
    -- Integer cents HALF UP once: 5000 * 25 * 1 => 13c; 10000 * 25 * 2 => 50c.
    v_fee := CASE WHEN p_extra_pickup_test_enabled THEN (v_subtotal * 25 * v_extra + 5000) / 10000 ELSE 0 END;
    IF v_fee > 2147483647 THEN RAISE EXCEPTION 'extra pickup fee is out of bounds'; END IF;
    IF v_fee > 0 THEN
        INSERT INTO checkout_invoice_lines(checkout_id, line_type, description, amount_cents)
            VALUES(v_id, 'extra_pickup_platform_fee', 'Additional pickup platform fee (test only)', v_fee::integer);
    END IF;
    IF p_priority_service THEN
        INSERT INTO checkout_invoice_lines(checkout_id, line_type, description, amount_cents)
        VALUES(v_id, 'priority_shopping', 'Priority shopping service (test only)', p_priority_shopping_cents),
              (v_id, 'priority_delivery', 'Priority delivery service (test only)', p_priority_delivery_cents);
    END IF;
    SELECT sum(amount_cents) INTO v_total FROM checkout_invoice_lines WHERE checkout_id = v_id;
    IF v_total > 2147483647 OR v_total <= 0 THEN RAISE EXCEPTION 'checkout invoice total is out of bounds'; END IF;
    INSERT INTO checkout_priority_snapshots(checkout_id, selected, test_only, shopping_cents,
        delivery_cents, shopper_allocation_cents, driver_allocation_cents, allocation_status, invoice_total_cents)
    VALUES(v_id, p_priority_service, p_priority_service,
        CASE WHEN p_priority_service THEN p_priority_shopping_cents ELSE 0 END,
        CASE WHEN p_priority_service THEN p_priority_delivery_cents ELSE 0 END,
        CASE WHEN p_priority_service THEN p_priority_shopping_cents ELSE 0 END,
        CASE WHEN p_priority_service THEN p_priority_delivery_cents ELSE 0 END,
        CASE WHEN p_priority_service THEN 'pending_fulfillment' ELSE 'not_applicable' END, v_total::integer);
    INSERT INTO checkout_extra_pickup_snapshots(checkout_id, test_only, merchandise_subtotal_cents,
        additional_pickups, rate_basis_points, amount_cents, invoice_total_cents)
    VALUES(v_id, p_extra_pickup_test_enabled, v_subtotal, v_extra, 25, v_fee::integer, v_total::integer);
    RETURN v_id;
END;
$$;
REVOKE ALL ON FUNCTION create_unified_checkout(text, uuid, uuid[], double precision, double precision,
    timestamptz, timestamptz, integer, integer, text, boolean, boolean, integer, integer, boolean) FROM PUBLIC;