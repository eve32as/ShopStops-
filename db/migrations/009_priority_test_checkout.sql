-- Test-only operational choice. These are immutable allocation intentions,
-- NOT worker earnings, expenses, payables, payroll, or a payout ledger.
CREATE TABLE checkout_priority_snapshots (
    checkout_id uuid PRIMARY KEY REFERENCES unified_checkouts(id),
    selected boolean NOT NULL,
    test_only boolean NOT NULL,
    shopping_cents integer NOT NULL CHECK (shopping_cents BETWEEN 0 AND 1000000),
    delivery_cents integer NOT NULL CHECK (delivery_cents BETWEEN 0 AND 1000000),
    shopper_allocation_cents integer NOT NULL,
    driver_allocation_cents integer NOT NULL,
    allocation_status text NOT NULL,
    invoice_total_cents integer NOT NULL CHECK (invoice_total_cents > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (shopper_allocation_cents = shopping_cents),
    CHECK (driver_allocation_cents = delivery_cents),
    CHECK ((selected AND test_only AND shopping_cents > 0 AND delivery_cents > 0
            AND allocation_status = 'pending_fulfillment')
        OR (NOT selected AND NOT test_only AND shopping_cents = 0 AND delivery_cents = 0
            AND allocation_status = 'not_applicable'))
);
REVOKE ALL ON checkout_priority_snapshots FROM PUBLIC;

CREATE FUNCTION protect_checkout_priority_snapshot() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'checkout priority snapshots are immutable';
END;
$$;
CREATE TRIGGER checkout_priority_snapshot_immutable
BEFORE UPDATE OR DELETE ON checkout_priority_snapshots
FOR EACH ROW EXECUTE FUNCTION protect_checkout_priority_snapshot();

ALTER TABLE checkout_invoice_lines
    DROP CONSTRAINT checkout_invoice_lines_line_type_check;
ALTER TABLE checkout_invoice_lines ADD CONSTRAINT checkout_invoice_lines_line_type_check
    CHECK (line_type IN ('merchant_items', 'delivery_fee', 'service_fee', 'dc_surcharge',
                         'priority_shopping', 'priority_delivery'));
CREATE UNIQUE INDEX checkout_priority_invoice_once
    ON checkout_invoice_lines (checkout_id, line_type)
    WHERE line_type IN ('priority_shopping', 'priority_delivery');

-- Once the snapshot exists, the selected priority invoice is frozen, including
-- its basket/base fees/tax. Webhooks/refunds change payment ledgers, not invoices.
CREATE FUNCTION protect_priority_invoice() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM checkout_priority_snapshots
               WHERE checkout_id = CASE WHEN TG_OP = 'DELETE' THEN OLD.checkout_id
                                        ELSE NEW.checkout_id END AND selected)
       OR (TG_OP = 'UPDATE' AND EXISTS (
           SELECT 1 FROM checkout_priority_snapshots WHERE checkout_id = OLD.checkout_id AND selected
       )) THEN
        RAISE EXCEPTION 'priority checkout invoice is immutable';
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER priority_invoice_immutable BEFORE INSERT OR UPDATE OR DELETE
ON checkout_invoice_lines FOR EACH ROW EXECUTE FUNCTION protect_priority_invoice();

-- Deferred checks let the existing checkout function and new wrapper build one
-- atomic quote. Neither a priority line nor a snapshot can commit alone.
CREATE FUNCTION validate_priority_snapshot_invoice() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_id uuid;
    v_snapshot checkout_priority_snapshots%ROWTYPE;
    v_shopping bigint;
    v_delivery bigint;
    v_count integer;
    v_total bigint;
BEGIN
    v_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.checkout_id ELSE NEW.checkout_id END;
    SELECT * INTO v_snapshot FROM checkout_priority_snapshots WHERE checkout_id = v_id;
    SELECT count(*), COALESCE(sum(amount_cents) FILTER (WHERE line_type = 'priority_shopping'), 0),
           COALESCE(sum(amount_cents) FILTER (WHERE line_type = 'priority_delivery'), 0)
    INTO v_count, v_shopping, v_delivery FROM checkout_invoice_lines
    WHERE checkout_id = v_id AND line_type IN ('priority_shopping', 'priority_delivery');
    IF (v_count > 0 AND (v_snapshot.checkout_id IS NULL OR NOT v_snapshot.selected))
       OR (v_snapshot.selected AND (v_count <> 2
           OR v_shopping <> v_snapshot.shopping_cents OR v_delivery <> v_snapshot.delivery_cents)) THEN
        RAISE EXCEPTION 'priority invoice must match its allocation snapshot';
    END IF;
    IF v_snapshot.selected THEN
        SELECT sum(amount_cents) INTO v_total FROM checkout_invoice_lines WHERE checkout_id = v_id;
        IF v_total IS DISTINCT FROM v_snapshot.invoice_total_cents::bigint THEN
            RAISE EXCEPTION 'priority invoice total must match its snapshot';
        END IF;
    END IF;
    RETURN NULL;
END;
$$;
CREATE CONSTRAINT TRIGGER priority_snapshot_invoice_consistent
AFTER INSERT ON checkout_priority_snapshots DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION validate_priority_snapshot_invoice();
CREATE CONSTRAINT TRIGGER priority_invoice_snapshot_consistent
AFTER INSERT OR UPDATE OR DELETE ON checkout_invoice_lines DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION validate_priority_snapshot_invoice();

