package routeoptimization

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"
)

type PostgresRepository struct {
	db *sql.DB
}

func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (p *PostgresRepository) ClaimNext(
	ctx context.Context, now time.Time, lease time.Duration,
) (WorkItem, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkItem{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		WITH expired AS (
			UPDATE route_optimization_jobs
			SET status = 'failed', error_code = 'provider_unavailable',
			    locked_until = NULL, lease_token = NULL, updated_at = $1
			WHERE status = 'processing' AND locked_until <= $1 AND attempts >= $2
			RETURNING id, route_id
		)
		INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
		SELECT 'unified_delivery_route', route_id,
		       'delivery.route.optimization.failed',
		       jsonb_build_object(
		           'routeId', route_id, 'optimizationId', id,
		           'errorCode', 'provider_unavailable'
		       )
		FROM expired`, now, maxAttempts,
	); err != nil {
		return WorkItem{}, err
	}
	var item WorkItem
	err = tx.QueryRowContext(ctx, `
		WITH candidate AS (
			SELECT id
			FROM route_optimization_jobs
			WHERE (
				(status = 'queued' AND available_at <= $1)
				OR (status = 'processing' AND locked_until <= $1)
			) AND attempts < $3
			ORDER BY available_at, created_at, id
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE route_optimization_jobs job
		SET status = 'processing', attempts = attempts + 1,
		    locked_until = $1 + $2::double precision * interval '1 second',
		    lease_token = gen_random_uuid(), updated_at = $1
		FROM candidate
		WHERE job.id = candidate.id
		RETURNING job.id::text, job.route_id::text, job.attempts,
		          job.lease_token::text`,
		now, lease.Seconds(), maxAttempts,
	).Scan(&item.ID, &item.RouteID, &item.Attempts, &item.LeaseToken)
	if errors.Is(err, sql.ErrNoRows) {
		if commitErr := tx.Commit(); commitErr != nil {
			return WorkItem{}, commitErr
		}
		return WorkItem{}, ErrNoJob
	}
	if err != nil {
		return WorkItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkItem{}, err
	}
	return item, nil
}

func (p *PostgresRepository) LoadSnapshot(
	ctx context.Context, item WorkItem,
) (Snapshot, error) {
	var snapshot Snapshot
	var snapshotJSON string
	var routeStatus string
	var driverRef sql.NullString
	err := p.db.QueryRowContext(ctx, `
		SELECT array_to_json(job.route_snapshot_ids)::text,
		       job.baseline_distance_meters, job.baseline_duration_seconds,
		       route.status, route.driver_ref
		FROM route_optimization_jobs job
		JOIN unified_delivery_routes route ON route.id = job.route_id
		WHERE job.id = $1 AND job.status = 'processing'
		  AND route.id = $2`,
		item.ID, item.RouteID,
	).Scan(
		&snapshotJSON, &snapshot.BaselineDistanceMeters,
		&snapshot.BaselineDurationSeconds, &routeStatus, &driverRef,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, ErrStale
	}
	if err != nil {
		return Snapshot{}, err
	}
	if routeStatus != "routed" || driverRef.Valid {
		return Snapshot{}, ErrStale
	}
	if err := json.Unmarshal([]byte(snapshotJSON), &snapshot.StopIDs); err != nil {
		return Snapshot{}, err
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT id::text, sequence, stop_type,
		       ST_Y(location::geometry), ST_X(location::geometry), status
		FROM unified_route_stops
		WHERE route_id = $1 AND id = ANY($2::uuid[])
		ORDER BY sequence`,
		item.RouteID, snapshot.StopIDs,
	)
	if err != nil {
		return Snapshot{}, err
	}
	defer rows.Close()
	snapshot.Stops = make([]Stop, 0, len(snapshot.StopIDs))
	for rows.Next() {
		var stop Stop
		var status string
		if err := rows.Scan(
			&stop.ID, &stop.Sequence, &stop.Type, &stop.Latitude,
			&stop.Longitude, &status,
		); err != nil {
			return Snapshot{}, err
		}
		if status != "pending" {
			return Snapshot{}, ErrStale
		}
		snapshot.Stops = append(snapshot.Stops, stop)
	}
	if err := rows.Err(); err != nil {
		return Snapshot{}, err
	}
	if !validSnapshot(snapshot) {
		return Snapshot{}, ErrStale
	}
	return snapshot, nil
}

func (p *PostgresRepository) Complete(
	ctx context.Context, item WorkItem, baselineDistance, baselineDuration int,
	proposedIDs []string, savings int, legs []Leg,
) error {
	if len(legs) != 2 || baselineDistance < 0 || baselineDuration < 0 {
		return errors.New("invalid route optimization measurements")
	}
	var proposedDistance, proposedDuration int64
	for _, leg := range legs {
		if leg.DistanceMeters < 0 || leg.DurationSeconds < 0 ||
			len(leg.Geometry) < 2 {
			return errors.New("invalid optimized route leg")
		}
		proposedDistance += int64(leg.DistanceMeters)
		proposedDuration += int64(leg.DurationSeconds)
	}
	if proposedDistance > math.MaxInt32 || proposedDuration > math.MaxInt32 {
		return errors.New("optimized route measurements exceed database limits")
	}
	proposedLegs, err := json.Marshal(legs)
	if err != nil {
		return err
	}
	var status string
	return p.db.QueryRowContext(ctx, `
		SELECT complete_route_optimization(
			$1, $2::uuid, $3, $4, $5::uuid[], $6, $7, $8, $9::jsonb
		)`,
		item.ID, item.LeaseToken, baselineDistance, baselineDuration, proposedIDs,
		int(proposedDistance), int(proposedDuration), savings,
		string(proposedLegs),
	).Scan(&status)
}

func (p *PostgresRepository) RetryOrFail(
	ctx context.Context, item WorkItem, code string, availableAt time.Time,
) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	err = tx.QueryRowContext(ctx, `
		UPDATE route_optimization_jobs
		SET status = CASE WHEN attempts >= $2 THEN 'failed' ELSE 'queued' END,
		    available_at = $3, locked_until = NULL, lease_token = NULL, error_code = $4,
		    updated_at = now()
		WHERE id = $1 AND status = 'processing' AND lease_token = $5::uuid
		RETURNING status`,
		item.ID, maxAttempts, availableAt, code, item.LeaseToken,
	).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if status == "failed" {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
			VALUES (
				'unified_delivery_route', $1::uuid,
				'delivery.route.optimization.failed',
				jsonb_build_object(
					'routeId', $1::uuid, 'optimizationId', $2::uuid, 'errorCode', $3
				)
			)`, item.RouteID, item.ID, code); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (p *PostgresRepository) MarkStale(
	ctx context.Context, item WorkItem,
) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE route_optimization_jobs
		SET status = 'stale', locked_until = NULL, lease_token = NULL,
		    updated_at = now()
		WHERE id = $1 AND status = 'processing' AND lease_token = $2::uuid`,
		item.ID, item.LeaseToken)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 1 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
			VALUES (
				'unified_delivery_route', $1::uuid,
				'delivery.route.optimization.stale',
				jsonb_build_object('routeId', $1::uuid, 'optimizationId', $2::uuid)
			)`, item.RouteID, item.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
