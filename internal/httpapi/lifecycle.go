package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stripe/stripe-go/v82"
)

type MerchantBillingProfile struct {
	MerchantID      string
	Name            string
	CustomerID      string
	PaymentMethodID string
	SubscriptionID  string
	Status          string
	MonthlyCents    int
	Visible         bool
	PeriodEnd       sql.NullTime
	PaymentFailedAt sql.NullTime
}

type RefundRecord struct {
	ID              string
	CheckoutID      string
	PaymentIntentID string
	IdempotencyKey  string
	AmountCents     int64
	Reason          string
	StripeRefundID  string
	Status          string
}

type TransferReversal struct {
	ID             string
	RefundID       string
	DisputeID      string
	CheckoutID     string
	MerchantID     string
	TransferID     string
	IdempotencyKey string
	AmountCents    int64
}

type AdminCheckoutRecord struct {
	ID                 string          `json:"id"`
	Status             string          `json:"status"`
	Currency           string          `json:"currency"`
	TotalAmountCents   int64           `json:"totalAmountCents"`
	PaymentStatus      string          `json:"paymentStatus"`
	PaymentAmountCents int64           `json:"paymentAmountCents"`
	CreatedAt          time.Time       `json:"createdAt"`
	Refunds            json.RawMessage `json:"refunds"`
	Disputes           json.RawMessage `json:"disputes"`
}

type AdminCheckoutRepository interface {
	SearchAdminCheckouts(
		context.Context, string, string, string, int,
	) ([]AdminCheckoutRecord, error)
}

type DeliveryZone struct {
	ID                            string          `json:"id"`
	Name                          string          `json:"name"`
	Enabled                       bool            `json:"enabled"`
	Boundary                      json.RawMessage `json:"boundary"`
	MaxOrders                     int             `json:"maxOrders"`
	PickupRadiusMeters            int             `json:"pickupRadiusMeters"`
	DropoffRadiusMeters           int             `json:"dropoffRadiusMeters"`
	DriverMileageCostPerMileCents int             `json:"driverMileageCostPerMileCents"`
	DispatchLimitsUpdatedBy       string          `json:"dispatchLimitsUpdatedBy"`
	DispatchLimitsUpdatedAt       time.Time       `json:"dispatchLimitsUpdatedAt"`
}

type AdminDeliveryRepository interface {
	AdminDeliveryZones(context.Context) ([]DeliveryZone, error)
	UpdateAdminDeliveryZone(context.Context, string, bool) (DeliveryZone, error)
	UpdateAdminDispatchLimits(context.Context, string, dispatchLimitsRequest, string) (DeliveryZone, error)
}

type DeliveryZoneOperations struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	Enabled            bool       `json:"enabled"`
	ReadyOrderCount    int64      `json:"readyOrderCount"`
	OldestReadyAt      *time.Time `json:"oldestReadyAt"`
	OldestReadyOrderID *string    `json:"oldestReadyOrderId"`
	ActiveBundleCount  int64      `json:"activeBundleCount"`
	OpenStopCount      int64      `json:"openStopCount"`
}

type AdminOperationsSnapshot struct {
	GeneratedAt            time.Time                `json:"generatedAt"`
	PendingOutboxEvents    int64                    `json:"pendingOutboxEvents"`
	OldestPendingOutboxAt  *time.Time               `json:"oldestPendingOutboxAt"`
	DeliveryZoneOperations []DeliveryZoneOperations `json:"deliveryZones"`
}

type AdminOperationsRepository interface {
	AdminOperationsSnapshot(context.Context) (AdminOperationsSnapshot, error)
	AdminDispatchRoutes(context.Context) ([]AdminDispatchRoute, error)
}

type AdminDispatchRoute struct {
	ID               string     `json:"id"`
	DriverRef        string     `json:"driverRef"`
	DeliveryZoneName string     `json:"deliveryZoneName"`
	Status           string     `json:"status"`
	OpenStopCount    int64      `json:"openStopCount"`
	LastLocationAt   *time.Time `json:"lastLocationAt"`
}

type dispatchLimitsRequest struct {
	MaxOrders                     *int `json:"maxOrders"`
	PickupRadiusMeters            *int `json:"pickupRadiusMeters"`
	DropoffRadiusMeters           *int `json:"dropoffRadiusMeters"`
	DriverMileageCostPerMileCents *int `json:"driverMileageCostPerMileCents"`
}

