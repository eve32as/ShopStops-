ALTER TABLE merchant_subscriptions
    ADD COLUMN billing_setup_attempted_at timestamptz,
    ADD COLUMN subscription_attempted_at timestamptz,
    ADD COLUMN stripe_default_payment_method_id text;
ALTER TABLE merchant_subscriptions
    ADD COLUMN stripe_customer_attempted_at timestamptz;

CREATE TABLE checkout_refunds (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    checkout_id uuid NOT NULL REFERENCES unified_checkouts(id),
    stripe_payment_intent_id text NOT NULL REFERENCES checkout_payments(stripe_payment_intent_id),
    stripe_refund_id text UNIQUE,
    idempotency_key text NOT NULL UNIQUE,
    requested_by text NOT NULL,
    amount_cents integer NOT NULL CHECK (amount_cents > 0),
    reason text NOT NULL CHECK (reason IN ('duplicate', 'fraudulent', 'requested_by_customer')),
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'succeeded', 'failed', 'canceled')),
    attempted_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX checkout_refunds_one_successful_idx
    ON checkout_refunds (checkout_id) WHERE status = 'succeeded';

CREATE TABLE stripe_disputes (
    stripe_dispute_id text PRIMARY KEY,
    checkout_id uuid NOT NULL REFERENCES unified_checkouts(id),
    stripe_payment_intent_id text NOT NULL,
    amount_cents integer NOT NULL CHECK (amount_cents > 0),
    currency char(3) NOT NULL DEFAULT 'usd' CHECK (currency = 'usd'),
    reason text NOT NULL,
    status text NOT NULL
        CHECK (status IN ('needs_response', 'under_review', 'won', 'lost', 'warning_closed')),
    evidence_due_by timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE connect_transfer_reversals (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    checkout_refund_id uuid REFERENCES checkout_refunds(id),
    stripe_dispute_id text REFERENCES stripe_disputes(stripe_dispute_id),
    checkout_id uuid NOT NULL REFERENCES unified_checkouts(id),
    merchant_id uuid NOT NULL REFERENCES merchants(id),
    stripe_transfer_id text NOT NULL REFERENCES connect_transfers(stripe_transfer_id),
    stripe_reversal_id text UNIQUE,
    idempotency_key text NOT NULL UNIQUE,
    amount_cents integer NOT NULL CHECK (amount_cents > 0),
    attempted_at timestamptz NOT NULL DEFAULT now(),
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'reversed', 'failed')),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((checkout_refund_id IS NOT NULL) <> (stripe_dispute_id IS NOT NULL))
);
CREATE UNIQUE INDEX connect_reversal_per_refund_merchant_idx
    ON connect_transfer_reversals (checkout_refund_id, merchant_id)
    WHERE checkout_refund_id IS NOT NULL;
CREATE UNIQUE INDEX connect_reversal_per_dispute_merchant_idx
    ON connect_transfer_reversals (stripe_dispute_id, merchant_id)
    WHERE stripe_dispute_id IS NOT NULL;
CREATE INDEX connect_transfer_reversals_pending_idx
    ON connect_transfer_reversals (attempted_at) WHERE status = 'pending';

CREATE FUNCTION process_merchant_billing_event(
    p_event_id text,
    p_event_type text,
    p_payload jsonb,
    p_merchant_id uuid,
    p_stripe_customer_id text,
    p_stripe_subscription_id text,
    p_subscription_status text,
    p_current_period_end timestamptz,
    p_payment_failed boolean,
    p_default_payment_method_id text
) RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    v_event stripe_webhook_events%ROWTYPE;
    v_merchant_id uuid := p_merchant_id;
