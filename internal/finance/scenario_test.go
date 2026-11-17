package finance

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestFractionalContributionBreakEvenIsExact(t *testing.T) {
	s := Scenario{DeliveryFeeCents: 1, DriverBaseCents: 2, DropsPerBundle: 3, HostingCents: 1}
	r, err := Calculate(s)
	if err != nil || r.ContributionPerCheckoutCents != 0 || r.BreakEvenCheckouts == nil || *r.BreakEvenCheckouts != 3 {
		t.Fatalf("%+v %v", r, err)
	}
	s.Checkouts = 3
	r, _ = Calculate(s)
	if r.NetOperatingCents != 0 {
		t.Fatal(r)
	}
	s.Checkouts = 2
	r, _ = Calculate(s)
	if r.NetOperatingCents >= 0 {
		t.Fatal(r)
	}
}

func TestBreakEvenCeilingCoversRoundedMonthlyCosts(t *testing.T) {
	for drops := int64(1); drops <= 100; drops++ {
		for base := int64(0); base <= 200; base += 7 {
			s := Scenario{DeliveryFeeCents: 10, DriverBaseCents: base, DropsPerBundle: drops, HostingCents: 97, SubscriptionCents: 31, AssumedPaidMerchants: 1}
			r, _ := Calculate(s)
			if r.BreakEvenCheckouts == nil {
				continue
			}
			s.Checkouts = *r.BreakEvenCheckouts
			at, _ := Calculate(s)
			if at.NetOperatingCents < 0 {
				t.Fatalf("break-even fails: %+v %+v", s, at)
			}
			if s.Checkouts > 0 {
				s.Checkouts--
				before, _ := Calculate(s)
				if before.NetOperatingCents >= 0 {
					t.Fatalf("not minimum break-even: %+v", s)
				}
			}
		}
	}
}

func example() Scenario {
	return Scenario{Checkouts: 1000, DeliveryFeeCents: 600, ServiceFeeCents: 200, DriverBaseCents: 500, DropsPerBundle: 1, SubscriptionCents: 19900, HostingCents: 60000}
}