type LifecycleRepository interface {
	MerchantBillingProfile(context.Context, string) (MerchantBillingProfile, error)
	ReserveMerchantBillingAttempt(context.Context, string, string) (MerchantBillingProfile, error)
	RecordMerchantStripeCustomer(context.Context, string, string) error
	RecordMerchantStripeSubscription(context.Context, string, string, string) error
	ApplyMerchantBillingEvent(context.Context, StripePaymentEvent, json.RawMessage) error
	ReserveCheckoutRefund(context.Context, string, string, string, string) (RefundRecord, error)
	RecordStripeRefund(context.Context, string, string, string) error
	ApplyCheckoutRefundEvent(context.Context, StripePaymentEvent, json.RawMessage) error
	ApplyStripeDisputeEvent(context.Context, StripePaymentEvent, json.RawMessage) error
	PendingTransferReversals(context.Context, string, string) ([]TransferReversal, error)
	BeginTransferReversalAttempt(context.Context, string) error
	RecordTransferReversal(context.Context, string, string) error
}

type StripeLifecycleGateway interface {
	CreateMerchantCustomer(context.Context, string, string, string) (string, error)
	CreateMerchantSetupIntent(context.Context, string, string, string) (string, string, error)
	SetMerchantDefaultPaymentMethod(context.Context, string, string) error
	CreateMerchantSubscription(context.Context, string, string, string, string) (string, string, error)
	CreateCheckoutRefund(context.Context, RefundRecord) (string, string, error)
	CreateTransferReversal(context.Context, TransferReversal) (string, error)
}

func (g *StripeGateway) CreateMerchantCustomer(
	ctx context.Context, merchantID, name, idempotencyKey string,
) (string, error) {
	params := &stripe.CustomerCreateParams{
		Name:        stripe.String(name),
		Description: stripe.String("ShopStops merchant subscription"),
		Metadata: map[string]string{
			"shopstops_merchant_id": merchantID,
		},
	}
	params.SetIdempotencyKey(idempotencyKey)
	customer, err := g.client.V1Customers.Create(ctx, params)
	if err != nil {
		return "", err
	}
	return customer.ID, nil
}

func (g *StripeGateway) CreateMerchantSetupIntent(
	ctx context.Context, merchantID, customerID, idempotencyKey string,
) (string, string, error) {
	params := &stripe.SetupIntentCreateParams{
		Customer: stripe.String(customerID),
		Usage:    stripe.String("off_session"),
		AutomaticPaymentMethods: &stripe.SetupIntentCreateAutomaticPaymentMethodsParams{
			Enabled: stripe.Bool(true),
		},
		Metadata: map[string]string{
			"shopstops_merchant_id": merchantID,
		},
	}
	params.SetIdempotencyKey(idempotencyKey)
	intent, err := g.client.V1SetupIntents.Create(ctx, params)
	if err != nil {
		return "", "", err
	}
	return intent.ID, intent.ClientSecret, nil
}

func (g *StripeGateway) SetMerchantDefaultPaymentMethod(
	ctx context.Context, customerID, paymentMethodID string,
) error {
	_, err := g.client.V1Customers.Update(ctx, customerID, &stripe.CustomerUpdateParams{
		InvoiceSettings: &stripe.CustomerUpdateInvoiceSettingsParams{
			DefaultPaymentMethod: stripe.String(paymentMethodID),
		},
	})
	return err
}

func (g *StripeGateway) CreateMerchantSubscription(
	ctx context.Context, merchantID, customerID, paymentMethodID, priceID string,
) (string, string, error) {
	price, err := g.client.V1Prices.Retrieve(ctx, priceID, &stripe.PriceRetrieveParams{})
	if err != nil {
		return "", "", err
	}
	if price.UnitAmount != 19900 || string(price.Currency) != "usd" ||
		price.Recurring == nil || string(price.Recurring.Interval) != "month" ||
		price.Recurring.IntervalCount != 1 {
		return "", "", errors.New("configured merchant price must be USD 199 monthly")
	}
	now := time.Now().UTC()
	anchor := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC).Unix()
	params := &stripe.SubscriptionCreateParams{
		Customer:             stripe.String(customerID),
		CollectionMethod:     stripe.String("charge_automatically"),
		PaymentBehavior:      stripe.String("default_incomplete"),
		BillingCycleAnchor:   stripe.Int64(anchor),
		ProrationBehavior:    stripe.String("none"),
		DefaultPaymentMethod: stripe.String(paymentMethodID),
		Items: []*stripe.SubscriptionCreateItemParams{{
			Price: stripe.String(priceID), Quantity: stripe.Int64(1),
		}},
		Metadata: map[string]string{
			"shopstops_merchant_id": merchantID,
		},
	}
	params.SetIdempotencyKey("shopstops-merchant-subscription-" + merchantID)
	subscription, err := g.client.V1Subscriptions.Create(ctx, params)
	if err != nil {
		return "", "", err
	}
	return subscription.ID, string(subscription.Status), nil
}

