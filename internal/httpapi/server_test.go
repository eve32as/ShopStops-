package httpapi

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eve32as/ShopStops-/internal/dispatch"
	"github.com/eve32as/ShopStops-/internal/routing"
)

type fakeRepository struct {
	pingErr               error
	nearby                []NearbyMerchant
	nearbyErr             error
	nearbyStoreID         string
	nearbyRadius          int
	restaurants           []NearbyMerchant
	restaurantsErr        error
	restaurantsLatitude   float64
	restaurantsLongitude  float64
	restaurantsRadius     int
	catalog               []CatalogItem
	catalogErr            error
	catalogStoreID        string
	merchantLocations     []MerchantStoreLocation
	merchantLocationsErr  error
	merchantCatalog       []MerchantCatalogItem
	merchantCatalogErr    error
	merchantCatalogID     string
	merchantItem          MerchantCatalogItem
	merchantItemErr       error
	merchantRequest       merchantCatalogItemRequest
	merchantUpdate        merchantCatalogItemUpdateRequest
	merchantKey           string
	merchantFingerprint   string
	order                 customerOrderResponse
	orderErr              error
	orderReq              customerOrderRequest
	orderCustomer         string
	orderKey              string
	orderFingerprint      string
	bundle                bundleResponse
	bundleErr             error
	bundleRequest         bundleRequest
	bundleKey             string
	route                 routeResponse
	routeErr              error
	routeID               string
	activeRouteID         string
	activeRouteErr        error
	activeRouteDriver     string
	driverRef             string
	locationID            int64
	locationErr           error
	location              driverLocationRequest
	stopEvent             driverStopEventResponse
	stopEventErr          error
	stopID                string
	stopEventReq          driverStopEventRequest
	fraudAlerts           []DriverFraudAlert
	fraudAlertsErr        error
	fraudStatus           string
	fraudLimit            int
	resolvedAlertID       string
	resolvedAlertActor    string
	resolvedAlertStatus   string
	resolveFraudAlertErr  error
	messageLogs           []DeliveryMessageLog
	messageLogsErr        error
	messageStatus         string
	messageLimit          int
	retriedMessageID      string
	retriedMessageActor   string
	retryMessageErr       error
	payoutProfileDriver   string
	payoutProfileEmail    string
	payoutProfileActor    string
	payoutProfileErr      error
	checkout              checkoutResponse
	checkoutErr           error
	checkoutReq           checkoutRequest
	customerRef           string
	checkoutKey           string
	routeCoordinates      []routing.Coordinate
	routeCoordinatesErr   error
	createdRouteID        string
	createdRouteErr       error
	routeLegs             []routing.Route
	existingRouteID       string
	existingRouteFound    bool
	existingRouteErr      error
	reroutePlan           driverReroutePlan
	reroutePlanErr        error
	savedRerouteKey       string
	savedRerouteDeviation float64
	savedRerouteRoute     routing.Route
	savedRerouteGeometry  []dispatch.LocationCoordinate
	savedRerouteErr       error
	shopperTracking       ShopperTrackingResponse
	shopperTrackingErr    error
}

type fakeOSRMRouter struct {
	route routing.Route
	err   error
	calls *atomic.Int32
}

func (f fakeOSRMRouter) Route(context.Context, []routing.Coordinate) (routing.Route, error) {
	if f.calls != nil {
		f.calls.Add(1)
	}
	return f.route, f.err
}

