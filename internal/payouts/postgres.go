package payouts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
)

type PostgresRepository struct {
	db *sql.DB
}

func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (p *PostgresRepository) CreateWeeklyStatements(
	ctx context.Context, start, end time.Time, mileageRateCents int64,
) (int, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		WITH completed_routes AS (
			SELECT route.id, route.driver_ref, completion.completed_at,
			       COALESCE(distance.distance_meters, 0) AS distance_meters
			FROM unified_delivery_routes route
			JOIN LATERAL (
				SELECT max(completed_at) AS completed_at
				FROM unified_route_stops
				WHERE route_id = route.id
				HAVING count(*) > 0 AND count(*) = count(completed_at)
			) completion ON true
			LEFT JOIN LATERAL (
				SELECT sum(distance_meters)::bigint AS distance_meters
				FROM unified_route_legs
				WHERE route_id = route.id
			) distance ON true
			WHERE route.status = 'completed'
			  AND route.driver_ref IS NOT NULL
			  AND completion.completed_at >= $1
			  AND completion.completed_at < $2
		)
		SELECT completed.driver_ref, completed.id::text,
		       completed.distance_meters, profile.email_address
		FROM completed_routes completed
		JOIN driver_payout_profiles profile
		  ON profile.driver_ref = completed.driver_ref AND profile.enabled
		ORDER BY completed.driver_ref, completed.completed_at, completed.id`,
		start, end,
	)
	if err != nil {
		return 0, err
	}
	routesByDriver := make(map[string][]CompletedRoute)
	emailsByDriver := make(map[string]string)
	for rows.Next() {
		var driverRef, routeID, email string
		var distance int64
		if err := rows.Scan(&driverRef, &routeID, &distance, &email); err != nil {
			rows.Close()
			return 0, err
		}
		routesByDriver[driverRef] = append(routesByDriver[driverRef], CompletedRoute{
			DriverRef: driverRef, DistanceMeters: distance,
		})
		emailsByDriver[driverRef] = email
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	created := 0
	for driverRef, routes := range routesByDriver {
		statement, err := CalculateWeeklyStatement(
			ctx, driverRef, routes, start, end, mileageRateCents,
		)
		if err != nil {
			return 0, err
		}
		var statementID string
		err = tx.QueryRowContext(ctx, `
			INSERT INTO driver_weekly_earnings_statements (
				driver_ref, period_start, period_end, completed_routes,
				distance_meters, base_pay_cents, mileage_pay_cents,
				total_earnings_cents, mileage_rate_cents_per_mile
			) VALUES ($1, $2::date, $3::date, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (driver_ref, period_start, period_end) DO NOTHING
			RETURNING id::text`,
			driverRef, start, end, statement.CompletedRoutes, statement.DistanceMeters,
			statement.BasePayCents, statement.MileagePayCents, statement.TotalEarningsCents,
			mileageRateCents,
		).Scan(&statementID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO delivery_message_logs (
				statement_id, recipient_type, recipient_ref, recipient_address,
				channel, notification_type
			) VALUES ($1, 'driver', $2, $3, 'email', 'weekly_payout_statement')`,
			statementID, driverRef, emailsByDriver[driverRef],
		); err != nil {
			return 0, err
		}
		created++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return created, nil
}

