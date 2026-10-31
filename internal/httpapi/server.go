package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/eve32as/ShopStops-/internal/dispatch"
	"github.com/eve32as/ShopStops-/internal/routing"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrNotFound   = errors.New("not found")
	ErrIneligible = errors.New("not eligible")
	ErrForbidden  = errors.New("forbidden")
	ErrConflict   = errors.New("conflict")
)

type Server struct {
	repo            Repository
	token           string
	log             *slog.Logger
	payments        PaymentGateway
	screening       DriverScreeningProvider
	webhookSecret   string
	merchantPriceID string
	router          OSRMRouter
	rerouter        *dispatch.ReRouter
}

type OSRMRouter interface {
	Route(context.Context, []routing.Coordinate) (routing.Route, error)
}

type Repository interface {
	PingContext(context.Context) error
	NearbyMerchants(context.Context, string, int) ([]NearbyMerchant, error)
	NearbyRestaurants(context.Context, float64, float64, int) ([]NearbyMerchant, error)
	CatalogItems(context.Context, string) ([]CatalogItem, error)
	MerchantStoreLocations(context.Context, string) ([]MerchantStoreLocation, error)
	MerchantCatalog(context.Context, string) ([]MerchantCatalogItem, error)
	CreateMerchantCatalogItem(
		context.Context, string, merchantCatalogItemRequest, string, string,
	) (MerchantCatalogItem, error)
	UpdateMerchantCatalogItem(
		context.Context, string, string, merchantCatalogItemUpdateRequest,
	) (MerchantCatalogItem, error)
	CreateCustomerOrder(
		context.Context, customerOrderRequest, string, string, string,
	) (customerOrderResponse, error)
	CreateBundle(context.Context, bundleRequest, string) (bundleResponse, error)
	CreateCheckout(context.Context, checkoutRequest, string, string) (checkoutResponse, error)
	DriverRoute(context.Context, string, string) (routeResponse, error)
	DriverActiveRoute(context.Context, string) (string, error)
	RecordDriverLocation(context.Context, string, string, driverLocationRequest) (int64, error)
	RecordDriverStopEvent(
		context.Context, string, string, string, driverStopEventRequest,
	) (driverStopEventResponse, error)
}

type DriverFraudAlertRepository interface {
	ListDriverFraudAlerts(context.Context, string, int) ([]DriverFraudAlert, error)
	ResolveDriverFraudAlert(context.Context, string, string, string) error
}

type UnifiedRouteRepository interface {
	FindUnifiedRoute(context.Context, string, string) (string, bool, error)
	UnifiedRouteCoordinates(context.Context, string) ([]routing.Coordinate, error)
	CreateUnifiedRoute(context.Context, string, string, []routing.Route) (string, error)
}

type DriverAssignmentRepository interface {
	AssignUnifiedDriver(context.Context, string, string, string) (string, error)
}

type DriverDirectoryRepository interface {
	ListDriverProfiles(context.Context, string, int) ([]DriverProfile, error)
	SetDriverProfile(context.Context, string, string, string, string) (DriverProfile, error)
}

type DriverRerouteRepository interface {
	DriverReroutePlan(context.Context, string, string) (driverReroutePlan, error)
	SaveDriverReroute(
		context.Context, string, string, driverReroutePlan, string,
		float64, routing.Route, []dispatch.LocationCoordinate,
	) error
}

type ShopperTrackingRepository interface {
	ShopperOrderTracking(context.Context, string, string) (ShopperTrackingResponse, error)
}

type driverReroutePlan struct {
	Current              geoPoint
	Target               geoPoint
	PlannedDistanceMiles float64
	ToSequence           int
	SampleID             int64
}

type ShopperTrackingResponse struct {
	Status           string                        `json:"status"`
	DriverLocation   *geoPoint                     `json:"driverLocation,omitempty"`
	ETAMinutes       *int                          `json:"etaMinutes,omitempty"`
	LastUpdatedAt    *time.Time                    `json:"lastUpdatedAt,omitempty"`
	RouteCoordinates []dispatch.LocationCoordinate `json:"routeCoordinates,omitempty"`
}

type DriverFraudAlert struct {
	ID         string          `json:"id"`
	RouteID    string          `json:"routeId"`
	DriverRef  string          `json:"driverRef"`
	AlertType  string          `json:"alertType"`
	Severity   string          `json:"severity"`
	Status     string          `json:"status"`
	Evidence   json.RawMessage `json:"evidence"`
	DetectedAt time.Time       `json:"detectedAt"`
	ResolvedAt *time.Time      `json:"resolvedAt,omitempty"`
	ResolvedBy string          `json:"resolvedBy,omitempty"`
}

type NearbyMerchant struct {
	MerchantID     string  `json:"merchantId"`
	Name           string  `json:"name"`
	MerchantType   string  `json:"merchantType"`
	StoreLocation  string  `json:"storeLocationId"`
	DistanceMeters float64 `json:"distanceMeters"`
}

type CatalogItem struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	UnitPriceCents int    `json:"unitPriceCents"`
	VolumeLiters   string `json:"volumeLiters"`
}

