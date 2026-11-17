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

func TestExtraPickupPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("SHOPSTOPS_EXTRA_PICKUP_TEST_DSN")
	if dsn == "" {
		t.Skip("disposable PostGIS DSN not configured")
	}
	c, err := pgx.ParseConfig(dsn)
	if err != nil || (c.Host != "localhost" && c.Host != "127.0.0.1") || c.Database != "shopstops_ci" || c.User != "shopstops_ci" {
		t.Fatal("extra pickup integration requires local disposable shopstops_ci database/user")
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
	var zone, anchor, store, merchant, retailer string
	if err := db.QueryRowContext(ctx, `INSERT INTO delivery_zones(name,boundary)
        VALUES('extra Go zone',ST_Multi(ST_MakeEnvelope(-77.05,38.85,-76.95,38.95,4326))) RETURNING id`).Scan(&zone); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO merchants(name,merchant_type,stripe_account_id)
        VALUES('extra Go anchor','restaurant','acct_extra_go_anchor') RETURNING id`).Scan(&merchant); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO merchants(name,merchant_type,stripe_account_id)
        VALUES('extra Go retailer','retail','acct_extra_go_retail') RETURNING id`).Scan(&retailer); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO merchant_subscriptions(merchant_id,status) VALUES($1,'active')`, retailer); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		merchant  string
		longitude float64
		target    *string
	}{{merchant, -76.99, &anchor}, {retailer, -76.989, &store}} {
		if err := db.QueryRowContext(ctx, `INSERT INTO store_locations(name,address,location,merchant_id)
            VALUES('extra Go store','fixture',ST_SetSRID(ST_MakePoint($1,38.91),4326)::geography,$2) RETURNING id`, entry.longitude, entry.merchant).Scan(entry.target); err != nil {
			t.Fatal(err)
		}
	}
	orders := make([]string, 3)
	for i, location := range []string{anchor, store, store} {
		amount := 2000
		if i == 2 {
			amount = 1000
		}
		if err := db.QueryRowContext(ctx, `INSERT INTO orders(customer_ref,store_location_id,delivery_zone_id,dropoff_location,
            delivery_window_start,delivery_window_end,volume_liters,state,item_subtotal_cents)
            VALUES('extra-go-customer',$1,$2,ST_SetSRID(ST_MakePoint(-76.98,38.91),4326)::geography,$3,$4,1,'placed',$5) RETURNING id`,
			location, zone, start, end, amount).Scan(&orders[i]); err != nil {
			t.Fatal(err)
		}
	}
	request := checkoutRequest{AnchorStoreLocationID: anchor, OrderIDs: orders, Dropoff: geoPoint{Latitude: 38.91, Longitude: -76.98}, DeliveryWindowStart: start, DeliveryWindowEnd: end}
	p := NewPostgresRepository(db, 311, 89).WithExtraPickupPricing(ExtraPickupConfig{enabled: true})
	if _, err := p.CreateCheckout(ctx, request, "wrong-customer", "extra-go-wrong"); !errors.Is(err, ErrConflict) {
		t.Fatal("ownership", err)
	}
	first, err := p.CreateCheckout(ctx, request, "extra-go-customer", "extra-go-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if first.TotalCents != 5413 || first.Priority.Selected || first.ExtraPickup == nil || *first.ExtraPickup != (extraPickupSnapshot{TestOnly: true, MerchandiseSubtotalCents: 5000, AdditionalPickups: 1, RateBasisPoints: 25, AmountCents: 13}) {
		t.Fatalf("quote %+v", first)
	}
	for _, repo := range []*PostgresRepository{p, NewPostgresRepository(db, 999, 888)} {
		retry, err := repo.CreateCheckout(ctx, request, "extra-go-customer", "extra-go-fixture")
		if err != nil || !reflect.DeepEqual(first, retry) {
			t.Fatal("repriced retry", err, retry)
		}
	}
	if _, err := p.CheckoutForPayment(ctx, first.ID, "wrong-customer"); !errors.Is(err, ErrNotFound) {
		t.Fatal("payment ownership", err)
	}
	payment, err := p.CheckoutForPayment(ctx, first.ID, "extra-go-customer")
	if err != nil || payment.AmountCents != 5413 || !payment.ExtraPickupTestOnly || payment.PriorityTestOnly {
		t.Fatal(payment, err)
	}
	if err := p.RecordPaymentIntent(ctx, first.ID, "pi_extra_go_fixture", 5400, "requires_payment_method"); err == nil {
		t.Fatal("omitted fee accepted")
	}
	if err := p.RecordPaymentIntent(ctx, first.ID, "pi_extra_go_fixture", payment.AmountCents, "requires_payment_method"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE checkout_invoice_lines SET amount_cents=0 WHERE checkout_id=$1 AND line_type='extra_pickup_platform_fee'`, first.ID); err == nil {
		t.Fatal("invoice changed")
	}
}
