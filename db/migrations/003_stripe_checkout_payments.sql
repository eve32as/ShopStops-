ALTER TABLE connect_transfers
    ADD COLUMN source_charge_id text;

CREATE FUNCTION process_stripe_checkout_event(
    p_event_id text,
    p_event_type text,
    p_payload jsonb,
    p_checkout_id uuid,
    p_payment_intent_id text,
    p_amount_cents integer,
    p_currency text,
    p_payment_status text,
    p_processing_fee_cents integer,
    p_charge_id text
) RETURNS uuid
LANGUAGE plpgsql
AS $$
DECLARE
    v_event stripe_webhook_events%ROWTYPE;
    v_checkout_id uuid;
    v_payment_status text;
    v_item_total bigint;
BEGIN
    IF p_event_id IS NULL OR btrim(p_event_id) = ''
       OR p_event_type IS NULL OR btrim(p_event_type) = ''
       OR p_payload IS NULL OR jsonb_typeof(p_payload) <> 'object' THEN
        RAISE EXCEPTION 'invalid Stripe event';
    END IF;

    INSERT INTO stripe_webhook_events (stripe_event_id, event_type, payload)
    VALUES (p_event_id, p_event_type, p_payload)
    ON CONFLICT (stripe_event_id) DO NOTHING;

    SELECT * INTO v_event
    FROM stripe_webhook_events
    WHERE stripe_event_id = p_event_id
    FOR UPDATE;

    IF v_event.processed_at IS NOT NULL THEN
        RETURN p_checkout_id;
    END IF;

    IF p_event_type IN (
        'payment_intent.succeeded',
        'payment_intent.processing',
        'payment_intent.payment_failed'
    ) THEN
        IF p_checkout_id IS NULL OR p_payment_intent_id IS NULL
           OR p_amount_cents IS NULL OR p_amount_cents <= 0
           OR p_currency IS NULL OR lower(p_currency) <> 'usd' THEN
            RAISE EXCEPTION 'Stripe payment event is missing checkout details';
        END IF;

        SELECT c.id, cp.status
        INTO v_checkout_id, v_payment_status
        FROM unified_checkouts c
        JOIN checkout_payments cp ON cp.checkout_id = c.id
        WHERE c.id = p_checkout_id
          AND cp.stripe_payment_intent_id = p_payment_intent_id
          AND cp.gross_amount_cents = p_amount_cents
        FOR UPDATE OF c, cp;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'Stripe payment does not match a persisted checkout payment';
        END IF;

        IF p_event_type = 'payment_intent.succeeded' THEN
            IF p_processing_fee_cents IS NULL OR p_processing_fee_cents < 0
               OR p_charge_id IS NULL OR btrim(p_charge_id) = '' THEN
                RAISE EXCEPTION 'successful Stripe payment is missing charge details';
            END IF;
            IF v_payment_status <> 'succeeded' THEN
                PERFORM record_checkout_payment(
                    p_checkout_id, p_payment_intent_id, p_amount_cents,
                    p_processing_fee_cents, 'succeeded'
                );
            ELSIF NOT EXISTS (
                SELECT 1 FROM connect_transfers WHERE checkout_id = p_checkout_id
            ) THEN
                RAISE EXCEPTION 'successful payment is missing its transfer ledger';
            END IF;

            SELECT COALESCE(sum(amount_cents), 0)
            INTO v_item_total
            FROM checkout_invoice_lines
            WHERE checkout_id = p_checkout_id
              AND line_type = 'merchant_items';

            IF NOT EXISTS (
                SELECT 1 FROM connect_transfers WHERE checkout_id = p_checkout_id
            ) THEN
                WITH merchant_items AS (
                    SELECT line.merchant_id, sum(line.amount_cents)::bigint AS item_gross
                    FROM checkout_invoice_lines line
                    WHERE line.checkout_id = p_checkout_id
                      AND line.line_type = 'merchant_items'
                    GROUP BY line.merchant_id
                ),
                amounts AS (
                    SELECT merchant_id, item_gross,
                           CASE WHEN v_item_total = 0 THEN 0
                                ELSE LEAST(p_processing_fee_cents, v_item_total)
                           END::bigint AS allocated_fee,
                           CASE WHEN v_item_total = 0 THEN 0
                                ELSE floor(
                                    LEAST(p_processing_fee_cents, v_item_total)
                                    * item_gross::numeric / v_item_total
                                )
                           END::bigint AS base_fee,
                           CASE WHEN v_item_total = 0 THEN 0
                                ELSE mod(
                                    LEAST(p_processing_fee_cents, v_item_total)::numeric
                                    * item_gross, v_item_total
                                )
                           END::bigint AS fee_remainder
                    FROM merchant_items
                ),
                ranked AS (
                    SELECT *,
                           allocated_fee - sum(base_fee) OVER () AS remaining_cents,
                           row_number() OVER (
                               ORDER BY fee_remainder DESC, merchant_id
                           ) AS remainder_rank
                    FROM amounts
                )
                INSERT INTO connect_transfers (
                    checkout_id, merchant_id, source_charge_id,
                    item_gross_cents, processing_fee_cents, status
                )
                SELECT p_checkout_id, merchant_id, p_charge_id,
                       item_gross::integer,
                       (base_fee + CASE
                           WHEN remainder_rank <= remaining_cents THEN 1 ELSE 0
                       END)::integer,
                       'pending'
                FROM ranked;
            ELSE
                IF EXISTS (
                    SELECT 1 FROM connect_transfers
                    WHERE checkout_id = p_checkout_id
                      AND source_charge_id <> p_charge_id
                ) THEN
                    RAISE EXCEPTION 'payment charge does not match the existing transfer ledger';
                END IF;
            END IF;
        ELSIF v_payment_status NOT IN ('succeeded', 'failed') THEN
            PERFORM record_checkout_payment(
                p_checkout_id, p_payment_intent_id, p_amount_cents, 0,
                p_payment_status
            );
        END IF;
    END IF;

    UPDATE stripe_webhook_events
    SET processed_at = now()
    WHERE stripe_event_id = p_event_id;
    RETURN p_checkout_id;
END;
$$;