func (p *PostgresRepository) ClaimNextMessage(
	ctx context.Context, now time.Time, lease time.Duration,
) (Message, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()

	var messageID string
	var attempts int
	err = tx.QueryRowContext(ctx, `
		SELECT id::text, attempts
		FROM delivery_message_logs
		WHERE (
			(status IN ('queued', 'failed') AND available_at <= $1 AND attempts < $2)
			OR (status = 'sending' AND locked_until < $1)
		)
		ORDER BY available_at, created_at, id
		FOR UPDATE SKIP LOCKED
		LIMIT 1`, now, maxAttempts,
	).Scan(&messageID, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNoMessage
	}
	if err != nil {
		return Message{}, err
	}
	if attempts > 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE delivery_message_attempts
			SET status = 'failed', error_code = 'worker_lease_expired', completed_at = $2
			WHERE id = (
				SELECT id FROM delivery_message_attempts
				WHERE message_id = $1 AND status = 'sending'
				ORDER BY started_at DESC, id DESC LIMIT 1
			)`, messageID, now,
		); err != nil {
			return Message{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE delivery_message_logs
		SET status = 'sending', attempts = attempts + 1,
		    locked_until = $2, updated_at = $1, last_error = NULL
		WHERE id = $3`, now, now.Add(lease), messageID,
	); err != nil {
		return Message{}, err
	}
	var message Message
	message.ID = messageID
	message.AttemptNumber = attempts + 1
	message.Recipient = ""
	err = tx.QueryRowContext(ctx, `
		SELECT message.recipient_address, statement.driver_ref,
		       statement.period_start, statement.period_end,
		       statement.completed_routes, statement.distance_meters,
		       statement.base_pay_cents, statement.mileage_pay_cents,
		       statement.total_earnings_cents,
		       statement.mileage_rate_cents_per_mile
		FROM delivery_message_logs message
		JOIN driver_weekly_earnings_statements statement
		  ON statement.id = message.statement_id
		WHERE message.id = $1`, messageID,
	).Scan(
		&message.Recipient, &message.Statement.DriverRef,
		&message.Statement.PeriodStart, &message.Statement.PeriodEnd,
		&message.Statement.CompletedRoutes, &message.Statement.DistanceMeters,
		&message.Statement.BasePayCents, &message.Statement.MileagePayCents,
		&message.Statement.TotalEarningsCents,
		&message.Statement.MileageRateCentsPerMile,
	)
	if err != nil {
		return Message{}, err
	}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO delivery_message_attempts (message_id, status, started_at)
		VALUES ($1, 'sending', $2) RETURNING id`,
		messageID, now,
	).Scan(&message.AttemptID); err != nil {
		return Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	return message, nil
}

func (p *PostgresRepository) CompleteMessage(
	ctx context.Context, message Message, providerMessageID string,
) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `
		UPDATE delivery_message_logs
		SET status = 'sent', sent_at = $2, updated_at = $2,
		    locked_until = NULL, last_error = NULL, provider_message_id = $3
		WHERE id = $1 AND status = 'sending'`,
		message.ID, now, providerMessageID,
	)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return errors.New("payout message lease is no longer active")
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE delivery_message_attempts
		SET status = 'sent', provider_message_id = $2, completed_at = $3
		WHERE id = $1 AND status = 'sending'`,
		message.AttemptID, providerMessageID, now,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *PostgresRepository) FailMessage(
	ctx context.Context, message Message, errorCode string, now time.Time,
) error {
	delay := time.Minute
	for attempt := 1; attempt < message.AttemptNumber && delay < 24*time.Hour; attempt++ {
		delay *= 2
	}
	if delay > 24*time.Hour {
		delay = 24 * time.Hour
	}
	if message.AttemptNumber > maxAttempts {
		delay = 365 * 24 * time.Hour
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE delivery_message_logs
		SET status = 'failed', locked_until = NULL, last_error = $2,
		    available_at = $3, updated_at = $4
		WHERE id = $1 AND status = 'sending'`,
		message.ID, errorCode, now.Add(delay), now,
	)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return errors.New("payout message lease is no longer active")
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE delivery_message_attempts
		SET status = 'failed', error_code = $2, completed_at = $3
		WHERE id = $1 AND status = 'sending'`,
		message.AttemptID, errorCode, now,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func validateStatement(statement WeeklyStatement) error {
	if statement.TotalEarningsCents != statement.BasePayCents+statement.MileagePayCents ||
		statement.CompletedRoutes <= 0 || statement.DistanceMeters < 0 ||
		statement.BasePayCents < 0 || statement.MileagePayCents < 0 ||
		statement.BasePayCents > math.MaxInt64-statement.MileagePayCents {
		return fmt.Errorf("invalid weekly earnings statement")
	}
	return nil
}
