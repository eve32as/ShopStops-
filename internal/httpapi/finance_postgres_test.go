package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Opt-in only on the disposable CI database after the migration/SQL fixtures.
// Never point this integration test at an application database.
func TestFinancePostgresIntegration(t *testing.T) {
	dsn := os.Getenv("SHOPSTOPS_FINANCE_TEST_DSN")
	if dsn == "" {
		t.Skip("disposable PostGIS DSN not configured")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil || (config.Host != "127.0.0.1" && config.Host != "localhost") || config.Database != "shopstops_ci" || config.User != "shopstops_ci" {
		t.Fatal("finance integration requires local disposable shopstops_ci database/user")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	p := NewPostgresRepository(db, 0, 0)
	request := FinanceCostRequest{Period: "2026-11", Category: "driver", AmountCents: 501}
	first, err := p.CreateFinanceCost(ctx, "integration-admin", financeKey, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.CreateFinanceCost(ctx, "integration-admin", financeKey, request)
	if err != nil || first != second || first.Kind != "manual_cost" || first.CreatedAt.Location() != time.UTC {
		t.Fatalf("retry: %+v %+v %v", first, second, err)
	}
	different := request
	different.AmountCents++
	if _, err := p.CreateFinanceCost(ctx, "integration-admin", financeKey, different); !errors.Is(err, ErrConflict) {
		t.Fatalf("payload conflict %v", err)
	}
	if _, err := p.CreateFinanceCost(ctx, "other-admin", financeKey, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("actor conflict %v", err)
	}
	otherKey := "22222222-2222-4222-8222-222222222222"
	other := FinanceCostRequest{Period: "2026-11", Category: "hosting", AmountCents: 60000}
	if _, err := p.CreateFinanceCost(ctx, "integration-admin", otherKey, other); err != nil {
		t.Fatal(err)
	}
	otherPeriod := FinanceCostRequest{Period: "2026-12", Category: "refund_loss", AmountCents: 123}
	if _, err := p.CreateFinanceCost(ctx, "integration-admin", "33333333-3333-4333-8333-333333333333", otherPeriod); err != nil {
		t.Fatal(err)
	}
	for _, offset := range []int{0, 1, 100000} {
		report, err := p.FinanceCosts(ctx, "2026-11", 1, offset)
		if err != nil || report.Kind != "manual_cost_report" || report.TotalCents != "60501" || report.Limit != 1 || report.Offset != offset {
			t.Fatalf("report %+v %v", report, err)
		}
		if offset == 100000 {
			if len(report.Costs) != 0 {
				t.Fatal("empty page expected")
			}
			continue
		}
		want := financeKey
		if offset == 1 {
			want = otherKey
		}
		if len(report.Costs) != 1 || report.Costs[0].ID != want || report.Costs[0].CreatedAt.Location() != time.UTC {
			t.Fatal(report)
		}
	}
	empty, err := p.FinanceCosts(ctx, "2000-01", 50, 0)
	if err != nil || empty.TotalCents != "0" || empty.Costs == nil || len(empty.Costs) != 0 {
		t.Fatalf("empty report %+v %v", empty, err)
	}
}