BEGIN
    IF p_event_id IS NULL OR btrim(p_event_id) = ''
       OR p_event_type IS NULL OR btrim(p_event_type) = ''
       OR p_payload IS NULL OR jsonb_typeof(p_payload) <> 'object' THEN
        RAISE EXCEPTION 'invalid Stripe billing event';
    END IF;
    INSERT INTO stripe_webhook_events (stripe_event_id, event_type, payload)
    VALUES (p_event_id, p_event_type, p_payload)
    ON CONFLICT (stripe_event_id) DO NOTHING;
    SELECT * INTO v_event FROM stripe_webhook_events
    WHERE stripe_event_id = p_event_id FOR UPDATE;
    IF v_event.processed_at IS NOT NULL THEN
        RETURN;
    END IF;

    IF v_merchant_id IS NULL AND p_stripe_subscription_id IS NOT NULL THEN
        SELECT merchant_id INTO v_merchant_id
        FROM merchant_subscriptions
        WHERE stripe_subscription_id = p_stripe_subscription_id;
    END IF;

    IF p_event_type = 'setup_intent.succeeded' THEN
        IF v_merchant_id IS NULL OR p_stripe_customer_id IS NULL
           OR p_default_payment_method_id IS NULL THEN
            RAISE EXCEPTION 'merchant setup intent is missing billing details';
        END IF;
        INSERT INTO merchant_subscriptions (
            merchant_id, stripe_customer_id, stripe_default_payment_method_id,
            status, monthly_amount_cents
        ) VALUES (
            v_merchant_id, p_stripe_customer_id,
            p_default_payment_method_id, 'incomplete', 19900
        )
        ON CONFLICT (merchant_id) DO UPDATE
        SET stripe_customer_id = EXCLUDED.stripe_customer_id,
            stripe_default_payment_method_id = EXCLUDED.stripe_default_payment_method_id,
            updated_at = now();
    ELSIF p_event_type IN (
        'customer.subscription.created',
        'customer.subscription.updated',
        'customer.subscription.deleted',
        'invoice.payment_succeeded',
        'invoice.payment_failed'
    ) THEN
        IF v_merchant_id IS NULL THEN
            RAISE EXCEPTION 'Stripe billing event does not match a merchant';
        END IF;
        IF p_event_type = 'customer.subscription.deleted'
           OR p_subscription_status IN ('canceled', 'unpaid', 'incomplete_expired') THEN
            UPDATE merchant_subscriptions
            SET status = 'cancelled', current_period_end = p_current_period_end,
                updated_at = now()
            WHERE merchant_id = v_merchant_id;
        ELSIF p_event_type = 'invoice.payment_failed' OR p_payment_failed THEN
            UPDATE merchant_subscriptions
            SET status = 'past_due',
                payment_failed_at = COALESCE(payment_failed_at, now()),
                updated_at = now()
            WHERE merchant_id = v_merchant_id;
        ELSIF p_event_type = 'invoice.payment_succeeded' THEN
            UPDATE merchant_subscriptions
            SET status = 'active',
                stripe_subscription_id = COALESCE(
                    p_stripe_subscription_id, stripe_subscription_id
                ),
                stripe_customer_id = COALESCE(
                    p_stripe_customer_id, stripe_customer_id
                ),
                current_period_end = COALESCE(
                    p_current_period_end, current_period_end
                ),
                payment_failed_at = NULL,
                updated_at = now()
            WHERE merchant_id = v_merchant_id;
        ELSIF p_subscription_status IN ('active', 'trialing') THEN
            UPDATE merchant_subscriptions
            SET status = CASE WHEN status = 'incomplete'
                              THEN 'incomplete' ELSE p_subscription_status END,
                stripe_subscription_id = COALESCE(
                    p_stripe_subscription_id, stripe_subscription_id
                ),
                stripe_customer_id = COALESCE(
                    p_stripe_customer_id, stripe_customer_id
                ),
                current_period_end = COALESCE(
                    p_current_period_end, current_period_end
                ),
                updated_at = now()
            WHERE merchant_id = v_merchant_id;
        ELSIF p_subscription_status = 'incomplete' THEN
            UPDATE merchant_subscriptions
            SET status = 'incomplete',
                stripe_subscription_id = COALESCE(
                    p_stripe_subscription_id, stripe_subscription_id
                ),
                stripe_customer_id = COALESCE(
                    p_stripe_customer_id, stripe_customer_id
                ),
                updated_at = now()
            WHERE merchant_id = v_merchant_id;
        END IF;
    END IF;
    UPDATE stripe_webhook_events SET processed_at = now()
    WHERE stripe_event_id = p_event_id;
