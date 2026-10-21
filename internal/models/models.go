// Package models maps the ShopStops PostgreSQL schema to GORM-compatible models.
// SQL migrations remain authoritative; do not use GORM AutoMigrate.
package models

import "time"

type DeliveryZone struct {
	ID                      string       `gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	Name                    string       `gorm:"column:name;type:text;not null"`
	Boundary                SpatialValue `gorm:"column:boundary;type:geometry(MultiPolygon,4326);not null"`
	Enabled                 bool         `gorm:"column:enabled;not null;default:true"`
	CreatedAt               time.Time    `gorm:"column:created_at;not null;default:now()"`
	DispatchMaxOrders       int32        `gorm:"column:dispatch_max_orders;not null;default:3"`
	DispatchPickupRadiusM   int32        `gorm:"column:dispatch_pickup_radius_m;not null;default:2000"`
	DispatchDropoffRadiusM  int32        `gorm:"column:dispatch_dropoff_radius_m;not null;default:2000"`
	DispatchLimitsUpdatedBy string       `gorm:"column:dispatch_limits_updated_by;type:text;not null;default:migration"`
	DispatchLimitsUpdatedAt time.Time    `gorm:"column:dispatch_limits_updated_at;not null;default:now()"`
}

func (DeliveryZone) TableName() string { return "delivery_zones" }

type StoreLocation struct {
	ID         string       `gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	Name       string       `gorm:"column:name;type:text;not null"`
	Address    string       `gorm:"column:address;type:text;not null"`
	Location   SpatialValue `gorm:"column:location;type:geography(Point,4326);not null"`
	Active     bool         `gorm:"column:active;not null;default:true"`
	CreatedAt  time.Time    `gorm:"column:created_at;not null;default:now()"`
	MerchantID *string      `gorm:"column:merchant_id;type:uuid"`
}

func (StoreLocation) TableName() string { return "store_locations" }

type Order struct {
	ID                     string       `gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	CustomerRef            string       `gorm:"column:customer_ref;type:text;not null"`
	StoreLocationID        string       `gorm:"column:store_location_id;type:uuid;not null"`
	DeliveryZoneID         string       `gorm:"column:delivery_zone_id;type:uuid;not null"`
	DropoffLocation        SpatialValue `gorm:"column:dropoff_location;type:geography(Point,4326);not null"`
	DeliveryWindowStart    time.Time    `gorm:"column:delivery_window_start;not null"`
	DeliveryWindowEnd      time.Time    `gorm:"column:delivery_window_end;not null"`
	VolumeLiters           Decimal      `gorm:"column:volume_liters;type:numeric(10,2);not null"`
	State                  OrderState   `gorm:"column:state;type:text;not null;default:placed"`
	ReadyAt                *time.Time   `gorm:"column:ready_at"`
	CreatedAt              time.Time    `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt              time.Time    `gorm:"column:updated_at;not null;default:now()"`
	ItemSubtotalCents      int32        `gorm:"column:item_subtotal_cents;not null;default:0"`
	CreationIdempotencyKey *string      `gorm:"column:creation_idempotency_key;type:text;uniqueIndex"`
	CreationFingerprint    *string      `gorm:"column:creation_fingerprint;type:text"`
}

func (Order) TableName() string { return "orders" }

