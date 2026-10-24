package payouts

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeRepository struct {
	createdStart time.Time
	createdEnd   time.Time
	rate         int64
	created      int
	createErr       error
	failCreateAt    int
	createCalls     int
	periods         []StatementPeriod
	periodLimit  int
	throughEnd   time.Time
	createdPeriods []StatementPeriod
	messages     []Message
	completeErr  error
	failedCodes  []string
}

func (f *fakeRepository) PendingStatementPeriods(
	_ context.Context, throughEnd time.Time, limit int,
) ([]StatementPeriod, error) {
	f.throughEnd = throughEnd
	f.periodLimit = limit
	return f.periods, nil
}

func (f *fakeRepository) CreateWeeklyStatements(
	_ context.Context, start, end time.Time, rate int64,
) (int, error) {
	f.createCalls++
	f.createdStart, f.createdEnd, f.rate = start, end, rate
	f.createdPeriods = append(f.createdPeriods, StatementPeriod{Start: start, End: end})
	if f.failCreateAt == f.createCalls {
		return 0, f.createErr
	}
	return f.created, nil
}

func (f *fakeRepository) ClaimNextMessage(
	_ context.Context, _ time.Time, _ time.Duration,
) (Message, error) {
	if len(f.messages) == 0 {
		return Message{}, ErrNoMessage
	}
	message := f.messages[0]
	f.messages = f.messages[1:]
	return message, nil
}

func (f *fakeRepository) CompleteMessage(context.Context, Message, string) error {
	return f.completeErr
}

func (f *fakeRepository) FailMessage(
	_ context.Context, _ Message, errorCode string, _ time.Time,
) error {
	f.failedCodes = append(f.failedCodes, errorCode)
	return nil
}

type fakeSender struct {
	err error
}

func (f fakeSender) Send(context.Context, Message) (string, error) {
	return "provider-message-id", f.err
}

func TestPreviousCompleteWeekUsesUTCWeeksStartingMonday(t *testing.T) {
	now := time.Date(2026, time.October, 21, 19, 45, 0, 0, time.FixedZone("test", -7*60*60))
	start, end := previousCompleteWeek(now)
	if want := time.Date(2026, time.October, 12, 0, 0, 0, 0, time.UTC); !start.Equal(want) {
		t.Fatalf("start = %s, want %s", start, want)
	}
	if want := time.Date(2026, time.October, 19, 0, 0, 0, 0, time.UTC); !end.Equal(want) {
		t.Fatalf("end = %s, want %s", end, want)
	}
}

func TestCalculateWeeklyStatementAggregatesCompletedRoutePay(t *testing.T) {
	start := time.Date(2026, time.January, 5, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 7)
	statement, err := CalculateWeeklyStatement(
		context.Background(), "driver-1",
		[]CompletedRoute{
			{DriverRef: "driver-1", DistanceMeters: 1609},
			{DriverRef: "driver-1", DistanceMeters: 3219},
		},
		start, end, 100,
	)
	if err != nil {
		t.Fatal(err)
	}
	if statement.CompletedRoutes != 2 || statement.DistanceMeters != 4828 ||
		statement.BasePayCents != 1400 || statement.MileagePayCents != 300 ||
		statement.TotalEarningsCents != 1700 {
		t.Fatalf("unexpected statement: %+v", statement)
	}
}

func TestCalculateWeeklyStatementRejectsInvalidRoutes(t *testing.T) {
	start := time.Date(2026, time.January, 5, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		ref    string
		routes []CompletedRoute
		rate   int64
	}{
		{name: "missing driver", routes: []CompletedRoute{{DriverRef: "driver-1"}}},
		{name: "wrong driver", ref: "driver-1", routes: []CompletedRoute{{DriverRef: "driver-2"}}},
		{name: "negative distance", ref: "driver-1", routes: []CompletedRoute{{DriverRef: "driver-1", DistanceMeters: -1}}},
		{name: "negative rate", ref: "driver-1", routes: []CompletedRoute{{DriverRef: "driver-1"}}, rate: -1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := CalculateWeeklyStatement(
				context.Background(), test.ref, test.routes, start, start.AddDate(0, 0, 7), test.rate,
			); err == nil {
				t.Fatal("expected invalid input error")
			}
		})
	}
}

