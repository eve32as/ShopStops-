CREATE TABLE route_optimization_jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    route_id uuid NOT NULL REFERENCES unified_delivery_routes(id) ON DELETE CASCADE,
    idempotency_key text NOT NULL
        CHECK (length(btrim(idempotency_key)) BETWEEN 1 AND 200
            AND idempotency_key = btrim(idempotency_key)),
    requested_by text NOT NULL
        CHECK (length(btrim(requested_by)) BETWEEN 1 AND 200
            AND requested_by = btrim(requested_by)),
    status text NOT NULL DEFAULT 'queued'
        CHECK (status IN (
            'queued', 'processing', 'preview_ready', 'not_improved',
            'failed', 'stale', 'applied'
        )),
    route_snapshot_ids uuid[] NOT NULL
        CHECK (cardinality(route_snapshot_ids) = 3),
    route_snapshot_legs jsonb NOT NULL
        CHECK (jsonb_typeof(route_snapshot_legs) = 'array'
            AND jsonb_array_length(route_snapshot_legs) = 2),
    proposed_stop_ids uuid[],
    baseline_distance_meters integer NOT NULL CHECK (baseline_distance_meters >= 0),
    baseline_duration_seconds integer NOT NULL CHECK (baseline_duration_seconds >= 0),
    proposed_distance_meters integer CHECK (proposed_distance_meters >= 0),
    proposed_duration_seconds integer CHECK (proposed_duration_seconds >= 0),
    estimated_savings_cents integer CHECK (estimated_savings_cents >= 0),
    proposed_legs jsonb CHECK (
        proposed_legs IS NULL OR (
            jsonb_typeof(proposed_legs) = 'array'
            AND jsonb_array_length(proposed_legs) = 2
        )
    ),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at timestamptz NOT NULL DEFAULT now(),
    locked_until timestamptz,
    lease_token uuid,
    error_code text CHECK (
        error_code IS NULL OR error_code IN (
            'route_unavailable', 'provider_unavailable', 'invalid_provider_result'
        )
    ),
    approved_by text,
    approved_at timestamptz,
    applied_by text,
    applied_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (route_id, idempotency_key),
    CHECK ((approved_by IS NULL) = (approved_at IS NULL)),
    CHECK ((applied_by IS NULL) = (applied_at IS NULL)),
    CHECK (
        (status = 'applied' AND approved_by IS NOT NULL AND applied_by IS NOT NULL)
        OR (status <> 'applied' AND applied_by IS NULL)
    ),
    CHECK (
        status NOT IN ('preview_ready', 'not_improved', 'applied')
        OR (proposed_stop_ids IS NOT NULL
            AND proposed_distance_meters IS NOT NULL
            AND proposed_duration_seconds IS NOT NULL
            AND estimated_savings_cents IS NOT NULL
            AND proposed_legs IS NOT NULL)
    )
);

CREATE UNIQUE INDEX route_optimization_jobs_active_route_idx
    ON route_optimization_jobs (route_id)
    WHERE status IN ('queued', 'processing', 'preview_ready');
CREATE INDEX route_optimization_jobs_claim_idx
    ON route_optimization_jobs (available_at, created_at, id)
    WHERE status IN ('queued', 'processing');
CREATE INDEX route_optimization_jobs_route_idx
    ON route_optimization_jobs (route_id, created_at DESC, id);

CREATE FUNCTION request_route_optimization(
    p_route_id uuid,
    p_idempotency_key text,
    p_actor text
) RETURNS uuid
LANGUAGE plpgsql
AS $$
DECLARE
    v_job_id uuid;
    v_route unified_delivery_routes%ROWTYPE;
    v_stop_ids uuid[];
    v_route_legs jsonb;
    v_baseline_distance integer;
    v_baseline_duration integer;