type DeliveryBundle struct {
	ID             string               `gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	DeliveryZoneID string               `gorm:"column:delivery_zone_id;type:uuid;not null"`
	Status         DeliveryBundleStatus `gorm:"column:status;type:text;not null"`
	DriverRef      *string              `gorm:"column:driver_ref;type:text"`
	CapacityLiters Decimal              `gorm:"column:capacity_liters;type:numeric(10,2);not null"`
	MaxOrders      int32                `gorm:"column:max_orders;not null"`
	IdempotencyKey string               `gorm:"column:idempotency_key;type:text;not null;uniqueIndex"`
	CreatedAt      time.Time            `gorm:"column:created_at;not null;default:now()"`
	ActivatedAt    time.Time            `gorm:"column:activated_at;not null;default:now()"`
}

func (DeliveryBundle) TableName() string { return "delivery_bundles" }

type BundleOrder struct {
	BundleID       string `gorm:"column:bundle_id;type:uuid;primaryKey"`
	OrderID        string `gorm:"column:order_id;type:uuid;primaryKey;uniqueIndex"`
	BundlePosition int32  `gorm:"column:bundle_position;not null"`
}

func (BundleOrder) TableName() string { return "bundle_orders" }

type DeliveryStop struct {
	ID              string             `gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	BundleID        string             `gorm:"column:bundle_id;type:uuid;not null"`
	OrderID         string             `gorm:"column:order_id;type:uuid;not null"`
	Sequence        int32              `gorm:"column:sequence;not null"`
	StopType        StopType           `gorm:"column:stop_type;type:text;not null"`
	StoreLocationID *string            `gorm:"column:store_location_id;type:uuid"`
	Location        SpatialValue       `gorm:"column:location;type:geography(Point,4326);not null"`
	Status          DeliveryStopStatus `gorm:"column:status;type:text;not null;default:pending"`
	CreatedAt       time.Time          `gorm:"column:created_at;not null;default:now()"`
	ArrivedAt       *time.Time         `gorm:"column:arrived_at"`
	CompletedAt     *time.Time         `gorm:"column:completed_at"`
}

func (DeliveryStop) TableName() string { return "delivery_stops" }

type DeliveryStopEvent struct {
	EventID    string                `gorm:"column:event_id;type:text;primaryKey"`
	StopID     string                `gorm:"column:stop_id;type:uuid;not null"`
	DriverRef  string                `gorm:"column:driver_ref;type:text;not null"`
	EventType  DeliveryStopEventType `gorm:"column:event_type;type:text;not null"`
	ObservedAt time.Time             `gorm:"column:observed_at;not null"`
	ReceivedAt time.Time             `gorm:"column:received_at;not null;default:now()"`
	Payload    JSONB                 `gorm:"column:payload;type:jsonb;not null;default:'{}'"`
}

func (DeliveryStopEvent) TableName() string { return "delivery_stop_events" }

type OutboxEvent struct {
	ID            int64      `gorm:"column:id;primaryKey;autoIncrement"`
	AggregateType string     `gorm:"column:aggregate_type;type:text;not null"`
	AggregateID   string     `gorm:"column:aggregate_id;type:uuid;not null"`
	EventType     string     `gorm:"column:event_type;type:text;not null"`
	Payload       JSONB      `gorm:"column:payload;type:jsonb;not null"`
	CreatedAt     time.Time  `gorm:"column:created_at;not null;default:now()"`
	PublishedAt   *time.Time `gorm:"column:published_at"`
}

func (OutboxEvent) TableName() string { return "outbox_events" }

type Merchant struct {
	ID              string       `gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	Name            string       `gorm:"column:name;type:text;not null"`
	MerchantType    MerchantType `gorm:"column:merchant_type;type:text;not null"`
	StripeAccountID *string      `gorm:"column:stripe_account_id;type:text;uniqueIndex"`
	Active          bool         `gorm:"column:active;not null;default:true"`
	CreatedAt       time.Time    `gorm:"column:created_at;not null;default:now()"`
}

func (Merchant) TableName() string { return "merchants" }

type MerchantSubscription struct {
	MerchantID                   string             `gorm:"column:merchant_id;type:uuid;primaryKey"`
	StripeCustomerID             *string            `gorm:"column:stripe_customer_id;type:text"`
	StripeSubscriptionID         *string            `gorm:"column:stripe_subscription_id;type:text;uniqueIndex"`
	Status                       SubscriptionStatus `gorm:"column:status;type:text;not null"`
	MonthlyAmountCents           int32              `gorm:"column:monthly_amount_cents;not null;default:19900"`
	Currency                     string             `gorm:"column:currency;type:char(3);not null;default:usd"`
	CurrentPeriodEnd             *time.Time         `gorm:"column:current_period_end"`
	PaymentFailedAt              *time.Time         `gorm:"column:payment_failed_at"`
	UpdatedAt                    time.Time          `gorm:"column:updated_at;not null;default:now()"`
	BillingSetupAttemptedAt      *time.Time         `gorm:"column:billing_setup_attempted_at"`
	SubscriptionAttemptedAt      *time.Time         `gorm:"column:subscription_attempted_at"`
	StripeDefaultPaymentMethodID *string            `gorm:"column:stripe_default_payment_method_id;type:text"`
	StripeCustomerAttemptedAt    *time.Time         `gorm:"column:stripe_customer_attempted_at"`
}

func (MerchantSubscription) TableName() string { return "merchant_subscriptions" }

type MerchantStorefrontVisibility struct {
	MerchantID string `gorm:"column:merchant_id;type:uuid;primaryKey;->"`
	Visible    bool   `gorm:"column:visible;->"`
}

func (MerchantStorefrontVisibility) TableName() string { return "merchant_storefront_visibility" }

type Jurisdiction struct {
	ID       string       `gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	Code     string       `gorm:"column:code;type:text;not null;uniqueIndex"`
	Name     string       `gorm:"column:name;type:text;not null"`
	Boundary SpatialValue `gorm:"column:boundary;type:geometry(MultiPolygon,4326);not null"`
	Enabled  bool         `gorm:"column:enabled;not null;default:true"`
}

