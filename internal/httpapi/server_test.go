package httpapi

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeRepository struct {
	pingErr             error
	nearby              []NearbyMerchant
	nearbyErr           error
	nearbyStoreID       string
	nearbyRadius        int
	catalog             []CatalogItem
	catalogErr          error
	catalogStoreID      string
	merchantLocations  []MerchantStoreLocation
	merchantLocationsErr error
	merchantCatalog     []MerchantCatalogItem
	merchantCatalogErr  error
	merchantCatalogID   string
	merchantItem        MerchantCatalogItem
	merchantItemErr     error
	merchantRequest     merchantCatalogItemRequest
	merchantUpdate      merchantCatalogItemUpdateRequest
	merchantKey         string
	merchantFingerprint string
	order               customerOrderResponse
	orderErr            error
	orderReq            customerOrderRequest
	orderCustomer       string
	orderKey            string
	orderFingerprint    string
	bundle              bundleResponse
	bundleErr           error
	bundleRequest       bundleRequest
	bundleKey           string
	route               routeResponse
	routeErr            error
	routeID             string
	driverRef           string
	locationID          int64
	locationErr         error
	location            driverLocationRequest
	stopEvent           driverStopEventResponse
	stopEventErr        error
	stopID              string
	stopEventReq        driverStopEventRequest
	checkout            checkoutResponse
	checkoutErr         error
	checkoutReq         checkoutRequest
	customerRef         string
	checkoutKey         string
}

