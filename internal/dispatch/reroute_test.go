package dispatch

import (
	"context"
	"errors"
	"testing"

	"github.com/eve32as/ShopStops-/internal/routing"
)

type fakeRouter struct {
	route       routing.Route
	err         error
	coordinates []routing.Coordinate
}

func (f *fakeRouter) Route(_ context.Context, coordinates []routing.Coordinate) (routing.Route, error) {
	f.coordinates = coordinates
	return f.route, f.err
}

func TestEvaluateCourseCorrectionUsesPointTwoMileThreshold(t *testing.T) {
	router := &fakeRouter{
		route: routing.Route{
			DistanceMeters: 1.3 * 1609.344,
			Coordinates: []routing.Coordinate{
				{Longitude: -77, Latitude: 38.9},
				{Longitude: -76.99, Latitude: 38.91},
			},
		},
	}
	rerouter := NewReRouter(router)
	rerouted, coordinates, deviation, route, err := rerouter.EvaluateCourseCorrectionDetails(
		context.Background(), "route-1", -77, 38.9, -76.99, 38.91, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !rerouted || len(coordinates) != 2 || deviation < 0.2999 ||
		route.DistanceMeters != router.route.DistanceMeters {
		t.Fatalf("rerouted=%v coords=%+v deviation=%f route=%+v",
			rerouted, coordinates, deviation, route)
	}
	if len(router.coordinates) != 2 || router.coordinates[0].Longitude != -77 ||
		router.coordinates[1].Latitude != 38.91 {
		t.Fatalf("router coordinates = %+v", router.coordinates)
	}
}

func TestEvaluateCourseCorrectionDoesNotRerouteAtThreshold(t *testing.T) {
	router := &fakeRouter{route: routing.Route{DistanceMeters: 1.2 * 1609.344}}
	rerouted, coordinates, err := NewReRouter(router).EvaluateCourseCorrection(
		context.Background(), "route-1", -77, 38.9, -76.99, 38.91, 1,
	)
	if err != nil || rerouted || coordinates != nil {
		t.Fatalf("rerouted=%v coordinates=%+v err=%v", rerouted, coordinates, err)
	}
}

func TestEvaluateCourseCorrectionReturnsRouterErrors(t *testing.T) {
	wantErr := errors.New("OSRM unavailable")
	rerouted, coordinates, err := NewReRouter(&fakeRouter{err: wantErr}).
		EvaluateCourseCorrection(context.Background(), "route-1", -77, 38.9, -76.99, 38.91, 1)
	if !errors.Is(err, wantErr) || rerouted || coordinates != nil {
		t.Fatalf("rerouted=%v coordinates=%+v err=%v", rerouted, coordinates, err)
	}
}

func TestEvaluateCourseCorrectionRejectsInvalidInputs(t *testing.T) {
	rerouter := NewReRouter(&fakeRouter{})
	if _, _, err := rerouter.EvaluateCourseCorrection(
		context.Background(), "route", 181, 0, 0, 0, 1,
	); err == nil {
		t.Fatal("invalid coordinate was accepted")
	}
	if _, _, err := rerouter.EvaluateCourseCorrection(
		context.Background(), "route", 0, 0, 1, 1, -1,
	); err == nil {
		t.Fatal("negative planned distance was accepted")
	}
}
