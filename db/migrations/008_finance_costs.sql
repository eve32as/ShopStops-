-- Manual costs only: no wage entitlement, price approval, cash movement or payout status.
CREATE TABLE finance_costs (
    id uuid PRIMARY KEY,
    actor text NOT NULL CHECK (length(btrim(actor)) BETWEEN 1 AND 200),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    period text NOT NULL CHECK (period ~ '^(20[0-9]{2}|2100)-(0[1-9]|1[0-2])$'),
    category text NOT NULL CHECK (category IN ('driver','staff','hosting','processing_excess','refund_loss','other')),
    amount_cents bigint NOT NULL CHECK (amount_cents BETWEEN 0 AND 100000000)
);
CREATE INDEX finance_costs_period_page ON finance_costs (period, created_at, id);
REVOKE ALL ON finance_costs FROM PUBLIC;

CREATE FUNCTION reject_finance_cost_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'manual finance costs are append-only' USING ERRCODE = '55000';
END;
$$;
REVOKE ALL ON FUNCTION reject_finance_cost_mutation() FROM PUBLIC;
CREATE TRIGGER finance_costs_no_mutation
BEFORE UPDATE OR DELETE OR TRUNCATE ON finance_costs
FOR EACH STATEMENT EXECUTE FUNCTION reject_finance_cost_mutation();

-- Invoker security: deploy operator must grant the dedicated API role only
-- SELECT, INSERT on this table and EXECUTE on this function; never PUBLIC.
CREATE FUNCTION record_finance_cost(p_id uuid,p_actor text,p_period text,p_category text,p_amount bigint)
RETURNS SETOF finance_costs LANGUAGE plpgsql AS $$
DECLARE
    v finance_costs%ROWTYPE;
BEGIN
    IF p_id IS NULL OR p_actor IS NULL OR length(btrim(p_actor)) NOT BETWEEN 1 AND 200
       OR p_period IS NULL OR p_period !~ '^(20[0-9]{2}|2100)-(0[1-9]|1[0-2])$'
       OR p_category IS NULL OR p_category NOT IN ('driver','staff','hosting','processing_excess','refund_loss','other')
       OR p_amount IS NULL OR p_amount NOT BETWEEN 0 AND 100000000 THEN
        RAISE EXCEPTION 'invalid manual finance cost' USING ERRCODE = '22023';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('finance-cost:' || p_id::text,0));
    SELECT * INTO v FROM finance_costs WHERE id=p_id;
    IF FOUND THEN
        IF v.actor<>p_actor OR v.period<>p_period OR v.category<>p_category OR v.amount_cents<>p_amount THEN
            RAISE EXCEPTION 'finance cost idempotency conflict' USING ERRCODE = '23505';
        END IF;
        RETURN NEXT v;
        RETURN;
    END IF;
    INSERT INTO finance_costs(id,actor,period,category,amount_cents)
    VALUES(p_id,p_actor,p_period,p_category,p_amount) RETURNING * INTO v;
    RETURN NEXT v;
END;
$$;
REVOKE ALL ON FUNCTION record_finance_cost(uuid,text,text,text,bigint) FROM PUBLIC;