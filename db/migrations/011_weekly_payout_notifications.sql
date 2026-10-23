CREATE TABLE driver_payout_profiles (
    driver_ref text PRIMARY KEY CHECK (length(btrim(driver_ref)) BETWEEN 1 AND 200),
    email_address text NOT NULL CHECK (
        length(email_address) BETWEEN 3 AND 320
        AND position('@' IN email_address) > 1
        AND email_address = btrim(email_address)
    ),
    enabled boolean NOT NULL DEFAULT true,
    updated_by text NOT NULL CHECK (length(btrim(updated_by)) BETWEEN 1 AND 200),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE driver_weekly_earnings_statements (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    driver_ref text NOT NULL REFERENCES driver_payout_profiles(driver_ref),
    period_start date NOT NULL,
    period_end date NOT NULL,
    completed_routes integer NOT NULL CHECK (completed_routes > 0),
    distance_meters bigint NOT NULL CHECK (distance_meters >= 0),
    base_pay_cents bigint NOT NULL CHECK (base_pay_cents >= 0),
    mileage_pay_cents bigint NOT NULL CHECK (mileage_pay_cents >= 0),
    total_earnings_cents bigint NOT NULL CHECK (
        total_earnings_cents = base_pay_cents + mileage_pay_cents
    ),
    mileage_rate_cents_per_mile bigint NOT NULL CHECK (mileage_rate_cents_per_mile >= 0),
    currency text NOT NULL DEFAULT 'usd' CHECK (currency = 'usd'),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (driver_ref, period_start, period_end),
    CHECK (period_end > period_start)
);
CREATE INDEX driver_weekly_earnings_period_idx
    ON driver_weekly_earnings_statements (period_start DESC, driver_ref);

CREATE TABLE delivery_message_logs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    statement_id uuid UNIQUE
        REFERENCES driver_weekly_earnings_statements(id) ON DELETE CASCADE,
    recipient_type text NOT NULL CHECK (recipient_type IN ('driver', 'admin', 'customer', 'merchant')),
    recipient_ref text NOT NULL,
    recipient_address text NOT NULL,
    channel text NOT NULL CHECK (channel IN ('email')),
    notification_type text NOT NULL CHECK (notification_type IN ('weekly_payout_statement')),
    status text NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'sending', 'sent', 'failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at timestamptz NOT NULL DEFAULT now(),
    locked_until timestamptz,
    last_error text,
    provider_message_id text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    sent_at timestamptz,
    CHECK ((status = 'sending' AND locked_until IS NOT NULL)
        OR status <> 'sending'),
    CHECK ((status = 'sent' AND sent_at IS NOT NULL)
        OR status <> 'sent')
);
CREATE INDEX delivery_message_logs_queue_idx
    ON delivery_message_logs (available_at, created_at)
    WHERE status IN ('queued', 'failed', 'sending');
CREATE INDEX delivery_message_logs_admin_idx
    ON delivery_message_logs (created_at DESC, id DESC);

CREATE TABLE delivery_message_attempts (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    message_id uuid NOT NULL REFERENCES delivery_message_logs(id) ON DELETE CASCADE,
    status text NOT NULL CHECK (status IN ('sending', 'sent', 'failed')),
    error_code text,
    provider_message_id text,
    started_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CHECK ((status = 'sending' AND completed_at IS NULL)
        OR (status <> 'sending' AND completed_at IS NOT NULL))
);
CREATE INDEX delivery_message_attempts_message_idx
    ON delivery_message_attempts (message_id, started_at, id);
