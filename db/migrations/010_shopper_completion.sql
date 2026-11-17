-- Supervised administrative records only. No enrollment, legal clearance or transfers.
CREATE TABLE shopper_tasks (
    id uuid PRIMARY KEY,
    order_id uuid NOT NULL UNIQUE REFERENCES orders(id),
    shopper_actor text NOT NULL CHECK (shopper_actor ~ '^[A-Za-z0-9_-]{1,100}$'),
    assigned_by text NOT NULL CHECK (length(btrim(assigned_by)) BETWEEN 1 AND 200),
    merchant_id uuid NOT NULL REFERENCES merchants(id),
    customer_ref text NOT NULL,
    completion_amount_cents bigint NOT NULL CHECK (completion_amount_cents BETWEEN 1 AND 100000000),
    currency text NOT NULL DEFAULT 'usd' CHECK (currency = 'usd'),
    jurisdiction_ref text NOT NULL CHECK (jurisdiction_ref ~ '^[A-Za-z0-9_-]{1,100}$'),
    eligibility_status text NOT NULL DEFAULT 'pending_review' CHECK (eligibility_status = 'pending_review'),
    payment_status_at_assignment text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (id, order_id)
);
CREATE TABLE shopper_completions (
    task_id uuid PRIMARY KEY,
    order_id uuid NOT NULL UNIQUE,
    idempotency_key uuid NOT NULL UNIQUE,
    verified_by text NOT NULL CHECK (length(btrim(verified_by)) BETWEEN 1 AND 200),
    items_confirmed boolean NOT NULL CHECK (items_confirmed),
    substitutions_confirmed boolean NOT NULL CHECK (substitutions_confirmed),
    receipt_ref text NOT NULL CHECK (receipt_ref ~ '^[A-Za-z0-9_-]{1,100}$'),
    handoff_ref text NOT NULL CHECK (handoff_ref ~ '^[A-Za-z0-9_-]{1,100}$'),
    payment_status_at_completion text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (task_id, order_id) REFERENCES shopper_tasks(id, order_id)
);
CREATE TABLE shopper_earnings (
    task_id uuid PRIMARY KEY REFERENCES shopper_completions(task_id),
    order_id uuid NOT NULL UNIQUE REFERENCES orders(id),
    shopper_actor text NOT NULL,
    completion_amount_cents bigint NOT NULL CHECK (completion_amount_cents BETWEEN 1 AND 100000000),
    currency text NOT NULL CHECK (currency = 'usd'),
    kind text NOT NULL DEFAULT 'earned_per_order' CHECK (kind = 'earned_per_order'),
    status text NOT NULL DEFAULT 'pending_review' CHECK (status = 'pending_review'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE shopper_task_audit (
    task_id uuid NOT NULL REFERENCES shopper_tasks(id),
    event text NOT NULL CHECK (event IN ('assigned','verified_completed')),
    actor text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (task_id, event)
);
CREATE INDEX shopper_tasks_page ON shopper_tasks(created_at, id);
REVOKE ALL ON shopper_tasks, shopper_completions, shopper_earnings, shopper_task_audit FROM PUBLIC;

CREATE FUNCTION reject_shopper_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'shopper records are append-only' USING ERRCODE = '55000';
END;
$$;
REVOKE ALL ON FUNCTION reject_shopper_mutation() FROM PUBLIC;
CREATE TRIGGER shopper_tasks_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON shopper_tasks
FOR EACH STATEMENT EXECUTE FUNCTION reject_shopper_mutation();
CREATE TRIGGER shopper_completions_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON shopper_completions
FOR EACH STATEMENT EXECUTE FUNCTION reject_shopper_mutation();
CREATE TRIGGER shopper_earnings_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON shopper_earnings
FOR EACH STATEMENT EXECUTE FUNCTION reject_shopper_mutation();
CREATE TRIGGER shopper_audit_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON shopper_task_audit
FOR EACH STATEMENT EXECUTE FUNCTION reject_shopper_mutation();

-- Validate binding against actual checkout/payment tables, never client payer data.
-- Payment failure/refund/missing payment is a review fact, NOT a wage forfeiture gate.
CREATE FUNCTION shopper_order_context(p_order uuid) RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE
    v_order orders%ROWTYPE;
    v_merchant uuid;
    v_checkout unified_checkouts%ROWTYPE;
    v_payment text;
BEGIN
    SELECT * INTO v_order FROM orders WHERE id=p_order FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION 'order not found' USING ERRCODE='P0002'; END IF;
    SELECT merchant_id INTO v_merchant FROM store_locations WHERE id=v_order.store_location_id FOR SHARE;
    IF v_merchant IS NULL THEN RAISE EXCEPTION 'merchant order required' USING ERRCODE='22023'; END IF;
    SELECT c.* INTO v_checkout FROM unified_checkouts c JOIN checkout_orders co ON co.checkout_id=c.id
        WHERE co.order_id=p_order FOR SHARE OF c,co;
    IF FOUND AND v_checkout.customer_ref IS DISTINCT FROM v_order.customer_ref THEN
        RAISE EXCEPTION 'order payer binding conflict' USING ERRCODE='23505';
    END IF;
    SELECT status INTO v_payment FROM checkout_payments WHERE checkout_id=v_checkout.id;
    RETURN jsonb_build_object('merchantId',v_merchant,'customerRef',v_order.customer_ref,
        'orderState',v_order.state,'paymentStatus',COALESCE(v_payment,'not_recorded'));
END;
$$;
REVOKE ALL ON FUNCTION shopper_order_context(uuid) FROM PUBLIC;

CREATE VIEW shopper_task_records AS SELECT t.id, t.created_at,
    jsonb_build_object('id',t.id,'orderId',t.order_id,'shopperActor',t.shopper_actor,
        'assignedBy',t.assigned_by,'merchantId',t.merchant_id,
        'completionAmountCents',t.completion_amount_cents,'currency',t.currency,
        'jurisdictionRef',t.jurisdiction_ref,'eligibilityStatus',t.eligibility_status,
        'paymentStatusAtAssignment',t.payment_status_at_assignment,'createdAt',t.created_at,
        'status',CASE WHEN c.task_id IS NULL THEN 'assigned' ELSE 'verified_completed' END,
        'completion',CASE WHEN c.task_id IS NULL THEN NULL ELSE jsonb_build_object(
            'verifiedBy',c.verified_by,'itemsConfirmed',c.items_confirmed,
            'substitutionsConfirmed',c.substitutions_confirmed,'receiptRef',c.receipt_ref,
            'handoffRef',c.handoff_ref,'paymentStatusAtCompletion',c.payment_status_at_completion,
            'createdAt',c.created_at) END,
        'earnings',CASE WHEN e.task_id IS NULL THEN NULL ELSE jsonb_build_object(
            'kind',e.kind,'status',e.status,'completionAmountCents',e.completion_amount_cents,
            'currency',e.currency,'createdAt',e.created_at) END) AS record
FROM shopper_tasks t LEFT JOIN shopper_completions c ON c.task_id=t.id
LEFT JOIN shopper_earnings e ON e.task_id=t.id;
REVOKE ALL ON shopper_task_records FROM PUBLIC;

CREATE FUNCTION assign_shopper_task(p_key uuid,p_admin text,p_order uuid,p_shopper text,p_amount bigint,p_jurisdiction text)
RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE v shopper_tasks%ROWTYPE; v_context jsonb; v_result jsonb;
BEGIN
    IF p_key IS NULL OR p_order IS NULL OR p_admin IS NULL OR length(btrim(p_admin)) NOT BETWEEN 1 AND 200
       OR p_shopper IS NULL OR p_shopper !~ '^[A-Za-z0-9_-]{1,100}$'
       OR p_jurisdiction IS NULL OR p_jurisdiction !~ '^[A-Za-z0-9_-]{1,100}$'
       OR p_amount IS NULL OR p_amount NOT BETWEEN 1 AND 100000000 THEN
        RAISE EXCEPTION 'invalid shopper assignment' USING ERRCODE='22023'; END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('shopper-assign:'||p_key::text,0));
    SELECT * INTO v FROM shopper_tasks WHERE id=p_key;
    IF FOUND THEN
        IF v.assigned_by<>p_admin OR v.order_id<>p_order OR v.shopper_actor<>p_shopper
           OR v.completion_amount_cents<>p_amount OR v.jurisdiction_ref<>p_jurisdiction THEN
            RAISE EXCEPTION 'assignment idempotency conflict' USING ERRCODE='23505'; END IF;
    ELSE
        v_context := shopper_order_context(p_order);
        IF v_context->>'orderState'='cancelled' THEN
            RAISE EXCEPTION 'cannot assign cancelled order' USING ERRCODE='22023'; END IF;
        INSERT INTO shopper_tasks(id,order_id,shopper_actor,assigned_by,merchant_id,customer_ref,
            completion_amount_cents,jurisdiction_ref,payment_status_at_assignment)
        VALUES(p_key,p_order,p_shopper,p_admin,(v_context->>'merchantId')::uuid,v_context->>'customerRef',
            p_amount,p_jurisdiction,v_context->>'paymentStatus');
        INSERT INTO shopper_task_audit(task_id,event,actor) VALUES(p_key,'assigned',p_admin);
    END IF;
    SELECT record INTO v_result FROM shopper_task_records WHERE id=p_key;
    RETURN v_result;
END;
$$;
REVOKE ALL ON FUNCTION assign_shopper_task(uuid,text,uuid,text,bigint,text) FROM PUBLIC;

CREATE FUNCTION complete_shopper_task(p_key uuid,p_admin text,p_task uuid,p_items boolean,p_substitutions boolean,p_receipt text,p_handoff text)
RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE v shopper_tasks%ROWTYPE; v_old shopper_completions%ROWTYPE; v_context jsonb; v_result jsonb;
BEGIN
    IF p_key IS NULL OR p_task IS NULL OR p_admin IS NULL OR length(btrim(p_admin)) NOT BETWEEN 1 AND 200
       OR p_items IS DISTINCT FROM true OR p_substitutions IS DISTINCT FROM true
       OR p_receipt IS NULL OR p_receipt !~ '^[A-Za-z0-9_-]{1,100}$'
       OR p_handoff IS NULL OR p_handoff !~ '^[A-Za-z0-9_-]{1,100}$' THEN
        RAISE EXCEPTION 'verified evidence required' USING ERRCODE='22023'; END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('shopper-complete:'||p_key::text,0));
    SELECT * INTO v FROM shopper_tasks WHERE id=p_task FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION 'task not found' USING ERRCODE='P0002'; END IF;
    SELECT * INTO v_old FROM shopper_completions WHERE idempotency_key=p_key OR task_id=p_task;
    IF FOUND THEN
        IF v_old.task_id<>p_task OR v_old.idempotency_key<>p_key OR v_old.verified_by<>p_admin
           OR v_old.receipt_ref<>p_receipt OR v_old.handoff_ref<>p_handoff THEN
            RAISE EXCEPTION 'completion idempotency conflict' USING ERRCODE='23505'; END IF;
    ELSE
        v_context := shopper_order_context(v.order_id);
        IF (v_context->>'merchantId')::uuid<>v.merchant_id OR v_context->>'customerRef'<>v.customer_ref THEN
            RAISE EXCEPTION 'assignment ownership changed' USING ERRCODE='23505'; END IF;
        INSERT INTO shopper_completions(task_id,order_id,idempotency_key,verified_by,items_confirmed,
            substitutions_confirmed,receipt_ref,handoff_ref,payment_status_at_completion)
        VALUES(p_task,v.order_id,p_key,p_admin,true,true,p_receipt,p_handoff,v_context->>'paymentStatus');
        INSERT INTO shopper_earnings(task_id,order_id,shopper_actor,completion_amount_cents,currency)
        VALUES(p_task,v.order_id,v.shopper_actor,v.completion_amount_cents,v.currency);
        INSERT INTO shopper_task_audit(task_id,event,actor) VALUES(p_task,'verified_completed',p_admin);
    END IF;
    SELECT record INTO v_result FROM shopper_task_records WHERE id=p_task;
    RETURN v_result;