func TestIllustrativeScenario(t *testing.T) {
	s := example()
	r, err := Calculate(s)
	if err != nil || r.ContributionPerCheckoutCents != 300 || r.ContributionMonthCents != 300000 || r.NetOperatingCents != 240000 || *r.BreakEvenCheckouts != 200 || *r.NetMarginBasisPoints != 3000 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestMaintenanceFixedCostsAndFeeBreakdowns(t *testing.T) {
	for _, merchants := range []int64{0, 1, 4} {
		t.Run(fmt.Sprint(merchants), func(t *testing.T) {
			s := example()
			s.AssumedPaidMerchants = merchants
			s.OverheadCents, s.PayrollCents = 1000, 2000
			before, err := Calculate(s)
			if err != nil {
				t.Fatal(err)
			}
			// An omitted monthly maintenance input retains the zero-input result.
			body, _ := json.Marshal(s)
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(body, &fields); err != nil {
				t.Fatal(err)
			}
			delete(fields, "maintenanceCents")
			body, _ = json.Marshal(fields)
			var omitted Scenario
			if err := json.Unmarshal(body, &omitted); err != nil {
				t.Fatal(err)
			}
			compatible, err := Calculate(omitted)
			if err != nil || !reflect.DeepEqual(before, compatible) || compatible.MaintenanceMonthCents != 0 {
				t.Fatal(before, compatible, err)
			}
			s.MaintenanceCents = 30000 // test fixture, not an approved budget
			r, err := Calculate(s)
			wantFixed := int64(93000)
			wantBreakEven := ceil(max(int64(0), wantFixed-merchants*19900), 300)
			if err != nil || r.FixedMonthCents != wantFixed || r.MaintenanceMonthCents != 30000 || r.NetOperatingCents != before.NetOperatingCents-30000 || r.BreakEvenCheckouts == nil || *r.BreakEvenCheckouts != wantBreakEven {
				t.Fatalf("%+v %v", r, err)
			}
			if r.ContributionNumeratorCents != before.ContributionNumeratorCents || r.ContributionMonthCents != before.ContributionMonthCents || r.PlatformRevenueForecastCents != before.PlatformRevenueForecastCents || r.CustomerFeesMonthCents != 800000 || r.PlatformServiceFeesMonthCents != 200000 || r.DeliveryFeesMonthCents != 600000 {
				t.Fatal("maintenance must not affect fees, income or contribution", r, before)
			}
		})
	}
}

func TestFeeBreakdownsAreIncludedOnce(t *testing.T) {
	for _, checkouts := range []int64{0, 4, MaxQuantity} {
		s := example()
		s.Checkouts = checkouts
		s.FastServiceAdoptionBasisPoints, s.FastShoppingFeeCents, s.FastDeliveryFeeCents = 10000, 301, 200
		s.AssumedPaidMerchants = 1
		r, err := Calculate(s)
		if err != nil || r.PlatformServiceFeesMonthCents != 200*checkouts || r.DeliveryFeesMonthCents != 600*checkouts || r.CustomerFeesMonthCents != r.PlatformServiceFeesMonthCents+r.DeliveryFeesMonthCents+r.FastFeesMonthCents || r.PlatformRevenueForecastCents != r.CustomerFeesMonthCents+r.SubscriptionForecastCents || r.ContributionMonthCents != r.CustomerFeesMonthCents-r.DriverMonthCents {
			t.Fatalf("breakdowns must not be added to totals twice: %+v %v", r, err)
		}
	}
}

func TestMaintenanceBounds(t *testing.T) {
	for _, amount := range []int64{-1, MaxMoney + 1} {
		s := example()
		s.MaintenanceCents = amount
		if _, err := Calculate(s); err == nil || !strings.Contains(err.Error(), "maintenanceCents") {
			t.Fatal("invalid maintenance accepted", amount, err)
		}
	}
	for _, amount := range []int64{0, MaxMoney} {
		s := example()
		s.MaintenanceCents = amount
		r, err := Calculate(s)
		if err != nil || r.MaintenanceMonthCents != amount || r.FixedMonthCents != 60000+amount || r.NetOperatingCents != 240000-amount {
			t.Fatal(r, err)
		}
	}
}

func TestSubscriptionFixedEffects(t *testing.T) {
	for _, merchants := range []int64{0, 1, 4} {
		t.Run(string(rune('0'+merchants)), func(t *testing.T) {
			s := example()
			s.AssumedPaidMerchants = merchants
			s.Checkouts = 0
			r, _ := Calculate(s)
			want := ceil(max(int64(0), 60000-merchants*19900), 300)
			if *r.BreakEvenCheckouts != want || r.CustomerFeesMonthCents != 0 || r.SubscriptionForecastCents != merchants*19900 {
				t.Fatal(r)
			}
		})
	}
}

func TestNoBreakEvenWithDeliveryLosses(t *testing.T) {
	for _, driver := range []int64{800, 900} {
		s := example()
		s.DriverBaseCents = driver
		s.AssumedPaidMerchants = 100
		r, _ := Calculate(s)
		if r.BreakEvenCheckouts != nil {
			t.Fatal("subscriptions must not hide nonpositive delivery contribution")
		}
	}
}

func TestBundleAmortizationAndRounding(t *testing.T) {
	s := example()
	s.DriverBaseCents = 100
	s.DropsPerBundle = 3
	s.Checkouts = 4
	s.BundleMilliMiles = 500
	s.MileageCentsPerMile = 1
	s.BundleSeconds = 1800
	s.TimeCentsPerHour = 1
	r, _ := Calculate(s)
	if r.DriverBundleCents != 102 || r.DriverMonthCents != 136 || r.CustomerFeesMonthCents != 3200 {
		t.Fatal(r)
	}
	s.DriverBaseCents = 101
	s.BundleMilliMiles = 0
	s.BundleSeconds = 0
	r, _ = Calculate(s)
	if r.DriverMonthCents != 135 || r.ContributionNumeratorCents != 2299 || r.ContributionDenominator != 3 {
		t.Fatal(r)
	}
	if round(-5, 2) != -3 || round(1, 3) != 0 {
		t.Fatal("signed nearest rounding")
	}
}

func TestExplicitCostsAndUndefinedMargin(t *testing.T) {
	s := example()
	s.ProcessingExcessCents = 10
	s.RefundLossCents = 20
	s.ManualVariableCents = 30
	s.PayrollCents = 10000
	s.OverheadCents = 20000
	r, _ := Calculate(s)
	if r.ContributionMonthCents != 240000 || r.FixedMonthCents != 90000 || r.NetOperatingCents != 150000 || *r.BreakEvenCheckouts != 375 {
		t.Fatal(r)
	}
	s.Checkouts = 0
	r, _ = Calculate(s)
	if r.NetMarginBasisPoints != nil {
		t.Fatal("zero revenue margin is undefined")
	}
}

func TestBoundsAndMaximums(t *testing.T) {
	for _, mutate := range []func(*Scenario){func(s *Scenario) { s.DriverBaseCents = -1 }, func(s *Scenario) { s.HostingCents = MaxMoney + 1 }, func(s *Scenario) { s.Checkouts = MaxQuantity + 1 }, func(s *Scenario) { s.DropsPerBundle = 0 }, func(s *Scenario) { s.DropsPerBundle = 101 }} {
		s := example()
		mutate(&s)
		if _, err := Calculate(s); err == nil {
			t.Fatal("invalid accepted")
		}
	}
	s := Scenario{Checkouts: MaxQuantity, AssumedPaidMerchants: MaxQuantity, DeliveryFeeCents: MaxMoney, ServiceFeeCents: MaxMoney, SubscriptionCents: MaxMoney, DriverBaseCents: MaxMoney, BundleMilliMiles: MaxQuantity, MileageCentsPerMile: MaxMoney, BundleSeconds: MaxQuantity, TimeCentsPerHour: MaxMoney, DropsPerBundle: 100, ProcessingExcessCents: MaxMoney, RefundLossCents: MaxMoney, ManualVariableCents: MaxMoney, HostingCents: MaxMoney, OverheadCents: MaxMoney, PayrollCents: MaxMoney}
	r, err := Calculate(s)
	if err != nil || r.DriverMonthCents <= 0 || r.NetOperatingCents >= 0 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestPickupPayIsSeparateAndRadiusIsMetadata(t *testing.T) {
	for _, count := range []int64{0, 1, 2, 100} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			s := Scenario{Checkouts: 4, DeliveryFeeCents: 800, DriverBaseCents: 100, OriginalPickupCents: 501, AdditionalPickupsPerBundle: count, DropsPerBundle: 3}
			r, err := Calculate(s)
			pickup := 501 + count*251
			if err != nil || r.AdditionalPickupCents != 251 || r.PickupAddonBundleCents != count*251 || r.PickupBundleCents != pickup || r.DriverBundleCents != 100+pickup || r.DriverMonthCents != ceil((100+pickup)*4, 3) || r.CustomerFeesMonthCents != 3200 || r.ContributionDenominator != 3 {
				t.Fatalf("%+v %v", r, err)
			}
			s.PickupRadiusMilliMiles = 500
			withRadius, _ := Calculate(s)
			if withRadius.PickupRadiusMilliMiles != 500 {
				t.Fatal(withRadius)
			}
			withRadius.PickupRadiusMilliMiles = 0
			if !reflect.DeepEqual(withRadius, r) {
				t.Fatal("radius changed arithmetic", withRadius, r)
			}
			s.OriginalPickupCents = 0
			zero, _ := Calculate(s)
			if zero.PickupBundleCents != 0 || zero.DriverBundleCents != 100 {
				t.Fatal(zero)
			}
		})
	}
}