func (g *StripeGateway) CreateCheckoutRefund(
	ctx context.Context, refund RefundRecord,
) (string, string, error) {
	params := &stripe.RefundCreateParams{
		PaymentIntent: stripe.String(refund.PaymentIntentID),
		Amount:        stripe.Int64(refund.AmountCents),
		Reason:        stripe.String(refund.Reason),
		Metadata: map[string]string{
			"shopstops_checkout_id": refund.CheckoutID,
			"shopstops_refund_key":  refund.IdempotencyKey,
		},
	}
	params.SetIdempotencyKey(refund.IdempotencyKey)
	result, err := g.client.V1Refunds.Create(ctx, params)
	if err != nil {
		return "", "", err
	}
	return result.ID, string(result.Status), nil
}

func (g *StripeGateway) CreateTransferReversal(
	ctx context.Context, reversal TransferReversal,
) (string, error) {
	params := &stripe.TransferReversalCreateParams{
		ID:          stripe.String(reversal.TransferID),
		Amount:      stripe.Int64(reversal.AmountCents),
		Description: stripe.String("ShopStops customer refund or dispute"),
		Metadata: map[string]string{
			"shopstops_checkout_id": reversal.CheckoutID,
			"shopstops_merchant_id": reversal.MerchantID,
		},
	}
	params.SetIdempotencyKey(reversal.IdempotencyKey)
	result, err := g.client.V1TransferReversals.Create(ctx, params)
	if err != nil {
		return "", err
	}
	return result.ID, nil
}

func (s *Server) getMerchantSubscription(w http.ResponseWriter, r *http.Request) {
	merchantID, ok := s.authorizedActorRole(w, r, "merchant")
	if !ok {
		return
	}
	repository, ok := s.repo.(LifecycleRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "merchant billing is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	profile, err := repository.MerchantBillingProfile(ctx, merchantID)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "merchant not found")
		return
	}
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"merchantId": profile.MerchantID, "status": profile.Status,
		"monthlyAmountCents": profile.MonthlyCents,
		"currency":           "usd", "storefrontVisible": profile.Visible,
		"currentPeriodEnd":   nullableTime(profile.PeriodEnd),
		"paymentFailedAt":    nullableTime(profile.PaymentFailedAt),
		"paymentMethodReady": profile.PaymentMethodID != "",
	})
}

func (s *Server) createMerchantSetupIntent(w http.ResponseWriter, r *http.Request) {
	merchantID, ok := s.authorizedActorRole(w, r, "merchant")
	if !ok {
		return
	}
	repository, gateway, ok := s.lifecycleServices(w)
	if !ok {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if !validIdempotencyKey(key) {
		writeError(w, http.StatusBadRequest, "valid Idempotency-Key header is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	profile, err := repository.ReserveMerchantBillingAttempt(ctx, merchantID, "customer")
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "merchant not found")
		return
	}
	if errors.Is(err, ErrConflict) {
		writeError(w, http.StatusConflict, "merchant billing requires Stripe reconciliation")
		return
	}
	if err != nil {
		s.databaseError(w, err)
		return
	}
	if profile.CustomerID == "" {
		customerID, err := gateway.CreateMerchantCustomer(
			ctx, merchantID, profile.Name, "shopstops-merchant-customer-"+merchantID,
		)
		if err != nil {
			s.log.Error("Stripe merchant customer creation failed")
			writeError(w, http.StatusBadGateway, "merchant billing setup could not be initialized")
			return
		}
		if err := repository.RecordMerchantStripeCustomer(ctx, merchantID, customerID); err != nil {
			s.databaseError(w, err)
			return
		}
		profile.CustomerID = customerID
	}
	setupID, clientSecret, err := gateway.CreateMerchantSetupIntent(
		ctx, merchantID, profile.CustomerID,
		"shopstops-merchant-setup-"+merchantID+"-"+key,
	)
	if err != nil {
		s.log.Error("Stripe merchant SetupIntent creation failed")
		writeError(w, http.StatusBadGateway, "merchant billing setup could not be initialized")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"setupIntentId": setupID, "clientSecret": clientSecret,
	})
}

