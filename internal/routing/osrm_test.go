package routing

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewOSRMClientDefaultsToValidatedLocalPort(t *testing.T) {
	client := NewOSRMClient("")
	if client == nil {
		t.Fatal("NewOSRMClient() returned nil")
	}
	if got := client.baseURL; got != "http://localhost:5001" {
		t.Fatalf("NewOSRMClient() baseURL = %q; want %q", got, "http://localhost:5001")
	}
}

func TestOSRMClientRouteLegs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/route/v1/driving/") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("overview") != "false" {
			t.Fatalf("expected overview=false, got %q", r.URL.Query().Get("overview"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":"Ok","routes":[{"distance":1234.5,"duration":456.7}]}`)
	}))
	defer server.Close()

	client := NewOSRMClient(server.URL)
	legs, err := client.RouteLegs(context.Background(), []Coordinate{
		{Latitude: 38.91, Longitude: -76.99},
		{Latitude: 38.92, Longitude: -77.00},
	})
	if err != nil {
		t.Fatalf("RouteLegs() returned error: %v", err)
	}
	if len(legs) != 1 {
		t.Fatalf("RouteLegs() returned %d legs; want 1", len(legs))
	}
	if legs[0].Provider != "osrm" {
		t.Fatalf("RouteLegs() provider = %q; want %q", legs[0].Provider, "osrm")
	}
	if legs[0].DistanceMeters != 1234 {
		t.Fatalf("RouteLegs() distance = %d; want %d", legs[0].DistanceMeters, 1234)
	}
	if legs[0].DurationSeconds != 457 {
		t.Fatalf("RouteLegs() duration = %d; want %d", legs[0].DurationSeconds, 457)
	}
}