BEGIN
    IF p_route_id IS NULL
       OR p_idempotency_key IS NULL
       OR length(btrim(p_idempotency_key)) NOT BETWEEN 1 AND 200
       OR p_idempotency_key <> btrim(p_idempotency_key)
       OR p_actor IS NULL
       OR length(btrim(p_actor)) NOT BETWEEN 1 AND 200
       OR p_actor <> btrim(p_actor) THEN
        RAISE EXCEPTION 'invalid route optimization request';
    END IF;

    SELECT id INTO v_job_id
    FROM route_optimization_jobs
    WHERE route_id = p_route_id AND idempotency_key = p_idempotency_key;
    IF FOUND THEN
        RETURN v_job_id;
    END IF;

    SELECT * INTO v_route
    FROM unified_delivery_routes
    WHERE id = p_route_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'route not found' USING ERRCODE = 'P0002';
    END IF;
    SELECT id INTO v_job_id
    FROM route_optimization_jobs
    WHERE route_id = p_route_id AND idempotency_key = p_idempotency_key;
    IF FOUND THEN
        RETURN v_job_id;
    END IF;
    IF v_route.status <> 'routed' OR v_route.driver_ref IS NOT NULL THEN
        RAISE EXCEPTION 'route is not eligible for optimization';
    END IF;

    SELECT array_agg(stop.id ORDER BY stop.sequence)
    INTO v_stop_ids
    FROM unified_route_stops stop
    WHERE stop.route_id = p_route_id
      AND stop.status = 'pending'
      AND stop.sequence BETWEEN 1 AND 3;
    IF cardinality(v_stop_ids) <> 3
       OR (SELECT count(*) FROM unified_route_stops WHERE route_id = p_route_id) <> 3
       OR (SELECT count(*) FROM unified_route_stops
           WHERE route_id = p_route_id AND stop_type = 'pickup') <> 2
       OR (SELECT count(*) FROM unified_route_stops
           WHERE route_id = p_route_id AND sequence = 3 AND stop_type = 'dropoff') <> 1
       OR (SELECT count(*) FROM unified_route_legs WHERE route_id = p_route_id) <> 2 THEN
        RAISE EXCEPTION 'route does not have an eligible unstarted pickup sequence';
    END IF;

    SELECT sum(leg.distance_meters)::integer,
           sum(leg.duration_seconds)::integer,
           jsonb_agg(
               jsonb_build_object(
                   'fromSequence', leg.from_sequence,
                   'toSequence', leg.to_sequence,
                   'distanceMeters', leg.distance_meters,
                   'durationSeconds', leg.duration_seconds,
                   'provider', leg.provider,
                   'geometry', leg.geometry
               ) ORDER BY leg.from_sequence
           )
    INTO v_baseline_distance, v_baseline_duration, v_route_legs
    FROM unified_route_legs leg
    WHERE leg.route_id = p_route_id;
    IF v_baseline_distance IS NULL OR v_baseline_duration IS NULL THEN
        RAISE EXCEPTION 'route has no complete routing estimate';
    END IF;

    INSERT INTO route_optimization_jobs (
        route_id, idempotency_key, requested_by, route_snapshot_ids,
        route_snapshot_legs,
        baseline_distance_meters, baseline_duration_seconds
    ) VALUES (
        p_route_id, p_idempotency_key, p_actor, v_stop_ids, v_route_legs,
        v_baseline_distance, v_baseline_duration
    )
    RETURNING id INTO v_job_id;

    INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
    VALUES (
        'unified_delivery_route', p_route_id, 'delivery.route.optimization.requested',
        jsonb_build_object(
            'routeId', p_route_id, 'optimizationId', v_job_id, 'requestedBy', p_actor
        )
    );
    RETURN v_job_id;
END;
$$;

CREATE FUNCTION complete_route_optimization(
    p_job_id uuid,
    p_lease_token uuid,
    p_baseline_distance_meters integer,
    p_baseline_duration_seconds integer,
    p_proposed_stop_ids uuid[],
    p_proposed_distance_meters integer,
    p_proposed_duration_seconds integer,
    p_estimated_savings_cents integer,
    p_proposed_legs jsonb
) RETURNS text
LANGUAGE plpgsql
AS $$
DECLARE
    v_job route_optimization_jobs%ROWTYPE;
    v_status text;
