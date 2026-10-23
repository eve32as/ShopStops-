package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func notificationRequest(method, path, body, role string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "admin-user")
	request.Header.Set("X-ShopStops-Actor-Role", role)
	return request
}

func notificationHandler(repository *fakeRepository) http.Handler {
	return New(repository, strings.Repeat("t", 32),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestListDeliveryMessageLogsRequiresAdminAndValidFilters(t *testing.T) {
	repository := &fakeRepository{
		messageLogs: []DeliveryMessageLog{{ID: "message-1", Status: "failed"}},
	}
	handler := notificationHandler(repository)

	for _, test := range []struct {
		name string
		role string
		path string
		want int
	}{
		{name: "admin success", role: "admin", path: "/admin/notifications?status=failed&limit=12", want: http.StatusOK},
		{name: "wrong role", role: "driver", path: "/admin/notifications", want: http.StatusForbidden},
		{name: "invalid status", role: "admin", path: "/admin/notifications?status=unknown", want: http.StatusBadRequest},
		{name: "invalid limit", role: "admin", path: "/admin/notifications?limit=101", want: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, notificationRequest(
				http.MethodGet, test.path, "", test.role,
			))
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.want, recorder.Body)
			}
		})
	}
	if repository.messageStatus != "failed" || repository.messageLimit != 12 {
		t.Fatalf("repository filters = %q/%d, want failed/12",
			repository.messageStatus, repository.messageLimit)
	}
}

func TestRetryDeliveryMessageRequiresAdmin(t *testing.T) {
	repository := &fakeRepository{}
	handler := notificationHandler(repository)
	messageID := "123e4567-e89b-42d3-a456-426614174000"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, notificationRequest(
		http.MethodPost, "/admin/notifications/"+messageID+"/retry", "", "admin",
	))
	if recorder.Code != http.StatusOK || repository.retriedMessageID != messageID {
		t.Fatalf("status/id = %d/%q, want 200/%q", recorder.Code, repository.retriedMessageID, messageID)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, notificationRequest(
		http.MethodPost, "/admin/notifications/"+messageID+"/retry", "", "driver",
	))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("wrong-role status = %d, want 403", recorder.Code)
	}
}

func TestUpsertDriverPayoutProfileValidatesAndAuditsActor(t *testing.T) {
	repository := &fakeRepository{}
	handler := notificationHandler(repository)
	path := "/admin/driver-payout-profiles/driver-42"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, notificationRequest(
		http.MethodPut, path, `{"email":"courier@example.com"}`, "admin",
	))
	if recorder.Code != http.StatusOK ||
		repository.payoutProfileDriver != "driver-42" ||
		repository.payoutProfileEmail != "courier@example.com" ||
		repository.payoutProfileActor != "admin-user" {
		t.Fatalf("unexpected profile result: status=%d repository=%+v",
			recorder.Code, repository)
	}
	var response map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["status"] != "enabled" {
		t.Fatalf("profile response = %v", response)
	}

	for _, invalid := range []struct {
		path string
		body string
	}{
		{path: path, body: `{"email":"not-an-email"}`},
		{path: "/admin/driver-payout-profiles/driver%2Fother", body: `{"email":"courier@example.com"}`},
		{path: path, body: `{"email":"courier@example.com","extra":true}`},
	} {
		recorder = httptest.NewRecorder()
		handler.ServeHTTP(recorder, notificationRequest(
			http.MethodPut, invalid.path, invalid.body, "admin",
		))
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("invalid input %q returned %d, want 400", invalid.path, recorder.Code)
		}
	}
}