func (f *fakeRepository) PingContext(context.Context) error { return f.pingErr }
func (f *fakeRepository) NearbyMerchants(
	_ context.Context, storeID string, radius int,
) ([]NearbyMerchant, error) {
	f.nearbyStoreID, f.nearbyRadius = storeID, radius
	return f.nearby, f.nearbyErr
}
func (f *fakeRepository) CatalogItems(
	_ context.Context, storeID string,
) ([]CatalogItem, error) {
	f.catalogStoreID = storeID
	return f.catalog, f.catalogErr
}
func (f *fakeRepository) MerchantStoreLocations(
	_ context.Context, merchantID string,
) ([]MerchantStoreLocation, error) {
	f.merchantCatalogID = merchantID
	return f.merchantLocations, f.merchantLocationsErr
}
func (f *fakeRepository) MerchantCatalog(
	_ context.Context, merchantID string,
) ([]MerchantCatalogItem, error) {
	f.merchantCatalogID = merchantID
	return f.merchantCatalog, f.merchantCatalogErr
}
func (f *fakeRepository) CreateMerchantCatalogItem(
	_ context.Context, merchantID string, request merchantCatalogItemRequest,
	key, fingerprint string,
) (MerchantCatalogItem, error) {
	f.merchantCatalogID, f.merchantRequest = merchantID, request
	f.merchantKey, f.merchantFingerprint = key, fingerprint
	return f.merchantItem, f.merchantItemErr
}
func (f *fakeRepository) UpdateMerchantCatalogItem(
	_ context.Context, merchantID, itemID string, request merchantCatalogItemUpdateRequest,
) (MerchantCatalogItem, error) {
	f.merchantCatalogID, f.stopID, f.merchantUpdate = merchantID, itemID, request
	return f.merchantItem, f.merchantItemErr
}
func (f *fakeRepository) CreateCustomerOrder(
	_ context.Context, request customerOrderRequest, customerRef, idempotencyKey, fingerprint string,
) (customerOrderResponse, error) {
	f.orderReq, f.orderCustomer, f.orderKey, f.orderFingerprint =
		request, customerRef, idempotencyKey, fingerprint
	return f.order, f.orderErr
}
func (f *fakeRepository) CreateBundle(
	_ context.Context, request bundleRequest, key string,
) (bundleResponse, error) {
	f.bundleRequest, f.bundleKey = request, key
	return f.bundle, f.bundleErr
}
func (f *fakeRepository) CreateCheckout(
	_ context.Context, request checkoutRequest, customerRef, idempotencyKey string,
) (checkoutResponse, error) {
	f.checkoutReq, f.customerRef, f.checkoutKey = request, customerRef, idempotencyKey
	return f.checkout, f.checkoutErr
}
func (f *fakeRepository) DriverRoute(
	_ context.Context, routeID, driverRef string,
) (routeResponse, error) {
	f.routeID, f.driverRef = routeID, driverRef
	return f.route, f.routeErr
}
func (f *fakeRepository) RecordDriverLocation(
	_ context.Context, routeID, driverRef string, location driverLocationRequest,
) (int64, error) {
	f.routeID, f.driverRef, f.location = routeID, driverRef, location
	return f.locationID, f.locationErr
}
func (f *fakeRepository) RecordDriverStopEvent(
	_ context.Context, routeID, stopID, driverRef string, request driverStopEventRequest,
) (driverStopEventResponse, error) {
	f.routeID, f.stopID, f.driverRef, f.stopEventReq = routeID, stopID, driverRef, request
	return f.stopEvent, f.stopEventErr
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

func TestCatalogReturnsItemsForStore(t *testing.T) {
	repo := &fakeRepository{catalog: []CatalogItem{{ID: "item-1", Name: "Bread"}}}
	request := httptest.NewRequest(http.MethodGet,
		"/stores/00000000-0000-0000-0000-000000000001/catalog", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	recorder := httptest.NewRecorder()
	New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK ||
		repo.catalogStoreID != "00000000-0000-0000-0000-000000000001" ||
		!strings.Contains(recorder.Body.String(), `"name":"Bread"`) {
		t.Fatalf("status=%d store=%s body=%s", recorder.Code, repo.catalogStoreID, recorder.Body.String())
	}
}

func TestMerchantCatalogRequiresMerchantRole(t *testing.T) {
	repo := &fakeRepository{}
	request := httptest.NewRequest(http.MethodGet, "/merchant/catalog", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "00000000-0000-0000-0000-000000000001")
	request.Header.Set("X-ShopStops-Actor-Role", "customer")
	recorder := httptest.NewRecorder()
	New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || repo.merchantCatalogID != "" {
		t.Fatalf("status=%d merchant=%s", recorder.Code, repo.merchantCatalogID)
	}
}

func TestMerchantCanListOwnedStoreLocations(t *testing.T) {
	merchantID := "00000000-0000-0000-0000-000000000001"
	repo := &fakeRepository{merchantLocations: []MerchantStoreLocation{
		{ID: "store-1", Name: "Main Street"},
	}}
	request := httptest.NewRequest(http.MethodGet, "/merchant/store-locations", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", merchantID)
	request.Header.Set("X-ShopStops-Actor-Role", "merchant")
	recorder := httptest.NewRecorder()
	New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || repo.merchantCatalogID != merchantID ||
		!strings.Contains(recorder.Body.String(), `"name":"Main Street"`) {
		t.Fatalf("status=%d merchant=%s body=%s",
			recorder.Code, repo.merchantCatalogID, recorder.Body.String())
	}
}

func TestMerchantCanCreateOwnedCatalogItem(t *testing.T) {
	merchantID := "00000000-0000-0000-0000-000000000001"
	repo := &fakeRepository{merchantItem: MerchantCatalogItem{
		ID: "item-1", StoreLocationID: "store-1", Name: "Soup",
		UnitPriceCents: 650, VolumeLiters: "0.50", Available: true,
	}}
	request := httptest.NewRequest(http.MethodPost, "/merchant/catalog", strings.NewReader(
		`{"storeLocationId":"00000000-0000-0000-0000-000000000002","name":"Soup","description":"Daily special","unitPriceCents":650,"volumeLiters":0.5}`))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", merchantID)
	request.Header.Set("X-ShopStops-Actor-Role", "merchant")
	request.Header.Set("Idempotency-Key", "catalog-item-create-1")
	recorder := httptest.NewRecorder()
	New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || repo.merchantCatalogID != merchantID ||
		repo.merchantRequest.StoreLocationID != "00000000-0000-0000-0000-000000000002" ||
		repo.merchantRequest.UnitPriceCents != 650 || repo.merchantKey != "catalog-item-create-1" ||
		len(repo.merchantFingerprint) != 64 {
		t.Fatalf("status=%d merchant=%s request=%+v body=%s",
			recorder.Code, repo.merchantCatalogID, repo.merchantRequest, recorder.Body.String())
	}
}

func TestMerchantCanUpdateCatalogAvailability(t *testing.T) {
	repo := &fakeRepository{merchantItem: MerchantCatalogItem{
		ID:        "00000000-0000-0000-0000-000000000002",
		Available: false,
	}}
	request := httptest.NewRequest(http.MethodPatch,
		"/merchant/catalog/00000000-0000-0000-0000-000000000002",
		strings.NewReader(`{"name":"Soup","description":"Daily special","unitPriceCents":650,"volumeLiters":0.5,"available":false}`))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "00000000-0000-0000-0000-000000000001")
	request.Header.Set("X-ShopStops-Actor-Role", "merchant")
	recorder := httptest.NewRecorder()
	New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || repo.merchantUpdate.Available ||
		repo.stopID != "00000000-0000-0000-0000-000000000002" {
		t.Fatalf("status=%d update=%+v", recorder.Code, repo.merchantUpdate)
	}
}

func TestCreateCustomerOrderUsesAuthenticatedActorAndIdempotency(t *testing.T) {
	repo := &fakeRepository{order: customerOrderResponse{
		ID: "order-1", Status: "placed", ItemSubtotalCents: 250,
	}}
	start := time.Now().Add(time.Hour).Format(time.RFC3339)
	end := time.Now().Add(3 * time.Hour).Format(time.RFC3339)
	request := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(fmt.Sprintf(
		`{"storeLocationId":"00000000-0000-0000-0000-000000000001","dropoff":{"latitude":38.91,"longitude":-76.99},"deliveryWindowStart":%q,"deliveryWindowEnd":%q,"items":[{"catalogItemId":"00000000-0000-0000-0000-000000000002","quantity":2}]}`,
		start, end,
	)))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "customer-123")
	request.Header.Set("Idempotency-Key", "order-attempt-1")
	recorder := httptest.NewRecorder()
	New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || repo.orderCustomer != "customer-123" ||
		repo.orderKey != "order-attempt-1" || repo.orderReq.Items[0].Quantity != 2 ||
		len(repo.orderFingerprint) != 64 ||
		!strings.Contains(recorder.Body.String(), `"itemSubtotalCents":250`) {
		t.Fatalf("status=%d order=%+v key=%s body=%s", recorder.Code, repo.orderReq, repo.orderKey, recorder.Body.String())
	}
}

