CREATE FUNCTION assign_unified_driver(
    p_route_id uuid,
    p_driver_ref text,
    p_assigned_by text
) RETURNS text
LANGUAGE plpgsql
AS $$
DECLARE
    v_route unified_delivery_routes%ROWTYPE;
    v_active_route_id uuid;
BEGIN
    IF p_route_id IS NULL
       OR p_driver_ref IS NULL OR btrim(p_driver_ref) = ''
       OR btrim(p_driver_ref) <> p_driver_ref
       OR length(p_driver_ref) > 200
       OR p_assigned_by IS NULL OR btrim(p_assigned_by) = ''
       OR btrim(p_assigned_by) <> p_assigned_by
       OR length(p_assigned_by) > 200 THEN
        RAISE EXCEPTION 'route, driver, and assigning actor are required';
    END IF;

    PERFORM pg_advisory_xact_lock(
        hashtextextended('driver-assignment:' || p_driver_ref, 0)
    );

    SELECT * INTO v_route
    FROM unified_delivery_routes
    WHERE id = p_route_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'route not found' USING ERRCODE = 'P0002';
    END IF;
    IF v_route.driver_ref IS NOT NULL THEN
        IF v_route.driver_ref = p_driver_ref THEN
            RETURN v_route.status;
        END IF;
        RAISE EXCEPTION 'route is already assigned';
    END IF;
    IF v_route.status <> 'routed' THEN
        RAISE EXCEPTION 'route is not ready for assignment';
    END IF;

    SELECT id INTO v_active_route_id
    FROM unified_delivery_routes
    WHERE driver_ref = p_driver_ref
      AND status IN ('routed', 'in_progress')
      AND id <> p_route_id
    LIMIT 1;
    IF FOUND THEN
        RAISE EXCEPTION 'driver already has an active route';
    END IF;

    UPDATE unified_delivery_routes
    SET driver_ref = p_driver_ref
    WHERE id = p_route_id;
    INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
    VALUES (
        'unified_delivery_route', p_route_id, 'delivery.route.assigned',
        jsonb_build_object(
            'routeId', p_route_id,
            'driverRef', p_driver_ref,
            'assignedBy', p_assigned_by
        )
    );
    RETURN 'routed';
END;
$$;
