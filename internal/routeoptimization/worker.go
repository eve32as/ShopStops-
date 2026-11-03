package routeoptimization

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/eve32as/ShopStops-/internal/routing"
)

var ErrNoJob = errors.New("no route optimization job available")
var ErrStale = errors.New("route optimization snapshot is stale")

const maxAttempts = 5

type Router interface {
	Route(context.Context, []routing.Coordinate) (routing.Route, error)
}

type Stop struct {
	ID        string
	Sequence  int
	Type      string
	Latitude  float64
	Longitude float64
}

type WorkItem struct {
	ID         string
	RouteID    string
	Attempts   int
	LeaseToken string
}

type Snapshot struct {
	StopIDs                 []string
	Stops                   []Stop
	BaselineDistanceMeters  int
	BaselineDurationSeconds int
}

type Leg struct {
	FromSequence    int                  `json:"from_sequence"`
	ToSequence      int                  `json:"to_sequence"`
	DistanceMeters  int                  `json:"distance_meters"`
	DurationSeconds int                  `json:"duration_seconds"`
	Geometry        []routing.Coordinate `json:"geometry"`
}

type Repository interface {
	ClaimNext(context.Context, time.Time, time.Duration) (WorkItem, error)
	LoadSnapshot(context.Context, WorkItem) (Snapshot, error)
	Complete(context.Context, WorkItem, int, int, []string, int, []Leg) error
	RetryOrFail(context.Context, WorkItem, string, time.Time) error
	MarkStale(context.Context, WorkItem) error
}

type Worker struct {
	Repository       Repository
	Router           Router
	CostCentsPerMile int64
	Lease            time.Duration
	RetryDelay       time.Duration
}

func (w *Worker) RunOnce(ctx context.Context, now time.Time) (bool, error) {
	if w.Repository == nil || w.Router == nil || w.CostCentsPerMile <= 0 ||
		w.CostCentsPerMile > math.MaxInt32 {
		return false, errors.New("route optimization worker is not configured")
	}
	lease := w.Lease
	if lease <= 0 {
		lease = 2 * time.Minute
	}
	item, err := w.Repository.ClaimNext(ctx, now, lease)
	if errors.Is(err, ErrNoJob) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	snapshot, err := w.Repository.LoadSnapshot(ctx, item)
	if errors.Is(err, ErrStale) {
		return true, w.Repository.MarkStale(ctx, item)
	}
	if err != nil {
		return true, w.retry(ctx, item, "route_unavailable", now)
	}
	if !validSnapshot(snapshot) {
		return true, w.retry(ctx, item, "route_unavailable", now)
	}

	originalIDs := []string{snapshot.Stops[0].ID, snapshot.Stops[1].ID, snapshot.Stops[2].ID}
	swappedIDs := []string{originalIDs[1], originalIDs[0], originalIDs[2]}
	currentLegs, currentDistance, currentDuration, err := w.calculate(ctx, snapshot.Stops)
	if err != nil {
		return true, w.retry(ctx, item, "provider_unavailable", now)
	}
	alternativeStops := []Stop{snapshot.Stops[1], snapshot.Stops[0], snapshot.Stops[2]}
	alternativeLegs, alternativeDistance, _, err :=
		w.calculate(ctx, alternativeStops)
	if err != nil {
		return true, w.retry(ctx, item, "provider_unavailable", now)
	}

	proposedIDs, proposedLegs := originalIDs, currentLegs
	distanceSaved := currentDistance - alternativeDistance
	savings := int64(0)
	if distanceSaved > 0 {
		estimated := math.Round(float64(distanceSaved) / 1609.344 *
			float64(w.CostCentsPerMile))
		if estimated >= math.MaxInt32 {
			savings = math.MaxInt32
		} else {
			savings = int64(estimated)
		}
	}
	if savings > 0 {
		proposedIDs, proposedLegs = swappedIDs, alternativeLegs
	}
	return true, w.Repository.Complete(
		ctx, item, currentDistance, currentDuration, proposedIDs,
		int(savings), withSequences(proposedLegs),
	)
}

func (w *Worker) retry(
	ctx context.Context, item WorkItem, code string, now time.Time,
) error {
	delay := w.RetryDelay
	if delay <= 0 {
		delay = 15 * time.Second
	}
	if item.Attempts >= maxAttempts {
		delay = 0
	}
	return w.Repository.RetryOrFail(ctx, item, code, now.Add(delay))
}

func (w *Worker) calculate(
	ctx context.Context, stops []Stop,
) ([]Leg, int, int, error) {
	if len(stops) != 3 {
		return nil, 0, 0, ErrStale
	}
	results := make(chan legResult, 2)
	for i := 0; i < 2; i++ {
		go func(index int) {
			coordinates := []routing.Coordinate{
				{Latitude: stops[index].Latitude, Longitude: stops[index].Longitude},
				{Latitude: stops[index+1].Latitude, Longitude: stops[index+1].Longitude},
			}
			route, err := w.Router.Route(ctx, coordinates)
			results <- legResult{index: index, route: route, err: err}
		}(i)
	}
	legs := make([]Leg, 2)
	var distance, duration int64
	var firstErr error
	for range 2 {
		result := <-results
		if result.err != nil {
			if firstErr == nil {
				firstErr = result.err
			}
			continue
		}
		if !validMeasurement(result.route.DistanceMeters) ||
			!validMeasurement(result.route.DurationSeconds) ||
			len(result.route.Coordinates) < 2 {
			if firstErr == nil {
				firstErr = errors.New("invalid OSRM route result")
			}
			continue
		}
		legDistance := int(math.Round(result.route.DistanceMeters))
		legDuration := int(math.Round(result.route.DurationSeconds))
		legs[result.index] = Leg{
			FromSequence:    result.index + 1,
			ToSequence:      result.index + 2,
			DistanceMeters:  legDistance,
			DurationSeconds: legDuration,
			Geometry:        result.route.Coordinates,
		}
		distance += int64(legDistance)
		duration += int64(legDuration)
	}
	if firstErr != nil {
		return nil, 0, 0, firstErr
	}
	if distance > math.MaxInt32 || duration > math.MaxInt32 {
		return nil, 0, 0, errors.New("OSRM route totals exceed supported limits")
	}
	return legs, int(distance), int(duration), nil
}

type legResult struct {
	index int
	route routing.Route
	err   error
}

func validSnapshot(snapshot Snapshot) bool {
	if len(snapshot.Stops) != 3 || len(snapshot.StopIDs) != 3 {
		return false
	}
	return snapshot.Stops[0].Sequence == 1 &&
		snapshot.Stops[1].Sequence == 2 &&
		snapshot.Stops[2].Sequence == 3 &&
		snapshot.Stops[0].Type == "pickup" &&
		snapshot.Stops[1].Type == "pickup" &&
		snapshot.Stops[2].Type == "dropoff" &&
		snapshot.StopIDs[0] == snapshot.Stops[0].ID &&
		snapshot.StopIDs[1] == snapshot.Stops[1].ID &&
		snapshot.StopIDs[2] == snapshot.Stops[2].ID
}

func validMeasurement(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) &&
		value >= 0 && value <= math.MaxInt32
}

func withSequences(legs []Leg) []Leg {
	for i := range legs {
		legs[i].FromSequence = i + 1
		legs[i].ToSequence = i + 2
	}
	return legs
}