END;
$$;

CREATE FUNCTION reserve_checkout_refund(
    p_checkout_id uuid,
    p_requested_by text,
    p_idempotency_key text,
    p_reason text
) RETURNS checkout_refunds
LANGUAGE plpgsql
AS $$
DECLARE
    v_checkout unified_checkouts%ROWTYPE;
    v_payment checkout_payments%ROWTYPE;
    v_refund checkout_refunds%ROWTYPE;
    v_total_refunded bigint;
BEGIN
    IF p_checkout_id IS NULL OR p_requested_by IS NULL OR btrim(p_requested_by) = ''
       OR p_idempotency_key IS NULL OR btrim(p_idempotency_key) = ''
       OR p_reason NOT IN ('duplicate', 'fraudulent', 'requested_by_customer') THEN
        RAISE EXCEPTION 'invalid refund request';
    END IF;
    SELECT * INTO v_checkout FROM unified_checkouts
    WHERE id = p_checkout_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'checkout not found';
    END IF;
    SELECT * INTO v_payment FROM checkout_payments
    WHERE checkout_id = p_checkout_id FOR UPDATE;
    IF NOT FOUND OR v_payment.status <> 'succeeded' THEN
        RAISE EXCEPTION 'checkout payment is not refundable';
    END IF;

    SELECT * INTO v_refund FROM checkout_refunds
    WHERE idempotency_key = p_idempotency_key;
    IF FOUND THEN
        IF v_refund.checkout_id <> p_checkout_id OR v_refund.reason <> p_reason THEN
            RAISE EXCEPTION 'refund idempotency key conflicts with another request';
        END IF;
        IF v_refund.attempted_at < now() - interval '23 hours'
           AND v_refund.stripe_refund_id IS NULL THEN
            RAISE EXCEPTION 'refund attempt requires Stripe reconciliation';
        END IF;
        RETURN v_refund;
    END IF;

    IF EXISTS (
        SELECT 1 FROM checkout_refunds
        WHERE checkout_id = p_checkout_id AND status IN ('pending', 'succeeded')
    ) THEN
        RAISE EXCEPTION 'checkout already has an active or completed refund';
    END IF;
    SELECT COALESCE(sum(amount_cents), 0) INTO v_total_refunded
    FROM checkout_refunds WHERE checkout_id = p_checkout_id AND status = 'succeeded';
    IF v_total_refunded >= v_payment.gross_amount_cents THEN
        RAISE EXCEPTION 'checkout has already been fully refunded';
    END IF;

    INSERT INTO checkout_refunds (
        checkout_id, stripe_payment_intent_id, idempotency_key,
        requested_by, amount_cents, reason
    ) VALUES (
        p_checkout_id, v_payment.stripe_payment_intent_id, p_idempotency_key,
        p_requested_by, v_payment.gross_amount_cents - v_total_refunded, p_reason
    ) RETURNING * INTO v_refund;
    RETURN v_refund;
END;
$$;

CREATE FUNCTION process_checkout_refund_event(
    p_event_id text,
    p_event_type text,
    p_payload jsonb,
    p_refund_id text,
    p_refund_status text
) RETURNS uuid
LANGUAGE plpgsql
AS $$
DECLARE
    v_event stripe_webhook_events%ROWTYPE;
    v_refund checkout_refunds%ROWTYPE;