BEGIN
    SELECT * INTO v_job
    FROM route_optimization_jobs
    WHERE id = p_job_id
    FOR UPDATE;
    IF NOT FOUND OR v_job.status <> 'processing'
       OR v_job.lease_token IS DISTINCT FROM p_lease_token
       OR v_job.locked_until IS NULL OR v_job.locked_until <= now() THEN
        RAISE EXCEPTION 'route optimization job is not being processed';
    END IF;
    IF cardinality(p_proposed_stop_ids) <> 3
       OR p_proposed_stop_ids[3] <> v_job.route_snapshot_ids[3]
       OR p_proposed_stop_ids[1] = p_proposed_stop_ids[2]
       OR NOT (p_proposed_stop_ids @> v_job.route_snapshot_ids
               AND p_proposed_stop_ids <@ v_job.route_snapshot_ids)
       OR p_baseline_distance_meters IS NULL OR p_baseline_distance_meters < 0
       OR p_baseline_duration_seconds IS NULL OR p_baseline_duration_seconds < 0
       OR p_proposed_distance_meters IS NULL OR p_proposed_distance_meters < 0
       OR p_proposed_duration_seconds IS NULL OR p_proposed_duration_seconds < 0
       OR p_estimated_savings_cents IS NULL OR p_estimated_savings_cents < 0
       OR jsonb_typeof(p_proposed_legs) <> 'array'
       OR jsonb_array_length(p_proposed_legs) <> 2 THEN
        RAISE EXCEPTION 'invalid route optimization result';
    END IF;
    IF (SELECT count(*) FROM unified_route_stops
        WHERE id = p_proposed_stop_ids[1] AND route_id = v_job.route_id
          AND stop_type = 'pickup') <> 1
       OR (SELECT count(*) FROM unified_route_stops
           WHERE id = p_proposed_stop_ids[2] AND route_id = v_job.route_id
             AND stop_type = 'pickup') <> 1
       OR (SELECT count(*) FROM unified_route_stops
           WHERE id = p_proposed_stop_ids[3] AND route_id = v_job.route_id
             AND stop_type = 'dropoff') <> 1 THEN
        RAISE EXCEPTION 'route optimization must preserve pickup-before-dropoff';
    END IF;

    v_status := CASE WHEN p_estimated_savings_cents > 0
        THEN 'preview_ready' ELSE 'not_improved' END;
    UPDATE route_optimization_jobs
    SET status = v_status,
        baseline_distance_meters = p_baseline_distance_meters,
        baseline_duration_seconds = p_baseline_duration_seconds,
        proposed_stop_ids = p_proposed_stop_ids,
        proposed_distance_meters = p_proposed_distance_meters,
        proposed_duration_seconds = p_proposed_duration_seconds,
        estimated_savings_cents = p_estimated_savings_cents,
        proposed_legs = p_proposed_legs,
        locked_until = NULL,
        lease_token = NULL,
        error_code = NULL,
        updated_at = now()
    WHERE id = p_job_id;

    INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
    VALUES (
        'unified_delivery_route', v_job.route_id, 'delivery.route.optimization.completed',
        jsonb_build_object(
            'routeId', v_job.route_id, 'optimizationId', p_job_id,
            'status', v_status, 'estimatedSavingsCents', p_estimated_savings_cents,
            'baselineDistanceMeters', v_job.baseline_distance_meters,
            'proposedDistanceMeters', p_proposed_distance_meters,
            'baselineDurationSeconds', v_job.baseline_duration_seconds,
            'proposedDurationSeconds', p_proposed_duration_seconds
        )
    );
    RETURN v_status;
END;
$$;

CREATE FUNCTION apply_route_optimization(
    p_route_id uuid,
    p_job_id uuid,
    p_actor text
) RETURNS text
LANGUAGE plpgsql
AS $$
DECLARE
    v_job route_optimization_jobs%ROWTYPE;
    v_route unified_delivery_routes%ROWTYPE;
    v_current_stop_ids uuid[];
    v_current_legs jsonb;
    v_changed integer;
