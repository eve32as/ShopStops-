\set ON_ERROR_STOP on
BEGIN;
-- Synthetic quotes and ledgers only; no external Stripe calls or worker pay.
DO $$
DECLARE
    v_zone uuid; v_merchant uuid; v_retail uuid; v_anchor uuid; v_store uuid;
    v_orders uuid[]; v_order uuid; v_id uuid; v_retry uuid;
    v_snapshot checkout_extra_pickup_snapshots%ROWTYPE;
    v_total integer; v_fee integer; v_extra integer;
    v_enabled boolean; v_priority boolean; v_rejected boolean;
    v_refund checkout_refunds%ROWTYPE;
    i integer; j integer;
BEGIN
    INSERT INTO delivery_zones(name,boundary) VALUES('extra pickup fixture',
        ST_Multi(ST_MakeEnvelope(-77.05,38.85,-76.95,38.95,4326))) RETURNING id INTO v_zone;
    INSERT INTO jurisdictions(code,name,boundary) VALUES('US-DC','extra pickup DC',
        ST_Multi(ST_MakeEnvelope(-77.12,38.79,-76.91,39.0,4326)));
    INSERT INTO merchants(name,merchant_type,stripe_account_id)
        VALUES('extra fixture anchor','restaurant','acct_extra_anchor') RETURNING id INTO v_merchant;
    INSERT INTO merchants(name,merchant_type,stripe_account_id)
        VALUES('extra fixture retailer','retail','acct_extra_retail') RETURNING id INTO v_retail;
    INSERT INTO merchant_subscriptions(merchant_id,status) VALUES(v_retail,'active');
    INSERT INTO store_locations(name,address,location,merchant_id)
        VALUES('extra anchor','fixture',ST_SetSRID(ST_MakePoint(-76.99,38.91),4326)::geography,v_merchant)
        RETURNING id INTO v_anchor;
    INSERT INTO store_locations(name,address,location,merchant_id)
        VALUES('extra retail','fixture',ST_SetSRID(ST_MakePoint(-76.989,38.91),4326)::geography,v_retail)
        RETURNING id INTO v_store;

    FOR i IN 1..5 LOOP
        v_enabled := i IN (2,3,4);
        v_priority := i IN (3,5);
        v_extra := CASE WHEN i=4 THEN 0 ELSE 1 END;
        v_fee := CASE WHEN v_enabled AND v_extra=1 THEN 13 ELSE 0 END;
        v_orders := ARRAY[]::uuid[];
        FOR j IN 1..3 LOOP
            INSERT INTO orders(customer_ref,store_location_id,delivery_zone_id,dropoff_location,
                delivery_window_start,delivery_window_end,volume_liters,state,item_subtotal_cents)
            VALUES('extra-customer',CASE WHEN j=1 OR i=4 THEN v_anchor ELSE v_store END,v_zone,
                ST_SetSRID(ST_MakePoint(-76.98,38.91),4326)::geography,now(),now()+interval '2 hours',
                1,'placed',CASE WHEN j=3 THEN 1000 ELSE 2000 END) RETURNING id INTO v_order;
            v_orders := array_append(v_orders,v_order);
        END LOOP;
        v_id := create_unified_checkout('extra-customer',v_anchor,v_orders,38.91,-76.98,
            now(),now()+interval '2 hours',311,89,'extra-sql-'||i,v_priority,true,137,263,v_enabled);
        SELECT * INTO v_snapshot FROM checkout_extra_pickup_snapshots WHERE checkout_id=v_id;
        SELECT sum(amount_cents)::integer INTO v_total FROM checkout_invoice_lines WHERE checkout_id=v_id;
        IF v_snapshot.test_only <> v_enabled OR v_snapshot.merchandise_subtotal_cents <> 5000
           OR v_snapshot.additional_pickups <> v_extra OR v_snapshot.rate_basis_points <> 25
           OR v_snapshot.amount_cents <> v_fee OR v_snapshot.invoice_total_cents <> v_total
           OR v_total <> 5420+v_fee+CASE WHEN v_priority THEN 400 ELSE 0 END
           OR (SELECT count(*) FROM checkout_invoice_lines WHERE checkout_id=v_id AND line_type='extra_pickup_platform_fee')
                <> CASE WHEN v_fee>0 THEN 1 ELSE 0 END THEN
            RAISE EXCEPTION 'server quote, distinct count, basis or total incorrect for case %',i;
        END IF;
        IF (SELECT invoice_total_cents FROM checkout_priority_snapshots WHERE checkout_id=v_id) <> v_total THEN
            RAISE EXCEPTION 'priority snapshot froze before extra fee';
        END IF;
        v_retry := create_unified_checkout('extra-customer',v_anchor,v_orders,38.91,-76.98,
            now(),now()+interval '2 hours',999,888,'extra-sql-'||i,v_priority,false,555,666,NOT v_enabled);
        IF v_retry <> v_id OR (SELECT sum(amount_cents) FROM checkout_invoice_lines WHERE checkout_id=v_id) <> v_total
           OR (SELECT test_only FROM checkout_extra_pickup_snapshots WHERE checkout_id=v_id) <> v_enabled THEN
            RAISE EXCEPTION 'retry repriced or changed feature snapshot';
        END IF;
        v_rejected:=false;
        BEGIN PERFORM create_unified_checkout('wrong-customer',v_anchor,v_orders,38.91,-76.98,
            now(),now()+interval '2 hours',311,89,'extra-sql-'||i,v_priority,true,137,263,v_enabled);
        EXCEPTION WHEN raise_exception THEN v_rejected:=true; END;
        IF NOT v_rejected THEN RAISE EXCEPTION 'retry customer ownership bypass'; END IF;
        v_rejected:=false;
        BEGIN UPDATE checkout_extra_pickup_snapshots SET additional_pickups=0 WHERE checkout_id=v_id;
        EXCEPTION WHEN raise_exception THEN v_rejected:=true; END;
        IF NOT v_rejected THEN RAISE EXCEPTION 'snapshot update accepted'; END IF;
        v_rejected:=false;
        BEGIN DELETE FROM checkout_extra_pickup_snapshots WHERE checkout_id=v_id;
        EXCEPTION WHEN raise_exception THEN v_rejected:=true; END;
        IF NOT v_rejected THEN RAISE EXCEPTION 'snapshot delete accepted'; END IF;
        IF v_enabled THEN
            v_rejected:=false;
            BEGIN UPDATE checkout_invoice_lines SET amount_cents=amount_cents+1
                WHERE checkout_id=v_id AND line_type='service_fee';
            EXCEPTION WHEN raise_exception THEN v_rejected:=true; END;
            IF NOT v_rejected THEN RAISE EXCEPTION 'test invoice update accepted'; END IF;
            v_rejected:=false;
            BEGIN INSERT INTO checkout_invoice_lines(checkout_id,line_type,description,amount_cents)
                VALUES(v_id,'service_fee','late line',1);
            EXCEPTION WHEN raise_exception THEN v_rejected:=true; END;
            IF NOT v_rejected THEN RAISE EXCEPTION 'test invoice insertion accepted'; END IF;
        END IF;
        IF i=3 THEN
            v_rejected:=false;
            BEGIN PERFORM record_checkout_payment(v_id,'pi_extra_fixture',v_total-v_fee,0,'requires_payment_method');
            EXCEPTION WHEN raise_exception THEN v_rejected:=true; END;
            IF NOT v_rejected THEN RAISE EXCEPTION 'payment omitted extra fee'; END IF;
            PERFORM record_checkout_payment(v_id,'pi_extra_fixture',v_total,0,'requires_payment_method');
            PERFORM process_stripe_checkout_event('evt_extra_fixture','payment_intent.succeeded','{}'::jsonb,
                v_id,'pi_extra_fixture',v_total,'usd','succeeded',50,'ch_extra_fixture');
            IF (SELECT sum(item_gross_cents) FROM connect_transfers WHERE checkout_id=v_id) <> 5000
               OR (SELECT sum(transfer_amount_cents) FROM connect_transfers WHERE checkout_id=v_id) <> 4950 THEN
                RAISE EXCEPTION 'platform fee paid to merchants';
            END IF;
            v_refund:=reserve_checkout_refund(v_id,'fixture-admin','extra-refund','requested_by_customer');
            IF v_refund.amount_cents <> v_total THEN RAISE EXCEPTION 'refund omitted surcharge'; END IF;
        END IF;
    END LOOP;
    -- Pre-009 existing checkouts still get the legacy standard priority
    -- snapshot, but no extra fee/snapshot may be backfilled on a retry.
    INSERT INTO orders(customer_ref,store_location_id,delivery_zone_id,dropoff_location,
        delivery_window_start,delivery_window_end,volume_liters,state,item_subtotal_cents)
    VALUES('extra-customer',v_anchor,v_zone,ST_SetSRID(ST_MakePoint(-76.98,38.91),4326)::geography,
        now(),now()+interval '2 hours',1,'placed',5000) RETURNING id INTO v_order;
    v_id:=create_unified_checkout_base('extra-customer',v_anchor,ARRAY[v_order],38.91,-76.98,
        now(),now()+interval '2 hours',311,89,'extra-legacy');
    v_retry:=create_unified_checkout('extra-customer',v_anchor,ARRAY[v_order],38.91,-76.98,
        now(),now()+interval '2 hours',999,888,'extra-legacy',false,false,0,0,true);
    IF v_retry <> v_id OR EXISTS(SELECT 1 FROM checkout_extra_pickup_snapshots WHERE checkout_id=v_id)
       OR NOT EXISTS(SELECT 1 FROM checkout_priority_snapshots WHERE checkout_id=v_id AND NOT selected)
       OR (SELECT sum(amount_cents) FROM checkout_invoice_lines WHERE checkout_id=v_id) <> 5420 THEN
        RAISE EXCEPTION 'legacy retry failed or backfilled pricing';
    END IF;
    -- Deferred checks reject a fee on a disabled invoice even without priority.
    v_rejected:=false;
    BEGIN
        SELECT id INTO v_id FROM unified_checkouts WHERE idempotency_key='extra-sql-1';
        INSERT INTO checkout_invoice_lines(checkout_id,line_type,description,amount_cents)
            VALUES(v_id,'extra_pickup_platform_fee','forged',13);
        SET CONSTRAINTS ALL IMMEDIATE;
    EXCEPTION WHEN raise_exception THEN v_rejected:=true; END;
    IF NOT v_rejected THEN RAISE EXCEPTION 'disabled fee committed'; END IF;
    -- Invoker-only functions and no PUBLIC table or function grant.
    IF EXISTS(SELECT 1 FROM pg_class c, LATERAL aclexplode(COALESCE(c.relacl,acldefault('r',c.relowner))) a
        WHERE c.oid='checkout_extra_pickup_snapshots'::regclass AND a.grantee=0)
       OR EXISTS(SELECT 1 FROM pg_proc p, LATERAL aclexplode(COALESCE(p.proacl,acldefault('f',p.proowner))) a
        WHERE p.proname='create_unified_checkout' AND (a.grantee=0 OR p.prosecdef)) THEN
        RAISE EXCEPTION 'unexpected public privileges or definer security';
    END IF;
END;
$$;
SET CONSTRAINTS ALL IMMEDIATE;
ROLLBACK;