func (s *Server) createMerchantSubscription(w http.ResponseWriter, r *http.Request) {
	merchantID, ok := s.authorizedActorRole(w, r, "merchant")
	if !ok {
		return
	}
	repository, gateway, ok := s.lifecycleServices(w)
	if !ok {
		return
	}
	if s.merchantPriceID == "" {
		writeError(w, http.StatusServiceUnavailable, "merchant subscription price is not configured")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	profile, err := repository.ReserveMerchantBillingAttempt(ctx, merchantID, "subscription")
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "merchant not found")
		return
	}
	if errors.Is(err, ErrConflict) {
		writeError(w, http.StatusConflict, "save a billing method or reconcile the existing subscription first")
		return
	}
	if err != nil {
		s.databaseError(w, err)
		return
	}
	if profile.SubscriptionID == "" {
		subscriptionID, _, err := gateway.CreateMerchantSubscription(
			ctx, merchantID, profile.CustomerID, profile.PaymentMethodID, s.merchantPriceID,
		)
		if err != nil {
			s.log.Error("Stripe merchant subscription creation failed")
			writeError(w, http.StatusBadGateway, "merchant subscription could not be created")
			return
		}
		if err := repository.RecordMerchantStripeSubscription(
			ctx, merchantID, subscriptionID, "incomplete",
		); err != nil {
			s.databaseError(w, err)
			return
		}
		profile.SubscriptionID, profile.Status = subscriptionID, "incomplete"
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"subscriptionId": profile.SubscriptionID,
		"status":         profile.Status, "monthlyAmountCents": 19900,
		"currency": "usd", "billingStartsOn": nextBillingDate(time.Now()),
	})
}

func (s *Server) refundCheckout(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorizedActorRole(w, r, "admin")
	if !ok {
		return
	}
	checkoutID := r.PathValue("checkoutId")
	if !isUUID(checkoutID) {
		writeError(w, http.StatusBadRequest, "invalid checkoutId")
		return
	}
	repository, gateway, ok := s.lifecycleServices(w)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8*1024)
	var request struct {
		Reason string `json:"reason"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid refund request")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return
	}
	if request.Reason != "duplicate" && request.Reason != "fraudulent" &&
		request.Reason != "requested_by_customer" {
		writeError(w, http.StatusBadRequest, "invalid refund reason")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if !validIdempotencyKey(key) {
		writeError(w, http.StatusBadRequest, "valid Idempotency-Key header is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	refund, err := repository.ReserveCheckoutRefund(ctx, checkoutID, actor, key, request.Reason)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "checkout not found")
		return
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "checkout cannot be refunded or refund requires reconciliation")
		return
	case err != nil:
		s.databaseError(w, err)
		return
	}
	if refund.StripeRefundID == "" {
		refundID, status, err := gateway.CreateCheckoutRefund(ctx, refund)
		if err != nil {
			s.log.Error("Stripe checkout refund request failed")
			writeError(w, http.StatusBadGateway, "refund could not be created")
			return
		}
		if err := repository.RecordStripeRefund(ctx, refund.ID, refundID, status); err != nil {
			s.databaseError(w, err)
			return
		}
		refund.StripeRefundID, refund.Status = refundID, status
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"refundId": refund.StripeRefundID, "status": refund.Status,
		"amountCents": refund.AmountCents, "currency": "usd",
	})
}

func (s *Server) searchAdminCheckouts(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizedActorRole(w, r, "admin"); !ok {
		return
	}
	repository, ok := s.repo.(AdminCheckoutRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "admin checkout search is unavailable")
		return
	}

	checkoutID := r.URL.Query().Get("checkoutId")
	if checkoutID != "" && !isUUID(checkoutID) {
		writeError(w, http.StatusBadRequest, "invalid checkoutId")
		return
	}
	status := r.URL.Query().Get("status")
	switch status {
	case "", "building", "quoted", "paid", "dispatching", "in_progress", "completed", "cancelled":
	default:
		writeError(w, http.StatusBadRequest, "invalid checkout status")
		return
	}
	disputeStatus := r.URL.Query().Get("disputeStatus")
	switch disputeStatus {
	case "", "needs_response", "under_review", "won", "lost", "warning_closed":
	default:
		writeError(w, http.StatusBadRequest, "invalid dispute status")
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

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	checkouts, err := repository.SearchAdminCheckouts(ctx, checkoutID, status, disputeStatus, limit)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checkouts": checkouts})
}

func (s *Server) listAdminDeliveryZones(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizedActorRole(w, r, "admin"); !ok {
		return
	}
	repository, ok := s.repo.(AdminDeliveryRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "delivery-zone management is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	zones, err := repository.AdminDeliveryZones(ctx)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliveryZones": zones})
}

func (s *Server) getAdminOperationsSnapshot(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizedActorRole(w, r, "admin"); !ok {
		return
	}
	repository, ok := s.repo.(AdminOperationsRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "operations monitoring is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	snapshot, err := repository.AdminOperationsSnapshot(ctx)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) updateAdminDeliveryZone(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizedActorRole(w, r, "admin"); !ok {
		return
	}
	zoneID := r.PathValue("zoneId")
	if !isUUID(zoneID) {
		writeError(w, http.StatusBadRequest, "invalid delivery zone ID")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8*1024)
	var request struct {
		Enabled *bool `json:"enabled"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.Enabled == nil {
		writeError(w, http.StatusBadRequest, "enabled must be provided")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return
	}
	repository, ok := s.repo.(AdminDeliveryRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "delivery-zone management is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	zone, err := repository.UpdateAdminDeliveryZone(ctx, zoneID, *request.Enabled)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "delivery zone not found")
		return
	}
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, zone)
}