func (Jurisdiction) TableName() string { return "jurisdictions" }

type UnifiedCheckout struct {
	ID                    string         `gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	IdempotencyKey        string         `gorm:"column:idempotency_key;type:text;not null;uniqueIndex"`
	CustomerRef           string         `gorm:"column:customer_ref;type:text;not null"`
	AnchorStoreLocationID string         `gorm:"column:anchor_store_location_id;type:uuid;not null"`
	DeliveryZoneID        string         `gorm:"column:delivery_zone_id;type:uuid;not null"`
	DropoffLocation       SpatialValue   `gorm:"column:dropoff_location;type:geography(Point,4326);not null"`
	DeliveryWindowStart   time.Time      `gorm:"column:delivery_window_start;not null"`
	DeliveryWindowEnd     time.Time      `gorm:"column:delivery_window_end;not null"`
	Status                CheckoutStatus `gorm:"column:status;type:text;not null;default:building"`
	DeliveryFeeCents      int32          `gorm:"column:delivery_fee_cents;not null;default:0"`
	ServiceFeeCents       int32          `gorm:"column:service_fee_cents;not null;default:0"`
	Currency              string         `gorm:"column:currency;type:char(3);not null;default:usd"`
	CreatedAt             time.Time      `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt             time.Time      `gorm:"column:updated_at;not null;default:now()"`
}

func (UnifiedCheckout) TableName() string { return "unified_checkouts" }

type CheckoutOrder struct {
	CheckoutID string    `gorm:"column:checkout_id;type:uuid;primaryKey"`
	OrderID    string    `gorm:"column:order_id;type:uuid;primaryKey;uniqueIndex"`
	AddedAt    time.Time `gorm:"column:added_at;not null;default:now()"`
}

func (CheckoutOrder) TableName() string { return "checkout_orders" }

