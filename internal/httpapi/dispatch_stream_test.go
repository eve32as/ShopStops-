package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestSuccessfulDriverLocationWriteFansOutFreshnessEvent(t *testing.T) {
	repository := &fakeRepository{locationID: 42}
	server := &Server{repo: repository, dispatchStreams: newDispatchStreamHub()}
	updates, unsubscribe, ok := server.dispatchStreams.subscribe()
	if !ok {
		t.Fatal("could not subscribe to dispatch updates")
	}
	defer unsubscribe()
	observedAt := time.Date(2026, time.October, 21, 22, 0, 0, 0, time.UTC)
	request := httptest.NewRequest(http.MethodPost, "/driver/routes/00000000-0000-0000-0000-000000000001/location",
		strings.NewReader(`{"eventId":"location-1","observedAt":"2026-10-21T22:00:00Z","location":{"latitude":38.9,"longitude":-77.0}}`))
	request.SetPathValue("routeId", "00000000-0000-0000-0000-000000000001")
	request.Header.Set("X-ShopStops-Actor", "driver-1")
	recorder := httptest.NewRecorder()
	server.recordDriverLocation(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	select {
	case update := <-updates:
		if update.Type != "driver.location.updated" || update.DriverRef != "driver-1" ||
			update.RouteID != request.PathValue("routeId") ||
			!update.ObservedAt.Equal(observedAt) {
			t.Fatalf("unexpected dispatch update: %+v", update)
		}
	case <-time.After(time.Second):
		t.Fatal("successful location write did not fan out")
	}
}

func TestAdminDispatchStreamRequiresAdminRole(t *testing.T) {
	handler := New(&fakeRepository{}, strings.Repeat("t", 32),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodGet, "/admin/dispatch/stream", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "driver-1")
	request.Header.Set("X-ShopStops-Actor-Role", "driver")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestAdminDispatchStreamPublishesLocationFreshnessWithoutCoordinates(t *testing.T) {
	server := &Server{
		repo: &fakeRepository{}, token: strings.Repeat("t", 32),
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		dispatchStreams: newDispatchStreamHub(),
	}
	httpServer := httptest.NewServer(server.handler())
	defer httpServer.Close()
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+server.token)
	headers.Set("X-ShopStops-Actor", "admin-1")
	headers.Set("X-ShopStops-Actor-Role", "admin")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, response, err := websocket.Dial(ctx,
		"ws"+strings.TrimPrefix(httpServer.URL, "http")+"/admin/dispatch/stream",
		&websocket.DialOptions{
			HTTPHeader:  headers,
			Subprotocols: []string{dispatchStreamSubprotocol},
		},
	)
	if err != nil {
		if response != nil {
			t.Fatalf("websocket dial status=%d: %v", response.StatusCode, err)
		}
		t.Fatal(err)
	}
	defer conn.CloseNow()

	observedAt := time.Date(2026, time.October, 21, 22, 0, 0, 0, time.UTC)
	server.dispatchStreams.publish(DriverLocationUpdate{
		RouteID: "route-1", DriverRef: "driver-1", ObservedAt: observedAt,
	})
	_, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var update map[string]any
	if err := json.Unmarshal(payload, &update); err != nil {
		t.Fatal(err)
	}
	if update["type"] != "driver.location.updated" ||
		update["routeId"] != "route-1" ||
		update["driverRef"] != "driver-1" ||
		update["observedAt"] != observedAt.Format(time.RFC3339Nano) ||
		update["latitude"] != nil || update["longitude"] != nil {
		t.Fatalf("unexpected location update payload: %s", payload)
	}
}
