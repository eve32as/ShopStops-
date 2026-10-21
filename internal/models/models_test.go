package models

import (
	"database/sql/driver"
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

type tableNamer interface {
	TableName() string
}

func TestModelsMapEveryPersistedTableAndView(t *testing.T) {
	models := []tableNamer{
		DeliveryZone{}, StoreLocation{}, Order{}, DeliveryBundle{}, BundleOrder{},
		DeliveryStop{}, DeliveryStopEvent{}, OutboxEvent{}, Merchant{},
		MerchantSubscription{}, MerchantStorefrontVisibility{}, Jurisdiction{},
		UnifiedCheckout{}, CheckoutOrder{}, CheckoutInvoiceLine{}, DCTaxLedger{},
		CheckoutPayment{}, StripeWebhookEvent{}, ConnectTransfer{},
		PlatformCheckoutLedger{}, UnifiedDeliveryRoute{}, UnifiedRouteStop{},
		UnifiedRouteLeg{}, DriverLocationSample{}, StripePaymentIntentAttempt{},
		CheckoutRefund{}, StripeDispute{}, ConnectTransferReversal{},
		UnifiedRouteStopEvent{}, CatalogItem{}, OrderLineItem{},
	}
	want := map[string]bool{
		"delivery_zones": true, "store_locations": true, "orders": true,
		"delivery_bundles": true, "bundle_orders": true, "delivery_stops": true,
		"delivery_stop_events": true, "outbox_events": true, "merchants": true,
		"merchant_subscriptions": true, "merchant_storefront_visibility": true,
		"jurisdictions": true, "unified_checkouts": true, "checkout_orders": true,
		"checkout_invoice_lines": true, "dc_tax_ledger": true, "checkout_payments": true,
		"stripe_webhook_events": true, "connect_transfers": true,
		"platform_checkout_ledger": true, "unified_delivery_routes": true,
		"unified_route_stops": true, "unified_route_legs": true,
		"driver_location_samples": true, "stripe_payment_intent_attempts": true,
		"checkout_refunds": true, "stripe_disputes": true,
		"connect_transfer_reversals": true, "unified_route_stop_events": true,
		"catalog_items": true, "order_line_items": true,
	}
	if len(models) != len(want) {
		t.Fatalf("model count = %d, want %d", len(models), len(want))
	}
	seen := make(map[string]bool, len(models))
	var cache sync.Map
	for _, model := range models {
		table := model.TableName()
		if !want[table] {
			t.Errorf("unexpected model table %q", table)
		}
		if seen[table] {
			t.Errorf("duplicate model table %q", table)
		}
		seen[table] = true
		parsed, err := schema.Parse(model, &cache, schema.NamingStrategy{})
		if err != nil {
			t.Errorf("parse %s model: %v", table, err)
		} else if parsed.Table != table {
			t.Errorf("GORM table for %T = %q, want %q", model, parsed.Table, table)
		}
	}
	for table := range want {
		if !seen[table] {
			t.Errorf("missing model for %q", table)
		}
	}
}

func TestDecimalScannerAndValuerPreservePrecision(t *testing.T) {
	var decimal Decimal
	if err := decimal.Scan([]byte("12.30")); err != nil {
		t.Fatal(err)
	}
	if decimal != "12.30" {
		t.Fatalf("decimal = %q, want exact scale 12.30", decimal)
	}
	value, err := decimal.Value()
	if err != nil || value != driver.Value("12.30") {
		t.Fatalf("value = %#v, err = %v", value, err)
	}
	if err := decimal.Scan(float64(12.3)); err == nil {
		t.Fatal("float input was accepted even though it cannot preserve decimal precision")
	}
	if value, err := Decimal("").Value(); err != nil || value != driver.Value("0") {
		t.Fatalf("zero decimal value = %#v, err = %v", value, err)
	}
}

func TestSpatialAndJSONBScannerValuer(t *testing.T) {
	var point SpatialValue
	if err := point.Scan("POINT(-77 38)"); err != nil {
		t.Fatal(err)
	}
	if value, err := point.Value(); err != nil || value != driver.Value("POINT(-77 38)") {
		t.Fatalf("spatial value = %#v, err = %v", value, err)
	}

	var payload JSONB
	if err := payload.Scan([]byte(`{"event":"ready"}`)); err != nil {
		t.Fatal(err)
	}
	if value, err := payload.Value(); err != nil || value != driver.Value(`{"event":"ready"}`) {
		t.Fatalf("JSONB value = %#v, err = %v", value, err)
	}
	if err := payload.Scan([]byte(`not-json`)); err == nil {
		t.Fatal("invalid JSONB was accepted")
	}
	if value, err := JSONB(nil).Value(); err != nil || value != driver.Value("{}") {
		t.Fatalf("empty JSONB value = %#v, err = %v", value, err)
	}
}
