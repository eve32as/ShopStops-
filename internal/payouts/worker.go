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
	maxAttempts     = 8
	messageLease    = 5 * time.Minute
)

type WeeklyStatement struct {
	DriverRef           string
	PeriodStart         time.Time
	PeriodEnd           time.Time
	CompletedRoutes     int
	DistanceMeters      int64
	BasePayCents        int64
	MileagePayCents     int64
	TotalEarningsCents  int64
	MileageRateCentsPerMile int64
}

type Message struct {
	ID                string
	AttemptID         int64
	AttemptNumber     int
	Recipient         string
	Statement         WeeklyStatement
}

type Repository interface {
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
	start, end := previousCompleteWeek(now)
	created, err := w.Repository.CreateWeeklyStatements(
		ctx, start, end, w.MileageRateCents,
	)
	if err != nil {
		return 0, 0, fmt.Errorf("create weekly payout statements: %w", err)
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
		DriverRef: driverRef,
		PeriodStart: start.UTC(),
		PeriodEnd: end.UTC(),
		CompletedRoutes: len(routes),
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
