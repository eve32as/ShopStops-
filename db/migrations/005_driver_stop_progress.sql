CREATE TABLE unified_route_stop_events (
    event_id text PRIMARY KEY,
    route_id uuid NOT NULL REFERENCES unified_delivery_routes(id) ON DELETE CASCADE,
    stop_id uuid NOT NULL REFERENCES unified_route_stops(id) ON DELETE CASCADE,
    driver_ref text NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('arrive', 'complete')),
    observed_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX unified_route_stop_events_route_idx
    ON unified_route_stop_events (route_id, created_at);

CREATE FUNCTION record_unified_route_stop_event(
    p_route_id uuid,
    p_stop_id uuid,
    p_driver_ref text,
    p_event_id text,
    p_event_type text,
    p_observed_at timestamptz
) RETURNS TABLE(stop_status text, route_status text)
LANGUAGE plpgsql
AS $$
DECLARE
    v_route unified_delivery_routes%ROWTYPE;
    v_stop unified_route_stops%ROWTYPE;
    v_event unified_route_stop_events%ROWTYPE;
    v_current_stop_id uuid;
BEGIN
    IF p_route_id IS NULL OR p_stop_id IS NULL OR p_driver_ref IS NULL
       OR btrim(p_driver_ref) = '' OR length(p_driver_ref) > 200
       OR p_event_id IS NULL OR btrim(p_event_id) = ''
       OR length(p_event_id) > 200 OR p_event_type IS NULL
       OR p_event_type NOT IN ('arrive', 'complete')
       OR p_observed_at IS NULL THEN
        RAISE EXCEPTION 'route stop event fields are required';
    END IF;

    SELECT * INTO v_route
    FROM unified_delivery_routes
    WHERE id = p_route_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'route not found' USING ERRCODE = 'P0002';
    END IF;
    IF v_route.driver_ref IS DISTINCT FROM p_driver_ref THEN
        RAISE EXCEPTION 'route is not assigned to this driver' USING ERRCODE = '42501';
    END IF;

    SELECT * INTO v_stop
    FROM unified_route_stops
    WHERE id = p_stop_id AND route_id = p_route_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'route stop not found' USING ERRCODE = 'P0002';
    END IF;

    SELECT * INTO v_event
    FROM unified_route_stop_events
    WHERE event_id = p_event_id;
    IF FOUND THEN
        IF v_event.route_id <> p_route_id OR v_event.stop_id <> p_stop_id
           OR v_event.driver_ref <> p_driver_ref
           OR v_event.event_type <> p_event_type
           OR v_event.observed_at <> p_observed_at THEN
            RAISE EXCEPTION 'event ID was already used for a different stop event';
        END IF;
        RETURN QUERY SELECT v_stop.status, v_route.status;
        RETURN;
    END IF;

    IF v_route.status <> 'in_progress' THEN
        RAISE EXCEPTION 'route is not in progress';
    END IF;
    IF p_observed_at > now() + interval '1 minute' THEN
        RAISE EXCEPTION 'stop event timestamp is in the future';
    END IF;

    SELECT id INTO v_current_stop_id
    FROM unified_route_stops
    WHERE route_id = p_route_id AND status <> 'completed'
    ORDER BY sequence
    LIMIT 1;
    IF v_current_stop_id IS DISTINCT FROM p_stop_id THEN
        RAISE EXCEPTION 'route stops must be completed in sequence';
    END IF;

    IF p_event_type = 'arrive' AND v_stop.status = 'pending' THEN
        UPDATE unified_route_stops
        SET status = 'arrived', arrived_at = p_observed_at
        WHERE id = p_stop_id;
        UPDATE unified_checkouts
        SET status = 'in_progress', updated_at = now()
        WHERE id = v_route.checkout_id AND status = 'dispatching';
        INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
        VALUES (
            'unified_delivery_route', p_route_id, 'delivery.route.stop.arrived',
            jsonb_build_object(
                'routeId', p_route_id, 'stopId', p_stop_id,
                'sequence', v_stop.sequence, 'stopType', v_stop.stop_type,
                'eventId', p_event_id, 'driverRef', p_driver_ref,
                'observedAt', p_observed_at
            )
        );
    ELSIF p_event_type = 'complete' AND v_stop.status = 'arrived' THEN
        UPDATE unified_route_stops
        SET status = 'completed', completed_at = p_observed_at
        WHERE id = p_stop_id;
        INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
        VALUES (
            'unified_delivery_route', p_route_id, 'delivery.route.stop.completed',
            jsonb_build_object(
                'routeId', p_route_id, 'stopId', p_stop_id,
                'sequence', v_stop.sequence, 'stopType', v_stop.stop_type,
                'eventId', p_event_id, 'driverRef', p_driver_ref,
                'observedAt', p_observed_at
            )
        );
        IF NOT EXISTS (
            SELECT 1 FROM unified_route_stops
            WHERE route_id = p_route_id AND status <> 'completed'
        ) THEN
            UPDATE unified_delivery_routes
            SET status = 'completed'
            WHERE id = p_route_id;
            UPDATE unified_checkouts
            SET status = 'completed', updated_at = now()
            WHERE id = v_route.checkout_id AND status = 'in_progress';
        END IF;
    ELSE
        RAISE EXCEPTION 'invalid route stop event for current stop state';
    END IF;

    INSERT INTO unified_route_stop_events (
        event_id, route_id, stop_id, driver_ref, event_type, observed_at
    ) VALUES (
        p_event_id, p_route_id, p_stop_id, p_driver_ref, p_event_type, p_observed_at
    );

    RETURN QUERY
    SELECT s.status, r.status
    FROM unified_route_stops s
    JOIN unified_delivery_routes r ON r.id = s.route_id
    WHERE s.id = p_stop_id;
END;
$$;
