package routing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Coordinate is a WGS84 point with longitude, latitude order for OSRM calls.
type Coordinate struct {
	Longitude float64
	Latitude  float64
}

// Leg is a single road-network segment between consecutive stops.
type Leg struct {
	DistanceMeters int
	DurationSeconds int
	Provider        string
}

// OSRMClient queries a local OSRM routing backend.
type OSRMClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewOSRMClient(baseURL string) *OSRMClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "http://localhost:5001"
	}
	return &OSRMClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *OSRMClient) RouteLegs(ctx context.Context, points []Coordinate) ([]Leg, error) {
	if c == nil {
		return nil, fmt.Errorf("osrm client is nil")
	}
	if len(points) < 2 {
		return nil, fmt.Errorf("at least two coordinates are required")
	}

	legs := make([]Leg, 0, len(points)-1)
	for i := 0; i < len(points)-1; i++ {
		leg, err := c.routeLeg(ctx, points[i], points[i+1])
		if err != nil {
			return nil, fmt.Errorf("route from point %d to %d: %w", i, i+1, err)
		}
		legs = append(legs, leg)
	}
	return legs, nil
}

func (c *OSRMClient) routeLeg(ctx context.Context, from, to Coordinate) (Leg, error) {
	urlPath := fmt.Sprintf(
		"%s/route/v1/driving/%s,%s;%s,%s?overview=false&alternatives=false",
		c.baseURL,
		strconv.FormatFloat(from.Longitude, 'f', -1, 64),
		strconv.FormatFloat(from.Latitude, 'f', -1, 64),
		strconv.FormatFloat(to.Longitude, 'f', -1, 64),
		strconv.FormatFloat(to.Latitude, 'f', -1, 64),
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, urlPath, nil)
	if err != nil {
		return Leg{}, err
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return Leg{}, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return Leg{}, fmt.Errorf("osrm request failed: %s", response.Status)
	}

	var payload osrmRouteResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return Leg{}, err
	}
	if payload.Code != "Ok" {
		return Leg{}, fmt.Errorf("osrm route response code: %s", payload.Code)
	}
	if len(payload.Routes) == 0 {
		return Leg{}, fmt.Errorf("osrm returned no route")
	}

	route := payload.Routes[0]
	return Leg{
		DistanceMeters: int(route.Distance),
		DurationSeconds: int(route.Duration + 0.5),
		Provider:       "osrm",
	}, nil
}

type osrmRouteResponse struct {
	Code   string       `json:"code"`
	Routes []osrmRoute `json:"routes"`
}

type osrmRoute struct {
	Distance float64 `json:"distance"`
	Duration float64 `json:"duration"`
}
