package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type routeOptimizationFakeRepository struct {
	*fakeRepository
	job       RouteOptimization
	metrics   RouteOptimizationMetrics
	requested string
	approved  string
}

func (f *routeOptimizationFakeRepository) RequestRouteOptimization(
	_ context.Context, routeID, key, actor string,
) (RouteOptimization, error) {
	f.requested = key + ":" + actor
	f.job.RouteID = routeID
	return f.job, nil
}

func (f *routeOptimizationFakeRepository) GetRouteOptimization(
	_ context.Context, routeID, _ string,
) (RouteOptimization, error) {
	if routeID != f.job.RouteID {
		return RouteOptimization{}, ErrNotFound
	}
	return f.job, nil
}

func (f *routeOptimizationFakeRepository) ApproveRouteOptimization(
	_ context.Context, routeID, jobID, actor string,
) (RouteOptimization, error) {
	if routeID != f.job.RouteID || jobID != f.job.ID {
		return RouteOptimization{}, ErrNotFound
	}
	f.approved = actor
	f.job.Status = "applied"
	f.job.ApprovedBy = actor
	f.job.AppliedBy = actor
	return f.job, nil
}

func (f *routeOptimizationFakeRepository) RouteOptimizationMetrics(
	context.Context,
) (RouteOptimizationMetrics, error) {
	return f.metrics, nil
}

func TestRouteOptimizationRequestRequiresDispatcherAndIdempotency(t *testing.T) {
	repository := &routeOptimizationFakeRepository{
		fakeRepository: &fakeRepository{},
		job: RouteOptimization{
			ID:     "00000000-0000-0000-0000-000000000022",
			Status: "queued", RequestedAt: time.Now(),
		},
	}
	handler := New(
		repository, strings.Repeat("t", 32),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	for _, test := range []struct {
		name        string
		role        string
		key         string
		wantStatus  int
		wantRequest string
	}{
		{name: "unauthorized role", role: "customer", key: "key-1", wantStatus: http.StatusForbidden},
		{name: "missing idempotency key", role: "dispatch", wantStatus: http.StatusBadRequest},
		{name: "dispatcher request", role: "dispatch", key: "key-1", wantStatus: http.StatusAccepted, wantRequest: "key-1:dispatcher-1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPost,
				"/dispatch/routes/00000000-0000-0000-0000-000000000010/optimizations",
				nil,
			)
			request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
			request.Header.Set("X-ShopStops-Actor", "dispatcher-1")
			request.Header.Set("X-ShopStops-Actor-Role", test.role)
			if test.key != "" {
				request.Header.Set("Idempotency-Key", test.key)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.wantStatus, response.Body)
			}
			if test.wantRequest != "" && repository.requested != test.wantRequest {
				t.Fatalf("request recorded = %q, want %q", repository.requested, test.wantRequest)
			}
		})
	}
}

func TestRouteOptimizationPreviewMustBeExplicitlyApproved(t *testing.T) {
	const routeID = "00000000-0000-0000-0000-000000000010"
	const jobID = "00000000-0000-0000-0000-000000000022"
	repository := &routeOptimizationFakeRepository{
		fakeRepository: &fakeRepository{},
		job: RouteOptimization{
			ID: jobID, RouteID: routeID, Status: "preview_ready",
			RequestedAt: time.Now(),
		},
	}
	handler := New(
		repository, strings.Repeat("t", 32),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	request := httptest.NewRequest(
		http.MethodGet,
		"/dispatch/routes/"+routeID+"/optimizations/"+jobID,
		nil,
	)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "dispatcher-1")
	request.Header.Set("X-ShopStops-Actor-Role", "dispatch")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"status":"preview_ready"`) {
		t.Fatalf("preview response = %d %s", response.Code, response.Body)
	}
	if repository.approved != "" {
		t.Fatal("reading a preview must not approve or apply it")
	}

	request = httptest.NewRequest(
		http.MethodPost,
		"/dispatch/routes/"+routeID+"/optimizations/"+jobID+"/approve",
		nil,
	)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "dispatcher-1")
	request.Header.Set("X-ShopStops-Actor-Role", "dispatch")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || repository.approved != "dispatcher-1" ||
		!strings.Contains(response.Body.String(), `"status":"applied"`) {
		t.Fatalf("approval response = %d %s", response.Code, response.Body)
	}
}
