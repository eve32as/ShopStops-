package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Only the fresh local CI database; no Stripe calls. SQL fixtures exercise the
// refund/merchant ledger; this test exercises the actual Go repository commits.
func TestPriorityPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("SHOPSTOPS_PRIORITY_TEST_DSN")
	if dsn == "" {
		t.Skip("disposable PostGIS DSN not configured")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil || (config.Host != "localhost" && config.Host != "127.0.0.1") || config.Database != "shopstops_ci" || config.User != "shopstops_ci" {
		t.Fatal("priority integration requires local disposable shopstops_ci database/user")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	var storeID, orderID string
	err = db.QueryRowContext(ctx, `WITH zone AS (
	    INSERT INTO delivery_zones(name,boundary) VALUES ('priority Go fixture zone',
	    ST_Multi(ST_MakeEnvelope(-77.05,38.85,-76.95,38.95,4326))) RETURNING id
	), merchant AS (
	    INSERT INTO merchants(name,merchant_type,stripe_account_id)
	    VALUES ('priority Go fixture merchant','restaurant','acct_priority_go_fixture') RETURNING id
	), store AS (
	    INSERT INTO store_locations(name,address,location,merchant_id)
	    SELECT 'priority Go fixture store','fixture',ST_SetSRID(ST_MakePoint(-76.99,38.91),4326)::geography,id
	    FROM merchant RETURNING id
	), customer_order AS (
	    INSERT INTO orders(customer_ref,store_location_id,delivery_zone_id,dropoff_location,
	        delivery_window_start,delivery_window_end,volume_liters,state,item_subtotal_cents)
	    SELECT 'priority-go-customer',store.id,zone.id,ST_SetSRID(ST_MakePoint(-76.98,38.91),4326)::geography,
	        $1,$2,1,'placed',1200 FROM store CROSS JOIN zone RETURNING id,store_location_id
	) SELECT store_location_id,id FROM customer_order`, start, end).Scan(&storeID, &orderID)
	if err != nil {
		t.Fatal(err)
	}
	request := checkoutRequest{PriorityService: true, AnchorStoreLocationID: storeID, OrderIDs: []string{orderID}, Dropoff: geoPoint{Latitude: 38.91, Longitude: -76.98}, DeliveryWindowStart: start, DeliveryWindowEnd: end}
	p := NewPostgresRepository(db, 311, 89)
	if _, err := p.CreateCheckout(ctx, request, "priority-go-customer", "priority-go-disabled"); !errors.Is(err, ErrPriorityUnavailable) {
		t.Fatalf("disabled: %v", err)
	}
	e := priorityTestEnv()
	c, err := PriorityConfigFromEnv(func(key string) string { return e[key] })
	if err != nil {
		t.Fatal(err)
	}
	p = NewPostgresRepository(db, 311, 89, c)
	if _, err := p.CreateCheckout(ctx, request, "another-customer", "priority-go-owner-reject"); !errors.Is(err, ErrConflict) {
		t.Fatalf("ownership: %v", err)
	}
	first, err := p.CreateCheckout(ctx, request, "priority-go-customer", "priority-go-fixture")
	if err != nil {
		t.Fatal(err)
	}
	// No DC fixture is committed by the SQL suite: 1200 + 311 + 89 + 137 + 263.
	if first.TotalCents != 2000 || first.Priority != (prioritySnapshot{Selected: true, TestOnly: true, ShoppingCents: 137, DeliveryCents: 263, ShopperAllocationCents: 137, DriverAllocationCents: 263, AllocationStatus: "pending_fulfillment"}) {
		t.Fatalf("quote %+v", first)
	}
	changed := NewPostgresRepository(db, 999, 888, PriorityConfig{enabled: true, shoppingCents: 555, deliveryCents: 666})
	for _, retryRepo := range []*PostgresRepository{changed, NewPostgresRepository(db, 0, 0)} {
		retry, err := retryRepo.CreateCheckout(ctx, request, "priority-go-customer", "priority-go-fixture")
		if err != nil || !reflect.DeepEqual(first, retry) {
			t.Fatalf("stored-price retry %+v %v", retry, err)
		}
	}
	request.PriorityService = false
	if _, err := p.CreateCheckout(ctx, request, "priority-go-customer", "priority-go-fixture"); !errors.Is(err, ErrConflict) {
		t.Fatalf("choice retry: %v", err)
	}
	request.PriorityService = true
	if _, err := p.CreateCheckout(ctx, request, "another-customer", "priority-go-fixture"); !errors.Is(err, ErrConflict) {
		t.Fatalf("retry owner: %v", err)
	}
	if _, err := p.CheckoutForPayment(ctx, first.ID, "another-customer"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("payment owner: %v", err)
	}
	payment, err := p.CheckoutForPayment(ctx, first.ID, "priority-go-customer")
	if err != nil || payment.AmountCents != 2000 || !payment.PriorityTestOnly {
		t.Fatalf("payment %+v %v", payment, err)
	}
	if err := p.RecordPaymentIntent(ctx, first.ID, "pi_priority_go_fixture", 1600, "requires_payment_method"); err == nil {
		t.Fatal("payment omitted priority fees")
	}
	if err := p.RecordPaymentIntent(ctx, first.ID, "pi_priority_go_fixture", payment.AmountCents, "requires_payment_method"); err != nil {
		t.Fatal(err)
	}
	retryPayment, err := p.CheckoutForPayment(ctx, first.ID, "priority-go-customer")
	if err != nil || retryPayment.PaymentIntentID != "pi_priority_go_fixture" || retryPayment.AmountCents != 2000 {
		t.Fatal(retryPayment, err)
	}
}