type CheckoutInvoiceLine struct {
	ID          string          `gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	CheckoutID  string          `gorm:"column:checkout_id;type:uuid;not null"`
	MerchantID  *string         `gorm:"column:merchant_id;type:uuid"`
	LineType    InvoiceLineType `gorm:"column:line_type;type:text;not null"`
	Description string          `gorm:"column:description;type:text;not null"`
	AmountCents int32           `gorm:"column:amount_cents;not null"`
	Currency    string          `gorm:"column:currency;type:char(3);not null;default:usd"`
	CreatedAt   time.Time       `gorm:"column:created_at;not null;default:now()"`
}

func (CheckoutInvoiceLine) TableName() string { return "checkout_invoice_lines" }

type DCTaxLedger struct {
	ID               int64           `gorm:"column:id;primaryKey;autoIncrement"`
	CheckoutID       string          `gorm:"column:checkout_id;type:uuid;not null;uniqueIndex"`
	InvoiceLineID    string          `gorm:"column:invoice_line_id;type:uuid;not null;uniqueIndex"`
	AmountCents      int32           `gorm:"column:amount_cents;not null"`
	Status           TaxLedgerStatus `gorm:"column:status;type:text;not null;default:held"`
	PaymentReference *string         `gorm:"column:payment_reference;type:text"`
	RemittedAt       *time.Time      `gorm:"column:remitted_at"`
	CreatedAt        time.Time       `gorm:"column:created_at;not null;default:now()"`
}

func (DCTaxLedger) TableName() string { return "dc_tax_ledger" }

type CheckoutPayment struct {
	ID                    string        `gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	CheckoutID            string        `gorm:"column:checkout_id;type:uuid;not null;uniqueIndex"`
	StripePaymentIntentID string        `gorm:"column:stripe_payment_intent_id;type:text;not null;uniqueIndex"`
	GrossAmountCents      int32         `gorm:"column:gross_amount_cents;not null"`
	Currency              string        `gorm:"column:currency;type:char(3);not null;default:usd"`
	Status                PaymentStatus `gorm:"column:status;type:text;not null"`
	CreatedAt             time.Time     `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt             time.Time     `gorm:"column:updated_at;not null;default:now()"`
}

func (CheckoutPayment) TableName() string { return "checkout_payments" }

type StripeWebhookEvent struct {
	StripeEventID string     `gorm:"column:stripe_event_id;type:text;primaryKey"`
	EventType     string     `gorm:"column:event_type;type:text;not null"`
	Payload       JSONB      `gorm:"column:payload;type:jsonb;not null"`
	ReceivedAt    time.Time  `gorm:"column:received_at;not null;default:now()"`
	ProcessedAt   *time.Time `gorm:"column:processed_at"`
}

func (StripeWebhookEvent) TableName() string { return "stripe_webhook_events" }

type ConnectTransfer struct {
	ID                  string         `gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	CheckoutID          string         `gorm:"column:checkout_id;type:uuid;not null;uniqueIndex:connect_transfers_checkout_merchant_key,priority:1"`
	MerchantID          string         `gorm:"column:merchant_id;type:uuid;not null;uniqueIndex:connect_transfers_checkout_merchant_key,priority:2"`
	StripeTransferID    *string        `gorm:"column:stripe_transfer_id;type:text;uniqueIndex"`
	ItemGrossCents      int32          `gorm:"column:item_gross_cents;not null"`
	ProcessingFeeCents  int32          `gorm:"column:processing_fee_cents;not null"`
	TransferAmountCents int32          `gorm:"column:transfer_amount_cents;->"`
	Status              TransferStatus `gorm:"column:status;type:text;not null;default:pending"`
	CreatedAt           time.Time      `gorm:"column:created_at;not null;default:now()"`
	SourceChargeID      *string        `gorm:"column:source_charge_id;type:text"`
	TransferAttemptedAt *time.Time     `gorm:"column:transfer_attempted_at"`
}

func (ConnectTransfer) TableName() string { return "connect_transfers" }

type PlatformCheckoutLedger struct {
	CheckoutID               string    `gorm:"column:checkout_id;type:uuid;primaryKey"`
	DeliveryFeeCents         int32     `gorm:"column:delivery_fee_cents;not null"`
	ServiceFeeCents          int32     `gorm:"column:service_fee_cents;not null"`
	DCSurchargeCents         int32     `gorm:"column:dc_surcharge_cents;not null"`
	StripeProcessingFeeCents int32     `gorm:"column:stripe_processing_fee_cents;not null;default:0"`
	StripePaymentIntentID    *string   `gorm:"column:stripe_payment_intent_id;type:text;uniqueIndex"`
	CreatedAt                time.Time `gorm:"column:created_at;not null;default:now()"`
}

func (PlatformCheckoutLedger) TableName() string { return "platform_checkout_ledger" }

type UnifiedDeliveryRoute struct {
	ID               string        `gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	CheckoutID       string        `gorm:"column:checkout_id;type:uuid;not null;uniqueIndex"`
	DriverRef        *string       `gorm:"column:driver_ref;type:text"`
	RoutingProvider  RouteProvider `gorm:"column:routing_provider;type:text;not null"`
	ProviderRouteRef *string       `gorm:"column:provider_route_ref;type:text"`
	Status           RouteStatus   `gorm:"column:status;type:text;not null;default:planned"`
	IdempotencyKey   string        `gorm:"column:idempotency_key;type:text;not null;uniqueIndex"`
	CreatedAt        time.Time     `gorm:"column:created_at;not null;default:now()"`
}

func (UnifiedDeliveryRoute) TableName() string { return "unified_delivery_routes" }

type UnifiedRouteStop struct {
	ID              string          `gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	RouteID         string          `gorm:"column:route_id;type:uuid;not null;uniqueIndex:unified_route_stops_route_sequence_key,priority:1"`
	Sequence        int32           `gorm:"column:sequence;not null;uniqueIndex:unified_route_stops_route_sequence_key,priority:2"`
	StopType        StopType        `gorm:"column:stop_type;type:text;not null"`
	StoreLocationID *string         `gorm:"column:store_location_id;type:uuid"`
	Location        SpatialValue    `gorm:"column:location;type:geography(Point,4326);not null"`
	Status          RouteStopStatus `gorm:"column:status;type:text;not null;default:pending"`
	CreatedAt       time.Time       `gorm:"column:created_at;not null;default:now()"`
	ArrivedAt       *time.Time      `gorm:"column:arrived_at"`
	CompletedAt     *time.Time      `gorm:"column:completed_at"`
}