func (f *fakeRepository) PingContext(context.Context) error { return f.pingErr }
func (f *fakeRepository) NearbyMerchants(
	_ context.Context, storeID string, radius int,
) ([]NearbyMerchant, error) {
	f.nearbyStoreID, f.nearbyRadius = storeID, radius
	return f.nearby, f.nearbyErr
}
func (f *fakeRepository) NearbyRestaurants(
	_ context.Context, latitude, longitude float64, radius int,
) ([]NearbyMerchant, error) {
	f.restaurantsLatitude, f.restaurantsLongitude, f.restaurantsRadius =
		latitude, longitude, radius
	return f.restaurants, f.restaurantsErr
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
func (f *fakeRepository) FindUnifiedRoute(
	_ context.Context, _, _ string,
) (string, bool, error) {
	return f.existingRouteID, f.existingRouteFound, f.existingRouteErr
}
func (f *fakeRepository) UnifiedRouteCoordinates(
	_ context.Context, _ string,
) ([]routing.Coordinate, error) {
	return f.routeCoordinates, f.routeCoordinatesErr
}
func (f *fakeRepository) CreateUnifiedRoute(
	_ context.Context, _, _ string, legs []routing.Route,
) (string, error) {
	f.routeLegs = legs
	return f.createdRouteID, f.createdRouteErr
}
func (f *fakeRepository) DriverReroutePlan(
	_ context.Context, _, _ string,
) (driverReroutePlan, error) {
	return f.reroutePlan, f.reroutePlanErr
}
func (f *fakeRepository) SaveDriverReroute(
	_ context.Context, _, _ string, _ driverReroutePlan, key string,
	deviation float64, route routing.Route, coordinates []dispatch.LocationCoordinate,
) error {
	f.savedRerouteKey = key
	f.savedRerouteDeviation = deviation
	f.savedRerouteRoute = route
	f.savedRerouteGeometry = coordinates
	return f.savedRerouteErr
}
func (f *fakeRepository) ShopperOrderTracking(
	_ context.Context, _, _ string,
) (ShopperTrackingResponse, error) {
	return f.shopperTracking, f.shopperTrackingErr
}
func (f *fakeRepository) DriverRoute(
	_ context.Context, routeID, driverRef string,
) (routeResponse, error) {
	f.routeID, f.driverRef = routeID, driverRef
	return f.route, f.routeErr
}
func (f *fakeRepository) DriverActiveRoute(
	_ context.Context, driverRef string,
) (string, error) {
	f.activeRouteDriver = driverRef
	return f.activeRouteID, f.activeRouteErr
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
func (f *fakeRepository) ListDriverFraudAlerts(
	_ context.Context, status string, limit int,
) ([]DriverFraudAlert, error) {
	f.fraudStatus, f.fraudLimit = status, limit
	return f.fraudAlerts, f.fraudAlertsErr
}
func (f *fakeRepository) ResolveDriverFraudAlert(
	_ context.Context, alertID, actor, status string,
) error {
	f.resolvedAlertID, f.resolvedAlertActor, f.resolvedAlertStatus =
		alertID, actor, status
	return f.resolveFraudAlertErr
}
func (f *fakeRepository) ListDeliveryMessageLogs(
	_ context.Context, status string, limit int,
) ([]DeliveryMessageLog, error) {
	f.messageStatus, f.messageLimit = status, limit
	return f.messageLogs, f.messageLogsErr
}
func (f *fakeRepository) RetryDeliveryMessage(
	_ context.Context, messageID, actor string,
) error {
	f.retriedMessageID, f.retriedMessageActor = messageID, actor
	return f.retryMessageErr
}
func (f *fakeRepository) UpsertDriverPayoutProfile(
	_ context.Context, driverRef, email, actor string,
) error {
	f.payoutProfileDriver, f.payoutProfileEmail, f.payoutProfileActor =
		driverRef, email, actor
	return f.payoutProfileErr
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

func TestSensitiveEndpointsRequireMatchingActorRole(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		role   string
	}{
		{name: "customer order", method: http.MethodPost, path: "/orders", role: "driver"},
		{name: "customer checkout", method: http.MethodPost, path: "/checkouts", role: "driver"},
		{name: "checkout payment", method: http.MethodPost, path: "/checkouts/00000000-0000-0000-0000-000000000001/payment", role: "driver"},
		{name: "driver route", method: http.MethodGet, path: "/driver/routes/00000000-0000-0000-0000-000000000010", role: "customer"},
		{name: "driver location", method: http.MethodPost, path: "/driver/routes/00000000-0000-0000-0000-000000000010/location", role: "customer"},
		{name: "driver stop event", method: http.MethodPost, path: "/driver/routes/00000000-0000-0000-0000-000000000010/stops/00000000-0000-0000-0000-000000000011/events", role: "customer"},
		{name: "driver reroute", method: http.MethodPost, path: "/driver/routes/00000000-0000-0000-0000-000000000010/reroute", role: "customer"},
		{name: "shopper track", method: http.MethodGet, path: "/shopper/orders/00000000-0000-0000-0000-000000000001/track", role: "driver"},
		{name: "dispatch bundle", method: http.MethodPost, path: "/dispatch/bundles", role: "customer"},
		{name: "route creation", method: http.MethodPost, path: "/checkouts/00000000-0000-0000-0000-000000000001/route", role: "customer"},
		{name: "missing customer role", method: http.MethodPost, path: "/orders", role: ""},
	}

	func TestNearbyRestaurantsUsesCustomerLocationAndBounds(t *testing.T) {
		repository := &fakeRepository{
			restaurants: []NearbyMerchant{{MerchantID: "merchant-1", StoreLocation: "store-1"}},
		}
		handler := New(repository, strings.Repeat("t", 32),
			slog.New(slog.NewTextHandler(io.Discard, nil)))
		request := httptest.NewRequest(http.MethodGet,
			"/stores/nearby-restaurants?latitude=38.9&longitude=-77.0&radiusMeters=2500", nil)
		request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
		request.Header.Set("X-ShopStops-Actor", "customer-1")
		request.Header.Set("X-ShopStops-Actor-Role", "customer")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || repository.restaurantsLatitude != 38.9 ||
			repository.restaurantsLongitude != -77 || repository.restaurantsRadius != 2500 {
			t.Fatalf("status/location/radius = %d/%.2f/%.2f/%d",
				recorder.Code, repository.restaurantsLatitude,
				repository.restaurantsLongitude, repository.restaurantsRadius)
		}
	}

	func TestNearbyRestaurantsRejectsInvalidLocationAndRole(t *testing.T) {
		handler := New(&fakeRepository{}, strings.Repeat("t", 32),
			slog.New(slog.NewTextHandler(io.Discard, nil)))
		for _, test := range []struct {
			name string
			path string
			role string
			want int
		}{
			{name: "invalid latitude", path: "/stores/nearby-restaurants?latitude=91&longitude=0", role: "customer", want: http.StatusBadRequest},
			{name: "invalid radius", path: "/stores/nearby-restaurants?latitude=0&longitude=0&radiusMeters=30000", role: "customer", want: http.StatusBadRequest},
			{name: "wrong role", path: "/stores/nearby-restaurants?latitude=0&longitude=0", role: "driver", want: http.StatusForbidden},
		} {
			t.Run(test.name, func(t *testing.T) {
				request := httptest.NewRequest(http.MethodGet, test.path, nil)
				request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
				request.Header.Set("X-ShopStops-Actor", "actor-1")
				request.Header.Set("X-ShopStops-Actor-Role", test.role)
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, request)
				if recorder.Code != test.want {
					t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.want, recorder.Body)
				}
			})
		}
	}

	func TestGetActiveDriverRouteRejectsNonDriverRole(t *testing.T) {
		handler := New(&fakeRepository{}, strings.Repeat("t", 32),
			slog.New(slog.NewTextHandler(io.Discard, nil)))
		request := httptest.NewRequest(http.MethodGet, "/driver/routes/active", nil)
		request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
		request.Header.Set("X-ShopStops-Actor", "customer-1")
		request.Header.Set("X-ShopStops-Actor-Role", "customer")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
		}
	}

	func TestGetActiveDriverRouteUsesAuthenticatedDriver(t *testing.T) {
		repository := &fakeRepository{
			activeRouteID: "00000000-0000-0000-0000-000000000010",
			route: routeResponse{
				ID: "00000000-0000-0000-0000-000000000010", Status: "in_progress",
			},
		}
		handler := New(repository, strings.Repeat("t", 32),
			slog.New(slog.NewTextHandler(io.Discard, nil)))
		request := httptest.NewRequest(http.MethodGet, "/driver/routes/active", nil)
		request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
		request.Header.Set("X-ShopStops-Actor", "driver-42")
		request.Header.Set("X-ShopStops-Actor-Role", "driver")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || repository.activeRouteDriver != "driver-42" ||
			repository.routeID != repository.activeRouteID {
			t.Fatalf("status/driver/route = %d/%q/%q",
				recorder.Code, repository.activeRouteDriver, repository.routeID)
		}
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
			request.Header.Set("X-ShopStops-Actor", "actor-123")
			if test.role != "" {
				request.Header.Set("X-ShopStops-Actor-Role", test.role)
			}
			recorder := httptest.NewRecorder()
			testServer().ServeHTTP(recorder, request)
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
			}
		})
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
	request.Header.Set("X-ShopStops-Actor-Role", "customer")
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
	request.Header.Set("X-ShopStops-Actor-Role", "customer")
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
	request.Header.Set("X-ShopStops-Actor", "dispatch-123")
	request.Header.Set("X-ShopStops-Actor-Role", "dispatch")
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

