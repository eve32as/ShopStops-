// Package finance provides estimates only; it never approves prices or compensation.
package finance

import "fmt"

const MaxMoney int64 = 100_000_000 // cents ($1m), per input
const MaxQuantity int64 = 100_000  // all result cents remain exact in JavaScript integers

const AdditionalPickupPlatformFeeBasisPoints int64 = 25 // fixed 0.25% of merchandise per extra pickup

type Scenario struct {
	MerchandiseSubtotalCents       int64 `json:"merchandiseSubtotalCents"`
	AdditionalPickupsPerCheckout   int64 `json:"additionalPickupsPerCheckout"` // platform scope, NOT driver bundle count
	Checkouts                      int64 `json:"checkouts"`
	DeliveryFeeCents               int64 `json:"deliveryFeeCents"`
	ServiceFeeCents                int64 `json:"serviceFeeCents"`
	AssumedPaidMerchants           int64 `json:"assumedPaidMerchants"`
	SubscriptionCents              int64 `json:"subscriptionCents"`
	DriverBaseCents                int64 `json:"driverBaseCents"`
	OriginalPickupCents            int64 `json:"originalPickupCents"` // additive per bundle; base excludes pickup pay
	AdditionalPickupsPerBundle     int64 `json:"additionalPickupsPerBundle"`
	PickupRadiusMilliMiles         int64 `json:"pickupRadiusMilliMiles"`         // metadata from FIRST pickup, not eligibility
	ShopperCentsPerCheckout        int64 `json:"shopperCentsPerCheckout"`        // separate person, not per pickup
	FastServiceAdoptionBasisPoints int64 `json:"fastServiceAdoptionBasisPoints"` // forecast, not checkout selection
	FastShoppingFeeCents           int64 `json:"fastShoppingFeeCents"`
	FastDeliveryFeeCents           int64 `json:"fastDeliveryFeeCents"`
	FastShopperPremiumCents        int64 `json:"fastShopperPremiumCents"`
	FastDriverPremiumCents         int64 `json:"fastDriverPremiumCents"` // per opted-in checkout, not bundle
	BundleMilliMiles               int64 `json:"bundleMilliMiles"`       // thousandths of a mile per bundle
	MileageCentsPerMile            int64 `json:"mileageCentsPerMile"`
	BundleSeconds                  int64 `json:"bundleSeconds"`
	TimeCentsPerHour               int64 `json:"timeCentsPerHour"`
	DropsPerBundle                 int64 `json:"dropsPerBundle"`        // one checkout per drop, NOT merchant orders
	ProcessingExcessCents          int64 `json:"processingExcessCents"` // per checkout, after merchant allocation
	RefundLossCents                int64 `json:"refundLossCents"`       // per checkout, unrecovered platform loss only
	ManualVariableCents            int64 `json:"manualVariableCents"`   // per checkout, non-overlapping
	HostingCents                   int64 `json:"hostingCents"`          // monthly
	MaintenanceCents               int64 `json:"maintenanceCents"`      // monthly platform cost, exclude overhead/payroll duplicates
	OverheadCents                  int64 `json:"overheadCents"`
	PayrollCents                   int64 `json:"payrollCents"` // monthly overhead, not driver guarantee again
}