BEGIN
    IF p_actor IS NULL
       OR length(btrim(p_actor)) NOT BETWEEN 1 AND 200
       OR p_actor <> btrim(p_actor) THEN
        RAISE EXCEPTION 'invalid route optimization approver';
    END IF;
    SELECT * INTO v_job
    FROM route_optimization_jobs
    WHERE id = p_job_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'route optimization not found' USING ERRCODE = 'P0002';
    END IF;
    IF v_job.route_id <> p_route_id THEN
        RAISE EXCEPTION 'route optimization not found' USING ERRCODE = 'P0002';
    END IF;
    IF v_job.status = 'applied' THEN
        RETURN v_job.status;
    END IF;
    IF v_job.status <> 'preview_ready' OR v_job.estimated_savings_cents <= 0 THEN
        RAISE EXCEPTION 'route optimization preview is not eligible for approval';
    END IF;

    SELECT * INTO v_route
    FROM unified_delivery_routes
    WHERE id = v_job.route_id
    FOR UPDATE;
    IF NOT FOUND
       OR v_route.status <> 'routed'
       OR v_route.driver_ref IS NOT NULL THEN
        UPDATE route_optimization_jobs
        SET status = 'stale', updated_at = now()
        WHERE id = p_job_id;
        RETURN 'stale';
    END IF;

    SELECT array_agg(id ORDER BY sequence) INTO v_current_stop_ids
    FROM unified_route_stops
    WHERE route_id = v_job.route_id AND status = 'pending';
    IF v_current_stop_ids IS DISTINCT FROM v_job.route_snapshot_ids
       OR (SELECT count(*) FROM unified_route_stops
           WHERE route_id = v_job.route_id) <> 3 THEN
        UPDATE route_optimization_jobs
        SET status = 'stale', updated_at = now()
        WHERE id = p_job_id;
        RETURN 'stale';
    END IF;
    SELECT jsonb_agg(
               jsonb_build_object(
                   'fromSequence', leg.from_sequence,
                   'toSequence', leg.to_sequence,
                   'distanceMeters', leg.distance_meters,
                   'durationSeconds', leg.duration_seconds,
                   'provider', leg.provider,
                   'geometry', leg.geometry
               ) ORDER BY leg.from_sequence
           )
    INTO v_current_legs
    FROM unified_route_legs leg
    WHERE leg.route_id = v_job.route_id;
    IF v_current_legs IS DISTINCT FROM v_job.route_snapshot_legs THEN
        UPDATE route_optimization_jobs
        SET status = 'stale', updated_at = now()
        WHERE id = p_job_id;
        RETURN 'stale';
    END IF;

    UPDATE route_optimization_jobs
    SET status = 'applied', approved_by = p_actor, approved_at = now(),
        applied_by = p_actor, applied_at = now(), updated_at = now()
    WHERE id = p_job_id;
    PERFORM set_config('shopstops.route_optimization_job', p_job_id::text, true);

    DELETE FROM unified_route_legs WHERE route_id = v_job.route_id;
    UPDATE unified_route_stops
    SET sequence = sequence + 3
    WHERE route_id = v_job.route_id;
    FOR v_changed IN 1..3 LOOP
        UPDATE unified_route_stops
        SET sequence = v_changed
        WHERE route_id = v_job.route_id
          AND id = v_job.proposed_stop_ids[v_changed];
        IF NOT FOUND THEN
            RAISE EXCEPTION 'route optimization stop snapshot no longer matches';
        END IF;
    END LOOP;
    PERFORM set_config('shopstops.route_optimization_job', '', true);

    INSERT INTO unified_route_legs (
        route_id, from_sequence, to_sequence, distance_meters,
        duration_seconds, provider, geometry
    )
    SELECT v_job.route_id, leg.from_sequence, leg.to_sequence,
           leg.distance_meters, leg.duration_seconds, 'osrm', leg.geometry
    FROM jsonb_to_recordset(v_job.proposed_legs) AS leg(
        from_sequence integer,
        to_sequence integer,
        distance_meters integer,
        duration_seconds integer,
        geometry jsonb
    )
    ORDER BY leg.from_sequence;

    INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
    VALUES (
        'unified_delivery_route', v_job.route_id, 'delivery.route.optimization.applied',
        jsonb_build_object(
            'routeId', v_job.route_id, 'optimizationId', p_job_id,
            'approvedBy', p_actor, 'appliedBy', p_actor,
            'baselineDistanceMeters', v_job.baseline_distance_meters,
            'projectedDistanceMeters', v_job.proposed_distance_meters,
            'baselineDurationSeconds', v_job.baseline_duration_seconds,
            'projectedDurationSeconds', v_job.proposed_duration_seconds,
            'estimatedSavingsCents', v_job.estimated_savings_cents
        )
    );
    RETURN 'applied';