func TestCreateBundleAllowsAdminRole(t *testing.T) {
	repo := &fakeRepository{bundle: bundleResponse{ID: "bundle-id", Status: "active"}}
	request := httptest.NewRequest(http.MethodPost, "/dispatch/bundles", strings.NewReader(
		`{"seedOrderId":"00000000-0000-0000-0000-000000000001","capacityLiters":10}`))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "admin-123")
	request.Header.Set("X-ShopStops-Actor-Role", "admin")
	request.Header.Set("Idempotency-Key", "admin-dispatch-request")
	recorder := httptest.NewRecorder()
	New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || repo.bundleKey != "admin-dispatch-request" {
		t.Fatalf("status=%d key=%q body=%s", recorder.Code, repo.bundleKey, recorder.Body.String())
	}
}

func TestCreateBundleAllowsExplicitZeroRadius(t *testing.T) {
	repo := &fakeRepository{bundle: bundleResponse{ID: "bundle-id", Status: "active"}}
	request := httptest.NewRequest(http.MethodPost, "/dispatch/bundles", strings.NewReader(
		`{"seedOrderId":"00000000-0000-0000-0000-000000000001","capacityLiters":10,"pickupRadiusMeters":0,"dropoffRadiusMeters":0}`))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "dispatch-123")
	request.Header.Set("X-ShopStops-Actor-Role", "dispatch")
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
	request.Header.Set("X-ShopStops-Actor", "dispatch-123")
	request.Header.Set("X-ShopStops-Actor-Role", "dispatch")
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
	request.Header.Set("X-ShopStops-Actor-Role", "driver")
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
	request.Header.Set("X-ShopStops-Actor-Role", "driver")
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
	request.Header.Set("X-ShopStops-Actor-Role", "driver")
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
	request.Header.Set("X-ShopStops-Actor-Role", "driver")
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
	request.Header.Set("X-ShopStops-Actor-Role", "driver")
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
	request.Header.Set("X-ShopStops-Actor-Role", "driver")
	recorder := httptest.NewRecorder()
	testServer().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestAdminCanListDriverFraudAlerts(t *testing.T) {
	repo := &fakeRepository{fraudAlerts: []DriverFraudAlert{{
		ID:        "00000000-0000-0000-0000-000000000012",
		AlertType: "impossible_speed", Status: "open",
	}}}
	request := httptest.NewRequest(http.MethodGet,
		"/admin/driver-fraud-alerts?status=open&limit=20", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "admin-123")
	request.Header.Set("X-ShopStops-Actor-Role", "admin")
	recorder := httptest.NewRecorder()
	New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || repo.fraudStatus != "open" ||
		repo.fraudLimit != 20 || !strings.Contains(recorder.Body.String(), "impossible_speed") {
		t.Fatalf("status=%d filters=%s/%d body=%s",
			recorder.Code, repo.fraudStatus, repo.fraudLimit, recorder.Body.String())
	}
}

