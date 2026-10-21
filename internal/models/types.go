package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strconv"
)

type Decimal string

func (d *Decimal) Scan(src any) error {
	switch value := src.(type) {
	case nil:
		*d = ""
	case string:
		*d = Decimal(value)
	case []byte:
		*d = Decimal(value)
	case int64:
		*d = Decimal(strconv.FormatInt(value, 10))
	default:
		return fmt.Errorf("models.Decimal: cannot scan %T", src)
	}
	return nil
}

func (d Decimal) Value() (driver.Value, error) {
	if d == "" {
		return "0", nil
	}
	return string(d), nil
}

func (Decimal) GormDataType() string { return "numeric" }

type SpatialValue string

func (s *SpatialValue) Scan(src any) error {
	switch value := src.(type) {
	case nil:
		*s = ""
	case string:
		*s = SpatialValue(value)
	case []byte:
		*s = SpatialValue(string(value))
	default:
		return fmt.Errorf("models.SpatialValue: cannot scan %T", src)
	}
	return nil
}

func (s SpatialValue) Value() (driver.Value, error) {
	if s == "" {
		return nil, nil
	}
	return string(s), nil
}

func (SpatialValue) GormDataType() string { return "geometry" }

type JSONB json.RawMessage

func (j *JSONB) Scan(src any) error {
	switch value := src.(type) {
	case nil:
		*j = nil
	case string:
		if !json.Valid([]byte(value)) {
			return fmt.Errorf("models.JSONB: invalid JSON")
		}
		*j = append((*j)[:0], value...)
	case []byte:
		if !json.Valid(value) {
			return fmt.Errorf("models.JSONB: invalid JSON")
		}
		*j = append((*j)[:0], value...)
	default:
		return fmt.Errorf("models.JSONB: cannot scan %T", src)
	}
	return nil
}

func (j JSONB) Value() (driver.Value, error) {
	if len(j) == 0 {
		return "{}", nil
	}
	if !json.Valid(j) {
		return nil, fmt.Errorf("models.JSONB: invalid JSON")
	}
	return string(j), nil
}

func (JSONB) GormDataType() string { return "json" }

type MerchantType string

const (
	MerchantTypeRestaurant MerchantType = "restaurant"
	MerchantTypeRetail     MerchantType = "retail"
	MerchantTypeGrocery    MerchantType = "grocery"
)

type OrderState string

const (
	OrderStatePlaced    OrderState = "placed"
	OrderStatePreparing OrderState = "preparing"
	OrderStateReady     OrderState = "ready"
	OrderStateBundled   OrderState = "bundled"
	OrderStatePickedUp  OrderState = "picked_up"
	OrderStateDelivered OrderState = "delivered"
	OrderStateCancelled OrderState = "cancelled"
)

type DeliveryBundleStatus string

const (
	DeliveryBundleActive     DeliveryBundleStatus = "active"
	DeliveryBundleInProgress DeliveryBundleStatus = "in_progress"
	DeliveryBundleCompleted  DeliveryBundleStatus = "completed"
	DeliveryBundleCancelled  DeliveryBundleStatus = "cancelled"
)

type StopType string

const (
	StopTypePickup  StopType = "pickup"
	StopTypeDropoff StopType = "dropoff"
)

type DeliveryStopStatus string

const (
	DeliveryStopPending   DeliveryStopStatus = "pending"
	DeliveryStopArrived   DeliveryStopStatus = "arrived"
	DeliveryStopCompleted DeliveryStopStatus = "completed"
	DeliveryStopSkipped   DeliveryStopStatus = "skipped"
)

type DeliveryStopEventType string

const (
	DeliveryStopEventArrived   DeliveryStopEventType = "arrived"
	DeliveryStopEventCompleted DeliveryStopEventType = "completed"
	DeliveryStopEventSkipped   DeliveryStopEventType = "skipped"
)

type CheckoutStatus string