func TestFastAndSeparateShopperForecasts(t *testing.T) {
	for _, adoption := range []int64{0, 3333, 10000} {
		t.Run(fmt.Sprint(adoption), func(t *testing.T) {
			s := Scenario{Checkouts: 4, DeliveryFeeCents: 600, ServiceFeeCents: 200, DriverBaseCents: 100, OriginalPickupCents: 501, AdditionalPickupsPerBundle: 2,
				BundleMilliMiles: 500, MileageCentsPerMile: 1, BundleSeconds: 1800, TimeCentsPerHour: 1, DropsPerBundle: 3,
				ShopperCentsPerCheckout: 50, FastServiceAdoptionBasisPoints: adoption, FastShoppingFeeCents: 301, FastDeliveryFeeCents: 200,
				FastShopperPremiumCents: 101, FastDriverPremiumCents: 103}
			r, err := Calculate(s)
			var fee, shopperPremium, driverPremium int64
			switch adoption {
			case 3333:
				fee, shopperPremium, driverPremium = 166, 34, 35
			case 10000:
				fee, shopperPremium, driverPremium = 501, 101, 103
			}
			if err != nil || r.DriverBundleCents != 1105 || r.DriverPerCheckoutCents != 368+driverPremium || r.DriverMonthCents != 1474+driverPremium*4 || r.ShopperMonthCents != (50+shopperPremium)*4 || r.FastFeesMonthCents != fee*4 || r.FastFeesPerCheckoutCents != fee || r.FastShopperPremiumMonthCents != shopperPremium*4 || r.FastDriverPremiumMonthCents != driverPremium*4 || r.CustomerFeesMonthCents != (800+fee)*4 {
				t.Fatalf("%+v %v", r, err)
			}
			if r.ContributionMonthCents != r.CustomerFeesMonthCents-r.DriverMonthCents-r.ShopperMonthCents || r.ContributionNumeratorCents != (800+fee-50-shopperPremium-driverPremium)*3-1105 {
				t.Fatal(r)
			}
			if fee*10000 > 501*adoption || shopperPremium*10000 < 101*adoption || driverPremium*10000 < 103*adoption {
				t.Fatal("not conservative", r)
			}
			s.Checkouts = 0
			zero, _ := Calculate(s)
			if zero.DriverMonthCents != 0 || zero.ShopperMonthCents != 0 || zero.FastFeesMonthCents != 0 || zero.FastDriverPremiumMonthCents != 0 || zero.FastShopperPremiumMonthCents != 0 {
				t.Fatal(zero)
			}
		})
	}
}

