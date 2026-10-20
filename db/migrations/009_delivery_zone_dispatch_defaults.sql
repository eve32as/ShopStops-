ALTER TABLE delivery_zones
    ADD COLUMN dispatch_max_orders integer NOT NULL DEFAULT 3
        CHECK (dispatch_max_orders BETWEEN 1 AND 100),
    ADD COLUMN dispatch_pickup_radius_m integer NOT NULL DEFAULT 2000
        CHECK (dispatch_pickup_radius_m BETWEEN 0 AND 100000),
    ADD COLUMN dispatch_dropoff_radius_m integer NOT NULL DEFAULT 2000
        CHECK (dispatch_dropoff_radius_m BETWEEN 0 AND 100000),
    ADD COLUMN dispatch_limits_updated_by text NOT NULL DEFAULT 'migration',
    ADD COLUMN dispatch_limits_updated_at timestamptz NOT NULL DEFAULT now();