func (UnifiedRouteStop) TableName() string { return "unified_route_stops" }

type UnifiedRouteLeg struct {
	ID              string           `gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	RouteID         string           `gorm:"column:route_id;type:uuid;not null;uniqueIndex:unified_route_legs_route_from_sequence_key,priority:1"`
	FromSequence    int32            `gorm:"column:from_sequence;not null;uniqueIndex:unified_route_legs_route_from_sequence_key,priority:2"`
	ToSequence      int32            `gorm:"column:to_sequence;not null"`
	DistanceMeters  int32            `gorm:"column:distance_meters;not null"`
	DurationSeconds int32            `gorm:"column:duration_seconds;not null"`
	Provider        RouteLegProvider `gorm:"column:provider;type:text;not null"`
	CreatedAt       time.Time        `gorm:"column:created_at;not null;default:now()"`
}

func (UnifiedRouteLeg) TableName() string { return "unified_route_legs" }

type DriverLocationSample struct {
	ID         int64        `gorm:"column:id;primaryKey;autoIncrement"`
	RouteID    string       `gorm:"column:route_id;type:uuid;not null;uniqueIndex:driver_location_samples_route_driver_observed_key,priority:1"`
	DriverRef  string       `gorm:"column:driver_ref;type:text;not null;uniqueIndex:driver_location_samples_route_driver_observed_key,priority:2"`
	Location   SpatialValue `gorm:"column:location;type:geography(Point,4326);not null"`
	ObservedAt time.Time    `gorm:"column:observed_at;not null;uniqueIndex:driver_location_samples_route_driver_observed_key,priority:3"`
	ReceivedAt time.Time    `gorm:"column:received_at;not null;default:now()"`
	EventID    string       `gorm:"column:event_id;type:text;not null;uniqueIndex"`
}

func (DriverLocationSample) TableName() string { return "driver_location_samples" }

type StripePaymentIntentAttempt struct {
	CheckoutID     string    `gorm:"column:checkout_id;type:uuid;primaryKey"`
	IdempotencyKey string    `gorm:"column:idempotency_key;type:text;not null;uniqueIndex"`
	CreatedAt      time.Time `gorm:"column:created_at;not null;default:now()"`
}

func (StripePaymentIntentAttempt) TableName() string { return "stripe_payment_intent_attempts" }

type CheckoutRefund struct {
	ID                    string       `gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	CheckoutID            string       `gorm:"column:checkout_id;type:uuid;not null"`
	StripePaymentIntentID string       `gorm:"column:stripe_payment_intent_id;type:text;not null"`
	StripeRefundID        *string      `gorm:"column:stripe_refund_id;type:text;uniqueIndex"`
	IdempotencyKey        string       `gorm:"column:idempotency_key;type:text;not null;uniqueIndex"`
	RequestedBy           string       `gorm:"column:requested_by;type:text;not null"`
	AmountCents           int32        `gorm:"column:amount_cents;not null"`
	Reason                RefundReason `gorm:"column:reason;type:text;not null"`
	Status                RefundStatus `gorm:"column:status;type:text;not null;default:pending"`
	AttemptedAt           time.Time    `gorm:"column:attempted_at;not null;default:now()"`
	CreatedAt             time.Time    `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt             time.Time    `gorm:"column:updated_at;not null;default:now()"`
}

func (CheckoutRefund) TableName() string { return "checkout_refunds" }

type StripeDispute struct {
	StripeDisputeID       string        `gorm:"column:stripe_dispute_id;type:text;primaryKey"`
	CheckoutID            string        `gorm:"column:checkout_id;type:uuid;not null"`
	StripePaymentIntentID string        `gorm:"column:stripe_payment_intent_id;type:text;not null"`
	AmountCents           int32         `gorm:"column:amount_cents;not null"`
	Currency              string        `gorm:"column:currency;type:char(3);not null;default:usd"`
	Reason                string        `gorm:"column:reason;type:text;not null"`
	Status                DisputeStatus `gorm:"column:status;type:text;not null"`
	EvidenceDueBy         *time.Time    `gorm:"column:evidence_due_by"`
	UpdatedAt             time.Time     `gorm:"column:updated_at;not null;default:now()"`
}

func (StripeDispute) TableName() string { return "stripe_disputes" }

type ConnectTransferReversal struct {
	ID               string         `gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	CheckoutRefundID *string        `gorm:"column:checkout_refund_id;type:uuid"`
	StripeDisputeID  *string        `gorm:"column:stripe_dispute_id;type:text"`
	CheckoutID       string         `gorm:"column:checkout_id;type:uuid;not null"`
	MerchantID       string         `gorm:"column:merchant_id;type:uuid;not null"`
	StripeTransferID string         `gorm:"column:stripe_transfer_id;type:text;not null"`
	StripeReversalID *string        `gorm:"column:stripe_reversal_id;type:text;uniqueIndex"`
	IdempotencyKey   string         `gorm:"column:idempotency_key;type:text;not null;uniqueIndex"`
	AmountCents      int32          `gorm:"column:amount_cents;not null"`
	AttemptedAt      time.Time      `gorm:"column:attempted_at;not null;default:now()"`
	Status           ReversalStatus `gorm:"column:status;type:text;not null;default:pending"`
	CreatedAt        time.Time      `gorm:"column:created_at;not null;default:now()"`
}

