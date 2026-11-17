package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/eve32as/ShopStops-/internal/finance"
	"github.com/jackc/pgx/v5/pgconn"
)

const financeKey = "11111111-1111-4111-8111-111111111111"
const costBody = `{"period":"2026-11","category":"driver","amountCents":501}`

type fakeFinanceRepository struct {
	fakeRepository
	records       map[string]FinanceCost
	err           error
	calls         int
	period        string
	limit, offset int
}

func (f *fakeFinanceRepository) CreateFinanceCost(_ context.Context, actor, key string, c FinanceCostRequest) (FinanceCost, error) {
	f.calls++
	if f.err != nil {
		return FinanceCost{}, f.err
	}
	if f.records == nil {
		f.records = map[string]FinanceCost{}
	}
	if old, ok := f.records[key]; ok {
		if old.Actor != actor || old.FinanceCostRequest != c {
			return FinanceCost{}, ErrConflict
		}
		return old, nil
	}
	v := FinanceCost{ID: key, Actor: actor, FinanceCostRequest: c, Kind: "manual_cost", CreatedAt: time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)}
	f.records[key] = v
	return v, nil
}
func (f *fakeFinanceRepository) FinanceCosts(_ context.Context, period string, limit, offset int) (FinanceCostReport, error) {
	f.calls++
	f.period, f.limit, f.offset = period, limit, offset
	return FinanceCostReport{Kind: "manual_cost_report", Period: period, Limit: limit, Offset: offset, TotalCents: "501", Costs: []FinanceCost{}}, f.err
}
func financeRequest(h http.Handler, method, path, body, actor, role, key string, auth bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if auth {
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	}
	r.Header.Set("X-ShopStops-Actor", actor)
	r.Header.Set("X-ShopStops-Actor-Role", role)
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestFinanceAdminAuthentication(t *testing.T) {
	f := &fakeFinanceRepository{}
	h := New(f, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, route := range []struct{ method, path string }{{"POST", "/admin/finance/scenarios"}, {"POST", "/admin/finance/costs"}, {"GET", "/admin/finance/costs?period=2026-11"}} {
		for _, tc := range []struct {
			name, actor, role string
			auth              bool
			code              int
		}{{"missing token", "admin", "admin", false, 401}, {"missing actor", "", "admin", true, 400}, {"oversize actor", strings.Repeat("a", 201), "admin", true, 400}, {"customer", "admin", "customer", true, 403}, {"merchant", "admin", "merchant", true, 403}} {
			t.Run(route.method+route.path+tc.name, func(t *testing.T) {
				w := financeRequest(h, route.method, route.path, costBody, tc.actor, tc.role, financeKey, tc.auth)
				if w.Code != tc.code {
					t.Fatalf("%d %s", w.Code, w.Body)
				}
			})
		}
	}
	if f.calls != 0 {
		t.Fatal("unauthorized ledger access")
	}
}
func TestFinanceScenarioPureAndStrict(t *testing.T) {
	h := testServer()
	for _, tc := range []struct {
		name, body string
		code       int
	}{{"valid", `{"checkouts":1000,"deliveryFeeCents":600,"serviceFeeCents":200,"driverBaseCents":500,"dropsPerBundle":1,"hostingCents":60000}`, 200}, {"negative", `{"dropsPerBundle":1,"deliveryFeeCents":-1}`, 400}, {"fraction", `{"dropsPerBundle":1,"deliveryFeeCents":1.2}`, 400}, {"unknown", `{"dropsPerBundle":1,"payout":true}`, 400}, {"trailing", `{"dropsPerBundle":1} {}`, 400}, {"empty", "", 400}, {"oversize", strings.Repeat(" ", 8193) + `{"dropsPerBundle":1}`, 400}, {"bound", `{"dropsPerBundle":1,"checkouts":100001}`, 400}, {"null", "null", 400}} {
		t.Run(tc.name, func(t *testing.T) {
			w := financeRequest(h, "POST", "/admin/finance/scenarios", tc.body, "admin", "admin", "", true)
			if w.Code != tc.code {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			if tc.code == 200 && !strings.Contains(w.Body.String(), `"kind":"estimate"`) {
				t.Fatal(w.Body)
			}
		})
	}
}
func TestFinancePlanningExtensionAPI(t *testing.T) {
	f := &fakeFinanceRepository{}
	h := New(f, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil)))
	base := `{"checkouts":4,"deliveryFeeCents":800,"driverBaseCents":100,"dropsPerBundle":3}`
	decode := func(body string) finance.Result {
		t.Helper()
		w := financeRequest(h, "POST", "/admin/finance/scenarios", body, "admin", "admin", "", true)
		var result finance.Result
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 {
			t.Fatalf("%d %s %v", w.Code, w.Body, err)
		}
		return result
	}
	omitted := decode(base)
	zeros := decode(strings.TrimSuffix(base, "}") + `,"maintenanceCents":0,"originalPickupCents":0,"additionalPickupsPerBundle":0,"pickupRadiusMilliMiles":0,"shopperCentsPerCheckout":0,"fastServiceAdoptionBasisPoints":0,"fastShoppingFeeCents":0,"fastDeliveryFeeCents":0,"fastShopperPremiumCents":0,"fastDriverPremiumCents":0}`)
	if !reflect.DeepEqual(omitted, zeros) || omitted.DriverMonthCents != 134 || omitted.ContributionMonthCents != 3066 {
		t.Fatal(omitted, zeros)
	}
	priced := decode(strings.TrimSuffix(base, "}") + `,"merchandiseSubtotalCents":5000,"additionalPickupsPerCheckout":2` + `}`)
	if priced.AdditionalPickupPlatformFeeBasisPoints != 25 || priced.AdditionalPickupPlatformFeePerCheckoutCents != 25 || priced.AdditionalPickupPlatformFeeMonthCents != 100 || priced.CustomerFeesMonthCents != 3300 || priced.ContributionMonthCents != 3166 || priced.DriverMonthCents != omitted.DriverMonthCents {
		t.Fatal(priced)
	}
	extended := decode(strings.TrimSuffix(base, "}") + `,"originalPickupCents":501,"additionalPickupsPerBundle":2,"pickupRadiusMilliMiles":250,"shopperCentsPerCheckout":50,"fastServiceAdoptionBasisPoints":3333,"fastShoppingFeeCents":301,"fastDeliveryFeeCents":200,"fastShopperPremiumCents":101,"fastDriverPremiumCents":103}`)
	if extended.Kind != "estimate" || extended.AdditionalPickupCents != 251 || extended.PickupAddonBundleCents != 502 || extended.DriverBundleCents != 1103 || extended.PickupRadiusMilliMiles != 250 || extended.DriverMonthCents != 1611 || extended.ShopperMonthCents != 336 || extended.FastFeesMonthCents != 664 || extended.ContributionMonthCents != 1917 {
		t.Fatal(extended)
	}
	maintained := decode(strings.TrimSuffix(base, "}") + `,"serviceFeeCents":200,"maintenanceCents":100000000,"hostingCents":10,"overheadCents":20,"payrollCents":30,"assumedPaidMerchants":1,"subscriptionCents":100}`)
	if maintained.PlatformServiceFeesMonthCents != 800 || maintained.DeliveryFeesMonthCents != 3200 || maintained.CustomerFeesMonthCents != 4000 || maintained.PlatformRevenueForecastCents != 4100 || maintained.MaintenanceMonthCents != finance.MaxMoney || maintained.FixedMonthCents != finance.MaxMoney+60 || maintained.ContributionMonthCents != 3866 || maintained.NetOperatingCents != 3966-finance.MaxMoney-60 || maintained.BreakEvenCheckouts == nil || *maintained.BreakEvenCheckouts != 103449 {
		t.Fatal(maintained)
	}
	for _, field := range []string{"merchandiseSubtotalCents", "additionalPickupsPerCheckout", "maintenanceCents", "originalPickupCents", "shopperCentsPerCheckout", "fastShoppingFeeCents", "fastDeliveryFeeCents", "fastShopperPremiumCents", "fastDriverPremiumCents", "additionalPickupsPerBundle", "pickupRadiusMilliMiles", "fastServiceAdoptionBasisPoints"} {
		for _, value := range []string{"-1", "100000001", "null", "1.2", `"1"`} {
			t.Run(field+value, func(t *testing.T) {
				w := financeRequest(h, "POST", "/admin/finance/scenarios", `{"dropsPerBundle":1,"`+field+`":`+value+`}`, "admin", "admin", "", true)
				if w.Code != 400 {
					t.Fatalf("%d %s", w.Code, w.Body)
				}
			})
		}
	}
	for _, body := range []string{`{"dropsPerBundle":1,"additionalPickupsPerCheckout":101}`, `{"dropsPerBundle":1,"additionalPickupsPerBundle":101}`, `{"dropsPerBundle":1,"pickupRadiusMilliMiles":501}`, `{"dropsPerBundle":1,"fastServiceAdoptionBasisPoints":10001}`} {
		if w := financeRequest(h, "POST", "/admin/finance/scenarios", body, "admin", "admin", "", true); w.Code != 400 {
			t.Fatal(w)
		}
	}
	if f.calls != 0 {
		t.Fatal("planning must not touch repository", f.calls)
	}
}

func TestFinanceCostIdempotencyAndConflicts(t *testing.T) {
	f := &fakeFinanceRepository{}
	h := New(f, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil)))
	first := financeRequest(h, "POST", "/admin/finance/costs", costBody, "admin", "admin", financeKey, true)
	second := financeRequest(h, "POST", "/admin/finance/costs", costBody, "admin", "admin", financeKey, true)
	if first.Code != 201 || second.Code != 201 || first.Body.String() != second.Body.String() || len(f.records) != 1 {
		t.Fatal(first, second)
	}
	for _, tc := range []struct{ body, actor string }{{strings.Replace(costBody, "501", "502", 1), "admin"}, {costBody, "other-admin"}, {strings.Replace(costBody, "driver", "staff", 1), "admin"}, {strings.Replace(costBody, "2026-11", "2026-12", 1), "admin"}} {
		w := financeRequest(h, "POST", "/admin/finance/costs", tc.body, tc.actor, "admin", financeKey, true)
		if w.Code != 409 {
			t.Fatal(w)
		}
	}
	if len(f.records) != 1 {
		t.Fatal("inflated manual costs")
	}
}
func TestFinanceCostValidation(t *testing.T) {
	f := &fakeFinanceRepository{}
	h := New(f, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, body := range []string{`{}`, `null`, `{"period":"2026-11","category":"driver"}`, strings.Replace(costBody, "501", "null", 1), strings.Replace(costBody, "501", "-1", 1), strings.Replace(costBody, "501", "100000001", 1), strings.Replace(costBody, "driver", "wage_approved", 1), strings.Replace(costBody, "2026-11", "2026-13", 1), strings.Replace(costBody, "2026-11", "1999-12", 1), costBody + ` {}`, `{"period":"2026-11","category":"staff","amountCents":1,"paid":true}`} {
		w := financeRequest(h, "POST", "/admin/finance/costs", body, "admin", "admin", financeKey, true)
		if w.Code != 400 {
			t.Fatal(w)
		}
	}
	w := financeRequest(h, "POST", "/admin/finance/costs", costBody, "admin", "admin", "not-uuid", true)
	if w.Code != 400 || f.calls != 0 {
		t.Fatal(w)
	}
	zero := strings.Replace(costBody, "501", "0", 1)
	if w := financeRequest(h, "POST", "/admin/finance/costs", zero, "admin", "admin", financeKey, true); w.Code != 201 {
		t.Fatal(w)
	}
}
func TestFinanceReportPaginationAndUnavailable(t *testing.T) {
	f := &fakeFinanceRepository{}
	h := New(f, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, query := range []string{"", "period=2026-00", "period=2026-11&limit=0", "period=2026-11&limit=101", "period=2026-11&offset=-1", "period=2026-11&offset=100001", "period=2026-11&limit=abc", "period=2026-11&offset=abc"} {
		w := financeRequest(h, "GET", "/admin/finance/costs?"+query, "", "admin", "admin", "", true)
		if w.Code != 400 {
			t.Fatal(w)
		}
	}
	w := financeRequest(h, "GET", "/admin/finance/costs?period=2026-11&limit=100&offset=100000", "", "admin", "admin", "", true)
	var report FinanceCostReport
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil || w.Code != 200 || report.TotalCents != "501" || f.limit != 100 || f.offset != 100000 || f.period != "2026-11" {
		t.Fatal(w, err)
	}
	for _, repo := range []Repository{&fakeRepository{}, &fakeFinanceRepository{err: &pgconn.PgError{Code: "42P01"}}, &fakeFinanceRepository{err: &pgconn.PgError{Code: "42883"}}} {
		h := New(repo, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil)))
		for _, tc := range []struct{ method, path, body string }{{"GET", "/admin/finance/costs?period=2026-11", ""}, {"POST", "/admin/finance/costs", costBody}} {
			w := financeRequest(h, tc.method, tc.path, tc.body, "admin", "admin", financeKey, true)
			if w.Code != 503 {
				t.Fatal(w)
			}
		}
	}
}
