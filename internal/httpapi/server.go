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
	webhookSecret   string
	merchantPriceID string
}

type Repository interface {
	PingContext(context.Context) error
	NearbyMerchants(context.Context, string, int) ([]NearbyMerchant, error)
	CatalogItems(context.Context, string) ([]CatalogItem, error)
	MerchantStoreLocations(context.Context, string) ([]MerchantStoreLocation, error)
	MerchantCatalog(context.Context, string) ([]MerchantCatalogItem, error)
	MerchantOrders(context.Context, string) ([]MerchantOrder, error)
	UpdateMerchantOrderState(context.Context, string, string, string) (string, error)
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
	RecordDriverLocation(context.Context, string, string, driverLocationRequest) (int64, error)
	RecordDriverStopEvent(
		context.Context, string, string, string, driverStopEventRequest,
	) (driverStopEventResponse, error)
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

type MerchantOrderItem struct {
	Name           string `json:"name"`
	Quantity       int    `json:"quantity"`
	UnitPriceCents int    `json:"unitPriceCents"`
}

type MerchantOrder struct {
	ID                  string              `json:"id"`
	StoreLocationID     string              `json:"storeLocationId"`
	StoreName           string              `json:"storeName"`
	Status              string              `json:"status"`
	DeliveryWindowStart time.Time           `json:"deliveryWindowStart"`
	DeliveryWindowEnd   time.Time           `json:"deliveryWindowEnd"`
	Items               []MerchantOrderItem `json:"items"`
}

type merchantOrderStateRequest struct {
	State string `json:"state"`
}

type merchantOrderStateResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
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

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.Handle("GET /stores/{storeLocationId}/nearby-merchants", s.authorized(http.HandlerFunc(s.nearbyMerchants)))
	mux.Handle("GET /stores/{storeLocationId}/catalog", s.authorized(http.HandlerFunc(s.catalogItems)))
	mux.Handle("POST /orders", s.authorized(http.HandlerFunc(s.createCustomerOrder)))
	mux.Handle("GET /merchant/store-locations", s.authorized(http.HandlerFunc(s.merchantStoreLocations)))
	mux.Handle("GET /merchant/catalog", s.authorized(http.HandlerFunc(s.merchantCatalog)))
	mux.Handle("GET /merchant/orders", s.authorized(http.HandlerFunc(s.merchantOrders)))
	mux.Handle("PATCH /merchant/orders/{orderId}", s.authorized(http.HandlerFunc(s.updateMerchantOrderState)))
	mux.Handle("POST /merchant/catalog", s.authorized(http.HandlerFunc(s.createMerchantCatalogItem)))
	mux.Handle("PATCH /merchant/catalog/{itemId}", s.authorized(http.HandlerFunc(s.updateMerchantCatalogItem)))
	mux.Handle("POST /dispatch/bundles", s.authorized(http.HandlerFunc(s.createBundle)))
	mux.Handle("GET /driver/routes/{routeId}", s.authorized(http.HandlerFunc(s.getDriverRoute)))
	mux.Handle("POST /driver/routes/{routeId}/location", s.authorized(http.HandlerFunc(s.recordDriverLocation)))
	mux.Handle("POST /driver/routes/{routeId}/stops/{stopId}/events", s.authorized(http.HandlerFunc(s.recordDriverStopEvent)))
	mux.Handle("POST /checkouts", s.authorized(http.HandlerFunc(s.createCheckout)))
	mux.Handle("POST /checkouts/{checkoutId}/payment", s.authorized(http.HandlerFunc(s.createCheckoutPayment)))
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

func (s *Server) merchantOrders(w http.ResponseWriter, r *http.Request) {
	merchantID, ok := s.authorizedActorRole(w, r, "merchant")
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	orders, err := s.repo.MerchantOrders(ctx, merchantID)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "merchant not found")
	case err != nil:
		s.databaseError(w, err)
	default:
		writeJSON(w, http.StatusOK, orders)
	}
}