type Result struct {
	AdditionalPickupPlatformFeeBasisPoints      int64 `json:"additionalPickupPlatformFeeBasisPoints"`
	AdditionalPickupPlatformFeePerCheckoutCents int64 `json:"additionalPickupPlatformFeePerCheckoutCents"`
	AdditionalPickupPlatformFeeMonthCents       int64 `json:"additionalPickupPlatformFeeMonthCents"`

	Kind                               string `json:"kind"`
	CustomerFeesPerCheckoutCents       int64  `json:"customerFeesPerCheckoutCents"`
	CustomerFeesMonthCents             int64  `json:"customerFeesMonthCents"`
	PlatformServiceFeesMonthCents      int64  `json:"platformServiceFeesMonthCents"` // included in customer fees, not extra revenue
	DeliveryFeesMonthCents             int64  `json:"deliveryFeesMonthCents"`        // included in customer fees, not worker pay
	SubscriptionForecastCents          int64  `json:"subscriptionForecastCents"`
	DriverBundleCents                  int64  `json:"driverBundleCents"`
	DriverPerCheckoutCents             int64  `json:"driverPerCheckoutCents"` // rounded display only
	DriverMonthCents                   int64  `json:"driverMonthCents"`
	AdditionalPickupCents              int64  `json:"additionalPickupCents"`  // each extra: half original, rounded up
	PickupAddonBundleCents             int64  `json:"pickupAddonBundleCents"` // extras only
	PickupBundleCents                  int64  `json:"pickupBundleCents"`      // original plus extras
	PickupRadiusMilliMiles             int64  `json:"pickupRadiusMilliMiles"` // planning metadata only
	ShopperPerCheckoutCents            int64  `json:"shopperPerCheckoutCents"`
	ShopperMonthCents                  int64  `json:"shopperMonthCents"`        // ordinary plus expected premium
	FastFeesPerCheckoutCents           int64  `json:"fastFeesPerCheckoutCents"` // weighted total fees, floored
	FastFeesMonthCents                 int64  `json:"fastFeesMonthCents"`
	FastShopperPremiumPerCheckoutCents int64  `json:"fastShopperPremiumPerCheckoutCents"` // weighted, ceiled
	FastShopperPremiumMonthCents       int64  `json:"fastShopperPremiumMonthCents"`
	FastDriverPremiumPerCheckoutCents  int64  `json:"fastDriverPremiumPerCheckoutCents"` // weighted, ceiled
	FastDriverPremiumMonthCents        int64  `json:"fastDriverPremiumMonthCents"`
	ContributionPerCheckoutCents       int64  `json:"contributionPerCheckoutCents"` // rounded display only
	ContributionNumeratorCents         int64  `json:"contributionNumeratorCents"`
	ContributionDenominator            int64  `json:"contributionDenominator"`
	ContributionMonthCents             int64  `json:"contributionMonthCents"`
	FixedMonthCents                    int64  `json:"fixedMonthCents"`
	MaintenanceMonthCents              int64  `json:"maintenanceMonthCents"` // included in fixed costs
	PlatformRevenueForecastCents       int64  `json:"platformRevenueForecastCents"`
	NetOperatingCents                  int64  `json:"netOperatingCents"`
	NetMarginBasisPoints               *int64 `json:"netMarginBasisPoints"` // null with zero platform revenue
	BreakEvenCheckouts                 *int64 `json:"breakEvenCheckouts"`   // null if exact contribution <= 0
}