func (ConnectTransferReversal) TableName() string { return "connect_transfer_reversals" }

type UnifiedRouteStopEvent struct {
	EventID    string             `gorm:"column:event_id;type:text;primaryKey"`
	RouteID    string             `gorm:"column:route_id;type:uuid;not null"`
	StopID     string             `gorm:"column:stop_id;type:uuid;not null"`
	DriverRef  string             `gorm:"column:driver_ref;type:text;not null"`
	EventType  RouteStopEventType `gorm:"column:event_type;type:text;not null"`
	ObservedAt time.Time          `gorm:"column:observed_at;not null"`
	CreatedAt  time.Time          `gorm:"column:created_at;not null;default:now()"`
}

func (UnifiedRouteStopEvent) TableName() string { return "unified_route_stop_events" }

type CatalogItem struct {
	ID                     string    `gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	StoreLocationID        string    `gorm:"column:store_location_id;type:uuid;not null"`
	Name                   string    `gorm:"column:name;type:text;not null"`
	Description            string    `gorm:"column:description;type:text;not null;default:''"`
	UnitPriceCents         int32     `gorm:"column:unit_price_cents;not null"`
	VolumeLiters           Decimal   `gorm:"column:volume_liters;type:numeric(10,2);not null"`
	Available              bool      `gorm:"column:available;not null;default:true"`
	CreatedAt              time.Time `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt              time.Time `gorm:"column:updated_at;not null;default:now()"`
	CreationIdempotencyKey *string   `gorm:"column:creation_idempotency_key;type:text;uniqueIndex"`
	CreationFingerprint    *string   `gorm:"column:creation_fingerprint;type:text"`
}

func (CatalogItem) TableName() string { return "catalog_items" }

type OrderLineItem struct {
	ID             string    `gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	OrderID        string    `gorm:"column:order_id;type:uuid;not null;uniqueIndex:order_line_items_order_catalog_item_key,priority:1"`
	CatalogItemID  string    `gorm:"column:catalog_item_id;type:uuid;not null;uniqueIndex:order_line_items_order_catalog_item_key,priority:2"`
	ItemName       string    `gorm:"column:item_name;type:text;not null"`
	Quantity       int32     `gorm:"column:quantity;not null"`
	UnitPriceCents int32     `gorm:"column:unit_price_cents;not null"`
	VolumeLiters   Decimal   `gorm:"column:volume_liters;type:numeric(10,2);not null"`
	CreatedAt      time.Time `gorm:"column:created_at;not null;default:now()"`
}

func (OrderLineItem) TableName() string { return "order_line_items" }
