ALTER TABLE delivery_zones
    ADD COLUMN driver_mileage_cost_per_mile_cents integer NOT NULL DEFAULT 65
        CHECK (driver_mileage_cost_per_mile_cents BETWEEN 0 AND 100000);

UPDATE delivery_zones
SET driver_mileage_cost_per_mile_cents = 65
WHERE name = 'Metro Downtown Sector';

UPDATE delivery_zones
SET driver_mileage_cost_per_mile_cents = 80
WHERE name = 'North Logistics Hub';
