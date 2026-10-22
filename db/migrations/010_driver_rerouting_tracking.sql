ALTER TABLE unified_route_legs
    ADD COLUMN geometry jsonb CHECK (
        geometry IS NULL OR (
            jsonb_typeof(geometry) = 'array' AND jsonb_array_length(geometry) BETWEEN 2 AND 10000
        )
    );

CREATE TABLE driver_route_reroutes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    route_id uuid NOT NULL REFERENCES unified_delivery_routes(id) ON DELETE CASCADE,
    driver_ref text NOT NULL,
    sample_id bigint NOT NULL UNIQUE REFERENCES driver_location_samples(id) ON DELETE CASCADE,
    to_sequence integer NOT NULL CHECK (to_sequence > 1),
    idempotency_key text NOT NULL UNIQUE,
    driver_location geography(Point, 4326) NOT NULL,
    deviation_miles numeric(10, 4) NOT NULL CHECK (deviation_miles > 0.2),
    distance_meters integer NOT NULL CHECK (distance_meters >= 0),
    duration_seconds integer NOT NULL CHECK (duration_seconds >= 0),
    geometry jsonb NOT NULL CHECK (
        jsonb_typeof(geometry) = 'array' AND jsonb_array_length(geometry) BETWEEN 2 AND 10000
    ),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX driver_route_reroutes_route_created_idx
    ON driver_route_reroutes (route_id, created_at DESC);
