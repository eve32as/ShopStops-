package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeRepository struct {
	pingErr       error
	nearby        []NearbyMerchant
	nearbyErr     error
	nearbyStoreID string
	nearbyRadius  int
	bundle        bundleResponse
	bundleErr     error
	bundleRequest bundleRequest
	bundleKey     string
}

func (f *fakeRepository) PingContext(context.Context) error { return f.pingErr }
func (f *fakeRepository) NearbyMerchants(
	_ context.Context, storeID string, radius int,
) ([]NearbyMerchant, error) {
	f.nearbyStoreID, f.nearbyRadius = storeID, radius
	return f.nearby, f.nearbyErr
}
func (f *fakeRepository) CreateBundle(
	_ context.Context, request bundleRequest, key string,
) (bundleResponse, error) {
	f.bundleRequest, f.bundleKey = request, key
	return f.bundle, f.bundleErr
}

func testServer() http.Handler {
	return New(nil, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestHealthIsPublic(t *testing.T) {
	recorder := httptest.NewRecorder()
	New(&fakeRepository{}, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
}

func TestProtectedEndpointsRequireBearerToken(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet,
		"/stores/00000000-0000-0000-0000-000000000001/nearby-merchants", nil)
	recorder := httptest.NewRecorder()
	testServer().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestNearbyPassesValidatedRadiusToRepository(t *testing.T) {
	repo := &fakeRepository{nearby: []NearbyMerchant{{MerchantID: "merchant"}}}
	request := httptest.NewRequest(http.MethodGet,
		"/stores/00000000-0000-0000-0000-000000000001/nearby-merchants?radiusMeters=250", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	recorder := httptest.NewRecorder()
	New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || repo.nearbyRadius != 250 ||
		repo.nearbyStoreID != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("status=%d radius=%d store=%s", recorder.Code, repo.nearbyRadius, repo.nearbyStoreID)
	}
	if !strings.Contains(recorder.Body.String(), `"merchantId":"merchant"`) {
		t.Fatalf("unexpected response: %s", recorder.Body.String())
	}
}

func TestNearbyReturnsNotFound(t *testing.T) {
	repo := &fakeRepository{nearbyErr: ErrNotFound}
	request := httptest.NewRequest(http.MethodGet,
		"/stores/00000000-0000-0000-0000-000000000001/nearby-merchants", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	recorder := httptest.NewRecorder()
	New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestNearbyRejectsInvalidPathBeforeDatabaseAccess(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/stores/not-a-uuid/nearby-merchants", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	recorder := httptest.NewRecorder()
	testServer().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestCreateBundleAppliesDefaultsAndUsesIdempotencyKey(t *testing.T) {
	repo := &fakeRepository{bundle: bundleResponse{
		ID: "bundle-id", Status: "active", Stops: []bundleStop{},
	}}
	request := httptest.NewRequest(http.MethodPost, "/dispatch/bundles", strings.NewReader(
		`{"seedOrderId":"00000000-0000-0000-0000-000000000001","capacityLiters":10}`))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("Idempotency-Key", "client-request-1")
	recorder := httptest.NewRecorder()
	New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if repo.bundleKey != "client-request-1" || *repo.bundleRequest.MaxOrders != 3 ||
		*repo.bundleRequest.PickupRadiusMeters != 2000 ||
		*repo.bundleRequest.DropoffRadiusMeters != 2000 {
		t.Fatalf("request defaults or idempotency key incorrect: %+v key=%q",
			repo.bundleRequest, repo.bundleKey)
	}
}

func TestCreateBundleAllowsExplicitZeroRadius(t *testing.T) {
	repo := &fakeRepository{bundle: bundleResponse{ID: "bundle-id", Status: "active"}}
	request := httptest.NewRequest(http.MethodPost, "/dispatch/bundles", strings.NewReader(
		`{"seedOrderId":"00000000-0000-0000-0000-000000000001","capacityLiters":10,"pickupRadiusMeters":0,"dropoffRadiusMeters":0}`))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("Idempotency-Key", "client-request-3")
	recorder := httptest.NewRecorder()
	New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK ||
		*repo.bundleRequest.PickupRadiusMeters != 0 ||
		*repo.bundleRequest.DropoffRadiusMeters != 0 {
		t.Fatalf("status=%d request=%+v", recorder.Code, repo.bundleRequest)
	}
}

func TestCreateBundleMapsIneligibleSeedToConflict(t *testing.T) {
	repo := &fakeRepository{bundleErr: ErrIneligible}
	request := httptest.NewRequest(http.MethodPost, "/dispatch/bundles", strings.NewReader(
		`{"seedOrderId":"00000000-0000-0000-0000-000000000001","capacityLiters":10}`))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("Idempotency-Key", "client-request-2")
	recorder := httptest.NewRecorder()
	New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusConflict)
	}
}

func TestNearbyRejectsRadiusAboveMVPMaximum(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet,
		"/stores/00000000-0000-0000-0000-000000000001/nearby-merchants?radiusMeters=501", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	recorder := httptest.NewRecorder()
	testServer().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestBundleRejectsUnknownJSONFields(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/dispatch/bundles",
		strings.NewReader(`{"seedOrderId":"00000000-0000-0000-0000-000000000001","capacityLiters":10,"unexpected":true}`))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("Idempotency-Key", "test-key")
	recorder := httptest.NewRecorder()
	testServer().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestUUIDValidation(t *testing.T) {
	if !isUUID("00000000-0000-0000-0000-000000000001") {
		t.Fatal("valid UUID rejected")
	}
	if isUUID("00000000-0000-0000-0000-00000000000z") {
		t.Fatal("invalid UUID accepted")
	}
}
