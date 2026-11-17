package finance

import (
	"reflect"
	"testing"
)

func TestExtraPickupPlatformRuleAndScopes(t *testing.T) {
	for _, tc := range []struct{ subtotal, extras, fee int64 }{
		{5000, 1, 13}, {10000, 2, 50}, {5000, 2, 25}, // once, NOT 13*2
		{1, 1, 0}, {200, 1, 1}, {10000, 0, 0}, {0, 100, 0}, {MaxMoney, 100, 25000000},
	} {
		s := Scenario{Checkouts: 4, MerchandiseSubtotalCents: tc.subtotal, AdditionalPickupsPerCheckout: tc.extras,
			DeliveryFeeCents: 800, ServiceFeeCents: 200, DriverBaseCents: 100, OriginalPickupCents: 501,
			AdditionalPickupsPerBundle: 2, DropsPerBundle: 3, HostingCents: 9000}
		r, err := Calculate(s)
		if err != nil {
			t.Fatal(err)
		}
		s.AdditionalPickupsPerCheckout = 0
		base, _ := Calculate(s)
		if r.AdditionalPickupPlatformFeeBasisPoints != 25 || r.AdditionalPickupPlatformFeePerCheckoutCents != tc.fee || r.AdditionalPickupPlatformFeeMonthCents != tc.fee*4 ||
			r.CustomerFeesPerCheckoutCents != base.CustomerFeesPerCheckoutCents+tc.fee || r.CustomerFeesMonthCents != base.CustomerFeesMonthCents+tc.fee*4 ||
			r.PlatformRevenueForecastCents != base.PlatformRevenueForecastCents+tc.fee*4 || r.ContributionMonthCents != base.ContributionMonthCents+tc.fee*4 ||
			r.NetOperatingCents != base.NetOperatingCents+tc.fee*4 || r.DriverMonthCents != base.DriverMonthCents || r.AdditionalPickupCents != 251 {
			t.Fatalf("%+v: %+v vs %+v", tc, r, base)
		}
		for _, drops := range []int64{1, 10, 100} {
			s.DropsPerBundle = drops
			s.AdditionalPickupsPerCheckout = tc.extras
			s.AdditionalPickupsPerBundle = 100
			other, _ := Calculate(s)
			if other.AdditionalPickupPlatformFeePerCheckoutCents != tc.fee {
				t.Fatal("bundle count leaked into pricing")
			}
		}
	}
	s := Scenario{DropsPerBundle: 1}
	zero, _ := Calculate(s)
	s.MerchandiseSubtotalCents = 0
	s.AdditionalPickupsPerCheckout = 0
	explicit, _ := Calculate(s)
	if !reflect.DeepEqual(zero, explicit) {
		t.Fatal("omission changed zero inputs")
	}
	for _, bad := range []Scenario{{DropsPerBundle: 1, MerchandiseSubtotalCents: -1}, {DropsPerBundle: 1, MerchandiseSubtotalCents: MaxMoney + 1}, {DropsPerBundle: 1, AdditionalPickupsPerCheckout: -1}, {DropsPerBundle: 1, AdditionalPickupsPerCheckout: 101}} {
		if _, err := Calculate(bad); err == nil {
			t.Fatal("unbounded input", bad)
		}
	}
}

func TestPlatformPickupMinimumBreakEven(t *testing.T) {
	for drops := int64(1); drops <= 100; drops++ {
		s := Scenario{DropsPerBundle: drops, MerchandiseSubtotalCents: 5000, AdditionalPickupsPerCheckout: 1,
			DriverBaseCents: 11, HostingCents: 99, AssumedPaidMerchants: 1, SubscriptionCents: 7}
		r, _ := Calculate(s)
		if r.BreakEvenCheckouts == nil {
			t.Fatal(r)
		}
		s.Checkouts = *r.BreakEvenCheckouts
		at, _ := Calculate(s)
		s.Checkouts--
		before, _ := Calculate(s)
		if at.NetOperatingCents < 0 || before.NetOperatingCents >= 0 {
			t.Fatal(drops, at, before)
		}
	}
}

func TestPlatformPickupMaximumIntegerSafety(t *testing.T) {
	s := Scenario{Checkouts: MaxQuantity, DropsPerBundle: 1, MerchandiseSubtotalCents: MaxMoney, AdditionalPickupsPerCheckout: 100,
		AssumedPaidMerchants: MaxQuantity, SubscriptionCents: MaxMoney, DeliveryFeeCents: MaxMoney, ServiceFeeCents: MaxMoney,
		DriverBaseCents: MaxMoney, OriginalPickupCents: MaxMoney, AdditionalPickupsPerBundle: 100,
		BundleMilliMiles: MaxQuantity, MileageCentsPerMile: MaxMoney, BundleSeconds: MaxQuantity, TimeCentsPerHour: MaxMoney,
		ShopperCentsPerCheckout: MaxMoney, FastServiceAdoptionBasisPoints: 10000, FastShoppingFeeCents: MaxMoney, FastDeliveryFeeCents: MaxMoney,
		FastShopperPremiumCents: MaxMoney, FastDriverPremiumCents: MaxMoney, ProcessingExcessCents: MaxMoney, RefundLossCents: MaxMoney,
		ManualVariableCents: MaxMoney, HostingCents: MaxMoney, MaintenanceCents: MaxMoney, OverheadCents: MaxMoney, PayrollCents: MaxMoney}
	r, err := Calculate(s)
	if err != nil || r.AdditionalPickupPlatformFeeMonthCents != 2500000000000 {
		t.Fatal(r, err)
	}
	v := reflect.ValueOf(r)
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).Kind() == reflect.Int64 {
			n := v.Field(i).Int()
			if n < -9007199254740991 || n > 9007199254740991 {
				t.Fatal("unsafe JavaScript integer", v.Type().Field(i).Name, n)
			}
		}
	}
}