func (s *Server) updateAdminDispatchLimits(w http.ResponseWriter, r *http.Request) {
	adminActor, ok := s.authorizedActorRole(w, r, "admin")
	if !ok {
		return
	}
	zoneID := r.PathValue("zoneId")
	if !isUUID(zoneID) {
		writeError(w, http.StatusBadRequest, "invalid delivery zone ID")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8*1024)
	var request dispatchLimitsRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid dispatch limits")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return
	}
	if request.MaxOrders == nil || request.PickupRadiusMeters == nil ||
		request.DropoffRadiusMeters == nil ||
		*request.MaxOrders < 1 || *request.MaxOrders > 100 ||
		*request.PickupRadiusMeters < 0 || *request.PickupRadiusMeters > 100000 ||
		*request.DropoffRadiusMeters < 0 || *request.DropoffRadiusMeters > 100000 ||
		(request.DriverMileageCostPerMileCents != nil &&
			(*request.DriverMileageCostPerMileCents < 0 ||
				*request.DriverMileageCostPerMileCents > 100000)) {
		writeError(w, http.StatusBadRequest, "zone settings are outside allowed ranges")
		return
	}
	repository, ok := s.repo.(AdminDeliveryRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "delivery-zone management is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	zone, err := repository.UpdateAdminDispatchLimits(ctx, zoneID, request, adminActor)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "delivery zone not found")
		return
	}
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, zone)
}

func (s *Server) authorizedActorRole(
	w http.ResponseWriter, r *http.Request, requiredRole string,
) (string, bool) {
	actor := strings.TrimSpace(r.Header.Get("X-ShopStops-Actor"))
	role := strings.TrimSpace(r.Header.Get("X-ShopStops-Actor-Role"))
	if actor == "" || len(actor) > 200 {
		writeError(w, http.StatusBadRequest, "authenticated actor identity is required")
		return "", false
	}
	if role != requiredRole {
		writeError(w, http.StatusForbidden, "required actor role is missing")
		return "", false
	}
	if requiredRole == "merchant" && !isUUID(actor) {
		writeError(w, http.StatusBadRequest, "valid merchant identity is required")
		return "", false
	}
	return actor, true
}

func (s *Server) lifecycleServices(
	w http.ResponseWriter,
) (LifecycleRepository, StripeLifecycleGateway, bool) {
	repository, repoOK := s.repo.(LifecycleRepository)
	gateway, gatewayOK := s.payments.(StripeLifecycleGateway)
	if !repoOK || !gatewayOK || s.webhookSecret == "" {
		writeError(w, http.StatusServiceUnavailable, "Stripe billing is unavailable")
		return nil, nil, false
	}
	return repository, gateway, true
}

func validIdempotencyKey(key string) bool {
	return len(key) > 0 && len(key) <= 200 && strings.TrimSpace(key) == key
}

func nullableTime(value sql.NullTime) any {
	if !value.Valid {
		return nil
	}
	return value.Time
}