type MerchantCatalogItem struct {
	ID              string `json:"id"`
	StoreLocationID string `json:"storeLocationId"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	UnitPriceCents  int    `json:"unitPriceCents"`
	VolumeLiters    string `json:"volumeLiters"`
	Available       bool   `json:"available"`
}

type MerchantStoreLocation struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type merchantCatalogItemRequest struct {
	StoreLocationID string  `json:"storeLocationId"`
	Name            string  `json:"name"`
	Description     string  `json:"description"`
	UnitPriceCents  int     `json:"unitPriceCents"`
	VolumeLiters    float64 `json:"volumeLiters"`
}

type merchantCatalogItemUpdateRequest struct {
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	UnitPriceCents int     `json:"unitPriceCents"`
	VolumeLiters   float64 `json:"volumeLiters"`
	Available      bool    `json:"available"`
}

type customerOrderRequest struct {
	StoreLocationID     string              `json:"storeLocationId"`
	Dropoff             geoPoint            `json:"dropoff"`
	DeliveryWindowStart time.Time           `json:"deliveryWindowStart"`
	DeliveryWindowEnd   time.Time           `json:"deliveryWindowEnd"`
	Items               []customerOrderItem `json:"items"`
}

type customerOrderItem struct {
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

type customerOrderResponse struct {
	ID                string `json:"id"`
	Status            string `json:"status"`
	ItemSubtotalCents int    `json:"itemSubtotalCents"`
}

type bundleRequest struct {
	SeedOrderID         string  `json:"seedOrderId"`
	CapacityLiters      float64 `json:"capacityLiters"`
	MaxOrders           *int    `json:"maxOrders"`
	PickupRadiusMeters  *int    `json:"pickupRadiusMeters"`
	DropoffRadiusMeters *int    `json:"dropoffRadiusMeters"`
}

type driverAssignmentRequest struct {
	DriverRef string `json:"driverRef"`
}

type bundleStop struct {
	ID              string  `json:"id"`
	OrderID         string  `json:"orderId"`
	Sequence        int     `json:"sequence"`
	Type            string  `json:"type"`
	Latitude        float64 `json:"latitude"`
	Longitude       float64 `json:"longitude"`
	Status          string  `json:"status"`
	StoreLocationID *string `json:"storeLocationId,omitempty"`
}

type bundleResponse struct {
	ID     string       `json:"id"`
	Status string       `json:"status"`
	Stops  []bundleStop `json:"stops"`
}

type routeResponse struct {
	ID       string      `json:"id"`
	Checkout string      `json:"checkoutId"`
	Status   string      `json:"status"`
	Provider string      `json:"routingProvider"`
	Stops    []routeStop `json:"stops"`
	Legs     []routeLeg  `json:"legs"`
}

type routeStop struct {
	ID              string  `json:"id"`
	Sequence        int     `json:"sequence"`
	Type            string  `json:"type"`
	Latitude        float64 `json:"latitude"`
	Longitude       float64 `json:"longitude"`
	Status          string  `json:"status"`
	StoreLocationID *string `json:"storeLocationId,omitempty"`
}

type routeLeg struct {
	FromSequence    int    `json:"fromSequence"`
	ToSequence      int    `json:"toSequence"`
	DistanceMeters  int    `json:"distanceMeters"`
	DurationSeconds int    `json:"durationSeconds"`
	Provider        string `json:"provider"`
}

type driverLocationRequest struct {
	EventID    string    `json:"eventId"`
	ObservedAt time.Time `json:"observedAt"`
	Location   geoPoint  `json:"location"`
}

type driverStopEventRequest struct {
	EventID    string    `json:"eventId"`
	Type       string    `json:"type"`
	ObservedAt time.Time `json:"observedAt"`
}

type driverStopEventResponse struct {
	StopID      string `json:"stopId"`
	StopStatus  string `json:"stopStatus"`
	RouteStatus string `json:"routeStatus"`
}

type geoPoint struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type checkoutRequest struct {
	AnchorStoreLocationID string    `json:"anchorStoreLocationId"`
	OrderIDs              []string  `json:"orderIds"`
	Dropoff               geoPoint  `json:"dropoff"`
	DeliveryWindowStart   time.Time `json:"deliveryWindowStart"`
	DeliveryWindowEnd     time.Time `json:"deliveryWindowEnd"`
}

type checkoutInvoiceLine struct {
	Type        string `json:"type"`
	MerchantID  string `json:"merchantId,omitempty"`
	AmountCents int    `json:"amountCents"`
}

type checkoutResponse struct {
	ID           string                `json:"id"`
	Status       string                `json:"status"`
	Currency     string                `json:"currency"`
	InvoiceLines []checkoutInvoiceLine `json:"invoiceLines"`
	TotalCents   int                   `json:"totalCents"`
}

func New(repo Repository, token string, logger *slog.Logger) http.Handler {
	s := &Server{repo: repo, token: token, log: logger}
	return s.handler()
}

func NewWithPayments(
	repo Repository, token string, logger *slog.Logger,
	payments PaymentGateway, webhookSecret string, merchantPriceIDs ...string,
) http.Handler {
	s := &Server{
		repo: repo, token: token, log: logger,
		payments: payments, webhookSecret: webhookSecret,
	}
	if len(merchantPriceIDs) > 0 {
		s.merchantPriceID = merchantPriceIDs[0]
	}
	return s.handler()
}

func NewWithRouting(
	repo Repository, token string, logger *slog.Logger, router OSRMRouter,
) http.Handler {
	s := &Server{
		repo: repo, token: token, log: logger, router: router,
		rerouter: dispatch.NewReRouter(router),
	}
	return s.handler()
}

func NewWithPaymentsAndRouting(
	repo Repository, token string, logger *slog.Logger,
	payments PaymentGateway, webhookSecret string, router OSRMRouter,
	merchantPriceIDs ...string,
) http.Handler {
	s := &Server{
		repo: repo, token: token, log: logger,
		payments: payments, webhookSecret: webhookSecret, router: router,
		rerouter: dispatch.NewReRouter(router),
	}
	if len(merchantPriceIDs) > 0 {
		s.merchantPriceID = merchantPriceIDs[0]
	}
	return s.handler()
}

func NewWithIntegrations(
	repo Repository, token string, logger *slog.Logger,
	payments PaymentGateway, webhookSecret string, router OSRMRouter,
	merchantPriceID string, screening DriverScreeningProvider,
) http.Handler {
	s := &Server{
		repo: repo, token: token, log: logger, payments: payments,
		webhookSecret: webhookSecret, router: router, screening: screening,
	}
	if router != nil {
		s.rerouter = dispatch.NewReRouter(router)
	}
	s.merchantPriceID = merchantPriceID
	return s.handler()
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.Handle("GET /stores/{storeLocationId}/nearby-merchants", s.authorized(http.HandlerFunc(s.nearbyMerchants)))
	mux.Handle("GET /stores/nearby-restaurants", s.authorized(http.HandlerFunc(s.nearbyRestaurants)))
	mux.Handle("GET /stores/{storeLocationId}/catalog", s.authorized(http.HandlerFunc(s.catalogItems)))
	mux.Handle("POST /orders", s.authorized(http.HandlerFunc(s.createCustomerOrder)))
	mux.Handle("GET /merchant/store-locations", s.authorized(http.HandlerFunc(s.merchantStoreLocations)))
	mux.Handle("GET /merchant/catalog", s.authorized(http.HandlerFunc(s.merchantCatalog)))
	mux.Handle("POST /merchant/catalog", s.authorized(http.HandlerFunc(s.createMerchantCatalogItem)))
	mux.Handle("PATCH /merchant/catalog/{itemId}", s.authorized(http.HandlerFunc(s.updateMerchantCatalogItem)))
	mux.Handle("POST /dispatch/bundles", s.authorized(http.HandlerFunc(s.createBundle)))
	mux.Handle("POST /dispatch/routes/{routeId}/assign", s.authorized(http.HandlerFunc(s.assignDriverToRoute)))
	mux.Handle("GET /admin/drivers", s.authorized(http.HandlerFunc(s.listDriverProfiles)))
	mux.Handle("PUT /admin/drivers/{driverRef}", s.authorized(http.HandlerFunc(s.setDriverProfile)))
	mux.Handle("POST /admin/drivers/{driverRef}/screenings", s.authorized(http.HandlerFunc(s.startDriverScreening)))
	mux.Handle("POST /admin/drivers/{driverRef}/screenings/refresh", s.authorized(http.HandlerFunc(s.refreshDriverScreening)))
	mux.Handle("PUT /admin/drivers/{driverRef}/screenings/review", s.authorized(http.HandlerFunc(s.reviewDriverScreening)))
	mux.Handle("POST /admin/drivers/{driverRef}/screenings/{screeningId}/reconcile", s.authorized(http.HandlerFunc(s.reconcileDriverScreening)))
	mux.Handle("GET /driver/routes/{routeId}", s.authorized(http.HandlerFunc(s.getDriverRoute)))
	mux.Handle("GET /driver/routes/active", s.authorized(http.HandlerFunc(s.getActiveDriverRoute)))
	mux.Handle("POST /driver/routes/{routeId}/location", s.authorized(http.HandlerFunc(s.recordDriverLocation)))
	mux.Handle("POST /driver/routes/{routeId}/stops/{stopId}/events", s.authorized(http.HandlerFunc(s.recordDriverStopEvent)))
	mux.Handle("POST /driver/routes/{routeId}/reroute", s.authorized(http.HandlerFunc(s.rerouteDriver)))
	mux.Handle("GET /shopper/orders/{orderId}/track", s.authorized(http.HandlerFunc(s.trackShopperOrder)))
	mux.Handle("GET /admin/driver-fraud-alerts", s.authorized(http.HandlerFunc(s.listDriverFraudAlerts)))
	mux.Handle("PATCH /admin/driver-fraud-alerts/{alertId}", s.authorized(http.HandlerFunc(s.resolveDriverFraudAlert)))
	mux.Handle("GET /admin/notifications", s.authorized(http.HandlerFunc(s.listDeliveryMessageLogs)))
	mux.Handle("POST /admin/notifications/{notificationId}/retry", s.authorized(http.HandlerFunc(s.retryDeliveryMessage)))
	mux.Handle("PUT /admin/driver-payout-profiles/{driverRef}", s.authorized(http.HandlerFunc(s.upsertDriverPayoutProfile)))
	mux.Handle("POST /checkouts", s.authorized(http.HandlerFunc(s.createCheckout)))
	mux.Handle("POST /checkouts/{checkoutId}/payment", s.authorized(http.HandlerFunc(s.createCheckoutPayment)))
	mux.Handle("POST /checkouts/{checkoutId}/route", s.authorized(http.HandlerFunc(s.createUnifiedRoute)))
	mux.Handle("GET /merchant/subscription", s.authorized(http.HandlerFunc(s.getMerchantSubscription)))
	mux.Handle("POST /merchant/subscription/setup-intent", s.authorized(http.HandlerFunc(s.createMerchantSetupIntent)))
	mux.Handle("POST /merchant/subscription", s.authorized(http.HandlerFunc(s.createMerchantSubscription)))
	mux.Handle("POST /admin/checkouts/{checkoutId}/refund", s.authorized(http.HandlerFunc(s.refundCheckout)))
	mux.HandleFunc("POST /webhooks/stripe", s.stripeWebhook)
	return mux
}

func (s *Server) authorized(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if value == r.Header.Get("Authorization") ||
			subtle.ConstantTimeCompare([]byte(value), []byte(s.token)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if s.repo == nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.repo.PingContext(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) nearbyMerchants(w http.ResponseWriter, r *http.Request) {
	storeID := r.PathValue("storeLocationId")
	if !isUUID(storeID) {
		writeError(w, http.StatusBadRequest, "invalid storeLocationId")
		return
	}

	radius := 500
	if raw := r.URL.Query().Get("radiusMeters"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > 500 {
			writeError(w, http.StatusBadRequest, "radiusMeters must be between 0 and 500")
			return
		}
		radius = value
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	merchants, err := s.repo.NearbyMerchants(ctx, storeID, radius)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "store location not found")
		return
	}
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, merchants)
}

func (s *Server) nearbyRestaurants(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizedActorRole(w, r, "customer"); !ok {
		return
	}
	latitude, latErr := strconv.ParseFloat(r.URL.Query().Get("latitude"), 64)
	longitude, lonErr := strconv.ParseFloat(r.URL.Query().Get("longitude"), 64)
	if latErr != nil || lonErr != nil || math.IsNaN(latitude) || math.IsInf(latitude, 0) ||
		math.IsNaN(longitude) || math.IsInf(longitude, 0) ||
		latitude < -90 || latitude > 90 || longitude < -180 || longitude > 180 {
		writeError(w, http.StatusBadRequest, "valid latitude and longitude are required")
		return
	}
	radius := 5000
	if raw := r.URL.Query().Get("radiusMeters"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 100 || value > 20000 {
			writeError(w, http.StatusBadRequest, "radiusMeters must be between 100 and 20000")
			return
		}
		radius = value
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	restaurants, err := s.repo.NearbyRestaurants(ctx, latitude, longitude, radius)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, restaurants)
}

func (s *Server) catalogItems(w http.ResponseWriter, r *http.Request) {
	storeID := r.PathValue("storeLocationId")
	if !isUUID(storeID) {
		writeError(w, http.StatusBadRequest, "invalid storeLocationId")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	items, err := s.repo.CatalogItems(ctx, storeID)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "storefront not found or unavailable")
	case err != nil:
		s.databaseError(w, err)
	default:
		writeJSON(w, http.StatusOK, items)
	}
}

func (s *Server) merchantCatalog(w http.ResponseWriter, r *http.Request) {
	merchantID, ok := s.authorizedActorRole(w, r, "merchant")
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	items, err := s.repo.MerchantCatalog(ctx, merchantID)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "merchant not found")
		return
	}
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) merchantStoreLocations(w http.ResponseWriter, r *http.Request) {
	merchantID, ok := s.authorizedActorRole(w, r, "merchant")
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	locations, err := s.repo.MerchantStoreLocations(ctx, merchantID)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "merchant not found")
		return
	}
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, locations)
}

func (s *Server) createMerchantCatalogItem(w http.ResponseWriter, r *http.Request) {
	merchantID, ok := s.authorizedActorRole(w, r, "merchant")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8*1024)
	var request merchantCatalogItemRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return
	}
	if !isUUID(request.StoreLocationID) || !validCatalogItemFields(
		request.Name, request.Description, request.UnitPriceCents, request.VolumeLiters,
	) {
		writeError(w, http.StatusBadRequest, "invalid catalog item")
		return
	}
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if len(idempotencyKey) == 0 || len(idempotencyKey) > 200 ||
		strings.TrimSpace(idempotencyKey) != idempotencyKey {
		writeError(w, http.StatusBadRequest, "valid Idempotency-Key header is required")
		return
	}
	canonicalRequest, err := json.Marshal(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid catalog item")
		return
	}
	fingerprintBytes := sha256.Sum256(canonicalRequest)
	fingerprint := hex.EncodeToString(fingerprintBytes[:])
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	item, err := s.repo.CreateMerchantCatalogItem(
		ctx, merchantID, request, idempotencyKey, fingerprint,
	)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "merchant or owned store location not found")
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "catalog idempotency key conflicts with another request")
	case err != nil:
		s.databaseError(w, err)
	default:
		writeJSON(w, http.StatusCreated, item)
	}
}

func (s *Server) updateMerchantCatalogItem(w http.ResponseWriter, r *http.Request) {
	merchantID, ok := s.authorizedActorRole(w, r, "merchant")
	if !ok {
		return
	}
	itemID := r.PathValue("itemId")
	if !isUUID(itemID) {
		writeError(w, http.StatusBadRequest, "invalid catalog item ID")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8*1024)
	var request merchantCatalogItemUpdateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return
	}
	if !validCatalogItemFields(
		request.Name, request.Description, request.UnitPriceCents, request.VolumeLiters,
	) {
		writeError(w, http.StatusBadRequest, "invalid catalog item")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	item, err := s.repo.UpdateMerchantCatalogItem(ctx, merchantID, itemID, request)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "catalog item not found")
	case err != nil:
		s.databaseError(w, err)
	default:
		writeJSON(w, http.StatusOK, item)
	}
}

func validCatalogItemFields(name, description string, price int, volume float64) bool {
	return strings.TrimSpace(name) != "" && len(strings.TrimSpace(name)) <= 200 &&
		len(description) <= 1000 && price >= 1 && price <= 10_000_000 &&
		volume > 0 && volume <= 100_000 && !math.IsInf(volume, 0) && !math.IsNaN(volume) &&
		math.Abs(volume*100-math.Round(volume*100)) < 1e-8
}

func (s *Server) createCustomerOrder(w http.ResponseWriter, r *http.Request) {
	customerRef, ok := s.authorizedActorRole(w, r, "customer")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
	var request customerOrderRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return
	}
	if !isUUID(request.StoreLocationID) ||
		request.Dropoff.Latitude < -90 || request.Dropoff.Latitude > 90 ||
		request.Dropoff.Longitude < -180 || request.Dropoff.Longitude > 180 ||
		request.DeliveryWindowStart.IsZero() || request.DeliveryWindowEnd.IsZero() ||
		!request.DeliveryWindowEnd.After(request.DeliveryWindowStart) ||
		request.DeliveryWindowStart.After(time.Now().Add(30*24*time.Hour)) ||
		request.DeliveryWindowEnd.Before(time.Now()) ||
		request.DeliveryWindowEnd.Sub(request.DeliveryWindowStart) > 24*time.Hour ||
		len(request.Items) == 0 || len(request.Items) > 50 {
		writeError(w, http.StatusBadRequest, "invalid order details")
		return
	}
	seenItems := make(map[string]struct{}, len(request.Items))
	for _, item := range request.Items {
		if !isUUID(item.CatalogItemID) || item.Quantity < 1 || item.Quantity > 99 {
			writeError(w, http.StatusBadRequest, "invalid catalog item or quantity")
			return
		}
		if _, exists := seenItems[item.CatalogItemID]; exists {
			writeError(w, http.StatusBadRequest, "catalog item IDs must be unique")
			return
		}
		seenItems[item.CatalogItemID] = struct{}{}
	}
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if len(idempotencyKey) == 0 || len(idempotencyKey) > 200 ||
		strings.TrimSpace(idempotencyKey) != idempotencyKey {
		writeError(w, http.StatusBadRequest, "valid Idempotency-Key header is required")
		return
	}
	canonicalRequest, err := json.Marshal(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid order details")
		return
	}
	fingerprintBytes := sha256.Sum256(canonicalRequest)
	fingerprint := hex.EncodeToString(fingerprintBytes[:])

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	response, err := s.repo.CreateCustomerOrder(
		ctx, request, customerRef, idempotencyKey, fingerprint,
	)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "storefront, catalog item, or delivery zone unavailable")
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "order is unavailable or idempotency key conflicts")
	case err != nil:
		s.databaseError(w, err)
	default:
		writeJSON(w, http.StatusCreated, response)
	}
}

func (s *Server) createBundle(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizedActorRoles(w, r, "dispatch", "admin"); !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var request bundleRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return
	}
	if !isUUID(request.SeedOrderID) || request.CapacityLiters <= 0 ||
		request.CapacityLiters > 10000 || optionalInt(request.MaxOrders, 3) < 1 ||
		optionalInt(request.MaxOrders, 3) > 100 ||
		optionalInt(request.PickupRadiusMeters, 2000) > 100000 ||
		optionalInt(request.DropoffRadiusMeters, 2000) > 100000 ||
		optionalInt(request.PickupRadiusMeters, 2000) < 0 ||
		optionalInt(request.DropoffRadiusMeters, 2000) < 0 {
		writeError(w, http.StatusBadRequest, "invalid bundle parameters")
		return
	}
	request.MaxOrders = intPointer(optionalInt(request.MaxOrders, 3))
	request.PickupRadiusMeters = intPointer(optionalInt(request.PickupRadiusMeters, 2000))
	request.DropoffRadiusMeters = intPointer(optionalInt(request.DropoffRadiusMeters, 2000))
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if len(idempotencyKey) == 0 || len(idempotencyKey) > 200 || strings.TrimSpace(idempotencyKey) != idempotencyKey {
		writeError(w, http.StatusBadRequest, "valid Idempotency-Key header is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	response, err := s.repo.CreateBundle(ctx, request, idempotencyKey)
	if errors.Is(err, ErrIneligible) {
		writeError(w, http.StatusConflict, "seed order is not eligible for dispatch")
		return
	}
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) createCheckout(w http.ResponseWriter, r *http.Request) {
	customerRef, ok := s.authorizedActorRole(w, r, "customer")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
	var request checkoutRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return
	}
	if !isUUID(request.AnchorStoreLocationID) ||
		len(request.OrderIDs) == 0 || len(request.OrderIDs) > 100 ||
		request.Dropoff.Latitude < -90 || request.Dropoff.Latitude > 90 ||
		request.Dropoff.Longitude < -180 || request.Dropoff.Longitude > 180 ||
		request.DeliveryWindowStart.IsZero() || request.DeliveryWindowEnd.IsZero() ||
		!request.DeliveryWindowEnd.After(request.DeliveryWindowStart) {
		writeError(w, http.StatusBadRequest, "invalid checkout details")
		return
	}
	seenOrders := make(map[string]struct{}, len(request.OrderIDs))
	for _, orderID := range request.OrderIDs {
		if !isUUID(orderID) {
			writeError(w, http.StatusBadRequest, "invalid order ID")
			return
		}
		if _, exists := seenOrders[orderID]; exists {
			writeError(w, http.StatusBadRequest, "order IDs must be unique")
			return
		}
		seenOrders[orderID] = struct{}{}
	}
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if len(idempotencyKey) == 0 || len(idempotencyKey) > 200 ||
		strings.TrimSpace(idempotencyKey) != idempotencyKey {
		writeError(w, http.StatusBadRequest, "valid Idempotency-Key header is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	response, err := s.repo.CreateCheckout(ctx, request, customerRef, idempotencyKey)
	if errors.Is(err, ErrConflict) {
		writeError(w, http.StatusConflict, "checkout orders are not eligible or key conflicts")
		return
	}
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) createUnifiedRoute(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizedActorRoles(w, r, "dispatch", "admin"); !ok {
		return
	}
	checkoutID := r.PathValue("checkoutId")
	if !isUUID(checkoutID) {
		writeError(w, http.StatusBadRequest, "valid checkout ID is required")
		return
	}
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" || len(idempotencyKey) > 200 ||
		strings.TrimSpace(idempotencyKey) != idempotencyKey {
		writeError(w, http.StatusBadRequest, "valid Idempotency-Key header is required")
		return
	}
	repository, ok := s.repo.(UnifiedRouteRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "route creation is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	routeID, found, err := repository.FindUnifiedRoute(ctx, checkoutID, idempotencyKey)
	if errors.Is(err, ErrConflict) {
		writeError(w, http.StatusConflict, "route idempotency key conflicts with another checkout")
		return
	}
	if err != nil {
		s.databaseError(w, err)
		return
	}
	if found {
		writeJSON(w, http.StatusOK, map[string]string{
			"routeId": routeID, "checkoutId": checkoutID, "routingProvider": "osrm",
		})
		return
	}
	if s.router == nil {
		writeError(w, http.StatusServiceUnavailable, "OSRM routing is unavailable")
		return
	}

	coordinates, err := repository.UnifiedRouteCoordinates(ctx, checkoutID)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "checkout not found")
		return
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "checkout is not eligible for routing")
		return
	case err != nil:
		s.databaseError(w, err)
		return
	}
	if len(coordinates) != 3 {
		s.log.Error("route coordinate query returned an invalid stop count", "checkout_id", checkoutID)
		writeError(w, http.StatusInternalServerError, "route could not be created")
		return
	}

	type legResult struct {
		index int
		route routing.Route
		err   error
	}
	routeCtx, cancelRoutes := context.WithCancel(ctx)
	results := make(chan legResult, 2)
	for i := range 2 {
		go func(index int) {
			route, err := s.router.Route(
				routeCtx, coordinates[index:index+2],
			)
			results <- legResult{index: index, route: route, err: err}
		}(i)
	}
	legs := make([]routing.Route, 2)
	var routeErr error
	for range 2 {
		result := <-results
		legs[result.index] = result.route
		if result.err != nil && routeErr == nil {
			routeErr = result.err
			cancelRoutes()
		}
	}
	cancelRoutes()
	if routeErr != nil {
		if errors.Is(routeErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, "OSRM routing timed out")
		} else {
			s.log.Error("OSRM route request failed", "error", routeErr)
			writeError(w, http.StatusBadGateway, "route could not be calculated")
		}
		return
	}
	for _, leg := range legs {
		if !validPersistedRouteMeasurement(leg.DistanceMeters) ||
			!validPersistedRouteMeasurement(leg.DurationSeconds) {
			writeError(w, http.StatusBadGateway, "OSRM returned invalid route measurements")
			return
		}
	}
	routeID, err = repository.CreateUnifiedRoute(ctx, checkoutID, idempotencyKey, legs)
	switch {
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "route idempotency key conflicts with another checkout")
	case err != nil:
		s.databaseError(w, err)
	default:
		writeJSON(w, http.StatusCreated, map[string]string{
			"routeId": routeID, "checkoutId": checkoutID, "routingProvider": "osrm",
		})
	}
}

func (s *Server) assignDriverToRoute(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorizedActorRoles(w, r, "dispatch", "admin")
	if !ok {
		return
	}
	routeID := r.PathValue("routeId")
	if !isUUID(routeID) {
		writeError(w, http.StatusBadRequest, "valid route ID is required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8*1024)
	var request driverAssignmentRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return
	}
	request.DriverRef = strings.TrimSpace(request.DriverRef)
	if request.DriverRef == "" || len(request.DriverRef) > 200 {
		writeError(w, http.StatusBadRequest, "valid driver identity is required")
		return
	}
	repository, ok := s.repo.(DriverAssignmentRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "driver assignment is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	routeStatus, err := repository.AssignUnifiedDriver(ctx, routeID, request.DriverRef, actor)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "route not found")
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "route or driver is not available for assignment")
	case err != nil:
		s.databaseError(w, err)
	default:
		writeJSON(w, http.StatusOK, map[string]string{
			"routeId": routeID, "driverRef": request.DriverRef, "status": routeStatus,
		})
	}
}

func validPersistedRouteMeasurement(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) &&
		value >= 0 && value <= math.MaxInt32
}

func (s *Server) rerouteDriver(w http.ResponseWriter, r *http.Request) {
	driverRef, ok := s.authorizedActorRole(w, r, "driver")
	if !ok {
		return
	}
	routeID := r.PathValue("routeId")
	if !isUUID(routeID) {
		writeError(w, http.StatusBadRequest, "valid route ID is required")
		return
	}
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" || len(idempotencyKey) > 200 ||
		strings.TrimSpace(idempotencyKey) != idempotencyKey {
		writeError(w, http.StatusBadRequest, "valid Idempotency-Key header is required")
		return
	}
	repository, ok := s.repo.(DriverRerouteRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "driver rerouting is unavailable")
		return
	}
	if s.rerouter == nil {
		writeError(w, http.StatusServiceUnavailable, "OSRM routing is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	plan, err := repository.DriverReroutePlan(ctx, routeID, driverRef)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "route not found")
		return
	case errors.Is(err, ErrForbidden):
		writeError(w, http.StatusForbidden, "route is not assigned to this driver")
		return
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "route needs an active stop and a fresh GPS sample to reroute")
		return
	case err != nil:
		s.databaseError(w, err)
		return
	}
	rerouted, coordinates, deviationMiles, route, err :=
		s.rerouter.EvaluateCourseCorrectionDetails(
			ctx, routeID,
			plan.Current.Longitude, plan.Current.Latitude,
			plan.Target.Longitude, plan.Target.Latitude,
			plan.PlannedDistanceMiles,
		)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, "OSRM rerouting timed out")
		} else {
			s.log.Error("OSRM reroute request failed", "error", err)
			writeError(w, http.StatusBadGateway, "route could not be recalculated")
		}
		return
	}
	if !rerouted {
		writeJSON(w, http.StatusOK, map[string]any{
			"routeId": routeID, "rerouted": false, "deviationMiles": deviationMiles,
		})
		return
	}
	if len(coordinates) < 2 || !validPersistedRouteMeasurement(route.DistanceMeters) ||
		!validPersistedRouteMeasurement(route.DurationSeconds) {
		writeError(w, http.StatusBadGateway, "OSRM returned invalid route geometry or measurements")
		return
	}
	if err := repository.SaveDriverReroute(
		ctx, routeID, driverRef, plan, idempotencyKey,
		deviationMiles, route, coordinates,
	); err != nil {
		switch {
		case errors.Is(err, ErrForbidden):
			writeError(w, http.StatusForbidden, "route is not assigned to this driver")
		case errors.Is(err, ErrConflict):
			writeError(w, http.StatusConflict, "route or GPS sample changed during rerouting")
		default:
			s.databaseError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"routeId": routeID, "rerouted": true, "deviationMiles": deviationMiles,
		"distanceMeters":   int(math.Round(route.DistanceMeters)),
		"durationSeconds":  int(math.Round(route.DurationSeconds)),
		"routeCoordinates": coordinates,
	})
}

func (s *Server) trackShopperOrder(w http.ResponseWriter, r *http.Request) {
	customerRef, ok := s.authorizedActorRole(w, r, "customer")
	if !ok {
		return
	}
	orderID := r.PathValue("orderId")
	if !isUUID(orderID) {
		writeError(w, http.StatusBadRequest, "valid order ID is required")
		return
	}
	repository, ok := s.repo.(ShopperTrackingRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "shopper tracking is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	tracking, err := repository.ShopperOrderTracking(ctx, orderID, customerRef)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "order not found")
	case err != nil:
		s.databaseError(w, err)
	default:
		writeJSON(w, http.StatusOK, tracking)
	}
}

func (s *Server) getDriverRoute(w http.ResponseWriter, r *http.Request) {
	routeID := r.PathValue("routeId")
	driverRef, ok := s.authorizedActorRole(w, r, "driver")
	if !ok {
		return
	}

	if !isUUID(routeID) {
		writeError(w, http.StatusBadRequest, "valid route and authenticated driver are required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	route, err := s.repo.DriverRoute(ctx, routeID, driverRef)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "route not found")
		return
	}
	if errors.Is(err, ErrForbidden) {
		writeError(w, http.StatusForbidden, "route is not assigned to this driver")
		return
	}
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, route)
}

func (s *Server) getActiveDriverRoute(w http.ResponseWriter, r *http.Request) {
	driverRef, ok := s.authorizedActorRole(w, r, "driver")
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	routeID, err := s.repo.DriverActiveRoute(ctx, driverRef)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "no active assigned route")
		return
	}
	if err != nil {
		s.databaseError(w, err)
		return
	}
	route, err := s.repo.DriverRoute(ctx, routeID, driverRef)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "no active assigned route")
	case err != nil:
		s.databaseError(w, err)
	default:
		writeJSON(w, http.StatusOK, route)
	}
}

func (s *Server) recordDriverLocation(w http.ResponseWriter, r *http.Request) {
	routeID := r.PathValue("routeId")
	driverRef, ok := s.authorizedActorRole(w, r, "driver")
	if !ok {
		return
	}
	if !isUUID(routeID) {
		writeError(w, http.StatusBadRequest, "valid route and authenticated driver are required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8*1024)
	var request driverLocationRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return
	}
	if request.EventID == "" || len(request.EventID) > 200 ||
		request.ObservedAt.IsZero() || request.Location.Latitude < -90 ||
		request.Location.Latitude > 90 || request.Location.Longitude < -180 ||
		request.Location.Longitude > 180 ||
		request.ObservedAt.After(time.Now().Add(time.Minute)) {
		writeError(w, http.StatusBadRequest, "invalid location event")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	sampleID, err := s.repo.RecordDriverLocation(ctx, routeID, driverRef, request)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "route not found")
	case errors.Is(err, ErrForbidden):
		writeError(w, http.StatusForbidden, "route is not assigned to this driver")
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "location conflicts with route state or sample interval")
	case err != nil:
		s.databaseError(w, err)
	default:
		writeJSON(w, http.StatusAccepted, map[string]int64{"sampleId": sampleID})
	}
}

func (s *Server) recordDriverStopEvent(w http.ResponseWriter, r *http.Request) {
	routeID := r.PathValue("routeId")
	stopID := r.PathValue("stopId")
	driverRef, ok := s.authorizedActorRole(w, r, "driver")
	if !ok {
		return
	}
	if !isUUID(routeID) || !isUUID(stopID) {
		writeError(w, http.StatusBadRequest, "valid route, stop, and authenticated driver are required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8*1024)
	var request driverStopEventRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return
	}
	if strings.TrimSpace(request.EventID) == "" || len(request.EventID) > 200 ||
		(request.Type != "arrive" && request.Type != "complete") ||
		request.ObservedAt.IsZero() || request.ObservedAt.After(time.Now().Add(time.Minute)) {
		writeError(w, http.StatusBadRequest, "invalid stop event")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	response, err := s.repo.RecordDriverStopEvent(ctx, routeID, stopID, driverRef, request)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "route or stop not found")
	case errors.Is(err, ErrForbidden):
		writeError(w, http.StatusForbidden, "route is not assigned to this driver")
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "stop event conflicts with the route sequence or current state")
	case err != nil:
		s.databaseError(w, err)
	default:
		writeJSON(w, http.StatusOK, response)
	}
}

func (s *Server) listDriverFraudAlerts(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizedActorRole(w, r, "admin"); !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && status != "open" && status != "reviewed" && status != "dismissed" {
		writeError(w, http.StatusBadRequest, "status must be open, reviewed, or dismissed")
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 100")
			return
		}
		limit = value
	}
	repository, ok := s.repo.(DriverFraudAlertRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "driver fraud monitoring is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	alerts, err := repository.ListDriverFraudAlerts(ctx, status, limit)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, alerts)
}

func (s *Server) resolveDriverFraudAlert(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorizedActorRole(w, r, "admin")
	if !ok {
		return
	}
	alertID := r.PathValue("alertId")
	if !isUUID(alertID) {
		writeError(w, http.StatusBadRequest, "invalid alert ID")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8*1024)
	var request struct {
		Status string `json:"status"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return
	}
	if request.Status != "reviewed" && request.Status != "dismissed" {
		writeError(w, http.StatusBadRequest, "status must be reviewed or dismissed")
		return
	}
	repository, ok := s.repo.(DriverFraudAlertRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "driver fraud monitoring is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	err := repository.ResolveDriverFraudAlert(ctx, alertID, actor, request.Status)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "driver fraud alert not found")
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "driver fraud alert is already resolved")
	case err != nil:
		s.databaseError(w, err)
	default:
		writeJSON(w, http.StatusOK, map[string]string{"id": alertID, "status": request.Status})
	}
}

