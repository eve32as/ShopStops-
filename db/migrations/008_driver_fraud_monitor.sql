CREATE TABLE driver_fraud_alerts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    route_id uuid NOT NULL REFERENCES unified_delivery_routes(id) ON DELETE CASCADE,
    driver_ref text NOT NULL,
    sample_id bigint NOT NULL REFERENCES driver_location_samples(id) ON DELETE CASCADE,
    alert_type text NOT NULL CHECK (alert_type IN ('impossible_speed', 'route_expansion')),
    severity text NOT NULL CHECK (severity IN ('high', 'medium')),
    status text NOT NULL DEFAULT 'open'
        CHECK (status IN ('open', 'reviewed', 'dismissed')),
    evidence jsonb NOT NULL CHECK (jsonb_typeof(evidence) = 'object'),
    detected_at timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz,
    resolved_by text,
    UNIQUE (route_id, alert_type),
    CHECK (
        (status = 'open' AND resolved_at IS NULL AND resolved_by IS NULL)
        OR (status <> 'open' AND resolved_at IS NOT NULL AND resolved_by IS NOT NULL)
    )
);
CREATE INDEX driver_fraud_alerts_status_detected_idx
    ON driver_fraud_alerts (status, detected_at DESC);
CREATE INDEX driver_fraud_alerts_driver_detected_idx
    ON driver_fraud_alerts (driver_ref, detected_at DESC);

CREATE FUNCTION detect_driver_telemetry_anomalies()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    v_previous_location geography(Point, 4326);
    v_previous_observed_at timestamptz;
    v_segment_meters double precision;
    v_elapsed_seconds double precision;
    v_planned_meters bigint;
    v_observed_meters double precision;
    v_sample_count bigint;
BEGIN
    SELECT location, observed_at
    INTO v_previous_location, v_previous_observed_at
    FROM driver_location_samples
    WHERE route_id = NEW.route_id
      AND driver_ref = NEW.driver_ref
      AND id <> NEW.id
      AND observed_at < NEW.observed_at
    ORDER BY observed_at DESC
    LIMIT 1;

    IF FOUND THEN
        v_segment_meters := ST_Distance(v_previous_location, NEW.location);
        v_elapsed_seconds := extract(epoch FROM NEW.observed_at - v_previous_observed_at);
        IF v_elapsed_seconds > 0
           AND v_segment_meters / v_elapsed_seconds > 55 THEN
            INSERT INTO driver_fraud_alerts (
                route_id, driver_ref, sample_id, alert_type, severity, evidence
            ) VALUES (
                NEW.route_id, NEW.driver_ref, NEW.id, 'impossible_speed', 'high',
                jsonb_build_object(
                    'speedMetersPerSecond',
                    round((v_segment_meters / v_elapsed_seconds)::numeric, 2),
                    'segmentMeters', round(v_segment_meters::numeric, 2),
                    'elapsedSeconds', round(v_elapsed_seconds::numeric, 2)
                )
            )
            ON CONFLICT (route_id, alert_type) DO NOTHING;
        END IF;
    END IF;

    SELECT COALESCE(sum(distance_meters), 0)
    INTO v_planned_meters
    FROM unified_route_legs
    WHERE route_id = NEW.route_id;

    IF v_planned_meters > 0 THEN
        SELECT count(*), COALESCE(sum(segment_meters), 0)
        INTO v_sample_count, v_observed_meters
        FROM (
            SELECT ST_Distance(
                       lag(location) OVER (ORDER BY observed_at, id), location
                   ) AS segment_meters
            FROM driver_location_samples
            WHERE route_id = NEW.route_id AND driver_ref = NEW.driver_ref
        ) segments;

        IF v_sample_count >= 3
           AND v_observed_meters > v_planned_meters * 2.5 + 1000 THEN
            INSERT INTO driver_fraud_alerts (
                route_id, driver_ref, sample_id, alert_type, severity, evidence
            ) VALUES (
                NEW.route_id, NEW.driver_ref, NEW.id, 'route_expansion', 'medium',
                jsonb_build_object(
                    'observedMeters', round(v_observed_meters::numeric, 2),
                    'plannedMeters', v_planned_meters,
                    'expansionRatio',
                    round((v_observed_meters / v_planned_meters)::numeric, 2),
                    'sampleCount', v_sample_count
                )
            )
            ON CONFLICT (route_id, alert_type) DO NOTHING;
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER driver_location_anomaly_monitor
AFTER INSERT ON driver_location_samples
FOR EACH ROW EXECUTE FUNCTION detect_driver_telemetry_anomalies();