func mapStripeSubscriptionStatus(status string) string {
	switch status {
	case "active":
		return "active"
	case "trialing":
		return "trialing"
	case "past_due", "unpaid":
		return "past_due"
	case "canceled", "incomplete_expired":
		return "cancelled"
	default:
		return "incomplete"
	}
}

func nextBillingDate(now time.Time) string {
	now = now.UTC()
	return time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
}

func (p *PostgresRepository) MerchantBillingProfile(
	ctx context.Context, merchantID string,
) (MerchantBillingProfile, error) {
	var profile MerchantBillingProfile
	err := p.db.QueryRowContext(ctx, `
		SELECT merchant.id, merchant.name, COALESCE(subscription.stripe_customer_id, ''),
		       COALESCE(subscription.stripe_default_payment_method_id, ''),
		       COALESCE(subscription.stripe_subscription_id, ''),
		       COALESCE(subscription.status, 'incomplete'),
		       COALESCE(subscription.monthly_amount_cents, 19900),
		       COALESCE(visibility.visible, false),
		       subscription.current_period_end, subscription.payment_failed_at
		FROM merchants merchant
		LEFT JOIN merchant_subscriptions subscription
		  ON subscription.merchant_id = merchant.id
		LEFT JOIN merchant_storefront_visibility visibility
		  ON visibility.merchant_id = merchant.id
		WHERE merchant.id = $1 AND merchant.active`,
		merchantID,
	).Scan(
		&profile.MerchantID, &profile.Name, &profile.CustomerID,
		&profile.PaymentMethodID, &profile.SubscriptionID, &profile.Status,
		&profile.MonthlyCents, &profile.Visible, &profile.PeriodEnd,
		&profile.PaymentFailedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return MerchantBillingProfile{}, ErrNotFound
	}
	return profile, err
}

func (p *PostgresRepository) ReserveMerchantBillingAttempt(
	ctx context.Context, merchantID, attemptKind string,
) (MerchantBillingProfile, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return MerchantBillingProfile{}, err
	}
	defer tx.Rollback()
	var id, name string
	err = tx.QueryRowContext(ctx, `
		SELECT id, name FROM merchants WHERE id = $1 AND active FOR UPDATE`,
		merchantID,
	).Scan(&id, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return MerchantBillingProfile{}, ErrNotFound
	}
	if err != nil {
		return MerchantBillingProfile{}, err
	}
	var reserved int
	err = tx.QueryRowContext(ctx, `
		SELECT 1 FROM reserve_merchant_billing_attempt($1, $2)`,
		merchantID, attemptKind,
	).Scan(&reserved)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "P0001" {
			return MerchantBillingProfile{}, ErrConflict
		}
		return MerchantBillingProfile{}, err
	}
	var profile MerchantBillingProfile
	err = tx.QueryRowContext(ctx, `
		SELECT merchant.id, merchant.name, COALESCE(subscription.stripe_customer_id, ''),
		       COALESCE(subscription.stripe_default_payment_method_id, ''),
		       COALESCE(subscription.stripe_subscription_id, ''),
		       subscription.status, subscription.monthly_amount_cents,
		       COALESCE(visibility.visible, false), subscription.current_period_end,
		       subscription.payment_failed_at
		FROM merchants merchant
		JOIN merchant_subscriptions subscription ON subscription.merchant_id = merchant.id
		LEFT JOIN merchant_storefront_visibility visibility ON visibility.merchant_id = merchant.id
		WHERE merchant.id = $1`, merchantID,
	).Scan(
		&profile.MerchantID, &profile.Name, &profile.CustomerID,
		&profile.PaymentMethodID, &profile.SubscriptionID, &profile.Status,
		&profile.MonthlyCents, &profile.Visible, &profile.PeriodEnd,
		&profile.PaymentFailedAt,
	)
	if err != nil {
		return MerchantBillingProfile{}, err
	}
	if err := tx.Commit(); err != nil {
		return MerchantBillingProfile{}, err
	}
	return profile, nil
}

func (p *PostgresRepository) RecordMerchantStripeCustomer(
	ctx context.Context, merchantID, customerID string,
) error {
	_, err := p.db.ExecContext(ctx,
		`SELECT record_merchant_stripe_customer($1, $2)`, merchantID, customerID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "P0001" {
			return ErrConflict
		}
	}
	return err
}

func (p *PostgresRepository) RecordMerchantStripeSubscription(
	ctx context.Context, merchantID, subscriptionID, status string,
) error {
	_, err := p.db.ExecContext(ctx,
		`SELECT record_merchant_stripe_subscription($1, $2, $3)`,
		merchantID, subscriptionID, status)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "P0001" {
			return ErrConflict
		}
	}
	return err
}

