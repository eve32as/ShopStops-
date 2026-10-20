CREATE FUNCTION set_merchant_order_state(
    p_merchant_id uuid,
    p_order_id uuid,
    p_new_state text
) RETURNS text
LANGUAGE plpgsql
AS $$
DECLARE
    v_current_state text;
    v_checkout_status text;
BEGIN
    IF p_merchant_id IS NULL OR p_order_id IS NULL OR p_new_state IS NULL
       OR p_new_state NOT IN ('preparing', 'ready') THEN
        RAISE EXCEPTION 'merchant order state fields are invalid';
    END IF;

    SELECT o.state, c.status
    INTO v_current_state, v_checkout_status
    FROM orders o
    JOIN store_locations s ON s.id = o.store_location_id
    JOIN checkout_orders co ON co.order_id = o.id
    JOIN unified_checkouts c ON c.id = co.checkout_id
    WHERE o.id = p_order_id
      AND s.merchant_id = p_merchant_id
    FOR UPDATE OF o, c;
    IF NOT FOUND OR v_checkout_status <> 'paid' THEN
        RAISE EXCEPTION 'paid order not found for merchant'
            USING ERRCODE = 'P0002';
    END IF;

    IF v_current_state = p_new_state THEN
        RETURN v_current_state;
    END IF;
    IF (v_current_state, p_new_state) NOT IN (
        ('placed', 'preparing'),
        ('preparing', 'ready')
    ) THEN
        RAISE EXCEPTION 'invalid merchant order state transition'
            USING ERRCODE = 'P0001';
    END IF;

    UPDATE orders
    SET state = p_new_state,
        ready_at = CASE WHEN p_new_state = 'ready' THEN now() ELSE NULL END,
        updated_at = now()
    WHERE id = p_order_id;

    RETURN p_new_state;
END;
$$;
