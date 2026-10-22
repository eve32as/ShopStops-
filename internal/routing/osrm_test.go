package routing

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

var testCoordinates = []Coordinate{
	{Latitude: 38.9, Longitude: -77.0},
	{Latitude: 38.91, Longitude: -76.99},
}

func TestOSRMRouteReturnsMeasurements(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/route/v1/driving/-77,38.9;-76.99,38.91" {
			t.Errorf("request path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("overview") != "false" {
			t.Errorf("overview = %q, want false", r.URL.Query().Get("overview"))
		}
		fmt.Fprint(w, `{"code":"Ok","routes":[{"distance":1234.5,"duration":321.25}]}`)
	}))
	defer server.Close()
	client, err := NewOSRMClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	route, err := client.Route(context.Background(), testCoordinates)
	if err != nil {
		t.Fatal(err)
	}
	if route.DistanceMeters != 1234.5 || route.DurationSeconds != 321.25 {
		t.Fatalf("route = %+v", route)
	}
}

func TestOSRMRouteTimesOutSlowResponseWithin500Milliseconds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	client, err := NewOSRMClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, err = client.Route(context.Background(), testCoordinates)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected request timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("request took %s, exceeds 500ms", elapsed)
	}
}

func TestOSRMRouteHonorsCallerCancellation(t *testing.T) {
	requestStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		<-r.Context().Done()
	}))
	defer server.Close()
	client, err := NewOSRMClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := client.Route(ctx, testCoordinates)
		result <- err
	}()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("OSRM request did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("request did not stop promptly after caller cancellation")
	}
}

func TestOSRMRouteRejectsInvalidResponse(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantErr    error
	}{
		{name: "HTTP error", statusCode: http.StatusBadGateway, body: `{"code":"Ok"}`},
		{name: "no route", statusCode: http.StatusOK, body: `{"code":"NoRoute","routes":[]}`, wantErr: ErrNoRoute},
		{name: "malformed JSON", statusCode: http.StatusOK, body: `{`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.statusCode)
				fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			client, err := NewOSRMClient(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Route(context.Background(), testCoordinates)
			if err == nil || (test.wantErr != nil && !errors.Is(err, test.wantErr)) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestNewOSRMClientRejectsInvalidURL(t *testing.T) {
	for _, baseURL := range []string{"", "/router", "ftp://router.example", "http://router.example?key=value"} {
		if _, err := NewOSRMClient(baseURL); err == nil {
			t.Errorf("NewOSRMClient(%q) succeeded", baseURL)
		}
	}
}

func TestOSRMRouteRejectsInvalidCoordinates(t *testing.T) {
	client, err := NewOSRMClient("http://router.example")
	if err != nil {
		t.Fatal(err)
	}
	for _, point := range []Coordinate{
		{Latitude: 91, Longitude: 0},
		{Latitude: 0, Longitude: 181},
		{Latitude: 0, Longitude: math.NaN()},
	} {
		if _, err := client.Route(context.Background(), []Coordinate{{}, point}); err == nil {
			t.Errorf("Route accepted invalid coordinate %+v", point)
		}
	}
	if _, err := client.Route(context.Background(), testCoordinates[:1]); err == nil {
		t.Error("Route accepted fewer than two coordinates")
	}
}