func TestExtendedMinimumBreakEvenMatchesNet(t *testing.T) {
	for _, adoption := range []int64{0, 1, 3333, 9999, 10000} {
		for drops := int64(1); drops <= 100; drops++ {
			for _, merchants := range []int64{0, 1, 4} {
				s := Scenario{DeliveryFeeCents: 10, DriverBaseCents: 2, OriginalPickupCents: 5, AdditionalPickupsPerBundle: 2, DropsPerBundle: drops,
					ShopperCentsPerCheckout: 1, ProcessingExcessCents: 1, RefundLossCents: 1, ManualVariableCents: 1,
					FastServiceAdoptionBasisPoints: adoption, FastShoppingFeeCents: 5, FastDeliveryFeeCents: 3, FastShopperPremiumCents: 1, FastDriverPremiumCents: 3,
					HostingCents: 97, MaintenanceCents: 17, SubscriptionCents: 31, AssumedPaidMerchants: merchants}
				r, err := Calculate(s)
				if err != nil {
					t.Fatal(err)
				}
				if r.ContributionNumeratorCents <= 0 {
					if r.BreakEvenCheckouts != nil {
						t.Fatal(r)
					}
					continue
				}
				if r.BreakEvenCheckouts == nil {
					t.Fatal(r)
				}
				s.Checkouts = *r.BreakEvenCheckouts
				at, err := Calculate(s)
				if err != nil || at.NetOperatingCents < 0 || at.ContributionMonthCents != at.CustomerFeesMonthCents-at.DriverMonthCents-at.ShopperMonthCents-3*s.Checkouts {
					t.Fatalf("%+v %+v %v", s, at, err)
				}
				if s.Checkouts > 0 {
					s.Checkouts--
					before, _ := Calculate(s)
					if before.NetOperatingCents >= 0 {
						t.Fatalf("not minimum: %+v %+v", s, before)
					}
				}
			}
		}
	}
}

