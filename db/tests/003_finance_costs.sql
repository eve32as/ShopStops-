\set ON_ERROR_STOP on
BEGIN;
DO $$
DECLARE
    v_id uuid := '11111111-1111-4111-8111-111111111111';
    first_record finance_costs%ROWTYPE;
    retry_record finance_costs%ROWTYPE;
BEGIN
    SELECT * INTO first_record FROM record_finance_cost(v_id,'admin-test','2026-11','driver',501);
    SELECT * INTO retry_record FROM record_finance_cost(v_id,'admin-test','2026-11','driver',501);
    IF first_record IS DISTINCT FROM retry_record OR (SELECT count(*) FROM finance_costs) <> 1 THEN
        RAISE EXCEPTION 'idempotent retry changed manual ledger';
    END IF;
    BEGIN
        PERFORM record_finance_cost(v_id,'admin-test','2026-11','driver',502);
        RAISE EXCEPTION 'different amount accepted';
    EXCEPTION WHEN unique_violation THEN NULL; END;
    BEGIN
        PERFORM record_finance_cost(v_id,'other-admin','2026-11','driver',501);
        RAISE EXCEPTION 'different actor accepted';
    EXCEPTION WHEN unique_violation THEN NULL; END;
    BEGIN
        PERFORM record_finance_cost(v_id,'admin-test','2026-12','driver',501);
        RAISE EXCEPTION 'different period accepted';
    EXCEPTION WHEN unique_violation THEN NULL; END;
    BEGIN
        PERFORM record_finance_cost(v_id,'admin-test','2026-11','staff',501);
        RAISE EXCEPTION 'different category accepted';
    EXCEPTION WHEN unique_violation THEN NULL; END;
    BEGIN
        PERFORM record_finance_cost(gen_random_uuid(),'admin-test','2026-11','driver',-1);
        RAISE EXCEPTION 'negative accepted';
    EXCEPTION WHEN invalid_parameter_value THEN NULL; END;
    BEGIN
        PERFORM record_finance_cost(gen_random_uuid(),'admin-test','2026-13','driver',1);
        RAISE EXCEPTION 'invalid month accepted';
    EXCEPTION WHEN invalid_parameter_value THEN NULL; END;
    BEGIN
        PERFORM record_finance_cost(gen_random_uuid(),'admin-test','2026-11','earned_wages',1);
        RAISE EXCEPTION 'wage category accepted';
    EXCEPTION WHEN invalid_parameter_value THEN NULL; END;
    BEGIN
        PERFORM record_finance_cost(gen_random_uuid(),'admin-test','2026-11','driver',100000001);
        RAISE EXCEPTION 'overflow accepted';
    EXCEPTION WHEN invalid_parameter_value THEN NULL; END;
    PERFORM record_finance_cost(gen_random_uuid(),'admin-test','2026-11','refund_loss',0);
    IF (SELECT sum(amount_cents) FROM finance_costs WHERE period='2026-11') <> 501 THEN
        RAISE EXCEPTION 'incorrect period total';
    END IF;
    BEGIN
        UPDATE finance_costs SET amount_cents=1 WHERE id=v_id;
        RAISE EXCEPTION 'update accepted';
    EXCEPTION WHEN object_not_in_prerequisite_state THEN NULL; END;
    BEGIN
        DELETE FROM finance_costs WHERE id=v_id;
        RAISE EXCEPTION 'delete accepted';
    EXCEPTION WHEN object_not_in_prerequisite_state THEN NULL; END;
    BEGIN
        TRUNCATE finance_costs;
        RAISE EXCEPTION 'truncate accepted';
    EXCEPTION WHEN object_not_in_prerequisite_state THEN NULL; END;
    IF EXISTS (SELECT 1 FROM pg_class c, LATERAL aclexplode(c.relacl) a
               WHERE c.oid='finance_costs'::regclass AND a.grantee=0)
       OR EXISTS (SELECT 1 FROM pg_proc p, LATERAL aclexplode(p.proacl) a
                  WHERE p.oid='record_finance_cost(uuid,text,text,text,bigint)'::regprocedure AND a.grantee=0) THEN
        RAISE EXCEPTION 'PUBLIC finance privileges';
    END IF;
END;
$$;
ROLLBACK;