END;
$$;
REVOKE ALL ON FUNCTION complete_shopper_task(uuid,text,uuid,boolean,boolean,text,text) FROM PUBLIC;

-- Deferred guards prohibit orphaned completion/earnings or forged amounts even
-- for an invoker role with INSERT rights. No mutable payable/paid transition exists.
CREATE FUNCTION validate_shopper_records() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_id uuid; v shopper_tasks%ROWTYPE; c shopper_completions%ROWTYPE; e shopper_earnings%ROWTYPE;
BEGIN
    IF TG_TABLE_NAME='shopper_tasks' THEN v_id:=NEW.id; ELSE v_id:=NEW.task_id; END IF;
    SELECT * INTO v FROM shopper_tasks WHERE id=v_id;
    SELECT * INTO c FROM shopper_completions WHERE task_id=v_id;
    SELECT * INTO e FROM shopper_earnings WHERE task_id=v_id;
    IF NOT EXISTS (SELECT 1 FROM shopper_task_audit WHERE task_id=v_id AND event='assigned' AND actor=v.assigned_by)
       OR ((c.task_id IS NULL) <> (e.task_id IS NULL))
       OR (c.task_id IS NOT NULL AND (e.order_id<>v.order_id OR e.shopper_actor<>v.shopper_actor
           OR e.completion_amount_cents<>v.completion_amount_cents OR e.currency<>v.currency
           OR NOT EXISTS (SELECT 1 FROM shopper_task_audit WHERE task_id=v_id AND event='verified_completed' AND actor=c.verified_by)))
       OR (c.task_id IS NULL AND EXISTS (SELECT 1 FROM shopper_task_audit WHERE task_id=v_id AND event='verified_completed')) THEN
        RAISE EXCEPTION 'inconsistent shopper records' USING ERRCODE='23514'; END IF;
    RETURN NULL;
END;
$$;
REVOKE ALL ON FUNCTION validate_shopper_records() FROM PUBLIC;
CREATE CONSTRAINT TRIGGER shopper_assignment_consistent AFTER INSERT ON shopper_tasks
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION validate_shopper_records();
CREATE CONSTRAINT TRIGGER shopper_completion_consistent AFTER INSERT ON shopper_completions
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION validate_shopper_records();
CREATE CONSTRAINT TRIGGER shopper_earnings_consistent AFTER INSERT ON shopper_earnings
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION validate_shopper_records();
CREATE CONSTRAINT TRIGGER shopper_audit_consistent AFTER INSERT ON shopper_task_audit
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION validate_shopper_records();