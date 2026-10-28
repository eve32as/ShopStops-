CREATE TABLE driver_profiles (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    driver_ref text NOT NULL UNIQUE
        CHECK (length(btrim(driver_ref)) BETWEEN 1 AND 200 AND driver_ref = btrim(driver_ref)),
    display_name text NOT NULL
        CHECK (length(btrim(display_name)) BETWEEN 1 AND 120 AND display_name = btrim(display_name)),
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'active', 'suspended')),
    created_by text NOT NULL CHECK (length(btrim(created_by)) BETWEEN 1 AND 200),
    updated_by text NOT NULL CHECK (length(btrim(updated_by)) BETWEEN 1 AND 200),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    verified_by text,
    verified_at timestamptz,
    CHECK ((verified_by IS NULL) = (verified_at IS NULL))
);

CREATE INDEX driver_profiles_status_updated_idx
    ON driver_profiles (status, updated_at DESC, driver_ref);

CREATE FUNCTION enforce_driver_route_eligibility()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.driver_ref IS NOT NULL
       AND NEW.driver_ref IS DISTINCT FROM OLD.driver_ref
       AND NOT EXISTS (
           SELECT 1 FROM driver_profiles
           WHERE driver_profiles.driver_ref = NEW.driver_ref
             AND driver_profiles.status = 'active'
       ) THEN
        RAISE EXCEPTION 'driver is not active and verified';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER unified_route_driver_eligibility_guard
BEFORE UPDATE OF driver_ref ON unified_delivery_routes
FOR EACH ROW EXECUTE FUNCTION enforce_driver_route_eligibility();

CREATE FUNCTION set_driver_profile(
    p_driver_ref text,
    p_display_name text,
    p_status text,
    p_actor text
) RETURNS TABLE (
    driver_ref text,
    display_name text,
    status text,
    created_at timestamptz,
    updated_at timestamptz,
    verified_at timestamptz
)
LANGUAGE plpgsql
AS $$
DECLARE
    v_profile driver_profiles%ROWTYPE;
    v_event_type text;
BEGIN
    IF p_driver_ref IS NULL
       OR length(btrim(p_driver_ref)) NOT BETWEEN 1 AND 200
       OR p_driver_ref <> btrim(p_driver_ref)
       OR p_display_name IS NULL
       OR length(btrim(p_display_name)) NOT BETWEEN 1 AND 120
       OR p_display_name <> btrim(p_display_name)
       OR p_status IS NULL OR p_status NOT IN ('pending', 'active', 'suspended')
       OR p_actor IS NULL
       OR length(btrim(p_actor)) NOT BETWEEN 1 AND 200
       OR p_actor <> btrim(p_actor) THEN
        RAISE EXCEPTION 'invalid driver profile';
    END IF;

    PERFORM pg_advisory_xact_lock(
        hashtextextended('driver-assignment:' || p_driver_ref, 0)
    );

    SELECT * INTO v_profile
    FROM driver_profiles
    WHERE driver_profiles.driver_ref = p_driver_ref
    FOR UPDATE;

    IF FOUND THEN
        IF v_profile.display_name = p_display_name
           AND v_profile.status = p_status THEN
            RETURN QUERY
            SELECT v_profile.driver_ref, v_profile.display_name, v_profile.status,
                   v_profile.created_at, v_profile.updated_at, v_profile.verified_at;
            RETURN;
        END IF;
        v_event_type := 'driver.profile.updated';
        UPDATE driver_profiles
        SET display_name = p_display_name,
            status = p_status,
            updated_by = p_actor,
            updated_at = now(),
            verified_by = CASE
                WHEN p_status = 'active' AND v_profile.status <> 'active'
                    THEN p_actor ELSE verified_by END,
            verified_at = CASE
                WHEN p_status = 'active' AND v_profile.status <> 'active'
                    THEN now() ELSE verified_at END
        WHERE driver_profiles.driver_ref = p_driver_ref
        RETURNING * INTO v_profile;
    ELSE
        v_event_type := 'driver.profile.created';
        INSERT INTO driver_profiles (
            driver_ref, display_name, status, created_by, updated_by, verified_by, verified_at
        ) VALUES (
            p_driver_ref, p_display_name, p_status, p_actor, p_actor,
            CASE WHEN p_status = 'active' THEN p_actor ELSE NULL END,
            CASE WHEN p_status = 'active' THEN now() ELSE NULL END
        )
        RETURNING * INTO v_profile;
    END IF;

    INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
    VALUES (
        'driver_profile', v_profile.id, v_event_type,
        jsonb_build_object(
            'driverRef', v_profile.driver_ref,
            'status', v_profile.status,
            'actor', p_actor
        )
    );

    RETURN QUERY
    SELECT v_profile.driver_ref, v_profile.display_name, v_profile.status,
           v_profile.created_at, v_profile.updated_at, v_profile.verified_at;
END;
$$;