func TestWorkerCreatesAndSendsWeeklyStatements(t *testing.T) {
	now := time.Date(2026, time.October, 21, 12, 0, 0, 0, time.UTC)
	repository := &fakeRepository{
		created:  1,
		periods: []StatementPeriod{{
			Start: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC),
		}},
		messages: []Message{{ID: "message-1", AttemptNumber: 1}},
	}
	worker := &Worker{
		Repository:       repository,
		Sender:           fakeSender{},
		MileageRateCents: 45,
	}
	created, sent, err := worker.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if created != 1 || sent != 1 {
		t.Fatalf("created/sent = %d/%d, want 1/1", created, sent)
	}
	if repository.rate != 45 ||
		!repository.createdStart.Equal(time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)) ||
		!repository.createdEnd.Equal(time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected statement period/rate: %s..%s rate=%d",
			repository.createdStart, repository.createdEnd, repository.rate)
	}
	if repository.periodLimit != weeklyPeriodLimit ||
		!repository.throughEnd.Equal(time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected catch-up cutoff/limit: %s/%d",
			repository.throughEnd, repository.periodLimit)
	}
}

func TestWorkerCatchesUpAllPendingWeeksInOrder(t *testing.T) {
	start := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	repository := &fakeRepository{
		created: 1,
		periods: []StatementPeriod{
			{Start: start, End: start.AddDate(0, 0, 7)},
			{Start: start.AddDate(0, 0, 7), End: start.AddDate(0, 0, 14)},
			{Start: start.AddDate(0, 0, 14), End: start.AddDate(0, 0, 21)},
		},
	}
	worker := &Worker{Repository: repository, Sender: fakeSender{}, MileageRateCents: 45}
	created, sent, err := worker.RunOnce(context.Background(),
		time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if created != 3 || sent != 0 || len(repository.createdPeriods) != 3 {
		t.Fatalf("created/sent/periods = %d/%d/%d, want 3/0/3",
			created, sent, len(repository.createdPeriods))
	}
	for index, period := range repository.createdPeriods {
		if !period.Start.Equal(repository.periods[index].Start) ||
			!period.End.Equal(repository.periods[index].End) {
			t.Errorf("period %d = %+v, want %+v", index, period, repository.periods[index])
		}
	}
}

func TestWorkerDoesNotGenerateStatementsWhenNoPeriodsArePending(t *testing.T) {
	repository := &fakeRepository{}
	worker := &Worker{Repository: repository, Sender: fakeSender{}}
	created, sent, err := worker.RunOnce(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}

	func TestWorkerKeepsEarlierPeriodsWhenCatchUpRunFails(t *testing.T) {
		start := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
		repository := &fakeRepository{
			created: 1,
			failCreateAt: 2,
			createErr: errors.New("database unavailable"),
			periods: []StatementPeriod{
				{Start: start, End: start.AddDate(0, 0, 7)},
				{Start: start.AddDate(0, 0, 7), End: start.AddDate(0, 0, 14)},
				{Start: start.AddDate(0, 0, 14), End: start.AddDate(0, 0, 21)},
			},
		}
		worker := &Worker{Repository: repository, Sender: fakeSender{}}
		created, _, err := worker.RunOnce(context.Background(),
			time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC))
		if err == nil {
			t.Fatal("expected catch-up run error")
		}
		if created != 1 || repository.createCalls != 2 {
			t.Fatalf("created/calls = %d/%d, want 1/2", created, repository.createCalls)
		}
	}
	if created != 0 || sent != 0 || len(repository.createdPeriods) != 0 {
		t.Fatalf("empty run created/sent/periods = %d/%d/%d",
			created, sent, len(repository.createdPeriods))
	}
}

func TestWorkerRecordsSMTPFailureAndContinues(t *testing.T) {
	repository := &fakeRepository{
		messages: []Message{{ID: "message-1", AttemptNumber: 1}},
	}
	worker := &Worker{
		Repository: repository,
		Sender:     fakeSender{err: errors.New("smtp rejected")},
	}
	_, sent, err := worker.RunOnce(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if sent != 0 || len(repository.failedCodes) != 1 ||
		repository.failedCodes[0] != "smtp_delivery_failed" {
		t.Fatalf("unexpected failed delivery result: sent=%d errors=%v",
			sent, repository.failedCodes)
	}
}

func TestWorkerStopsMessagesPastAutomaticAttemptLimit(t *testing.T) {
	repository := &fakeRepository{
		messages: []Message{{ID: "message-1", AttemptNumber: maxAttempts + 1}},
	}
	worker := &Worker{Repository: repository, Sender: fakeSender{}}
	_, sent, err := worker.RunOnce(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if sent != 0 || len(repository.failedCodes) != 1 ||
		repository.failedCodes[0] != "automatic_attempt_limit" {
		t.Fatalf("unexpected exhausted delivery result: sent=%d errors=%v",
			sent, repository.failedCodes)
	}
}