type PostgresRepository struct {
	db               *sql.DB
	deliveryFeeCents int
	serviceFeeCents  int
}

func NewPostgresRepository(db *sql.DB, deliveryFeeCents, serviceFeeCents int) *PostgresRepository {
	return &PostgresRepository{
		db: db, deliveryFeeCents: deliveryFeeCents, serviceFeeCents: serviceFeeCents,
	}
}

func (p *PostgresRepository) PingContext(ctx context.Context) error {
	return p.db.PingContext(ctx)
}

func (p *PostgresRepository) NearbyMerchants(
	ctx context.Context, storeID string, radius int,
) ([]NearbyMerchant, error) {
	var exists bool
	if err := p.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM store_locations WHERE id = $1 AND active)`,
		storeID,
	).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT merchant_id, merchant_name, merchant_type, store_location_id, distance_meters
		FROM nearby_subscribed_merchants($1, $2)`, storeID, radius)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	merchants := make([]NearbyMerchant, 0)
	for rows.Next() {
		var merchant NearbyMerchant
		if err := rows.Scan(
			&merchant.MerchantID, &merchant.Name, &merchant.MerchantType,
			&merchant.StoreLocation, &merchant.DistanceMeters,
		); err != nil {
			return nil, err
		}
		merchants = append(merchants, merchant)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return merchants, nil
}

