CREATE FUNCTION process_stripe_checkout_session_completed(
    p_event_id text,
    p_payload jsonb,
    p_checkout_id uuid,
    p_payment_intent_id text,
    p_amount_cents integer,
    p_currency text,
    p_processing_fee_cents integer,
    p_charge_id text
) RETURNS uuid
LANGUAGE plpgsql
AS $$
DECLARE
    v_event stripe_webhook_events%ROWTYPE;
    v_route_id uuid;
BEGIN
    IF p_event_id IS NULL OR btrim(p_event_id) = ''
       OR p_payload IS NULL OR jsonb_typeof(p_payload) <> 'object'
       OR p_checkout_id IS NULL OR p_payment_intent_id IS NULL
       OR btrim(p_payment_intent_id) = ''
       OR p_amount_cents IS NULL OR p_amount_cents <= 0
       OR p_currency IS NULL OR lower(p_currency) <> 'usd'
       OR p_processing_fee_cents IS NULL OR p_processing_fee_cents < 0
       OR p_charge_id IS NULL OR btrim(p_charge_id) = '' THEN
        RAISE EXCEPTION 'invalid Stripe checkout session event';
    END IF;

    INSERT INTO stripe_webhook_events (stripe_event_id, event_type, payload)
    VALUES (p_event_id, 'checkout.session.completed', p_payload)
    ON CONFLICT (stripe_event_id) DO NOTHING;

    SELECT * INTO v_event
    FROM stripe_webhook_events
    WHERE stripe_event_id = p_event_id
    FOR UPDATE;

    IF v_event.event_type <> 'checkout.session.completed' THEN
        RAISE EXCEPTION 'Stripe event ID was reused for a different event type';
    END IF;
    IF v_event.processed_at IS NOT NULL THEN
        SELECT id INTO v_route_id
        FROM unified_delivery_routes
        WHERE idempotency_key = 'stripe-checkout-session-' || p_event_id;
        RETURN v_route_id;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM checkout_payments WHERE checkout_id = p_checkout_id
    ) THEN
        PERFORM record_checkout_payment(
            p_checkout_id, p_payment_intent_id, p_amount_cents, 0,
            'requires_payment_method'
        );
    END IF;

    PERFORM process_stripe_checkout_event(
        p_event_id, 'payment_intent.succeeded', p_payload, p_checkout_id,
        p_payment_intent_id, p_amount_cents, p_currency, 'succeeded',
        p_processing_fee_cents, p_charge_id
    );

    SELECT id INTO v_route_id
    FROM unified_delivery_routes
    WHERE checkout_id = p_checkout_id;
    IF NOT FOUND THEN
        SELECT create_unified_route(
            p_checkout_id, 'stripe-checkout-session-' || p_event_id
        ) INTO v_route_id;
    END IF;

    UPDATE stripe_webhook_events
    SET processed_at = now()
    WHERE stripe_event_id = p_event_id;
    RETURN v_route_id;
END;
$$;
