package dispatch

import (
	"context"
	"errors"
	"math"

	"github.com/eve32as/ShopStops-/internal/routing"
)

const (
	milesPerMeter      = 1 / 1609.344
	rerouteThresholdMi = 0.2
)

type Router interface {
	Route(context.Context, []routing.Coordinate) (routing.Route, error)
}

type LocationCoordinate struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type ReRouter struct {
	osrmClient Router
}

func NewReRouter(client Router) *ReRouter {
	return &ReRouter{osrmClient: client}
}

func (r *ReRouter) EvaluateCourseCorrection(
	ctx context.Context, routeID string,
	currentLon, currentLat, targetLon, targetLat, plannedDistanceMiles float64,
) (bool, []LocationCoordinate, error) {
	rerouted, coordinates, _, _, err := r.EvaluateCourseCorrectionDetails(
		ctx, routeID, currentLon, currentLat, targetLon, targetLat, plannedDistanceMiles,
	)
	return rerouted, coordinates, err
}

func (r *ReRouter) EvaluateCourseCorrectionDetails(
	ctx context.Context, _ string,
	currentLon, currentLat, targetLon, targetLat, plannedDistanceMiles float64,
) (bool, []LocationCoordinate, float64, routing.Route, error) {
	if r == nil || r.osrmClient == nil {
		return false, nil, 0, routing.Route{}, errors.New("OSRM router is unavailable")
	}
	if !validCoordinate(currentLon, currentLat) || !validCoordinate(targetLon, targetLat) ||
		math.IsNaN(plannedDistanceMiles) || math.IsInf(plannedDistanceMiles, 0) ||
		plannedDistanceMiles < 0 {
		return false, nil, 0, routing.Route{}, errors.New("invalid route correction input")
	}
	route, err := r.osrmClient.Route(ctx, []routing.Coordinate{
		{Longitude: currentLon, Latitude: currentLat},
		{Longitude: targetLon, Latitude: targetLat},
	})
	if err != nil {
		return false, nil, 0, routing.Route{}, err
	}
	actualMiles := route.DistanceMeters * milesPerMeter
	if math.IsNaN(actualMiles) || math.IsInf(actualMiles, 0) || actualMiles < 0 {
		return false, nil, 0, routing.Route{}, errors.New("OSRM returned invalid route distance")
	}
	deviationMiles := actualMiles - plannedDistanceMiles
	if deviationMiles <= rerouteThresholdMi {
		return false, nil, deviationMiles, route, nil
	}
	coordinates := make([]LocationCoordinate, 0, len(route.Coordinates))
	for _, coordinate := range route.Coordinates {
		coordinates = append(coordinates, LocationCoordinate{
			Latitude: coordinate.Latitude, Longitude: coordinate.Longitude,
		})
	}
	return true, coordinates, deviationMiles, route, nil
}

func validCoordinate(longitude, latitude float64) bool {
	return !math.IsNaN(longitude) && !math.IsInf(longitude, 0) &&
		!math.IsNaN(latitude) && !math.IsInf(latitude, 0) &&
		longitude >= -180 && longitude <= 180 && latitude >= -90 && latitude <= 90
}