func (p *PostgresRepository) NearbyRestaurants(
	ctx context.Context, latitude, longitude float64, radius int,
) ([]NearbyMerchant, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT merchant.id::text, merchant.name, merchant.merchant_type,
		       store.id::text,
		       ST_Distance(
		           store.location,
		           ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography
		       )
		FROM store_locations store
		JOIN merchants merchant
		  ON merchant.id = store.merchant_id AND merchant.active
		JOIN merchant_storefront_visibility visibility
		  ON visibility.merchant_id = merchant.id AND visibility.visible
		WHERE store.active
		  AND merchant.merchant_type = 'restaurant'
		  AND ST_DWithin(
		      store.location,
		      ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography,
		      $3
		  )
		  AND EXISTS (
		      SELECT 1 FROM delivery_zones zone
		      WHERE zone.enabled AND ST_Covers(zone.boundary, store.location::geometry)
		  )
		ORDER BY store.location <-> ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography,
		         store.id
		LIMIT 50`, longitude, latitude, radius)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	restaurants := make([]NearbyMerchant, 0)
	for rows.Next() {
		var restaurant NearbyMerchant
		if err := rows.Scan(
			&restaurant.MerchantID, &restaurant.Name, &restaurant.MerchantType,
			&restaurant.StoreLocation, &restaurant.DistanceMeters,
		); err != nil {
			return nil, err
		}
		restaurants = append(restaurants, restaurant)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return restaurants, nil
}

func (p *PostgresRepository) CatalogItems(
	ctx context.Context, storeID string,
) ([]CatalogItem, error) {
	var visible bool
	if err := p.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM store_locations s
			JOIN merchants m ON m.id = s.merchant_id AND m.active
			JOIN merchant_storefront_visibility v
			  ON v.merchant_id = m.id AND v.visible
			WHERE s.id = $1 AND s.active
		)`, storeID,
	).Scan(&visible); err != nil {
		return nil, err
	}
	if !visible {
		return nil, ErrNotFound
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, name, description, unit_price_cents, volume_liters::text
		FROM catalog_items
		WHERE store_location_id = $1 AND available
		ORDER BY name, id
		LIMIT 100`, storeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]CatalogItem, 0)
	for rows.Next() {
		var item CatalogItem
		if err := rows.Scan(
			&item.ID, &item.Name, &item.Description,
			&item.UnitPriceCents, &item.VolumeLiters,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (p *PostgresRepository) MerchantCatalog(
	ctx context.Context, merchantID string,
) ([]MerchantCatalogItem, error) {
	var exists bool
	if err := p.db.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM merchants WHERE id = $1 AND active)`,
		merchantID,
	).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT i.id, i.store_location_id, i.name, i.description,
		       i.unit_price_cents, i.volume_liters::text, i.available
		FROM catalog_items i
		JOIN store_locations s ON s.id = i.store_location_id
		WHERE s.merchant_id = $1
		ORDER BY s.name, i.name, i.id
		LIMIT 500`, merchantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]MerchantCatalogItem, 0)
	for rows.Next() {
		var item MerchantCatalogItem
		if err := rows.Scan(
			&item.ID, &item.StoreLocationID, &item.Name, &item.Description,
			&item.UnitPriceCents, &item.VolumeLiters, &item.Available,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (p *PostgresRepository) MerchantStoreLocations(
	ctx context.Context, merchantID string,
) ([]MerchantStoreLocation, error) {
	var exists bool
	if err := p.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM merchants WHERE id = $1 AND active)`,
		merchantID,
	).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, name FROM store_locations
		WHERE merchant_id = $1
		ORDER BY name, id`, merchantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	locations := make([]MerchantStoreLocation, 0)
	for rows.Next() {
		var location MerchantStoreLocation
		if err := rows.Scan(&location.ID, &location.Name); err != nil {
			return nil, err
		}
		locations = append(locations, location)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return locations, nil
}

func (p *PostgresRepository) CreateMerchantCatalogItem(
	ctx context.Context, merchantID string, request merchantCatalogItemRequest,
	idempotencyKey, fingerprint string,
) (MerchantCatalogItem, error) {
	var itemID string
	err := p.db.QueryRowContext(ctx, `
		SELECT create_merchant_catalog_item($1, $2, $3, $4, $5, $6, $7, $8)`,
		merchantID, request.StoreLocationID, request.Name, request.Description,
		request.UnitPriceCents, request.VolumeLiters, idempotencyKey, fingerprint,
	).Scan(&itemID)
	if err != nil {
		if isCatalogNotFound(err) {
			return MerchantCatalogItem{}, ErrNotFound
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "P0001" {
			return MerchantCatalogItem{}, ErrConflict
		}
		return MerchantCatalogItem{}, err
	}
	return p.merchantCatalogItem(ctx, merchantID, itemID)
}

func (p *PostgresRepository) UpdateMerchantCatalogItem(
	ctx context.Context, merchantID, itemID string, request merchantCatalogItemUpdateRequest,
) (MerchantCatalogItem, error) {
	_, err := p.db.ExecContext(ctx, `
		SELECT update_merchant_catalog_item($1, $2, $3, $4, $5, $6, $7)`,
		merchantID, itemID, request.Name, request.Description,
		request.UnitPriceCents, request.VolumeLiters, request.Available,
	)
	if err != nil {
		if isCatalogNotFound(err) {
			return MerchantCatalogItem{}, ErrNotFound
		}
		return MerchantCatalogItem{}, err
	}
	return p.merchantCatalogItem(ctx, merchantID, itemID)
}

func (p *PostgresRepository) merchantCatalogItem(
	ctx context.Context, merchantID, itemID string,
) (MerchantCatalogItem, error) {
	var item MerchantCatalogItem
	err := p.db.QueryRowContext(ctx, `
		SELECT i.id, i.store_location_id, i.name, i.description,
		       i.unit_price_cents, i.volume_liters::text, i.available
		FROM catalog_items i
		JOIN store_locations s ON s.id = i.store_location_id
		WHERE i.id = $1 AND s.merchant_id = $2`,
		itemID, merchantID,
	).Scan(
		&item.ID, &item.StoreLocationID, &item.Name, &item.Description,
		&item.UnitPriceCents, &item.VolumeLiters, &item.Available,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return MerchantCatalogItem{}, ErrNotFound
	}
	return item, err
}

func isCatalogNotFound(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "P0002"
}

func (p *PostgresRepository) CreateCustomerOrder(
	ctx context.Context, request customerOrderRequest, customerRef, idempotencyKey, fingerprint string,
) (customerOrderResponse, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return customerOrderResponse{}, err
	}
	defer tx.Rollback()
	itemIDs := make([]string, len(request.Items))
	quantities := make([]int, len(request.Items))
	for index, item := range request.Items {
		itemIDs[index], quantities[index] = item.CatalogItemID, item.Quantity
	}
	var orderID string
	err = tx.QueryRowContext(ctx, `
		SELECT create_customer_order(
			$1, $2, $3, $4, $5, $6, $7::uuid[], $8::integer[], $9, $10
		)`,
		customerRef, request.StoreLocationID,
		request.Dropoff.Latitude, request.Dropoff.Longitude,
		request.DeliveryWindowStart, request.DeliveryWindowEnd,
		itemIDs, quantities, idempotencyKey, fingerprint,
	).Scan(&orderID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "P0002":
				return customerOrderResponse{}, ErrNotFound
			case "P0001":
				return customerOrderResponse{}, ErrConflict
			}
		}
		return customerOrderResponse{}, err
	}
	response := customerOrderResponse{ID: orderID}
	if err := tx.QueryRowContext(ctx, `
		SELECT state, item_subtotal_cents FROM orders WHERE id = $1`,
		orderID,
	).Scan(&response.Status, &response.ItemSubtotalCents); err != nil {
		return customerOrderResponse{}, err
	}
	if err := tx.Commit(); err != nil {
		return customerOrderResponse{}, err
	}
	return response, nil
}

func (p *PostgresRepository) CreateBundle(
	ctx context.Context, request bundleRequest, idempotencyKey string,
) (bundleResponse, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return bundleResponse{}, err
	}
	defer tx.Rollback()
	var bundleID string
	err = tx.QueryRowContext(ctx, `
		SELECT create_proximity_bundle($1, $2, $3, $4, $5, $6)`,
		request.SeedOrderID, idempotencyKey, request.CapacityLiters,
		*request.MaxOrders, *request.PickupRadiusMeters, *request.DropoffRadiusMeters,
	).Scan(&bundleID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "P0001" {
			return bundleResponse{}, ErrIneligible
		}
		return bundleResponse{}, err
	}
	response := bundleResponse{ID: bundleID, Status: "active", Stops: make([]bundleStop, 0)}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, order_id, sequence, stop_type,
		       ST_Y(location::geometry), ST_X(location::geometry), status,
		       store_location_id
		FROM delivery_stops WHERE bundle_id = $1 ORDER BY sequence`, bundleID)
	if err != nil {
		return bundleResponse{}, err
	}
	for rows.Next() {
		var stop bundleStop
		var storeID sql.NullString
		if err := rows.Scan(
			&stop.ID, &stop.OrderID, &stop.Sequence, &stop.Type,
			&stop.Latitude, &stop.Longitude, &stop.Status, &storeID,
		); err != nil {
			rows.Close()
			return bundleResponse{}, err
		}
		if storeID.Valid {
			stop.StoreLocationID = &storeID.String
		}
		response.Stops = append(response.Stops, stop)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return bundleResponse{}, err
	}
	rows.Close()
	if err := tx.Commit(); err != nil {
		return bundleResponse{}, err
	}
	return response, nil
}

