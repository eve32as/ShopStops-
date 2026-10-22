package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxRouteResponseBytes = 1 << 20

type Coordinate struct {
	Latitude  float64
	Longitude float64
}

type Leg struct {
	DistanceMeters  int
	DurationSeconds int
}

type Router interface {
	Route(context.Context, []Coordinate) ([]Leg, error)
}

type OSRMClient struct {
	httpClient *http.Client
	baseURL    string
}

type osrmRouteResponse struct {
	Code   string `json:"code"`
	Routes []struct {
		Legs []struct {
			Distance float64 `json:"distance"`
			Duration float64 `json:"duration"`
		} `json:"legs"`
	} `json:"routes"`
}

func NewOSRMClient(baseURL string) (*OSRMClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		(parsed.Path != "" && parsed.Path != "/") ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("OSRM URL must be an HTTP(S) origin without credentials or query")
	}
	if parsed.Scheme == "http" {
		host := parsed.Hostname()
		ip := net.ParseIP(host)
		isPrivateIP := ip != nil &&
			(ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast())
		isPrivateName := host == "localhost" || strings.HasSuffix(host, ".internal") ||
			!strings.Contains(host, ".")
		if !isPrivateIP && !isPrivateName {
			return nil, errors.New("unencrypted OSRM URLs must use a private or loopback host")
		}
	}
	return &OSRMClient{
		httpClient: &http.Client{Timeout: 500 * time.Millisecond},
		baseURL:    strings.TrimRight(parsed.String(), "/"),
	}, nil
}

func (c *OSRMClient) Route(
	ctx context.Context, coordinates []Coordinate,
) ([]Leg, error) {
	if len(coordinates) < 2 || len(coordinates) > 100 {
		return nil, errors.New("route requires between 2 and 100 coordinates")
	}
	parts := make([]string, len(coordinates))
	for i, coordinate := range coordinates {
		if math.IsNaN(coordinate.Latitude) || math.IsInf(coordinate.Latitude, 0) ||
			coordinate.Latitude < -90 || coordinate.Latitude > 90 ||
			math.IsNaN(coordinate.Longitude) || math.IsInf(coordinate.Longitude, 0) ||
			coordinate.Longitude < -180 || coordinate.Longitude > 180 {
			return nil, errors.New("route coordinate is outside valid latitude/longitude bounds")
		}
		parts[i] = strconv.FormatFloat(coordinate.Longitude, 'f', 6, 64) + "," +
			strconv.FormatFloat(coordinate.Latitude, 'f', 6, 64)
	}

	endpoint := c.baseURL + "/route/v1/driving/" + strings.Join(parts, ";") +
		"?overview=false&steps=false"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request OSRM route: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("OSRM returned HTTP %d", response.StatusCode)
	}
	var result osrmRouteResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, maxRouteResponseBytes)).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode OSRM route: %w", err)
	}
	if result.Code != "Ok" || len(result.Routes) == 0 {
		return nil, errors.New("OSRM could not find a driving route")
	}
	if len(result.Routes[0].Legs) != len(coordinates)-1 {
		return nil, errors.New("OSRM returned an unexpected number of route legs")
	}
	legs := make([]Leg, len(result.Routes[0].Legs))
	for i, leg := range result.Routes[0].Legs {
		if math.IsNaN(leg.Distance) || math.IsInf(leg.Distance, 0) || leg.Distance < 0 ||
			math.IsNaN(leg.Duration) || math.IsInf(leg.Duration, 0) || leg.Duration < 0 ||
			leg.Distance > math.MaxInt32 || leg.Duration > math.MaxInt32 {
			return nil, errors.New("OSRM returned an invalid route leg")
		}
		legs[i] = Leg{
			DistanceMeters:  int(math.Round(leg.Distance)),
			DurationSeconds: int(math.Round(leg.Duration)),
		}
	}
	return legs, nil
}