func TestExtensionBoundsAndOverflowSafety(t *testing.T) {
	for _, mutate := range []func(*Scenario){
		func(s *Scenario) { s.AdditionalPickupsPerBundle = -1 }, func(s *Scenario) { s.AdditionalPickupsPerBundle = 101 },
		func(s *Scenario) { s.PickupRadiusMilliMiles = -1 }, func(s *Scenario) { s.PickupRadiusMilliMiles = 501 },
		func(s *Scenario) { s.FastServiceAdoptionBasisPoints = -1 }, func(s *Scenario) { s.FastServiceAdoptionBasisPoints = 10001 },
		func(s *Scenario) { s.OriginalPickupCents = -1 }, func(s *Scenario) { s.OriginalPickupCents = MaxMoney + 1 },
		func(s *Scenario) { s.ShopperCentsPerCheckout = -1 }, func(s *Scenario) { s.ShopperCentsPerCheckout = MaxMoney + 1 },
		func(s *Scenario) { s.FastShoppingFeeCents = -1 }, func(s *Scenario) { s.FastShoppingFeeCents = MaxMoney + 1 },
		func(s *Scenario) { s.FastDeliveryFeeCents = -1 }, func(s *Scenario) { s.FastDeliveryFeeCents = MaxMoney + 1 },
		func(s *Scenario) { s.FastShopperPremiumCents = -1 }, func(s *Scenario) { s.FastShopperPremiumCents = MaxMoney + 1 },
		func(s *Scenario) { s.FastDriverPremiumCents = -1 }, func(s *Scenario) { s.FastDriverPremiumCents = MaxMoney + 1 },
	} {
		s := example()
		mutate(&s)
		if _, err := Calculate(s); err == nil {
			t.Fatal("invalid extension accepted", s)
		}
	}
	s := Scenario{Checkouts: MaxQuantity, DropsPerBundle: 1, AssumedPaidMerchants: MaxQuantity, SubscriptionCents: MaxMoney,
		DeliveryFeeCents: MaxMoney, ServiceFeeCents: MaxMoney, DriverBaseCents: MaxMoney, OriginalPickupCents: MaxMoney, AdditionalPickupsPerBundle: 100, PickupRadiusMilliMiles: 500,
		BundleMilliMiles: MaxQuantity, MileageCentsPerMile: MaxMoney, BundleSeconds: MaxQuantity, TimeCentsPerHour: MaxMoney,
		ShopperCentsPerCheckout: MaxMoney, FastServiceAdoptionBasisPoints: 10000, FastShoppingFeeCents: MaxMoney, FastDeliveryFeeCents: MaxMoney, FastShopperPremiumCents: MaxMoney, FastDriverPremiumCents: MaxMoney,
		ProcessingExcessCents: MaxMoney, RefundLossCents: MaxMoney, ManualVariableCents: MaxMoney, HostingCents: MaxMoney, MaintenanceCents: MaxMoney, OverheadCents: MaxMoney, PayrollCents: MaxMoney}
	r, err := Calculate(s)
	if err != nil || r.DriverMonthCents != 1_807_777_777_800_000 || r.NetOperatingCents != -1_807_778_177_800_000 || r.FixedMonthCents != 4*MaxMoney || r.NetMarginBasisPoints == nil {
		t.Fatalf("%+v %v", r, err)
	}
	for _, cents := range []int64{r.DriverMonthCents, r.NetOperatingCents, r.ContributionNumeratorCents, r.ContributionMonthCents, r.PlatformServiceFeesMonthCents, r.DeliveryFeesMonthCents, r.MaintenanceMonthCents, r.FixedMonthCents, *r.NetMarginBasisPoints} {
		if cents < -9_007_199_254_740_991 || cents > 9_007_199_254_740_991 {
			t.Fatal("unsafe JS integer", cents)
		}
	}
}