func (p *PostgresRepository) ApplyMerchantBillingEvent(
	ctx context.Context, event StripePaymentEvent, payload json.RawMessage,
) error {
	_, err := p.db.ExecContext(ctx, `
		SELECT process_merchant_billing_event(
			$1, $2, $3::jsonb, $4, $5, $6, $7, $8, $9, $10
		)`,
		event.ID, event.Type, string(payload), nullableString(event.MerchantID),
		nullableString(event.CustomerID), nullableString(event.SubscriptionID),
		nullableString(event.SubscriptionStatus), nullableTimestamp(event.PeriodEnd),
		event.PaymentStatus == "failed", nullableString(event.PaymentMethodID),
	)
	return err
}

func (p *PostgresRepository) ReserveCheckoutRefund(
	ctx context.Context, checkoutID, actor, key, reason string,
) (RefundRecord, error) {
	var refund RefundRecord
	err := p.db.QueryRowContext(ctx, `
		SELECT id, checkout_id, stripe_payment_intent_id, idempotency_key,
		       amount_cents, reason, COALESCE(stripe_refund_id, ''), status
		FROM reserve_checkout_refund($1, $2, $3, $4)`,
		checkoutID, actor, key, reason,
	).Scan(
		&refund.ID, &refund.CheckoutID, &refund.PaymentIntentID,
		&refund.IdempotencyKey, &refund.AmountCents, &refund.Reason,
		&refund.StripeRefundID, &refund.Status,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "P0001" {
			if strings.Contains(pgErr.Message, "not found") {
				return RefundRecord{}, ErrNotFound
			}
			return RefundRecord{}, ErrConflict
		}
	}
	return refund, err
}

func (p *PostgresRepository) RecordStripeRefund(
	ctx context.Context, refundID, stripeRefundID, status string,
) error {
	_, err := p.db.ExecContext(ctx,
		`SELECT record_checkout_refund($1, $2, $3)`,
		refundID, stripeRefundID, mapStripeRefundStatus(status))
	return err
}

func (p *PostgresRepository) ApplyCheckoutRefundEvent(
	ctx context.Context, event StripePaymentEvent, payload json.RawMessage,
) error {
	_, err := p.db.ExecContext(ctx, `
		SELECT process_checkout_refund_event($1, $2, $3::jsonb, $4, $5)`,
		event.ID, event.Type, string(payload), event.RefundID,
		mapStripeRefundStatus(event.RefundStatus))
	return err
}

