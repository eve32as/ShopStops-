CREATE OR REPLACE VIEW merchant_storefront_visibility AS
SELECT m.id AS merchant_id, m.active AS visible
FROM merchants m;

CREATE OR REPLACE FUNCTION nearby_subscribed_merchants(
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
    WHERE anchor.id = p_anchor_store_location_id
      AND anchor.active
      AND anchor_merchant.active
      AND anchor_merchant.merchant_type = 'restaurant'
      AND s.id <> anchor.id
      AND m.id <> anchor_merchant.id
      AND m.merchant_type IN ('retail', 'grocery')
      AND p_radius_meters >= 0
      AND ST_DWithin(anchor.location, s.location, p_radius_meters)
    ORDER BY ST_Distance(anchor.location, s.location), m.name, s.id
$$;

CREATE OR REPLACE FUNCTION enforce_checkout_merchant_limit()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    v_checkout unified_checkouts%ROWTYPE;
    v_order_customer text;
    v_merchant_id uuid;
    v_merchant_type text;
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

    SELECT o.customer_ref, s.merchant_id, m.merchant_type, s.id, o.delivery_zone_id,
           o.dropoff_location, s.location, o.delivery_window_start,
           o.delivery_window_end
    INTO v_order_customer, v_merchant_id, v_merchant_type, v_order_store_id,
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
    IF v_merchant_id = v_anchor_merchant_id
       AND v_order_store_id <> v_checkout.anchor_store_location_id THEN
        RAISE EXCEPTION 'anchor orders must be from the selected restaurant location';
    END IF;
    IF v_merchant_id <> v_anchor_merchant_id THEN
        IF v_merchant_type NOT IN ('retail', 'grocery')
           OR NOT ST_DWithin(
               v_order_store_location,
               (SELECT location FROM store_locations
                WHERE id = v_checkout.anchor_store_location_id),
               500
           ) THEN
            RAISE EXCEPTION 'additional merchant must be nearby retail or grocery';
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

CREATE TABLE checkout_merchant_financials (
    checkout_id uuid NOT NULL REFERENCES unified_checkouts(id) ON DELETE CASCADE,
    merchant_id uuid NOT NULL REFERENCES merchants(id),
    item_gross_cents integer NOT NULL CHECK (item_gross_cents > 0),
    commission_rate_bps integer NOT NULL
        CHECK (commission_rate_bps IN (500, 1000)),
    platform_fee_cents integer NOT NULL CHECK (platform_fee_cents >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (checkout_id, merchant_id),
    CHECK (platform_fee_cents =
        ((item_gross_cents::bigint * commission_rate_bps + 5000) / 10000))
);

CREATE FUNCTION validate_checkout_merchant_financial()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    v_status text;
    v_period_end timestamptz;
    v_payment_failed_at timestamptz;
    v_premium boolean := false;
    v_expected_rate integer;
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM unified_checkouts c
        JOIN checkout_invoice_lines line ON line.checkout_id = c.id
        WHERE c.id = NEW.checkout_id
          AND c.status = 'quoted'
          AND line.line_type = 'merchant_items'
          AND line.merchant_id = NEW.merchant_id
          AND line.amount_cents = NEW.item_gross_cents
    ) THEN
        RAISE EXCEPTION 'merchant financial snapshot must match a quoted merchant item line';
    END IF;

    SELECT status, current_period_end, payment_failed_at
    INTO v_status, v_period_end, v_payment_failed_at
    FROM merchant_subscriptions
    WHERE merchant_id = NEW.merchant_id
    FOR SHARE;
    IF FOUND THEN
        v_premium :=
            (v_status = 'active'
             AND (v_period_end IS NULL OR v_period_end > now()))
            OR (v_status = 'past_due'
                AND v_payment_failed_at > now() - interval '48 hours');
    END IF;
    v_expected_rate := CASE WHEN v_premium THEN 500 ELSE 1000 END;
    IF NEW.commission_rate_bps <> v_expected_rate THEN
        RAISE EXCEPTION 'merchant commission rate does not match its subscription tier';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER checkout_merchant_financials_validate
BEFORE INSERT OR UPDATE ON checkout_merchant_financials
FOR EACH ROW EXECUTE FUNCTION validate_checkout_merchant_financial();

CREATE FUNCTION snapshot_checkout_merchant_financials()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    INSERT INTO checkout_merchant_financials (
        checkout_id, merchant_id, item_gross_cents,
        commission_rate_bps, platform_fee_cents
    )
    SELECT NEW.id, line.merchant_id, line.amount_cents,
           CASE WHEN visibility.visible
                AND subscription.merchant_id IS NOT NULL
                AND (
                    (subscription.status = 'active'
                     AND (subscription.current_period_end IS NULL
                          OR subscription.current_period_end > now()))
                    OR (subscription.status = 'past_due'
                        AND subscription.payment_failed_at > now() - interval '48 hours')
                )
                THEN 500 ELSE 1000 END,
           ((line.amount_cents::bigint *
             CASE WHEN visibility.visible
                       AND subscription.merchant_id IS NOT NULL
                       AND (
                           (subscription.status = 'active'
                            AND (subscription.current_period_end IS NULL
                                 OR subscription.current_period_end > now()))
                           OR (subscription.status = 'past_due'
                               AND subscription.payment_failed_at > now() - interval '48 hours')
                       )
                  THEN 500 ELSE 1000 END
             + 5000) / 10000)::integer
    FROM checkout_invoice_lines line
    LEFT JOIN merchant_subscriptions subscription
      ON subscription.merchant_id = line.merchant_id
    JOIN merchant_storefront_visibility visibility
      ON visibility.merchant_id = line.merchant_id
    WHERE line.checkout_id = NEW.id AND line.line_type = 'merchant_items'
    ON CONFLICT (checkout_id, merchant_id) DO NOTHING;
    RETURN NEW;
END;
$$;

CREATE TRIGGER unified_checkout_snapshot_merchant_financials
AFTER UPDATE OF status ON unified_checkouts
FOR EACH ROW
WHEN (OLD.status IS DISTINCT FROM NEW.status AND NEW.status = 'quoted')
EXECUTE FUNCTION snapshot_checkout_merchant_financials();

CREATE TABLE checkout_financial_estimates (
    checkout_id uuid PRIMARY KEY REFERENCES unified_checkouts(id) ON DELETE CASCADE,
    item_subtotal_cents bigint NOT NULL CHECK (item_subtotal_cents > 0),
    commission_cents bigint NOT NULL CHECK (commission_cents >= 0),
    delivery_fee_cents integer NOT NULL CHECK (delivery_fee_cents >= 0),
    service_fee_cents integer NOT NULL CHECK (service_fee_cents >= 0),
    pass_through_cents integer NOT NULL CHECK (pass_through_cents IN (0, 20)),
    estimated_processor_fee_cents bigint NOT NULL
        CHECK (estimated_processor_fee_cents >= 0),
    driver_payout_cents bigint NOT NULL CHECK (driver_payout_cents >= 0),
    driver_base_payout_cents integer NOT NULL CHECK (driver_base_payout_cents >= 0),
    along_route_pickup_bonus_cents integer NOT NULL
        CHECK (along_route_pickup_bonus_cents >= 0),
    fixed_order_cost_cents integer NOT NULL CHECK (fixed_order_cost_cents >= 0),
    target_profit_cents bigint NOT NULL CHECK (target_profit_cents >= 0),
    projected_net_profit_cents bigint NOT NULL,
    target_margin_bps integer NOT NULL CHECK (target_margin_bps BETWEEN 0 AND 9999),
    stripe_fee_bps integer NOT NULL CHECK (stripe_fee_bps BETWEEN 0 AND 9999),
    stripe_fixed_fee_cents integer NOT NULL CHECK (stripe_fixed_fee_cents >= 0),
    along_route_pickup_count integer NOT NULL CHECK (along_route_pickup_count >= 0),
    typical_basket_cents integer NOT NULL CHECK (typical_basket_cents > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (driver_payout_cents =
        driver_base_payout_cents::bigint
        + along_route_pickup_count::bigint * along_route_pickup_bonus_cents),
    CHECK (target_profit_cents =
        (item_subtotal_cents * target_margin_bps + 9999) / 10000),
    CHECK (estimated_processor_fee_cents =
        ((item_subtotal_cents + delivery_fee_cents + service_fee_cents
          + pass_through_cents) * stripe_fee_bps + 5000) / 10000
        + stripe_fixed_fee_cents),
    CHECK (projected_net_profit_cents =
        commission_cents + delivery_fee_cents + service_fee_cents
        - estimated_processor_fee_cents - driver_payout_cents
        - fixed_order_cost_cents),
    CHECK (projected_net_profit_cents >= target_profit_cents)
);

CREATE FUNCTION validate_checkout_financial_estimate()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    v_checkout unified_checkouts%ROWTYPE;
    v_merchant_gross bigint;
    v_merchant_fees bigint;
    v_surcharge integer;
BEGIN
    SELECT * INTO v_checkout
    FROM unified_checkouts
    WHERE id = NEW.checkout_id
    FOR SHARE;
    IF NOT FOUND OR v_checkout.status <> 'quoted' THEN
        RAISE EXCEPTION 'financial estimate requires a quoted checkout';
    END IF;

    SELECT COALESCE(sum(item_gross_cents), 0),
           COALESCE(sum(platform_fee_cents), 0)
    INTO v_merchant_gross, v_merchant_fees
    FROM checkout_merchant_financials
    WHERE checkout_id = NEW.checkout_id;
    SELECT COALESCE(max(amount_cents), 0)
    INTO v_surcharge
    FROM checkout_invoice_lines
    WHERE checkout_id = NEW.checkout_id AND line_type = 'dc_surcharge';

    IF v_merchant_gross <> NEW.item_subtotal_cents
       OR v_merchant_fees <> NEW.commission_cents
       OR v_checkout.delivery_fee_cents <> NEW.delivery_fee_cents
       OR v_checkout.service_fee_cents <> NEW.service_fee_cents
       OR v_surcharge <> NEW.pass_through_cents
       OR (SELECT count(*) FROM checkout_merchant_financials
           WHERE checkout_id = NEW.checkout_id) <>
          (SELECT count(DISTINCT s.merchant_id)
           FROM checkout_orders co
           JOIN orders o ON o.id = co.order_id
           JOIN store_locations s ON s.id = o.store_location_id
           WHERE co.checkout_id = NEW.checkout_id) THEN
        RAISE EXCEPTION 'financial estimate does not match the persisted checkout';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER checkout_financial_estimates_validate
BEFORE INSERT OR UPDATE ON checkout_financial_estimates
FOR EACH ROW EXECUTE FUNCTION validate_checkout_financial_estimate();

ALTER TABLE connect_transfers
    ADD COLUMN platform_fee_cents integer NOT NULL DEFAULT 0
        CHECK (platform_fee_cents >= 0);
ALTER TABLE connect_transfers
    ADD COLUMN processor_fee_borne_by_platform boolean NOT NULL DEFAULT false;
ALTER TABLE connect_transfers DROP COLUMN transfer_amount_cents;
ALTER TABLE connect_transfers
    ADD COLUMN transfer_amount_cents integer GENERATED ALWAYS AS
        (item_gross_cents - platform_fee_cents
         - CASE WHEN processor_fee_borne_by_platform
                THEN 0 ELSE processing_fee_cents END) STORED,
    ADD CONSTRAINT connect_transfers_platform_fee_within_gross
        CHECK (platform_fee_cents <= item_gross_cents
            - CASE WHEN processor_fee_borne_by_platform
                   THEN 0 ELSE processing_fee_cents END);

CREATE FUNCTION apply_checkout_merchant_platform_fee()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    SELECT financial.platform_fee_cents
    INTO NEW.platform_fee_cents
    FROM checkout_merchant_financials financial
    WHERE financial.checkout_id = NEW.checkout_id
      AND financial.merchant_id = NEW.merchant_id;
    IF FOUND THEN
        NEW.processor_fee_borne_by_platform := true;
    ELSE
        NEW.platform_fee_cents := 0;
        NEW.processor_fee_borne_by_platform := false;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER connect_transfers_apply_platform_fee
BEFORE INSERT ON connect_transfers
FOR EACH ROW EXECUTE FUNCTION apply_checkout_merchant_platform_fee();
