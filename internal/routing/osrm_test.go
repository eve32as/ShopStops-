package routing

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOSRMClientRouteReturnsLegs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/route/v1/driving/-73.985000,40.758000;-73.980000,40.750000;-73.970000,40.740000" {
			t.Errorf("unexpected OSRM path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("overview") != "false" || r.URL.Query().Get("steps") != "false" {
			t.Errorf("unexpected OSRM query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"Ok","routes":[{"legs":[{"distance":804.7,"duration":65.4},{"distance":1200,"duration":90}]}]}`))
	}))
	defer server.Close()

	client, err := NewOSRMClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	legs, err := client.Route(context.Background(), []Coordinate{
		{Latitude: 40.758, Longitude: -73.985},
		{Latitude: 40.75, Longitude: -73.98},
		{Latitude: 40.74, Longitude: -73.97},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(legs) != 2 || legs[0] != (Leg{DistanceMeters: 805, DurationSeconds: 65}) ||
		legs[1] != (Leg{DistanceMeters: 1200, DurationSeconds: 90}) {
		t.Fatalf("unexpected route legs: %#v", legs)
	}
}

func TestOSRMClientRejectsInvalidCoordinate(t *testing.T) {
	requested := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requested = true
	}))
	defer server.Close()

	client, err := NewOSRMClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Route(context.Background(), []Coordinate{
		{Latitude: 0, Longitude: 0},
		{Latitude: 91, Longitude: 0},
	})
	if err == nil || requested {
		t.Fatalf("expected coordinate validation before request; err=%v requested=%v", err, requested)
	}
}

func TestOSRMClientTimesOutWhenRouterDoesNotRespond(t *testing.T) {
	requestStarted := make(chan struct{})
	var requestStartedOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		requestStartedOnce.Do(func() { close(requestStarted) })
		<-r.Context().Done()
	}))
	defer server.Close()

	client, err := NewOSRMClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if client.httpClient.Timeout != 500*time.Millisecond {
		t.Fatalf("unexpected HTTP client timeout: %s", client.httpClient.Timeout)
	}

	started := time.Now()
	result := make(chan error, 1)
	go func() {
		_, routeErr := client.Route(context.Background(), []Coordinate{
			{Latitude: 40, Longitude: -73},
			{Latitude: 41, Longitude: -72},
		})
		result <- routeErr
	}()
	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not reach the test router")
	}
	var routeErr error
	select {
	case routeErr = <-result:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not time out")
	}
	if routeErr == nil {
		t.Fatal("expected routing request to time out")
	}
	var networkError net.Error
	if !errors.As(routeErr, &networkError) || !networkError.Timeout() {
		t.Fatalf("expected timeout error, got %v", routeErr)
	}
	if time.Since(started) > 1500*time.Millisecond {
		t.Fatalf("client timeout took too long: %s", time.Since(started))
	}
}

func TestOSRMClientRejectsInvalidURL(t *testing.T) {
	if _, err := NewOSRMClient("https://router.example.com/"); err != nil {
		t.Fatalf("expected a URL with a trailing origin slash to be accepted: %v", err)
	}
	if _, err := NewOSRMClient("http://osrm-router:5000"); err != nil {
		t.Fatalf("expected the private Compose router hostname to be accepted: %v", err)
	}
	for _, baseURL := range []string{
		"http://router.example.com",
		"http://untrusted",
		"http://router.internal?token=example",
		"http://router.internal/path",
		"http://router.example.com/path",
		"https://router.example.com/path",
	} {
		if _, err := NewOSRMClient(baseURL); err == nil {
			t.Errorf("expected URL %q to be rejected", baseURL)
		}
	}
}

func TestOSRMClientRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{name: "http error", statusCode: http.StatusBadGateway, body: `upstream error`},
		{name: "malformed json", statusCode: http.StatusOK, body: `{`},
		{name: "no route", statusCode: http.StatusOK, body: `{"code":"NoRoute","routes":[]}`},
		{name: "wrong leg count", statusCode: http.StatusOK, body: `{"code":"Ok","routes":[{"legs":[]}]}`},
		{name: "negative distance", statusCode: http.StatusOK, body: `{"code":"Ok","routes":[{"legs":[{"distance":-1,"duration":1}]}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.statusCode)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			client, err := NewOSRMClient(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Route(context.Background(), []Coordinate{
				{Latitude: 40, Longitude: -73},
				{Latitude: 41, Longitude: -72},
			})
			if err == nil {
				t.Fatalf("expected response failure, got %v", err)
			}
		})
	}
}

func TestOSRMClientStress1000Concurrent(t *testing.T) {
	baseURL := os.Getenv("SHOPSTOPS_OSRM_STRESS_URL")
	if baseURL == "" {
		baseURL = os.Getenv("SHOPSTOPS_OSRM_URL")
	}
	if baseURL == "" {
		t.Skip("set SHOPSTOPS_OSRM_STRESS_URL or SHOPSTOPS_OSRM_URL to run the opt-in 1,000-request OSRM stress test")
	}
	client, err := NewOSRMClient(baseURL)
	if err != nil {
		t.Fatalf("configure OSRM stress client: %v", err)
	}

	coordinates := []Coordinate{
		{Latitude: 40.758, Longitude: -73.985},
		{Latitude: 40.740, Longitude: -73.970},
	}
	if rawCoordinates := os.Getenv("SHOPSTOPS_OSRM_STRESS_COORDINATES"); rawCoordinates != "" {
		parts := strings.Split(rawCoordinates, ";")
		if len(parts) < 2 || len(parts) > 100 {
			t.Fatal("SHOPSTOPS_OSRM_STRESS_COORDINATES must contain 2-100 lat,lon pairs separated by semicolons")
		}
		coordinates = make([]Coordinate, 0, len(parts))
		for _, part := range parts {
			values := strings.Split(part, ",")
			if len(values) != 2 {
				t.Fatalf("invalid stress coordinate %q; expected lat,lon", part)
			}
			latitude, latitudeErr := strconv.ParseFloat(strings.TrimSpace(values[0]), 64)
			longitude, longitudeErr := strconv.ParseFloat(strings.TrimSpace(values[1]), 64)
			if latitudeErr != nil || longitudeErr != nil {
				t.Fatalf("invalid stress coordinate %q; expected numeric lat,lon", part)
			}
			coordinates = append(coordinates, Coordinate{
				Latitude: latitude, Longitude: longitude,
			})
		}
	}

	const requestCount = 1000
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(requestCount)
	type result struct {
		duration time.Duration
		err      error
		legCount int
	}
	results := make(chan result, requestCount)
	for range requestCount {
		go func() {
			ready.Done()
			<-start
			requestStarted := time.Now()
			legs, routeErr := client.Route(context.Background(), coordinates)
			results <- result{
				duration: time.Since(requestStarted),
				err:      routeErr,
				legCount: len(legs),
			}
		}()
	}
	ready.Wait()
	releasedAt := time.Now()
	close(start)

	durations := make([]time.Duration, 0, requestCount)
	var failures []string
	for range requestCount {
		response := <-results
		durations = append(durations, response.duration)
		if response.err != nil {
			failures = append(failures, response.err.Error())
		} else if response.legCount != len(coordinates)-1 {
			failures = append(failures, "OSRM response had an unexpected leg count")
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	elapsed := time.Since(releasedAt)
	t.Logf("OSRM 1,000-request stress results: elapsed=%s p50=%s p95=%s p99=%s max=%s failures=%d",
		elapsed,
		durations[requestCount*50/100],
		durations[requestCount*95/100],
		durations[requestCount*99/100],
		durations[requestCount-1],
		len(failures),
	)
	for index, failure := range failures {
		if index == 5 {
			t.Logf("additional failures omitted (%d total)", len(failures))
			break
		}
		t.Errorf("OSRM stress request failed: %s", failure)
	}
	if len(failures) > 0 {
		t.FailNow()
	}
	for index, duration := range durations {
		if duration > 500*time.Millisecond {
			t.Errorf("OSRM stress request %d exceeded 500 ms: %s", index+1, duration)
			break
		}
	}
}
