package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const osrmRequestTimeout = 450 * time.Millisecond

var ErrNoRoute = errors.New("OSRM returned no route")

type Coordinate struct {
	Latitude  float64
	Longitude float64
}

type Route struct {
	DistanceMeters  float64
	DurationSeconds float64
	Coordinates     []Coordinate
}

type OSRMClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewOSRMClient(baseURL string) (*OSRMClient, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("OSRM base URL must be an absolute HTTP(S) URL without query or fragment")
	}
	return &OSRMClient{
		baseURL: parsed.String(),
		httpClient: &http.Client{
			Timeout: osrmRequestTimeout,
		},
	}, nil
}

func (c *OSRMClient) Route(ctx context.Context, coordinates []Coordinate) (Route, error) {
	if len(coordinates) < 2 {
		return Route{}, errors.New("OSRM route requires at least two coordinates")
	}
	parts := make([]string, len(coordinates))
	for i, coordinate := range coordinates {
		if math.IsNaN(coordinate.Latitude) || math.IsInf(coordinate.Latitude, 0) ||
			math.IsNaN(coordinate.Longitude) || math.IsInf(coordinate.Longitude, 0) ||
			coordinate.Latitude < -90 || coordinate.Latitude > 90 ||
			coordinate.Longitude < -180 || coordinate.Longitude > 180 {
			return Route{}, errors.New("OSRM coordinate is out of range")
		}
		parts[i] = strconv.FormatFloat(coordinate.Longitude, 'f', -1, 64) + "," +
			strconv.FormatFloat(coordinate.Latitude, 'f', -1, 64)
	}
	requestCtx, cancel := context.WithTimeout(ctx, osrmRequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(
		requestCtx, http.MethodGet,
		c.baseURL+"/route/v1/driving/"+strings.Join(parts, ";")+"?overview=full&geometries=geojson",
		nil,
	)
	if err != nil {
		return Route{}, fmt.Errorf("create OSRM request: %w", err)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return Route{}, fmt.Errorf("OSRM request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Route{}, fmt.Errorf("OSRM returned HTTP status %d", response.StatusCode)
	}
	const maxResponseBytes = 1 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return Route{}, fmt.Errorf("read OSRM response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return Route{}, errors.New("OSRM response exceeds 1 MiB")
	}
	var payload struct {
		Code   string `json:"code"`
		Routes []struct {
			Distance float64 `json:"distance"`
			Duration float64 `json:"duration"`
			Geometry struct {
				Coordinates [][]float64 `json:"coordinates"`
			} `json:"geometry"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Route{}, fmt.Errorf("decode OSRM response: %w", err)
	}
	if payload.Code != "Ok" || len(payload.Routes) == 0 {
		return Route{}, ErrNoRoute
	}
	result := Route{
		DistanceMeters:  payload.Routes[0].Distance,
		DurationSeconds: payload.Routes[0].Duration,
	}
	if math.IsNaN(result.DistanceMeters) || math.IsInf(result.DistanceMeters, 0) ||
		math.IsNaN(result.DurationSeconds) || math.IsInf(result.DurationSeconds, 0) ||
		result.DistanceMeters < 0 || result.DurationSeconds < 0 {
		return Route{}, errors.New("OSRM returned invalid route measurements")
	}
	geometry := payload.Routes[0].Geometry.Coordinates
	if len(geometry) > 10_000 {
		return Route{}, errors.New("OSRM route geometry exceeds 10000 coordinates")
	}
	result.Coordinates = make([]Coordinate, 0, len(geometry))
	for _, point := range geometry {
		if len(point) != 2 || math.IsNaN(point[0]) || math.IsInf(point[0], 0) ||
			math.IsNaN(point[1]) || math.IsInf(point[1], 0) ||
			point[0] < -180 || point[0] > 180 || point[1] < -90 || point[1] > 90 {
			return Route{}, errors.New("OSRM returned invalid route geometry")
		}
		result.Coordinates = append(result.Coordinates, Coordinate{
			Longitude: point[0], Latitude: point[1],
		})
	}
	return result, nil
}