func TestCreateCustomerOrderRejectsDuplicateCatalogItems(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(
		`{"storeLocationId":"00000000-0000-0000-0000-000000000001","dropoff":{"latitude":38.91,"longitude":-76.99},"deliveryWindowStart":"2099-01-01T10:00:00Z","deliveryWindowEnd":"2099-01-01T12:00:00Z","items":[{"catalogItemId":"00000000-0000-0000-0000-000000000002","quantity":1},{"catalogItemId":"00000000-0000-0000-0000-000000000002","quantity":2}]}`))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "customer-123")
	request.Header.Set("Idempotency-Key", "order-attempt-duplicate")
	recorder := httptest.NewRecorder()
	repo := &fakeRepository{}
	New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || repo.orderKey != "" {
		t.Fatalf("status=%d repository key=%s", recorder.Code, repo.orderKey)
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

func TestDriverCanReadAssignedRoute(t *testing.T) {
	repo := &fakeRepository{route: routeResponse{
		ID:     "00000000-0000-0000-0000-000000000010",
		Status: "in_progress",
		Stops:  []routeStop{{Sequence: 1, Type: "pickup"}, {Sequence: 2, Type: "pickup"}, {Sequence: 3, Type: "dropoff"}},
	}}
	request := httptest.NewRequest(http.MethodGet,
		"/driver/routes/00000000-0000-0000-0000-000000000010", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "driver-123")
	recorder := httptest.NewRecorder()
	New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || repo.driverRef != "driver-123" ||
		!strings.Contains(recorder.Body.String(), `"type":"dropoff"`) {
		t.Fatalf("status=%d driver=%s body=%s", recorder.Code, repo.driverRef, recorder.Body.String())
	}
}

func TestDriverLocationValidatesCoordinatesAndRecordsEvent(t *testing.T) {
	repo := &fakeRepository{locationID: 42}
	request := httptest.NewRequest(http.MethodPost,
		"/driver/routes/00000000-0000-0000-0000-000000000010/location",
		strings.NewReader(`{"eventId":"event-1","observedAt":"2026-10-19T08:00:00Z","location":{"latitude":38.91,"longitude":-76.99}}`))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "driver-123")
	recorder := httptest.NewRecorder()
	New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted || repo.locationID != 42 ||
		repo.location.EventID != "event-1" || repo.location.Location.Latitude != 38.91 {
		t.Fatalf("status=%d location=%+v body=%s", recorder.Code, repo.location, recorder.Body.String())
	}
}

