package routeoptimization

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/eve32as/ShopStops-/internal/routing"
)

type fakeRouter struct {
	distances map[string]float64
	err       error
}

func (f fakeRouter) Route(
	_ context.Context, coordinates []routing.Coordinate,
) (routing.Route, error) {
	if f.err != nil {
		return routing.Route{}, f.err
	}
	if len(coordinates) != 2 {
		return routing.Route{}, errors.New("expected a single route leg")
	}
	key := string(rune('0'+int(coordinates[0].Longitude))) +
		string(rune('0'+int(coordinates[1].Longitude)))
	distance, ok := f.distances[key]
	if !ok {
		return routing.Route{}, errors.New("missing test distance")
	}
	return routing.Route{
		DistanceMeters: distance, DurationSeconds: distance / 10,
		Coordinates: coordinates,
	}, nil
}

type fakeRepository struct {
	item        WorkItem
	snapshot    Snapshot
	claimed     bool
	completed   bool
	stale       bool
	failureCode string
	proposedIDs []string
	savings     int
	baseline    int
	legs        []Leg
}

func (f *fakeRepository) ClaimNext(
	_ context.Context, _ time.Time, _ time.Duration,
) (WorkItem, error) {
	if f.claimed {
		return WorkItem{}, ErrNoJob
	}
	f.claimed = true
	return f.item, nil
}

func (f *fakeRepository) LoadSnapshot(
	_ context.Context, _ WorkItem,
) (Snapshot, error) {
	return f.snapshot, nil
}

func (f *fakeRepository) Complete(
	_ context.Context, _ WorkItem, distance, _ int, ids []string,
	savings int, legs []Leg,
) error {
	f.completed = true
	f.baseline = distance
	f.proposedIDs = append([]string(nil), ids...)
	f.savings = savings
	f.legs = legs
	return nil
}

func (f *fakeRepository) RetryOrFail(
	_ context.Context, _ WorkItem, code string, _ time.Time,
) error {
	f.failureCode = code
	return nil
}

func (f *fakeRepository) MarkStale(_ context.Context, _ WorkItem) error {
	f.stale = true
	return nil
}

func validTestSnapshot() Snapshot {
	return Snapshot{
		StopIDs: []string{"stop-a", "stop-b", "stop-c"},
		Stops: []Stop{
			{ID: "stop-a", Sequence: 1, Type: "pickup", Longitude: 1},
			{ID: "stop-b", Sequence: 2, Type: "pickup", Longitude: 2},
			{ID: "stop-c", Sequence: 3, Type: "dropoff", Longitude: 3},
		},
	}
}

func TestWorkerCreatesPreviewForCheaperPickupOrder(t *testing.T) {
	repository := &fakeRepository{
		item:     WorkItem{ID: "job", RouteID: "route", Attempts: 1},
		snapshot: validTestSnapshot(),
	}
	router := fakeRouter{distances: map[string]float64{
		"12": 1000, "23": 2000, "21": 200, "13": 300,
	}}
	worker := Worker{
		Repository: repository, Router: router, CostCentsPerMile: 100,
	}
	processed, err := worker.RunOnce(context.Background(), time.Now())
	if err != nil || !processed {
		t.Fatalf("RunOnce = %v, %v", processed, err)
	}
	if !repository.completed || repository.baseline != 3000 ||
		repository.savings <= 0 ||
		len(repository.proposedIDs) != 3 ||
		repository.proposedIDs[0] != "stop-b" ||
		repository.proposedIDs[1] != "stop-a" ||
		len(repository.legs) != 2 ||
		repository.legs[0].FromSequence != 1 ||
		repository.legs[1].ToSequence != 3 {
		t.Fatalf("unexpected optimization preview: %#v", repository)
	}
}

func TestWorkerDoesNotSuggestMoreExpensivePickupOrder(t *testing.T) {
	repository := &fakeRepository{
		item:     WorkItem{ID: "job", RouteID: "route", Attempts: 1},
		snapshot: validTestSnapshot(),
	}
	router := fakeRouter{distances: map[string]float64{
		"12": 200, "23": 300, "21": 1000, "13": 2000,
	}}
	worker := Worker{
		Repository: repository, Router: router, CostCentsPerMile: 100,
	}
	_, err := worker.RunOnce(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !repository.completed || repository.savings != 0 ||
		repository.proposedIDs[0] != "stop-a" {
		t.Fatalf("unexpected no-improvement preview: %#v", repository)
	}
}

func TestWorkerRetriesProviderErrors(t *testing.T) {
	repository := &fakeRepository{
		item:     WorkItem{ID: "job", RouteID: "route", Attempts: 1},
		snapshot: validTestSnapshot(),
	}
	worker := Worker{
		Repository:       repository,
		Router:           fakeRouter{err: errors.New("provider unavailable")},
		CostCentsPerMile: 100,
	}
	_, err := worker.RunOnce(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if repository.failureCode != "provider_unavailable" || repository.completed {
		t.Fatalf("provider error was not retryable: %#v", repository)
	}
}