const (
	CheckoutBuilding    CheckoutStatus = "building"
	CheckoutQuoted      CheckoutStatus = "quoted"
	CheckoutPaid        CheckoutStatus = "paid"
	CheckoutDispatching CheckoutStatus = "dispatching"
	CheckoutInProgress  CheckoutStatus = "in_progress"
	CheckoutCompleted   CheckoutStatus = "completed"
	CheckoutCancelled   CheckoutStatus = "cancelled"
)

type SubscriptionStatus string

const (
	SubscriptionTrialing   SubscriptionStatus = "trialing"
	SubscriptionActive     SubscriptionStatus = "active"
	SubscriptionPastDue    SubscriptionStatus = "past_due"
	SubscriptionCancelled  SubscriptionStatus = "cancelled"
	SubscriptionIncomplete SubscriptionStatus = "incomplete"
)

type InvoiceLineType string

const (
	InvoiceLineMerchantItems InvoiceLineType = "merchant_items"
	InvoiceLineDeliveryFee   InvoiceLineType = "delivery_fee"
	InvoiceLineServiceFee    InvoiceLineType = "service_fee"
	InvoiceLineDCSurcharge   InvoiceLineType = "dc_surcharge"
)

type TaxLedgerStatus string

const (
	TaxLedgerHeld     TaxLedgerStatus = "held"
	TaxLedgerRemitted TaxLedgerStatus = "remitted"
	TaxLedgerRefunded TaxLedgerStatus = "refunded"
)

type PaymentStatus string

const (
	PaymentRequiresMethod PaymentStatus = "requires_payment_method"
	PaymentProcessing     PaymentStatus = "processing"
	PaymentSucceeded      PaymentStatus = "succeeded"
	PaymentFailed         PaymentStatus = "failed"
	PaymentRefunded       PaymentStatus = "refunded"
)

type TransferStatus string

const (
	TransferPending     TransferStatus = "pending"
	TransferTransferred TransferStatus = "transferred"
	TransferReversed    TransferStatus = "reversed"
	TransferFailed      TransferStatus = "failed"
)

type RouteProvider string

const (
	RouteProviderOSRM       RouteProvider = "osrm"
	RouteProviderGoogleMaps RouteProvider = "google_maps"
	RouteProviderManual     RouteProvider = "manual"
)

type RouteStatus string

const (
	RoutePlanned    RouteStatus = "planned"
	RouteRouted     RouteStatus = "routed"
	RouteInProgress RouteStatus = "in_progress"
	RouteCompleted  RouteStatus = "completed"
	RouteCancelled  RouteStatus = "cancelled"
)

type RouteStopStatus string

const (
	RouteStopPending   RouteStopStatus = "pending"
	RouteStopArrived   RouteStopStatus = "arrived"
	RouteStopCompleted RouteStopStatus = "completed"
)

type RouteStopEventType string

const (
	RouteStopEventArrive   RouteStopEventType = "arrive"
	RouteStopEventComplete RouteStopEventType = "complete"
)

type RouteLegProvider string

const (
	RouteLegProviderOSRM       RouteLegProvider = "osrm"
	RouteLegProviderGoogleMaps RouteLegProvider = "google_maps"
)

type RefundReason string

const (
	RefundReasonDuplicate           RefundReason = "duplicate"
	RefundReasonFraudulent          RefundReason = "fraudulent"
	RefundReasonRequestedByCustomer RefundReason = "requested_by_customer"
)

type RefundStatus string

const (
	RefundPending   RefundStatus = "pending"
	RefundSucceeded RefundStatus = "succeeded"
	RefundFailed    RefundStatus = "failed"
	RefundCanceled  RefundStatus = "canceled"
)

type DisputeStatus string

const (
	DisputeNeedsResponse DisputeStatus = "needs_response"
	DisputeUnderReview   DisputeStatus = "under_review"
	DisputeWon           DisputeStatus = "won"
	DisputeLost          DisputeStatus = "lost"
	DisputeWarningClosed DisputeStatus = "warning_closed"
)

type ReversalStatus string

const (
	ReversalPending  ReversalStatus = "pending"
	ReversalReversed ReversalStatus = "reversed"
	ReversalFailed   ReversalStatus = "failed"
)