func (p *PostgresRepository) CreateCheckout(
	ctx context.Context, request checkoutRequest, customerRef, idempotencyKey string,
) (checkoutResponse, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return checkoutResponse{}, err
	}
	defer tx.Rollback()
	var checkoutID string
	err = tx.QueryRowContext(ctx, `
		SELECT create_unified_checkout(
			$1, $2, $3::uuid[], $4, $5, $6, $7, $8, $9, $10
		)`,
		customerRef, request.AnchorStoreLocationID, request.OrderIDs,
		request.Dropoff.Latitude, request.Dropoff.Longitude,
		request.DeliveryWindowStart, request.DeliveryWindowEnd,
		p.deliveryFeeCents, p.serviceFeeCents, idempotencyKey,
	).Scan(&checkoutID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "P0001" {
			return checkoutResponse{}, ErrConflict
		}
		return checkoutResponse{}, err
	}

	response := checkoutResponse{ID: checkoutID, InvoiceLines: make([]checkoutInvoiceLine, 0)}
	if err := tx.QueryRowContext(ctx, `
		SELECT status, currency FROM unified_checkouts WHERE id = $1`, checkoutID,
	).Scan(&response.Status, &response.Currency); err != nil {
		return checkoutResponse{}, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT line_type, merchant_id, amount_cents
		FROM checkout_invoice_lines
		WHERE checkout_id = $1
		ORDER BY line_type, merchant_id NULLS FIRST`, checkoutID)
	if err != nil {
		return checkoutResponse{}, err
	}
	for rows.Next() {
		var line checkoutInvoiceLine
		var merchantID sql.NullString
		if err := rows.Scan(&line.Type, &merchantID, &line.AmountCents); err != nil {
			rows.Close()
			return checkoutResponse{}, err
		}
		if merchantID.Valid {
			line.MerchantID = merchantID.String
		}
		response.TotalCents += line.AmountCents
		response.InvoiceLines = append(response.InvoiceLines, line)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return checkoutResponse{}, err
	}
	rows.Close()
	if err := tx.Commit(); err != nil {
		return checkoutResponse{}, err
	}
	return response, nil
}

func (p *PostgresRepository) FindUnifiedRoute(
	ctx context.Context, checkoutID, idempotencyKey string,
) (string, bool, error) {
	var routeID, routeCheckoutID string
	err := p.db.QueryRowContext(ctx, `
		SELECT id, checkout_id
		FROM unified_delivery_routes
		WHERE idempotency_key = $1`, idempotencyKey,
	).Scan(&routeID, &routeCheckoutID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if routeCheckoutID != checkoutID {
		return "", false, ErrConflict
	}
	return routeID, true, nil
}

func (p *PostgresRepository) UnifiedRouteCoordinates(
	ctx context.Context, checkoutID string,
) ([]routing.Coordinate, error) {
	var status string
	err := p.db.QueryRowContext(ctx,
		`SELECT status FROM unified_checkouts WHERE id = $1`, checkoutID,
	).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if status != "paid" {
		return nil, ErrConflict
	}

	rows, err := p.db.QueryContext(ctx, `
		WITH checkout_data AS (
			SELECT c.id, c.dropoff_location, a.id AS anchor_store_id,
			       a.merchant_id AS anchor_merchant_id, a.location AS anchor_location
			FROM unified_checkouts c
			JOIN store_locations a ON a.id = c.anchor_store_location_id AND a.active
			JOIN merchants am ON am.id = a.merchant_id
				AND am.active AND am.merchant_type = 'restaurant'
			WHERE c.id = $1 AND c.status = 'paid'
			  AND EXISTS (
				SELECT 1
				FROM checkout_orders co
				JOIN orders o ON o.id = co.order_id
				JOIN store_locations anchor_order_store
					ON anchor_order_store.id = o.store_location_id
				WHERE co.checkout_id = c.id
				  AND anchor_order_store.merchant_id = a.merchant_id
			  )
		),
		partner_info AS (
			SELECT count(DISTINCT s.merchant_id) AS merchant_count
			FROM checkout_data c
			JOIN checkout_orders co ON co.checkout_id = c.id
			JOIN orders o ON o.id = co.order_id
			JOIN store_locations s ON s.id = o.store_location_id AND s.active
			WHERE s.merchant_id <> c.anchor_merchant_id
		),
		partner_store AS (
			SELECT s.location
			FROM checkout_data c
			JOIN checkout_orders co ON co.checkout_id = c.id
			JOIN orders o ON o.id = co.order_id
			JOIN store_locations s ON s.id = o.store_location_id AND s.active
			WHERE s.merchant_id <> c.anchor_merchant_id
			ORDER BY s.id
			LIMIT 1
		),
		stop_coordinates AS (
			SELECT 1 AS sequence, ST_Y(anchor_location::geometry) AS latitude,
			       ST_X(anchor_location::geometry) AS longitude
			FROM checkout_data, partner_info WHERE merchant_count = 1
			UNION ALL
			SELECT 2, ST_Y(location::geometry), ST_X(location::geometry)
			FROM partner_store, partner_info WHERE merchant_count = 1
			UNION ALL
			SELECT 3, ST_Y(dropoff_location::geometry), ST_X(dropoff_location::geometry)
			FROM checkout_data, partner_info WHERE merchant_count = 1
		)
		SELECT latitude, longitude
		FROM stop_coordinates
		ORDER BY sequence`, checkoutID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	coordinates := make([]routing.Coordinate, 0, 3)
	for rows.Next() {
		var coordinate routing.Coordinate
		if err := rows.Scan(&coordinate.Latitude, &coordinate.Longitude); err != nil {
			return nil, err
		}
		coordinates = append(coordinates, coordinate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(coordinates) != 3 {
		return nil, ErrConflict
	}
	return coordinates, nil
}

func (p *PostgresRepository) CreateUnifiedRoute(
	ctx context.Context, checkoutID, idempotencyKey string, legs []routing.Route,
) (string, error) {
	if len(legs) != 2 {
		return "", ErrConflict
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var routeID string
	err = tx.QueryRowContext(ctx,
		`SELECT create_unified_route($1, $2, 'osrm')`,
		checkoutID, idempotencyKey,
	).Scan(&routeID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "P0001" {
			return "", ErrConflict
		}
		return "", err
	}
	var routeCheckoutID string
	if err := tx.QueryRowContext(ctx,
		`SELECT checkout_id FROM unified_delivery_routes WHERE id = $1 FOR UPDATE`,
		routeID,
	).Scan(&routeCheckoutID); err != nil {
		return "", err
	}
	if routeCheckoutID != checkoutID {
		return "", ErrConflict
	}
	for i, leg := range legs {
		if !validPersistedRouteMeasurement(leg.DistanceMeters) ||
			!validPersistedRouteMeasurement(leg.DurationSeconds) ||
			len(leg.Coordinates) < 2 {
			return "", ErrConflict
		}
		geometry, err := json.Marshal(leg.Coordinates)
		if err != nil {
			return "", err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO unified_route_legs (
				route_id, from_sequence, to_sequence, distance_meters,
				duration_seconds, provider, geometry
			) VALUES ($1, $2, $3, $4, $5, 'osrm', $6::jsonb)
			ON CONFLICT (route_id, from_sequence) DO NOTHING`,
			routeID, i+1, i+2,
			int(math.Round(leg.DistanceMeters)), int(math.Round(leg.DurationSeconds)),
			string(geometry),
		)
		if err != nil {
			return "", err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE unified_delivery_routes SET status = 'routed'
		WHERE id = $1 AND status = 'planned'`, routeID); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return routeID, nil
}

func (p *PostgresRepository) DriverReroutePlan(
	ctx context.Context, routeID, driverRef string,
) (driverReroutePlan, error) {
	var exists bool
	if err := p.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM unified_delivery_routes WHERE id = $1)`, routeID,
	).Scan(&exists); err != nil {
		return driverReroutePlan{}, err
	}
	if !exists {
		return driverReroutePlan{}, ErrNotFound
	}
	if err := p.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM unified_delivery_routes
			WHERE id = $1 AND driver_ref = $2
		)`, routeID, driverRef,
	).Scan(&exists); err != nil {
		return driverReroutePlan{}, err
	}
	if !exists {
		return driverReroutePlan{}, ErrForbidden
	}

	var plan driverReroutePlan
	err := p.db.QueryRowContext(ctx, `
		SELECT ST_Y(sample.location::geometry), ST_X(sample.location::geometry),
		       sample.id, stop.sequence,
		       ST_Y(stop.location::geometry), ST_X(stop.location::geometry),
		       leg.distance_meters / 1609.344
		FROM unified_delivery_routes route
		JOIN LATERAL (
			SELECT id, sequence, status, location
			FROM unified_route_stops
			WHERE route_id = route.id AND status <> 'completed'
			ORDER BY sequence LIMIT 1
		) stop ON true
		JOIN unified_route_legs leg
		  ON leg.route_id = route.id AND leg.to_sequence = stop.sequence
		 AND leg.from_sequence = stop.sequence - 1
		JOIN LATERAL (
			SELECT id, location, observed_at
			FROM driver_location_samples
			WHERE route_id = route.id AND driver_ref = route.driver_ref
			  AND observed_at >= now() - interval '60 seconds'
			  AND observed_at <= now() + interval '1 minute'
			ORDER BY observed_at DESC, id DESC LIMIT 1
		) sample ON true
		WHERE route.id = $1 AND route.driver_ref = $2
		  AND route.status = 'in_progress'
		  AND stop.status = 'pending' AND stop.sequence > 1`,
		routeID, driverRef,
	).Scan(
		&plan.Current.Latitude, &plan.Current.Longitude, &plan.SampleID,
		&plan.ToSequence, &plan.Target.Latitude, &plan.Target.Longitude,
		&plan.PlannedDistanceMiles,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return driverReroutePlan{}, ErrConflict
	}
	if err != nil {
		return driverReroutePlan{}, err
	}
	return plan, nil
}

func (p *PostgresRepository) SaveDriverReroute(
	ctx context.Context, routeID, driverRef string, plan driverReroutePlan,
	idempotencyKey string, deviationMiles float64, route routing.Route,
	coordinates []dispatch.LocationCoordinate,
) error {
	geometry, err := json.Marshal(coordinates)
	if err != nil {
		return err
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var assignedDriver, routeStatus string
	err = tx.QueryRowContext(ctx, `
		SELECT driver_ref, status
		FROM unified_delivery_routes WHERE id = $1 FOR UPDATE`, routeID,
	).Scan(&assignedDriver, &routeStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if assignedDriver != driverRef {
		return ErrForbidden
	}
	var existingRouteID, existingDriverRef string
	var existingSampleID int64
	err = tx.QueryRowContext(ctx, `
		SELECT route_id, driver_ref, sample_id
		FROM driver_route_reroutes WHERE idempotency_key = $1`,
		idempotencyKey,
	).Scan(&existingRouteID, &existingDriverRef, &existingSampleID)
	if err == nil {
		if existingRouteID == routeID && existingDriverRef == driverRef &&
			existingSampleID == plan.SampleID {
			return nil
		}
		return ErrConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if routeStatus != "in_progress" {
		return ErrConflict
	}
	var latestSampleID int64
	err = tx.QueryRowContext(ctx, `
		SELECT id FROM driver_location_samples
		WHERE route_id = $1 AND driver_ref = $2
		  AND observed_at >= now() - interval '60 seconds'
		  AND observed_at <= now() + interval '1 minute'
		ORDER BY observed_at DESC, id DESC LIMIT 1`,
		routeID, driverRef,
	).Scan(&latestSampleID)
	if err != nil || latestSampleID != plan.SampleID {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return ErrConflict
	}
	var activeSequence int
	var activeStatus string
	err = tx.QueryRowContext(ctx, `
		SELECT sequence, status
		FROM unified_route_stops
		WHERE route_id = $1 AND status <> 'completed'
		ORDER BY sequence LIMIT 1`,
		routeID,
	).Scan(&activeSequence, &activeStatus)
	if err != nil || activeSequence != plan.ToSequence || activeStatus != "pending" {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return ErrConflict
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE unified_route_legs
		SET distance_meters = $3, duration_seconds = $4, provider = 'osrm',
		    geometry = $5::jsonb
		WHERE route_id = $1 AND from_sequence = $2`,
		routeID, plan.ToSequence-1,
		int(math.Round(route.DistanceMeters)), int(math.Round(route.DurationSeconds)),
		string(geometry),
	)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return ErrConflict
	}
	var rerouteID string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO driver_route_reroutes (
			route_id, driver_ref, sample_id, to_sequence, idempotency_key,
			driver_location, deviation_miles, distance_meters, duration_seconds, geometry
		)
		SELECT $1, $2, sample.id, $4, $5, sample.location,
		       $6, $7, $8, $9::jsonb
		FROM driver_location_samples sample
		WHERE sample.id = $3 AND sample.route_id = $1 AND sample.driver_ref = $2
		RETURNING id`,
		routeID, driverRef, plan.SampleID, plan.ToSequence, idempotencyKey,
		deviationMiles, int(math.Round(route.DistanceMeters)),
		int(math.Round(route.DurationSeconds)), string(geometry),
	).Scan(&rerouteID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrConflict
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
		VALUES ('unified_delivery_route', $1, 'delivery.route.rerouted',
		        jsonb_build_object(
		          'routeId', $1, 'rerouteId', $2, 'toSequence', $3,
		          'distanceMeters', $4, 'durationSeconds', $5
		        ))`,
		routeID, rerouteID, plan.ToSequence,
		int(math.Round(route.DistanceMeters)), int(math.Round(route.DurationSeconds)),
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *PostgresRepository) ShopperOrderTracking(
	ctx context.Context, orderID, customerRef string,
) (ShopperTrackingResponse, error) {
	var response ShopperTrackingResponse
	var latitude, longitude sql.NullFloat64
	var observedAt sql.NullTime
	var etaMinutes sql.NullInt64
	var geometry sql.NullString
	err := p.db.QueryRowContext(ctx, `
		SELECT COALESCE(route.status, 'awaiting_route'),
		       ST_Y(sample.location::geometry), ST_X(sample.location::geometry),
		       sample.observed_at,
		       CASE WHEN route.status = 'completed' THEN 0
		            WHEN route.id IS NULL THEN NULL
		            ELSE ceil(COALESCE(sum(leg.duration_seconds), 0) / 60.0)::bigint
		       END,
		       COALESCE(reroute.geometry, planned_path.geometry)::text
		FROM orders order_record
		LEFT JOIN checkout_orders co ON co.order_id = order_record.id
		LEFT JOIN unified_delivery_routes route ON route.checkout_id = co.checkout_id
		LEFT JOIN LATERAL (
			SELECT location, observed_at
			FROM driver_location_samples
			WHERE route_id = route.id AND route.status = 'in_progress'
			  AND observed_at >= now() - interval '60 seconds'
			  AND observed_at <= now() + interval '1 minute'
			ORDER BY observed_at DESC, id DESC LIMIT 1
		) sample ON true
		LEFT JOIN LATERAL (
			SELECT COALESCE(max(sequence), 0) AS sequence
			FROM unified_route_stops
			WHERE route_id = route.id AND status = 'completed'
		) completed ON true
		LEFT JOIN unified_route_legs leg
		  ON leg.route_id = route.id AND leg.from_sequence >= completed.sequence
		LEFT JOIN LATERAL (
			SELECT geometry
			FROM driver_route_reroutes
			WHERE route_id = route.id AND to_sequence > completed.sequence
			ORDER BY created_at DESC LIMIT 1
		) reroute ON true
		LEFT JOIN LATERAL (
			SELECT geometry
			FROM unified_route_legs
			WHERE route_id = route.id AND from_sequence >= completed.sequence
			ORDER BY from_sequence LIMIT 1
		) planned_path ON true
		WHERE order_record.id = $1 AND order_record.customer_ref = $2
		GROUP BY route.id, route.status, sample.location, sample.observed_at,
		         reroute.geometry, planned_path.geometry`,
		orderID, customerRef,
	).Scan(
		&response.Status, &latitude, &longitude, &observedAt,
		&etaMinutes, &geometry,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ShopperTrackingResponse{}, ErrNotFound
	}
	if err != nil {
		return ShopperTrackingResponse{}, err
	}
	if latitude.Valid && longitude.Valid {
		response.DriverLocation = &geoPoint{Latitude: latitude.Float64, Longitude: longitude.Float64}
	}
	if observedAt.Valid {
		response.LastUpdatedAt = &observedAt.Time
	}
	if etaMinutes.Valid {
		value := int(etaMinutes.Int64)
		response.ETAMinutes = &value
	}
	if geometry.Valid {
		if err := json.Unmarshal([]byte(geometry.String), &response.RouteCoordinates); err != nil {
			return ShopperTrackingResponse{}, err
		}
	}
	if response.RouteCoordinates == nil {
		response.RouteCoordinates = []dispatch.LocationCoordinate{}
	}
	return response, nil
}