END;
$$;

CREATE OR REPLACE FUNCTION prevent_unified_route_resequence()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.route_id <> OLD.route_id
       OR NEW.stop_type <> OLD.stop_type
       OR NEW.store_location_id IS DISTINCT FROM OLD.store_location_id
       OR NEW.location <> OLD.location THEN
        RAISE EXCEPTION 'driver stop locations and types are immutable';
    END IF;
    IF NEW.sequence <> OLD.sequence
       AND NOT EXISTS (
           SELECT 1
           FROM route_optimization_jobs job
           WHERE job.id::text = current_setting(
                     'shopstops.route_optimization_job', true
                 )
             AND job.route_id = NEW.route_id
             AND job.status = 'applied'
             AND job.applied_by IS NOT NULL
             AND (
                 (NEW.sequence = OLD.sequence + 3
                  AND NEW.sequence BETWEEN 4 AND 6)
                 OR (NEW.sequence BETWEEN 1 AND 3
                     AND job.proposed_stop_ids[NEW.sequence] = NEW.id)
             )
       ) THEN
        RAISE EXCEPTION 'driver stop sequence is immutable outside approved optimization';
    END IF;
    RETURN NEW;
END;
$$;

CREATE VIEW route_optimization_actual_metrics AS
WITH completed AS (
    SELECT job.id AS optimization_id, job.route_id,
           job.proposed_distance_meters AS projected_distance_meters,
           job.proposed_duration_seconds AS projected_duration_seconds,
           job.estimated_savings_cents,
           distance.distance_meters AS actual_distance_meters,
           duration.duration_seconds AS actual_duration_seconds
    FROM route_optimization_jobs job
    JOIN unified_delivery_routes route ON route.id = job.route_id
    JOIN LATERAL (
        SELECT sum(ST_Distance(ordered.location, ordered.previous_location))::bigint
                   AS distance_meters
        FROM (
            SELECT sample.location,
                   lag(sample.location) OVER (
                       ORDER BY sample.observed_at, sample.id
                   ) AS previous_location
            FROM driver_location_samples sample
            WHERE sample.route_id = route.id
        ) ordered
        WHERE ordered.previous_location IS NOT NULL
    ) distance ON distance.distance_meters IS NOT NULL
    JOIN LATERAL (
        SELECT extract(epoch FROM max(event.observed_at) - min(event.observed_at))::integer
                   AS duration_seconds
        FROM unified_route_stop_events event
        WHERE event.route_id = route.id
        HAVING count(*) > 0
    ) duration ON duration.duration_seconds IS NOT NULL
    WHERE job.status = 'applied'
      AND route.status = 'completed'
      AND NOT EXISTS (
          SELECT 1
          FROM route_optimization_jobs newer
          WHERE newer.route_id = job.route_id
            AND newer.status = 'applied'
            AND (newer.applied_at, newer.id) > (job.applied_at, job.id)
      )
      AND NOT EXISTS (
          SELECT 1 FROM unified_route_stops stop
          WHERE stop.route_id = route.id AND stop.status <> 'completed'
      )
)
SELECT optimization_id, route_id, projected_distance_meters,
       actual_distance_meters, projected_duration_seconds,
       actual_duration_seconds, estimated_savings_cents
FROM completed;
