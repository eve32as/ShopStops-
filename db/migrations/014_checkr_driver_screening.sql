CREATE TABLE driver_screenings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    driver_ref text NOT NULL REFERENCES driver_profiles(driver_ref) ON DELETE CASCADE,
    provider text NOT NULL DEFAULT 'checkr' CHECK (provider = 'checkr'),
    idempotency_key text NOT NULL
        CHECK (
            length(btrim(idempotency_key)) BETWEEN 1 AND 200
            AND idempotency_key = btrim(idempotency_key)
        ),
    package_id text NOT NULL CHECK (length(btrim(package_id)) BETWEEN 1 AND 200),
    work_location_city text NOT NULL
        CHECK (length(btrim(work_location_city)) BETWEEN 1 AND 100),
    work_location_region text NOT NULL
        CHECK (length(btrim(work_location_region)) BETWEEN 2 AND 3),
    work_location_country text NOT NULL
        CHECK (work_location_country ~ '^[A-Z]{2}$'),
    candidate_id text UNIQUE,
    invitation_id text UNIQUE,
    report_id text UNIQUE,
    status text NOT NULL DEFAULT 'requested'
        CHECK (status IN (
            'requested', 'candidate_created', 'invitation_submitting',
            'invited', 'in_progress', 'complete', 'failed',
            'reconciliation_required'
        )),
    provider_status text,
    provider_result text,
    review_decision text NOT NULL DEFAULT 'pending'
        CHECK (review_decision IN ('pending', 'approved', 'denied')),
    requested_by text NOT NULL
        CHECK (length(btrim(requested_by)) BETWEEN 1 AND 200),
    reviewed_by text,
    reviewed_at timestamptz,
    reconciled_by text,
    reconciled_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((reviewed_by IS NULL) = (reviewed_at IS NULL)),
    CHECK ((reconciled_by IS NULL) = (reconciled_at IS NULL)),
    CHECK (
        (review_decision = 'pending' AND reviewed_by IS NULL)
        OR (review_decision <> 'pending' AND reviewed_by IS NOT NULL)
    ),
    UNIQUE (driver_ref, idempotency_key)
);

CREATE INDEX driver_screenings_latest_idx
    ON driver_screenings (driver_ref, created_at DESC, id DESC);

CREATE FUNCTION enforce_screened_driver_activation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.status = 'active'
       AND (TG_OP = 'INSERT' OR OLD.status IS DISTINCT FROM NEW.status)
       AND NOT EXISTS (
           SELECT 1
           FROM driver_screenings screening
           WHERE screening.driver_ref = NEW.driver_ref
             AND screening.id = (
                 SELECT latest.id
                 FROM driver_screenings latest
                 WHERE latest.driver_ref = NEW.driver_ref
                 ORDER BY latest.created_at DESC, latest.id DESC
                 LIMIT 1
             )
             AND screening.status = 'complete'
             AND screening.review_decision = 'approved'
       ) THEN
        RAISE EXCEPTION 'latest completed driver screening must be approved by an administrator';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER driver_profile_screening_activation_guard
BEFORE INSERT OR UPDATE OF status ON driver_profiles
FOR EACH ROW EXECUTE FUNCTION enforce_screened_driver_activation();

CREATE OR REPLACE FUNCTION enforce_driver_route_eligibility()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.driver_ref IS NOT NULL
       AND (TG_OP = 'INSERT' OR NEW.driver_ref IS DISTINCT FROM OLD.driver_ref)
    THEN
        PERFORM 1
        FROM driver_profiles
        WHERE driver_ref = NEW.driver_ref
        FOR UPDATE;
    END IF;
    IF NEW.driver_ref IS NOT NULL
       AND (TG_OP = 'INSERT' OR NEW.driver_ref IS DISTINCT FROM OLD.driver_ref)
       AND NOT EXISTS (
           SELECT 1
           FROM driver_profiles profile
           JOIN driver_screenings screening
             ON screening.driver_ref = profile.driver_ref
           WHERE profile.driver_ref = NEW.driver_ref
             AND profile.status = 'active'
             AND screening.id = (
                 SELECT latest.id
                 FROM driver_screenings latest
                 WHERE latest.driver_ref = profile.driver_ref
                 ORDER BY latest.created_at DESC, latest.id DESC
                 LIMIT 1
             )
             AND screening.status = 'complete'
             AND screening.review_decision = 'approved'
       ) THEN
        RAISE EXCEPTION 'driver is not active with an approved current screening';
    END IF;
    RETURN NEW;
END;
$$;

WITH demoted_profiles AS (
    UPDATE driver_profiles
    SET status = 'pending',
        updated_by = 'system:migration-014',
        verified_by = NULL,
        verified_at = NULL,
        updated_at = now()
    WHERE status = 'active'
    RETURNING id, driver_ref
)
INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
SELECT 'driver_profile', id, 'driver.screening.required',
       jsonb_build_object('driverRef', driver_ref, 'actor', 'system:migration-014')
FROM demoted_profiles;

CREATE FUNCTION record_driver_screening_review(
    p_screening_id uuid,
    p_driver_ref text,
    p_decision text,
    p_actor text
) RETURNS TABLE (
    screening_id uuid,
    driver_ref text,
    status text,
    provider_result text,
    review_decision text,
    reviewed_by text,
    reviewed_at timestamptz
)
LANGUAGE plpgsql
AS $$
DECLARE
    v_screening driver_screenings%ROWTYPE;