func TestDriverLocationRejectsOutOfRangeCoordinate(t *testing.T) {
	repo := &fakeRepository{}
	request := httptest.NewRequest(http.MethodPost,
		"/driver/routes/00000000-0000-0000-0000-000000000010/location",
		strings.NewReader(`{"eventId":"event-1","observedAt":"2026-10-19T08:00:00Z","location":{"latitude":91,"longitude":0}}`))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "driver-123")
	recorder := httptest.NewRecorder()
	New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestDriverLocationMapsTooFrequentSampleToConflict(t *testing.T) {
	repo := &fakeRepository{locationErr: ErrConflict}
	request := httptest.NewRequest(http.MethodPost,
		"/driver/routes/00000000-0000-0000-0000-000000000010/location",
		strings.NewReader(`{"eventId":"event-1","observedAt":"2026-10-19T08:00:00Z","location":{"latitude":38.91,"longitude":-76.99}}`))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "driver-123")
	recorder := httptest.NewRecorder()
	New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusConflict)
	}
}

func TestDriverCanAdvanceAssignedRouteStop(t *testing.T) {
	repo := &fakeRepository{stopEvent: driverStopEventResponse{
		StopID:     "00000000-0000-0000-0000-000000000011",
		StopStatus: "arrived", RouteStatus: "in_progress",
	}}
	request := httptest.NewRequest(http.MethodPost,
		"/driver/routes/00000000-0000-0000-0000-000000000010/stops/00000000-0000-0000-0000-000000000011/events",
		strings.NewReader(`{"eventId":"stop-event-1","type":"arrive","observedAt":"2026-10-19T08:00:00Z"}`))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "driver-123")
	recorder := httptest.NewRecorder()
	New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || repo.routeID != "00000000-0000-0000-0000-000000000010" ||
		repo.stopID != "00000000-0000-0000-0000-000000000011" ||
		repo.driverRef != "driver-123" || repo.stopEventReq.EventID != "stop-event-1" ||
		repo.stopEventReq.Type != "arrive" ||
		!strings.Contains(recorder.Body.String(), `"stopStatus":"arrived"`) {
		t.Fatalf("status=%d stopEvent=%+v body=%s", recorder.Code, repo.stopEventReq, recorder.Body.String())
	}
}

func TestDriverStopEventRejectsInvalidType(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost,
		"/driver/routes/00000000-0000-0000-0000-000000000010/stops/00000000-0000-0000-0000-000000000011/events",
		strings.NewReader(`{"eventId":"stop-event-1","type":"skip","observedAt":"2026-10-19T08:00:00Z"}`))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "driver-123")
	recorder := httptest.NewRecorder()
	testServer().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestCreateCheckoutUsesAuthenticatedCustomerAndIdempotency(t *testing.T) {
	repo := &fakeRepository{checkout: checkoutResponse{
		ID: "checkout-id", Status: "quoted", Currency: "usd",
		InvoiceLines: []checkoutInvoiceLine{{Type: "dc_surcharge", AmountCents: 20}},
		TotalCents:   20,
	}}
	request := httptest.NewRequest(http.MethodPost, "/checkouts", strings.NewReader(
		`{"anchorStoreLocationId":"00000000-0000-0000-0000-000000000001","orderIds":["00000000-0000-0000-0000-000000000002"],"dropoff":{"latitude":38.91,"longitude":-76.99},"deliveryWindowStart":"2026-10-19T09:00:00Z","deliveryWindowEnd":"2026-10-19T11:00:00Z"}`))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "customer-123")
	request.Header.Set("Idempotency-Key", "checkout-request-1")
	recorder := httptest.NewRecorder()
	New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || repo.customerRef != "customer-123" ||
		repo.checkoutKey != "checkout-request-1" ||
		repo.checkoutReq.AnchorStoreLocationID != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("status=%d customer=%s key=%s body=%s", recorder.Code, repo.customerRef, repo.checkoutKey, recorder.Body.String())
	}
}

func TestCreateCheckoutRejectsDuplicateOrderIDs(t *testing.T) {
	repo := &fakeRepository{}
	request := httptest.NewRequest(http.MethodPost, "/checkouts", strings.NewReader(
		`{"anchorStoreLocationId":"00000000-0000-0000-0000-000000000001","orderIds":["00000000-0000-0000-0000-000000000002","00000000-0000-0000-0000-000000000002"],"dropoff":{"latitude":38.91,"longitude":-76.99},"deliveryWindowStart":"2026-10-19T09:00:00Z","deliveryWindowEnd":"2026-10-19T11:00:00Z"}`))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "customer-123")
	request.Header.Set("Idempotency-Key", "checkout-request-2")
	recorder := httptest.NewRecorder()
	New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
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
