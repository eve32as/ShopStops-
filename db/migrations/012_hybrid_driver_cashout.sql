ALTER TABLE orders
    ADD COLUMN fulfillment_mode text NOT NULL DEFAULT 'driver'
        CHECK (fulfillment_mode IN ('driver', 'in_store_shopper')),
    ADD COLUMN in_store_shopper_ref text,
    ADD COLUMN custody_pin_hash bytea,
    ADD COLUMN custody_pin_attempts integer NOT NULL DEFAULT 0
        CHECK (custody_pin_attempts BETWEEN 0 AND 5),
    ADD COLUMN custody_verified_at timestamptz,
    ADD COLUMN packed_at timestamptz,
    ADD CONSTRAINT orders_shopper_fulfillment_consistency CHECK (
        (fulfillment_mode = 'driver'
            AND in_store_shopper_ref IS NULL
            AND custody_pin_hash IS NULL
            AND packed_at IS NULL
            AND custody_verified_at IS NULL)
        OR
        (fulfillment_mode = 'in_store_shopper'
            AND in_store_shopper_ref IS NOT NULL
            AND ((packed_at IS NULL AND custody_pin_hash IS NULL)
                OR (packed_at IS NOT NULL AND custody_pin_hash IS NOT NULL))
            AND (custody_verified_at IS NULL OR packed_at IS NOT NULL))
    );

CREATE INDEX orders_in_store_shopper_queue_idx
    ON orders (ready_at, created_at)
    WHERE state = 'ready' AND fulfillment_mode = 'driver';

CREATE TABLE driver_connect_accounts (
    driver_ref text PRIMARY KEY CHECK (btrim(driver_ref) <> '' AND length(driver_ref) <= 200),
    stripe_account_id text NOT NULL UNIQUE,
    details_submitted boolean NOT NULL DEFAULT false,
    payouts_enabled boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE driver_route_earnings (
    route_id uuid PRIMARY KEY REFERENCES unified_delivery_routes(id),
    driver_ref text NOT NULL,
    amount_cents bigint NOT NULL CHECK (amount_cents > 0),
    currency char(3) NOT NULL DEFAULT 'usd' CHECK (currency = 'usd'),
    pickup_count integer NOT NULL CHECK (pickup_count > 0),
    status text NOT NULL DEFAULT 'available'
        CHECK (status IN ('available', 'reserved', 'paid')),
    created_at timestamptz NOT NULL DEFAULT now(),
    paid_at timestamptz
);
CREATE INDEX driver_route_earnings_available_idx
    ON driver_route_earnings (driver_ref, created_at)
    WHERE status = 'available';

CREATE TABLE driver_cashout_requests (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    driver_ref text NOT NULL,
    idempotency_key text NOT NULL CHECK (btrim(idempotency_key) <> ''),
    stripe_account_id text NOT NULL REFERENCES driver_connect_accounts(stripe_account_id),
    amount_cents bigint NOT NULL CHECK (amount_cents > 0),
    stripe_transfer_id text UNIQUE,
    stripe_payout_id text UNIQUE,
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'paid', 'failed')),
    failure_reason text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (driver_ref, idempotency_key)
);
CREATE INDEX driver_cashout_requests_history_idx
    ON driver_cashout_requests (driver_ref, created_at DESC);

CREATE FUNCTION record_driver_route_earnings()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    v_amount_cents bigint;
    v_pickup_count integer;
BEGIN
    IF OLD.status IS DISTINCT FROM NEW.status AND NEW.status = 'completed'
       AND NEW.driver_ref IS NOT NULL THEN
        SELECT estimate.driver_payout_cents,
               count(*) FILTER (WHERE stop.stop_type = 'pickup')::integer
        INTO v_amount_cents, v_pickup_count
        FROM checkout_financial_estimates estimate
        JOIN unified_route_stops stop ON stop.route_id = NEW.id
        WHERE estimate.checkout_id = NEW.checkout_id
        GROUP BY estimate.driver_payout_cents;

        IF v_amount_cents > 0 AND v_pickup_count > 0 THEN
            INSERT INTO driver_route_earnings (
                route_id, driver_ref, amount_cents, pickup_count
            ) VALUES (
                NEW.id, NEW.driver_ref, v_amount_cents, v_pickup_count
            )
            ON CONFLICT (route_id) DO NOTHING;
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER unified_delivery_route_earnings
AFTER UPDATE OF status ON unified_delivery_routes
FOR EACH ROW EXECUTE FUNCTION record_driver_route_earnings();

CREATE FUNCTION enforce_shopper_custody_before_pickup()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.status = 'arrived' AND NEW.status = 'completed'
       AND NEW.stop_type = 'pickup'
       AND EXISTS (
           SELECT 1
           FROM unified_delivery_routes route
           JOIN checkout_orders co ON co.checkout_id = route.checkout_id
           JOIN orders order_row ON order_row.id = co.order_id
           WHERE route.id = NEW.route_id
             AND order_row.store_location_id = NEW.store_location_id
             AND order_row.fulfillment_mode = 'in_store_shopper'
             AND order_row.custody_verified_at IS NULL
       ) THEN
        RAISE EXCEPTION 'in-store shopper custody must be verified before pickup';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER unified_route_stop_shopper_custody
BEFORE UPDATE OF status ON unified_route_stops
FOR EACH ROW EXECUTE FUNCTION enforce_shopper_custody_before_pickup();

GRANT SELECT, INSERT, UPDATE ON driver_connect_accounts TO shopstops_app;
GRANT SELECT, INSERT, UPDATE ON driver_route_earnings TO shopstops_app;
GRANT SELECT, INSERT, UPDATE ON driver_cashout_requests TO shopstops_app;