BEGIN
    IF p_screening_id IS NULL
       OR p_driver_ref IS NULL
       OR length(btrim(p_driver_ref)) NOT BETWEEN 1 AND 200
       OR p_driver_ref <> btrim(p_driver_ref)
       OR p_decision IS NULL
       OR p_decision NOT IN ('approved', 'denied')
       OR p_actor IS NULL
       OR length(btrim(p_actor)) NOT BETWEEN 1 AND 200
       OR p_actor <> btrim(p_actor) THEN
        RAISE EXCEPTION 'invalid driver screening review';
    END IF;

    PERFORM 1 FROM driver_profiles
    WHERE driver_ref = p_driver_ref
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'driver screening not found' USING ERRCODE = 'P0002';
    END IF;
    SELECT * INTO v_screening
    FROM driver_screenings
    WHERE id = p_screening_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'driver screening not found' USING ERRCODE = 'P0002';
    END IF;
    IF v_screening.driver_ref <> p_driver_ref THEN
        RAISE EXCEPTION 'driver screening not found' USING ERRCODE = 'P0002';
    END IF;
    IF v_screening.status <> 'complete'
       OR v_screening.id <> (
           SELECT latest.id FROM driver_screenings latest
           WHERE latest.driver_ref = v_screening.driver_ref
           ORDER BY latest.created_at DESC, latest.id DESC LIMIT 1
       ) THEN
        RAISE EXCEPTION 'only the latest completed screening can be reviewed';
    END IF;
    IF v_screening.review_decision = p_decision THEN
        RETURN QUERY
        SELECT v_screening.id, v_screening.driver_ref, v_screening.status,
               v_screening.provider_result, v_screening.review_decision,
               v_screening.reviewed_by, v_screening.reviewed_at;
        RETURN;
    END IF;

    UPDATE driver_screenings
    SET review_decision = p_decision,
        reviewed_by = p_actor,
        reviewed_at = now(),
        updated_at = now()
    WHERE id = p_screening_id
    RETURNING * INTO v_screening;

    INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
    SELECT 'driver_profile', profile.id, 'driver.screening.reviewed',
           jsonb_build_object(
               'driverRef', v_screening.driver_ref,
               'screeningId', v_screening.id,
               'decision', p_decision,
               'actor', p_actor
           )
    FROM driver_profiles profile
    WHERE profile.driver_ref = v_screening.driver_ref;

    RETURN QUERY
    SELECT v_screening.id, v_screening.driver_ref, v_screening.status,
           v_screening.provider_result, v_screening.review_decision,
           v_screening.reviewed_by, v_screening.reviewed_at;
END;
$$;

CREATE FUNCTION resolve_driver_screening_reconciliation(
    p_screening_id uuid,
    p_driver_ref text,
    p_actor text,
    p_confirmation text
) RETURNS TABLE (
    screening_id uuid,
    status text,
    reconciled_by text,
    reconciled_at timestamptz
)
LANGUAGE plpgsql
AS $$
DECLARE
    v_screening driver_screenings%ROWTYPE;
BEGIN
    IF p_screening_id IS NULL
       OR p_driver_ref IS NULL
       OR length(btrim(p_driver_ref)) NOT BETWEEN 1 AND 200
       OR p_driver_ref <> btrim(p_driver_ref)
       OR p_actor IS NULL
       OR length(btrim(p_actor)) NOT BETWEEN 1 AND 200
       OR p_actor <> btrim(p_actor)
       OR p_confirmation IS DISTINCT FROM 'no_active_check' THEN
        RAISE EXCEPTION 'invalid driver screening reconciliation';
    END IF;

    PERFORM 1 FROM driver_profiles
    WHERE driver_ref = p_driver_ref
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'driver screening not found' USING ERRCODE = 'P0002';
    END IF;
    SELECT * INTO v_screening
    FROM driver_screenings
    WHERE id = p_screening_id
    FOR UPDATE;
    IF NOT FOUND OR v_screening.driver_ref <> p_driver_ref THEN
        RAISE EXCEPTION 'driver screening not found' USING ERRCODE = 'P0002';
    END IF;
    IF v_screening.status NOT IN (
           'requested', 'candidate_created', 'invitation_submitting',
           'reconciliation_required'
       )
       OR v_screening.id <> (
           SELECT latest.id FROM driver_screenings latest
           WHERE latest.driver_ref = v_screening.driver_ref
           ORDER BY latest.created_at DESC, latest.id DESC LIMIT 1
       )
       OR (v_screening.status IN (
               'requested', 'invitation_submitting', 'reconciliation_required'
           )
           AND v_screening.updated_at > now() - interval '2 minutes') THEN
        RAISE EXCEPTION 'only the latest unresolved screening can be reconciled';
    END IF;

    UPDATE driver_screenings
    SET status = 'failed',
        reconciled_by = p_actor,
        reconciled_at = now(),
        updated_at = now()
    WHERE id = p_screening_id
    RETURNING * INTO v_screening;

    INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
    SELECT 'driver_profile', profile.id, 'driver.screening.reconciled',
           jsonb_build_object(
               'driverRef', v_screening.driver_ref,
               'screeningId', v_screening.id,
               'resolution', p_confirmation,
               'actor', p_actor
           )
    FROM driver_profiles profile
    WHERE profile.driver_ref = v_screening.driver_ref;

    RETURN QUERY
    SELECT v_screening.id, v_screening.status,
           v_screening.reconciled_by, v_screening.reconciled_at;
END;
$$;