func (p *PostgresRepository) DriverRoute(
	ctx context.Context, routeID, driverRef string,
) (routeResponse, error) {
	var exists bool
	if err := p.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM unified_delivery_routes WHERE id = $1)`,
		routeID,
	).Scan(&exists); err != nil {
		return routeResponse{}, err
	}
	if !exists {
		return routeResponse{}, ErrNotFound
	}
	var assigned bool
	if err := p.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM unified_delivery_routes
			WHERE id = $1 AND driver_ref = $2
		)`, routeID, driverRef,
	).Scan(&assigned); err != nil {
		return routeResponse{}, err
	}
	if !assigned {
		return routeResponse{}, ErrForbidden
	}

	var response routeResponse
	err := p.db.QueryRowContext(ctx, `
		SELECT id, checkout_id, status, routing_provider
		FROM unified_delivery_routes WHERE id = $1`,
		routeID,
	).Scan(&response.ID, &response.Checkout, &response.Status, &response.Provider)
	if err != nil {
		return routeResponse{}, err
	}
	response.Stops = make([]routeStop, 0)
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, sequence, stop_type, ST_Y(location::geometry),
		       ST_X(location::geometry), status, store_location_id
		FROM unified_route_stops WHERE route_id = $1 ORDER BY sequence`,
		routeID,
	)
	if err != nil {
		return routeResponse{}, err
	}
	for rows.Next() {
		var stop routeStop
		var storeID sql.NullString
		if err := rows.Scan(
			&stop.ID, &stop.Sequence, &stop.Type, &stop.Latitude,
			&stop.Longitude, &stop.Status, &storeID,
		); err != nil {
			rows.Close()
			return routeResponse{}, err
		}
		if storeID.Valid {
			stop.StoreLocationID = &storeID.String
		}
		response.Stops = append(response.Stops, stop)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return routeResponse{}, err
	}
	rows.Close()

	response.Legs = make([]routeLeg, 0)
	rows, err = p.db.QueryContext(ctx, `
		SELECT from_sequence, to_sequence, distance_meters, duration_seconds, provider
		FROM unified_route_legs WHERE route_id = $1 ORDER BY from_sequence`,
		routeID,
	)
	if err != nil {
		return routeResponse{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var leg routeLeg
		if err := rows.Scan(
			&leg.FromSequence, &leg.ToSequence, &leg.DistanceMeters,
			&leg.DurationSeconds, &leg.Provider,
		); err != nil {
			return routeResponse{}, err
		}
		response.Legs = append(response.Legs, leg)
	}
	if err := rows.Err(); err != nil {
		return routeResponse{}, err
	}
	return response, nil
}

func (p *PostgresRepository) DriverActiveRoute(
	ctx context.Context, driverRef string,
) (string, error) {
	var routeID string
	err := p.db.QueryRowContext(ctx, `
		SELECT id::text
		FROM unified_delivery_routes
		WHERE driver_ref = $1 AND status IN ('routed', 'in_progress')
		ORDER BY CASE WHEN status = 'in_progress' THEN 0 ELSE 1 END,
		         created_at DESC, id
		LIMIT 1`, driverRef).Scan(&routeID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return routeID, nil
}

func (p *PostgresRepository) AssignUnifiedDriver(
	ctx context.Context, routeID, driverRef, assignedBy string,
) (string, error) {
	var routeStatus string
	err := p.db.QueryRowContext(ctx,
		`SELECT assign_unified_driver($1, $2, $3)`, routeID, driverRef, assignedBy,
	).Scan(&routeStatus)
	if err == nil {
		return routeStatus, nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "P0002":
			return "", ErrNotFound
		case "P0001", "23505":
			return "", ErrConflict
		}
	}
	return "", err
}

func (p *PostgresRepository) RecordDriverLocation(
	ctx context.Context, routeID, driverRef string, request driverLocationRequest,
) (int64, error) {
	var routeStatus string
	err := p.db.QueryRowContext(ctx, `
		SELECT status FROM unified_delivery_routes WHERE id = $1`,
		routeID,
	).Scan(&routeStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	var assigned bool
	if err := p.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM unified_delivery_routes
			WHERE id = $1 AND driver_ref = $2
		)`, routeID, driverRef,
	).Scan(&assigned); err != nil {
		return 0, err
	}
	if !assigned {
		return 0, ErrForbidden
	}
	if routeStatus != "in_progress" {
		return 0, ErrConflict
	}

	var sampleID int64
	err = p.db.QueryRowContext(ctx, `
		SELECT record_driver_location(
			$1, $2, ST_SetSRID(ST_MakePoint($3, $4), 4326)::geography, $5, $6
		)`,
		routeID, driverRef, request.Location.Longitude,
		request.Location.Latitude, request.ObservedAt, request.EventID,
	).Scan(&sampleID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "P0001" {
			return 0, ErrConflict
		}
		return 0, err
	}
	return sampleID, nil
}