-- Retain the inspected existing ownership, merchant eligibility, geographic,
-- basket-tax, and order validation. Only this wrapper is the new entry point.
ALTER FUNCTION create_unified_checkout(text, uuid, uuid[], double precision,
    double precision, timestamptz, timestamptz, integer, integer, text)
    RENAME TO create_unified_checkout_base;
REVOKE ALL ON FUNCTION create_unified_checkout_base(text, uuid, uuid[], double precision,
    double precision, timestamptz, timestamptz, integer, integer, text) FROM PUBLIC;

CREATE FUNCTION create_unified_checkout(
    p_customer_ref text, p_anchor_store_location_id uuid, p_order_ids uuid[],
    p_dropoff_latitude double precision, p_dropoff_longitude double precision,
    p_delivery_window_start timestamptz, p_delivery_window_end timestamptz,
    p_delivery_fee_cents integer, p_service_fee_cents integer, p_idempotency_key text,
    p_priority_service boolean DEFAULT false, p_priority_test_enabled boolean DEFAULT false,
    p_priority_shopping_cents integer DEFAULT 0, p_priority_delivery_cents integer DEFAULT 0
) RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE
    v_id uuid;
    v_existing unified_checkouts%ROWTYPE;
    v_snapshot checkout_priority_snapshots%ROWTYPE;
    v_total bigint;
BEGIN
    IF p_priority_service IS NULL OR p_priority_test_enabled IS NULL
       OR p_idempotency_key IS NULL OR btrim(p_idempotency_key) = '' THEN
        RAISE EXCEPTION 'invalid priority checkout request';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended(p_idempotency_key, 0));
    SELECT * INTO v_existing FROM unified_checkouts WHERE idempotency_key = p_idempotency_key;
    IF FOUND THEN
        SELECT * INTO v_snapshot FROM checkout_priority_snapshots WHERE checkout_id = v_existing.id;
        IF COALESCE(v_snapshot.selected, false) <> p_priority_service THEN
            RAISE EXCEPTION 'idempotency key reused with different priority choice';
        END IF;
        -- Rates are server inputs, not the customer's request identity. A retry
        -- keeps ALL original prices, even after configuration changes/disable.
        v_id := create_unified_checkout_base(p_customer_ref, p_anchor_store_location_id,
            p_order_ids, p_dropoff_latitude, p_dropoff_longitude, p_delivery_window_start,
            p_delivery_window_end, v_existing.delivery_fee_cents,
            v_existing.service_fee_cents, p_idempotency_key);
        IF v_snapshot.checkout_id IS NOT NULL THEN RETURN v_id; END IF;
    ELSE
        IF p_priority_service AND NOT p_priority_test_enabled THEN
            RAISE EXCEPTION 'priority service is unavailable';
        END IF;
        IF p_priority_service AND (p_priority_shopping_cents IS NULL
            OR p_priority_delivery_cents IS NULL
            OR p_priority_shopping_cents NOT BETWEEN 1 AND 1000000
            OR p_priority_delivery_cents NOT BETWEEN 1 AND 1000000) THEN
            RAISE EXCEPTION 'invalid configured priority rates';
        END IF;
        v_id := create_unified_checkout_base(p_customer_ref, p_anchor_store_location_id,
            p_order_ids, p_dropoff_latitude, p_dropoff_longitude, p_delivery_window_start,
            p_delivery_window_end, p_delivery_fee_cents, p_service_fee_cents, p_idempotency_key);
        IF p_priority_service THEN
            INSERT INTO checkout_invoice_lines (checkout_id, line_type, description, amount_cents)
            VALUES (v_id, 'priority_shopping', 'Priority shopping service (test only)', p_priority_shopping_cents),
                   (v_id, 'priority_delivery', 'Priority delivery service (test only)', p_priority_delivery_cents);
        END IF;
    END IF;
    SELECT sum(amount_cents) INTO v_total FROM checkout_invoice_lines WHERE checkout_id = v_id;
    IF v_total > 2147483647 OR v_total <= 0 THEN
        RAISE EXCEPTION 'checkout invoice total is out of bounds';
    END IF;
    INSERT INTO checkout_priority_snapshots (checkout_id, selected, test_only,
        shopping_cents, delivery_cents, shopper_allocation_cents, driver_allocation_cents,
        allocation_status, invoice_total_cents)
    VALUES (v_id, p_priority_service, p_priority_service,
        CASE WHEN p_priority_service THEN p_priority_shopping_cents ELSE 0 END,
        CASE WHEN p_priority_service THEN p_priority_delivery_cents ELSE 0 END,
        CASE WHEN p_priority_service THEN p_priority_shopping_cents ELSE 0 END,
        CASE WHEN p_priority_service THEN p_priority_delivery_cents ELSE 0 END,
        CASE WHEN p_priority_service THEN 'pending_fulfillment' ELSE 'not_applicable' END, v_total::integer);
    RETURN v_id;
END;
$$;
REVOKE ALL ON FUNCTION create_unified_checkout(text, uuid, uuid[], double precision,
    double precision, timestamptz, timestamptz, integer, integer, text,
    boolean, boolean, integer, integer) FROM PUBLIC;