BEGIN
    INSERT INTO stripe_webhook_events (stripe_event_id, event_type, payload)
    VALUES (p_event_id, p_event_type, p_payload)
    ON CONFLICT (stripe_event_id) DO NOTHING;
    SELECT * INTO v_event FROM stripe_webhook_events
    WHERE stripe_event_id = p_event_id FOR UPDATE;
    IF v_event.processed_at IS NOT NULL THEN
        SELECT * INTO v_refund FROM checkout_refunds
        WHERE stripe_refund_id = p_refund_id;
        RETURN v_refund.id;
    END IF;
    UPDATE checkout_refunds
    SET status = CASE p_refund_status
            WHEN 'succeeded' THEN 'succeeded'
            WHEN 'failed' THEN 'failed'
            WHEN 'canceled' THEN 'canceled'
            ELSE 'pending'
        END,
        updated_at = now()
    WHERE stripe_refund_id = p_refund_id
       OR (stripe_refund_id IS NULL AND idempotency_key =
           p_payload->>'refundIdempotencyKey')
    RETURNING * INTO v_refund;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'Stripe refund event does not match a pending refund';
    END IF;
    IF v_refund.status = 'succeeded' THEN
        UPDATE checkout_payments
        SET status = CASE WHEN v_refund.amount_cents >= gross_amount_cents
                          THEN 'refunded' ELSE status END,
            updated_at = now()
        WHERE checkout_id = v_refund.checkout_id;
        UPDATE dc_tax_ledger
        SET status = 'refunded'
        WHERE checkout_id = v_refund.checkout_id AND status = 'held';
        INSERT INTO connect_transfer_reversals (
            checkout_refund_id, checkout_id, merchant_id,
            stripe_transfer_id, idempotency_key, amount_cents
        )
        SELECT v_refund.id, transfer.checkout_id, transfer.merchant_id,
               transfer.stripe_transfer_id,
               'shopstops-refund-reversal-' || v_refund.id::text || '-' || transfer.merchant_id::text,
               transfer.transfer_amount_cents
        FROM connect_transfers transfer
        WHERE transfer.checkout_id = v_refund.checkout_id
          AND transfer.status = 'transferred'
          AND transfer.stripe_transfer_id IS NOT NULL
          AND transfer.transfer_amount_cents > 0
        ON CONFLICT (checkout_refund_id, merchant_id)
            WHERE checkout_refund_id IS NOT NULL DO NOTHING;
    END IF;
    UPDATE stripe_webhook_events SET processed_at = now()
    WHERE stripe_event_id = p_event_id;
    RETURN v_refund.id;
END;
$$;

CREATE FUNCTION process_stripe_dispute_event(
    p_event_id text,
    p_event_type text,
    p_payload jsonb,
    p_dispute_id text,
    p_checkout_id uuid,
    p_payment_intent_id text,
    p_amount_cents integer,
    p_reason text,
    p_status text,
    p_evidence_due_by timestamptz
) RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    v_event stripe_webhook_events%ROWTYPE;
BEGIN
    INSERT INTO stripe_webhook_events (stripe_event_id, event_type, payload)
    VALUES (p_event_id, p_event_type, p_payload)
    ON CONFLICT (stripe_event_id) DO NOTHING;
    SELECT * INTO v_event FROM stripe_webhook_events
    WHERE stripe_event_id = p_event_id FOR UPDATE;
    IF v_event.processed_at IS NOT NULL THEN
        RETURN;
    END IF;
    IF p_dispute_id IS NULL OR p_checkout_id IS NULL
       OR p_payment_intent_id IS NULL OR p_amount_cents IS NULL
       OR p_amount_cents <= 0 OR p_status NOT IN (
            'needs_response', 'under_review', 'won', 'lost', 'warning_closed'
       ) THEN
        RAISE EXCEPTION 'invalid Stripe dispute event';
    END IF;
    INSERT INTO stripe_disputes (
        stripe_dispute_id, checkout_id, stripe_payment_intent_id,
        amount_cents, reason, status, evidence_due_by
    ) VALUES (
        p_dispute_id, p_checkout_id, p_payment_intent_id,
        p_amount_cents, COALESCE(p_reason, 'unknown'), p_status,
        p_evidence_due_by
    )
    ON CONFLICT (stripe_dispute_id) DO UPDATE
    SET status = EXCLUDED.status, evidence_due_by = EXCLUDED.evidence_due_by,
        updated_at = now();
    IF p_event_type = 'charge.dispute.created' THEN
        INSERT INTO connect_transfer_reversals (
            stripe_dispute_id, checkout_id, merchant_id,
            stripe_transfer_id, idempotency_key, amount_cents
        )
        SELECT p_dispute_id, transfer.checkout_id, transfer.merchant_id,
               transfer.stripe_transfer_id,
               'shopstops-dispute-reversal-' || p_dispute_id || '-' || transfer.merchant_id::text,
               transfer.transfer_amount_cents
        FROM connect_transfers transfer
        WHERE transfer.checkout_id = p_checkout_id
          AND transfer.status = 'transferred'
          AND transfer.stripe_transfer_id IS NOT NULL
          AND transfer.transfer_amount_cents > 0
        ON CONFLICT (stripe_dispute_id, merchant_id)
            WHERE stripe_dispute_id IS NOT NULL DO NOTHING;
    END IF;
    UPDATE stripe_webhook_events SET processed_at = now()
    WHERE stripe_event_id = p_event_id;
