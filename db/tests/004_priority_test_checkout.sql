\set ON_ERROR_STOP on
BEGIN;

-- Synthetic cents only. This fixture does not approve or configure rates.
DO $$
DECLARE
    v_zone uuid;
    v_merchant uuid;
    v_store uuid;
    v_order uuid;
    v_other uuid;
    v_standard_order uuid;
    v_checkout uuid;
    v_standard uuid;
    v_retry uuid;
    v_refund checkout_refunds%ROWTYPE;
    v_rejected boolean;
    v_total integer;
BEGIN
    INSERT INTO delivery_zones (name, boundary)
    VALUES ('priority fixture zone', ST_Multi(ST_MakeEnvelope(-77.05,38.85,-76.95,38.95,4326))) RETURNING id INTO v_zone;
    INSERT INTO jurisdictions (code,name,boundary)
    VALUES ('US-DC','priority fixture DC',ST_Multi(ST_MakeEnvelope(-77.12,38.79,-76.91,39.0,4326)));
    INSERT INTO merchants (name,merchant_type,stripe_account_id)
    VALUES ('priority fixture restaurant','restaurant','acct_priority_fixture') RETURNING id INTO v_merchant;
    INSERT INTO store_locations (name,address,location,merchant_id)
    VALUES ('priority fixture store','fixture',ST_SetSRID(ST_MakePoint(-76.99,38.91),4326)::geography,v_merchant) RETURNING id INTO v_store;
    INSERT INTO orders (customer_ref,store_location_id,delivery_zone_id,dropoff_location,
        delivery_window_start,delivery_window_end,volume_liters,state,item_subtotal_cents)
    VALUES ('priority-customer',v_store,v_zone,ST_SetSRID(ST_MakePoint(-76.98,38.91),4326)::geography,
        now(),now()+interval '2 hours',1,'placed',1200) RETURNING id INTO v_order;
    INSERT INTO orders (customer_ref,store_location_id,delivery_zone_id,dropoff_location,
        delivery_window_start,delivery_window_end,volume_liters,state,item_subtotal_cents)
    VALUES ('other-customer',v_store,v_zone,ST_SetSRID(ST_MakePoint(-76.98,38.91),4326)::geography,
        now(),now()+interval '2 hours',1,'placed',1200) RETURNING id INTO v_other;
    INSERT INTO orders (customer_ref,store_location_id,delivery_zone_id,dropoff_location,
        delivery_window_start,delivery_window_end,volume_liters,state,item_subtotal_cents)
    VALUES ('priority-customer',v_store,v_zone,ST_SetSRID(ST_MakePoint(-76.98,38.91),4326)::geography,
        now(),now()+interval '2 hours',1,'placed',1200) RETURNING id INTO v_standard_order;

    v_rejected := false;
    BEGIN
        PERFORM create_unified_checkout('priority-customer',v_store,ARRAY[v_order],38.91,-76.98,
            now(),now()+interval '2 hours',311,89,'priority-disabled',true,false,137,263);
    EXCEPTION WHEN raise_exception THEN v_rejected := SQLERRM = 'priority service is unavailable'; END;
    IF NOT v_rejected OR EXISTS (SELECT 1 FROM unified_checkouts WHERE idempotency_key='priority-disabled') THEN
        RAISE EXCEPTION 'disabled choice must fail without creating checkout'; END IF;

    v_rejected := false;
    BEGIN
        PERFORM create_unified_checkout('priority-customer',v_store,ARRAY[v_order],38.91,-76.98,
            now(),now()+interval '2 hours',311,89,'priority-invalid',true,true,-1,263);
    EXCEPTION WHEN raise_exception THEN v_rejected := true; END;
    IF NOT v_rejected THEN RAISE EXCEPTION 'negative rate accepted'; END IF;
    v_rejected := false;
    BEGIN
        PERFORM create_unified_checkout('priority-customer',v_store,ARRAY[v_order],38.91,-76.98,
            now(),now()+interval '2 hours',311,89,'priority-invalid',true,true,137,1000001);
    EXCEPTION WHEN raise_exception THEN v_rejected := true; END;
    IF NOT v_rejected THEN RAISE EXCEPTION 'unbounded rate accepted'; END IF;
    v_rejected := false;
    BEGIN
        PERFORM create_unified_checkout('priority-customer',v_store,ARRAY[v_other],38.91,-76.98,
            now(),now()+interval '2 hours',311,89,'priority-other',true,true,137,263);
    EXCEPTION WHEN raise_exception THEN v_rejected := true; END;
    IF NOT v_rejected OR EXISTS (SELECT 1 FROM unified_checkouts WHERE idempotency_key='priority-other') THEN
        RAISE EXCEPTION 'other customer order was accepted or partially persisted'; END IF;

    v_checkout := create_unified_checkout('priority-customer',v_store,ARRAY[v_order],38.91,-76.98,
        now(),now()+interval '2 hours',311,89,'priority-fixture',true,true,137,263);
    SELECT sum(amount_cents)::integer INTO v_total FROM checkout_invoice_lines WHERE checkout_id=v_checkout;
    IF v_total <> 2020 OR NOT EXISTS (SELECT 1 FROM checkout_priority_snapshots WHERE checkout_id=v_checkout
        AND selected AND test_only AND shopping_cents=137 AND delivery_cents=263
        AND shopper_allocation_cents=137 AND driver_allocation_cents=263
        AND allocation_status='pending_fulfillment' AND invoice_total_cents=2020) THEN
        RAISE EXCEPTION 'priority quote/snapshot/allocations not exact'; END IF;
    v_retry := create_unified_checkout('priority-customer',v_store,ARRAY[v_order],38.91,-76.98,
        now(),now()+interval '2 hours',999,888,'priority-fixture',true,false,555,666);
    IF v_retry <> v_checkout OR (SELECT sum(amount_cents) FROM checkout_invoice_lines WHERE checkout_id=v_checkout) <> 2020 THEN
        RAISE EXCEPTION 'retry repriced or duplicated lines'; END IF;
    v_rejected := false;
    BEGIN
        PERFORM create_unified_checkout('priority-customer',v_store,ARRAY[v_order],38.91,-76.98,
            now(),now()+interval '2 hours',311,89,'priority-fixture',false,false,0,0);
    EXCEPTION WHEN raise_exception THEN v_rejected := true; END;
    IF NOT v_rejected THEN RAISE EXCEPTION 'changed choice retry accepted'; END IF;
    v_rejected := false;
    BEGIN
        PERFORM create_unified_checkout('other-customer',v_store,ARRAY[v_order],38.91,-76.98,
            now(),now()+interval '2 hours',311,89,'priority-fixture',true,true,137,263);
    EXCEPTION WHEN raise_exception THEN v_rejected := true; END;
    IF NOT v_rejected THEN RAISE EXCEPTION 'retry customer ownership bypass'; END IF;
    v_rejected := false;
    BEGIN UPDATE checkout_priority_snapshots SET shopper_allocation_cents=0 WHERE checkout_id=v_checkout;
    EXCEPTION WHEN raise_exception THEN v_rejected := true; END;
    IF NOT v_rejected THEN RAISE EXCEPTION 'snapshot mutated'; END IF;
    v_rejected := false;
    BEGIN DELETE FROM checkout_invoice_lines WHERE checkout_id=v_checkout AND line_type='priority_delivery';
    EXCEPTION WHEN raise_exception THEN v_rejected := true; END;
    IF NOT v_rejected THEN RAISE EXCEPTION 'priority invoice mutated'; END IF;

    v_standard := create_unified_checkout('priority-customer',v_store,ARRAY[v_standard_order],38.91,-76.98,
        now(),now()+interval '2 hours',311,89,'priority-standard',false,true,137,263);
    IF EXISTS (SELECT 1 FROM checkout_invoice_lines WHERE checkout_id=v_standard AND line_type LIKE 'priority_%')
       OR NOT EXISTS (SELECT 1 FROM checkout_priority_snapshots WHERE checkout_id=v_standard
            AND NOT selected AND NOT test_only AND shopping_cents=0 AND delivery_cents=0) THEN
        RAISE EXCEPTION 'standard checkout collected priority fees'; END IF;

    v_rejected := false;
    BEGIN PERFORM record_checkout_payment(v_checkout,'pi_priority_fixture',1620,0,'requires_payment_method');
    EXCEPTION WHEN raise_exception THEN v_rejected := true; END;
    IF NOT v_rejected THEN RAISE EXCEPTION 'payment excluding priority accepted'; END IF;
    PERFORM record_checkout_payment(v_checkout,'pi_priority_fixture',v_total,0,'requires_payment_method');
    PERFORM process_stripe_checkout_event('evt_priority_fixture','payment_intent.succeeded','{}'::jsonb,
        v_checkout,'pi_priority_fixture',v_total,'usd','succeeded',50,'ch_priority_fixture');
    IF (SELECT sum(item_gross_cents) FROM connect_transfers WHERE checkout_id=v_checkout) <> 1200
       OR (SELECT sum(transfer_amount_cents) FROM connect_transfers WHERE checkout_id=v_checkout) <> 1150 THEN
        RAISE EXCEPTION 'priority fees incorrectly allocated to merchants'; END IF;
    v_refund := reserve_checkout_refund(v_checkout,'fixture-admin','refund-priority-fixture','requested_by_customer');
    IF v_refund.amount_cents <> 2020 THEN RAISE EXCEPTION 'full refund omitted priority'; END IF;
    UPDATE checkout_refunds SET stripe_refund_id='re_priority_fixture' WHERE id=v_refund.id;
    PERFORM process_checkout_refund_event('evt_priority_refund','refund.updated','{}'::jsonb,'re_priority_fixture','succeeded');
    IF (SELECT status FROM checkout_payments WHERE checkout_id=v_checkout) <> 'refunded'
       OR (SELECT status FROM dc_tax_ledger WHERE checkout_id=v_checkout) <> 'refunded'
       OR (SELECT gross_amount_cents FROM checkout_payments WHERE checkout_id=v_checkout)
          - (SELECT sum(amount_cents) FROM checkout_refunds WHERE checkout_id=v_checkout AND status='succeeded') <> 0 THEN
        RAISE EXCEPTION 'refund net or basket tax handling changed'; END IF;
END;
$$;
-- Actually exercise deferred consistency checks even though fixtures roll back.
SET CONSTRAINTS ALL IMMEDIATE;
ROLLBACK;