func (p *PostgresRepository) RecordDriverStopEvent(
	ctx context.Context, routeID, stopID, driverRef string, request driverStopEventRequest,
) (driverStopEventResponse, error) {
	var response driverStopEventResponse
	response.StopID = stopID
	err := p.db.QueryRowContext(ctx, `
		SELECT stop_status, route_status
		FROM record_unified_route_stop_event($1, $2, $3, $4, $5, $6)`,
		routeID, stopID, driverRef, request.EventID, request.Type, request.ObservedAt,
	).Scan(&response.StopStatus, &response.RouteStatus)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "P0002":
				return driverStopEventResponse{}, ErrNotFound
			case "42501":
				return driverStopEventResponse{}, ErrForbidden
			case "P0001":
				return driverStopEventResponse{}, ErrConflict
			}
		}
		return driverStopEventResponse{}, err
	}
	return response, nil
}

func (p *PostgresRepository) ListDriverFraudAlerts(
	ctx context.Context, status string, limit int,
) ([]DriverFraudAlert, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, route_id, driver_ref, alert_type, severity, status, evidence,
		       detected_at, resolved_at, resolved_by
		FROM driver_fraud_alerts
		WHERE ($1 = '' OR status = $1)
		ORDER BY detected_at DESC, id
		LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	alerts := make([]DriverFraudAlert, 0)
	for rows.Next() {
		var alert DriverFraudAlert
		var resolvedAt sql.NullTime
		var resolvedBy sql.NullString
		if err := rows.Scan(
			&alert.ID, &alert.RouteID, &alert.DriverRef, &alert.AlertType,
			&alert.Severity, &alert.Status, &alert.Evidence, &alert.DetectedAt,
			&resolvedAt, &resolvedBy,
		); err != nil {
			return nil, err
		}
		if resolvedAt.Valid {
			alert.ResolvedAt = &resolvedAt.Time
		}
		if resolvedBy.Valid {
			alert.ResolvedBy = resolvedBy.String
		}
		alerts = append(alerts, alert)
	}
	return alerts, rows.Err()
}

func (p *PostgresRepository) ResolveDriverFraudAlert(
	ctx context.Context, alertID, actor, status string,
) error {
	result, err := p.db.ExecContext(ctx, `
		UPDATE driver_fraud_alerts
		SET status = $2, resolved_at = now(), resolved_by = $3
		WHERE id = $1 AND status = 'open'`, alertID, status, actor)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 1 {
		return nil
	}
	var exists bool
	if err := p.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM driver_fraud_alerts WHERE id = $1)`,
		alertID,
	).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return ErrConflict
}

func (s *Server) databaseError(w http.ResponseWriter, err error) {
	s.log.Error("database request failed", "error", err)
	writeError(w, http.StatusInternalServerError, "request could not be completed")
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func isUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func optionalInt(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

func intPointer(value int) *int {
	return &value
}