END;
$$;

CREATE FUNCTION record_connect_transfer_reversal(
    p_checkout_refund_id uuid,
    p_stripe_dispute_id text,
    p_merchant_id uuid,
    p_reversal_id text
) RETURNS void
LANGUAGE plpgsql
AS $$
BEGIN
    UPDATE connect_transfer_reversals
    SET stripe_reversal_id = p_reversal_id, status = 'reversed'
    WHERE ((p_checkout_refund_id IS NOT NULL
            AND checkout_refund_id = p_checkout_refund_id)
           OR (p_stripe_dispute_id IS NOT NULL
               AND stripe_dispute_id = p_stripe_dispute_id))
      AND merchant_id = p_merchant_id
      AND (stripe_reversal_id IS NULL OR stripe_reversal_id = p_reversal_id);
    IF NOT FOUND THEN
        RAISE EXCEPTION 'transfer reversal record is missing or ID conflicts';
    END IF;
END;
$$;
CREATE FUNCTION record_checkout_refund(
    p_checkout_refund_id uuid,
    p_stripe_refund_id text,
    p_status text
) RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    v_refund checkout_refunds%ROWTYPE;
BEGIN
    IF p_status NOT IN ('pending', 'succeeded', 'failed', 'canceled')
       OR p_stripe_refund_id IS NULL OR btrim(p_stripe_refund_id) = '' THEN
        RAISE EXCEPTION 'invalid Stripe refund result';
    END IF;
    UPDATE checkout_refunds
    SET stripe_refund_id = p_stripe_refund_id, status = p_status,
        updated_at = now()
    WHERE id = p_checkout_refund_id
      AND (stripe_refund_id IS NULL OR stripe_refund_id = p_stripe_refund_id)
    RETURNING * INTO v_refund;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'refund record is missing or Stripe refund ID conflicts';
    END IF;
    IF p_status = 'succeeded' THEN
        UPDATE checkout_payments
        SET status = CASE
              WHEN v_refund.amount_cents >= gross_amount_cents THEN 'refunded'
              ELSE status
            END,
            updated_at = now()
        WHERE checkout_id = v_refund.checkout_id;
        UPDATE dc_tax_ledger
        SET status = 'refunded'
        WHERE checkout_id = v_refund.checkout_id AND status = 'held';
    END IF;
END;
$$;

CREATE FUNCTION reserve_merchant_billing_attempt(
    p_merchant_id uuid,
    p_attempt_kind text
) RETURNS merchant_subscriptions
LANGUAGE plpgsql
AS $$
DECLARE
    v_subscription merchant_subscriptions%ROWTYPE;