func (s *Server) updateMerchantOrderState(w http.ResponseWriter, r *http.Request) {
	merchantID, ok := s.authorizedActorRole(w, r, "merchant")
	if !ok {
		return
	}
	orderID := r.PathValue("orderId")
	if !isUUID(orderID) {
		writeError(w, http.StatusBadRequest, "invalid order ID")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8*1024)
	var request merchantOrderStateRequest
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
	if request.State != "preparing" && request.State != "ready" {
		writeError(w, http.StatusBadRequest, "state must be preparing or ready")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	state, err := s.repo.UpdateMerchantOrderState(ctx, merchantID, orderID, request.State)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "paid order not found for merchant")
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "order state transition is not allowed")
	case err != nil:
		s.databaseError(w, err)
	default:
		writeJSON(w, http.StatusOK, merchantOrderStateResponse{ID: orderID, Status: state})
	}
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
	customerRef := strings.TrimSpace(r.Header.Get("X-ShopStops-Actor"))
	if customerRef == "" || len(customerRef) > 200 {
		writeError(w, http.StatusBadRequest, "authenticated customer identity is required")
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
	customerRef := strings.TrimSpace(r.Header.Get("X-ShopStops-Actor"))
	if customerRef == "" || len(customerRef) > 200 {
		writeError(w, http.StatusBadRequest, "authenticated customer identity is required")
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

func (s *Server) getDriverRoute(w http.ResponseWriter, r *http.Request) {
	routeID := r.PathValue("routeId")
	driverRef := strings.TrimSpace(r.Header.Get("X-ShopStops-Actor"))
	if !isUUID(routeID) || driverRef == "" || len(driverRef) > 200 {
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

func (s *Server) recordDriverLocation(w http.ResponseWriter, r *http.Request) {
	routeID := r.PathValue("routeId")
	driverRef := strings.TrimSpace(r.Header.Get("X-ShopStops-Actor"))
	if !isUUID(routeID) || driverRef == "" || len(driverRef) > 200 {
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
	driverRef := strings.TrimSpace(r.Header.Get("X-ShopStops-Actor"))
	if !isUUID(routeID) || !isUUID(stopID) || driverRef == "" || len(driverRef) > 200 {
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

func (p *PostgresRepository) MerchantOrders(
	ctx context.Context, merchantID string,
) ([]MerchantOrder, error) {
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
		SELECT o.id, s.id, s.name, o.state,
		       o.delivery_window_start, o.delivery_window_end,
		       COALESCE(
		           jsonb_agg(jsonb_build_object(
		               'name', li.item_name,
		               'quantity', li.quantity,
		               'unitPriceCents', li.unit_price_cents
		           ) ORDER BY li.id) FILTER (WHERE li.id IS NOT NULL),
		           '[]'::jsonb
		       )
		FROM orders o
		JOIN store_locations s ON s.id = o.store_location_id
		JOIN checkout_orders co ON co.order_id = o.id
		JOIN unified_checkouts c ON c.id = co.checkout_id AND c.status = 'paid'
		LEFT JOIN order_line_items li ON li.order_id = o.id
		WHERE s.merchant_id = $1
		  AND o.state IN ('placed', 'preparing', 'ready')
		GROUP BY o.id, s.id, s.name
		ORDER BY o.created_at, o.id
		LIMIT 100`, merchantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	orders := make([]MerchantOrder, 0)
	for rows.Next() {
		var order MerchantOrder
		var itemsJSON []byte
		if err := rows.Scan(
			&order.ID, &order.StoreLocationID, &order.StoreName, &order.Status,
			&order.DeliveryWindowStart, &order.DeliveryWindowEnd, &itemsJSON,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(itemsJSON, &order.Items); err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return orders, nil
}

func (p *PostgresRepository) UpdateMerchantOrderState(
	ctx context.Context, merchantID, orderID, state string,
) (string, error) {
	var updatedState string
	err := p.db.QueryRowContext(ctx, `
		SELECT set_merchant_order_state($1, $2, $3)`,
		merchantID, orderID, state,
	).Scan(&updatedState)
	if err != nil {
		if isCatalogNotFound(err) {
			return "", ErrNotFound
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "P0001" {
			return "", ErrConflict
		}
		return "", err
	}
	return updatedState, nil
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
