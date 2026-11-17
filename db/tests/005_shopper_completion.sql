\set ON_ERROR_STOP on
BEGIN;
-- Synthetic supervised records. No identity clearance, actual work or transfers.
DO $$
DECLARE
    v_zone uuid; v_store uuid; v_merchant uuid; v_order uuid; v_other uuid; v_orphan_order uuid;
    v_checkout uuid; v_task uuid:=gen_random_uuid(); v_key uuid:=gen_random_uuid();
    v_result jsonb; v_rejected boolean; v_count integer;
BEGIN
    INSERT INTO delivery_zones(name,boundary) VALUES('shopper fixture zone',
        ST_Multi(ST_MakeEnvelope(-77.05,38.85,-76.95,38.95,4326))) RETURNING id INTO v_zone;
    INSERT INTO merchants(name,merchant_type,stripe_account_id)
        VALUES('shopper fixture merchant','restaurant','acct_shopper_fixture') RETURNING id INTO v_merchant;
    INSERT INTO store_locations(name,address,location,merchant_id)
        VALUES('shopper fixture store','fixture',ST_SetSRID(ST_MakePoint(-76.99,38.91),4326)::geography,v_merchant)
        RETURNING id INTO v_store;
    INSERT INTO orders(customer_ref,store_location_id,delivery_zone_id,dropoff_location,
        delivery_window_start,delivery_window_end,volume_liters,item_subtotal_cents)
        VALUES('shopper-fixture-customer',v_store,v_zone,ST_SetSRID(ST_MakePoint(-76.98,38.91),4326)::geography,
            now(),now()+interval '2 hours',1,1200) RETURNING id INTO v_order;
    INSERT INTO orders(customer_ref,store_location_id,delivery_zone_id,dropoff_location,
        delivery_window_start,delivery_window_end,volume_liters,item_subtotal_cents)
        VALUES('shopper-fixture-customer',v_store,v_zone,ST_SetSRID(ST_MakePoint(-76.98,38.91),4326)::geography,
            now(),now()+interval '2 hours',1,1200) RETURNING id INTO v_other;
    v_checkout:=create_unified_checkout('shopper-fixture-customer',v_store,ARRAY[v_order],38.91,-76.98,
        now(),now()+interval '2 hours',311,89,'shopper-sql-checkout');
    INSERT INTO checkout_payments(checkout_id,stripe_payment_intent_id,gross_amount_cents,status)
        SELECT v_checkout,'pi_shopper_sql_fixture',sum(amount_cents),'failed'
        FROM checkout_invoice_lines WHERE checkout_id=v_checkout;

    v_rejected:=false;
    BEGIN PERFORM assign_shopper_task(v_task,'admin',v_order,'opaque_worker',0,'US-DC');
    EXCEPTION WHEN invalid_parameter_value THEN v_rejected:=true; END;
    IF NOT v_rejected THEN RAISE EXCEPTION 'unapproved zero amount accepted'; END IF;
    v_rejected:=false;
    BEGIN PERFORM assign_shopper_task(v_task,'admin',gen_random_uuid(),'opaque_worker',501,'US-DC');
    EXCEPTION WHEN no_data_found THEN v_rejected:=true; END;
    IF NOT v_rejected THEN RAISE EXCEPTION 'unknown order accepted'; END IF;

    v_result:=assign_shopper_task(v_task,'admin',v_order,'opaque_worker',501,'US-DC');
    IF v_result->>'status'<>'assigned' OR v_result->'earnings'<>'null'::jsonb
       OR v_result->>'eligibilityStatus'<>'pending_review'
       OR v_result->>'paymentStatusAtAssignment'<>'failed'
       OR EXISTS(SELECT 1 FROM shopper_earnings WHERE task_id=v_task) THEN
        RAISE EXCEPTION 'assignment created earnings or false clearance'; END IF;
    PERFORM assign_shopper_task(v_task,'admin',v_order,'opaque_worker',501,'US-DC');
    FOR v_count IN 1..5 LOOP
        v_rejected:=false;
        BEGIN
            PERFORM assign_shopper_task(v_task,CASE WHEN v_count=1 THEN 'other' ELSE 'admin' END,
                CASE WHEN v_count=2 THEN v_other ELSE v_order END,
                CASE WHEN v_count=3 THEN 'other_worker' ELSE 'opaque_worker' END,
                CASE WHEN v_count=4 THEN 502 ELSE 501 END,
                CASE WHEN v_count=5 THEN 'US-NY' ELSE 'US-DC' END);
        EXCEPTION WHEN unique_violation THEN v_rejected:=true; END;
        IF NOT v_rejected THEN RAISE EXCEPTION 'assignment retry payload/actor conflict accepted'; END IF;
    END LOOP;
    v_rejected:=false;
    BEGIN PERFORM assign_shopper_task(gen_random_uuid(),'admin',v_order,'other_worker',999,'US-DC');
    EXCEPTION WHEN unique_violation THEN v_rejected:=true; END;
    IF NOT v_rejected THEN RAISE EXCEPTION 'order reassignment accepted'; END IF;
    v_rejected:=false;
    BEGIN PERFORM complete_shopper_task(v_key,'verifier',v_task,false,true,'r1','h1');
    EXCEPTION WHEN invalid_parameter_value THEN v_rejected:=true; END;
    IF NOT v_rejected OR EXISTS(SELECT 1 FROM shopper_completions WHERE task_id=v_task) THEN
        RAISE EXCEPTION 'unverified completion persisted'; END IF;
    v_rejected:=false;
    BEGIN PERFORM complete_shopper_task(v_key,'verifier',v_task,true,true,'https://proof','h1');
    EXCEPTION WHEN invalid_parameter_value THEN v_rejected:=true; END;
    IF NOT v_rejected THEN RAISE EXCEPTION 'raw evidence URL accepted'; END IF;

    -- Payer binding is enforced independently of payment success.
    UPDATE unified_checkouts SET customer_ref='wrong-payer' WHERE id=v_checkout;
    v_rejected:=false;
    BEGIN PERFORM complete_shopper_task(v_key,'verifier',v_task,true,true,'r1','h1');
    EXCEPTION WHEN unique_violation THEN v_rejected:=true; END;
    IF NOT v_rejected OR EXISTS(SELECT 1 FROM shopper_earnings WHERE task_id=v_task) THEN
        RAISE EXCEPTION 'payer conflict created completion/earnings'; END IF;
    UPDATE unified_checkouts SET customer_ref='shopper-fixture-customer' WHERE id=v_checkout;
    -- Cancellation after assignment does not erase verified work compensation.
    UPDATE orders SET state='cancelled' WHERE id=v_order;
    v_result:=complete_shopper_task(v_key,'verifier',v_task,true,true,'r1','h1');
    IF v_result->>'status'<>'verified_completed' OR v_result->'earnings'->>'status'<>'pending_review'
       OR v_result->'earnings'->>'kind'<>'earned_per_order'
       OR (v_result->'earnings'->>'completionAmountCents')::bigint<>501
       OR v_result->'completion'->>'paymentStatusAtCompletion'<>'failed' THEN
        RAISE EXCEPTION 'verified completion snapshot not exact'; END IF;
    IF complete_shopper_task(v_key,'verifier',v_task,true,true,'r1','h1')<>v_result THEN
        RAISE EXCEPTION 'retry changed immutable completion'; END IF;
    FOR v_count IN 1..4 LOOP
        v_rejected:=false;
        BEGIN PERFORM complete_shopper_task(CASE WHEN v_count=1 THEN gen_random_uuid() ELSE v_key END,
            CASE WHEN v_count=2 THEN 'other-verifier' ELSE 'verifier' END,v_task,true,true,
            CASE WHEN v_count=3 THEN 'r2' ELSE 'r1' END,CASE WHEN v_count=4 THEN 'h2' ELSE 'h1' END);
        EXCEPTION WHEN unique_violation THEN v_rejected:=true; END;
        IF NOT v_rejected THEN RAISE EXCEPTION 'completion key/actor/evidence conflict accepted'; END IF;
    END LOOP;
    IF (SELECT count(*) FROM shopper_earnings WHERE order_id=v_order)<>1
       OR (SELECT count(*) FROM shopper_task_audit WHERE task_id=v_task)<>2 THEN
        RAISE EXCEPTION 'duplicated earnings or missing transition audit'; END IF;
    -- A key cannot be replayed against another task/order. Missing customer
    -- payment is still a review fact, not a gate to recording verified work.
    v_result:=assign_shopper_task(gen_random_uuid(),'admin',v_other,'opaque_other_worker',703,'US-DC');
    v_rejected:=false;
    BEGIN PERFORM complete_shopper_task(v_key,'verifier',(v_result->>'id')::uuid,true,true,'r1','h1');
    EXCEPTION WHEN unique_violation THEN v_rejected:=true; END;
    IF NOT v_rejected THEN RAISE EXCEPTION 'completion key moved to another order'; END IF;
    v_result:=complete_shopper_task(gen_random_uuid(),'verifier',(v_result->>'id')::uuid,true,true,'r2','h2');
    IF v_result->'completion'->>'paymentStatusAtCompletion'<>'not_recorded'
       OR (v_result->'earnings'->>'completionAmountCents')::bigint<>703 THEN
        RAISE EXCEPTION 'missing payment withheld completion or amount changed'; END IF;
    v_rejected:=false;
    BEGIN UPDATE shopper_tasks SET completion_amount_cents=999 WHERE id=v_task;
    EXCEPTION WHEN object_not_in_prerequisite_state THEN v_rejected:=true; END;
    IF NOT v_rejected THEN RAISE EXCEPTION 'amount mutated'; END IF;
    v_rejected:=false;
    BEGIN DELETE FROM shopper_completions WHERE task_id=v_task;
    EXCEPTION WHEN object_not_in_prerequisite_state THEN v_rejected:=true; END;
    IF NOT v_rejected THEN RAISE EXCEPTION 'completion deleted'; END IF;
    v_rejected:=false;
    BEGIN UPDATE shopper_earnings SET status='paid' WHERE task_id=v_task;
    EXCEPTION WHEN object_not_in_prerequisite_state THEN v_rejected:=true; END;
    IF NOT v_rejected THEN RAISE EXCEPTION 'paid transition allowed'; END IF;
    v_rejected:=false;
    BEGIN TRUNCATE shopper_task_audit;
    EXCEPTION WHEN object_not_in_prerequisite_state THEN v_rejected:=true; END;
    IF NOT v_rejected THEN RAISE EXCEPTION 'audit truncated'; END IF;
    -- Deferred guards catch internally inconsistent ledgers in the same
    -- transaction, not just mutation of existing rows.
    v_rejected:=false;
    BEGIN
        INSERT INTO orders(customer_ref,store_location_id,delivery_zone_id,dropoff_location,
            delivery_window_start,delivery_window_end,volume_liters,item_subtotal_cents)
        SELECT customer_ref,store_location_id,delivery_zone_id,dropoff_location,
            delivery_window_start,delivery_window_end,volume_liters,item_subtotal_cents
        FROM orders WHERE id=v_other RETURNING id INTO v_orphan_order;
        INSERT INTO shopper_tasks(id,order_id,shopper_actor,assigned_by,merchant_id,customer_ref,
            completion_amount_cents,jurisdiction_ref,payment_status_at_assignment)
        VALUES(gen_random_uuid(),v_orphan_order,'opaque_worker','admin',v_merchant,'customer',501,'US-DC','not_recorded');
        SET CONSTRAINTS ALL IMMEDIATE;
    EXCEPTION WHEN check_violation THEN v_rejected:=true; END;
    IF NOT v_rejected THEN RAISE EXCEPTION 'orphaned assignment committed'; END IF;
    INSERT INTO orders(customer_ref,store_location_id,delivery_zone_id,dropoff_location,
        delivery_window_start,delivery_window_end,volume_liters,item_subtotal_cents)
    SELECT customer_ref,store_location_id,delivery_zone_id,dropoff_location,
        delivery_window_start,delivery_window_end,volume_liters,item_subtotal_cents
    FROM orders WHERE id=v_other RETURNING id INTO v_orphan_order;
    v_result:=assign_shopper_task(gen_random_uuid(),'admin',v_orphan_order,'opaque_worker',801,'US-DC');
    v_rejected:=false;
    BEGIN
        INSERT INTO shopper_completions(task_id,order_id,idempotency_key,verified_by,items_confirmed,
            substitutions_confirmed,receipt_ref,handoff_ref,payment_status_at_completion)
        VALUES((v_result->>'id')::uuid,v_orphan_order,gen_random_uuid(),'verifier',true,true,'r3','h3','not_recorded');
        INSERT INTO shopper_task_audit(task_id,event,actor)
        VALUES((v_result->>'id')::uuid,'verified_completed','verifier');
        INSERT INTO shopper_earnings(task_id,order_id,shopper_actor,completion_amount_cents,currency)
        VALUES((v_result->>'id')::uuid,v_orphan_order,'opaque_worker',999,'usd');
        SET CONSTRAINTS ALL IMMEDIATE;
    EXCEPTION WHEN check_violation THEN v_rejected:=true; END;
    IF NOT v_rejected OR EXISTS(SELECT 1 FROM shopper_completions WHERE order_id=v_orphan_order)
       OR EXISTS(SELECT 1 FROM shopper_earnings WHERE order_id=v_orphan_order) THEN
        RAISE EXCEPTION 'forged amount committed or partial completion persisted'; END IF;
    v_rejected:=false;
    BEGIN
        INSERT INTO shopper_completions(task_id,order_id,idempotency_key,verified_by,items_confirmed,
            substitutions_confirmed,receipt_ref,handoff_ref,payment_status_at_completion)
        VALUES((v_result->>'id')::uuid,v_orphan_order,gen_random_uuid(),'verifier',true,true,'r3','h3','not_recorded');
        SET CONSTRAINTS ALL IMMEDIATE;
    EXCEPTION WHEN check_violation THEN v_rejected:=true; END;
    IF NOT v_rejected THEN RAISE EXCEPTION 'completion without earnings/audit committed'; END IF;
    IF EXISTS (SELECT 1 FROM pg_class c CROSS JOIN LATERAL aclexplode(COALESCE(c.relacl,acldefault('r',c.relowner))) a
        WHERE c.oid IN ('shopper_tasks'::regclass,'shopper_completions'::regclass,
            'shopper_earnings'::regclass,'shopper_task_audit'::regclass,'shopper_task_records'::regclass)
          AND a.grantee=0) THEN RAISE EXCEPTION 'PUBLIC shopper relation privilege'; END IF;
    IF EXISTS (SELECT 1 FROM pg_proc p CROSS JOIN LATERAL aclexplode(COALESCE(p.proacl,acldefault('f',p.proowner))) a
        WHERE p.oid IN ('assign_shopper_task(uuid,text,uuid,text,bigint,text)'::regprocedure,
            'complete_shopper_task(uuid,text,uuid,boolean,boolean,text,text)'::regprocedure)
          AND a.grantee=0 AND a.privilege_type='EXECUTE') THEN
        RAISE EXCEPTION 'PUBLIC shopper mutation privilege'; END IF;
END;
$$;
SET CONSTRAINTS ALL IMMEDIATE;
ROLLBACK;