func TestDriverFraudAlertsRequireAdminRole(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/admin/driver-fraud-alerts", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "driver-123")
	request.Header.Set("X-ShopStops-Actor-Role", "driver")
	recorder := httptest.NewRecorder()
	New(&fakeRepository{}, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestDriverFraudAlertLimitIsBounded(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/admin/driver-fraud-alerts?limit=101", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "admin-123")
	request.Header.Set("X-ShopStops-Actor-Role", "admin")
	recorder := httptest.NewRecorder()
	New(&fakeRepository{}, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestAdminCanResolveDriverFraudAlert(t *testing.T) {
	const alertID = "00000000-0000-0000-0000-000000000012"
	repo := &fakeRepository{}
	request := httptest.NewRequest(http.MethodPatch, "/admin/driver-fraud-alerts/"+alertID,
		strings.NewReader(`{"status":"dismissed"}`))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "admin-123")
	request.Header.Set("X-ShopStops-Actor-Role", "admin")
	recorder := httptest.NewRecorder()
	New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || repo.resolvedAlertID != alertID ||
		repo.resolvedAlertActor != "admin-123" || repo.resolvedAlertStatus != "dismissed" {
		t.Fatalf("status=%d resolution=%s/%s/%s body=%s",
			recorder.Code, repo.resolvedAlertID, repo.resolvedAlertActor,
			repo.resolvedAlertStatus, recorder.Body.String())
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
	request.Header.Set("X-ShopStops-Actor-Role", "customer")
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
	request.Header.Set("X-ShopStops-Actor-Role", "customer")
	request.Header.Set("Idempotency-Key", "checkout-request-2")
	recorder := httptest.NewRecorder()
	New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestCreateUnifiedRouteRoutesAndPersistsBothLegs(t *testing.T) {
	repo := &fakeRepository{
		routeCoordinates: []routing.Coordinate{
			{Latitude: 38.9, Longitude: -77},
			{Latitude: 38.91, Longitude: -76.99},
			{Latitude: 38.92, Longitude: -76.98},
		},
		createdRouteID: "route-123",
	}
	calls := &atomic.Int32{}
	geometry := []routing.Coordinate{
		{Latitude: 38.9, Longitude: -77},
		{Latitude: 38.91, Longitude: -76.99},
	}
	handler := NewWithRouting(
		repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil)),
		fakeOSRMRouter{route: routing.Route{
			DistanceMeters: 1234.5, DurationSeconds: 321.25, Coordinates: geometry,
		}, calls: calls},
	)
	request := httptest.NewRequest(http.MethodPost,
		"/checkouts/00000000-0000-0000-0000-000000000001/route", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "dispatch-123")
	request.Header.Set("X-ShopStops-Actor-Role", "dispatch")
	request.Header.Set("Idempotency-Key", "route-request-1")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated || calls.Load() != 2 ||
		repo.createdRouteID != "route-123" || len(repo.routeLegs) != 2 ||
		repo.routeLegs[0].DistanceMeters != 1234.5 ||
		repo.routeLegs[1].DurationSeconds != 321.25 ||
		!strings.Contains(recorder.Body.String(), `"routingProvider":"osrm"`) {
		t.Fatalf("status=%d calls=%d legs=%+v body=%s",
			recorder.Code, calls.Load(), repo.routeLegs, recorder.Body.String())
	}
}

func TestCreateUnifiedRouteRetrySkipsOSRM(t *testing.T) {
	repo := &fakeRepository{existingRouteID: "route-existing", existingRouteFound: true}
	handler := NewWithRouting(
		repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
	)
	request := httptest.NewRequest(http.MethodPost,
		"/checkouts/00000000-0000-0000-0000-000000000001/route", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "dispatch-123")
	request.Header.Set("X-ShopStops-Actor-Role", "dispatch")
	request.Header.Set("Idempotency-Key", "route-request-1")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK ||
		!strings.Contains(recorder.Body.String(), `"routeId":"route-existing"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCreateUnifiedRouteMapsRouterTimeoutToGatewayTimeout(t *testing.T) {
	repo := &fakeRepository{
		routeCoordinates: []routing.Coordinate{{}, {Latitude: 1}, {Latitude: 2}},
	}
	handler := NewWithRouting(
		repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil)),
		fakeOSRMRouter{err: context.DeadlineExceeded},
	)
	request := httptest.NewRequest(http.MethodPost,
		"/checkouts/00000000-0000-0000-0000-000000000001/route", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "dispatch-123")
	request.Header.Set("X-ShopStops-Actor-Role", "dispatch")
	request.Header.Set("Idempotency-Key", "route-timeout")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusGatewayTimeout || len(repo.routeLegs) != 0 {
		t.Fatalf("status=%d legs=%+v body=%s", recorder.Code, repo.routeLegs, recorder.Body.String())
	}
}

func TestDriverReroutePersistsCourseCorrection(t *testing.T) {
	routeID := "00000000-0000-0000-0000-000000000010"
	repo := &fakeRepository{reroutePlan: driverReroutePlan{
		Current:              geoPoint{Latitude: 38.9, Longitude: -77},
		Target:               geoPoint{Latitude: 38.91, Longitude: -76.99},
		PlannedDistanceMiles: 1,
		ToSequence:           2,
		SampleID:             42,
	}}
	geometry := []routing.Coordinate{
		{Latitude: 38.9, Longitude: -77},
		{Latitude: 38.91, Longitude: -76.99},
	}
	handler := NewWithRouting(
		repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil)),
		fakeOSRMRouter{route: routing.Route{
			DistanceMeters: 2000, DurationSeconds: 300, Coordinates: geometry,
		}},
	)
	request := httptest.NewRequest(http.MethodPost, "/driver/routes/"+routeID+"/reroute", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "driver-123")
	request.Header.Set("X-ShopStops-Actor-Role", "driver")
	request.Header.Set("Idempotency-Key", "reroute-attempt-1")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || repo.savedRerouteKey != "reroute-attempt-1" ||
		repo.savedRerouteRoute.DistanceMeters != 2000 || len(repo.savedRerouteGeometry) != 2 ||
		!strings.Contains(recorder.Body.String(), `"rerouted":true`) {
		t.Fatalf("status=%d saved=%+v geometry=%+v body=%s",
			recorder.Code, repo.savedRerouteRoute, repo.savedRerouteGeometry, recorder.Body.String())
	}
}

func TestDriverRerouteDoesNotPersistBelowThreshold(t *testing.T) {
	repo := &fakeRepository{reroutePlan: driverReroutePlan{
		Current:              geoPoint{Latitude: 38.9, Longitude: -77},
		Target:               geoPoint{Latitude: 38.91, Longitude: -76.99},
		PlannedDistanceMiles: 1,
		ToSequence:           2,
		SampleID:             42,
	}}
	handler := NewWithRouting(
		repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil)),
		fakeOSRMRouter{route: routing.Route{DistanceMeters: 1800, DurationSeconds: 300}},
	)
	request := httptest.NewRequest(http.MethodPost, "/driver/routes/00000000-0000-0000-0000-000000000010/reroute", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "driver-123")
	request.Header.Set("X-ShopStops-Actor-Role", "driver")
	request.Header.Set("Idempotency-Key", "reroute-no-change")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || repo.savedRerouteKey != "" ||
		!strings.Contains(recorder.Body.String(), `"rerouted":false`) {
		t.Fatalf("status=%d savedKey=%q body=%s", recorder.Code, repo.savedRerouteKey, recorder.Body.String())
	}
}

func TestShopperTrackingRequiresOwnedOrder(t *testing.T) {
	expected := ShopperTrackingResponse{
		Status: "in_progress", ETAMinutes: intPointer(8),
		DriverLocation: &geoPoint{Latitude: 38.9, Longitude: -77},
	}
	repo := &fakeRepository{shopperTracking: expected}
	request := httptest.NewRequest(http.MethodGet,
		"/shopper/orders/00000000-0000-0000-0000-000000000001/track", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "customer-123")
	request.Header.Set("X-ShopStops-Actor-Role", "customer")
	recorder := httptest.NewRecorder()
	New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil))).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK ||
		!strings.Contains(recorder.Body.String(), `"etaMinutes":8`) ||
		!strings.Contains(recorder.Body.String(), `"driverLocation":{"latitude":38.9`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
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
	request.Header.Set("X-ShopStops-Actor", "dispatch-123")
	request.Header.Set("X-ShopStops-Actor-Role", "dispatch")
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
