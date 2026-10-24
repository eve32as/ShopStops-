package payouts

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/eve32as/ShopStops-/finance"
)

var ErrNoMessage = errors.New("no payout messages are ready")

const (
	weeklySendLimit = 50
	weeklyPeriodLimit = 52
	maxAttempts     = 8
	messageLease    = 5 * time.Minute
)

type StatementPeriod struct {
	Start time.Time
	End   time.Time
}

type WeeklyStatement struct {
	DriverRef               string
	PeriodStart             time.Time
	PeriodEnd               time.Time
	CompletedRoutes         int
	DistanceMeters          int64
	BasePayCents            int64
	MileagePayCents         int64
	TotalEarningsCents      int64
	MileageRateCentsPerMile int64
}

type Message struct {
	ID            string
	AttemptID     int64
	AttemptNumber int
	Recipient     string
	Statement     WeeklyStatement
}

type Repository interface {
	PendingStatementPeriods(context.Context, time.Time, int) ([]StatementPeriod, error)
	CreateWeeklyStatements(context.Context, time.Time, time.Time, int64) (int, error)
	ClaimNextMessage(context.Context, time.Time, time.Duration) (Message, error)
	CompleteMessage(context.Context, Message, string) error
	FailMessage(context.Context, Message, string, time.Time) error
}

type Sender interface {
	Send(context.Context, Message) (string, error)
}

type Worker struct {
	Repository       Repository
	Sender           Sender
	MileageRateCents int64
}

func (w *Worker) RunOnce(ctx context.Context, now time.Time) (int, int, error) {
	if w == nil || w.Repository == nil || w.Sender == nil || w.MileageRateCents < 0 {
		return 0, 0, errors.New("payout worker is not configured")
	}
	_, throughEnd := previousCompleteWeek(now)
	periods, err := w.Repository.PendingStatementPeriods(ctx, throughEnd, weeklyPeriodLimit)
	if err != nil {
		return 0, 0, fmt.Errorf("find pending weekly payout periods: %w", err)
	}
	created := 0
	for _, period := range periods {
		count, err := w.Repository.CreateWeeklyStatements(
			ctx, period.Start, period.End, w.MileageRateCents,
		)
		if err != nil {
			return created, 0, fmt.Errorf(
				"create weekly payout statements for %s: %w",
				period.Start.Format("2006-01-02"), err,
			)
		}
		created += count
	}
	sent := 0
	for range weeklySendLimit {
		message, err := w.Repository.ClaimNextMessage(ctx, now, messageLease)
		if errors.Is(err, ErrNoMessage) {
			break
		}
		if err != nil {
			return created, sent, fmt.Errorf("claim payout message: %w", err)
		}
		if message.AttemptNumber > maxAttempts {
			if err := w.Repository.FailMessage(ctx, message, "automatic_attempt_limit", now); err != nil {
				return created, sent, fmt.Errorf("stop exhausted payout message: %w", err)
			}
			continue
		}
		providerID, sendErr := w.Sender.Send(ctx, message)
		if sendErr != nil {
			if err := w.Repository.FailMessage(ctx, message, "smtp_delivery_failed", now); err != nil {
				return created, sent, fmt.Errorf("record payout delivery failure: %w", err)
			}
			continue
		}
		if err := w.Repository.CompleteMessage(ctx, message, providerID); err != nil {
			return created, sent, fmt.Errorf("record payout delivery: %w", err)
		}
		sent++
	}
	return created, sent, nil
}

func (p *PostgresRepository) PendingStatementPeriods(
	ctx context.Context, throughEnd time.Time, limit int,
) ([]StatementPeriod, error) {
	rows, err := p.db.QueryContext(ctx, `
		WITH completed_driver_weeks AS (
			SELECT DISTINCT route.driver_ref,
			       date_trunc('week', completion.completed_at AT TIME ZONE 'UTC')::date
			           AS period_start
			FROM unified_delivery_routes route
			JOIN LATERAL (
				SELECT max(completed_at) AS completed_at
				FROM unified_route_stops
				WHERE route_id = route.id
				HAVING count(*) > 0 AND count(*) = count(completed_at)
			) completion ON true
			JOIN driver_payout_profiles profile
			  ON profile.driver_ref = route.driver_ref AND profile.enabled
			WHERE route.status = 'completed'
			  AND route.driver_ref IS NOT NULL
			  AND completion.completed_at < $1
		)
		SELECT DISTINCT period.period_start,
		       period.period_start + 7 AS period_end
		FROM completed_driver_weeks period
		WHERE NOT EXISTS (
			SELECT 1
			FROM driver_weekly_earnings_statements statement
			WHERE statement.driver_ref = period.driver_ref
			  AND statement.period_start = period.period_start
		)
		ORDER BY period.period_start
		LIMIT $2`, throughEnd, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	periods := make([]StatementPeriod, 0)
	for rows.Next() {
		var period StatementPeriod
		if err := rows.Scan(&period.Start, &period.End); err != nil {
			return nil, err
		}
		period.Start = period.Start.UTC()
		period.End = period.End.UTC()
		periods = append(periods, period)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return periods, nil
}

func previousCompleteWeek(now time.Time) (time.Time, time.Time) {
	now = now.UTC()
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	currentMonday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).
		AddDate(0, 0, 1-weekday)
	start := currentMonday.AddDate(0, 0, -7)
	return start, currentMonday
}

type CompletedRoute struct {
	DriverRef      string
	DistanceMeters int64
}

func CalculateWeeklyStatement(
	ctx context.Context, driverRef string, routes []CompletedRoute,
	start, end time.Time, mileageRateCents int64,
) (WeeklyStatement, error) {
	if driverRef == "" || len(routes) == 0 || mileageRateCents < 0 ||
		!start.Before(end) {
		return WeeklyStatement{}, finance.ErrInvalidPricingInput
	}
	statement := WeeklyStatement{
		DriverRef:               driverRef,
		PeriodStart:             start.UTC(),
		PeriodEnd:               end.UTC(),
		CompletedRoutes:         len(routes),
		MileageRateCentsPerMile: mileageRateCents,
	}
	engine := finance.NewProductionPricingEngine(mileageRateCents, false, false)
	for _, route := range routes {
		if route.DriverRef != driverRef || route.DistanceMeters < 0 ||
			statement.DistanceMeters > math.MaxInt64-route.DistanceMeters {
			return WeeklyStatement{}, finance.ErrInvalidPricingInput
		}
		miles := float64(route.DistanceMeters) / 1609.344
		bill, err := engine.ComputeProductionInvoice(ctx, 0, miles)
		if err != nil {
			return WeeklyStatement{}, err
		}
		if statement.BasePayCents > math.MaxInt64-bill.DriverBasePay ||
			statement.MileagePayCents > math.MaxInt64-bill.DriverMileageCost {
			return WeeklyStatement{}, finance.ErrInvalidPricingInput
		}
		statement.DistanceMeters += route.DistanceMeters
		statement.BasePayCents += bill.DriverBasePay
		statement.MileagePayCents += bill.DriverMileageCost
	}
	if statement.BasePayCents > math.MaxInt64-statement.MileagePayCents {
		return WeeklyStatement{}, finance.ErrInvalidPricingInput
	}
	statement.TotalEarningsCents = statement.BasePayCents + statement.MileagePayCents
	return statement, nil
}