func (p *PostgresRepository) ApplyStripeDisputeEvent(
	ctx context.Context, event StripePaymentEvent, payload json.RawMessage,
) error {
	if event.CheckoutID == "" {
		err := p.db.QueryRowContext(ctx, `
			SELECT checkout_id::text FROM checkout_payments
			WHERE stripe_payment_intent_id = $1`, event.PaymentIntentID,
		).Scan(&event.CheckoutID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
	}
	status := mapStripeDisputeStatus(event.DisputeStatus)
	_, err := p.db.ExecContext(ctx, `
		SELECT process_stripe_dispute_event(
			$1, $2, $3::jsonb, $4, $5, $6, $7, $8, $9, $10
		)`,
		event.ID, event.Type, string(payload), event.DisputeID,
		event.CheckoutID, event.PaymentIntentID, event.AmountCents,
		nullableString(event.DisputeReason), status,
		nullableTimestamp(event.EvidenceDueBy),
	)
	return err
}

func (p *PostgresRepository) PendingTransferReversals(
	ctx context.Context, refundID, disputeID string,
) ([]TransferReversal, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, COALESCE(checkout_refund_id::text, ''),
		       COALESCE(stripe_dispute_id, ''), checkout_id, merchant_id,
		       stripe_transfer_id, idempotency_key, amount_cents
		FROM connect_transfer_reversals
		WHERE status IN ('pending', 'failed')
		  AND (($1 <> '' AND checkout_refund_id = (
		          SELECT id FROM checkout_refunds WHERE stripe_refund_id = $1
		      ))
		       OR ($2 <> '' AND stripe_dispute_id = $2))
		ORDER BY merchant_id`, nullableString(refundID), nullableString(disputeID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reversals := make([]TransferReversal, 0)
	for rows.Next() {
		var reversal TransferReversal
		if err := rows.Scan(
			&reversal.ID, &reversal.RefundID, &reversal.DisputeID,
			&reversal.CheckoutID, &reversal.MerchantID, &reversal.TransferID,
			&reversal.IdempotencyKey, &reversal.AmountCents,
		); err != nil {
			return nil, err
		}
		reversals = append(reversals, reversal)
	}
	return reversals, rows.Err()
}

func (p *PostgresRepository) BeginTransferReversalAttempt(
	ctx context.Context, reversalID string,
) error {
	_, err := p.db.ExecContext(ctx,
		`SELECT reserve_connect_reversal_attempt($1)`, reversalID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "P0001" {
			return ErrConflict
		}
	}
	return err
}

func (p *PostgresRepository) RecordTransferReversal(
	ctx context.Context, reversalID, stripeReversalID string,
) error {
	_, err := p.db.ExecContext(ctx, `
		UPDATE connect_transfer_reversals
		SET stripe_reversal_id = $2, status = 'reversed'
		WHERE id = $1 AND (stripe_reversal_id IS NULL OR stripe_reversal_id = $2)`,
		reversalID, stripeReversalID,
	)
	return err
}

func (p *PostgresRepository) SearchAdminCheckouts(
	ctx context.Context, checkoutID, status, disputeStatus string, limit int,
) ([]AdminCheckoutRecord, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT c.id, c.status, c.currency,
		       COALESCE(invoice.total_amount_cents, 0),
		       COALESCE(payment.status, ''),
		       COALESCE(payment.gross_amount_cents, 0),
		       c.created_at,
		       COALESCE(refunds.records, '[]'::jsonb),
		       COALESCE(disputes.records, '[]'::jsonb)
		FROM unified_checkouts c
		LEFT JOIN checkout_payments payment ON payment.checkout_id = c.id
		LEFT JOIN LATERAL (
		    SELECT sum(amount_cents)::bigint AS total_amount_cents
		    FROM checkout_invoice_lines
		    WHERE checkout_id = c.id
		) invoice ON true
		LEFT JOIN LATERAL (
		    SELECT jsonb_agg(jsonb_build_object(
		        'amountCents', amount_cents,
		        'reason', reason,
		        'status', status,
		        'createdAt', created_at
		    ) ORDER BY created_at DESC) AS records
		    FROM checkout_refunds
		    WHERE checkout_id = c.id
		) refunds ON true
		LEFT JOIN LATERAL (
		    SELECT jsonb_agg(jsonb_build_object(
		        'disputeId', stripe_dispute_id,
		        'amountCents', amount_cents,
		        'currency', currency,
		        'reason', reason,
		        'status', status,
		        'evidenceDueBy', evidence_due_by,
		        'updatedAt', updated_at
		    ) ORDER BY updated_at DESC) AS records
		    FROM stripe_disputes
		    WHERE checkout_id = c.id
		) disputes ON true
		WHERE ($1::uuid IS NULL OR c.id = $1::uuid)
		  AND ($2::text IS NULL OR c.status = $2)
		  AND ($3::text IS NULL OR EXISTS (
		      SELECT 1 FROM stripe_disputes d
		      WHERE d.checkout_id = c.id AND d.status = $3
		  ))
		ORDER BY c.created_at DESC, c.id
		LIMIT $4`,
		nullableString(checkoutID), nullableString(status), nullableString(disputeStatus), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	checkouts := make([]AdminCheckoutRecord, 0)
	for rows.Next() {
		var checkout AdminCheckoutRecord
		if err := rows.Scan(
			&checkout.ID, &checkout.Status, &checkout.Currency,
			&checkout.TotalAmountCents, &checkout.PaymentStatus,
			&checkout.PaymentAmountCents, &checkout.CreatedAt,
			&checkout.Refunds, &checkout.Disputes,
		); err != nil {
			return nil, err
		}
		checkouts = append(checkouts, checkout)
	}
	return checkouts, rows.Err()
}

func nullableTimestamp(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func mapStripeRefundStatus(status string) string {
	switch status {
	case "succeeded":
		return "succeeded"
	case "failed":
		return "failed"
	case "canceled":
		return "canceled"
	default:
		return "pending"
	}
}

func mapStripeDisputeStatus(status string) string {
	switch status {
	case "under_review":
		return "under_review"
	case "won":
		return "won"
	case "lost":
		return "lost"
	case "warning_closed":
		return "warning_closed"
	default:
		return "needs_response"
	}
}