func Calculate(s Scenario) (Result, error) {
	for _, input := range []struct {
		name  string
		value int64
	}{
		{"deliveryFeeCents", s.DeliveryFeeCents}, {"serviceFeeCents", s.ServiceFeeCents},
		{"subscriptionCents", s.SubscriptionCents}, {"driverBaseCents", s.DriverBaseCents},
		{"mileageCentsPerMile", s.MileageCentsPerMile}, {"timeCentsPerHour", s.TimeCentsPerHour},
		{"processingExcessCents", s.ProcessingExcessCents}, {"refundLossCents", s.RefundLossCents},
		{"manualVariableCents", s.ManualVariableCents}, {"hostingCents", s.HostingCents},
		{"maintenanceCents", s.MaintenanceCents},
		{"merchandiseSubtotalCents", s.MerchandiseSubtotalCents},
		{"overheadCents", s.OverheadCents}, {"payrollCents", s.PayrollCents},
		{"originalPickupCents", s.OriginalPickupCents}, {"shopperCentsPerCheckout", s.ShopperCentsPerCheckout},
		{"fastShoppingFeeCents", s.FastShoppingFeeCents}, {"fastDeliveryFeeCents", s.FastDeliveryFeeCents},
		{"fastShopperPremiumCents", s.FastShopperPremiumCents}, {"fastDriverPremiumCents", s.FastDriverPremiumCents},
	} {
		if input.value < 0 || input.value > MaxMoney {
			return Result{}, fmt.Errorf("%s must be 0..%d cents", input.name, MaxMoney)
		}
	}
	for _, v := range []int64{s.Checkouts, s.AssumedPaidMerchants, s.BundleMilliMiles, s.BundleSeconds} {
		if v < 0 || v > MaxQuantity {
			return Result{}, fmt.Errorf("counts, milli-miles and seconds must be 0..%d", MaxQuantity)
		}
	}
	if s.DropsPerBundle < 1 || s.DropsPerBundle > 100 {
		return Result{}, fmt.Errorf("dropsPerBundle must be 1..100")
	}
	for _, input := range []struct {
		name           string
		value, maximum int64
	}{
		{"additionalPickupsPerBundle", s.AdditionalPickupsPerBundle, 100},
		{"additionalPickupsPerCheckout", s.AdditionalPickupsPerCheckout, 100},
		{"pickupRadiusMilliMiles", s.PickupRadiusMilliMiles, 500},
		{"fastServiceAdoptionBasisPoints", s.FastServiceAdoptionBasisPoints, 10000},
	} {
		if input.value < 0 || input.value > input.maximum {
			return Result{}, fmt.Errorf("%s must be 0..%d", input.name, input.maximum)
		}
	}
	// Round expected fast amounts per checkout first: conservative, deterministic,
	// and preserves the exact bundle rational and minimum break-even formula.
	fastFees := (s.FastShoppingFeeCents + s.FastDeliveryFeeCents) * s.FastServiceAdoptionBasisPoints / 10000
	fastShopper := ceil(s.FastShopperPremiumCents*s.FastServiceAdoptionBasisPoints, 10000)
	fastDriver := ceil(s.FastDriverPremiumCents*s.FastServiceAdoptionBasisPoints, 10000)
	shopper := s.ShopperCentsPerCheckout + fastShopper
	extraPickup := round(s.OriginalPickupCents, 2)
	pickupAddon := s.AdditionalPickupsPerBundle * extraPickup
	pickup := s.OriginalPickupCents + pickupAddon
	// Half up ONCE on the combined surcharge, not once per pickup.
	platformPickupFee := round(s.MerchandiseSubtotalCents*AdditionalPickupPlatformFeeBasisPoints*s.AdditionalPickupsPerCheckout, 10000)
	fees := s.DeliveryFeeCents + s.ServiceFeeCents + fastFees + platformPickupFee
	bundle := s.DriverBaseCents + pickup + round(s.BundleMilliMiles*s.MileageCentsPerMile, 1000) + round(s.BundleSeconds*s.TimeCentsPerHour, 3600)
	variable := s.ProcessingExcessCents + s.RefundLossCents + s.ManualVariableCents + shopper + fastDriver
	numerator := (fees-variable)*s.DropsPerBundle - bundle
	regularDriverMonth := ceil(bundle*s.Checkouts, s.DropsPerBundle)
	r := Result{Kind: "estimate", CustomerFeesPerCheckoutCents: fees,
		AdditionalPickupPlatformFeeBasisPoints:      AdditionalPickupPlatformFeeBasisPoints,
		AdditionalPickupPlatformFeePerCheckoutCents: platformPickupFee,
		AdditionalPickupPlatformFeeMonthCents:       platformPickupFee * s.Checkouts,

		CustomerFeesMonthCents: fees * s.Checkouts, SubscriptionForecastCents: s.SubscriptionCents * s.AssumedPaidMerchants,
		PlatformServiceFeesMonthCents: s.ServiceFeeCents * s.Checkouts, DeliveryFeesMonthCents: s.DeliveryFeeCents * s.Checkouts,
		DriverBundleCents: bundle, DriverPerCheckoutCents: round(bundle, s.DropsPerBundle) + fastDriver, DriverMonthCents: regularDriverMonth + fastDriver*s.Checkouts,
		AdditionalPickupCents: extraPickup, PickupAddonBundleCents: pickupAddon, PickupBundleCents: pickup, PickupRadiusMilliMiles: s.PickupRadiusMilliMiles,
		ShopperPerCheckoutCents: shopper, ShopperMonthCents: shopper * s.Checkouts,
		FastFeesPerCheckoutCents: fastFees, FastFeesMonthCents: fastFees * s.Checkouts,
		FastShopperPremiumPerCheckoutCents: fastShopper, FastShopperPremiumMonthCents: fastShopper * s.Checkouts,
		FastDriverPremiumPerCheckoutCents: fastDriver, FastDriverPremiumMonthCents: fastDriver * s.Checkouts,
		ContributionPerCheckoutCents: round(numerator, s.DropsPerBundle), ContributionNumeratorCents: numerator,
		ContributionDenominator: s.DropsPerBundle, ContributionMonthCents: (fees-variable)*s.Checkouts - regularDriverMonth,
		FixedMonthCents: s.HostingCents + s.MaintenanceCents + s.OverheadCents + s.PayrollCents, MaintenanceMonthCents: s.MaintenanceCents}
	r.PlatformRevenueForecastCents = r.CustomerFeesMonthCents + r.SubscriptionForecastCents
	r.NetOperatingCents = r.ContributionMonthCents + r.SubscriptionForecastCents - r.FixedMonthCents
	if numerator > 0 {
		required := r.FixedMonthCents - r.SubscriptionForecastCents
		if required < 0 {
			required = 0
		}
		v := ceil(required*s.DropsPerBundle, numerator)
		r.BreakEvenCheckouts = &v
	}
	if r.PlatformRevenueForecastCents > 0 {
		// Avoid multiplying large monthly cents by 10,000: quotient/remainder
		// decomposition keeps all bounded-input intermediate results in int64.
		q, rem := r.NetOperatingCents/r.PlatformRevenueForecastCents, r.NetOperatingCents%r.PlatformRevenueForecastCents
		v := q*10000 + round(rem*10000, r.PlatformRevenueForecastCents)
		r.NetMarginBasisPoints = &v
	}
	return r, nil
}

// round is nearest integer, ties away from zero. ceil rounds nonnegative costs up.
func round(n, d int64) int64 {
	if n < 0 {
		return -round(-n, d)
	}
	return n/d + (n%d+d/2)/d
}
func ceil(n, d int64) int64 { return n/d + boolInt(n%d != 0) }
func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}