BEGIN
    IF p_merchant_id IS NULL OR p_attempt_kind NOT IN ('customer', 'subscription') THEN
        RAISE EXCEPTION 'invalid merchant billing attempt';
    END IF;
    INSERT INTO merchant_subscriptions (merchant_id, status, monthly_amount_cents)
    VALUES (p_merchant_id, 'incomplete', 19900)
    ON CONFLICT (merchant_id) DO NOTHING;
    SELECT * INTO v_subscription
    FROM merchant_subscriptions
    WHERE merchant_id = p_merchant_id
    FOR UPDATE;
    IF p_attempt_kind = 'customer' AND v_subscription.stripe_customer_id IS NULL THEN
        IF v_subscription.stripe_customer_attempted_at < now() - interval '23 hours' THEN
            RAISE EXCEPTION 'customer creation attempt requires Stripe reconciliation';
        END IF;
        UPDATE merchant_subscriptions
        SET stripe_customer_attempted_at =
            COALESCE(stripe_customer_attempted_at, now())
        WHERE merchant_id = p_merchant_id
        RETURNING * INTO v_subscription;
    ELSIF p_attempt_kind = 'subscription'
          AND v_subscription.stripe_subscription_id IS NULL THEN
        IF v_subscription.stripe_default_payment_method_id IS NULL THEN
            RAISE EXCEPTION 'merchant must save a payment method before subscribing';
        END IF;
        IF v_subscription.subscription_attempted_at < now() - interval '23 hours' THEN
            RAISE EXCEPTION 'subscription attempt requires Stripe reconciliation';
        END IF;
        UPDATE merchant_subscriptions
        SET subscription_attempted_at = COALESCE(subscription_attempted_at, now())
        WHERE merchant_id = p_merchant_id
        RETURNING * INTO v_subscription;
    END IF;
    RETURN v_subscription;
END;
$$;

CREATE FUNCTION record_merchant_stripe_customer(
    p_merchant_id uuid,
    p_stripe_customer_id text
) RETURNS void
LANGUAGE plpgsql
AS $$
BEGIN
    UPDATE merchant_subscriptions
    SET stripe_customer_id = p_stripe_customer_id, updated_at = now()
    WHERE merchant_id = p_merchant_id
      AND (stripe_customer_id IS NULL OR stripe_customer_id = p_stripe_customer_id);
    IF NOT FOUND THEN
        RAISE EXCEPTION 'merchant Stripe customer conflicts with persisted customer';
    END IF;
END;
$$;

CREATE FUNCTION record_merchant_stripe_subscription(
    p_merchant_id uuid,
    p_stripe_subscription_id text,
    p_status text
) RETURNS void
LANGUAGE plpgsql
AS $$
BEGIN
    IF p_status NOT IN ('incomplete', 'trialing', 'active', 'past_due', 'cancelled') THEN
        RAISE EXCEPTION 'invalid subscription status';
    END IF;
    UPDATE merchant_subscriptions
    SET stripe_subscription_id = p_stripe_subscription_id,
        status = p_status,
        updated_at = now()
    WHERE merchant_id = p_merchant_id
      AND (stripe_subscription_id IS NULL
           OR stripe_subscription_id = p_stripe_subscription_id);
    IF NOT FOUND THEN
        RAISE EXCEPTION 'merchant Stripe subscription conflicts with persisted subscription';
    END IF;
END;
$$;

CREATE FUNCTION reserve_connect_reversal_attempt(
    p_reversal_id uuid
) RETURNS connect_transfer_reversals
LANGUAGE plpgsql
AS $$
DECLARE
    v_reversal connect_transfer_reversals%ROWTYPE;
BEGIN
    UPDATE connect_transfer_reversals
    SET attempted_at = COALESCE(attempted_at, now())
    WHERE id = p_reversal_id AND status IN ('pending', 'failed')
      AND attempted_at > now() - interval '23 hours'
    RETURNING * INTO v_reversal;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'transfer reversal requires Stripe reconciliation';
    END IF;
    RETURN v_reversal;
END;
$$;
